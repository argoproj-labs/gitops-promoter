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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"text/template"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	corev1 "k8s.io/api/core/v1"
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

// TemplateData represents the data available to Go templates in URL, Headers, and Body.
type TemplateData struct {
	ProposedHydratedSha string
	ActiveHydratedSha   string
	Key                 string
	Name                string
	Namespace           string
	Labels              map[string]string
	Annotations         map[string]string
}

// WebRequestCommitStatusReconciler reconciles a WebRequestCommitStatus object
type WebRequestCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	SettingsMgr *settings.Manager
	HTTPClient  *http.Client

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

	// Process each environment and make HTTP requests
	transitionedEnvironments, commitStatuses, needsPolling, err := r.processEnvironments(ctx, &wrcs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to process environments: %w", err)
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
		err = touchChangeTransferPolicies(ctx, r.Client, &ps, transitionedEnvironments, "web request validation transition")
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to touch ChangeTransferPolicies: %w", err)
		}
	}

	// Determine requeue strategy based on reportOn and polling needs
	return r.calculateRequeue(&wrcs, needsPolling), nil
}

// calculateRequeue determines when to requeue based on reportOn mode and polling needs
func (r *WebRequestCommitStatusReconciler) calculateRequeue(wrcs *promoterv1alpha1.WebRequestCommitStatus, needsPolling bool) ctrl.Result {
	// If reportOn: active, always requeue at PollingInterval
	if wrcs.Spec.ReportOn == ReportOnActive {
		return ctrl.Result{RequeueAfter: wrcs.Spec.PollingInterval.Duration}
	}

	// If reportOn: proposed, only requeue if any environment needs polling
	if needsPolling {
		return ctrl.Result{RequeueAfter: wrcs.Spec.PollingInterval.Duration}
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

// checkKeyInSelectors checks if a key exists in a list of commit status selectors
func checkKeyInSelectors(key string, selectors []promoterv1alpha1.CommitStatusSelector) bool {
	for _, selector := range selectors {
		if selector.Key == key {
			return true
		}
	}
	return false
}

// appliesToEnvironment checks if the WebRequestCommitStatus applies to the given environment
func (r *WebRequestCommitStatusReconciler) appliesToEnvironment(wrcs *promoterv1alpha1.WebRequestCommitStatus, ps *promoterv1alpha1.PromotionStrategy, psEnv promoterv1alpha1.Environment) bool {
	// Check global commit statuses
	if checkKeyInSelectors(wrcs.Spec.Key, ps.Spec.ProposedCommitStatuses) {
		return true
	}
	if checkKeyInSelectors(wrcs.Spec.Key, ps.Spec.ActiveCommitStatuses) {
		return true
	}
	// Check environment-specific commit statuses
	if checkKeyInSelectors(wrcs.Spec.Key, psEnv.ProposedCommitStatuses) {
		return true
	}
	if checkKeyInSelectors(wrcs.Spec.Key, psEnv.ActiveCommitStatuses) {
		return true
	}
	return false
}

// findPreviousEnvStatus finds the previous status for a given environment branch
func findPreviousEnvStatus(previousStatus *promoterv1alpha1.WebRequestCommitStatusStatus, branch string) *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus {
	for i := range previousStatus.Environments {
		if previousStatus.Environments[i].Environment == branch {
			return &previousStatus.Environments[i]
		}
	}
	return nil
}

// shouldSkipRequest checks if we should skip the HTTP request for this environment
func shouldSkipRequest(reportOn string, previousEnvStatus *promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus, reportedSha string) bool {
	if reportOn != ReportOnProposed || previousEnvStatus == nil {
		return false
	}
	return previousEnvStatus.Phase == WebRequestPhaseSuccess &&
		previousEnvStatus.ReportedSha == reportedSha &&
		previousEnvStatus.LastSuccessfulSha == reportedSha
}

// processEnvironments processes each environment from the PromotionStrategy,
// making HTTP requests and evaluating expressions for matching environments.
// Returns transitioned environments, commit statuses, whether polling is needed, and any error.
func (r *WebRequestCommitStatusReconciler) processEnvironments(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, ps *promoterv1alpha1.PromotionStrategy) ([]string, []*promoterv1alpha1.CommitStatus, bool, error) {
	logger := log.FromContext(ctx)

	transitionedEnvironments := []string{}
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(ps.Spec.Environments))
	needsPolling := false

	// Save the previous status before clearing it
	previousStatus := wrcs.Status.DeepCopy()
	if previousStatus == nil {
		previousStatus = &promoterv1alpha1.WebRequestCommitStatusStatus{}
	}

	// Build a map of environments from PromotionStrategy for efficient lookup
	envStatusMap := make(map[string]*promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for i := range ps.Status.Environments {
		envStatusMap[ps.Status.Environments[i].Branch] = &ps.Status.Environments[i]
	}

	// Initialize environments status
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
		envStatus, found := envStatusMap[branch]
		if !found {
			logger.Info("Environment not found in PromotionStrategy status", "branch", branch)
			continue
		}

		// Process this environment
		result, err := r.processSingleEnvironment(ctx, wrcs, ps, branch, envStatus, previousStatus)
		if err != nil {
			return nil, nil, false, err
		}

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
	commitStatus *promoterv1alpha1.CommitStatus
	transitioned bool
	needsPolling bool
}

// processSingleEnvironment processes a single environment and returns the result
func (r *WebRequestCommitStatusReconciler) processSingleEnvironment(
	ctx context.Context,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	branch string,
	envStatus *promoterv1alpha1.EnvironmentStatus,
	previousStatus *promoterv1alpha1.WebRequestCommitStatusStatus,
) (*environmentProcessResult, error) {
	logger := log.FromContext(ctx)
	result := &environmentProcessResult{}

	// Get the proposed and active hydrated SHAs
	proposedSha := envStatus.Proposed.Hydrated.Sha
	activeSha := envStatus.Active.Hydrated.Sha

	// Determine which SHA to report on
	reportOn := wrcs.Spec.ReportOn
	if reportOn == "" {
		reportOn = ReportOnProposed
	}

	reportedSha := proposedSha
	if reportOn == ReportOnActive {
		reportedSha = activeSha
	}

	// Find previous status for this environment
	previousEnvStatus := findPreviousEnvStatus(previousStatus, branch)

	// Check if we should skip the HTTP request (for reportOn: proposed, already succeeded for this SHA)
	if shouldSkipRequest(reportOn, previousEnvStatus, reportedSha) {
		// Already succeeded for this SHA, keep the status and don't make HTTP request
		wrcs.Status.Environments = append(wrcs.Status.Environments, *previousEnvStatus)
		// Create CommitStatus with existing success
		cs, err := r.upsertCommitStatus(ctx, wrcs, ps, branch, reportedSha, WebRequestPhaseSuccess, wrcs.Spec.Key)
		if err != nil {
			return nil, fmt.Errorf("failed to upsert CommitStatus for environment %q: %w", branch, err)
		}
		result.commitStatus = cs
		return result, nil
	}

	// Validate we have the SHA to report on
	if reportedSha == "" {
		logger.V(4).Info("Reported SHA not yet available", "branch", branch, "reportOn", reportOn)
		wrcs.Status.Environments = append(wrcs.Status.Environments, promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus{
			Environment:         branch,
			ProposedHydratedSha: proposedSha,
			ActiveHydratedSha:   activeSha,
			ReportedSha:         "",
			Phase:               WebRequestPhasePending,
			ExpressionMessage:   fmt.Sprintf("Waiting for %s commit SHA", reportOn),
		})
		result.needsPolling = true
		return result, nil
	}

	// Make HTTP request and evaluate expression
	envResult := r.processEnvironmentRequest(ctx, wrcs, branch, proposedSha, activeSha, reportedSha)
	wrcs.Status.Environments = append(wrcs.Status.Environments, envResult)

	// Check if this validation transitioned to success
	var previousPhase string
	if previousEnvStatus != nil {
		previousPhase = previousEnvStatus.Phase
	}
	if previousPhase != WebRequestPhaseSuccess && envResult.Phase == WebRequestPhaseSuccess {
		result.transitioned = true
		logger.Info("Validation transitioned to success",
			"branch", branch,
			"sha", reportedSha)
	}

	// Update LastSuccessfulSha if successful
	lastIdx := len(wrcs.Status.Environments) - 1
	if envResult.Phase == WebRequestPhaseSuccess {
		wrcs.Status.Environments[lastIdx].LastSuccessfulSha = reportedSha
	} else if previousEnvStatus != nil {
		// Preserve previous LastSuccessfulSha
		wrcs.Status.Environments[lastIdx].LastSuccessfulSha = previousEnvStatus.LastSuccessfulSha
	}

	// Determine if this environment needs polling
	if envResult.Phase != WebRequestPhaseSuccess || reportOn == ReportOnActive {
		result.needsPolling = true
	}

	// Create or update the CommitStatus
	cs, err := r.upsertCommitStatus(ctx, wrcs, ps, branch, reportedSha, envResult.Phase, wrcs.Spec.Key)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert CommitStatus for environment %q: %w", branch, err)
	}
	result.commitStatus = cs

	logger.Info("Processed environment web request",
		"branch", branch,
		"reportedSha", reportedSha,
		"phase", envResult.Phase,
		"statusCode", envResult.ResponseStatusCode)

	return result, nil
}

// processEnvironmentRequest makes the HTTP request and evaluates the expression for a single environment
func (r *WebRequestCommitStatusReconciler) processEnvironmentRequest(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, branch, proposedSha, activeSha, reportedSha string) promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus {
	logger := log.FromContext(ctx)
	now := metav1.Now()

	result := promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus{
		Environment:         branch,
		ProposedHydratedSha: proposedSha,
		ActiveHydratedSha:   activeSha,
		ReportedSha:         reportedSha,
		LastRequestTime:     &now,
	}

	// Prepare template data
	templateData := TemplateData{
		ProposedHydratedSha: proposedSha,
		ActiveHydratedSha:   activeSha,
		Key:                 wrcs.Spec.Key,
		Name:                wrcs.Name,
		Namespace:           wrcs.Namespace,
		Labels:              wrcs.Labels,
		Annotations:         wrcs.Annotations,
	}

	// Render URL template
	url, err := r.renderTemplate("url", wrcs.Spec.HTTPRequest.URL, templateData)
	if err != nil {
		result.Phase = WebRequestPhaseFailure
		result.Error = fmt.Sprintf("Failed to render URL template: %v", err)
		result.ExpressionMessage = result.Error
		return result
	}

	// Render body template
	body, err := r.renderTemplate("body", wrcs.Spec.HTTPRequest.Body, templateData)
	if err != nil {
		result.Phase = WebRequestPhaseFailure
		result.Error = fmt.Sprintf("Failed to render body template: %v", err)
		result.ExpressionMessage = result.Error
		return result
	}

	// Create HTTP request
	var reqBody io.Reader
	if body != "" {
		reqBody = bytes.NewBufferString(body)
	}

	req, err := http.NewRequestWithContext(ctx, wrcs.Spec.HTTPRequest.Method, url, reqBody)
	if err != nil {
		result.Phase = WebRequestPhasePending
		result.Error = fmt.Sprintf("Failed to create HTTP request: %v", err)
		result.ExpressionMessage = result.Error
		return result
	}

	// Render and add headers
	for key, value := range wrcs.Spec.HTTPRequest.Headers {
		renderedValue, err := r.renderTemplate("header-"+key, value, templateData)
		if err != nil {
			result.Phase = WebRequestPhaseFailure
			result.Error = fmt.Sprintf("Failed to render header %q template: %v", key, err)
			result.ExpressionMessage = result.Error
			return result
		}
		req.Header.Set(key, renderedValue)
	}

	// Add authentication
	if wrcs.Spec.HTTPRequest.AuthSecretRef != nil {
		if err := r.addAuthentication(ctx, req, wrcs); err != nil {
			result.Phase = WebRequestPhasePending
			result.Error = fmt.Sprintf("Failed to add authentication: %v", err)
			result.ExpressionMessage = result.Error
			return result
		}
	}

	// Set timeout from spec if provided
	timeout := 30 * time.Second
	if wrcs.Spec.HTTPRequest.Timeout.Duration > 0 {
		timeout = wrcs.Spec.HTTPRequest.Timeout.Duration
	}

	// Create a client with the specific timeout for this request
	client := &http.Client{Timeout: timeout}

	// Make the HTTP request
	resp, err := client.Do(req)
	if err != nil {
		logger.Error(err, "HTTP request failed", "url", url)
		result.Phase = WebRequestPhasePending
		result.Error = fmt.Sprintf("HTTP request failed: %v", err)
		result.ExpressionMessage = result.Error
		return result
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	result.ResponseStatusCode = ptr.To(resp.StatusCode)

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		result.Phase = WebRequestPhasePending
		result.Error = fmt.Sprintf("Failed to read response body: %v", err)
		result.ExpressionMessage = result.Error
		return result
	}

	// Parse response body as JSON if possible
	var bodyData any
	if err := json.Unmarshal(respBody, &bodyData); err != nil {
		// If not JSON, use raw string
		bodyData = string(respBody)
	}

	// Build response data for expression
	responseData := ResponseData{
		StatusCode: resp.StatusCode,
		Body:       bodyData,
		Headers:    resp.Header,
	}

	// Evaluate expression
	phase, message, exprResult := r.evaluateExpression(ctx, wrcs.Spec.Expression, responseData)
	result.Phase = phase
	result.ExpressionMessage = message
	result.ExpressionResult = exprResult

	return result
}

// renderTemplate renders a Go template with the given data
func (r *WebRequestCommitStatusReconciler) renderTemplate(name, tmplStr string, data TemplateData) (string, error) {
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

// addAuthentication adds authentication to the HTTP request based on the AuthSecretRef
func (r *WebRequestCommitStatusReconciler) addAuthentication(ctx context.Context, req *http.Request, wrcs *promoterv1alpha1.WebRequestCommitStatus) error {
	authRef := wrcs.Spec.HTTPRequest.AuthSecretRef
	if authRef == nil || authRef.Type == "none" {
		return nil
	}

	// Fetch the secret
	var secret corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: wrcs.Namespace, Name: authRef.Name}, &secret); err != nil {
		return fmt.Errorf("failed to get auth secret %q: %w", authRef.Name, err)
	}

	switch authRef.Type {
	case "basic":
		username := string(secret.Data["username"])
		password := string(secret.Data["password"])
		if username == "" || password == "" {
			return errors.New("basic auth secret must contain 'username' and 'password' keys")
		}
		auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
		req.Header.Set("Authorization", "Basic "+auth)

	case "bearer":
		token := string(secret.Data["token"])
		if token == "" {
			return errors.New("bearer auth secret must contain 'token' key")
		}
		req.Header.Set("Authorization", "Bearer "+token)

	case "header":
		for key, value := range secret.Data {
			req.Header.Set(key, string(value))
		}

	default:
		return fmt.Errorf("unknown auth type: %s", authRef.Type)
	}

	return nil
}

