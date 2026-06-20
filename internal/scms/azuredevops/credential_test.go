package azuredevops

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type fakeCredential struct {
	token    string
	err      error
	getCalls int
}

func (f *fakeCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	f.getCalls++
	if f.err != nil {
		return azcore.AccessToken{}, f.err
	}
	return azcore.AccessToken{Token: f.token, ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestWorkloadIdentityTokenSource_Token(t *testing.T) {
	cred := &fakeCredential{token: "entra-token"}
	newCalls := 0
	src := &workloadIdentityTokenSource{
		cache: map[string]credential{},
		newCred: func(authConfig) (credential, error) {
			newCalls++
			return cred, nil
		},
	}

	cfg := authConfig{authType: AuthTypeWorkloadIdentity, clientID: "c", tenantID: "t"}

	tok, err := src.Token(context.Background(), cfg)
	if err != nil || tok != "entra-token" {
		t.Fatalf("Token() = %q, %v; want entra-token, nil", tok, err)
	}

	// Second call with the same client/tenant reuses the cached credential.
	if _, err := src.Token(context.Background(), cfg); err != nil {
		t.Fatalf("second Token() error: %v", err)
	}
	if newCalls != 1 {
		t.Fatalf("newCred called %d times, want 1 (credential should be cached)", newCalls)
	}
}

func TestWorkloadIdentityTokenSource_TokenError(t *testing.T) {
	src := &workloadIdentityTokenSource{
		cache: map[string]credential{},
		newCred: func(authConfig) (credential, error) {
			return nil, errors.New("boom")
		},
	}
	if _, err := src.Token(context.Background(), authConfig{}); err == nil {
		t.Fatal("expected error from Token(), got nil")
	}
}
