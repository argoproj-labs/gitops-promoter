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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
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
	"github.com/argoproj-labs/gitops-promoter/internal/utils/httpauth"
)

// WebRequestCommitStatusReconciler reconciles a WebRequestCommitStatus object
type WebRequestCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager
	EnqueueCTP  CTPEnqueueFunc

	// expressionCache caches compiled expressions to avoid recompilation on every reconciliation
	// Key: expression string, Value: compiled *vm.Program
	expressionCache sync.Map

	// httpClient is a shared HTTP client with sensible defaults
	httpClient *http.Client
}

// TemplateData is the data passed to Go templates for URL, headers, body, and description rendering.
type TemplateData struct {
	// Convenience fields for common use cases
	ReportedSha       string // the SHA being reported on (based on reportOn setting)
	LastSuccessfulSha string // last SHA that achieved success for this environment
	Phase             string // current phase (success/pending/failure)

	// Full objects for advanced use cases
	PromotionStrategy *promoterv1alpha1.PromotionStrategy
	Environment       *promoterv1alpha1.EnvironmentStatus
	NamespaceMetadata NamespaceMetadata
}

// NamespaceMetadata holds namespace labels and annotations for template rendering.
type NamespaceMetadata struct {
	Labels      map[string]string
	Annotations map[string]string
}

// ResponseContext holds the HTTP response data for expression evaluation.
type ResponseContext struct {
	StatusCode int
	Body       any // JSON as map[string]any, or raw string if not JSON
	Headers    map[string][]string
}

// TriggerContext holds the context for trigger expression evaluation.
type TriggerContext struct {
	// Convenience fields
	ReportedSha       string // the SHA being validated (based on reportOn setting)
	LastSuccessfulSha string // last SHA that achieved success
	Phase             string // phase from previous reconcile

	// Full objects (Branch, ProposedHydratedSha, ActiveHydratedSha all accessible via Environment)
	PromotionStrategy *promoterv1alpha1.PromotionStrategy
	Environment       *promoterv1alpha1.EnvironmentStatus

	// Persistent state between reconciles
	TriggerData  map[string]any // custom data from previous trigger evaluation
	ResponseData map[string]any // response data from previous HTTP request
}

// ValidationContext holds the context for validation expression evaluation.
type ValidationContext struct {
	Response ResponseContext
}

