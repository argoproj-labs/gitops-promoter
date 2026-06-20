package azuredevops

import (
	"context"
	"testing"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
)

type stubTokenSource struct {
	token  string
	err    error
	gotCfg authConfig
}

func (s *stubTokenSource) Token(_ context.Context, cfg authConfig) (string, error) {
	s.gotCfg = cfg
	return s.token, s.err
}

// withStubTokenSource swaps the package token source for the duration of the test.
func withStubTokenSource(t *testing.T, stub tokenSource) {
	t.Helper()
	prev := tokens
	tokens = stub
	t.Cleanup(func() { tokens = prev })
}

func TestGitAuthenticationProvider_GetToken_PAT(t *testing.T) {
	t.Parallel()

	scm := &v1alpha1.ScmProvider{Spec: v1alpha1.ScmProviderSpec{AzureDevOps: &v1alpha1.AzureDevOps{Organization: "org"}}}
	secret := &v1.Secret{Data: map[string][]byte{"token": []byte("pat-abc")}}

	provider := NewAzdoGitAuthenticationProvider(scm, secret)

	got, err := provider.GetToken(context.Background())
	if err != nil || got != "pat-abc" {
		t.Fatalf("GetToken() = %q, %v; want pat-abc, nil", got, err)
	}
}

// Not parallel: mutates the package-level tokens var via withStubTokenSource.
//
//nolint:paralleltest // swaps the shared package-level tokens source
func TestGitAuthenticationProvider_GetToken_WorkloadIdentity(t *testing.T) {
	stub := &stubTokenSource{token: "wi-token"}
	withStubTokenSource(t, stub)

	scm := &v1alpha1.ScmProvider{Spec: v1alpha1.ScmProviderSpec{AzureDevOps: &v1alpha1.AzureDevOps{Organization: "org"}}}
	secret := &v1.Secret{Data: map[string][]byte{
		"workloadIdentity": []byte("true"),
		"azureClientID":    []byte("client-9"),
	}}

	provider := NewAzdoGitAuthenticationProvider(scm, secret)

	got, err := provider.GetToken(context.Background())
	if err != nil || got != "wi-token" {
		t.Fatalf("GetToken() = %q, %v; want wi-token, nil", got, err)
	}
	if stub.gotCfg.clientID != "client-9" {
		t.Fatalf("token source got clientID %q, want client-9", stub.gotCfg.clientID)
	}
}

func TestGetClient_PAT(t *testing.T) {
	t.Parallel()

	scm := &v1alpha1.ScmProvider{Spec: v1alpha1.ScmProviderSpec{AzureDevOps: &v1alpha1.AzureDevOps{Organization: "myorg"}}}
	secret := v1.Secret{Data: map[string][]byte{"token": []byte("pat-abc")}}

	conn, _, err := GetClient(context.Background(), scm, secret, "myorg")
	if err != nil {
		t.Fatalf("GetClient() error: %v", err)
	}
	// base64(":pat-abc") == "OnBhdC1hYmM="
	if conn.AuthorizationString != "Basic OnBhdC1hYmM=" {
		t.Fatalf("AuthorizationString = %q, want PAT Basic header", conn.AuthorizationString)
	}
	if conn.BaseUrl != "https://dev.azure.com/myorg" {
		t.Fatalf("BaseUrl = %q", conn.BaseUrl)
	}
}

// Not parallel: mutates the package-level tokens var via withStubTokenSource.
//
//nolint:paralleltest // swaps the shared package-level tokens source
func TestGetClient_WorkloadIdentity(t *testing.T) {
	withStubTokenSource(t, &stubTokenSource{token: "wi-token"})

	scm := &v1alpha1.ScmProvider{Spec: v1alpha1.ScmProviderSpec{AzureDevOps: &v1alpha1.AzureDevOps{Organization: "myorg"}}}
	secret := v1.Secret{Data: map[string][]byte{"workloadIdentity": []byte("true")}}

	conn, _, err := GetClient(context.Background(), scm, secret, "myorg")
	if err != nil {
		t.Fatalf("GetClient() error: %v", err)
	}
	if conn.AuthorizationString != "Bearer wi-token" {
		t.Fatalf("AuthorizationString = %q, want Bearer wi-token", conn.AuthorizationString)
	}
	if !conn.SuppressFedAuthRedirect {
		t.Fatal("SuppressFedAuthRedirect should be true")
	}
	if conn.BaseUrl != "https://dev.azure.com/myorg" {
		t.Fatalf("BaseUrl = %q", conn.BaseUrl)
	}
}
