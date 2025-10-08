package azuredevops

import (
	"context"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTokenCredential is a mock implementation of azcore.TokenCredential for testing
type mockTokenCredential struct {
	expiresAt time.Time // 24 bytes (largest field first)
	token     string    // 16 bytes
	err       error     // 16 bytes (interface: type pointer + data pointer)
}

func (m *mockTokenCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	if m.err != nil {
		return azcore.AccessToken{}, m.err
	}
	return azcore.AccessToken{
		Token:     m.token,
		ExpiresOn: m.expiresAt,
	}, nil
}

func TestTokenManager_GetToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("successful token retrieval", func(t *testing.T) {
		t.Parallel()
		mockCred := &mockTokenCredential{
			token:     "test-token-123",
			expiresAt: time.Now().Add(1 * time.Hour),
		}

		tm := NewTokenManager(mockCred)
		token, err := tm.GetToken(ctx)

		require.NoError(t, err)
		assert.Equal(t, "test-token-123", token)
	})

	t.Run("token caching", func(t *testing.T) {
		t.Parallel()
		mockCred := &mockTokenCredential{
			token:     "cached-token",
			expiresAt: time.Now().Add(1 * time.Hour),
		}

		tm := NewTokenManager(mockCred)

		// First call should cache the token
		token1, err := tm.GetToken(ctx)
		require.NoError(t, err)

		// Change the mock to return a different token
		mockCred.token = "different-token"

		// Second call should return cached token
		token2, err := tm.GetToken(ctx)
		require.NoError(t, err)

		assert.Equal(t, token1, token2)
		assert.Equal(t, "cached-token", token2)
	})

	t.Run("token refresh when expired", func(t *testing.T) {
		t.Parallel()
		mockCred := &mockTokenCredential{
			token:     "expired-token",
			expiresAt: time.Now().Add(-1 * time.Hour), // Already expired
		}

		tm := NewTokenManager(mockCred)

		// First call with expired token
		_, err := tm.GetToken(ctx)
		require.NoError(t, err)

		// Update mock to return new token
		mockCred.token = "fresh-token"
		mockCred.expiresAt = time.Now().Add(1 * time.Hour)

		// Second call should get fresh token
		token, err := tm.GetToken(ctx)
		require.NoError(t, err)
		assert.Equal(t, "fresh-token", token)
	})

	t.Run("error handling", func(t *testing.T) {
		t.Parallel()
		mockCred := &mockTokenCredential{
			err: assert.AnError,
		}

		tm := NewTokenManager(mockCred)
		_, err := tm.GetToken(ctx)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get access token")
	})
}

func TestTokenManager_IsValid(t *testing.T) {
	t.Parallel()

	t.Run("valid token", func(t *testing.T) {
		t.Parallel()
		mockCred := &mockTokenCredential{
			token:     "valid-token",
			expiresAt: time.Now().Add(1 * time.Hour),
		}

		tm := NewTokenManager(mockCred)
		_, err := tm.GetToken(context.Background())
		require.NoError(t, err)

		assert.True(t, tm.IsValid())
	})

	t.Run("expired token", func(t *testing.T) {
		t.Parallel()
		mockCred := &mockTokenCredential{
			token:     "expired-token",
			expiresAt: time.Now().Add(-1 * time.Hour),
		}

		tm := NewTokenManager(mockCred)
		_, err := tm.GetToken(context.Background())
		require.NoError(t, err)

		assert.False(t, tm.IsValid())
	})

	t.Run("no token", func(t *testing.T) {
		t.Parallel()
		tm := NewTokenManager(&mockTokenCredential{})
		assert.False(t, tm.IsValid())
	})
}
