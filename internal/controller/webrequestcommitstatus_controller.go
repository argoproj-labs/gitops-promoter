/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sync"
	"text/template"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	acmetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/internal/utils/httpclient"
)

const (
	// WebRequestPhaseSuccess indicates the expression evaluated to true
	WebRequestPhaseSuccess = "success"
	// WebRequestPhasePending indicates waiting for success (HTTP error, expression false, etc.)
	WebRequestPhasePending = "pending"
	// WebRequestPhaseFailure indicates a permanent failure (expression compilation error)
	WebRequestPhaseFailure = "failure"
	// ReportOnActive is the value for reportOn when reporting on active SHA
	ReportOnActive = "active"
	// ReportOnProposed is the value for reportOn when reporting on proposed SHA
	ReportOnProposed = "proposed"
)

// ResponseData represents the data structure available to expressions during validation.
type ResponseData struct {
	Body       any                 `expr:"Body"`
	Headers    map[string][]string `expr:"Headers"`
	StatusCode int                 `expr:"StatusCode"`
}

// NamespaceMetadata contains labels and annotations from the namespace.
type NamespaceMetadata struct {
	Labels      map[string]string
	Annotations map[string]string
}

// TemplateData represents the data available to Go templates in all templatable fields.
// All fields are available in all templatable locations.
// For HTTPRequest fields (url, headers, body): Phase and LastSuccessfulSha are from the previous reconcile.
// For CommitStatus fields (description, url): Phase and LastSuccessfulSha reflect the current reconcile result.
type TemplateData struct {
	NamespaceMetadata   NamespaceMetadata
	Branch              string // The environment branch name
	ProposedHydratedSha string // Proposed SHA for this environment
	ActiveHydratedSha   string // Active SHA for this environment
	ReportedSha         string // The SHA being reported on (based on reportOn setting)
	LastSuccessfulSha   string // Last SHA that achieved success
	Phase               string // Current phase (success/pending/failure)
}

// PollingExpressionData represents the data available to trigger expressions (polling.expression).
// The trigger expression is evaluated BEFORE making HTTP requests to decide if the request should be made.
// This enables conditional request triggering, throttling, attempt limiting, and custom state tracking.
type PollingExpressionData struct {
	ExpressionData    map[string]any
	Branch            string
	Environment       promoterv1alpha1.EnvironmentStatus
	PromotionStrategy promoterv1alpha1.PromotionStrategy
}

// WebRequestCommitStatusReconciler reconciles a WebRequestCommitStatus object
type WebRequestCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager
	HTTPClient  *http.Client
	EnqueueCTP  CTPEnqueueFunc

	// expressionCache caches compiled expressions to avoid recompilation on every reconciliation
	expressionCache sync.Map
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=webrequestcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=webrequestcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=webrequestcommitstatuses/finalizers,verbs=update
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses,verbs=get;list;create;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies/status,verbs=get
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicies,verbs=get;list;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *WebRequestCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling WebRequestCommitStatus", "name", req.Name)
	startTime := time.Now()

	var wrcs promoterv1alpha1.WebRequestCommitStatus
	defer utils.HandleReconciliationResult(ctx, startTime, &wrcs, r.Client, r.Recorder, &err)

	err = r.Get(ctx, req.NamespacedName, &wrcs, &client.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("WebRequestCommitStatus not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get WebRequestCommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get WebRequestCommitStatus %q: %w", req.Name, err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	meta.RemoveStatusCondition(wrcs.GetConditions(), string(promoterConditions.Ready))

	// Fetch the referenced PromotionStrategy
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{
		Namespace: wrcs.Namespace,
		Name:      wrcs.Spec.PromotionStrategyRef.Name,
	}
	err = r.Get(ctx, psKey, &ps)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Error(err, "referenced PromotionStrategy not found", "promotionStrategy", wrcs.Spec.PromotionStrategyRef.Name)
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", wrcs.Spec.PromotionStrategyRef.Name, err)
		}
		logger.Error(err, "failed to get PromotionStrategy")
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", wrcs.Spec.PromotionStrategyRef.Name, err)
	}

	// Calculate status by processing all environments
	transitionedEnvironments, commitStatuses, needsPolling, err := r.calculateStatus(ctx, &wrcs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to calculate status: %w", err)
	}

	// Inherit conditions from CommitStatus objects
	utils.InheritNotReadyConditionFromObjects(&wrcs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	// Update status
	err = r.Status().Update(ctx, &wrcs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update WebRequestCommitStatus status: %w", err)
	}

	// If any validations transitioned to success, touch the corresponding ChangeTransferPolicies
	if len(transitionedEnvironments) > 0 {
		r.touchChangeTransferPolicies(ctx, &ps, transitionedEnvironments)
	}

	// Determine requeue strategy based on reportOn and polling needs
	return r.calculateRequeue(&wrcs, needsPolling), nil
}

