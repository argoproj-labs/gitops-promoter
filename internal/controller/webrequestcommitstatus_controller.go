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
	Recorder        events.EventRecorder
	Scheme          *runtime.Scheme
	SettingsMgr     *settings.Manager
	EnqueueCTP      CTPEnqueueFunc
	httpClient      *http.Client
	expressionCache sync.Map
}

// templateData is the data passed to Go templates for URL, headers, body, and description rendering.
type templateData struct {
	NamespaceMetadata namespaceMetadata
	PromotionStrategy *promoterv1alpha1.PromotionStrategy
	Environment       *promoterv1alpha1.EnvironmentStatus
	TriggerData       map[string]any
	ResponseData      map[string]any
	ReportedSha       string
	LastSuccessfulSha string
	Phase             string
}

// namespaceMetadata holds namespace labels and annotations for template rendering.
type namespaceMetadata struct {
	Labels      map[string]string
	Annotations map[string]string
}

// httpResponse holds the HTTP response data for validation/response expression evaluation.
type httpResponse struct {
	Body       any
	Headers    map[string][]string
	StatusCode int
}

// triggerResult holds the result of a trigger expression evaluation.
type triggerResult struct {
	TriggerData map[string]any
	Trigger     bool
}

// httpValidationResult holds the result of HTTP request and validation.
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
//
//nolint:gocyclo // Complex business logic, refactoring would reduce readability
func (r *WebRequestCommitStatusReconciler) processEnvironments(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, ps *promoterv1alpha1.PromotionStrategy, namespaceMeta namespaceMetadata) ([]string, []*promoterv1alpha1.CommitStatus, time.Duration, error) {
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
		templateData := templateData{
			ReportedSha:       reportedSha,
			LastSuccessfulSha: lastSuccessfulSha,
			Phase:             previousPhase,
			PromotionStrategy: ps,
			Environment:       envStatus,
			NamespaceMetadata: namespaceMeta,
			TriggerData:       triggerData,
			ResponseData:      previousResponseData,
		}

		// For polling mode with reportOn "proposed", skip HTTP request if already succeeded for this SHA
		// but still ensure CommitStatus exists
		if wrcs.Spec.Mode.Polling != nil && wrcs.Spec.ReportOn == "proposed" {
			if prevEnvStatus != nil && previousPhase == string(promoterv1alpha1.CommitPhaseSuccess) && lastSuccessfulSha == reportedSha {
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
			tr, err := r.evaluateTriggerExpression(ctx, wrcs.Spec.Mode.Trigger.TriggerExpression, templateData)
			if err != nil {
				return nil, nil, 0, fmt.Errorf("failed to evaluate trigger expression for environment %q: %w", branch, err)
			}
			shouldTrigger = tr.Trigger
			newTriggerData = tr.TriggerData
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

		// Use latest response and trigger data for commit status description template
		commitTemplateData := templateData
		if responseDataJSON != nil {
			var latestResponseData map[string]any
			if err := json.Unmarshal(responseDataJSON.Raw, &latestResponseData); err == nil {
				commitTemplateData.ResponseData = latestResponseData
			}
		}
		if newTriggerData != nil {
			commitTemplateData.TriggerData = newTriggerData
		}

		// Create or update the CommitStatus
		cs, err := r.upsertCommitStatus(ctx, wrcs, ps, branch, reportedSha, phase, commitTemplateData)
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

// handleHTTPRequestAndValidation makes the HTTP request and evaluates the validation expression.
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

	// Log response details for debugging
	bodyPreview := fmt.Sprintf("%v", response.Body)
	if len(bodyPreview) > 200 {
		bodyPreview = bodyPreview[:200] + "..."
	}
	logger.V(3).Info("HTTP response received", "branch", branch, "statusCode", response.StatusCode, "bodyPreview", bodyPreview)

	// Store response data (only in trigger mode with responseExpression)
	var responseDataJSON *apiextensionsv1.JSON
	if wrcs.Spec.Mode.Trigger != nil && wrcs.Spec.Mode.Trigger.ResponseExpression != "" {
		extractedData, err := r.evaluateResponseExpression(ctx, wrcs.Spec.Mode.Trigger.ResponseExpression, response)
		if err != nil {
			return httpValidationResult{}, fmt.Errorf("failed to evaluate response expression for environment %q: %w", branch, err)
		}

		responseDataBytes, err := json.Marshal(extractedData)
		if err != nil {
			return httpValidationResult{}, fmt.Errorf("failed to marshal response data: %w", err)
		}
		responseDataJSON = &apiextensionsv1.JSON{Raw: responseDataBytes}
	}

	// Evaluate the validation expression
	passed, err := r.evaluateValidationExpression(ctx, wrcs.Spec.Expression, response)
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
func (r *WebRequestCommitStatusReconciler) makeHTTPRequest(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, templateData templateData) (httpResponse, error) {
	logger := log.FromContext(ctx)

	// Render URL template
	url, err := utils.RenderStringTemplate(wrcs.Spec.HTTPRequest.URLTemplate, templateData)
	if err != nil {
		return httpResponse{}, fmt.Errorf("failed to render URL template: %w", err)
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

	// Apply authentication if configured
	if wrcs.Spec.HTTPRequest.Authentication != nil {
		httpClient, err := r.applyAuthentication(ctx, wrcs, req)
		if err != nil {
			return httpResponse{}, fmt.Errorf("failed to apply authentication: %w", err)
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

// applyAuthentication applies the configured authentication to the HTTP request.
// Returns a custom HTTP client if TLS auth is used, otherwise nil.
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
		client, err := httpauth.BuildTLSClientFromSecret(ctx, r.Client, wrcs.Namespace, auth.TLS.SecretRef.Name, 60*time.Second)
		if err != nil {
			return nil, fmt.Errorf("failed to build TLS client: %w", err)
		}
		return client, nil
	}

	return nil, nil
}

// evaluateTriggerExpression runs the trigger expression to decide whether to perform the HTTP request for this environment.
// It is called before each potential request; the expression has access to ReportedSha, LastSuccessfulSha, Phase, PromotionStrategy, Environment, TriggerData, and ResponseData.
// Returns a bool (and optional triggerData map). When true, the controller issues the request; when false, it keeps the previous phase (e.g. Pending) and skips the request.
func (r *WebRequestCommitStatusReconciler) evaluateTriggerExpression(ctx context.Context, expression string, td templateData) (triggerResult, error) {
	logger := log.FromContext(ctx)

	// Get compiled expression from cache or compile it
	program, err := r.getCompiledTriggerExpression(expression)
	if err != nil {
		return triggerResult{}, fmt.Errorf("failed to compile trigger expression: %w", err)
	}

	// Build environment for expression evaluation
	env := map[string]any{
		"ReportedSha":       td.ReportedSha,
		"LastSuccessfulSha": td.LastSuccessfulSha,
		"Phase":             td.Phase,
		"PromotionStrategy": td.PromotionStrategy,
		"Environment":       td.Environment,
		"TriggerData":       td.TriggerData,
		"ResponseData":      td.ResponseData,
	}

	// Run the expression
	output, err := expr.Run(program, env)
	if err != nil {
		return triggerResult{}, fmt.Errorf("failed to evaluate trigger expression: %w", err)
	}

	// Handle boolean result
	if boolResult, ok := output.(bool); ok {
		return triggerResult{Trigger: boolResult}, nil
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
		return triggerResult{Trigger: trigger, TriggerData: triggerData}, nil
	}

	return triggerResult{}, fmt.Errorf("trigger expression must return bool or {trigger: bool, ...}, got %T", output)
}

// evaluateValidationExpression runs the validation expression against the HTTP response to determine the commit status phase.
// The expression receives Response.StatusCode, Response.Body, and Response.Headers. Its boolean return is used directly:
// true sets the CommitStatus phase to Success, false sets it to Pending. Used when deciding whether promotion can proceed.
func (r *WebRequestCommitStatusReconciler) evaluateValidationExpression(_ context.Context, expression string, resp httpResponse) (bool, error) {
	// Get compiled expression from cache or compile it
	program, err := r.getCompiledValidationExpression(expression)
	if err != nil {
		return false, fmt.Errorf("failed to compile validation expression: %w", err)
	}

	// Build environment for expression evaluation
	env := map[string]any{
		"Response": map[string]any{
			"StatusCode": resp.StatusCode,
			"Body":       resp.Body,
			"Headers":    resp.Headers,
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

// evaluateResponseExpression runs the response expression to extract or transform data from the HTTP response.
// The expression receives Response.StatusCode, Response.Body, and Response.Headers and must return a map.
// The returned map is stored as ResponseData and is available to the trigger expression and to description/URL templates.
func (r *WebRequestCommitStatusReconciler) evaluateResponseExpression(_ context.Context, expression string, resp httpResponse) (map[string]any, error) {
	// Get compiled expression from cache or compile it
	program, err := r.getCompiledResponseExpression(expression)
	if err != nil {
		return nil, fmt.Errorf("failed to compile response expression: %w", err)
	}

	// Build environment for expression evaluation (same as validation expression)
	env := map[string]any{
		"Response": map[string]any{
			"StatusCode": resp.StatusCode,
			"Body":       resp.Body,
			"Headers":    resp.Headers,
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

// getCompiledExpression returns a compiled expr program from the reconciler's cache, or compiles the expression and caches it.
// cacheKeyPrefix (e.g. "trigger:", "validation:", "response:") ensures the same expression string compiled with different
// options (e.g. expr.AsBool() for validation) gets distinct cache entries. opts are passed through to expr.Compile.
func (r *WebRequestCommitStatusReconciler) getCompiledExpression(expression string, cacheKeyPrefix string, opts ...expr.Option) (*vm.Program, error) {
	cacheKey := cacheKeyPrefix + expression

	if cached, ok := r.expressionCache.Load(cacheKey); ok {
		program, ok := cached.(*vm.Program)
		if !ok {
			return nil, errors.New("cached value is not a *vm.Program")
		}
		return program, nil
	}

	program, err := expr.Compile(expression, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to compile expression: %w", err)
	}

	r.expressionCache.Store(cacheKey, program)
	return program, nil
}

// getCompiledTriggerExpression returns a cached or newly compiled trigger expression program.
// Used by evaluateTriggerExpression. Compiled without a result type constraint (expression may return bool or a map with "trigger" and other fields).
func (r *WebRequestCommitStatusReconciler) getCompiledTriggerExpression(expression string) (*vm.Program, error) {
	return r.getCompiledExpression(expression, "trigger:")
}

// getCompiledValidationExpression returns a cached or newly compiled validation expression program.
// Used by evaluateValidationExpression. Compiled with expr.AsBool() so the expression must return a boolean.
func (r *WebRequestCommitStatusReconciler) getCompiledValidationExpression(expression string) (*vm.Program, error) {
	return r.getCompiledExpression(expression, "validation:", expr.AsBool())
}

// getCompiledResponseExpression returns a cached or newly compiled response expression program.
// Used by evaluateResponseExpression. Compiled without a result type constraint; the expression is expected to return a map for ResponseData.
func (r *WebRequestCommitStatusReconciler) getCompiledResponseExpression(expression string) (*vm.Program, error) {
	return r.getCompiledExpression(expression, "response:")
}

// upsertCommitStatus creates or updates the CommitStatus resource that reports this WebRequestCommitStatus's result to the SCM.
// The phase (Success or Pending) and sha are set from the validation outcome; description and URL are rendered from templateData.
// The created resource is owned by the WebRequestCommitStatus so it is cleaned up when the WebRequestCommitStatus is deleted.
func (r *WebRequestCommitStatusReconciler) upsertCommitStatus(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, ps *promoterv1alpha1.PromotionStrategy, branch, sha string, phase promoterv1alpha1.CommitStatusPhase, templateData templateData) (*promoterv1alpha1.CommitStatus, error) {
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
