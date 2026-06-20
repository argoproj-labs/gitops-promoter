package azuredevops

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "k8s.io/api/core/v1"
)

func TestApplyHTTPAuth_PAT(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	secret := v1.Secret{Data: map[string][]byte{"token": []byte("ado-pat")}}
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "https://dev.azure.com/", nil)

	if err := ApplyHTTPAuth(ctx, secret, req); err != nil {
		t.Fatalf("ApplyHTTPAuth() error: %v", err)
	}
	// base64(":ado-pat") == "OmFkby1wYXQ="
	if got := req.Header.Get("Authorization"); got != "Basic OmFkby1wYXQ=" {
		t.Fatalf("Authorization = %q, want Basic header", got)
	}
}

// Not parallel: mutates the package-level tokens var via withStubTokenSource.
//
//nolint:paralleltest // swaps the shared package-level tokens source
func TestApplyHTTPAuth_WorkloadIdentity(t *testing.T) {
	withStubTokenSource(t, &stubTokenSource{token: "wi-token"})

	ctx := context.Background()
	secret := v1.Secret{Data: map[string][]byte{"workloadIdentity": []byte("true")}}
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "https://dev.azure.com/", nil)

	if err := ApplyHTTPAuth(ctx, secret, req); err != nil {
		t.Fatalf("ApplyHTTPAuth() error: %v", err)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer wi-token" {
		t.Fatalf("Authorization = %q, want Bearer wi-token", got)
	}
}

func TestApplyHTTPAuth_PAT_Empty(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "https://dev.azure.com/", nil)
	if err := ApplyHTTPAuth(ctx, v1.Secret{}, req); err == nil {
		t.Fatal("expected error for empty PAT, got nil")
	}
}
