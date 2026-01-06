package webhookreceiver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"

	"github.com/tidwall/gjson"

	"k8s.io/apimachinery/pkg/fields"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// EnqueueFunc is a function type that can be used to enqueue CTP reconcile requests
// without modifying the CTP object. This matches controller.CTPEnqueueFunc.
type EnqueueFunc func(namespace, name string)

// WebhookReceiver is a server that listens for webhooks and triggers reconciles of ChangeTransferPolicies.
type WebhookReceiver struct {
	mgr        controllerruntime.Manager
	k8sClient  client.Client
	enqueueCTP EnqueueFunc
}

// NewWebhookReceiver creates a new instance of WebhookReceiver.
func NewWebhookReceiver(mgr controllerruntime.Manager, enqueueCTP EnqueueFunc) WebhookReceiver {
	return WebhookReceiver{
		mgr:        mgr,
		k8sClient:  mgr.GetClient(),
		enqueueCTP: enqueueCTP,
	}
}

// Start starts the webhook receiver server on the given address.
func (wr *WebhookReceiver) Start(ctx context.Context, addr string) error {
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
		logger.Error(err, "webhook receiver server shutdown failed", "error", err)
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
	logger := logger.WithValues("provider", provider, "deliveryID", deliveryID)

	if provider == ProviderUnknown {
		logger.V(4).Info("unable to detect provider from headers")
		responseCode = http.StatusBadRequest
		http.Error(w, "unable to detect SCM provider from headers", responseCode)
		return
	}

	// TODO: add a configurable payload max side for DoS protection.
	jsonBytes, err := io.ReadAll(r.Body)
	if err != nil {
		responseCode = http.StatusInternalServerError
		http.Error(w, "error reading body", responseCode)
		return
	}

	ctp, err := wr.findChangeTransferPolicy(r.Context(), provider, jsonBytes)
	if err != nil {
		logger.V(4).Info("could not find any matching ChangeTransferPolicies", "error", err)
		responseCode = http.StatusNoContent
		w.WriteHeader(responseCode)
		return
	}
	if ctp == nil {
		logger.Info("no ChangeTransferPolicy found for webhook delivery")
		responseCode = http.StatusNoContent
		w.WriteHeader(responseCode)
		return
	}

	ctpFound = true

	// Use the enqueue function to trigger reconciliation.
	startUpdate := time.Now()
	if wr.enqueueCTP != nil {
		wr.enqueueCTP(ctp.Namespace, ctp.Name)
	}
	updateDuration = time.Since(startUpdate)
	logger.Info("Triggered reconcile of ChangeTransferPolicy via webhook", "namespace", ctp.Namespace, "name", ctp.Name)

	responseCode = http.StatusNoContent
	w.WriteHeader(responseCode)
}

func (wr *WebhookReceiver) findChangeTransferPolicy(ctx context.Context, provider string, jsonBytes []byte) (*promoterv1alpha1.ChangeTransferPolicy, error) {
	var beforeSha string
	var ref string
	ctpLists := promoterv1alpha1.ChangeTransferPolicyList{}

	// Extract webhook data based on provider
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
		logger.V(4).Info("unsupported provider", "provider", provider)
		return nil, nil
	}

	if beforeSha == "" {
		logger.V(4).Info("unable to extract commit SHA from provider payload", "provider", provider)
		return nil, nil
	}

	err := wr.k8sClient.List(ctx, &ctpLists, &client.ListOptions{
		FieldSelector: fields.SelectorFromSet(map[string]string{
			".status.proposed.hydrated.sha": beforeSha,
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list changetransferpolicies for webhook receiver: %w", err)
	}

	if len(ctpLists.Items) == 0 {
		// List again, this time checking the active sha. This lets us catch cases where someone manually merged a PR in the SCM.
		err = wr.k8sClient.List(ctx, &ctpLists, &client.ListOptions{
			FieldSelector: fields.SelectorFromSet(map[string]string{
				".status.active.hydrated.sha": beforeSha,
			}),
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list changetransferpolicies for webhook receiver: %w", err)
		}
	}

	if len(ctpLists.Items) == 0 {
		return nil, fmt.Errorf("no changetransferpolicies found from webhook receiver sha: %s, ref: %s", beforeSha, ref)
	}
	if len(ctpLists.Items) > 1 {
		return nil, fmt.Errorf("too many changetranferpolicies found for sha: %s, ref: %s", beforeSha, ref)
	}

	return &ctpLists.Items[0], nil
}

// extractDeliveryID inspects common webhook headers and returns the first non-empty delivery ID string found (provider-agnostic).
func (wr *WebhookReceiver) extractDeliveryID(r *http.Request) string {
	// Check common headers in a sensible order and return the first non-empty value.
	// GitHub
	fmt.Println("received a webhook event")
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