// calculateRequeue determines when to requeue based on reportOn mode and polling needs
func (r *WebRequestCommitStatusReconciler) calculateRequeue(wrcs *promoterv1alpha1.WebRequestCommitStatus, needsPolling bool) ctrl.Result {
	// If reportOn: active, always requeue at Polling.Interval
	if wrcs.Spec.ReportOn == ReportOnActive {
		return ctrl.Result{RequeueAfter: wrcs.Spec.Polling.Interval.Duration}
	}

	// If reportOn: proposed, only requeue if any environment needs polling
	if needsPolling {
		return ctrl.Result{RequeueAfter: wrcs.Spec.Polling.Interval.Duration}
	}

	// All environments successful - no requeue, PromotionStrategy watch handles SHA changes
	return ctrl.Result{}
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebRequestCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Initialize HTTP client if not set
	if r.HTTPClient == nil {
		r.HTTPClient = &http.Client{
			Timeout: 30 * time.Second,
		}
	}

	// Use Direct methods to read configuration from the API server without cache during setup.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.WebRequestCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get WebRequestCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.WebRequestCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get WebRequestCommitStatus max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.WebRequestCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.PromotionStrategy{}, r.enqueueWebRequestCommitStatusForPromotionStrategy()).
		Named("webrequestcommitstatus").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
			RateLimiter:             rateLimiter,
		}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// enqueueWebRequestCommitStatusForPromotionStrategy returns a handler that enqueues all
// WebRequestCommitStatus resources that reference a PromotionStrategy when it changes
func (r *WebRequestCommitStatusReconciler) enqueueWebRequestCommitStatusForPromotionStrategy() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
		if !ok {
			return nil
		}

		// List all WebRequestCommitStatus resources in the same namespace
		var wrcsList promoterv1alpha1.WebRequestCommitStatusList
		if err := r.List(ctx, &wrcsList, client.InNamespace(ps.Namespace)); err != nil {
			log.FromContext(ctx).Error(err, "failed to list WebRequestCommitStatus resources")
			return nil
		}

		// Enqueue all WebRequestCommitStatus resources that reference this PromotionStrategy
		var requests []ctrl.Request
		for _, wrcs := range wrcsList.Items {
			if wrcs.Spec.PromotionStrategyRef.Name == ps.Name {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKeyFromObject(&wrcs),
				})
			}
		}

		return requests
	})
}

// appliesToEnvironment checks if the WebRequestCommitStatus applies to the given environment
func (r *WebRequestCommitStatusReconciler) appliesToEnvironment(wrcs *promoterv1alpha1.WebRequestCommitStatus, ps *promoterv1alpha1.PromotionStrategy, psEnv promoterv1alpha1.Environment) bool {
	// Helper to check if key exists in a list of commit status selectors
	keyInSelectors := func(selectors []promoterv1alpha1.CommitStatusSelector) bool {
		for _, selector := range selectors {
			if selector.Key == wrcs.Spec.Key {
				return true
			}
		}
		return false
	}

	// Check global commit statuses
	if keyInSelectors(ps.Spec.ProposedCommitStatuses) {
		return true
	}
	if keyInSelectors(ps.Spec.ActiveCommitStatuses) {
		return true
	}
	// Check environment-specific commit statuses
	if keyInSelectors(psEnv.ProposedCommitStatuses) {
		return true
	}
	if keyInSelectors(psEnv.ActiveCommitStatuses) {
		return true
	}
	return false
}

// findPreviousReconcileStatus looks up the environment status from the previous reconciliation.
// Returns nil if this is the first time processing this environment, which is valid and expected for:
// - First reconciliation of a WebRequestCommitStatus
// - New environment just added to the PromotionStrategy
// - Environment that was previously skipped (not applicable to this WebRequestCommitStatus)
// Callers must handle nil gracefully as a "no previous status" condition, not an error.
func findPreviousReconcileStatus(previousWebRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatusStatus, branch string) *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus {
	for i := range previousWebRequestCommitStatus.Environments {
		if previousWebRequestCommitStatus.Environments[i].Branch == branch {
			return &previousWebRequestCommitStatus.Environments[i]
		}
	}
	return nil
}

