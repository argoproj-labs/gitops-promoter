package azuredevops

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

// TokenManager manages Azure DevOps access tokens with automatic refresh
type TokenManager struct {
	expiresAt   time.Time
	credential  azcore.TokenCredential
	cachedToken string
	scope       string
	mutex       sync.RWMutex
}

// NewTokenManager creates a new token manager for workload identity
func NewTokenManager(credential azcore.TokenCredential) *TokenManager {
	return &TokenManager{
		credential: credential,
		scope:      azureDevOpsScope,
	}
}

// GetToken retrieves a valid access token, refreshing if necessary
func (tm *TokenManager) GetToken(ctx context.Context) (string, error) {
	tm.mutex.RLock()
	if tm.cachedToken != "" && time.Now().Before(tm.expiresAt.Add(-5*time.Minute)) {
		token := tm.cachedToken
		tm.mutex.RUnlock()
		return token, nil
	}
	tm.mutex.RUnlock()

	// Need to refresh the token
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	// Double-check after acquiring write lock
	if tm.cachedToken != "" && time.Now().Before(tm.expiresAt.Add(-5*time.Minute)) {
		return tm.cachedToken, nil
	}

	// Get new token
	tokenResult, err := tm.credential.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{tm.scope},
	})
	if err != nil {
		return "", fmt.Errorf("failed to get access token: %w", err)
	}

	tm.cachedToken = tokenResult.Token
	tm.expiresAt = tokenResult.ExpiresOn

	return tm.cachedToken, nil
}

// IsValid checks if the current token is still valid
func (tm *TokenManager) IsValid() bool {
	tm.mutex.RLock()
	defer tm.mutex.RUnlock()

	return tm.cachedToken != "" && time.Now().Before(tm.expiresAt.Add(-5*time.Minute))
}
