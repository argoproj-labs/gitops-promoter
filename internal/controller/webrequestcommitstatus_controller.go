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
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/internal/utils/httpauth"
	"github.com/argoproj-labs/gitops-promoter/internal/webrequest"
)

// WebRequestCommitStatusReconciler reconciles WebRequestCommitStatus resources by running HTTP requests
// per environment, evaluating trigger/validation/response expressions, and upserting CommitStatus resources
// so the SCM (e.g. GitHub) shows success or pending based on the validation result.
type WebRequestCommitStatusReconciler struct {
	client.Client
	Recorder    events.EventRecorder
	Scheme      *runtime.Scheme
	SettingsMgr *settings.Manager
	EnqueueCTP  CTPEnqueueFunc
	httpClient  *http.Client
	// rvTracker remembers the ResourceVersion of the last successful status patch this
	// reconciler made for each object key, so Reconcile can short-circuit (with a brief
	// requeue) when the informer cache hasn't yet observed our previous write. Without
	// this guard, a reconcile enqueued by a side effect of the previous reconcile —
	// e.g. touching a ChangeTransferPolicy that fans back via the PromotionStrategy
	// watch — can start within milliseconds of the prior patch and read pre-patch
	// state, causing trigger expressions to re-fire HTTP side effects against stale
	// inputs.
	rvTracker *utils.ResourceVersionTracker
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=webrequestcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=webrequestcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=webrequestcommitstatuses/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch

// Reconcile fetches the WebRequestCommitStatus and its PromotionStrategy, processes each applicable
// environment (evaluating trigger and optionally making the HTTP request and validation), upserts
// CommitStatus resources, cleans up orphaned CommitStatuses, and enqueues ChangeTransferPolicies when
// an environment transitions to success. Result status and requeue time are updated via the deferred handler.
func (r *WebRequestCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling WebRequestCommitStatus")
	startTime := time.Now()

	var wrcs promoterv1alpha1.WebRequestCommitStatus
	// skipStatusWrite is set on the stale-cache requeue path below to suppress the
	// deferred status apply, because writing status from the stale snapshot would
	// clobber whatever the previous reconcile wrote that the cache hasn't yet
	// observed (SSA with our FieldOwner and ForceOwnership would revert our owned
	// fields to the stale values).
	skipStatusWrite := false
	var previousReady *metav1.Condition
	// This function applies the resource status via Server-Side Apply at the end of the reconciliation. Don't write status manually.
	defer func() {
		if skipStatusWrite {
			return
		}
		utils.HandleReconciliationResult(ctx, startTime, &wrcs, r.Client, r.Recorder, constants.WebRequestCommitStatusControllerFieldOwner, &result, &err, &previousReady)
		// Record the post-patch ResourceVersion so the next reconcile for this key can
		// detect a stale-cache read above. HandleReconciliationResult mutates wrcs in
		// place with the patched object on success, so wrcs.ResourceVersion here is
		// the apiserver's post-patch value (or unchanged if the SSA patch was a
		// no-op). Record() is a no-op for empty RV, which covers the
		// NotFound-during-patch path.
		r.rvTracker.Record(req.NamespacedName, wrcs.ResourceVersion)
	}()

	// 1. Fetch the WebRequestCommitStatus instance
	err = r.Get(ctx, req.NamespacedName, &wrcs)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Object is gone. Drop any rvTracker entry so we don't accumulate
			// records for deleted objects over the controller's lifetime. Safe
			// against name reuse: a future object reusing this key will get a
			// fresh, larger RV from the apiserver, so a leftover record would
			// not have caused incorrect staleness anyway — Forget just keeps
			// memory tidy.
			r.rvTracker.Forget(req.NamespacedName)
			logger.Info("WebRequestCommitStatus not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get WebRequestCommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get WebRequestCommitStatus %q: %w", req.Name, err)
	}

	// If the cached object we just read is older than our last successful status write
	// for this key, the informer hasn't observed our previous patch yet. Acting on the
	// stale snapshot would re-evaluate trigger expressions against pre-patch state and
	// can fire duplicate HTTP side effects (see field doc on rvTracker). Requeue with
	// a short backoff so the next reconcile gets a fresh snapshot, and skip the
	// deferred status apply so we don't clobber the previous reconcile's write.
	if r.rvTracker.IsCacheStale(req.NamespacedName, wrcs.ResourceVersion) {
		logger.V(4).Info("informer cache is behind our last status write; requeuing to avoid acting on stale state",
			"cachedResourceVersion", wrcs.ResourceVersion)
		skipStatusWrite = true
		return ctrl.Result{RequeueAfter: 100 * time.Millisecond}, nil
	}

	// Remove any existing Ready condition. We want to start fresh.
	previousReady = utils.RemoveReadyCondition(&wrcs)

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

	// 4. Dispatch based on context mode
	wr := webrequest.NewReconciler(r, wrcsCommitUpserter{r: r})
	var (
		commitStatuses           []*promoterv1alpha1.CommitStatus
		transitionedEnvironments []string
		requeueAfter             time.Duration
	)
	// Snapshot the per-branch phases persisted by the previous reconcile before the webrequest
	// reconcile overwrites wrcs.Status, so phase-change events stay transition-only.
	previousPhases := wrcsPhasesByBranch(&wrcs.Status)
	if wrcs.Spec.Mode.Context == promoterv1alpha1.ContextPromotionStrategy {
		commitStatuses, transitionedEnvironments, requeueAfter, err = wr.ReconcileWebRequestCommitStatusPromotionStrategy(ctx, &wrcs, &ps, namespaceMeta)
	} else {
		commitStatuses, transitionedEnvironments, requeueAfter, err = wr.ReconcileWebRequestCommitStatusEnvironments(ctx, &wrcs, &ps, namespaceMeta)
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to process WebRequestCommitStatus: %w", err)
	}
	for branch, phase := range wrcsPhasesByBranch(&wrcs.Status) {
		emitCommitStatusPhaseChangedEvent(r.Recorder, &wrcs, wrcs.Spec.Key, branch, previousPhases[branch], phase)
	}

	// 5. Clean up orphaned CommitStatus resources that are no longer in the environment list
	err = utils.CleanupOrphanedCommitStatuses(ctx, r.Client, r.Recorder, &wrcs, commitStatuses)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to cleanup orphaned CommitStatus resources: %w", err)
	}

	// 6. Inherit conditions from CommitStatus objects
	utils.InheritNotReadyConditionFromObjects(&wrcs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	// 7. If any validations transitioned to success, enqueue the corresponding ChangeTransferPolicies to trigger reconciliation
	utils.EnqueueChangeTransferPolicies(ctx, r.EnqueueCTP, &ps, transitionedEnvironments, "validation transition")

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

	// Initialize the per-key ResourceVersion tracker used to detect stale-cache reads
	// at the top of Reconcile. See the rvTracker field doc for context.
	if r.rvTracker == nil {
		r.rvTracker = utils.NewResourceVersionTracker()
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

// wrcsCommitUpserter implements webrequest.CommitStatusEmitter using SSA upsert.
type wrcsCommitUpserter struct {
	r *WebRequestCommitStatusReconciler
}

func (e wrcsCommitUpserter) EmitCommitStatus(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, repositoryRefName, branch, sha string, phase promoterv1alpha1.CommitStatusPhase, td webrequest.TemplateData) (*promoterv1alpha1.CommitStatus, error) {
	return e.r.upsertCommitStatus(ctx, wrcs, repositoryRefName, branch, sha, phase, td)
}

// Execute implements webrequest.HTTPEXecutor by performing the real HTTP round-trip.
func (r *WebRequestCommitStatusReconciler) Execute(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, td webrequest.TemplateData) (webrequest.HTTPResponse, error) {
	resp, err := r.makeHTTPRequest(ctx, wrcs, td)
	if err != nil {
		// Edge-triggered rather than transition-gated: repeated identical failures are
		// aggregated server-side into an EventSeries, and this event is what keeps HTTP
		// failures visible now that ReconciliationError events only fire on transitions.
		r.Recorder.Eventf(wrcs, nil, "Warning", constants.WebRequestFailedReason, "MakingHTTPRequest", constants.WebRequestFailedMessage, wrcs.Spec.Key, err)
		return webrequest.HTTPResponse{}, fmt.Errorf("failed to make HTTP request: %w", err)
	}
	return resp, nil
}

// wrcsPhasesByBranch flattens the per-branch phases out of a WebRequestCommitStatus status,
// regardless of which context mode produced it (Environments or PromotionStrategyContext).
func wrcsPhasesByBranch(status *promoterv1alpha1.WebRequestCommitStatusStatus) map[string]string {
	phases := make(map[string]string)
	for _, env := range status.Environments {
		phases[env.Branch] = string(env.Phase)
	}
	if status.PromotionStrategyContext != nil {
		for _, item := range status.PromotionStrategyContext.PhasePerBranch {
			phases[item.Branch] = string(item.Phase)
		}
	}
	return phases
}

// makeHTTPRequest builds and executes the HTTP request from the WebRequestCommitStatus spec. It renders
// URL, body, and headers from TemplateData, applies authentication (basic, bearer, OAuth2, or TLS), uses the
// configured timeout, and parses the response body as JSON or plain text. The returned HTTPResponse is used
// for validation and response expression evaluation.
// When Scm is configured, the rendered URL host is validated against the SCM provider's allowed
// domains before the request is made, to prevent SCM credentials leaking to unintended hosts.
func (r *WebRequestCommitStatusReconciler) makeHTTPRequest(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, templateData webrequest.TemplateData) (webrequest.HTTPResponse, error) {
	logger := log.FromContext(ctx)

	rendered, err := webrequest.BuildRenderedHTTPRequestFromTemplates(wrcs, templateData)
	if err != nil {
		return webrequest.HTTPResponse{}, fmt.Errorf("failed to render HTTP request templates: %w", err)
	}

	// When Scm is configured, credentials are sourced directly from the SCM provider, so the
	// URL host must belong to that provider's allowed domains to prevent credential leakage.
	if wrcs.Spec.HTTPRequest.Authentication != nil && wrcs.Spec.HTTPRequest.Authentication.Scm != nil {
		if err := r.validateURLHostAgainstScmProvider(ctx, wrcs, rendered.URL); err != nil {
			return webrequest.HTTPResponse{}, fmt.Errorf("SCM host validation failed: %w", err)
		}
	}

	var body io.Reader
	if wrcs.Spec.HTTPRequest.BodyTemplate != "" {
		body = strings.NewReader(rendered.Body)
	}

	req, err := http.NewRequestWithContext(ctx, rendered.Method, rendered.URL, body)
	if err != nil {
		return webrequest.HTTPResponse{}, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	for headerName, headerValue := range rendered.Headers {
		req.Header.Set(headerName, headerValue)
	}

	// Use shared default client unless authentication returns a per-request client (e.g. TLS).
	// Never assign to r.httpClient here: concurrent reconciliations share the reconciler, and
	// overwriting r.httpClient would create a data race and wrong client usage across goroutines.
	clientToUse := r.httpClient
	if wrcs.Spec.HTTPRequest.Authentication != nil {
		authClient, err := r.applyAuthentication(ctx, wrcs, req)
		if err != nil {
			return webrequest.HTTPResponse{}, fmt.Errorf("failed to apply authentication: %w", err)
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

	logger.V(4).Info("Making HTTP request", "method", rendered.Method, "url", rendered.URL)

	// Execute request (metrics: counter and histogram only after Do; duration is Do through body read).
	httpMetricsStart := time.Now()
	resp, err := clientToUse.Do(req)
	if err != nil {
		return webrequest.HTTPResponse{}, fmt.Errorf("HTTP request failed: %w", err)
	}
	if resp == nil {
		return webrequest.HTTPResponse{}, errors.New("HTTP response is nil")
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			logger.V(4).Info("Failed to close response body", "error", closeErr)
		}
	}()

	// Read response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return webrequest.HTTPResponse{}, fmt.Errorf("failed to read response body: %w", err)
	}
	httpRequestMetricsDuration := time.Since(httpMetricsStart)
	metrics.RecordWebRequestCommitStatusHTTPRequest(wrcs, resp.StatusCode, httpRequestMetricsDuration)

	// Try to parse body as JSON
	var parsedBody any
	if err := json.Unmarshal(bodyBytes, &parsedBody); err != nil {
		// Not JSON, use raw string
		parsedBody = string(bodyBytes)
	}

	response := webrequest.HTTPResponse{
		StatusCode: resp.StatusCode,
		Body:       parsedBody,
		Headers:    resp.Header,
	}

	logger.V(4).Info("HTTP request completed", "statusCode", resp.StatusCode, "latency", httpRequestMetricsDuration)

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
func (r *WebRequestCommitStatusReconciler) upsertCommitStatus(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, repositoryRefName string, branch, sha string, phase promoterv1alpha1.CommitStatusPhase, templateData webrequest.TemplateData) (*promoterv1alpha1.CommitStatus, error) {
	kind := reflect.TypeFor[promoterv1alpha1.WebRequestCommitStatus]().Name()
	commitStatusName := utils.CommitStatusResourceName(ctx, wrcs, branch)

	// Render description template
	var description string
	if wrcs.Spec.DescriptionTemplate != "" {
		rendered, err := utils.RenderStringTemplate(wrcs.Spec.DescriptionTemplate, templateData)
		if err != nil {
			return nil, fmt.Errorf("failed to render description template: %w", err)
		}
		description = rendered
	}

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
		WithLabels(utils.CommitStatusStandardLabels(wrcs, branch, wrcs.Spec.Key)).
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

// enqueueWebRequestCommitStatusForPromotionStrategy returns the watch handler for PromotionStrategy. When a
// PromotionStrategy is created/updated (e.g. environment SHAs or status change), it enqueues every
// WebRequestCommitStatus in the same namespace that references that strategy, so they reconcile with fresh data.
func (r *WebRequestCommitStatusReconciler) enqueueWebRequestCommitStatusForPromotionStrategy() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
		if !ok {
			return nil
		}
		return utils.EnqueueCommitStatusGatesForPromotionStrategy(
			ctx, r.Client, ps,
			&promoterv1alpha1.WebRequestCommitStatusList{},
			func(wrcs *promoterv1alpha1.WebRequestCommitStatus) string {
				return wrcs.Spec.PromotionStrategyRef.Name
			},
		)
	})
}

// getNamespaceMetadata fetches the namespace's labels and annotations for use in templateData, so URL, header,
// body, and description templates can reference them. Called at the start of Reconcile for the
// WebRequestCommitStatus's namespace.
func (r *WebRequestCommitStatusReconciler) getNamespaceMetadata(ctx context.Context, namespace string) (webrequest.NamespaceMetadata, error) {
	var ns corev1.Namespace
	if err := r.Get(ctx, client.ObjectKey{Name: namespace}, &ns); err != nil {
		return webrequest.NamespaceMetadata{}, fmt.Errorf("failed to get namespace %q: %w", namespace, err)
	}

	return webrequest.NamespaceMetadata{
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