// TriggerResult holds the result of a trigger expression evaluation.
type TriggerResult struct {
	Trigger     bool
	TriggerData map[string]any
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=webrequestcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=webrequestcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=webrequestcommitstatuses/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *WebRequestCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling WebRequestCommitStatus")
	startTime := time.Now()

	var wrcs promoterv1alpha1.WebRequestCommitStatus
	// This function will update the resource status at the end of the reconciliation. don't call .Status().Update manually.
	defer utils.HandleReconciliationResult(ctx, startTime, &wrcs, r.Client, r.Recorder, &err)

	// 1. Fetch the WebRequestCommitStatus instance
	err = r.Get(ctx, req.NamespacedName, &wrcs)
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

	// 2. Fetch the referenced PromotionStrategy
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

	// 3. Get namespace metadata for template rendering
	namespaceMeta, err := r.getNamespaceMetadata(ctx, wrcs.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get namespace metadata: %w", err)
	}

	// 4. Process each environment defined in the WebRequestCommitStatus
	transitionedEnvironments, commitStatuses, requeueAfter, err := r.processEnvironments(ctx, &wrcs, &ps, namespaceMeta)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to process environments: %w", err)
	}

	// 5. Clean up orphaned CommitStatus resources that are no longer in the environment list
	err = r.cleanupOrphanedCommitStatuses(ctx, &wrcs, commitStatuses)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to cleanup orphaned CommitStatus resources: %w", err)
	}

	// 6. Inherit conditions from CommitStatus objects
	utils.InheritNotReadyConditionFromObjects(&wrcs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	// 7. If any validations transitioned to success, touch the corresponding ChangeTransferPolicies to trigger reconciliation
	if len(transitionedEnvironments) > 0 {
		r.touchChangeTransferPolicies(ctx, &ps, transitionedEnvironments)
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *WebRequestCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Initialize the HTTP client
	r.httpClient = &http.Client{
		Timeout: 60 * time.Second, // Default timeout, can be overridden per-request
	}

	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
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
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Named("webrequestcommitstatus").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// processEnvironments processes each applicable environment, making HTTP requests and evaluating expressions.
// Returns the list of environments that transitioned to success, the CommitStatus objects, and the requeue duration.
func (r *WebRequestCommitStatusReconciler) processEnvironments(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, ps *promoterv1alpha1.PromotionStrategy, namespaceMeta NamespaceMetadata) ([]string, []*promoterv1alpha1.CommitStatus, time.Duration, error) {
	logger := log.FromContext(ctx)

	// Track which environments transitioned to success
	transitionedEnvironments := []string{}
	// Track all CommitStatus objects created/updated
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0)

	// Save the previous status before clearing it, so we can detect transitions
	previousStatus := wrcs.Status.DeepCopy()
	if previousStatus == nil {
		previousStatus = &promoterv1alpha1.WebRequestCommitStatusStatus{}
	}

	// Build a map of previous environment statuses from WebRequestCommitStatus for efficient lookup
	wrcsEnvStatusMap := make(map[string]*promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus, len(previousStatus.Environments))
	for i := range previousStatus.Environments {
		wrcsEnvStatusMap[previousStatus.Environments[i].Branch] = &previousStatus.Environments[i]
	}

	// Build a map of environments from PromotionStrategy for efficient lookup
	psEnvStatusMap := make(map[string]*promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for i := range ps.Status.Environments {
		psEnvStatusMap[ps.Status.Environments[i].Branch] = &ps.Status.Environments[i]
	}

	// Get applicable environments based on the key
	applicableEnvs := r.getApplicableEnvironments(ps, wrcs.Spec.Key)

	// Initialize or clear the environments status
	wrcs.Status.Environments = make([]promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus, 0, len(applicableEnvs))

	for _, env := range applicableEnvs {
		branch := env.Branch

		// Look up the environment status in PromotionStrategy
		envStatus, found := psEnvStatusMap[branch]
		if !found {
			return nil, nil, 0, fmt.Errorf("environment %q not found in PromotionStrategy status", branch)
		}

		// Determine which SHA to report on based on reportOn setting
		reportedSha := envStatus.Proposed.Hydrated.Sha
		if wrcs.Spec.ReportOn == "active" {
			reportedSha = envStatus.Active.Hydrated.Sha
		}

		if reportedSha == "" {
			return nil, nil, 0, fmt.Errorf("no SHA available for environment %q (reportOn: %q)", branch, wrcs.Spec.ReportOn)
		}

		// Get previous status for this environment
		prevEnvStatus := wrcsEnvStatusMap[branch]
		previousPhase := ""
		lastSuccessfulSha := ""
		var triggerData map[string]any

		if prevEnvStatus != nil {
			previousPhase = prevEnvStatus.Phase
			lastSuccessfulSha = prevEnvStatus.LastSuccessfulSha
			// Parse trigger data from JSON
			if prevEnvStatus.TriggerData != nil {
				triggerData = make(map[string]any)
				if err := json.Unmarshal(prevEnvStatus.TriggerData.Raw, &triggerData); err != nil {
					logger.Error(err, "Failed to unmarshal trigger data", "branch", branch)
				}
			}
		}

		// Parse previous response data for trigger expression
		var previousResponseData map[string]any
		if prevEnvStatus != nil && prevEnvStatus.ResponseData != nil {
			if err := json.Unmarshal(prevEnvStatus.ResponseData.Raw, &previousResponseData); err != nil {
				logger.Error(err, "Failed to unmarshal response data", "branch", branch)
			}
		}

		// Build template data
		templateData := TemplateData{
			ReportedSha:       reportedSha,
			LastSuccessfulSha: lastSuccessfulSha,
			Phase:             previousPhase,
			PromotionStrategy: ps,
			Environment:       envStatus,
			NamespaceMetadata: namespaceMeta,
		}

		// For polling mode with reportOn "proposed", skip HTTP request if already succeeded for this SHA
		// but still ensure CommitStatus exists
		if wrcs.Spec.Mode.Polling != nil && wrcs.Spec.ReportOn == "proposed" {
			if previousPhase == string(promoterv1alpha1.CommitPhaseSuccess) && lastSuccessfulSha == reportedSha {
				logger.V(4).Info("Skipping already successful SHA in polling mode", "branch", branch, "sha", reportedSha)
				// Keep the previous status
				wrcs.Status.Environments = append(wrcs.Status.Environments, *prevEnvStatus)

				// Still ensure CommitStatus exists (it may have been deleted)
				cs, err := r.upsertCommitStatus(ctx, wrcs, ps, branch, reportedSha, promoterv1alpha1.CommitPhaseSuccess, templateData)
				if err != nil {
					return nil, nil, 0, fmt.Errorf("failed to upsert CommitStatus for skipped environment %q: %w", branch, err)
				}
				commitStatuses = append(commitStatuses, cs)
				continue
			}
		}

		// For trigger mode, evaluate the trigger expression first
		shouldTrigger := true
		var newTriggerData map[string]any

		if wrcs.Spec.Mode.Trigger != nil {
			triggerCtx := TriggerContext{
				ReportedSha:       reportedSha,
				LastSuccessfulSha: lastSuccessfulSha,
				Phase:             previousPhase,
				PromotionStrategy: ps,
				Environment:       envStatus,
				TriggerData:       triggerData,
				ResponseData:      previousResponseData,
			}

			triggerResult, err := r.evaluateTriggerExpression(ctx, wrcs.Spec.Mode.Trigger.TriggerExpression, triggerCtx)
			if err != nil {
				return nil, nil, 0, fmt.Errorf("failed to evaluate trigger expression for environment %q: %w", branch, err)
			}
			shouldTrigger = triggerResult.Trigger
			newTriggerData = triggerResult.TriggerData
		}

		var phase promoterv1alpha1.CommitStatusPhase
		var lastRequestTime *metav1.Time
		var lastResponseStatusCode *int
		var responseDataJSON *apiextensionsv1.JSON

		if shouldTrigger {
			// Make the HTTP request
			response, err := r.makeHTTPRequest(ctx, wrcs, templateData)
			if err != nil {
				return nil, nil, 0, fmt.Errorf("failed to make HTTP request for environment %q: %w", branch, err)
			}

			now := metav1.Now()
			lastRequestTime = &now
			lastResponseStatusCode = &response.StatusCode

			// Log response details for debugging
			bodyPreview := fmt.Sprintf("%v", response.Body)
			if len(bodyPreview) > 200 {
				bodyPreview = bodyPreview[:200] + "..."
			}
			logger.V(3).Info("HTTP response received", "branch", branch, "statusCode", response.StatusCode, "bodyPreview", bodyPreview)

			// Store response data (only in trigger mode with responseExpression - allows trigger expressions to use response data in subsequent evaluations)
			if wrcs.Spec.Mode.Trigger != nil && wrcs.Spec.Mode.Trigger.ResponseExpression != "" {
				validationCtx := ValidationContext{
					Response: response,
				}

				extractedData, err := r.evaluateResponseExpression(ctx, wrcs.Spec.Mode.Trigger.ResponseExpression, validationCtx)
				if err != nil {
					return nil, nil, 0, fmt.Errorf("failed to evaluate response expression for environment %q: %w", branch, err)
				}

				responseDataBytes, err := json.Marshal(extractedData)
				if err != nil {
					return nil, nil, 0, fmt.Errorf("failed to marshal response data: %w", err)
				}
				responseDataJSON = &apiextensionsv1.JSON{Raw: responseDataBytes}
			}

			// Evaluate the validation expression
			validationCtx := ValidationContext{
				Response: response,
			}

			passed, err := r.evaluateValidationExpression(ctx, wrcs.Spec.Expression, validationCtx)
			if err != nil {
				return nil, nil, 0, fmt.Errorf("failed to evaluate validation expression for environment %q: %w", branch, err)
			}

			if passed {
				phase = promoterv1alpha1.CommitPhaseSuccess
				lastSuccessfulSha = reportedSha
			} else {
				phase = promoterv1alpha1.CommitPhasePending
			}
		} else {
			// Trigger expression returned false, keep previous phase or default to pending
			logger.V(3).Info("Trigger expression returned false, not making HTTP request", "branch", branch, "triggerExpression", wrcs.Spec.Mode.Trigger.TriggerExpression)
			if previousPhase != "" {
				phase = promoterv1alpha1.CommitStatusPhase(previousPhase)
			} else {
				phase = promoterv1alpha1.CommitPhasePending
			}
			// Preserve previous request metadata
			if prevEnvStatus != nil {
				lastRequestTime = prevEnvStatus.LastRequestTime
				lastResponseStatusCode = prevEnvStatus.LastResponseStatusCode
				responseDataJSON = prevEnvStatus.ResponseData
			}
		}

		// Check if this validation transitioned to success
		if previousPhase != string(promoterv1alpha1.CommitPhaseSuccess) && phase == promoterv1alpha1.CommitPhaseSuccess {
			transitionedEnvironments = append(transitionedEnvironments, branch)
			logger.Info("Validation transitioned to success", "branch", branch, "sha", reportedSha)
		}

		// Convert trigger data to JSON for status
		var triggerDataJSON *apiextensionsv1.JSON
		if newTriggerData != nil {
			jsonBytes, err := json.Marshal(newTriggerData)
			if err != nil {
				return nil, nil, 0, fmt.Errorf("failed to marshal trigger data: %w", err)
			}
			triggerDataJSON = &apiextensionsv1.JSON{Raw: jsonBytes}
		}

		// Update status for this environment
		envWebRequestStatus := promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus{
			Branch:                 branch,
			ReportedSha:            reportedSha,
			LastSuccessfulSha:      lastSuccessfulSha,
			Phase:                  string(phase),
			LastRequestTime:        lastRequestTime,
			LastResponseStatusCode: lastResponseStatusCode,
			TriggerData:            triggerDataJSON,
			ResponseData:           responseDataJSON,
		}
		wrcs.Status.Environments = append(wrcs.Status.Environments, envWebRequestStatus)

		// Create or update the CommitStatus
		cs, err := r.upsertCommitStatus(ctx, wrcs, ps, branch, reportedSha, phase, templateData)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("failed to upsert CommitStatus for environment %q: %w", branch, err)
		}
		commitStatuses = append(commitStatuses, cs)

		logger.Info("Processed environment",
			"branch", branch,
			"reportedSha", reportedSha,
			"phase", phase,
			"triggered", shouldTrigger)
	}

	// Determine requeue duration based on mode
	var requeueAfter time.Duration
	if wrcs.Spec.Mode.Polling != nil {
		requeueAfter = wrcs.Spec.Mode.Polling.Interval.Duration
	} else if wrcs.Spec.Mode.Trigger != nil {
		requeueAfter = wrcs.Spec.Mode.Trigger.RequeueDuration.Duration
	}

	return transitionedEnvironments, commitStatuses, requeueAfter, nil
}

// getApplicableEnvironments returns the environments from the PromotionStrategy that this WebRequestCommitStatus applies to.
// An environment is applicable if the WebRequestCommitStatus key is referenced in either:
// - The global ps.Spec.ProposedCommitStatuses or ps.Spec.ActiveCommitStatuses, or
// - The environment-specific psEnv.ProposedCommitStatuses or psEnv.ActiveCommitStatuses
func (r *WebRequestCommitStatusReconciler) getApplicableEnvironments(ps *promoterv1alpha1.PromotionStrategy, key string) []promoterv1alpha1.Environment {
	// Check if globally referenced in proposed commit statuses
	globallyProposed := false
	for _, selector := range ps.Spec.ProposedCommitStatuses {
		if selector.Key == key {
			globallyProposed = true
			break
		}
	}

	// Check if globally referenced in active commit statuses
	globallyActive := false
	for _, selector := range ps.Spec.ActiveCommitStatuses {
		if selector.Key == key {
			globallyActive = true
			break
		}
	}

	applicable := make([]promoterv1alpha1.Environment, 0, len(ps.Spec.Environments))
	for _, env := range ps.Spec.Environments {
		if globallyProposed || globallyActive {
			applicable = append(applicable, env)
			continue
		}
		// Check environment-specific proposed commit statuses
		for _, selector := range env.ProposedCommitStatuses {
			if selector.Key == key {
				applicable = append(applicable, env)
				break
			}
		}
		// Check environment-specific active commit statuses
		for _, selector := range env.ActiveCommitStatuses {
			if selector.Key == key {
				applicable = append(applicable, env)
				break
			}
		}
	}
	return applicable
}

// makeHTTPRequest makes the HTTP request with the configured settings.
func (r *WebRequestCommitStatusReconciler) makeHTTPRequest(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, templateData TemplateData) (ResponseContext, error) {
	logger := log.FromContext(ctx)

	// Render URL template
	url, err := utils.RenderStringTemplate(wrcs.Spec.HTTPRequest.URLTemplate, templateData)
	if err != nil {
		return ResponseContext{}, fmt.Errorf("failed to render URL template: %w", err)
	}

	// Render body template if present
	var body io.Reader
	if wrcs.Spec.HTTPRequest.BodyTemplate != "" {
		bodyStr, err := utils.RenderStringTemplate(wrcs.Spec.HTTPRequest.BodyTemplate, templateData)
		if err != nil {
			return ResponseContext{}, fmt.Errorf("failed to render body template: %w", err)
		}
		body = strings.NewReader(bodyStr)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, wrcs.Spec.HTTPRequest.Method, url, body)
	if err != nil {
		return ResponseContext{}, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Render and set headers
	for headerName, headerTemplate := range wrcs.Spec.HTTPRequest.HeaderTemplates {
		headerValue, err := utils.RenderStringTemplate(headerTemplate, templateData)
		if err != nil {
			return ResponseContext{}, fmt.Errorf("failed to render header template %q: %w", headerName, err)
		}
		req.Header.Set(headerName, headerValue)
	}

	// Apply authentication if configured
	if wrcs.Spec.HTTPRequest.Authentication != nil {
		httpClient, err := r.applyAuthentication(ctx, wrcs, req)
		if err != nil {
			return ResponseContext{}, fmt.Errorf("failed to apply authentication: %w", err)
		}
		if httpClient != nil {
			// Use the custom client (e.g., for TLS)
			r.httpClient = httpClient
		}
	}

	// Set timeout
	timeout := wrcs.Spec.HTTPRequest.Timeout.Duration
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	// Create a context with timeout
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req = req.WithContext(reqCtx)

	logger.V(4).Info("Making HTTP request", "method", wrcs.Spec.HTTPRequest.Method, "url", url)

	// Execute request
	resp, err := r.httpClient.Do(req)
	if err != nil {
		return ResponseContext{}, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return ResponseContext{}, fmt.Errorf("failed to read response body: %w", err)
	}

	// Try to parse body as JSON
	var parsedBody any
	if err := json.Unmarshal(bodyBytes, &parsedBody); err != nil {
		// Not JSON, use raw string
		parsedBody = string(bodyBytes)
	}

	response := ResponseContext{
		StatusCode: resp.StatusCode,
		Body:       parsedBody,
		Headers:    resp.Header,
	}

	logger.V(4).Info("HTTP request completed", "statusCode", resp.StatusCode)

	return response, nil
}

// applyAuthentication applies the configured authentication to the HTTP request.
// Returns a custom HTTP client if TLS auth is used, otherwise nil.
func (r *WebRequestCommitStatusReconciler) applyAuthentication(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, req *http.Request) (*http.Client, error) {
	auth := wrcs.Spec.HTTPRequest.Authentication

	if auth.Basic != nil {
		err := httpauth.ApplyBasicAuthFromSecret(ctx, r.Client, wrcs.Namespace, auth.Basic.SecretRef.Name, req)
		return nil, err
	}

	if auth.Bearer != nil {
		err := httpauth.ApplyBearerAuthFromSecret(ctx, r.Client, wrcs.Namespace, auth.Bearer.SecretRef.Name, req)
		return nil, err
	}

	if auth.OAuth2 != nil {
		config := &httpauth.OAuth2Config{
			SecretName: auth.OAuth2.SecretRef.Name,
			TokenURL:   auth.OAuth2.TokenURL,
			Scopes:     auth.OAuth2.Scopes,
		}
		err := httpauth.ApplyOAuth2AuthFromSecret(ctx, r.Client, wrcs.Namespace, config, req)
		return nil, err
	}

	if auth.TLS != nil {
		return httpauth.BuildTLSClientFromSecret(ctx, r.Client, wrcs.Namespace, auth.TLS.SecretRef.Name, 60*time.Second)
	}

	return nil, nil
}

// evaluateTriggerExpression evaluates the trigger expression to determine if the HTTP request should be made.
func (r *WebRequestCommitStatusReconciler) evaluateTriggerExpression(ctx context.Context, expression string, triggerCtx TriggerContext) (TriggerResult, error) {
	logger := log.FromContext(ctx)

	// Get compiled expression from cache or compile it
	program, err := r.getCompiledTriggerExpression(expression)
	if err != nil {
		return TriggerResult{}, fmt.Errorf("failed to compile trigger expression: %w", err)
	}

	// Build environment for expression evaluation
	env := map[string]any{
		"ReportedSha":       triggerCtx.ReportedSha,
		"LastSuccessfulSha": triggerCtx.LastSuccessfulSha,
		"Phase":             triggerCtx.Phase,
		"PromotionStrategy": triggerCtx.PromotionStrategy,
		"Environment":       triggerCtx.Environment,
		"TriggerData":       triggerCtx.TriggerData,
		"ResponseData":      triggerCtx.ResponseData,
	}

	// Run the expression
	output, err := expr.Run(program, env)
	if err != nil {
		return TriggerResult{}, fmt.Errorf("failed to evaluate trigger expression: %w", err)
	}

	// Handle boolean result
	if boolResult, ok := output.(bool); ok {
		return TriggerResult{Trigger: boolResult}, nil
	}

	// Handle object result with "trigger" field
	if mapResult, ok := output.(map[string]any); ok {
		trigger := false
		if triggerVal, exists := mapResult["trigger"]; exists {
			if boolVal, ok := triggerVal.(bool); ok {
				trigger = boolVal
			}
		}

		// Extract trigger data (everything except "trigger")
		triggerData := make(map[string]any)
		for k, v := range mapResult {
			if k != "trigger" {
				triggerData[k] = v
			}
		}

		logger.V(4).Info("Trigger expression returned object", "trigger", trigger, "triggerData", triggerData)
		return TriggerResult{Trigger: trigger, TriggerData: triggerData}, nil
	}

	return TriggerResult{}, fmt.Errorf("trigger expression must return bool or {trigger: bool, ...}, got %T", output)
}

// evaluateValidationExpression evaluates the validation expression against the HTTP response.
func (r *WebRequestCommitStatusReconciler) evaluateValidationExpression(ctx context.Context, expression string, validationCtx ValidationContext) (bool, error) {
	// Get compiled expression from cache or compile it
	program, err := r.getCompiledValidationExpression(expression)
	if err != nil {
		return false, fmt.Errorf("failed to compile validation expression: %w", err)
	}

	// Build environment for expression evaluation
	env := map[string]any{
		"Response": map[string]any{
			"StatusCode": validationCtx.Response.StatusCode,
			"Body":       validationCtx.Response.Body,
			"Headers":    validationCtx.Response.Headers,
		},
	}

	// Run the expression
	output, err := expr.Run(program, env)
	if err != nil {
		return false, fmt.Errorf("failed to evaluate validation expression: %w", err)
	}

	// Check the result
	result, ok := output.(bool)
	if !ok {
		return false, fmt.Errorf("validation expression must return boolean, got %T", output)
	}

	return result, nil
}

// evaluateResponseExpression evaluates the response expression to extract/transform response data.
func (r *WebRequestCommitStatusReconciler) evaluateResponseExpression(ctx context.Context, expression string, validationCtx ValidationContext) (map[string]any, error) {
	// Get compiled expression from cache or compile it
	program, err := r.getCompiledResponseExpression(expression)
	if err != nil {
		return nil, fmt.Errorf("failed to compile response expression: %w", err)
	}

	// Build environment for expression evaluation (same as validation expression)
	env := map[string]any{
		"Response": map[string]any{
			"StatusCode": validationCtx.Response.StatusCode,
			"Body":       validationCtx.Response.Body,
			"Headers":    validationCtx.Response.Headers,
		},
	}

	// Run the expression
	output, err := expr.Run(program, env)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate response expression: %w", err)
	}

	// Convert output to map[string]any
	result, ok := output.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("response expression must return a map/object, got %T", output)
	}

	return result, nil
}

