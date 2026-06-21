package azuredevops

import (
	"context"
	"fmt"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// azureDevOpsScope is the Entra resource scope for Azure DevOps access tokens.
const azureDevOpsScope = "499b84ac-1321-427f-aa17-267ca6975798/.default"

// credential is the subset of azidentity.WorkloadIdentityCredential used for token acquisition.
// It is an interface so tests can inject a fake.
type credential interface {
	GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error)
}

// tokenSource supplies Azure DevOps access tokens for a parsed auth configuration.
type tokenSource interface {
	Token(ctx context.Context, cfg authConfig) (string, error)
}

// workloadIdentityTokenSource acquires Entra tokens via azidentity.WorkloadIdentityCredential.
// Credential instances are cached by client/tenant so the SDK's internal token cache is reused
// across reconciles (the credential refreshes the ~1h token internally).
type workloadIdentityTokenSource struct {
	cache   map[string]credential
	newCred func(cfg authConfig) (credential, error)
	mu      sync.Mutex
}

// tokens is the package-level token source used by production code. Tests may replace it.
var tokens tokenSource = &workloadIdentityTokenSource{
	cache:   map[string]credential{},
	newCred: newWorkloadIdentityCredential,
}

// newWorkloadIdentityCredential builds a WorkloadIdentityCredential. Empty ClientID/TenantID cause
// azidentity to read the injected AZURE_CLIENT_ID, AZURE_TENANT_ID, AZURE_FEDERATED_TOKEN_FILE and
// AZURE_AUTHORITY_HOST environment variables.
func newWorkloadIdentityCredential(cfg authConfig) (credential, error) {
	cred, err := azidentity.NewWorkloadIdentityCredential(&azidentity.WorkloadIdentityCredentialOptions{
		ClientID: cfg.clientID,
		TenantID: cfg.tenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create workload identity credential: %w", err)
	}
	return cred, nil
}

// Token returns an Azure DevOps access token, creating and caching the credential as needed.
func (s *workloadIdentityTokenSource) Token(ctx context.Context, cfg authConfig) (string, error) {
	key := cfg.clientID + "|" + cfg.tenantID

	s.mu.Lock()
	cred, ok := s.cache[key]
	if !ok {
		var err error
		cred, err = s.newCred(cfg)
		if err != nil {
			s.mu.Unlock()
			return "", err
		}
		s.cache[key] = cred
	}
	s.mu.Unlock()

	tok, err := cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{azureDevOpsScope}})
	if err != nil {
		return "", fmt.Errorf("failed to acquire Azure DevOps access token: %w", err)
	}
	return tok.Token, nil
}
