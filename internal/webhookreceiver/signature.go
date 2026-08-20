package webhookreceiver

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
)

const sha256Prefix = "sha256="

// defaultSignatureHeader returns the provider-sensible header when the Secret omits
// webhookSignatureHeader.
func defaultSignatureHeader(provider string) string {
	if provider == ProviderGitLab {
		return "X-Gitlab-Token"
	}
	return "X-Hub-Signature-256"
}

// webhookSecretFromSecret extracts webhook verification material from an ScmProvider Secret.
// Returns ok=false when webhookSecret is absent (verification not configured for this Secret).
func webhookSecretFromSecret(secret *v1.Secret, provider string) (secretBytes []byte, headerName string, ok bool) {
	if secret == nil || secret.Data == nil {
		return nil, "", false
	}
	secretBytes = secret.Data[promoterv1alpha1.ScmProviderSecretKeyWebhookSecret]
	if len(secretBytes) == 0 {
		return nil, "", false
	}
	headerName = string(secret.Data[promoterv1alpha1.ScmProviderSecretKeyWebhookSignatureHeader])
	if headerName == "" {
		headerName = defaultSignatureHeader(provider)
	}
	return secretBytes, headerName, true
}

// verifyWebhookSignature validates an inbound webhook using the shared secret.
//
// If headerValue has a "sha256=" prefix, it is treated as GitHub-style HMAC-SHA256 of the
// raw body. Otherwise the header value is compared to the secret as a shared token
// (GitLab-style) using constant-time comparison.
func verifyWebhookSignature(secret, headerValue []byte, body []byte) bool {
	if len(secret) == 0 {
		return false
	}
	headerStr := string(headerValue)
	if strings.HasPrefix(headerStr, sha256Prefix) {
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write(body)
		expected := sha256Prefix + hex.EncodeToString(mac.Sum(nil))
		return hmac.Equal([]byte(expected), headerValue)
	}
	if len(headerValue) != len(secret) {
		return false
	}
	return subtle.ConstantTimeCompare(headerValue, secret) == 1
}
