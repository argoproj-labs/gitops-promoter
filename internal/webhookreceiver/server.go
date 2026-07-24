package webhookreceiver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"

	"github.com/tidwall/gjson"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	controllerruntime "sigs.k8s.io/controller-runtime/pkg/manager"
)

var logger = ctrl.Log.WithName("webhookReceiver")

// Provider type constants
const (
	ProviderGitHub         = "github"
	ProviderGitLab         = "gitlab"
	ProviderForgejo        = "forgejo"
	ProviderGitea          = "gitea"
	ProviderBitbucketCloud = "bitbucketCloud"
	ProviderAzureDevops    = "azureDevOps"
	ProviderUnknown        = ""
)

// Field index path literals duplicated here to avoid an import cycle with internal/controller
// (suite_test.go imports webhookreceiver). Keep in sync with controller.GitRepositoryRepoKeyField,
// controller.GitRepositoryRefField, and controller.PromotionStrategyRefField.
const (
	gitRepositoryRepoKeyField              = ".metadata.repoKey"
	promotionStrategyGitRepositoryRefField = ".spec.gitRepositoryRef.name"
	promotionStrategyRefField              = ".spec.promotionStrategyRef.name"
)

// Miss-retry defaults for async field-index lookups after an initial webhook miss.
// TODO: consider making these configurable via ControllerConfiguration.
const (
	missRetryTimeout      = 15 * time.Second
	missRetryBaseDelay    = 100 * time.Millisecond
	missRetryMaxDelay     = 2 * time.Second
	missRetryFactor       = 2.0
	maxPendingMissRetries = 256

	// wrcsFanoutTimeout bounds async WRCS fan-out work after the HTTP handler returns.
	wrcsFanoutTimeout = 15 * time.Second
)

// EnqueueFunc is a function type that can be used to enqueue reconcile requests
// without modifying the object. This matches controller.CTPEnqueueFunc / WRCSEnqueueFunc.
type EnqueueFunc func(namespace, name string)

// WebhookReceiver is a server that listens for webhooks and triggers reconciles of
// ChangeTransferPolicies (by hydrated SHA) and WebRequestCommitStatuses (by repository).
type WebhookReceiver struct {
	mgr                 controllerruntime.Manager
	k8sClient           client.Client
	baseCtx             context.Context //nolint:containedctx // server-lifetime context set once in Start, not a request context
	enqueueCTP          EnqueueFunc
	enqueueWRCS         EnqueueFunc
	controllerNamespace string
	pendingMissRetries  atomic.Int64
	retryTimeout        time.Duration
	retryBaseDelay      time.Duration
	retryMaxDelay       time.Duration
	retryFactor         float64
	maxPendingRetries   int
}

// NewWebhookReceiver creates a new instance of WebhookReceiver.
// enqueueWRCS may be nil when WRCS fan-out is not needed (tests that only cover the CTP path).
// controllerNamespace is used to resolve ClusterScmProvider Secrets for webhook verification.
func NewWebhookReceiver(mgr controllerruntime.Manager, enqueueCTP, enqueueWRCS EnqueueFunc, controllerNamespace string) *WebhookReceiver {
	return &WebhookReceiver{
		mgr:                 mgr,
		k8sClient:           mgr.GetClient(),
		enqueueCTP:          enqueueCTP,
		enqueueWRCS:         enqueueWRCS,
		controllerNamespace: controllerNamespace,
	}
}

// Start starts the webhook receiver server on the given address.
func (wr *WebhookReceiver) Start(ctx context.Context, addr string) error {
	wr.baseCtx = ctx

	mux := http.NewServeMux()
	mux.HandleFunc("/", wr.postRoot)

	server := http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			logger.Info("webhook receiver server closed")
		} else if err != nil {
			logger.Error(err, "error listening for server")
		}
	}()
	logger.Info("webhook receiver server started")

	<-ctx.Done()
	logger.Info("webhook receiver server stopped")

	if err := server.Shutdown(ctx); err != nil {
		logger.Error(err, "webhook receiver server shutdown failed")
	}
	logger.Info("webhook receiver server exited properly")

	return nil
}

