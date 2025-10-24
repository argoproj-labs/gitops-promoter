package azuredevops_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	azuredevopsscm "github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
)

var _ = Describe("CommitStatus Error Handling", func() {
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

		_, err := azuredevopsscm.NewAzureDevopsCommitStatusProvider(context.Background(), nil, &scmProvider, emptySecret, "test-org")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("azure DevOps token not found in secret"))
	})

	It("should handle missing token in secret", func() {
		scmProvider := v1alpha1.ScmProvider{
			Spec: v1alpha1.ScmProviderSpec{
				AzureDevOps: &v1alpha1.AzureDevOps{
					Organization: "test-org",
				},
			},
		}

		noTokenSecret := v1.Secret{
			Data: map[string][]byte{},
		}

		_, err := azuredevopsscm.NewAzureDevopsCommitStatusProvider(context.Background(), nil, &scmProvider, noTokenSecret, "test-org")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("azure DevOps token not found in secret"))
	})
})
