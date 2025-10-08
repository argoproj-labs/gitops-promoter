package azuredevops_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	azuredevopsscm "github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
)

var _ = Describe("PullRequest Integration Points", func() {
	Describe("Error Handling", func() {
		It("should handle authentication with invalid token", func() {
			scmProvider := v1alpha1.ScmProvider{
				Spec: v1alpha1.ScmProviderSpec{
					AzureDevOps: &v1alpha1.AzureDevOps{
						Organization: "test-org",
					},
				},
			}

			secret := v1.Secret{
				Data: map[string][]byte{"token": []byte("invalid-token")},
			}

			_, err := azuredevopsscm.NewAzdoPullRequestProvider(context.Background(), nil, &scmProvider, secret, "test-org")
			_ = err
		})

		It("should handle empty token in secret", func() {
			scmProvider := v1alpha1.ScmProvider{
				Spec: v1alpha1.ScmProviderSpec{
					AzureDevOps: &v1alpha1.AzureDevOps{
						Organization: "test-org",
					},
				},
			}

			emptySecret := v1.Secret{
				Data: map[string][]byte{"token": []byte("")},
			}

			_, err := azuredevopsscm.NewAzdoPullRequestProvider(context.Background(), nil, &scmProvider, emptySecret, "test-org")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("azure DevOps token not found in secret"))
		})
	})
})