// DetectProvider determines the SCM provider based on webhook headers.
// Returns ProviderGitHub, ProviderGitLab, ProviderForgejo, ProviderGitea, ProviderBitbucketCloud, ProviderAzureDevops or ProviderUnknown.
func (wr *WebhookReceiver) DetectProvider(r *http.Request) string {
	// Check for GitHub webhook headers
	if r.Header.Get("X-Github-Event") != "" || r.Header.Get("X-Github-Delivery") != "" {
		return ProviderGitHub
	}

	// Check for GitLab webhook headers
	if r.Header.Get("X-Gitlab-Event") != "" || r.Header.Get("X-Gitlab-Token") != "" {
		return ProviderGitLab
	}

	// Check for Forgejo-specific headers first (Forgejo has its own headers)
	if r.Header.Get("X-Forgejo-Event") != "" {
		return ProviderForgejo
	}

	// Check for Gitea webhook headers (only if no Forgejo headers present)
	if r.Header.Get("X-Gitea-Event") != "" {
		return ProviderGitea
	}

	// Check for Bitbucket Cloud webhook headers
	if r.Header.Get("X-Hook-Uuid") != "" {
		return ProviderBitbucketCloud
	}

	if r.ContentLength > 0 {
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			logger.Error(err, "error reading request body for provider detection")
			return ProviderUnknown
		}
		// Restore the body for downstream handlers
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// Azure DevOps: check for both EventType and PublisherId
		if gjson.GetBytes(bodyBytes, "eventType").Exists() && gjson.GetBytes(bodyBytes, "publisherId").Exists() {
			return ProviderAzureDevops
		}
	}

	return ProviderUnknown
}

func (wr *WebhookReceiver) postRoot(w http.ResponseWriter, r *http.Request) {
	var responseCode int
	var ctpFound bool
	startTime := time.Now()

	// Record the webhook call metrics. We use a deferred function to ensure that the metrics are recorded even if an error occurs.
	defer func() {
		metrics.RecordWebhookCall(ctpFound, responseCode, time.Since(startTime))
	}()

	if r.Method != http.MethodPost {
		responseCode = http.StatusMethodNotAllowed
		http.Error(w, "must be a POST request", responseCode)
		return
	}

	// Determine provider from headers
	provider := wr.DetectProvider(r)

	// Extract and log a single delivery ID from common webhook headers (GitHub, GitLab, Forgejo/Gitea).
	deliveryID := wr.extractDeliveryID(r)
	reqLogger := logger.WithValues("provider", provider, "deliveryID", deliveryID)

	if provider == ProviderUnknown {
		reqLogger.V(4).Info("unable to detect provider from headers")
		responseCode = http.StatusBadRequest
		http.Error(w, "unable to detect SCM provider from headers", responseCode)
		return
	}

	// TODO: add a configurable payload max size for DoS protection.
	jsonBytes, err := io.ReadAll(r.Body)
	if err != nil {
		responseCode = http.StatusInternalServerError
		http.Error(w, "error reading body", responseCode)
		return
	}

	ctx := log.IntoContext(r.Context(), reqLogger)

	owner, name := parseWebhookRepo(provider, jsonBytes)
	if status, msg := wr.authorizeWebhookAndEnqueueWRCS(ctx, provider, owner, name, r.Header, jsonBytes); status != 0 {
		responseCode = status
		http.Error(w, msg, responseCode)
		return
	}

	beforeSha, ref := parseWebhookPush(provider, jsonBytes)
	if beforeSha == "" {
		reqLogger.V(4).Info("unable to extract commit SHA from provider payload", "provider", provider)
		responseCode = http.StatusNoContent
		w.WriteHeader(responseCode)
		return
	}

	found, retryable, lookupErr := wr.tryLookupAndEnqueue(ctx, beforeSha, ref, "webhook")
	switch {
	case found:
		ctpFound = true
	case retryable:
		reqLogger.Info("no ChangeTransferPolicy matched webhook delivery; scheduling miss retry")
		//nolint:contextcheck // the retry must outlive the HTTP request, so it inherits the server-lifetime context instead
		wr.scheduleMissRetry(provider, beforeSha, ref, deliveryID)
	default:
		// Terminal outcomes (ambiguous SHA match, unexpected lookup result) are config/logic
		// problems operators should see at default verbosity.
		reqLogger.Error(lookupErr, "giving up on webhook delivery")
	}

	responseCode = http.StatusNoContent
	w.WriteHeader(responseCode)
}