// evaluateExpression evaluates the expr expression against the response data
func (r *WebRequestCommitStatusReconciler) evaluateExpression(ctx context.Context, expression string, response ResponseData) (string, string, *bool) {
	logger := log.FromContext(ctx)

	// Try to get cached compiled expression
	var program *vm.Program
	if cached, ok := r.expressionCache.Load(expression); ok {
		program, ok = cached.(*vm.Program)
		if !ok {
			return WebRequestPhaseFailure, "Invalid cached expression program", nil
		}
	} else {
		// Compile the expression
		env := map[string]any{"Response": response}
		compiled, err := expr.Compile(expression, expr.Env(env), expr.AsBool())
		if err != nil {
			logger.Error(err, "Failed to compile expression", "expression", expression)
			return WebRequestPhaseFailure, fmt.Sprintf("Expression compilation failed: %v", err), nil
		}
		program = compiled
		r.expressionCache.Store(expression, program)
	}

	// Run the expression
	env := map[string]any{"Response": response}
	output, err := expr.Run(program, env)
	if err != nil {
		logger.Error(err, "Failed to evaluate expression", "expression", expression)
		return WebRequestPhasePending, fmt.Sprintf("Expression evaluation failed: %v", err), nil
	}

	// Check if output is boolean
	result, ok := output.(bool)
	if !ok {
		return WebRequestPhaseFailure, fmt.Sprintf("Expression must return boolean, got %T", output), nil
	}

	if result {
		return WebRequestPhaseSuccess, "Expression evaluated to true", ptr.To(true)
	}
	return WebRequestPhasePending, "Expression evaluated to false", ptr.To(false)
}