// getCompiledTriggerExpression retrieves a compiled trigger expression from the cache or compiles and caches it.
func (r *WebRequestCommitStatusReconciler) getCompiledTriggerExpression(expression string) (*vm.Program, error) {
	cacheKey := "trigger:" + expression

	// Check cache first
	if cached, ok := r.expressionCache.Load(cacheKey); ok {
		program, ok := cached.(*vm.Program)
		if !ok {
			return nil, fmt.Errorf("cached value is not a *vm.Program")
		}
		return program, nil
	}

	// Compile without type constraints since we allow bool or map return
	program, err := expr.Compile(expression)
	if err != nil {
		return nil, fmt.Errorf("failed to compile expression: %w", err)
	}

	// Store in cache
	r.expressionCache.Store(cacheKey, program)
	return program, nil
}

// getCompiledValidationExpression retrieves a compiled validation expression from the cache or compiles and caches it.
func (r *WebRequestCommitStatusReconciler) getCompiledValidationExpression(expression string) (*vm.Program, error) {
	cacheKey := "validation:" + expression

	// Check cache first
	if cached, ok := r.expressionCache.Load(cacheKey); ok {
		program, ok := cached.(*vm.Program)
		if !ok {
			return nil, fmt.Errorf("cached value is not a *vm.Program")
		}
		return program, nil
	}

	// Compile with bool return type constraint
	program, err := expr.Compile(expression, expr.AsBool())
	if err != nil {
		return nil, fmt.Errorf("failed to compile expression: %w", err)
	}

	// Store in cache
	r.expressionCache.Store(cacheKey, program)
	return program, nil
}