// authorizeWebhookAndEnqueueWRCS verifies the inbound delivery when webhook secrets are
// configured, then fans out to matching WRCS when repository identity is present.
// A non-zero status means the HTTP handler should reject immediately.
func (wr *WebhookReceiver) authorizeWebhookAndEnqueueWRCS(ctx context.Context, provider, owner, name string, headers http.Header, body []byte) (status int, msg string) {
	logger := log.FromContext(ctx)
	if owner == "" || name == "" {
		// Without repository identity we cannot select ScmProvider Secrets to verify against,
		// and cannot fan out to WRCS. If any webhookSecret is configured, fail closed so CTP
		// cannot be enqueued via an unverified delivery either.
		configured, err := wr.anyWebhookSecretConfigured(ctx)
		if err != nil {
			logger.Error(err, "failed to check for configured webhook secrets")
			return http.StatusInternalServerError, "error verifying webhook"
		}
		if configured {
			logger.V(4).Info("webhook lacks repository identity; rejecting because webhookSecret is configured")
			return http.StatusUnauthorized, "unauthorized"
		}
		return 0, ""
	}

	authorized, err := wr.verifyWebhookIfConfigured(ctx, provider, owner, name, headers, body)
	if err != nil {
		logger.Error(err, "failed to resolve webhook secrets for verification")
		return http.StatusInternalServerError, "error verifying webhook"
	}
	if !authorized {
		logger.V(4).Info("webhook signature verification failed")
		return http.StatusUnauthorized, "unauthorized"
	}

	// Fan out to WebRequestCommitStatus asynchronously so List/filter work does not delay
	// the HTTP response (SCM providers retry on slow acknowledgements). Failures are logged
	// inside enqueueWRCSForRepo and never affect the CTP path below.
	bodyCopy := append([]byte(nil), body...)
	//nolint:contextcheck // fan-out must outlive the HTTP request; inherits server-lifetime context
	go func() {
		fanoutCtx, cancel := context.WithTimeout(wr.getBaseContext(), wrcsFanoutTimeout)
		defer cancel()
		wr.enqueueWRCSForRepo(fanoutCtx, owner, name, "webhook", bodyCopy)
	}()
	return 0, ""
}

// scheduleMissRetry starts an async retry of the CTP lookup, bounded by the pending-retry
// capacity. The retry context inherits from baseCtx (not the HTTP request), so it outlives
// the handler but is cancelled on shutdown.
func (wr *WebhookReceiver) scheduleMissRetry(provider, sha, ref, deliveryID string) {
	if wr.pendingMissRetries.Add(1) > int64(wr.getMaxPendingRetries()) {
		wr.pendingMissRetries.Add(-1)
		logger.V(4).Info("skipping webhook miss retry; at capacity", "deliveryID", deliveryID)
		return
	}
	metrics.IncWebhookMissRetryPending()
	go func() {
		defer func() {
			wr.pendingMissRetries.Add(-1)
			metrics.DecWebhookMissRetryPending()
		}()
		retryCtx, cancel := context.WithTimeout(wr.getBaseContext(), wr.getRetryTimeout())
		defer cancel()
		wr.retryFindAndEnqueue(retryCtx, provider, sha, ref, deliveryID)
	}()
}

func (wr *WebhookReceiver) getBaseContext() context.Context {
	if wr.baseCtx != nil {
		return wr.baseCtx
	}
	return context.Background()
}

