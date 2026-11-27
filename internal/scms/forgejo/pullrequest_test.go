package forgejo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	forgejo "codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("FindOpen", func() {
	pullJSON := func(index int, head, base string) string {
		return fmt.Sprintf(`{"number":%d,"head":{"label":%[2]q,"ref":%[2]q},"base":{"label":%[3]q,"ref":%[3]q},"created_at":"2024-01-01T00:00:00Z"}`, index, head, base)
	}

	It("finds a matching open pull request beyond the first page", func() {
		pageOne := make([]string, 0, pullRequestPageSize)
		for i := 1; i <= pullRequestPageSize; i++ {
			pageOne = append(pageOne, pullJSON(i, fmt.Sprintf("other-%d-next", i), fmt.Sprintf("other-%d", i)))
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/api/v1/version":
				_, _ = io.WriteString(w, `{"version":"1.22.0"}`)
			case strings.HasSuffix(r.URL.Path, "/pulls") && r.URL.Query().Get("page") == "2":
				_, _ = io.WriteString(w, "["+pullJSON(51, "env-next", "env")+"]")
			case strings.HasSuffix(r.URL.Path, "/pulls"):
				_, _ = io.WriteString(w, "["+strings.Join(pageOne, ",")+"]")
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		DeferCleanup(server.Close)

		client, err := forgejo.NewClient(server.URL, forgejo.SetToken("token"))
		Expect(err).NotTo(HaveOccurred())

		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		gitRepo := &v1alpha1.GitRepository{
			ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "default"},
			Spec: v1alpha1.GitRepositorySpec{
				Forgejo: &v1alpha1.ForgejoRepo{Owner: "owner", Name: "repo"},
			},
		}
		provider := &PullRequest{
			foregejoClient: client,
			k8sClient:      fake.NewClientBuilder().WithScheme(scheme).WithObjects(gitRepo).Build(),
		}

		result, err := provider.FindOpen(context.Background(), v1alpha1.PullRequest{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
			Spec: v1alpha1.PullRequestSpec{
				RepositoryReference: v1alpha1.ObjectReference{Name: "repo"},
				SourceBranch:        "env-next",
				TargetBranch:        "env",
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Found).To(BeTrue())
		Expect(result.ID).To(Equal("51"))
	})
})