// getCompiledResponseExpression retrieves a compiled response expression from the cache or compiles and caches it.
func (r *WebRequestCommitStatusReconciler) getCompiledResponseExpression(expression string) (*vm.Program, error) {
	cacheKey := "response:" + expression

	// Check cache first
	if cached, ok := r.expressionCache.Load(cacheKey); ok {
		program, ok := cached.(*vm.Program)
		if !ok {
			return nil, fmt.Errorf("cached value is not a *vm.Program")
		}
		return program, nil
	}

	// Compile without type constraints since we expect a map return
	program, err := expr.Compile(expression)
	if err != nil {
		return nil, fmt.Errorf("failed to compile expression: %w", err)
	}

	// Store in cache
	r.expressionCache.Store(cacheKey, program)
	return program, nil
}

// upsertCommitStatus creates or updates a CommitStatus resource for the validation result.
func (r *WebRequestCommitStatusReconciler) upsertCommitStatus(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, ps *promoterv1alpha1.PromotionStrategy, branch, sha string, phase promoterv1alpha1.CommitStatusPhase, templateData TemplateData) (*promoterv1alpha1.CommitStatus, error) {
	// Generate a consistent name for the CommitStatus
	commitStatusName := utils.KubeSafeUniqueName(ctx, fmt.Sprintf("%s-%s-webrequest", wrcs.Name, branch))

	// Render description template
	var description string
	if wrcs.Spec.DescriptionTemplate != "" {
		rendered, err := utils.RenderStringTemplate(wrcs.Spec.DescriptionTemplate, templateData)
		if err != nil {
			return nil, fmt.Errorf("failed to render description template: %w", err)
		}
		description = rendered
	}

	// Build owner reference
	kind := reflect.TypeOf(promoterv1alpha1.WebRequestCommitStatus{}).Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)

	// Build the spec
	commitStatusSpec := acv1alpha1.CommitStatusSpec().
		WithRepositoryReference(acv1alpha1.ObjectReference().WithName(ps.Spec.RepositoryReference.Name)).
		WithName(wrcs.Spec.Key + "/" + branch).
		WithDescription(description).
		WithPhase(phase).
		WithSha(sha)

	// Render URL template if present
	if wrcs.Spec.UrlTemplate != "" {
		renderedURL, err := utils.RenderStringTemplate(wrcs.Spec.UrlTemplate, templateData)
		if err != nil {
			return nil, fmt.Errorf("failed to render URL template: %w", err)
		}
		commitStatusSpec = commitStatusSpec.WithUrl(renderedURL)
	}

	// Build the apply configuration
	commitStatusApply := acv1alpha1.CommitStatus(commitStatusName, wrcs.Namespace).
		WithLabels(map[string]string{
			promoterv1alpha1.WebRequestCommitStatusLabel: utils.KubeSafeLabel(wrcs.Name),
			promoterv1alpha1.EnvironmentLabel:            utils.KubeSafeLabel(branch),
			promoterv1alpha1.CommitStatusLabel:           wrcs.Spec.Key,
		}).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(wrcs.Name).
			WithUID(wrcs.UID).
			WithController(true).
			WithBlockOwnerDeletion(true)).
		WithSpec(commitStatusSpec)

	// Apply using Server-Side Apply with Patch to get the result directly
	commitStatus := &promoterv1alpha1.CommitStatus{}
	commitStatus.Name = commitStatusName
	commitStatus.Namespace = wrcs.Namespace
	if err := r.Patch(ctx, commitStatus, utils.ApplyPatch{ApplyConfig: commitStatusApply}, client.FieldOwner(constants.WebRequestCommitStatusControllerFieldOwner), client.ForceOwnership); err != nil {
		return nil, fmt.Errorf("failed to apply CommitStatus: %w", err)
	}

	return commitStatus, nil
}

