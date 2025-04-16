package webhookreceiver

import (
	"bytes"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
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
			maxPayloadSize uint32
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
				wr := NewWebhookReceiver(
					k8sManager,
					WithMaxPayloadSize(tt.maxPayloadSize),
				)

				Expect(tt.maxPayloadSize).To(Equal(wr.maxWebhookPayloadSizeBytes))

				body := bytes.Repeat([]byte("a"), tt.payloadSize)
				req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
				resp := httptest.NewRecorder()

				wr.postRoot(resp, req)
				Expect(tt.wantStatus).To(Equal(resp.Code))
			})
		}
	})
})
