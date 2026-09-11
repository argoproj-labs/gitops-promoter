package webhookreceiver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
)

var _ = Describe("verifyWebhookSignature", func() {
	It("accepts a valid HMAC-SHA256 signature and rejects wrong, malformed, missing, or empty-secret cases", func() {
		secret := []byte("whsec_test")
		body := []byte(`{"action":"created"}`)
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write(body)
		good := []byte(sha256Prefix + hex.EncodeToString(mac.Sum(nil)))

		Expect(verifyWebhookSignature(secret, good, body)).To(BeTrue())
		Expect(verifyWebhookSignature(secret, []byte(sha256Prefix+"0000000000000000000000000000000000000000000000000000000000000000"), body)).To(BeFalse())
		Expect(verifyWebhookSignature(secret, []byte(sha256Prefix+"not-hex"), body)).To(BeFalse())
		Expect(verifyWebhookSignature(secret, nil, body)).To(BeFalse())
		Expect(verifyWebhookSignature(nil, good, body)).To(BeFalse())
	})

	It("accepts a matching shared token and rejects mismatched or different-length tokens", func() {
		secret := []byte("gitlab-shared-token")
		body := []byte(`{}`)

		Expect(verifyWebhookSignature(secret, secret, body)).To(BeTrue())
		Expect(verifyWebhookSignature(secret, []byte("wrong-token"), body)).To(BeFalse())
		Expect(verifyWebhookSignature(secret, []byte("short"), body)).To(BeFalse())
	})
})

var _ = Describe("webhookSecretFromSecret", func() {
	It("returns ok=false when webhookSecret is absent", func() {
		_, _, ok := webhookSecretFromSecret(&v1.Secret{Data: map[string][]byte{"token": []byte("x")}}, ProviderGitHub)
		Expect(ok).To(BeFalse())
	})

	It("defaults to X-Hub-Signature-256 for GitHub-style providers", func() {
		secret, header, ok := webhookSecretFromSecret(&v1.Secret{Data: map[string][]byte{
			promoterv1alpha1.ScmProviderSecretKeyWebhookSecret: []byte("s"),
		}}, ProviderGitHub)
		Expect(ok).To(BeTrue())
		Expect(string(secret)).To(Equal("s"))
		Expect(header).To(Equal("X-Hub-Signature-256"))
	})

	It("defaults to X-Gitlab-Token for GitLab", func() {
		_, header, ok := webhookSecretFromSecret(&v1.Secret{Data: map[string][]byte{
			promoterv1alpha1.ScmProviderSecretKeyWebhookSecret: []byte("s"),
		}}, ProviderGitLab)
		Expect(ok).To(BeTrue())
		Expect(header).To(Equal("X-Gitlab-Token"))
	})

	It("uses an explicit webhookSignatureHeader when set", func() {
		_, header, ok := webhookSecretFromSecret(&v1.Secret{Data: map[string][]byte{
			promoterv1alpha1.ScmProviderSecretKeyWebhookSecret:          []byte("s"),
			promoterv1alpha1.ScmProviderSecretKeyWebhookSignatureHeader: []byte("X-Custom-Sig"),
		}}, ProviderGitHub)
		Expect(ok).To(BeTrue())
		Expect(header).To(Equal("X-Custom-Sig"))
	})
})

var _ = Describe("evaluateWebhookFilter", func() {
	payload := map[string]any{
		"context": "ArgoCD/my-app",
		"state":   "success",
	}

	It("returns true when the expression matches Payload", func() {
		matched, err := evaluateWebhookFilter(`Payload.context startsWith "ArgoCD/"`, payload)
		Expect(err).NotTo(HaveOccurred())
		Expect(matched).To(BeTrue())
	})

	It("returns false when the expression does not match Payload", func() {
		matched, err := evaluateWebhookFilter(`Payload.context startsWith "CI/"`, payload)
		Expect(err).NotTo(HaveOccurred())
		Expect(matched).To(BeFalse())
	})

	It("returns an error for invalid path access", func() {
		_, err := evaluateWebhookFilter(`Payload.missing.field == 1`, payload)
		Expect(err).To(HaveOccurred())
	})

	It("returns an error for an uncompilable expression", func() {
		_, err := evaluateWebhookFilter(`not valid {{{`, payload)
		Expect(err).To(HaveOccurred())
	})
})
