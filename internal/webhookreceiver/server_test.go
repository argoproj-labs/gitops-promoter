package webhookreceiver

import (
	"bytes"
	"context"
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"net/http"
	"net/http/httptest"
	ctrl "sigs.k8s.io/controller-runtime"
	cr_client "sigs.k8s.io/controller-runtime/pkg/client"
	controllerruntime "sigs.k8s.io/controller-runtime/pkg/manager"
)

var _ = Describe("WebhookReceiver", func() {
	var k8sManager controllerruntime.Manager

	BeforeEach(func() {
		var err error
		k8sManager, err = ctrl.NewManager(&rest.Config{}, ctrl.Options{})
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("test max payload", func() {
		tests := []struct {
			name           string
			payloadSize    int
			maxPayloadSize int64
			wantStatus     int
		}{
			{
				name:           "with payload which exceeds max size",
				payloadSize:    2,
				maxPayloadSize: 1,
				wantStatus:     http.StatusRequestEntityTooLarge,
			},
			{
				name:           "with valid size but invalid payload",
				payloadSize:    1,
				maxPayloadSize: 1,
				wantStatus:     http.StatusInternalServerError,
			},
			{
				name:           "with max set to zero",
				payloadSize:    1,
				maxPayloadSize: 0,
				wantStatus:     http.StatusInternalServerError,
			},
		}

		for _, tt := range tests {
			It(tt.name, func() {
				// Patch the ControllerConfiguration to set the max payload size
				currentConfig := &promoterv1alpha1.ControllerConfiguration{}
				err := k8sClient.Get(context.Background(), types.NamespacedName{Name: settings.ControllerConfigurationName, Namespace: "default"}, currentConfig)
				Expect(err).NotTo(HaveOccurred())
				patch := cr_client.MergeFrom(currentConfig)
				controllerConfig := currentConfig.DeepCopy()
				controllerConfig.Spec.Webhook.MaxPayloadSize = resource.NewQuantity(tt.maxPayloadSize, resource.BinarySI)
				err = k8sClient.Patch(context.Background(), controllerConfig, patch)
				Expect(err).NotTo(HaveOccurred())

				wr := NewWebhookReceiver(
					k8sManager,
					settings.NewManager(k8sClient, settings.ManagerConfig{GlobalNamespace: "default"}),
				)

				body := bytes.Repeat([]byte("a"), tt.payloadSize)
				req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
				resp := httptest.NewRecorder()

				wr.postRoot(resp, req)
				Expect(tt.wantStatus).To(Equal(resp.Code))
			})
		}
	})
})
