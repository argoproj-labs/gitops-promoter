package gitlab_test

import (
	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitlab"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("PullRequest", func() {
	Describe("FormatMergeRequestUrl", func() {
		type testCase struct {
			name        string
			domain      string
			namespace   string
			projectName string
			prID        string
			expectedUrl string
		}

		testCases := []testCase{
			{
				name:        "default gitlab.com domain",
				domain:      "",
				namespace:   "mygroup",
				projectName: "myproject",
				prID:        "123",
				expectedUrl: "https://gitlab.com/mygroup/myproject/-/merge_requests/123",
			},
			{
				name:        "custom domain",
				domain:      "gitlab.example.com",
				namespace:   "oam/k8s/addons",
				projectName: "addon-template",
				prID:        "23",
				expectedUrl: "https://gitlab.example.com/oam/k8s/addons/addon-template/-/merge_requests/23",
			},
			{
				name:        "custom domain with subgroup",
				domain:      "code.mycompany.com",
				namespace:   "team/subteam",
				projectName: "awesome-app",
				prID:        "456",
				expectedUrl: "https://code.mycompany.com/team/subteam/awesome-app/-/merge_requests/456",
			},
		}

		for _, tc := range testCases {
			It("should return correct URL for "+tc.name, func() {
				// Create test secret
				secret := &corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: "default",
					},
					Data: map[string][]byte{"token": []byte("fake-token")},
				}

				// Create GitLab client with the test domain
				client, err := gitlab.GetClient(*secret, tc.domain)
				Expect(err).NotTo(HaveOccurred())

				// Create GitLabRepo object
				gitlabRepo := &v1alpha1.GitLabRepo{
					Namespace: tc.namespace,
					Name:      tc.projectName,
					ProjectID: 12345,
				}

				// Test the URL formatting function directly
				url := gitlab.FormatMergeRequestUrl(client, gitlabRepo, tc.prID)
				Expect(url).To(Equal(tc.expectedUrl))
			})
		}
	})
})