func (wr *WebhookReceiver) retryFindAndEnqueue(ctx context.Context, provider, sha, ref, deliveryID string) {
	reqLogger := logger.WithValues("provider", provider, "deliveryID", deliveryID, "sha", sha, "ref", ref)
	ctx = log.IntoContext(ctx, reqLogger)

	backoff := wait.Backoff{
		Duration: wr.getRetryBaseDelay(),
		Factor:   wr.getRetryFactor(),
		Cap:      wr.getRetryMaxDelay(),
		Steps:    math.MaxInt32,
	}

	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		// Retryable misses (no match yet, transient list failure) return (false, nil) to keep
		// backing off until the outer timeout; terminal outcomes return an error to stop.
		found, _, err := wr.tryLookupAndEnqueue(ctx, sha, ref, "deferred webhook retry")
		return found, err
	})
	if err != nil {
		if wait.Interrupted(err) {
			// Expected when no CTP appears before the miss-retry timeout.
			reqLogger.V(4).Info("deferred webhook miss retry exhausted without a match", "error", err)
		} else {
			// Terminal stop (e.g. ambiguous SHA match) — surface at Error so it is not filtered.
			reqLogger.Error(err, "deferred webhook miss retry stopped")
		}
	}
}

func (wr *WebhookReceiver) getRetryTimeout() time.Duration {
	if wr.retryTimeout > 0 {
		return wr.retryTimeout
	}
	return missRetryTimeout
}

func (wr *WebhookReceiver) getRetryBaseDelay() time.Duration {
	if wr.retryBaseDelay > 0 {
		return wr.retryBaseDelay
	}
	return missRetryBaseDelay
}

func (wr *WebhookReceiver) getRetryMaxDelay() time.Duration {
	if wr.retryMaxDelay > 0 {
		return wr.retryMaxDelay
	}
	return missRetryMaxDelay
}

func (wr *WebhookReceiver) getRetryFactor() float64 {
	if wr.retryFactor > 0 {
		return wr.retryFactor
	}
	return missRetryFactor
}

func (wr *WebhookReceiver) getMaxPendingRetries() int {
	if wr.maxPendingRetries > 0 {
		return wr.maxPendingRetries
	}
	return maxPendingMissRetries
}

// tryLookupAndEnqueue performs a single hydrated-SHA lookup and enqueues the matching
// ChangeTransferPolicy when exactly one is found. via distinguishes the synchronous webhook
// path from the deferred retry path in logs. found reports whether a CTP was enqueued;
// retryable reports whether a later attempt could still succeed (no match yet, or a
// transient list failure). err is non-nil only for terminal outcomes.
func (wr *WebhookReceiver) tryLookupAndEnqueue(ctx context.Context, sha, ref, via string) (found, retryable bool, err error) {
	logger := log.FromContext(ctx)
	ctp, outcome, lookupErr := wr.lookupCTPByHydratedSHA(ctx, sha, ref)
	switch outcome {
	case ctpLookupFound:
		if ctp == nil {
			// Defensive: lookupCTPByHydratedSHA should never return Found with a nil CTP.
			return false, false, errors.New("CTP lookup reported found but returned nil")
		}
		if wr.enqueueCTP != nil {
			wr.enqueueCTP(ctp.Namespace, ctp.Name)
		}
		logger.Info("Triggered reconcile of ChangeTransferPolicy via "+via, "namespace", ctp.Namespace, "name", ctp.Name)
		return true, false, nil
	case ctpLookupNotFound:
		return false, true, nil
	case ctpLookupListError:
		// Transient API/index failures: a retry may succeed.
		logger.V(4).Info("transient CTP lookup failure", "error", lookupErr)
		return false, true, nil
	case ctpLookupTooManyMatches:
		return false, false, fmt.Errorf("too many changetransferpolicies found for sha: %s, ref: %s", sha, ref)
	default:
		return false, false, fmt.Errorf("unexpected CTP lookup outcome: %v", outcome)
	}
}

// ctpLookupOutcome is the result of a hydrated-SHA field-index lookup.
type ctpLookupOutcome int

