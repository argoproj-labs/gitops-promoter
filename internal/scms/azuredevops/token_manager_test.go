package azuredevops_test

import (
	"context"
	"errors"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
)

// mockTokenCredential is a mock implementation of azcore.TokenCredential for testing
type mockTokenCredential struct {
	expiresAt time.Time // 24 bytes
	err       error     // 16 bytes (interface: type pointer + data pointer)
	token     string    // 16 bytes
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

var _ = Describe("TokenManager", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("GetToken", func() {
		It("should successfully retrieve a token", func() {
			mockCred := &mockTokenCredential{
				token:     "test-token-123",
				expiresAt: time.Now().Add(1 * time.Hour),
			}

			tm := azuredevops.NewTokenManager(mockCred)
			token, err := tm.GetToken(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(token).To(Equal("test-token-123"))
		})

		It("should cache tokens to avoid repeated calls", func() {
			mockCred := &mockTokenCredential{
				token:     "cached-token",
				expiresAt: time.Now().Add(1 * time.Hour),
			}

			tm := azuredevops.NewTokenManager(mockCred)

			// First call should cache the token
			token1, err := tm.GetToken(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Change the mock to return a different token
			mockCred.token = "different-token"

			// Second call should return cached token
			token2, err := tm.GetToken(ctx)
			Expect(err).ToNot(HaveOccurred())

			Expect(token1).To(Equal(token2))
			Expect(token2).To(Equal("cached-token"))
		})

		It("should refresh tokens when expired", func() {
			mockCred := &mockTokenCredential{
				token:     "expired-token",
				expiresAt: time.Now().Add(-1 * time.Hour), // Already expired
			}

			tm := azuredevops.NewTokenManager(mockCred)

			// First call with expired token
			_, err := tm.GetToken(ctx)
			Expect(err).ToNot(HaveOccurred())

			// Update mock to return new token
			mockCred.token = "fresh-token"
			mockCred.expiresAt = time.Now().Add(1 * time.Hour)

			// Second call should get fresh token
			token, err := tm.GetToken(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(token).To(Equal("fresh-token"))
		})

		It("should handle credential errors gracefully", func() {
			mockCred := &mockTokenCredential{
				err: errors.New("credential error"),
			}

			tm := azuredevops.NewTokenManager(mockCred)
			_, err := tm.GetToken(ctx)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to get access token"))
		})
	})

	Describe("IsValid", func() {
		It("should return true for valid tokens", func() {
			mockCred := &mockTokenCredential{
				token:     "valid-token",
				expiresAt: time.Now().Add(1 * time.Hour),
			}

			tm := azuredevops.NewTokenManager(mockCred)
			_, err := tm.GetToken(ctx)
			Expect(err).ToNot(HaveOccurred())

			Expect(tm.IsValid()).To(BeTrue())
		})

		It("should return false for expired tokens", func() {
			mockCred := &mockTokenCredential{
				token:     "expired-token",
				expiresAt: time.Now().Add(-1 * time.Hour),
			}

			tm := azuredevops.NewTokenManager(mockCred)
			_, err := tm.GetToken(ctx)
			Expect(err).ToNot(HaveOccurred())

			Expect(tm.IsValid()).To(BeFalse())
		})

		It("should return false when no token is cached", func() {
			tm := azuredevops.NewTokenManager(&mockTokenCredential{})
			Expect(tm.IsValid()).To(BeFalse())
		})
	})
})
