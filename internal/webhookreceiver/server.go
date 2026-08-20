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

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	controllerruntime "sigs.k8s.io/controller-runtime/pkg/manager"
)

var logger = ctrl.Log.WithName("webhookReceiver")

// Provider type constants. Values are tied to utils.RepoKey's provider scoping,
// so they alias the utils constants rather than duplicating the literals.
const (
	ProviderGitHub         = utils.ProviderGitHub
	ProviderGitLab         = utils.ProviderGitLab
	ProviderForgejo        = utils.ProviderForgejo
	ProviderGitea          = utils.ProviderGitea
	ProviderBitbucketCloud = utils.ProviderBitbucketCloud
	ProviderAzureDevops    = utils.ProviderAzureDevOps
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

	// maxPendingWRCSFanout bounds concurrent in-flight WRCS fan-out goroutines so a
	// burst of authorized webhook traffic cannot spawn unbounded concurrent List calls.
	maxPendingWRCSFanout = 2048

	// maxWebhookBodyBytes caps the inbound webhook body size read in postRoot, so an
	// unauthenticated large-body request cannot force unbounded memory/HMAC/JSON-decode
	// work before any rejection.
	maxWebhookBodyBytes = 5 << 20 // 5 MiB

	unauthorizedMessage = "unauthorized"
)

// EnqueueFunc is a function type that can be used to enqueue reconcile requests
// without modifying the object. This matches controller.CTPEnqueueFunc / WRCSEnqueueFunc.
type EnqueueFunc func(namespace, name string)

// WebhookReceiver is a server that listens for webhooks and triggers reconciles of
// ChangeTransferPolicies (by hydrated SHA) and WebRequestCommitStatuses (by repository).
type WebhookReceiver struct {
	k8sClient           client.Client
	baseCtx             context.Context //nolint:containedctx // server-lifetime context set once in Start, not a request context
	mgr                 controllerruntime.Manager
	enqueueCTP          EnqueueFunc
	enqueueWRCS         EnqueueFunc
	controllerNamespace string
	retryTimeout        time.Duration
	retryBaseDelay      time.Duration
	retryMaxDelay       time.Duration
	retryFactor         float64
	maxPendingRetries   int
	pendingMissRetries  atomic.Int64
	pendingWRCSFanouts  atomic.Int64
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
		bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes+1))
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

	jsonBytes, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes+1))
	if err != nil {
		responseCode = http.StatusInternalServerError
		http.Error(w, "error reading body", responseCode)
		return
	}
	if len(jsonBytes) > maxWebhookBodyBytes {
		responseCode = http.StatusRequestEntityTooLarge
		http.Error(w, "request body too large", responseCode)
		return
	}

	ctx := log.IntoContext(r.Context(), reqLogger)

	owner, name := parseWebhookRepo(provider, jsonBytes)
	beforeSha, ref := parseWebhookPush(provider, jsonBytes)

	var ctp *promoterv1alpha1.ChangeTransferPolicy
	var ctpOutcome ctpLookupOutcome
	var ctpLookupErr error
	if beforeSha != "" {
		ctp, ctpOutcome, ctpLookupErr = wr.lookupCTPByHydratedSHA(ctx, beforeSha, ref)
		if ctpOutcome == ctpLookupListError && ctpLookupErr != nil {
			reqLogger.V(4).Info("transient CTP lookup failure during webhook auth", "error", ctpLookupErr)
		}
	}

	if status, msg := wr.verifyInboundWebhook(ctx, provider, owner, name, r.Header, jsonBytes, ctp); status != 0 {
		responseCode = status
		http.Error(w, msg, responseCode)
		return
	}

	if owner != "" && name != "" {
		wr.startWRCSFanout(ctx, provider, owner, name, jsonBytes)
	}

	if beforeSha == "" {
		reqLogger.V(4).Info("unable to extract commit SHA from provider payload", "provider", provider)
		responseCode = http.StatusNoContent
		w.WriteHeader(responseCode)
		return
	}

	switch ctpOutcome {
	case ctpLookupFound:
		if ctp == nil {
			reqLogger.Error(errors.New("CTP lookup reported found but returned nil"), "giving up on webhook delivery")
			break
		}
		if wr.enqueueCTP != nil {
			wr.enqueueCTP(ctp.Namespace, ctp.Name)
		}
		ctpFound = true
		reqLogger.Info("Triggered reconcile of ChangeTransferPolicy via webhook", "namespace", ctp.Namespace, "name", ctp.Name)
	case ctpLookupNotFound, ctpLookupListError:
		reqLogger.Info("no ChangeTransferPolicy matched webhook delivery; scheduling miss retry")
		//nolint:contextcheck // the retry must outlive the HTTP request, so it inherits the server-lifetime context instead
		wr.scheduleMissRetry(provider, beforeSha, ref, deliveryID, r.Header, jsonBytes)
	case ctpLookupTooManyMatches:
		reqLogger.Error(fmt.Errorf("too many changetransferpolicies found for sha: %s, ref: %s", beforeSha, ref), "giving up on webhook delivery")
	default:
		if ctpLookupErr != nil {
			reqLogger.Error(ctpLookupErr, "giving up on webhook delivery")
		}
	}

	responseCode = http.StatusNoContent
	w.WriteHeader(responseCode)
}