// shouldSkipRequest checks if we should skip the HTTP request for this environment
// calculateStatus processes each environment from the PromotionStrategy,
// making HTTP requests and evaluating expressions, then updates wrcs.Status.
// Returns transitioned environments, commit statuses, whether polling is needed, and any error.
func (r *WebRequestCommitStatusReconciler) calculateStatus(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, ps *promoterv1alpha1.PromotionStrategy) ([]string, []*promoterv1alpha1.CommitStatus, bool, error) {
	logger := log.FromContext(ctx)

	transitionedEnvironments := []string{}
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(ps.Spec.Environments))
	needsPolling := false

	// Save a snapshot of the current status to compare against when processing environments
	previousWebRequestCommitStatus := wrcs.Status.DeepCopy()
	if previousWebRequestCommitStatus == nil {
		previousWebRequestCommitStatus = &promoterv1alpha1.WebRequestCommitStatusStatus{}
	}

	// Build a map of environments from PromotionStrategy for efficient lookup
	promoterEnvStatusMap := make(map[string]*promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for i := range ps.Status.Environments {
		promoterEnvStatusMap[ps.Status.Environments[i].Branch] = &ps.Status.Environments[i]
	}

	// Clear and rebuild the environments status from scratch
	wrcs.Status.Environments = make([]promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus, 0, len(ps.Spec.Environments))

	// Iterate over ALL environments from the PromotionStrategy
	for _, psEnv := range ps.Spec.Environments {
		branch := psEnv.Branch

		// Skip this environment if the WebRequestCommitStatus doesn't apply
		if !r.appliesToEnvironment(wrcs, ps, psEnv) {
			logger.V(4).Info("WebRequestCommitStatus does not apply to environment, skipping",
				"branch", branch,
				"key", wrcs.Spec.Key)
			continue
		}

		// Look up the environment status in the map
		promoterEnvStatus, found := promoterEnvStatusMap[branch]
		if !found {
			logger.Info("Environment not found in PromotionStrategy status", "branch", branch)
			continue
		}

		// Process this environment
		result, err := r.processEnvironment(ctx, wrcs, ps, branch, promoterEnvStatus, previousWebRequestCommitStatus)
		if err != nil {
			return nil, nil, false, err
		}

		// Update status with the processed result
		wrcs.Status.Environments = append(wrcs.Status.Environments, result.envStatus)

		// Collect results
		if result.transitioned {
			transitionedEnvironments = append(transitionedEnvironments, branch)
		}
		if result.needsPolling {
			needsPolling = true
		}
		if result.commitStatus != nil {
			commitStatuses = append(commitStatuses, result.commitStatus)
		}
	}

	return transitionedEnvironments, commitStatuses, needsPolling, nil
}

// environmentProcessResult holds the result of processing a single environment
type environmentProcessResult struct {
	envStatus    promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus
	commitStatus *promoterv1alpha1.CommitStatus
	transitioned bool
	needsPolling bool
}

// determineLastSuccessfulSha determines what LastSuccessfulSha should be for an environment.
// This preserves the most recent successful SHA across reconciliations:
// - If the current phase is success, we update to the current reportedSha
// - If the current phase is not success (pending/failure), we preserve the last successful SHA from previous reconciliation
// - If there's no previous reconciliation, we return empty string (no success yet)
// This allows tracking the last known good state even when the current validation is failing.
func determineLastSuccessfulSha(
	currentPhase, reportedSha string,
	previousReconcileStatus *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus,
) string {
	if currentPhase == WebRequestPhaseSuccess {
		return reportedSha
	}
	if previousReconcileStatus != nil {
		return previousReconcileStatus.LastSuccessfulSha
	}
	return ""
}

