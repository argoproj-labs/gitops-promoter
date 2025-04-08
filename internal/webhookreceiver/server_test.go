package webhookreceiver

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestPostRootMaxPayload(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		payloadSize    int
		maxPayloadSize int64
		wantStatus     int
	}{
		{
			name:           "payload exceeds max size",
			payloadSize:    2,
			maxPayloadSize: 1,
			wantStatus:     http.StatusRequestEntityTooLarge,
		},
		{
			name:           "valid size but invalid payload",
			payloadSize:    1,
			maxPayloadSize: 1,
			wantStatus:     http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			k8sManager, err := ctrl.NewManager(&rest.Config{}, ctrl.Options{})
			require.NoError(t, err)

			wr := NewWebhookReceiver(
				k8sManager,
				WithMaxPayloadSize(tt.maxPayloadSize),
			)

			body := bytes.Repeat([]byte("a"), tt.payloadSize)
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
			resp := httptest.NewRecorder()

			wr.postRoot(resp, req)

			assert.Equal(t, tt.wantStatus, resp.Code)
		})
	}
}
