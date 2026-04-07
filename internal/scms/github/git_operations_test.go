package github_test

import (
	"context"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("GetClient", func() {
	var (
		ctx         context.Context
		scmProvider *v1alpha1.ScmProvider
		secret      corev1.Secret
	)

	BeforeEach(func() {
		ctx = context.Background()
		scmProvider = &v1alpha1.ScmProvider{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-provider",
				Namespace: "default",
			},
			Spec: v1alpha1.ScmProviderSpec{
				GitHub: &v1alpha1.GitHub{
					AppID: 12345,
				},
			},
		}
	})

	Describe("with an invalid GitHub App private key", func() {
		It("should return an error, not panic", func() {
			secret = corev1.Secret{
				Data: map[string][]byte{
					"githubAppPrivateKey": []byte("this-is-not-a-valid-pem-key"),
				},
			}

			_, _, err := github.GetClient(ctx, scmProvider, secret, "myorg")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("could not parse private key"))
		})

		It("should return an error when the private key is empty", func() {
			secret = corev1.Secret{
				Data: map[string][]byte{
					"githubAppPrivateKey": []byte(""),
				},
			}

			_, _, err := github.GetClient(ctx, scmProvider, secret, "myorg")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("could not parse private key"))
		})

		It("should return an error when the private key secret key is missing entirely", func() {
			secret = corev1.Secret{
				Data: map[string][]byte{},
			}

			_, _, err := github.GetClient(ctx, scmProvider, secret, "myorg")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("could not parse private key"))
		})
	})
})