// upsertCommitStatus creates or updates a CommitStatus for the given environment
func (r *WebRequestCommitStatusReconciler) upsertCommitStatus(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, ps *promoterv1alpha1.PromotionStrategy, branch, sha, phase, key string) (*promoterv1alpha1.CommitStatus, error) {
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

	commitStatus := promoterv1alpha1.CommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      commitStatusName,
			Namespace: wrcs.Namespace,
		},
	}

	// Create or update the CommitStatus
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, &commitStatus, func() error {
		// Set owner reference to the WebRequestCommitStatus
		if err := ctrl.SetControllerReference(wrcs, &commitStatus, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference: %w", err)
		}

		// Set labels for easy identification
		if commitStatus.Labels == nil {
			commitStatus.Labels = make(map[string]string)
		}
		commitStatus.Labels["promoter.argoproj.io/web-request-commit-status"] = utils.KubeSafeLabel(wrcs.Name)
		commitStatus.Labels[promoterv1alpha1.EnvironmentLabel] = utils.KubeSafeLabel(branch)
		commitStatus.Labels[promoterv1alpha1.CommitStatusLabel] = key

		// Set the spec
		commitStatus.Spec.RepositoryReference = ps.Spec.RepositoryReference
		commitStatus.Spec.Name = key
		commitStatus.Spec.Description = wrcs.Spec.Description
		commitStatus.Spec.Phase = commitPhase
		commitStatus.Spec.Sha = sha

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create or update CommitStatus: %w", err)
	}

	return &commitStatus, nil
}
