package webhookreceiver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/webhookreceiver/webhooks"

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

	// Extract and log delivery ID
	deliveryID := wr.extractDeliveryID(r)
	logger := logger.WithValues("deliveryID", deliveryID)

	// Try to parse the webhook using each provider's library
	provider, beforeSha, ref, err := wr.parseWebhook(r)
	if err != nil {
		logger.V(4).Info("unable to parse webhook", "error", err)
		responseCode = http.StatusBadRequest
		http.Error(w, fmt.Sprintf("unable to parse webhook: %v", err), responseCode)
		return
	}

	logger = logger.WithValues("provider", provider, "beforeSha", beforeSha, "ref", ref)

	if beforeSha == "" {
		logger.V(4).Info("unable to extract commit SHA from webhook payload")
		responseCode = http.StatusNoContent
		w.WriteHeader(responseCode)
		return
	}

	ctp, err := wr.findChangeTransferPolicy(r.Context(), beforeSha, ref)
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

// parseWebhook attempts to parse the incoming webhook using the webhooks library.
// Returns the provider name, beforeSha, ref, and any error.
func (wr *WebhookReceiver) parseWebhook(r *http.Request) (provider string, beforeSha string, ref string, err error) {
	result, err := webhooks.ParseAny(r)
	if err != nil {
		return "", "", "", err
	}

	// TODO: Add secret validation here if needed
	// Example: result.ValidateSecret(secret)

	// Currently only handle push events
	if result.Event.Type == webhooks.EventTypePush && result.Event.Push != nil {
		return result.Event.Push.Provider, result.Event.Push.Before, result.Event.Push.Ref, nil
	}

	return "", "", "", errors.New("unsupported event type")
}

func (wr *WebhookReceiver) findChangeTransferPolicy(ctx context.Context, beforeSha, ref string) (*promoterv1alpha1.ChangeTransferPolicy, error) {
	ctpLists := promoterv1alpha1.ChangeTransferPolicyList{}

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
