package webhookreceiver

import (
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

// WebhookReceiver is a server that listens for webhooks and triggers reconciles of ChangeTransferPolicies.
type WebhookReceiver struct {
	mgr       controllerruntime.Manager
	k8sClient client.Client
}

// NewWebhookReceiver creates a new instance of WebhookReceiver.
func NewWebhookReceiver(mgr controllerruntime.Manager) WebhookReceiver {
	return WebhookReceiver{
		mgr:       mgr,
		k8sClient: mgr.GetClient(),
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
	// TODO: add a configurable payload max side for DoS protection.
	jsonBytes, err := io.ReadAll(r.Body)
	if err != nil {
		responseCode = http.StatusInternalServerError
		http.Error(w, "error reading body", responseCode)
		return
	}

	ctp, err := wr.findChangeTransferPolicy(r.Context(), jsonBytes)
	if err != nil {
		logger.V(4).Info("could not find any matching ChangeTransferPolicies", "error", err)
		responseCode = http.StatusNoContent
		w.WriteHeader(responseCode)
		return
	}
	if ctp == nil {
		responseCode = http.StatusNoContent
		w.WriteHeader(responseCode)
		return
	}

	ctpFound = true

	if ctp.Annotations == nil {
		ctp.Annotations = make(map[string]string)
	}
	ctp.Annotations[promoterv1alpha1.ReconcileAtAnnotation] = time.Now().Format(time.RFC3339)

	startUpdate := time.Now()
	err = wr.k8sClient.Update(r.Context(), ctp)
	updateDuration = time.Since(startUpdate)
	if err != nil {
		logger.Error(err, fmt.Sprintf("failed to update ChangeTransferPolicy annotations '%s/%s' from webhook", ctp.Namespace, ctp.Name))
		responseCode = http.StatusInternalServerError
		http.Error(w, "could not cause reconcile of ChangeTransferPolicy", responseCode)
	}
	logger.Info("Triggered reconcile of ChangeTransferPolicy via webhook", "namespace", ctp.Namespace, "name", ctp.Name)

	responseCode = http.StatusNoContent
	w.WriteHeader(responseCode)
}

func (wr *WebhookReceiver) findChangeTransferPolicy(ctx context.Context, jsonBytes []byte) (*promoterv1alpha1.ChangeTransferPolicy, error) {
	var beforeSha string
	var ref string
	ctpLists := promoterv1alpha1.ChangeTransferPolicyList{}

	// TODO: probably move to own function once we start adding providers because rules might be more complex
	if gjson.GetBytes(jsonBytes, "before").Exists() && gjson.GetBytes(jsonBytes, "pusher").Exists() {
		// Github
		beforeSha = gjson.GetBytes(jsonBytes, "before").String()
		ref = gjson.GetBytes(jsonBytes, "ref").String()
	}

	if beforeSha == "" {
		logger.V(4).Info("unable to match provider payload, might not be a pull request event or is malformed")
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
		return nil, fmt.Errorf("no changetransferpolicies found from webhook receiver sha: %s, ref: %s", beforeSha, ref)
	}
	if len(ctpLists.Items) > 1 {
		return nil, fmt.Errorf("too many changetranferpolicies found for sha: %s, ref: %s", beforeSha, ref)
	}

	return &ctpLists.Items[0], nil
}
