package azuredevops

import (
	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("PullRequest", func() {
	Describe("generatePullRequestUrl", func() {
		table := []struct {
			name         string
			domain       string
			organization string
			project      string
			repoName     string
			expectedUrl  string
			prId         int
		}{
			{
				name:         "default dev.azure.com domain",
				domain:       "",
				organization: "myorg",
				project:      "myproject",
				repoName:     "myrepo",
				prId:         123,
				expectedUrl:  "https://dev.azure.com/myorg/myproject/_git/myrepo/pullrequest/123",
			},
			{
				name:         "custom domain",
				domain:       "devops.example.com",
				organization: "myorg",
				project:      "myproject",
				repoName:     "myrepo",
				prId:         456,
				expectedUrl:  "https://devops.example.com/myorg/myproject/_git/myrepo/pullrequest/456",
			},
		}

		for _, tc := range table {
			It("should generate the correct URL for "+tc.name, func(ctx SpecContext) {
				namespace := "default"
				resourceName := "test-resource"

				scheme := runtime.NewScheme()
				Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
				Expect(corev1.AddToScheme(scheme)).To(Succeed())

				scmProvider := &v1alpha1.ScmProvider{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: namespace,
					},
					Spec: v1alpha1.ScmProviderSpec{
						SecretRef: &corev1.LocalObjectReference{Name: resourceName},
						AzureDevOps: &v1alpha1.AzureDevOps{
							Organization: tc.organization,
							Domain:       tc.domain,
						},
					},
				}

				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: namespace,
					},
					Data: map[string][]byte{"token": []byte("fake-token")},
				}

				gitRepo := &v1alpha1.GitRepository{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: namespace,
					},
					Spec: v1alpha1.GitRepositorySpec{
						AzureDevOps: &v1alpha1.AzureDevOpsRepo{
							Project: tc.project,
							Name:    tc.repoName,
						},
						ScmProviderRef: v1alpha1.ScmProviderObjectReference{
							Kind: v1alpha1.ScmProviderKind,
							Name: resourceName,
						},
					},
				}

				k8sClient := fake.NewClientBuilder().
					WithScheme(scheme).
					WithObjects(scmProvider, secret, gitRepo).
					Build()

				pr := &PullRequest{
					k8sClient: k8sClient,
				}

				prObj := v1alpha1.PullRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-pr",
						Namespace: namespace,
					},
					Spec: v1alpha1.PullRequestSpec{
						RepositoryReference: v1alpha1.ObjectReference{
							Name: resourceName,
						},
					},
				}

				url, err := pr.generatePullRequestUrl(ctx, prObj, tc.prId)
				Expect(err).NotTo(HaveOccurred())
				Expect(url).To(Equal(tc.expectedUrl))
			})
		}
	})
})
