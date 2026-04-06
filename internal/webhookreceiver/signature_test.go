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
		beforeSHA           = "abc123"
	)

	// body is a minimal GitHub push payload whose "before" SHA matches the CTP status SHA below.
	body := []byte(`{"ref":"refs/heads/main","before":"` + beforeSHA + `","pusher":{"name":"user"}}`)

	// buildReceiverWithScmProvider sets up a WebhookReceiver backed by a fake k8s client with
	// a full CTP → GitRepository → ScmProvider chain.
	// scmProvider may be nil to omit the ScmProvider (using ClusterScmProvider instead).
	// clusterScmProvider may be nil to use the ScmProvider instead.
	// withSecret controls whether the referenced Kubernetes Secret is created.
	// withCTP controls whether the CTP + GitRepository objects are created (enabling sig check).
	buildReceiverWithScmProvider := func(scmProvider *promoterv1alpha1.ScmProvider, clusterScmProvider *promoterv1alpha1.ClusterScmProvider, withSecret, withCTP bool) *webhookreceiver.WebhookReceiver {
		scheme := utils.GetScheme()

		b := fake.NewClientBuilder().WithScheme(scheme).
			WithIndex(&promoterv1alpha1.ChangeTransferPolicy{}, ".status.proposed.hydrated.sha", func(o client.Object) []string {
				ctp := o.(*promoterv1alpha1.ChangeTransferPolicy) //nolint:forcetypeassert
				return []string{ctp.Status.Proposed.Hydrated.Sha}
			}).
			WithIndex(&promoterv1alpha1.ChangeTransferPolicy{}, ".status.active.hydrated.sha", func(o client.Object) []string {
				ctp := o.(*promoterv1alpha1.ChangeTransferPolicy) //nolint:forcetypeassert
				return []string{ctp.Status.Active.Hydrated.Sha}
			})

		if scmProvider != nil {
			b = b.WithObjects(scmProvider)
		}
		if clusterScmProvider != nil {
			b = b.WithObjects(clusterScmProvider)
		}
		if withSecret {
			ns := controllerNamespace
			if scmProvider != nil {
				ns = scmProvider.Namespace
			}
			b = b.WithObjects(&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: ns,
				},
				Data: map[string][]byte{
					"webhookSecret": []byte(signingSecret),
				},
			})
		}
		if withCTP {
			var scmProviderRefKind, scmProviderRefName string
			if clusterScmProvider != nil {
				scmProviderRefKind = promoterv1alpha1.ClusterScmProviderKind
				scmProviderRefName = clusterScmProvider.Name
			} else if scmProvider != nil {
				scmProviderRefKind = promoterv1alpha1.ScmProviderKind
				scmProviderRefName = scmProvider.Name
			}

			gitRepo := &promoterv1alpha1.GitRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-git-repo",
					Namespace: controllerNamespace,
				},
				Spec: promoterv1alpha1.GitRepositorySpec{
					ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
						Kind: scmProviderRefKind,
						Name: scmProviderRefName,
					},
				},
			}
			ctp := &promoterv1alpha1.ChangeTransferPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-ctp",
					Namespace: controllerNamespace,
				},
				Spec: promoterv1alpha1.ChangeTransferPolicySpec{
					RepositoryReference:    promoterv1alpha1.ObjectReference{Name: "my-git-repo"},
					ProposedBranch:         "main-next",
					ActiveBranch:           "main",
					ActiveCommitStatuses:   []promoterv1alpha1.CommitStatusSelector{},
					ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{},
				},
				Status: promoterv1alpha1.ChangeTransferPolicyStatus{
					Proposed: promoterv1alpha1.CommitBranchState{
						Hydrated: promoterv1alpha1.CommitShaState{
							Sha: beforeSHA,
						},
					},
				},
			}
			b = b.WithObjects(gitRepo).WithStatusSubresource(ctp).WithObjects(ctp)
		}

		fakeClient := b.Build()
		return webhookreceiver.NewWebhookReceiverWithClient(fakeClient, nil, controllerNamespace)
	}

	// buildScmProvider creates a namespaced ScmProvider with an optional webhookSecretRef.
	buildScmProvider := func(withWebhookSecretRef bool) *promoterv1alpha1.ScmProvider {
		smp := &promoterv1alpha1.ScmProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-github-provider",
				Namespace: controllerNamespace,
			},
			Spec: promoterv1alpha1.ScmProviderSpec{
				GitHub: &promoterv1alpha1.GitHub{
					AppID: 12345,
				},
			},
		}
		if withWebhookSecretRef {
			smp.Spec.GitHub.WebhookSecretRef = &corev1.LocalObjectReference{Name: secretName}
		}
		return smp
	}

	// buildClusterScmProvider creates a ClusterScmProvider with an optional webhookSecretRef.
	buildClusterScmProvider := func(withWebhookSecretRef bool) *promoterv1alpha1.ClusterScmProvider {
		csmp := &promoterv1alpha1.ClusterScmProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-cluster-github-provider",
			},
			Spec: promoterv1alpha1.ScmProviderSpec{
				GitHub: &promoterv1alpha1.GitHub{
					AppID: 12345,
				},
			},
		}
		if withWebhookSecretRef {
			csmp.Spec.GitHub.WebhookSecretRef = &corev1.LocalObjectReference{Name: secretName}
		}
		return csmp
	}

	// invoke sends a POST GitHub push request with an optional signature header.
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

	Context("when no ScmProvider has a webhookSecretRef configured", func() {
		It("allows requests without any signature (returns 204 – no matching CTP)", func() {
			wr := buildReceiverWithScmProvider(buildScmProvider(false), nil, false, false)
			Expect(invoke(wr, "")).To(Equal(http.StatusNoContent))
		})

		It("allows requests with a signature (returns 204 – no matching CTP)", func() {
			wr := buildReceiverWithScmProvider(buildScmProvider(false), nil, false, false)
			Expect(invoke(wr, computeGitHubSig(signingSecret, body))).To(Equal(http.StatusNoContent))
		})

		It("allows requests even when a CTP is present (ScmProvider has no secret – skip verification)", func() {
			wr := buildReceiverWithScmProvider(buildScmProvider(false), nil, false, true)
			Expect(invoke(wr, "")).To(Equal(http.StatusNoContent))
		})
	})

	Context("when a namespaced ScmProvider has a webhookSecretRef configured", func() {
		Context("with a matching CTP", func() {
			It("accepts a request with a valid signature (returns 204)", func() {
				wr := buildReceiverWithScmProvider(buildScmProvider(true), nil, true, true)
				Expect(invoke(wr, computeGitHubSig(signingSecret, body))).To(Equal(http.StatusNoContent))
			})

			It("rejects a request with an invalid signature with 401", func() {
				wr := buildReceiverWithScmProvider(buildScmProvider(true), nil, true, true)
				Expect(invoke(wr, "sha256=00000000000000000000000000000000000000000000000000000000000000ff")).
					To(Equal(http.StatusUnauthorized))
			})

			It("rejects a request with a missing signature with 401", func() {
				wr := buildReceiverWithScmProvider(buildScmProvider(true), nil, true, true)
				Expect(invoke(wr, "")).To(Equal(http.StatusUnauthorized))
			})

			It("returns 500 when the referenced secret does not exist", func() {
				wr := buildReceiverWithScmProvider(buildScmProvider(true), nil, false, true)
				Expect(invoke(wr, computeGitHubSig(signingSecret, body))).
					To(Equal(http.StatusInternalServerError))
			})
		})

		Context("without a matching CTP", func() {
			It("returns 204 without performing signature check", func() {
				// Even though a secret is configured, no CTP is found so no sig check occurs.
				wr := buildReceiverWithScmProvider(buildScmProvider(true), nil, true, false)
				Expect(invoke(wr, "")).To(Equal(http.StatusNoContent))
			})
		})
	})

	Context("when a ClusterScmProvider has a webhookSecretRef configured", func() {
		Context("with a matching CTP", func() {
			It("accepts a request with a valid signature (returns 204)", func() {
				wr := buildReceiverWithScmProvider(nil, buildClusterScmProvider(true), true, true)
				Expect(invoke(wr, computeGitHubSig(signingSecret, body))).To(Equal(http.StatusNoContent))
			})

			It("rejects a request with a missing signature with 401", func() {
				wr := buildReceiverWithScmProvider(nil, buildClusterScmProvider(true), true, true)
				Expect(invoke(wr, "")).To(Equal(http.StatusUnauthorized))
			})

			It("returns 500 when the referenced secret does not exist", func() {
				wr := buildReceiverWithScmProvider(nil, buildClusterScmProvider(true), false, true)
				Expect(invoke(wr, computeGitHubSig(signingSecret, body))).
					To(Equal(http.StatusInternalServerError))
			})
		})
	})

	Context("non-GitHub providers", func() {
		It("does not verify signatures for GitLab webhooks even when a ScmProvider has a webhookSecretRef", func() {
			wr := buildReceiverWithScmProvider(buildScmProvider(true), nil, true, false)
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
			req.Header.Set("X-Gitlab-Event", "Push Hook")
			// No signature header – signature check is skipped for non-GitHub providers.
			rr := httptest.NewRecorder()
			wr.ServeHTTP(rr, req)
			Expect(rr.Code).To(Equal(http.StatusNoContent))
		})
	})
})