const (
	ctpLookupFound ctpLookupOutcome = iota
	ctpLookupNotFound
	ctpLookupTooManyMatches
	ctpLookupListError
)

// parseWebhookPush extracts the pre-push commit SHA and ref from a provider payload.
// Returns an empty beforeSha when the payload cannot be parsed for this provider.
func parseWebhookPush(provider string, jsonBytes []byte) (beforeSha, ref string) {
	switch provider {
	case ProviderGitHub, ProviderForgejo, ProviderGitea:
		// GitHub, Forgejo, and Gitea webhook format (all use 'pusher')
		if gjson.GetBytes(jsonBytes, "before").Exists() && gjson.GetBytes(jsonBytes, "pusher").Exists() {
			beforeSha = gjson.GetBytes(jsonBytes, "before").String()
			ref = gjson.GetBytes(jsonBytes, "ref").String()
		}
	case ProviderGitLab:
		// GitLab webhook format
		if gjson.GetBytes(jsonBytes, "before").Exists() && gjson.GetBytes(jsonBytes, "user_name").Exists() {
			beforeSha = gjson.GetBytes(jsonBytes, "before").String()
			ref = gjson.GetBytes(jsonBytes, "ref").String()
		}
	case ProviderBitbucketCloud:
		// Bitbucket Cloud webhook format
		if gjson.GetBytes(jsonBytes, "push.changes").Exists() && gjson.GetBytes(jsonBytes, "actor").Exists() {
			changes := gjson.GetBytes(jsonBytes, "push.changes")
			if changes.IsArray() && len(changes.Array()) > 0 {
				firstChange := changes.Array()[0]
				beforeSha = firstChange.Get("old.target.hash").String()
				if newName := firstChange.Get("new.name"); newName.Exists() {
					ref = "refs/heads/" + newName.String()
				} else if oldName := firstChange.Get("old.name"); oldName.Exists() {
					ref = "refs/heads/" + oldName.String()
				}
			}
		}
	case ProviderAzureDevops:
		// Azure DevOps webhook format
		if gjson.GetBytes(jsonBytes, "resource.refUpdates").Exists() {
			refUpdates := gjson.GetBytes(jsonBytes, "resource.refUpdates")
			if refUpdates.IsArray() && len(refUpdates.Array()) > 0 {
				firstUpdate := refUpdates.Array()[0]
				beforeSha = firstUpdate.Get("oldObjectId").String()
				ref = firstUpdate.Get("name").String()
			}
		}
	default:
		// Unsupported or unknown provider: leave beforeSha empty so the caller no-ops.
	}
	return beforeSha, ref
}

// parseWebhookRepo extracts repository owner and name from a provider webhook payload.
// Returns empty strings when the payload has no usable repository identity.
func parseWebhookRepo(provider string, jsonBytes []byte) (owner, name string) {
	switch provider {
	case ProviderGitHub, ProviderForgejo, ProviderGitea:
		owner = gjson.GetBytes(jsonBytes, "repository.owner.login").String()
		if owner == "" {
			owner = gjson.GetBytes(jsonBytes, "repository.owner.username").String()
		}
		name = gjson.GetBytes(jsonBytes, "repository.name").String()
		if owner == "" || name == "" {
			if fullName := gjson.GetBytes(jsonBytes, "repository.full_name").String(); fullName != "" {
				owner, name = splitOwnerName(fullName)
			}
		}
	case ProviderGitLab:
		if pathWithNS := gjson.GetBytes(jsonBytes, "project.path_with_namespace").String(); pathWithNS != "" {
			owner, name = splitOwnerName(pathWithNS)
		}
	case ProviderBitbucketCloud:
		if fullName := gjson.GetBytes(jsonBytes, "repository.full_name").String(); fullName != "" {
			owner, name = splitOwnerName(fullName)
		}
	case ProviderAzureDevops:
		owner = gjson.GetBytes(jsonBytes, "resource.repository.project.name").String()
		name = gjson.GetBytes(jsonBytes, "resource.repository.name").String()
	default:
		// Unsupported or unknown provider: leave owner/name empty so the caller skips fan-out.
	}
	return owner, name
}

