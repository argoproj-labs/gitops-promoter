package webhookreceiver

import (
	"context"
	"errors"
	"fmt"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"io"
	"net/http"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/tidwall/gjson"

	"k8s.io/apimachinery/pkg/fields"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerruntime "sigs.k8s.io/controller-runtime/pkg/manager"
)

var logger = ctrl.Log.WithName("webhookReceiver")

type webhookReceiver struct {
	mgr         controllerruntime.Manager
	k8sClient   client.Client
	settingsMgr *settings.Manager
}

func NewWebhookReceiver(mgr controllerruntime.Manager, settingsMgr *settings.Manager) webhookReceiver {
	wr := webhookReceiver{
		mgr:         mgr,
		k8sClient:   mgr.GetClient(),
		settingsMgr: settingsMgr,
	}

	return wr
}

func (wr *webhookReceiver) Start(ctx context.Context, addr string) error {
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

func (wr *webhookReceiver) postRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "must be a POST request", http.StatusMethodNotAllowed)
		return
	}
	maxWebhookPayloadSize, err := wr.settingsMgr.GetWebhookMaxPayloadSize(r.Context())
	if err != nil {
		logger.Error(err, "failed to get webhook max payload size from settings manager")
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if !maxWebhookPayloadSize.IsZero() {
		r.Body = http.MaxBytesReader(w, r.Body, maxWebhookPayloadSize.Value())
	}
	jsonBytes, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			http.Error(w, "payload size exceeds limit", http.StatusRequestEntityTooLarge)
			return
		}

		http.Error(w, "error reading body", http.StatusInternalServerError)
		return
	}

	ctp, err := wr.findChangeTransferPolicy(r.Context(), jsonBytes)
	if err != nil {
		logger.V(4).Info("could not find any matching ChangeTransferPolicies", "error", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if ctp == nil {
		return
	}

	if ctp.Annotations == nil {
		ctp.Annotations = make(map[string]string)
	}
	ctp.Annotations[promoterv1alpha1.ReconcileAtAnnotation] = time.Now().Format(time.RFC3339)
	err = wr.k8sClient.Update(r.Context(), ctp)
	if err != nil {
		logger.Error(err, fmt.Sprintf("failed to update ChangeTransferPolicy annotations '%s/%s' from webhook", ctp.Namespace, ctp.Name))
		http.Error(w, "could not cause reconcile of ChangeTransferPolicy", http.StatusInternalServerError)
	}
	logger.Info("Triggered reconcile via webhook", "ChangeTransferPolicy", ctp.Namespace+"/"+ctp.Name)

	w.WriteHeader(http.StatusNoContent)
}

func (wr *webhookReceiver) findChangeTransferPolicy(ctx context.Context, jsonBytes []byte) (*promoterv1alpha1.ChangeTransferPolicy, error) {
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
		return nil, errors.New("payload did not match expected format")
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