// processEnvironment processes a single environment, making HTTP requests and evaluating conditions.
// Returns the environment status and metadata without mutating wrcs.Status.
func (r *WebRequestCommitStatusReconciler) processEnvironment(
	ctx context.Context,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	branch string,
	promoterEnvStatus *promoterv1alpha1.EnvironmentStatus,
	previousWebRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatusStatus,
) (*environmentProcessResult, error) {
	logger := log.FromContext(ctx)

	// Extract both SHAs from the environment status
	proposedSha := promoterEnvStatus.Proposed.Hydrated.Sha
	activeSha := promoterEnvStatus.Active.Hydrated.Sha

	// Get the report mode (Kubernetes defaults to "proposed" via CRD validation)
	reportOn := wrcs.Spec.ReportOn

	// Select the SHA to report on based on the mode
	reportedSha := proposedSha
	if reportOn == ReportOnActive {
		reportedSha = activeSha
	}

	// Return error if SHA not available yet (will requeue and show in conditions)
	if reportedSha == "" {
		return nil, fmt.Errorf("reported SHA not yet available for branch %q (reportOn=%s): waiting for hydration", branch, reportOn)
	}

	// Find status from last reconciliation for this same environment.
	// Will be nil on first reconciliation or for newly added environments (not an error).
	previousReconcileStatus := findPreviousReconcileStatus(previousWebRequestCommitStatus, branch)

	// Get values from previous reconcile (defaults if first run)
	previousPhase := WebRequestPhasePending // Default to pending on first run
	var lastSuccessfulSha string
	if previousReconcileStatus != nil {
		previousPhase = previousReconcileStatus.Phase
		lastSuccessfulSha = previousReconcileStatus.LastSuccessfulSha
	}

	// If a custom trigger expression (polling.expression) is configured, evaluate it FIRST to decide whether to make the HTTP request.
	// The expression has access to current PromotionStrategy, Environment, Branch, and custom ExpressionData for state tracking.
	// This allows expressions to implement custom logic like:
	// - "Only trigger request if environment state meets conditions"
	// - "Limit to N request attempts"
	// - "Throttle requests based on time"
	// - "Only trigger when SHA changes in another environment"
	if wrcs.Spec.Polling.Expression != "" {
		shaCtx := environmentSHAContext{
			reportedSha:       reportedSha,
			branch:            branch,
			proposedSha:       proposedSha,
			activeSha:         activeSha,
			previousPhase:     previousPhase,
			lastSuccessfulSha: lastSuccessfulSha,
		}
		return r.processEnvironmentWithPollingExpression(ctx, wrcs, ps, promoterEnvStatus, previousReconcileStatus, shaCtx)
	}

	// Legacy behavior: skip HTTP request if we can reuse previous successful status
	// We can skip (reuse) only when ALL of these conditions are met:
	// - No custom polling expression is configured (already checked above)
	// - reportOn is "proposed" (not "active", which requires continuous polling)
	// - previousReconcileStatus exists (we've processed this environment before)
	// - previousReconcileStatus.Phase is "success" (previous validation passed)
	// - previousReconcileStatus.ReportedSha matches current reportedSha (same SHA)
	// - previousReconcileStatus.LastSuccessfulSha matches reportedSha (successful for this SHA)
	canReuseStatus := previousReconcileStatus != nil && reportOn == ReportOnProposed &&
		previousReconcileStatus.Phase == WebRequestPhaseSuccess &&
		previousReconcileStatus.ReportedSha == reportedSha &&
		previousReconcileStatus.LastSuccessfulSha == reportedSha

	if canReuseStatus {
		result := &environmentProcessResult{
			envStatus: *previousReconcileStatus,
		}

		// Ensure CommitStatus exists even when reusing status
		cs, err := r.upsertCommitStatus(ctx, wrcs, ps, branch, reportedSha, WebRequestPhaseSuccess, wrcs.Spec.Key, &result.envStatus)
		if err != nil {
			return nil, fmt.Errorf("failed to upsert CommitStatus for environment %q: %w", branch, err)
		}
		result.commitStatus = cs

		return result, nil
	}

	// Make HTTP request and evaluate validation expression
	envResult, err := r.processEnvironmentRequest(ctx, wrcs, branch, proposedSha, activeSha, reportedSha, previousPhase, lastSuccessfulSha)
	if err != nil {
		return nil, fmt.Errorf("failed to process environment request for %q: %w", branch, err)
	}

	// Determine if this environment needs continued polling for requeue
	// Fall back to legacy reportOn behavior:
	// - Always poll if not yet successful (pending/failure)
	// - Always poll if reportOn="active" (continuous monitoring)
	// - Stop polling if reportOn="proposed" and successful (optimization)
	needsPolling := envResult.Phase != WebRequestPhaseSuccess || reportOn == ReportOnActive

	// Detect if environment just transitioned to success (was not success before, is success now)
	transitioned := previousPhase != WebRequestPhaseSuccess && envResult.Phase == WebRequestPhaseSuccess

	// Build result
	result := &environmentProcessResult{
		envStatus:    envResult,
		transitioned: transitioned,
		needsPolling: needsPolling,
	}
	result.envStatus.LastSuccessfulSha = determineLastSuccessfulSha(envResult.Phase, reportedSha, previousReconcileStatus)

	// Log transition if detected
	if transitioned {
		logger.Info("Validation transitioned to success", "branch", branch, "sha", reportedSha)
	}

	// Create or update CommitStatus with full environment status
	cs, err := r.upsertCommitStatus(ctx, wrcs, ps, branch, reportedSha, envResult.Phase, wrcs.Spec.Key, &result.envStatus)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert CommitStatus for environment %q: %w", branch, err)
	}
	result.commitStatus = cs

	logger.Info("Processed environment web request",
		"branch", branch,
		"reportedSha", reportedSha,
		"phase", envResult.Phase,
		"statusCode", envResult.Response.StatusCode)

	return result, nil
}

// environmentSHAContext bundles SHA and branch information for environment processing
type environmentSHAContext struct {
	reportedSha       string
	branch            string
	proposedSha       string
	activeSha         string
	previousPhase     string
	lastSuccessfulSha string
}

