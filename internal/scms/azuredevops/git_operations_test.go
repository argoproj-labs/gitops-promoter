package azuredevops_test

import (
	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("GitAuthenticationProvider", func() {
	Describe("GetGitHttpsRepoUrl", func() {
		table := []struct {
			name        string
			scmSpec     v1alpha1.ScmProviderSpec
			repoSpec    v1alpha1.GitRepositorySpec
			expectedUrl string
		}{
			{
				name: "default dev.azure.com domain",
				scmSpec: v1alpha1.ScmProviderSpec{
					AzureDevOps: &v1alpha1.AzureDevOps{
						Organization: "myorg",
						Domain:       "",
					},
				},
				repoSpec: v1alpha1.GitRepositorySpec{
					AzureDevOps: &v1alpha1.AzureDevOpsRepo{
						Project: "myproj",
						Name:    "myrepo",
					},
				},
				expectedUrl: "https://dev.azure.com/myorg/myproj/_git/myrepo",
			},
			{
				name: "custom domain",
				scmSpec: v1alpha1.ScmProviderSpec{
					AzureDevOps: &v1alpha1.AzureDevOps{
						Organization: "myorg",
						Domain:       "devops.example.com",
					},
				},
				repoSpec: v1alpha1.GitRepositorySpec{
					AzureDevOps: &v1alpha1.AzureDevOpsRepo{
						Project: "myproj",
						Name:    "myrepo",
					},
				},
				expectedUrl: "https://devops.example.com/myorg/myproj/_git/myrepo",
			},
		}

		for _, tc := range table {
			It("should return correct URL for "+tc.name, func() {
				scmProvider := &v1alpha1.ScmProvider{Spec: tc.scmSpec}
				secret := &corev1.Secret{}

				provider := azuredevops.NewAzdoGitAuthenticationProvider(scmProvider, secret)

				repo := v1alpha1.GitRepository{Spec: tc.repoSpec}
				url := provider.GetGitHttpsRepoUrl(repo)
				Expect(url).To(Equal(tc.expectedUrl))
			})
		}
	})
})