// cleanupOrphanedCommitStatuses deletes CommitStatus resources that are owned by this WebRequestCommitStatus
// but are not in the current list of valid CommitStatus resources.
func (r *WebRequestCommitStatusReconciler) cleanupOrphanedCommitStatuses(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, validCommitStatuses []*promoterv1alpha1.CommitStatus) error {
	logger := log.FromContext(ctx)

	// Create a set of valid CommitStatus names for quick lookup
	validCommitStatusNames := make(map[string]bool)
	for _, cs := range validCommitStatuses {
		validCommitStatusNames[cs.Name] = true
	}

	// List all CommitStatus resources in the namespace with the WebRequestCommitStatus label
	var commitStatusList promoterv1alpha1.CommitStatusList
	err := r.List(ctx, &commitStatusList, client.InNamespace(wrcs.Namespace), client.MatchingLabels{
		promoterv1alpha1.WebRequestCommitStatusLabel: utils.KubeSafeLabel(wrcs.Name),
	})
	if err != nil {
		return fmt.Errorf("failed to list CommitStatus resources: %w", err)
	}

	// Delete CommitStatus resources that are not in the valid list
	for _, cs := range commitStatusList.Items {
		// Skip if this CommitStatus is in the valid list
		if validCommitStatusNames[cs.Name] {
			continue
		}

		// Verify this CommitStatus is owned by this WebRequestCommitStatus before deleting
		if !metav1.IsControlledBy(&cs, wrcs) {
			logger.V(4).Info("Skipping CommitStatus not owned by this WebRequestCommitStatus",
				"commitStatusName", cs.Name,
				"webRequestCommitStatus", wrcs.Name)
			continue
		}

		// Delete the orphaned CommitStatus
		logger.Info("Deleting orphaned CommitStatus",
			"commitStatusName", cs.Name,
			"webRequestCommitStatus", wrcs.Name,
			"namespace", wrcs.Namespace)

		if err := r.Delete(ctx, &cs); err != nil {
			if k8serrors.IsNotFound(err) {
				// Already deleted, which is fine
				logger.V(4).Info("CommitStatus already deleted", "commitStatusName", cs.Name)
				continue
			}
			return fmt.Errorf("failed to delete orphaned CommitStatus %q: %w", cs.Name, err)
		}

		r.Recorder.Eventf(wrcs, nil, "Normal", constants.OrphanedCommitStatusDeletedReason, "CleaningOrphanedResources", constants.OrphanedCommitStatusDeletedMessage, cs.Name)
	}

	return nil
}

// touchChangeTransferPolicies triggers reconciliation of the ChangeTransferPolicies
// for the environments that had validations transition to success.
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

// enqueueWebRequestCommitStatusForPromotionStrategy returns a handler that enqueues all WebRequestCommitStatus resources
// that reference a PromotionStrategy when that PromotionStrategy changes.
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

// getNamespaceMetadata retrieves the labels and annotations from the namespace.
func (r *WebRequestCommitStatusReconciler) getNamespaceMetadata(ctx context.Context, namespace string) (NamespaceMetadata, error) {
	var ns corev1.Namespace
	if err := r.Get(ctx, client.ObjectKey{Name: namespace}, &ns); err != nil {
		return NamespaceMetadata{}, fmt.Errorf("failed to get namespace %q: %w", namespace, err)
	}

	return NamespaceMetadata{
		Labels:      ns.Labels,
		Annotations: ns.Annotations,
	}, nil
}