// processEnvironmentWithPollingExpression handles environment processing when a custom polling expression is configured.
func (r *WebRequestCommitStatusReconciler) processEnvironmentWithPollingExpression(
	ctx context.Context,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	promoterEnvStatus *promoterv1alpha1.EnvironmentStatus,
	previousReconcileStatus *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus,
	shaCtx environmentSHAContext,
) (*environmentProcessResult, error) {
	logger := log.FromContext(ctx)

	// On first reconcile (no previous status), always trigger request
	if previousReconcileStatus == nil {
		return r.makeRequestAndProcessResult(ctx, wrcs, ps, nil, previousReconcileStatus, shaCtx)
	}

	// Evaluate trigger expression with current state
	shouldTriggerRequest, expressionData, triggerErr := r.evaluatePollingExpressionPreRequest(ctx, wrcs, ps, promoterEnvStatus, previousReconcileStatus, shaCtx.branch)
	if triggerErr != nil {
		return nil, fmt.Errorf("failed to evaluate trigger expression for environment %q: %w", shaCtx.branch, triggerErr)
	}

	// If expression says not to trigger, reuse previous status
	if !shouldTriggerRequest {
		logger.V(1).Info("Trigger expression returned false, skipping HTTP request",
			"branch", shaCtx.branch,
			"reportedSha", shaCtx.reportedSha)

		result := &environmentProcessResult{
			envStatus:    *previousReconcileStatus,
			needsPolling: false, // Expression explicitly said not to trigger
		}

		// Update expression data if provided
		if expressionData != nil {
			jsonData, err := json.Marshal(expressionData)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal expression data for environment %q: %w", shaCtx.branch, err)
			}
			result.envStatus.ExpressionData = &apiextensionsv1.JSON{Raw: jsonData}
		}

		// Ensure CommitStatus exists even when skipping request
		cs, err := r.upsertCommitStatus(ctx, wrcs, ps, shaCtx.branch, shaCtx.reportedSha, previousReconcileStatus.Phase, wrcs.Spec.Key, &result.envStatus)
		if err != nil {
			return nil, fmt.Errorf("failed to upsert CommitStatus for environment %q: %w", shaCtx.branch, err)
		}
		result.commitStatus = cs

		return result, nil
	}

	// Make request and store expression data
	return r.makeRequestAndProcessResult(ctx, wrcs, ps, expressionData, previousReconcileStatus, shaCtx)
}

// makeRequestAndProcessResult makes the HTTP request, processes the result, and stores expression data if provided.
func (r *WebRequestCommitStatusReconciler) makeRequestAndProcessResult(
	ctx context.Context,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	expressionData map[string]any,
	previousReconcileStatus *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus,
	shaCtx environmentSHAContext,
) (*environmentProcessResult, error) {
	logger := log.FromContext(ctx)

	// Make HTTP request and evaluate validation expression
	envResult, err := r.processEnvironmentRequest(ctx, wrcs, shaCtx.branch, shaCtx.proposedSha, shaCtx.activeSha, shaCtx.reportedSha, shaCtx.previousPhase, shaCtx.lastSuccessfulSha)
	if err != nil {
		return nil, fmt.Errorf("failed to process environment request for %q: %w", shaCtx.branch, err)
	}

	// Store expression data from pre-request evaluation if we have it
	if expressionData != nil {
		jsonData, err := json.Marshal(expressionData)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal expression data for environment %q: %w", shaCtx.branch, err)
		}
		envResult.ExpressionData = &apiextensionsv1.JSON{Raw: jsonData}
	}

	// With custom trigger expression, always requeue
	// Trigger expression will be re-evaluated on next reconcile to decide if request should be made
	needsPolling := true

	// Detect if environment just transitioned to success
	transitioned := shaCtx.previousPhase != WebRequestPhaseSuccess && envResult.Phase == WebRequestPhaseSuccess

	// Build result
	result := &environmentProcessResult{
		envStatus:    envResult,
		transitioned: transitioned,
		needsPolling: needsPolling,
	}
	result.envStatus.LastSuccessfulSha = determineLastSuccessfulSha(envResult.Phase, shaCtx.reportedSha, previousReconcileStatus)

	// Log transition if detected
	if transitioned {
		logger.Info("Validation transitioned to success", "branch", shaCtx.branch, "sha", shaCtx.reportedSha)
	}

	// Create or update CommitStatus with full environment status
	cs, err := r.upsertCommitStatus(ctx, wrcs, ps, shaCtx.branch, shaCtx.reportedSha, envResult.Phase, wrcs.Spec.Key, &result.envStatus)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert CommitStatus for environment %q: %w", shaCtx.branch, err)
	}
	result.commitStatus = cs

	logger.Info("Processed environment web request",
		"branch", shaCtx.branch,
		"reportedSha", shaCtx.reportedSha,
		"phase", envResult.Phase,
		"statusCode", envResult.Response.StatusCode)

	return result, nil
}

