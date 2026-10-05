package bitbucket_datacenter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("PullRequest version handling", func() {
	var (
		server   *httptest.Server
		requests map[string]map[string]any
		prJSON   string
		provider *PullRequest
		prObj    v1alpha1.PullRequest
	)

	BeforeEach(func() {
		requests = map[string]map[string]any{}
		prJSON = `{"id":1,"version":0,"fromRef":{"displayId":"env-next"},"toRef":{"displayId":"env"}}`
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/activities") {
				if r.URL.Query().Get("start") == "0" {
					_, _ = io.WriteString(w, `{"values":[{"action":"APPROVED"}],"isLastPage":false,"nextPageStart":1}`)
					return
				}
				_, _ = io.WriteString(w, `{"values":[{"action":"MERGED","commit":{"id":"faff7dec56951c180943ac6861860e51a98eb313"}},{"action":"OPENED"}],"isLastPage":true}`)
				return
			}
			if r.Method == http.MethodGet {
				_, _ = io.WriteString(w, prJSON)
				return
			}
			var body map[string]any
			Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
			requests[r.Method] = body
			if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pull-requests") {
				w.WriteHeader(http.StatusCreated)
			}
			_, _ = io.WriteString(w, `{"id":1,"version":1}`)
		}))
		DeferCleanup(server.Close)

		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		gitRepo := &v1alpha1.GitRepository{
			ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "default"},
			Spec: v1alpha1.GitRepositorySpec{
				BitbucketDataCenter: &v1alpha1.BitbucketDataCenterRepo{Project: "PROJ", Name: "repo"},
			},
		}
		provider = &PullRequest{
			client:    &Client{httpClient: server.Client(), baseURL: server.URL, authHeader: "Bearer token"},
			k8sClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(gitRepo).Build(),
		}
		prObj = v1alpha1.PullRequest{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default"},
			Spec: v1alpha1.PullRequestSpec{
				RepositoryReference: v1alpha1.ObjectReference{Name: "repo"},
				Commit:              v1alpha1.CommitConfiguration{Message: "Promote\n\nPull-request-id: 1"},
			},
			Status: v1alpha1.PullRequestStatus{ID: "1"},
		}
	})

	It("omits the version when creating a pull request", func() {
		_, err := provider.Create(context.Background(), "title", "env-next", "env", "description", prObj)
		Expect(err).NotTo(HaveOccurred())
		Expect(requests[http.MethodPost]).NotTo(HaveKey("version"))
	})

	It("sends version 0 when updating a freshly created pull request", func() {
		Expect(provider.Update(context.Background(), "title", "description", prObj)).To(Succeed())
		Expect(requests[http.MethodPut]).To(HaveKeyWithValue("version", BeNumerically("==", 0)))
	})

	It("merges with a merge commit and the promoter's commit message", func() {
		_, err := provider.Merge(context.Background(), prObj)
		Expect(err).NotTo(HaveOccurred())
		Expect(requests[http.MethodPost]).To(HaveKeyWithValue("strategyId", "no-ff"))
		Expect(requests[http.MethodPost]).To(HaveKeyWithValue("message", "Promote\n\nPull-request-id: 1"))
	})

	It("takes the merge commit from the MERGED activity when the pull request has no mergeCommit property", func() {
		prJSON = `{"id":1,"version":3,"state":"MERGED","closedDate":1791237666432,"properties":null}`
		result, err := provider.Get(context.Background(), prObj)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.State).To(Equal(v1alpha1.PullRequestMerged))
		Expect(result.MergedTargetSHA).To(Equal("faff7dec56951c180943ac6861860e51a98eb313"))
	})

	It("prefers the mergeCommit property when the pull request has one", func() {
		prJSON = `{"id":1,"version":3,"state":"MERGED","properties":{"mergeCommit":{"id":"0123456789abcdef0123456789abcdef01234567"}}}`
		result, err := provider.Get(context.Background(), prObj)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.MergedTargetSHA).To(Equal("0123456789abcdef0123456789abcdef01234567"))
	})
})