// splitOwnerName splits "owner/name" or "group/subgroup/name" on the last '/'.
func splitOwnerName(fullName string) (owner, name string) {
	i := strings.LastIndex(fullName, "/")
	if i <= 0 || i == len(fullName)-1 {
		return "", ""
	}
	return fullName[:i], fullName[i+1:]
}

// anyWebhookSecretConfigured reports whether any ScmProvider or ClusterScmProvider Secret
// has webhookSecret set. Used to fail closed when a delivery lacks repository identity and
// therefore cannot be verified against the correct Secret.
func (wr *WebhookReceiver) anyWebhookSecretConfigured(ctx context.Context) (bool, error) {
	if wr.k8sClient == nil {
		return false, nil
	}

	var scmList promoterv1alpha1.ScmProviderList
	if listErr := wr.k8sClient.List(ctx, &scmList); listErr != nil {
		return false, fmt.Errorf("list ScmProviders for webhook secret check: %w", listErr)
	}
	for i := range scmList.Items {
		scm := &scmList.Items[i]
		if wr.secretRefHasWebhookSecret(ctx, scm.Spec.SecretRef, scm.Namespace) {
			return true, nil
		}
	}

	var cspList promoterv1alpha1.ClusterScmProviderList
	if listErr := wr.k8sClient.List(ctx, &cspList); listErr != nil {
		return false, fmt.Errorf("list ClusterScmProviders for webhook secret check: %w", listErr)
	}
	for i := range cspList.Items {
		csp := &cspList.Items[i]
		if wr.secretRefHasWebhookSecret(ctx, csp.Spec.SecretRef, wr.controllerNamespace) {
			return true, nil
		}
	}
	return false, nil
}

// secretRefHasWebhookSecret loads the Secret referenced by secretRef and reports whether it
// contains a non-empty webhookSecret. Unresolvable refs are treated as not configured.
func (wr *WebhookReceiver) secretRefHasWebhookSecret(ctx context.Context, secretRef *v1.LocalObjectReference, namespace string) bool {
	if secretRef == nil || secretRef.Name == "" || namespace == "" {
		return false
	}
	var secret v1.Secret
	if getErr := wr.k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: secretRef.Name}, &secret); getErr != nil {
		return false
	}
	_, _, ok := webhookSecretFromSecret(&secret, "")
	return ok
}

// verifyWebhookIfConfigured checks inbound webhook authenticity against ScmProvider Secrets
// for GitRepositories matching owner/name. If no matching Secret has webhookSecret configured,
// verification is skipped (backward compatible). If any such Secret exists, at least one must
// successfully verify the request.
func (wr *WebhookReceiver) verifyWebhookIfConfigured(ctx context.Context, provider, owner, name string, headers http.Header, body []byte) (authorized bool, err error) {
	if wr.k8sClient == nil || owner == "" || name == "" {
		return true, nil
	}
	logger := log.FromContext(ctx)
	repoKey := utils.RepoKey(owner, name)

	var gitRepos promoterv1alpha1.GitRepositoryList
	if listErr := wr.k8sClient.List(ctx, &gitRepos, client.MatchingFields{gitRepositoryRepoKeyField: repoKey}); listErr != nil {
		return false, fmt.Errorf("list GitRepositories for webhook verification: %w", listErr)
	}

	type candidate struct {
		header string
		secret []byte
	}
	var candidates []candidate
	seenSecrets := map[string]struct{}{}
	var resolutionErrors int

	for i := range gitRepos.Items {
		gr := &gitRepos.Items[i]
		if gr.Spec.ScmProviderRef.Name == "" {
			continue
		}
		_, secret, getErr := utils.GetScmProviderAndSecretFromGitRepository(ctx, wr.k8sClient, wr.controllerNamespace, gr)
		if getErr != nil {
			resolutionErrors++
			logger.V(4).Info("skipping GitRepository for webhook verification; could not resolve ScmProvider Secret",
				"namespace", gr.Namespace, "name", gr.Name, "error", getErr.Error())
			continue
		}
		secretBytes, headerName, ok := webhookSecretFromSecret(secret, provider)
		if !ok {
			continue
		}
		secretKey := secret.Namespace + "/" + secret.Name
		if _, seen := seenSecrets[secretKey]; seen {
			continue
		}
		seenSecrets[secretKey] = struct{}{}
		candidates = append(candidates, candidate{secret: secretBytes, header: headerName})
	}

	if len(candidates) == 0 {
		// Fail closed when any ScmProvider Secret resolution failed and no verification
		// candidates were built: a failed-to-resolve Secret might have had webhookSecret set.
		if resolutionErrors > 0 {
			return false, fmt.Errorf("could not resolve ScmProvider Secret for %d matching GitRepository(ies)", resolutionErrors)
		}
		return true, nil
	}

	for _, c := range candidates {
		headerValue := []byte(headers.Get(c.header))
		if verifyWebhookSignature(c.secret, headerValue, body) {
			return true, nil
		}
	}
	return false, nil
}

