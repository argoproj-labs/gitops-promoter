package webhookreceiver_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/internal/webhookreceiver"
)

// computeGitHubSig computes a valid X-Hub-Signature-256 header value for the given secret and body.
func computeGitHubSig(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

var _ = Describe("VerifyGitHubSignature", func() {
	const signingSecret = "test-webhook-secret"
	body := []byte(`{"ref":"refs/heads/main","before":"abc123","pusher":{"name":"user"}}`)

	It("returns true for a valid signature", func() {
		sig := computeGitHubSig(signingSecret, body)
		Expect(webhookreceiver.VerifyGitHubSignature([]byte(signingSecret), body, sig)).To(BeTrue())
	})

	It("returns false for an invalid signature", func() {
		Expect(webhookreceiver.VerifyGitHubSignature([]byte(signingSecret), body, "sha256=deadbeef")).To(BeFalse())
	})

	It("returns false when signature is missing the sha256= prefix", func() {
		mac := hmac.New(sha256.New, []byte(signingSecret))
		mac.Write(body)
		Expect(webhookreceiver.VerifyGitHubSignature([]byte(signingSecret), body, hex.EncodeToString(mac.Sum(nil)))).To(BeFalse())
	})

	It("returns false for an empty signature", func() {
		Expect(webhookreceiver.VerifyGitHubSignature([]byte(signingSecret), body, "")).To(BeFalse())
	})

	It("returns false when the signature contains invalid hex", func() {
		Expect(webhookreceiver.VerifyGitHubSignature([]byte(signingSecret), body, "sha256=notvalidhex!")).To(BeFalse())
	})

	It("returns false when the wrong secret is used", func() {
		sig := computeGitHubSig("different-secret", body)
		Expect(webhookreceiver.VerifyGitHubSignature([]byte(signingSecret), body, sig)).To(BeFalse())
	})

	It("returns false when the body differs from what was signed", func() {
		sig := computeGitHubSig(signingSecret, body)
		tamperedBody := append([]byte{}, body...)
		tamperedBody[0] = '!'
		Expect(webhookreceiver.VerifyGitHubSignature([]byte(signingSecret), tamperedBody, sig)).To(BeFalse())
	})
})

var _ = Describe("GitHub webhook signature enforcement in postRoot", func() {
	const (
		controllerNamespace = "promoter-system"
		signingSecret       = "s3cr3t"
		secretName          = "github-webhook-secret"
	)

	body := []byte(`{"ref":"refs/heads/main","before":"abc123","pusher":{"name":"user"}}`)

	// buildReceiver sets up a WebhookReceiver backed by a fake k8s client.
	// If withSecretRef is true, the ControllerConfiguration includes the GitHub secretRef.
	// If withSecret is true, the referenced Secret is also created.
	buildReceiver := func(withSecretRef, withSecret bool) *webhookreceiver.WebhookReceiver {
		scheme := utils.GetScheme()

		cc := &promoterv1alpha1.ControllerConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name:      settings.ControllerConfigurationName,
				Namespace: controllerNamespace,
			},
			Spec: promoterv1alpha1.ControllerConfigurationSpec{},
		}
		if withSecretRef {
			cc.Spec.WebhookReceiver = &promoterv1alpha1.WebhookReceiverConfiguration{
				GitHub: &promoterv1alpha1.GitHubWebhookReceiverConfiguration{
					SecretRef: &corev1.LocalObjectReference{Name: secretName},
				},
			}
		}

		b := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cc)
		if withSecret {
			b = b.WithObjects(&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: controllerNamespace,
				},
				Data: map[string][]byte{
					"webhookSecret": []byte(signingSecret),
				},
			})
		}
		fakeClient := b.Build()

		mgr := settings.NewManager(fakeClient, fakeClient, settings.ManagerConfig{
			ControllerNamespace: controllerNamespace,
		})
		wr := webhookreceiver.NewWebhookReceiverWithClient(fakeClient, nil, mgr)
		return &wr
	}

	// invoke sends a POST request with a GitHub event header and optional signature.
	invoke := func(wr *webhookreceiver.WebhookReceiver, sig string) int {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
		req.Header.Set("X-GitHub-Event", "push")
		if sig != "" {
			req.Header.Set("X-Hub-Signature-256", sig)
		}
		rr := httptest.NewRecorder()
		wr.ServeHTTP(rr, req)
		return rr.Code
	}

	Context("when no webhook secret is configured", func() {
		It("allows requests without any signature header (returns 204 – no matching CTP)", func() {
			wr := buildReceiver(false, false)
			Expect(invoke(wr, "")).To(Equal(http.StatusNoContent))
		})

		It("allows requests even when a signature header is present", func() {
			wr := buildReceiver(false, false)
			Expect(invoke(wr, computeGitHubSig(signingSecret, body))).To(Equal(http.StatusNoContent))
		})
	})

	Context("when a webhook secret is configured", func() {
		It("accepts a request with a valid signature (returns 204 – no matching CTP)", func() {
			wr := buildReceiver(true, true)
			sig := computeGitHubSig(signingSecret, body)
			Expect(invoke(wr, sig)).To(Equal(http.StatusNoContent))
		})

		It("rejects a request with an invalid signature with 401", func() {
			wr := buildReceiver(true, true)
			Expect(invoke(wr, "sha256=00000000000000000000000000000000000000000000000000000000000000ff")).
				To(Equal(http.StatusUnauthorized))
		})

		It("rejects a request with a missing signature with 401", func() {
			wr := buildReceiver(true, true)
			Expect(invoke(wr, "")).To(Equal(http.StatusUnauthorized))
		})

		It("returns 500 when the referenced secret does not exist", func() {
			wr := buildReceiver(true, false) // secretRef configured but Secret not created
			Expect(invoke(wr, computeGitHubSig(signingSecret, body))).
				To(Equal(http.StatusInternalServerError))
		})
	})

	Context("non-GitHub providers", func() {
		It("does not verify signatures for GitLab webhooks even when a GitHub secret is configured", func() {
			wr := buildReceiver(true, true)
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
			req.Header.Set("X-Gitlab-Event", "Push Hook")
			// No signature header – signature check is skipped for non-GitHub providers.
			rr := httptest.NewRecorder()
			wr.ServeHTTP(rr, req)
			Expect(rr.Code).To(Equal(http.StatusNoContent))
		})
	})
})

// Compile-time assertion: WebhookReceiver must satisfy http.Handler.
var _ http.Handler = (*webhookreceiver.WebhookReceiver)(nil)

// Compile-time assertion: settings.Manager Reader interface is satisfied by fake client.
var _ client.Reader = (client.Client)(nil)