// processEnvironmentRequest makes the HTTP request and evaluates the validation expression for a single environment.
// Returns the environment status.
func (r *WebRequestCommitStatusReconciler) processEnvironmentRequest(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, branch, proposedSha, activeSha, reportedSha, previousPhase, lastSuccessfulSha string) (promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus, error) {
	logger := log.FromContext(ctx)
	now := metav1.Now()

	result := promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus{
		Branch:              branch,
		ProposedHydratedSha: proposedSha,
		ActiveHydratedSha:   activeSha,
		ReportedSha:         reportedSha,
		LastRequestTime:     &now,
	}

	// Fetch the namespace to get its labels and annotations
	var ns corev1.Namespace
	err := r.Get(ctx, client.ObjectKey{Name: wrcs.Namespace}, &ns)
	if err != nil {
		return result, fmt.Errorf("failed to get namespace %q: %w", wrcs.Namespace, err)
	}

	// Prepare template data for request (URL, headers, body)
	// Phase is from the previous reconcile (empty on first run)
	templateData := TemplateData{
		NamespaceMetadata: NamespaceMetadata{
			Labels:      ns.Labels,
			Annotations: ns.Annotations,
		},
		Branch:              branch,
		ProposedHydratedSha: proposedSha,
		ActiveHydratedSha:   activeSha,
		ReportedSha:         reportedSha,
		LastSuccessfulSha:   lastSuccessfulSha,
		Phase:               previousPhase,
	}

	// Render URL template
	url, err := r.renderTemplate("url", wrcs.Spec.HTTPRequest.URLTemplate, templateData)
	if err != nil {
		return result, fmt.Errorf("failed to render URL template: %w", err)
	}

	// Render body template
	body, err := r.renderTemplate("body", wrcs.Spec.HTTPRequest.BodyTemplate, templateData)
	if err != nil {
		return result, fmt.Errorf("failed to render body template: %w", err)
	}

	// Create HTTP request
	var reqBody io.Reader
	if body != "" {
		reqBody = bytes.NewBufferString(body)
	}

	req, err := http.NewRequestWithContext(ctx, wrcs.Spec.HTTPRequest.Method, url, reqBody)
	if err != nil {
		return result, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Render and add headers
	for nameTemplate, valueTemplate := range wrcs.Spec.HTTPRequest.HeaderTemplates {
		renderedName, err := r.renderTemplate("header-name", nameTemplate, templateData)
		if err != nil {
			return result, fmt.Errorf("failed to render header name template %q: %w", nameTemplate, err)
		}
		renderedValue, err := r.renderTemplate("header-value", valueTemplate, templateData)
		if err != nil {
			return result, fmt.Errorf("failed to render header value template for %q: %w", renderedName, err)
		}
		req.Header.Set(renderedName, renderedValue)
	}

	// Use the timeout from spec (Kubernetes defaults to 30s via CRD validation)
	timeout := wrcs.Spec.HTTPRequest.Timeout.Duration

	// Create HTTP client with appropriate configuration
	httpClient, err := r.createHTTPClient(ctx, wrcs, timeout)
	if err != nil {
		return result, fmt.Errorf("failed to create HTTP client: %w", err)
	}

	// Apply authentication (except TLS which is handled in client creation)
	if wrcs.Spec.HTTPRequest.Authentication != nil {
		if err := httpclient.ApplyAuth(ctx, r.Client, req, wrcs.Spec.HTTPRequest.Authentication, wrcs.Namespace); err != nil {
			return result, fmt.Errorf("failed to apply authentication: %w", err)
		}
	}

	// Make the HTTP request
	resp, err := httpClient.Do(req)
	if err != nil {
		logger.Error(err, "HTTP request failed", "url", url)
		return result, fmt.Errorf("HTTP request failed: %w", err)
	}
	if resp == nil {
		return result, errors.New("HTTP request returned nil response")
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	result.Response.StatusCode = ptr.To(resp.StatusCode)

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return result, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse response body as JSON if possible
	var bodyData any
	if err := json.Unmarshal(respBody, &bodyData); err != nil {
		// If not JSON, use raw string
		bodyData = string(respBody)
	}

	// Build response data for validation expression
	responseData := ResponseData{
		StatusCode: resp.StatusCode,
		Body:       bodyData,
		Headers:    resp.Header,
	}

	// Evaluate validation expression
	phase, exprResult, err := r.evaluateExpression(ctx, wrcs.Spec.Expression, responseData)
	if err != nil {
		return result, fmt.Errorf("failed to evaluate expression: %w", err)
	}
	result.Phase = phase
	result.ExpressionResult = exprResult

	return result, nil
}

// renderTemplate renders a Go template with the given data
func (r *WebRequestCommitStatusReconciler) renderTemplate(name, tmplStr string, data any) (string, error) {
	if tmplStr == "" {
		return "", nil
	}

	tmpl, err := template.New(name).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// createHTTPClient creates an HTTP client with appropriate configuration including TLS.
func (r *WebRequestCommitStatusReconciler) createHTTPClient(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, timeout time.Duration) (*http.Client, error) {
	transport := &http.Transport{}

	// Configure TLS if TLS auth is specified
	if wrcs.Spec.HTTPRequest.Authentication != nil && wrcs.Spec.HTTPRequest.Authentication.TLS != nil {
		tlsConfig, err := httpclient.BuildTLSConfig(ctx, r.Client, wrcs.Namespace, wrcs.Spec.HTTPRequest.Authentication.TLS)
		if err != nil {
			return nil, fmt.Errorf("failed to build TLS config: %w", err)
		}
		transport.TLSClientConfig = tlsConfig
	}

	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}, nil
}

// evaluateExpression evaluates the expr expression against the response data
func (r *WebRequestCommitStatusReconciler) evaluateExpression(ctx context.Context, expression string, response ResponseData) (string, *bool, error) {
	logger := log.FromContext(ctx)

	// Try to get cached compiled expression
	var program *vm.Program
	if cached, ok := r.expressionCache.Load(expression); ok {
		program, ok = cached.(*vm.Program)
		if !ok {
			return WebRequestPhaseFailure, nil, errors.New("invalid cached expression program")
		}
	} else {
		// Compile the expression
		env := map[string]any{"Response": response}
		compiled, err := expr.Compile(expression, expr.Env(env), expr.AsBool())
		if err != nil {
			logger.Error(err, "Failed to compile expression", "expression", expression)
			return WebRequestPhaseFailure, nil, fmt.Errorf("expression compilation failed: %w", err)
		}
		program = compiled
		r.expressionCache.Store(expression, program)
	}

	// Run the expression
	env := map[string]any{"Response": response}
	output, err := expr.Run(program, env)
	if err != nil {
		logger.Error(err, "Failed to evaluate expression", "expression", expression)
		return WebRequestPhasePending, nil, fmt.Errorf("expression evaluation failed: %w", err)
	}

	// Check if output is boolean
	result, ok := output.(bool)
	if !ok {
		return WebRequestPhaseFailure, nil, fmt.Errorf("expression must return boolean, got %T", output)
	}

	if result {
		return WebRequestPhaseSuccess, ptr.To(true), nil
	}
	return WebRequestPhasePending, ptr.To(false), nil
}

// evaluatePollingExpressionPreRequest evaluates the trigger expression (polling.expression) BEFORE making an HTTP request.
// This allows the expression to control whether the HTTP request should be made at all.
// The expression has access to PromotionStrategy, Environment, Branch, and ExpressionData (for state tracking).
// Returns (shouldMakeRequest bool, expressionData map[string]any, error).
func (r *WebRequestCommitStatusReconciler) evaluatePollingExpressionPreRequest(
	ctx context.Context,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	envStatus *promoterv1alpha1.EnvironmentStatus,
	previousReconcileStatus *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus,
	branch string,
) (bool, map[string]any, error) {
	logger := log.FromContext(ctx)

	// Get previous expression data from the previous reconcile status
	var previousExpressionData map[string]any
	if previousReconcileStatus != nil && previousReconcileStatus.ExpressionData != nil && previousReconcileStatus.ExpressionData.Raw != nil {
		if err := json.Unmarshal(previousReconcileStatus.ExpressionData.Raw, &previousExpressionData); err != nil {
			logger.Error(err, "Failed to unmarshal previous expression data, starting fresh")
			previousExpressionData = make(map[string]any)
		}
	} else {
		previousExpressionData = make(map[string]any)
	}

	// Build expression data
	data := PollingExpressionData{
		PromotionStrategy: *ps,
		Environment:       *envStatus,
		Branch:            branch,
		ExpressionData:    previousExpressionData,
	}

	// Try to get cached compiled expression
	cacheKey := "trigger:" + wrcs.Spec.Polling.Expression
	var program *vm.Program
	if cached, ok := r.expressionCache.Load(cacheKey); ok {
		program, ok = cached.(*vm.Program)
		if !ok {
			return false, nil, errors.New("invalid cached polling expression program")
		}
	} else {
		// Compile the expression (don't use AsBool - we need to handle both types)
		compiled, err := expr.Compile(wrcs.Spec.Polling.Expression, expr.Env(data))
		if err != nil {
			logger.Error(err, "Failed to compile polling expression", "expression", wrcs.Spec.Polling.Expression)
			return false, nil, fmt.Errorf("polling expression compilation failed: %w", err)
		}
		program = compiled
		r.expressionCache.Store(cacheKey, program)
	}

	// Run the expression
	output, err := expr.Run(program, data)
	if err != nil {
		logger.Error(err, "Failed to evaluate polling expression", "expression", wrcs.Spec.Polling.Expression)
		return false, nil, fmt.Errorf("polling expression evaluation failed: %w", err)
	}

	// Handle both boolean and object returns
	switch v := output.(type) {
	case bool:
		// Simple boolean return
		logger.V(1).Info("Evaluated trigger expression (boolean)",
			"expression", wrcs.Spec.Polling.Expression,
			"result", v,
			"branch", branch)
		return v, nil, nil

	case map[string]any:
		// Object return with 'trigger' field
		triggerField, ok := v["trigger"]
		if !ok {
			return false, nil, errors.New("trigger expression returned object without 'trigger' field")
		}

		shouldTrigger, ok := triggerField.(bool)
		if !ok {
			return false, nil, fmt.Errorf("trigger expression 'trigger' field must be boolean, got %T", triggerField)
		}

		// Extract custom data (everything except 'trigger' field)
		customData := make(map[string]any)
		for k, val := range v {
			if k != "trigger" {
				customData[k] = val
			}
		}

		logger.V(1).Info("Evaluated trigger expression (object)",
			"expression", wrcs.Spec.Polling.Expression,
			"trigger", shouldTrigger,
			"customDataKeys", getMapKeys(customData),
			"branch", branch)

		return shouldTrigger, customData, nil

	default:
		return false, nil, fmt.Errorf("trigger expression must return boolean or object with 'trigger' field, got %T", output)
	}
}

// getMapKeys returns the keys of a map as a slice (helper for logging)
func getMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// touchChangeTransferPolicies triggers reconciliation of the ChangeTransferPolicies
// for the environments that had validations transition to success.
// This triggers the ChangeTransferPolicy controller to reconcile and potentially merge PRs.
func (r *WebRequestCommitStatusReconciler) touchChangeTransferPolicies(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, transitionedEnvironments []string) {
	logger := log.FromContext(ctx)

	// For each transitioned environment, trigger reconciliation of the corresponding ChangeTransferPolicy
	for _, envBranch := range transitionedEnvironments {
		// Generate the ChangeTransferPolicy name using the same logic as the PromotionStrategy controller
		ctpName := utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(ps.Name, envBranch))

		logger.Info("Triggering ChangeTransferPolicy reconciliation due to validation transition",
			"changeTransferPolicy", ctpName,
			"branch", envBranch)

		// Use the enqueue function to trigger reconciliation.
		if r.EnqueueCTP != nil {
			r.EnqueueCTP(ps.Namespace, ctpName)
		}
	}
}

// upsertCommitStatus creates or updates a CommitStatus for the given environment
func (r *WebRequestCommitStatusReconciler) upsertCommitStatus(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, ps *promoterv1alpha1.PromotionStrategy, branch, sha, phase, key string, envStatus *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus) (*promoterv1alpha1.CommitStatus, error) {
	// Generate a consistent name for the CommitStatus
	commitStatusName := utils.KubeSafeUniqueName(ctx, fmt.Sprintf("%s-%s-webrequest", wrcs.Name, branch))

	// Map phase to CommitStatusPhase
	var commitPhase promoterv1alpha1.CommitStatusPhase
	switch phase {
	case WebRequestPhaseSuccess:
		commitPhase = promoterv1alpha1.CommitPhaseSuccess
	case WebRequestPhaseFailure:
		commitPhase = promoterv1alpha1.CommitPhaseFailure
	default:
		commitPhase = promoterv1alpha1.CommitPhasePending
	}

	// Fetch the namespace to get its labels and annotations
	var ns corev1.Namespace
	err := r.Get(ctx, client.ObjectKey{Name: wrcs.Namespace}, &ns)
	if err != nil {
		return nil, fmt.Errorf("failed to get namespace %q: %w", wrcs.Namespace, err)
	}

	// Render description with environment-specific context (includes Phase)
	templateData := TemplateData{
		NamespaceMetadata: NamespaceMetadata{
			Labels:      ns.Labels,
			Annotations: ns.Annotations,
		},
		Branch:              envStatus.Branch,
		ProposedHydratedSha: envStatus.ProposedHydratedSha,
		ActiveHydratedSha:   envStatus.ActiveHydratedSha,
		ReportedSha:         envStatus.ReportedSha,
		LastSuccessfulSha:   envStatus.LastSuccessfulSha,
		Phase:               envStatus.Phase,
	}

	description, err := r.renderTemplate("description", wrcs.Spec.DescriptionTemplate, templateData)
	if err != nil {
		return nil, fmt.Errorf("failed to render description template: %w", err)
	}

	// Render URL with the same template data as description
	url, err := r.renderTemplate("url", wrcs.Spec.UrlTemplate, templateData)
	if err != nil {
		return nil, fmt.Errorf("failed to render url template: %w", err)
	}

	// Build owner reference
	kind := reflect.TypeOf(promoterv1alpha1.WebRequestCommitStatus{}).Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)

	// Build the apply configuration
	commitStatusApply := acv1alpha1.CommitStatus(commitStatusName, wrcs.Namespace).
		WithLabels(map[string]string{
			promoterv1alpha1.WebRequestCommitStatusLabel: utils.KubeSafeLabel(wrcs.Name),
			promoterv1alpha1.EnvironmentLabel:            utils.KubeSafeLabel(branch),
			promoterv1alpha1.CommitStatusLabel:           key,
		}).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(wrcs.Name).
			WithUID(wrcs.UID).
			WithController(true).
			WithBlockOwnerDeletion(true)).
		WithSpec(acv1alpha1.CommitStatusSpec().
			WithRepositoryReference(acv1alpha1.ObjectReference().WithName(ps.Spec.RepositoryReference.Name)).
			WithName(key).
			WithDescription(description).
			WithUrl(url).
			WithPhase(commitPhase).
			WithSha(sha))

	// Apply using Server-Side Apply with Patch to get the result directly
	commitStatus := &promoterv1alpha1.CommitStatus{}
	commitStatus.Name = commitStatusName
	commitStatus.Namespace = wrcs.Namespace
	if err = r.Patch(ctx, commitStatus, utils.ApplyPatch{ApplyConfig: commitStatusApply}, client.FieldOwner(constants.WebRequestCommitStatusControllerFieldOwner), client.ForceOwnership); err != nil {
		return nil, fmt.Errorf("failed to apply CommitStatus: %w", err)
	}

	return commitStatus, nil
}