// enqueueWRCSForRepo fans out webhook deliveries to WebRequestCommitStatus resources
// whose PromotionStrategy references a GitRepository matching owner/name. List failures
// are logged and ignored so they never affect the HTTP response or the CTP path.
// When a WRCS has mode.webhook.filter set, the filter expression is evaluated against
// Payload (decoded JSON body) and non-matching payloads are skipped.
func (wr *WebhookReceiver) enqueueWRCSForRepo(ctx context.Context, owner, name, via string, payload []byte) {
	if wr.enqueueWRCS == nil || owner == "" || name == "" {
		return
	}
	logger := log.FromContext(ctx)
	repoKey := utils.RepoKey(owner, name)

	var gitRepos promoterv1alpha1.GitRepositoryList
	if err := wr.k8sClient.List(ctx, &gitRepos, client.MatchingFields{gitRepositoryRepoKeyField: repoKey}); err != nil {
		logger.Error(err, "failed to list GitRepositories for WRCS webhook fan-out", "repoKey", repoKey)
		return
	}

	var payloadObj map[string]any
	var payloadErr error
	payloadChecked := false

	for i := range gitRepos.Items {
		gr := &gitRepos.Items[i]
		var psList promoterv1alpha1.PromotionStrategyList
		if err := wr.k8sClient.List(ctx, &psList,
			client.InNamespace(gr.Namespace),
			client.MatchingFields{promotionStrategyGitRepositoryRefField: gr.Name},
		); err != nil {
			logger.Error(err, "failed to list PromotionStrategies for WRCS webhook fan-out",
				"namespace", gr.Namespace, "gitRepository", gr.Name)
			continue
		}

		for j := range psList.Items {
			ps := &psList.Items[j]
			var wrcsList promoterv1alpha1.WebRequestCommitStatusList
			if err := wr.k8sClient.List(ctx, &wrcsList,
				client.InNamespace(ps.Namespace),
				client.MatchingFields{promotionStrategyRefField: ps.Name},
			); err != nil {
				logger.Error(err, "failed to list WebRequestCommitStatuses for WRCS webhook fan-out",
					"namespace", ps.Namespace, "promotionStrategy", ps.Name)
				continue
			}

			for k := range wrcsList.Items {
				item := &wrcsList.Items[k]
				if item.Spec.Mode.Webhook != nil && item.Spec.Mode.Webhook.Filter != nil && item.Spec.Mode.Webhook.Filter.Expression != "" {
					if !payloadChecked {
						payloadErr = json.Unmarshal(payload, &payloadObj)
						payloadChecked = true
					}
					if payloadErr != nil {
						logger.Error(payloadErr, "failed to unmarshal webhook payload for WRCS filter; skipping filtered WRCS enqueue",
							"namespace", item.Namespace, "name", item.Name)
						continue
					}
					matched, filterErr := evaluateWebhookFilter(item.Spec.Mode.Webhook.Filter.Expression, payloadObj)
					if filterErr != nil {
						logger.Error(filterErr, "webhook filter evaluation failed; skipping WRCS enqueue",
							"namespace", item.Namespace, "name", item.Name)
						continue
					}
					if !matched {
						logger.V(4).Info("webhook filter did not match; skipping WRCS enqueue",
							"namespace", item.Namespace, "name", item.Name)
						continue
					}
				}
				wr.enqueueWRCS(item.Namespace, item.Name)
				logger.Info("Triggered reconcile of WebRequestCommitStatus via "+via,
					"namespace", item.Namespace, "name", item.Name)
			}
		}
	}
}