// startWRCSFanout fans out to WebRequestCommitStatus asynchronously so List/filter work does not delay
// the HTTP response (SCM providers retry on slow acknowledgements).
func (wr *WebhookReceiver) startWRCSFanout(ctx context.Context, provider, owner, name string, body []byte) {
	if wr.enqueueWRCS == nil {
		return
	}
	logger := log.FromContext(ctx)
	if wr.pendingWRCSFanouts.Add(1) > int64(maxPendingWRCSFanout) {
		wr.pendingWRCSFanouts.Add(-1)
		logger.V(4).Info("skipping WRCS webhook fan-out; at capacity")
		return
	}
	bodyCopy := bytes.Clone(body)
	//nolint:contextcheck // fan-out must outlive the HTTP request; inherits server-lifetime context
	go func() {
		defer wr.pendingWRCSFanouts.Add(-1)
		fanoutCtx, cancel := context.WithTimeout(wr.getBaseContext(), wrcsFanoutTimeout)
		defer cancel()
		wr.enqueueWRCSForRepo(fanoutCtx, provider, owner, name, "webhook", bodyCopy)
	}()
}

// scheduleMissRetry starts an async retry of the CTP lookup, bounded by the pending-retry
// capacity. The retry context inherits from baseCtx (not the HTTP request), so it outlives
// the handler but is cancelled on shutdown.
func (wr *WebhookReceiver) scheduleMissRetry(provider, sha, ref, deliveryID string, headers http.Header, body []byte) {
	if wr.pendingMissRetries.Add(1) > int64(wr.getMaxPendingRetries()) {
		wr.pendingMissRetries.Add(-1)
		logger.V(4).Info("skipping webhook miss retry; at capacity", "deliveryID", deliveryID)
		return
	}
	metrics.IncWebhookMissRetryPending()
	headersCopy := cloneHeader(headers)
	bodyCopy := bytes.Clone(body)
	go func() {
		defer func() {
			wr.pendingMissRetries.Add(-1)
			metrics.DecWebhookMissRetryPending()
		}()
		retryCtx, cancel := context.WithTimeout(wr.getBaseContext(), wr.getRetryTimeout())
		defer cancel()
		wr.retryFindAndEnqueue(retryCtx, provider, sha, ref, deliveryID, headersCopy, bodyCopy)
	}()
}

func (wr *WebhookReceiver) getBaseContext() context.Context {
	if wr.baseCtx != nil {
		return wr.baseCtx
	}
	return context.Background()
}

func (wr *WebhookReceiver) retryFindAndEnqueue(ctx context.Context, provider, sha, ref, deliveryID string, headers http.Header, body []byte) {
	reqLogger := logger.WithValues("provider", provider, "deliveryID", deliveryID, "sha", sha, "ref", ref)
	ctx = log.IntoContext(ctx, reqLogger)

	backoff := wait.Backoff{
		Duration: wr.getRetryBaseDelay(),
		Factor:   wr.getRetryFactor(),
		Cap:      wr.getRetryMaxDelay(),
		Steps:    math.MaxInt32,
	}

	err := wait.ExponentialBackoffWithContext(ctx, backoff, func(ctx context.Context) (bool, error) {
		ctp, outcome, lookupErr := wr.lookupCTPByHydratedSHA(ctx, sha, ref)
		switch outcome {
		case ctpLookupFound:
			if ctp == nil {
				return false, errors.New("CTP lookup reported found but returned nil")
			}
			owner, name := parseWebhookRepo(provider, body)
			if status, msg := wr.verifyInboundWebhook(ctx, provider, owner, name, headers, body, ctp); status != 0 {
				return false, fmt.Errorf("webhook verification failed after CTP match (status %d): %s", status, msg)
			}
			if wr.enqueueCTP != nil {
				wr.enqueueCTP(ctp.Namespace, ctp.Name)
			}
			reqLogger.Info("Triggered reconcile of ChangeTransferPolicy via deferred webhook retry",
				"namespace", ctp.Namespace, "name", ctp.Name)
			return true, nil
		case ctpLookupNotFound:
			return false, nil
		case ctpLookupListError:
			reqLogger.V(4).Info("transient CTP lookup failure during deferred webhook retry", "error", lookupErr)
			return false, nil
		case ctpLookupTooManyMatches:
			return false, fmt.Errorf("too many changetransferpolicies found for sha: %s, ref: %s", sha, ref)
		default:
			return false, fmt.Errorf("unexpected CTP lookup outcome: %v", outcome)
		}
	})
	if err != nil {
		if wait.Interrupted(err) {
			// Expected when no CTP appears before the miss-retry timeout.
			reqLogger.V(4).Info("deferred webhook miss retry exhausted without a match", "error", err)
		} else {
			// Terminal stop (e.g. ambiguous SHA match, verification failure) — surface at Error so it is not filtered.
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

// enqueueWRCSForRepo fans out webhook deliveries to WebRequestCommitStatus resources
// whose PromotionStrategy references a GitRepository matching owner/name. List failures
// are logged and ignored so they never affect the HTTP response or the CTP path.
// When a WRCS has mode.webhook.filter set, the filter expression is evaluated against
// Payload (decoded JSON body) and non-matching payloads are skipped.
func (wr *WebhookReceiver) enqueueWRCSForRepo(ctx context.Context, provider, owner, name, via string, payload []byte) {
	if wr.enqueueWRCS == nil || owner == "" || name == "" {
		return
	}
	logger := log.FromContext(ctx)
	repoKey := utils.RepoKey(provider, owner, name)

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
