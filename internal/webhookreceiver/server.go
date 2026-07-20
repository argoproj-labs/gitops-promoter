package webhookreceiver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"

	"github.com/tidwall/gjson"

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

// Miss-retry defaults for async field-index lookups after an initial webhook miss.
// TODO: consider making these configurable via ControllerConfiguration.
const (
	missRetryTimeout      = 15 * time.Second
	missRetryBaseDelay    = 100 * time.Millisecond
	missRetryMaxDelay     = 2 * time.Second
	missRetryFactor       = 2.0
	maxPendingMissRetries = 256
)

// EnqueueFunc is a function type that can be used to enqueue CTP reconcile requests
// without modifying the CTP object. This matches controller.CTPEnqueueFunc.
type EnqueueFunc func(namespace, name string)

// WebhookReceiver is a server that listens for webhooks and triggers reconciles of ChangeTransferPolicies.
type WebhookReceiver struct {
	mgr          controllerruntime.Manager
	k8sClient    client.Client
	enqueueCTP   EnqueueFunc
	shutdown     <-chan struct{} // closed when Start's context is cancelled
	missRetrySem chan struct{}

	// Optional overrides for tests (zero values use the package constants).
	retryTimeout   time.Duration
	retryBaseDelay time.Duration
	retryMaxDelay  time.Duration
	retryFactor    float64
}

// NewWebhookReceiver creates a new instance of WebhookReceiver.
func NewWebhookReceiver(mgr controllerruntime.Manager, enqueueCTP EnqueueFunc) WebhookReceiver {
	return WebhookReceiver{
		mgr:          mgr,
		k8sClient:    mgr.GetClient(),
		enqueueCTP:   enqueueCTP,
		missRetrySem: make(chan struct{}, maxPendingMissRetries),
	}
}

// Start starts the webhook receiver server on the given address.
func (wr *WebhookReceiver) Start(ctx context.Context, addr string) error {
	wr.shutdown = ctx.Done()

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
	var updateDuration time.Duration

	// Record the webhook call metrics. // We use a deferred function to ensure that the metrics are recorded even if an error occurs.
	// We also subtract the update duration from the total time to get a more accurate measurement of how long actual
	// processing took.
	defer func() {
		metrics.RecordWebhookCall(ctpFound, responseCode, time.Since(startTime)-updateDuration)
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
	beforeSha, ref := parseWebhookPush(provider, jsonBytes)
	if beforeSha == "" {
		reqLogger.V(4).Info("unable to extract commit SHA from provider payload", "provider", provider)
		responseCode = http.StatusNoContent
		w.WriteHeader(responseCode)
		return
	}

	ctp, outcome, lookupErr := wr.lookupCTPByHydratedSHA(ctx, beforeSha, ref)
	switch outcome {
	case ctpLookupFound:
		if ctp == nil {
			reqLogger.V(4).Info("CTP lookup reported found but returned nil")
			break
		}
		ctpFound = true
		startUpdate := time.Now()
		if wr.enqueueCTP != nil {
			wr.enqueueCTP(ctp.Namespace, ctp.Name)
		}
		updateDuration = time.Since(startUpdate)
		reqLogger.Info("Triggered reconcile of ChangeTransferPolicy via webhook", "namespace", ctp.Namespace, "name", ctp.Name)
	case ctpLookupTooManyMatches:
		reqLogger.V(4).Info("too many ChangeTransferPolicies matched hydrated sha for webhook delivery", "sha", beforeSha, "ref", ref)
	case ctpLookupNotFound:
		reqLogger.Info("no ChangeTransferPolicy found for webhook delivery")
		// Detach from the request context so the async retry outlives the HTTP handler.
		wr.scheduleMissRetry(context.WithoutCancel(ctx), provider, beforeSha, ref, deliveryID)
	case ctpLookupListError:
		reqLogger.V(4).Info("transient CTP lookup failure; scheduling miss retry", "error", lookupErr)
		wr.scheduleMissRetry(context.WithoutCancel(ctx), provider, beforeSha, ref, deliveryID)
	default:
		reqLogger.V(4).Info("unexpected CTP lookup outcome", "outcome", outcome)
	}

	responseCode = http.StatusNoContent
	w.WriteHeader(responseCode)
}

func (wr *WebhookReceiver) scheduleMissRetry(ctx context.Context, provider, sha, ref, deliveryID string) {
	if wr.missRetrySem == nil {
		return
	}

	select {
	case wr.missRetrySem <- struct{}{}:
		metrics.IncWebhookMissRetryPending()
		go func() {
			defer func() {
				<-wr.missRetrySem
				metrics.DecWebhookMissRetryPending()
			}()
			retryCtx, cancel := context.WithTimeout(ctx, wr.getRetryTimeout())
			defer cancel()
			if wr.shutdown != nil {
				go func() {
					select {
					case <-wr.shutdown:
						cancel()
					case <-retryCtx.Done():
					}
				}()
			}
			wr.retryFindAndEnqueue(retryCtx, provider, sha, ref, deliveryID)
		}()
	default:
		logger.V(4).Info("skipping webhook miss retry; at capacity", "deliveryID", deliveryID)
	}
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
		ctp, outcome, lookupErr := wr.lookupCTPByHydratedSHA(ctx, sha, ref)
		switch outcome {
		case ctpLookupFound:
			if ctp == nil {
				return false, errors.New("CTP lookup reported found but returned nil")
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
			// Transient API/index failures: keep retrying until the outer timeout.
			reqLogger.V(4).Info("transient CTP lookup failure; will retry", "error", lookupErr)
			return false, nil
		case ctpLookupTooManyMatches:
			return false, fmt.Errorf("too many changetransferpolicies found for sha: %s, ref: %s", sha, ref)
		default:
			return false, fmt.Errorf("unexpected CTP lookup outcome: %v", outcome)
		}
	})
	if err != nil {
		if wait.Interrupted(err) {
			reqLogger.V(4).Info("deferred webhook miss retry exhausted without a match", "error", err)
		} else {
			reqLogger.V(4).Info("deferred webhook miss retry stopped", "error", err)
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
