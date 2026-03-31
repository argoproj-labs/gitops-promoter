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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

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

// WebRequestCommitStatusReconciler reconciles WebRequestCommitStatus resources by running HTTP requests
// per environment, evaluating trigger/validation/response expressions, and upserting CommitStatus resources
// so the SCM (e.g. GitHub) shows success or pending based on the validation result.
type WebRequestCommitStatusReconciler struct {
	client.Client
	Recorder        events.EventRecorder
	Scheme          *runtime.Scheme
	SettingsMgr     *settings.Manager
	EnqueueCTP      CTPEnqueueFunc
	httpClient      *http.Client
	expressionCache sync.Map
}

// templateData is the data passed to Go templates when rendering URL, headers, body, and description.
// It is built per environment and includes ReportedSha, Phase, TriggerOutput, ResponseOutput, and namespace metadata.
type templateData struct {
	NamespaceMetadata namespaceMetadata
	PromotionStrategy *promoterv1alpha1.PromotionStrategy
	Environment       *promoterv1alpha1.EnvironmentStatus
	TriggerOutput     map[string]any
	ResponseOutput    map[string]any
	ReportedSha       string
	LastSuccessfulSha string
	Phase             string
}

// namespaceMetadata holds the labels and annotations of the WebRequestCommitStatus's namespace.
// It is included in templateData so URL, header, body, and description templates can reference them.
type namespaceMetadata struct {
	Labels      map[string]string
	Annotations map[string]string
}

// httpResponse holds the raw HTTP response (status, body, headers) after makeHTTPRequest.
// It is passed to evaluateValidationExpression and evaluateResponseExpression as the Response variable.
type httpResponse struct {
	Body       any
	Headers    map[string][]string
	StatusCode int
}

// triggerResult holds the result of evaluateTriggerExpression. Trigger is true when the controller
// should perform the HTTP request. When when.output.expression is configured its map result is
// stored in WebRequestCommitStatusEnvironmentStatus.TriggerOutput and on the next reconcile is
// passed back into templateData.TriggerOutput for both trigger expressions and into the CommitStatus
// description/URL templates.
type triggerResult struct {
	Trigger bool
}

