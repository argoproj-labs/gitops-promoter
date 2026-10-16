package forgejo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	forgejo "codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v2"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
)

func TestPullRequest(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)
	RunSpecs(t, "Forgejo PullRequest Suite")
}

var _ = Describe("Merge", func() {
	DescribeTable("handles the SDK merge result", func(status int, accepted bool) {
		mergeCalls := 0
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v1/version", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"version":"10.0.0"}`))
		})
		mux.HandleFunc("GET /api/v1/repos/owner/repo/pulls/1", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"state":"open"}`))
		})
		mux.HandleFunc("POST /api/v1/repos/owner/repo/pulls/1/merge", func(w http.ResponseWriter, _ *http.Request) {
			mergeCalls++
			w.WriteHeader(status)
		})
		server := httptest.NewServer(mux)
		DeferCleanup(server.Close)

		sdkClient, err := forgejo.NewClient(server.URL, forgejo.SetHTTPClient(server.Client()))
		Expect(err).NotTo(HaveOccurred())
		scheme := runtime.NewScheme()
		Expect(promoterv1alpha1.AddToScheme(scheme)).To(Succeed())
		repo := &promoterv1alpha1.GitRepository{
			ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "default"},
			Spec: promoterv1alpha1.GitRepositorySpec{
				Forgejo: &promoterv1alpha1.ForgejoRepo{Owner: "owner", Name: "repo"},
			},
		}
		provider := &PullRequest{
			foregejoClient: sdkClient,
			k8sClient:      fake.NewClientBuilder().WithScheme(scheme).WithObjects(repo).Build(),
		}
		pr := promoterv1alpha1.PullRequest{
			ObjectMeta: metav1.ObjectMeta{Namespace: repo.Namespace},
			Spec: promoterv1alpha1.PullRequestSpec{
				RepositoryReference: promoterv1alpha1.ObjectReference{Name: repo.Name},
			},
			Status: promoterv1alpha1.PullRequestStatus{ID: "1"},
		}

		result, err := provider.Merge(context.Background(), pr)
		Expect(result).To(Equal(scms.MergeResult{}))
		Expect(mergeCalls).To(Equal(1))
		if accepted {
			Expect(err).NotTo(HaveOccurred())
			return
		}
		Expect(err).To(MatchError(fmt.Sprintf("forgejo refused to merge pull request 1: %d %s", status, http.StatusText(status))))

		By("allowing a subsequent merge attempt to succeed")
		status = http.StatusOK
		_, err = provider.Merge(context.Background(), pr)
		Expect(err).NotTo(HaveOccurred())
		Expect(mergeCalls).To(Equal(2))
	},
		Entry("completed merge", http.StatusOK, true),
		Entry("scheduled merge is not accepted by the SDK", http.StatusCreated, false),
		Entry("unexpected success status", http.StatusNoContent, false),
		Entry("forbidden merge", http.StatusForbidden, false),
		Entry("required checks pending", http.StatusMethodNotAllowed, false),
		Entry("stale head commit", http.StatusConflict, false),
		Entry("server failure", http.StatusInternalServerError, false),
	)
})
