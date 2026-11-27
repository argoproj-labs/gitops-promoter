package gitlab_test

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitlab"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
					Name:      "test-secret",
					Namespace: "default",
					Data:      map[string][]byte{"token": []byte("fake-token")},
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

	Describe("request context", func() {
		var (
			provider *gitlab.PullRequest
			prObj    v1alpha1.PullRequest
		)

		BeforeEach(func() {
			// A server that accepts connections but never answers, so only the request context can end a call.
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			Expect(err).NotTo(HaveOccurred())
			var mu sync.Mutex
			var conns []net.Conn
			go func() {
				for {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					mu.Lock()
					conns = append(conns, conn)
					mu.Unlock()
				}
			}()
			DeferCleanup(func() {
				_ = listener.Close()
				mu.Lock()
				defer mu.Unlock()
				for _, conn := range conns {
					_ = conn.Close()
				}
			})

			scheme := runtime.NewScheme()
			Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
			gitRepo := &v1alpha1.GitRepository{
				ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "default"},
				Spec: v1alpha1.GitRepositorySpec{
					GitLab: &v1alpha1.GitLabRepo{Namespace: "group", Name: "repo", ProjectID: 1},
				},
			}
			k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(gitRepo).Build()
			secret := corev1.Secret{Data: map[string][]byte{"token": []byte("token")}}
			provider, err = gitlab.NewGitlabPullRequestProvider(k8sClient, secret, listener.Addr().String())
			Expect(err).NotTo(HaveOccurred())

			prObj = v1alpha1.PullRequest{
				ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
				Spec: v1alpha1.PullRequestSpec{
					RepositoryReference: v1alpha1.ObjectReference{Name: "repo"},
					SourceBranch:        "env-next",
					TargetBranch:        "env",
				},
			}
		})

		// callWithDeadline runs call with a short deadline and returns its error, failing if it outlives the deadline.
		callWithDeadline := func(call func(ctx context.Context, errCh chan<- error)) error {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			DeferCleanup(cancel)
			errCh := make(chan error, 1)
			go call(ctx, errCh)
			var err error
			Eventually(errCh).WithTimeout(5 * time.Second).Should(Receive(&err))
			return err
		}

		It("stops Create when the context ends", func() {
			err := callWithDeadline(func(ctx context.Context, errCh chan<- error) {
				_, err := provider.Create(ctx, "title", "env-next", "env", "description", prObj)
				errCh <- err
			})
			Expect(err).To(MatchError(context.DeadlineExceeded))
		})

		It("stops FindOpen when the context ends", func() {
			err := callWithDeadline(func(ctx context.Context, errCh chan<- error) {
				_, err := provider.FindOpen(ctx, prObj)
				errCh <- err
			})
			Expect(err).To(MatchError(context.DeadlineExceeded))
		})
	})
})