// httpValidationResult holds the outcome of handleHTTPRequestAndValidation. Phase (Success or Pending)
// is derived from the validation expression and is written to the CommitStatus.
//
// ResponseDataJSON is set only in trigger mode when response.output.expression is configured: it is the
// JSON-serialized map returned by the data expression (extract/transform from the HTTP response).
// It is stored in WebRequestCommitStatusEnvironmentStatus.ResponseOutput so it persists across
// reconciles. On the next run it is unmarshalled into templateData.ResponseOutput, so the trigger
// expression can read it (e.g. ResponseOutput.buildUrl). When upserting the CommitStatus it is also
// passed into the description and URL templates as ResponseOutput, so the SCM status can show links
// or text derived from the response (e.g. a link to the CI run).
type httpValidationResult struct {
	LastRequestTime        *metav1.Time
	LastResponseStatusCode *int
	ResponseDataJSON       *apiextensionsv1.JSON
	Phase                  promoterv1alpha1.CommitStatusPhase
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=webrequestcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=webrequestcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=webrequestcommitstatuses/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// Reconcile fetches the WebRequestCommitStatus and its PromotionStrategy, processes each applicable
// environment (evaluating trigger and optionally making the HTTP request and validation), upserts
// CommitStatus resources, cleans up orphaned CommitStatuses, and touches ChangeTransferPolicies when
// an environment transitions to success. Result status and requeue time are updated via the deferred handler.
func (r *WebRequestCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling WebRequestCommitStatus")
	startTime := time.Now()

	var wrcs promoterv1alpha1.WebRequestCommitStatus
	// This function will update the resource status at the end of the reconciliation. don't call .Status().Update manually.
	defer utils.HandleReconciliationResult(ctx, startTime, &wrcs, r.Client, r.Recorder, &result, &err)

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

// SetupWithManager registers the controller with the manager: watch for WebRequestCommitStatus (and
// PromotionStrategy so reconciles are triggered when strategy or environment SHAs change), and applies
// rate limiting and max concurrent reconciles from ControllerConfiguration.
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

// processEnvironments iterates over environments that reference this WebRequestCommitStatus's key. For each,
// it evaluates the trigger expression; if it fires (or in polling mode), it makes the HTTP request and
// runs the validation expression, then upserts the CommitStatus. It updates wrcs.Status.Environments and
// returns the list of branches that transitioned to success, all created/updated CommitStatuses, and the
// requeue duration (from spec or default).
//
//nolint:gocyclo // Complex business logic, refactoring would reduce readability
func (r *WebRequestCommitStatusReconciler) processEnvironments(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, ps *promoterv1alpha1.PromotionStrategy, namespaceMeta namespaceMetadata) ([]string, []*promoterv1alpha1.CommitStatus, time.Duration, error) {
	logger := log.FromContext(ctx)

	// Track which environments transitioned to success
	transitionedEnvironments := []string{}
	// Track all CommitStatus objects created/updated
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0)

	// Save the previous status before we clear wrcs.Status.Environments and rebuild it below.
	// We need this copy because:
	// - Transition detection: identify when an environment first reaches Success (pending -> success).
	// - Polling optimization: skip HTTP when we already succeeded for this SHA (reportOn proposed).
	// - Trigger mode: when the trigger expression returns false, we keep the previous phase and
	//   request metadata instead of making a new HTTP request.
	// - Template/trigger input: URL, body, trigger expression, and descriptions can use previous
	//   phase, lastSuccessfulSha, triggerOutput, and response output from the last run.
	previousStatus := wrcs.Status.DeepCopy()
	if previousStatus == nil {
		previousStatus = &promoterv1alpha1.WebRequestCommitStatusStatus{}
	}

	// Map branch -> previous environment status for quick lookup when processing each environment.
	wrcsEnvStatusMap := make(map[string]*promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus, len(previousStatus.Environments))
	for i := range previousStatus.Environments {
		wrcsEnvStatusMap[previousStatus.Environments[i].Branch] = &previousStatus.Environments[i]
	}

	// Build a map of environments from PromotionStrategy for efficient lookup
	psEnvStatusMap := make(map[string]*promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for i := range ps.Status.Environments {
		psEnvStatusMap[ps.Status.Environments[i].Branch] = &ps.Status.Environments[i]
	}

	// Get applicable environments based on the key and reportOn (only proposed lists when reportOn is proposed, only active when reportOn is active)
	applicableEnvs := r.getApplicableEnvironments(ps, wrcs.Spec.Key, wrcs.Spec.ReportOn)

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
		if wrcs.Spec.ReportOn == constants.CommitRefActive {
			reportedSha = envStatus.Active.Hydrated.Sha
		}

		if reportedSha == "" {
			return nil, nil, 0, fmt.Errorf("no SHA available for environment %q (reportOn: %q)", branch, wrcs.Spec.ReportOn)
		}

		// --- Previous environment status (from last reconciliation) ---
		// We use the last run's status for this branch for:
		//
		// 1. previousPhase: Last reported phase (Success/Pending). Used to:
		//    - Feed into templateData so URL/body/trigger/description templates can branch on it.
		//    - In polling mode: skip the HTTP request when we already succeeded for this SHA.
		//    - In trigger mode: when the trigger expression is false, we keep previousPhase instead of making a new request.
		//    - Detect "transition to success" (previousPhase != Success && new phase == Success) for downstream logic.
		//
		// 2. lastSuccessfulSha: SHA that last passed validation. Used to:
		//    - Expose in templateData (e.g. "last known good" in descriptions or trigger logic).
		//    - In polling mode: skip request when reportedSha == lastSuccessfulSha and phase is already Success.
		//
		// 3. triggerOutput: Output from the last when.output expression evaluation (when it ran). Used to:
		//    - Feed into templateData so the next trigger expression and request templates can use it (e.g. cursor, token).
		//
		// 4. previousResponseData: Body of the last HTTP response (after response expression). Used to:
		//    - Feed into templateData so the trigger expression can decide whether to re-request (e.g. "re-run only if previous result was pending").
		prevEnvStatus := wrcsEnvStatusMap[branch]
		previousPhase := ""
		lastSuccessfulSha := ""
		var triggerData map[string]any

		if prevEnvStatus != nil {
			previousPhase = prevEnvStatus.Phase
			lastSuccessfulSha = prevEnvStatus.LastSuccessfulSha
			if prevEnvStatus.TriggerOutput != nil {
				triggerData = make(map[string]any)
				if err := json.Unmarshal(prevEnvStatus.TriggerOutput.Raw, &triggerData); err != nil {
					// Log but do not return: corrupted or legacy-format data should not block reconciliation.
					// We continue with nil triggerData; the next successful run will overwrite status.
					logger.Error(err, "Failed to unmarshal trigger data", "branch", branch)
				}
			}
		}

		var previousResponseData map[string]any
		if prevEnvStatus != nil && prevEnvStatus.ResponseOutput != nil {
			if err := json.Unmarshal(prevEnvStatus.ResponseOutput.Raw, &previousResponseData); err != nil {
				// Log but do not return: same resilience as trigger data; continue with nil previousResponseData.
				logger.Error(err, "Failed to unmarshal response data", "branch", branch)
			}
		}

		// Build template data
		templateData := templateData{
			ReportedSha:       reportedSha,
			LastSuccessfulSha: lastSuccessfulSha,
			Phase:             previousPhase,
			PromotionStrategy: ps,
			Environment:       envStatus,
			NamespaceMetadata: namespaceMeta,
			TriggerOutput:     triggerData,
			ResponseOutput:    previousResponseData,
		}

		// For polling mode with reportOn "proposed", skip HTTP request if already succeeded for this SHA
		// but still ensure CommitStatus exists
		if wrcs.Spec.Mode.Polling != nil && wrcs.Spec.ReportOn == constants.CommitRefProposed {
			if prevEnvStatus != nil && previousPhase == string(promoterv1alpha1.CommitPhaseSuccess) && lastSuccessfulSha == reportedSha {
				logger.V(4).Info("Skipping already successful SHA in polling mode", "branch", branch, "sha", reportedSha)
				// Keep the previous status
				wrcs.Status.Environments = append(wrcs.Status.Environments, *prevEnvStatus)

				// Still ensure CommitStatus exists (it may have been deleted)
				cs, err := r.upsertCommitStatus(ctx, wrcs, ps.Spec.RepositoryReference.Name, branch, reportedSha, promoterv1alpha1.CommitPhaseSuccess, templateData)
				if err != nil {
					return nil, nil, 0, fmt.Errorf("failed to upsert CommitStatus for skipped environment %q: %w", branch, err)
				}
				commitStatuses = append(commitStatuses, cs)
				continue
			}
		}

		// Decide whether to make the HTTP request this reconcile
		shouldTrigger := true
		var newTriggerData map[string]any

		// In polling mode: only make a request when the configured interval has elapsed since last request.
		// Other events (e.g. PromotionStrategy update) can trigger reconcile; we respect the interval.
		if wrcs.Spec.Mode.Polling != nil {
			if prevEnvStatus != nil && prevEnvStatus.LastRequestTime != nil {
				elapsed := time.Since(prevEnvStatus.LastRequestTime.Time)
				if elapsed < wrcs.Spec.Mode.Polling.Interval.Duration {
					logger.V(4).Info("Within polling interval, skipping HTTP request", "branch", branch, "elapsed", elapsed, "interval", wrcs.Spec.Mode.Polling.Interval.Duration)
					shouldTrigger = false
				}
			}
		}

		if wrcs.Spec.Mode.Trigger != nil {
			tr, err := r.evaluateTriggerExpression(ctx, wrcs.Spec.Mode.Trigger.When.Expression, templateData)
			if err != nil {
				return nil, nil, 0, fmt.Errorf("failed to evaluate trigger expression for environment %q: %w", branch, err)
			}
			shouldTrigger = tr.Trigger

			// Evaluate the when.output expression (if configured) to produce TriggerOutput for the next reconcile.
			if wrcs.Spec.Mode.Trigger.When.Output != nil && wrcs.Spec.Mode.Trigger.When.Output.Expression != "" {
				newTriggerData, err = r.evaluateTriggerDataExpression(ctx, wrcs.Spec.Mode.Trigger.When.Output.Expression, templateData)
				if err != nil {
					return nil, nil, 0, fmt.Errorf("failed to evaluate trigger data expression for environment %q: %w", branch, err)
				}
			}
		}

		var phase promoterv1alpha1.CommitStatusPhase
		var lastRequestTime *metav1.Time
		var lastResponseStatusCode *int
		var responseDataJSON *apiextensionsv1.JSON
		var err error

		//nolint:nestif // Extracted most logic to helper function, remaining complexity is minimal
		if shouldTrigger {
			result, err := r.handleHTTPRequestAndValidation(ctx, wrcs, templateData, branch)
			if err != nil {
				return nil, nil, 0, err
			}
			phase = result.Phase
			lastRequestTime = result.LastRequestTime
			lastResponseStatusCode = result.LastResponseStatusCode
			responseDataJSON = result.ResponseDataJSON
			// Only update lastSuccessfulSha if validation actually succeeded
			if phase == promoterv1alpha1.CommitPhaseSuccess {
				lastSuccessfulSha = reportedSha
			}
		} else {
			// Not making HTTP request: either trigger expression returned false or (polling mode) within interval
			if wrcs.Spec.Mode.Trigger != nil {
				logger.V(4).Info("Trigger expression returned false, not making HTTP request", "branch", branch, "triggerExpression", wrcs.Spec.Mode.Trigger.When.Expression)
			}
			if previousPhase != "" {
				phase = promoterv1alpha1.CommitStatusPhase(previousPhase)
			} else {
				phase = promoterv1alpha1.CommitPhasePending
			}
			// Preserve previous request metadata
			if prevEnvStatus != nil {
				lastRequestTime = prevEnvStatus.LastRequestTime
				lastResponseStatusCode = prevEnvStatus.LastResponseStatusCode
				responseDataJSON = prevEnvStatus.ResponseOutput
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
			TriggerOutput:          triggerDataJSON,
			ResponseOutput:         responseDataJSON,
		}
		wrcs.Status.Environments = append(wrcs.Status.Environments, envWebRequestStatus)

		// Use latest response and trigger output for commit status description template
		commitTemplateData := templateData
		if responseDataJSON != nil {
			var latestResponseData map[string]any
			if err := json.Unmarshal(responseDataJSON.Raw, &latestResponseData); err == nil {
				commitTemplateData.ResponseOutput = latestResponseData
			}
		}
		if newTriggerData != nil {
			commitTemplateData.TriggerOutput = newTriggerData
		}

		// Create or update the CommitStatus
		cs, err := r.upsertCommitStatus(ctx, wrcs, ps.Spec.RepositoryReference.Name, branch, reportedSha, phase, commitTemplateData)
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

// handleHTTPRequestAndValidation is called when the trigger allows (or in polling mode). It performs the
// HTTP request, optionally runs the response expression to populate ResponseOutput, then runs the validation
// expression to set Phase (Success or Pending). The returned httpValidationResult is used to update
// environment status and to upsert the CommitStatus for that branch.
func (r *WebRequestCommitStatusReconciler) handleHTTPRequestAndValidation(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, templateData templateData, branch string) (httpValidationResult, error) {
	logger := log.FromContext(ctx)

	// Make the HTTP request
	response, err := r.makeHTTPRequest(ctx, wrcs, templateData)
	if err != nil {
		return httpValidationResult{}, fmt.Errorf("failed to make HTTP request for environment %q: %w", branch, err)
	}

	now := metav1.Now()
	lastRequestTime := &now
	lastResponseStatusCode := &response.StatusCode

	logger.V(4).Info("HTTP response received", "branch", branch, "statusCode", response.StatusCode)

	// Store response data (only in trigger mode with response.output.expression)
	var responseDataJSON *apiextensionsv1.JSON
	if wrcs.Spec.Mode.Trigger != nil && wrcs.Spec.Mode.Trigger.Response != nil {
		extractedData, err := r.evaluateResponseDataExpression(ctx, wrcs.Spec.Mode.Trigger.Response.Output.Expression, response)
		if err != nil {
			return httpValidationResult{}, fmt.Errorf("failed to evaluate response data expression for environment %q: %w", branch, err)
		}

		responseDataBytes, err := json.Marshal(extractedData)
		if err != nil {
			return httpValidationResult{}, fmt.Errorf("failed to marshal response data: %w", err)
		}
		responseDataJSON = &apiextensionsv1.JSON{Raw: responseDataBytes}
	}

	// Evaluate the success expression
	passed, err := r.evaluateValidationExpression(ctx, wrcs.Spec.Success.When.Expression, response)
	if err != nil {
		return httpValidationResult{}, fmt.Errorf("failed to evaluate validation expression for environment %q: %w", branch, err)
	}

	var phase promoterv1alpha1.CommitStatusPhase
	if passed {
		phase = promoterv1alpha1.CommitPhaseSuccess
	} else {
		phase = promoterv1alpha1.CommitPhasePending
	}

	return httpValidationResult{
		Phase:                  phase,
		LastRequestTime:        lastRequestTime,
		LastResponseStatusCode: lastResponseStatusCode,
		ResponseDataJSON:       responseDataJSON,
	}, nil
}

// getApplicableEnvironments returns the PromotionStrategy environments this WebRequestCommitStatus should run for.
// An environment is included if its key is referenced in global or environment-specific ProposedCommitStatuses
// (when reportOn is "proposed" or default) or ActiveCommitStatuses (when reportOn is "active").
func (r *WebRequestCommitStatusReconciler) getApplicableEnvironments(ps *promoterv1alpha1.PromotionStrategy, key string, reportOn string) []promoterv1alpha1.Environment {
	globalSelectors := ps.Spec.ProposedCommitStatuses
	getEnvSelectors := func(e promoterv1alpha1.Environment) []promoterv1alpha1.CommitStatusSelector {
		return e.ProposedCommitStatuses
	}
	if reportOn == constants.CommitRefActive {
		globalSelectors = ps.Spec.ActiveCommitStatuses
		getEnvSelectors = func(e promoterv1alpha1.Environment) []promoterv1alpha1.CommitStatusSelector {
			return e.ActiveCommitStatuses
		}
	}

	keyInSelectors := func(selectors []promoterv1alpha1.CommitStatusSelector) bool {
		for _, sel := range selectors {
			if sel.Key == key {
				return true
			}
		}
		return false
	}
	keyInGlobal := keyInSelectors(globalSelectors)

	applicable := make([]promoterv1alpha1.Environment, 0, len(ps.Spec.Environments))
	for _, env := range ps.Spec.Environments {
		if keyInGlobal || keyInSelectors(getEnvSelectors(env)) {
			applicable = append(applicable, env)
		}
	}
	return applicable
}

// makeHTTPRequest builds and executes the HTTP request from the WebRequestCommitStatus spec. It renders
// URL, body, and headers from templateData, applies authentication (basic, bearer, OAuth2, or TLS), uses the
// configured timeout, and parses the response body as JSON or plain text. The returned httpResponse is used
// for validation and response expression evaluation.
// When Scm is configured, the rendered URL host is validated against the SCM provider's allowed
// domains before the request is made, to prevent SCM credentials leaking to unintended hosts.
func (r *WebRequestCommitStatusReconciler) makeHTTPRequest(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, templateData templateData) (httpResponse, error) {
	logger := log.FromContext(ctx)

	// Render URL template
	url, err := utils.RenderStringTemplate(wrcs.Spec.HTTPRequest.URLTemplate, templateData)
	if err != nil {
		return httpResponse{}, fmt.Errorf("failed to render URL template: %w", err)
	}

	// When Scm is configured, credentials are sourced directly from the SCM provider, so the
	// URL host must belong to that provider's allowed domains to prevent credential leakage.
	if wrcs.Spec.HTTPRequest.Authentication != nil && wrcs.Spec.HTTPRequest.Authentication.Scm != nil {
		if err := r.validateURLHostAgainstScmProvider(ctx, wrcs, url); err != nil {
			return httpResponse{}, fmt.Errorf("SCM host validation failed: %w", err)
		}
	}

	// Render body template if present
	var body io.Reader
	if wrcs.Spec.HTTPRequest.BodyTemplate != "" {
		bodyStr, err := utils.RenderStringTemplate(wrcs.Spec.HTTPRequest.BodyTemplate, templateData)
		if err != nil {
			return httpResponse{}, fmt.Errorf("failed to render body template: %w", err)
		}
		body = strings.NewReader(bodyStr)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, wrcs.Spec.HTTPRequest.Method, url, body)
	if err != nil {
		return httpResponse{}, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Render and set headers
	for headerName, headerTemplate := range wrcs.Spec.HTTPRequest.HeaderTemplates {
		headerValue, err := utils.RenderStringTemplate(headerTemplate, templateData)
		if err != nil {
			return httpResponse{}, fmt.Errorf("failed to render header template %q: %w", headerName, err)
		}
		req.Header.Set(headerName, headerValue)
	}

	// Use shared default client unless authentication returns a per-request client (e.g. TLS).
	// Never assign to r.httpClient here: concurrent reconciliations share the reconciler, and
	// overwriting r.httpClient would create a data race and wrong client usage across goroutines.
	clientToUse := r.httpClient
	if wrcs.Spec.HTTPRequest.Authentication != nil {
		authClient, err := r.applyAuthentication(ctx, wrcs, req)
		if err != nil {
			return httpResponse{}, fmt.Errorf("failed to apply authentication: %w", err)
		}
		if authClient != nil {
			clientToUse = authClient
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
	resp, err := clientToUse.Do(req)
	if err != nil {
		return httpResponse{}, fmt.Errorf("HTTP request failed: %w", err)
	}
	if resp == nil {
		return httpResponse{}, errors.New("HTTP response is nil")
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			logger.V(4).Info("Failed to close response body", "error", closeErr)
		}
	}()

	// Read response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return httpResponse{}, fmt.Errorf("failed to read response body: %w", err)
	}

	// Try to parse body as JSON
	var parsedBody any
	if err := json.Unmarshal(bodyBytes, &parsedBody); err != nil {
		// Not JSON, use raw string
		parsedBody = string(bodyBytes)
	}

	response := httpResponse{
		StatusCode: resp.StatusCode,
		Body:       parsedBody,
		Headers:    resp.Header,
	}

	logger.V(4).Info("HTTP request completed", "statusCode", resp.StatusCode)

	return response, nil
}

// applyAuthentication configures the request (or client) with the auth from spec: Basic, Bearer, OAuth2, TLS, or Scm.
// For Basic/Bearer/OAuth2 it mutates the request and returns nil. For TLS or Scm it builds and returns
// a custom http.Client. Credentials are read from the referenced Secrets or from the SCM provider (Scm).
func (r *WebRequestCommitStatusReconciler) applyAuthentication(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, req *http.Request) (*http.Client, error) {
	auth := wrcs.Spec.HTTPRequest.Authentication

	if auth.Basic != nil {
		if err := httpauth.ApplyBasicAuthFromSecret(ctx, r.Client, wrcs.Namespace, auth.Basic.SecretRef.Name, req); err != nil {
			return nil, fmt.Errorf("failed to apply basic auth: %w", err)
		}
		return nil, nil
	}

	if auth.Bearer != nil {
		if err := httpauth.ApplyBearerAuthFromSecret(ctx, r.Client, wrcs.Namespace, auth.Bearer.SecretRef.Name, req); err != nil {
			return nil, fmt.Errorf("failed to apply bearer auth: %w", err)
		}
		return nil, nil
	}

	if auth.OAuth2 != nil {
		config := &httpauth.OAuth2Config{
			SecretName: auth.OAuth2.SecretRef.Name,
			TokenURL:   auth.OAuth2.TokenURL,
			Scopes:     auth.OAuth2.Scopes,
		}
		if err := httpauth.ApplyOAuth2AuthFromSecret(ctx, r.Client, wrcs.Namespace, config, req); err != nil {
			return nil, fmt.Errorf("failed to apply oauth2 auth: %w", err)
		}
		return nil, nil
	}

	if auth.TLS != nil {
		timeout := wrcs.Spec.HTTPRequest.Timeout.Duration
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		client, err := httpauth.BuildTLSClientFromSecret(ctx, r.Client, wrcs.Namespace, auth.TLS.SecretRef.Name, timeout)
		if err != nil {
			return nil, fmt.Errorf("failed to build TLS client: %w", err)
		}
		return client, nil
	}

	if auth.Scm != nil {
		var ps promoterv1alpha1.PromotionStrategy
		if err := r.Get(ctx, client.ObjectKey{Namespace: wrcs.Namespace, Name: wrcs.Spec.PromotionStrategyRef.Name}, &ps); err != nil {
			return nil, fmt.Errorf("failed to get PromotionStrategy for Scm: %w", err)
		}
		return r.applySCMAuthentication(ctx, wrcs.Namespace, ps.Spec.RepositoryReference, req)
	}

	return nil, nil
}

// applySCMAuthentication applies authentication using the SCM provider credentials from the referenced GitRepository.
func (r *WebRequestCommitStatusReconciler) applySCMAuthentication(ctx context.Context, namespace string, repositoryRef promoterv1alpha1.ObjectReference, req *http.Request) (*http.Client, error) {
	scmProvider, secret, gitRepo, err := utils.GetScmProviderSecretAndGitRepositoryFromRepositoryReference(
		ctx,
		r.Client,
		r.SettingsMgr.GetControllerNamespace(),
		repositoryRef,
		&metav1.ObjectMeta{Namespace: namespace},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get SCM provider and secret: %w", err)
	}
	client, err := httpauth.ApplySCMAuth(ctx, scmProvider, *secret, req, gitRepo)
	if err != nil {
		return nil, fmt.Errorf("failed to apply SCM auth: %w", err)
	}
	return client, nil
}

// upsertCommitStatus creates or updates the CommitStatus resource that reports this WebRequestCommitStatus's result to the SCM.
// The phase (Success or Pending) and sha are set from the validation outcome; description and URL are rendered from templateData.
// The created resource is owned by the WebRequestCommitStatus so it is cleaned up when the WebRequestCommitStatus is deleted.
func (r *WebRequestCommitStatusReconciler) upsertCommitStatus(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, repositoryRefName string, branch, sha string, phase promoterv1alpha1.CommitStatusPhase, templateData templateData) (*promoterv1alpha1.CommitStatus, error) {
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
		WithRepositoryReference(acv1alpha1.ObjectReference().WithName(repositoryRefName)).
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

// cleanupOrphanedCommitStatuses removes CommitStatus resources that are owned by this WebRequestCommitStatus
// and labeled with its key but are not in validCommitStatuses (e.g. branches no longer in the strategy).
// Called after processEnvironments so the cluster state matches the current set of applicable environments.
//
//nolint:dupl // Similar to cleanupOrphanedChangeTransferPolicies but operates on different types
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

// touchChangeTransferPolicies enqueues the ChangeTransferPolicy for each environment in transitionedEnvironments,
// so the CTP controller re-runs and can merge the PR now that this WebRequestCommitStatus has reported success.
// Called from Reconcile when at least one environment's validation has just transitioned to success.
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

// enqueueWebRequestCommitStatusForPromotionStrategy returns the watch handler for PromotionStrategy. When a
// PromotionStrategy is created/updated (e.g. environment SHAs or status change), it enqueues every
// WebRequestCommitStatus in the same namespace that references that strategy, so they reconcile with fresh data.
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

// getNamespaceMetadata fetches the namespace's labels and annotations for use in templateData, so URL, header,
// body, and description templates can reference them. Called at the start of Reconcile for the
// WebRequestCommitStatus's namespace.
func (r *WebRequestCommitStatusReconciler) getNamespaceMetadata(ctx context.Context, namespace string) (namespaceMetadata, error) {
	var ns corev1.Namespace
	if err := r.Get(ctx, client.ObjectKey{Name: namespace}, &ns); err != nil {
		return namespaceMetadata{}, fmt.Errorf("failed to get namespace %q: %w", namespace, err)
	}

	return namespaceMetadata{
		Labels:      ns.Labels,
		Annotations: ns.Annotations,
	}, nil
}

// allowedHostsForScmProvider returns the URL hostname permitted for the given ScmProviderSpec.
// It may include a port (e.g. "gitlab.corp.example.com:8443"). Matching strips the port from both
// the configured value and the request URL so that standard-port URLs always match.
func allowedHostsForScmProvider(spec *promoterv1alpha1.ScmProviderSpec) string {
	switch {
	case spec.GitHub != nil:
		if spec.GitHub.Domain != "" {
			return spec.GitHub.Domain
		}
		return "api.github.com"
	case spec.GitLab != nil:
		if spec.GitLab.Domain != "" {
			return spec.GitLab.Domain
		}
		return "gitlab.com"
	case spec.Forgejo != nil:
		return spec.Forgejo.Domain
	case spec.Gitea != nil:
		return spec.Gitea.Domain
	case spec.BitbucketCloud != nil:
		return "api.bitbucket.org"
	case spec.AzureDevOps != nil:
		if spec.AzureDevOps.Domain != "" {
			return spec.AzureDevOps.Domain
		}
		return "dev.azure.com"
	case spec.Fake != nil:
		return spec.Fake.Domain
	default:
		return ""
	}
}

// hostMatches reports whether a request URL host matches a configured host entry.
// If the configured entry includes a port (e.g. "host:8443"), the full host:port must match.
// If the configured entry has no port (e.g. "host"), only the hostname is compared so any port is allowed.
// All comparisons are case-insensitive.
func hostMatches(configuredHost, requestHost, requestHostname string) bool {
	configured := &url.URL{Host: configuredHost}
	if configured.Port() != "" {
		return strings.ToLower(configuredHost) == requestHost
	}
	return strings.ToLower(configured.Hostname()) == requestHostname
}

// validateURLHostAgainstScmProvider checks that the host of renderedURL is among the hosts permitted by
// the SCM provider that backs the PromotionStrategy's GitRepository. This is called only when Scm
// is configured, preventing SCM credentials from being sent to an arbitrary host.
func (r *WebRequestCommitStatusReconciler) validateURLHostAgainstScmProvider(
	ctx context.Context,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	renderedURL string,
) error {
	parsed, err := url.Parse(renderedURL)
	if err != nil {
		return fmt.Errorf("failed to parse URL %q: %w", renderedURL, err)
	}
	if parsed.Host == "" {
		return fmt.Errorf("URL %q has no host", renderedURL)
	}
	requestHost := strings.ToLower(parsed.Host)
	requestHostname := strings.ToLower(parsed.Hostname())

	var ps promoterv1alpha1.PromotionStrategy
	if err := r.Get(ctx, client.ObjectKey{Namespace: wrcs.Namespace, Name: wrcs.Spec.PromotionStrategyRef.Name}, &ps); err != nil {
		return fmt.Errorf("failed to get PromotionStrategy for SCM host validation: %w", err)
	}

	// Resolve the GitRepository for this PromotionStrategy.
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{
		Namespace: wrcs.Namespace,
		Name:      ps.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository for SCM host validation: %w", err)
	}

	// Resolve the ScmProvider (namespaced or cluster-scoped).
	scmProvider, err := utils.GetScmProviderFromGitRepository(ctx, r.Client, gitRepo, wrcs)
	if err != nil {
		return fmt.Errorf("failed to get ScmProvider for SCM host validation: %w", err)
	}

	allowed := allowedHostsForScmProvider(scmProvider.GetSpec())
	if hostMatches(allowed, requestHost, requestHostname) {
		return nil
	}

	return fmt.Errorf("URL host %q is not allowed for the configured SCM provider; permitted host: %q", requestHostname, allowed)
}