// lookupCTPByHydratedSHA finds a ChangeTransferPolicy by proposed (then active) hydrated SHA.
// Parse failures are handled by the caller; this only performs the k8s index lookup.
func (wr *WebhookReceiver) lookupCTPByHydratedSHA(ctx context.Context, sha, ref string) (*promoterv1alpha1.ChangeTransferPolicy, ctpLookupOutcome, error) {
	logger := log.FromContext(ctx)
	ctpLists := promoterv1alpha1.ChangeTransferPolicyList{}

	err := wr.k8sClient.List(ctx, &ctpLists, &client.ListOptions{
		FieldSelector: fields.SelectorFromSet(map[string]string{
			".status.proposed.hydrated.sha": sha,
		}),
	})
	if err != nil {
		return nil, ctpLookupListError, fmt.Errorf("failed to list via proposed sha changetransferpolicies for webhook receiver: %w", err)
	}

	if len(ctpLists.Items) == 0 {
		// List again, this time checking the active sha. This lets us catch cases where someone manually merged a PR in the SCM.
		err = wr.k8sClient.List(ctx, &ctpLists, &client.ListOptions{
			FieldSelector: fields.SelectorFromSet(map[string]string{
				".status.active.hydrated.sha": sha,
			}),
		})
		if err != nil {
			return nil, ctpLookupListError, fmt.Errorf("failed to list via active sha changetransferpolicies for webhook receiver: %w", err)
		}
	}

	switch len(ctpLists.Items) {
	case 0:
		logger.V(4).Info("no changetransferpolicies found from webhook receiver", "sha", sha, "ref", ref)
		return nil, ctpLookupNotFound, nil
	case 1:
		return &ctpLists.Items[0], ctpLookupFound, nil
	default:
		return nil, ctpLookupTooManyMatches, nil
	}
}

// extractDeliveryID inspects common webhook headers and returns the first non-empty delivery ID string found (provider-agnostic).
func (wr *WebhookReceiver) extractDeliveryID(r *http.Request) string {
	// Check common headers in a sensible order and return the first non-empty value.
	// GitHub
	if id := r.Header.Get("X-Github-Delivery"); id != "" {
		return id
	}
	// GitLab - prefer Event UUID, fall back to Delivery
	if id := r.Header.Get("X-Gitlab-Event-Uuid"); id != "" {
		return id
	}
	if id := r.Header.Get("X-Gitlab-Delivery"); id != "" {
		return id
	}
	// Forgejo/Gitea
	if id := r.Header.Get("X-Forgejo-Delivery"); id != "" {
		return id
	}
	if id := r.Header.Get("X-Gitea-Delivery"); id != "" {
		return id
	}
	// Azure DevOps
	if id := r.Header.Get("X-Vss-Activityid"); id != "" {
		return id
	}
	// Bitbucket Cloud
	// X-Request-UUID: Unique identifier for the webhook request
	// X-Hook-UUID: Unique identifier for the webhook itself (also used for provider detection)
	// Note: Go's http.Header.Get is case-insensitive, so this will match X-Request-UUID correctly
	if id := r.Header.Get("X-Request-Uuid"); id != "" {
		return id
	}
	if id := r.Header.Get("X-Hook-Uuid"); id != "" {
		return id
	}
	return ""
}
