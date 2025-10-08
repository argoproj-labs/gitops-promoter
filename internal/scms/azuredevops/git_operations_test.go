package azuredevops_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
)

var _ = Describe("GitAuthenticationProvider", func() {
	var (
		ctx        context.Context
		gitRepo    v1alpha1.GitRepository
		mockClient client.Client
	)

	BeforeEach(func() {
		ctx = context.Background()
		gitRepo = v1alpha1.GitRepository{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-repo",
				Namespace: "test-namespace",
			},
			Spec: v1alpha1.GitRepositorySpec{
				AzureDevOps: &v1alpha1.AzureDevOpsRepo{
					Project: "test-project",
					Name:    "test-repo-name",
				},
			},
		}
		mockClient = nil // We'll use nil for these unit tests
	})

	Describe("GetGitHttpsRepoUrl", func() {
		Context("with default Azure DevOps domain", func() {
			It("should generate correct HTTPS URL for dev.azure.com", func() {
				scmProvider := v1alpha1.ScmProvider{
					Spec: v1alpha1.ScmProviderSpec{
						AzureDevOps: &v1alpha1.AzureDevOps{
							Organization: "test-org",
							// Domain is empty, should use dev.azure.com
						},
					},
				}
				secret := &v1.Secret{Data: map[string][]byte{"token": []byte("test-token")}}

				authProvider := azuredevops.NewAzdoGitAuthenticationProvider(ctx, mockClient, &scmProvider, secret, client.ObjectKey{})
				url := authProvider.GetGitHttpsRepoUrl(gitRepo)

				Expect(url).To(Equal("https://dev.azure.com/test-org/test-project/_git/test-repo-name"))
			})
		})

		Context("with custom domain", func() {
			It("should generate correct HTTPS URL for custom domain", func() {
				scmProvider := v1alpha1.ScmProvider{
					Spec: v1alpha1.ScmProviderSpec{
						AzureDevOps: &v1alpha1.AzureDevOps{
							Organization: "test-org",
							Domain:       "devops.company.com",
						},
					},
				}
				secret := &v1.Secret{Data: map[string][]byte{"token": []byte("test-token")}}

				authProvider := azuredevops.NewAzdoGitAuthenticationProvider(ctx, mockClient, &scmProvider, secret, client.ObjectKey{})
				url := authProvider.GetGitHttpsRepoUrl(gitRepo)

				Expect(url).To(Equal("https://devops.company.com/test-org/test-project/_git/test-repo-name"))
			})
		})
	})

	Describe("GetUser", func() {
		It("should return 'git' as the user", func() {
			scmProvider := v1alpha1.ScmProvider{
				Spec: v1alpha1.ScmProviderSpec{
					AzureDevOps: &v1alpha1.AzureDevOps{Organization: "test-org"},
				},
			}
			secret := &v1.Secret{Data: map[string][]byte{"token": []byte("test-token")}}

			authProvider := azuredevops.NewAzdoGitAuthenticationProvider(ctx, mockClient, &scmProvider, secret, client.ObjectKey{})
			user, err := authProvider.GetUser(ctx)

			Expect(err).ToNot(HaveOccurred())
			Expect(user).To(Equal("git"))
		})
	})

	Describe("GetToken", func() {
		Context("with PAT authentication", func() {
			It("should return the PAT token", func() {
				scmProvider := v1alpha1.ScmProvider{
					Spec: v1alpha1.ScmProviderSpec{
						AzureDevOps: &v1alpha1.AzureDevOps{Organization: "test-org"},
					},
				}
				secret := &v1.Secret{Data: map[string][]byte{"token": []byte("test-pat-token")}}

				authProvider := azuredevops.NewAzdoGitAuthenticationProvider(ctx, mockClient, &scmProvider, secret, client.ObjectKey{})
				token, err := authProvider.GetToken(ctx)

				Expect(err).ToNot(HaveOccurred())
				Expect(token).To(Equal("test-pat-token"))
			})

			It("should return error when PAT token is empty", func() {
				scmProvider := v1alpha1.ScmProvider{
					Spec: v1alpha1.ScmProviderSpec{
						AzureDevOps: &v1alpha1.AzureDevOps{Organization: "test-org"},
					},
				}
				secret := &v1.Secret{Data: map[string][]byte{"token": []byte("")}}

				authProvider := azuredevops.NewAzdoGitAuthenticationProvider(ctx, mockClient, &scmProvider, secret, client.ObjectKey{})
				_, err := authProvider.GetToken(ctx)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("azure DevOps Personal Access Token is empty"))
			})
		})

		Context("with workload identity authentication", func() {
			It("should handle workload identity when disabled", func() {
				scmProvider := v1alpha1.ScmProvider{
					Spec: v1alpha1.ScmProviderSpec{
						AzureDevOps: &v1alpha1.AzureDevOps{
							Organization: "test-org",
							WorkloadIdentity: &v1alpha1.AzureWorkloadIdentity{
								Enabled:  false, // Disabled
								TenantID: "test-tenant",
								ClientID: "test-client",
							},
						},
					},
				}
				secret := &v1.Secret{Data: map[string][]byte{"token": []byte("fallback-token")}}

				authProvider := azuredevops.NewAzdoGitAuthenticationProvider(ctx, mockClient, &scmProvider, secret, client.ObjectKey{})
				token, err := authProvider.GetToken(ctx)

				// Should fall back to PAT authentication
				Expect(err).ToNot(HaveOccurred())
				Expect(token).To(Equal("fallback-token"))
			})
		})
	})
})

var _ = Describe("GetClient", func() {
	var (
		ctx         context.Context
		scmProvider v1alpha1.ScmProvider
		secret      v1.Secret
	)

	BeforeEach(func() {
		ctx = context.Background()
		scmProvider = v1alpha1.ScmProvider{
			Spec: v1alpha1.ScmProviderSpec{
				AzureDevOps: &v1alpha1.AzureDevOps{
					Organization: "test-org",
				},
			},
		}
	})

	Context("with PAT authentication", func() {
		It("should create connection with PAT token", func() {
			secret = v1.Secret{
				Data: map[string][]byte{
					"token": []byte("test-pat-token"),
				},
			}

			connection, authProvider, err := azuredevops.GetClient(ctx, &scmProvider, secret, "test-org")

			Expect(err).ToNot(HaveOccurred())
			Expect(connection).ToNot(BeNil())
			Expect(authProvider).ToNot(BeNil())
		})

		It("should return error when PAT token is missing", func() {
			secret = v1.Secret{
				Data: map[string][]byte{},
			}

			_, _, err := azuredevops.GetClient(ctx, &scmProvider, secret, "test-org")

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("azure DevOps token not found in secret"))
		})
	})
})
