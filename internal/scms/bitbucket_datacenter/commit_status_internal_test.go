package bitbucket_datacenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("CommitStatus", func() {
	const sha = "0123456789abcdef0123456789abcdef01234567"

	var (
		server   *httptest.Server
		path     string
		body     map[string]any
		status   int
		provider *CommitStatus
		cs       *v1alpha1.CommitStatus
	)

	BeforeEach(func() {
		path, body, status = "", nil, http.StatusNoContent
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path = r.Method + " " + r.URL.EscapedPath()
			Expect(json.NewDecoder(r.Body).Decode(&body)).To(Succeed())
			w.WriteHeader(status)
		}))
		DeferCleanup(server.Close)

		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		gitRepo := &v1alpha1.GitRepository{
			ObjectMeta: metav1.ObjectMeta{Name: "repo", Namespace: "default"},
			Spec: v1alpha1.GitRepositorySpec{
				BitbucketDataCenter: &v1alpha1.BitbucketDataCenterRepo{Project: "PROJ", Name: "my repo"},
			},
		}
		provider = &CommitStatus{
			client:    &Client{httpClient: server.Client(), baseURL: server.URL, authHeader: "Bearer token"},
			k8sClient: fake.NewClientBuilder().WithScheme(scheme).WithObjects(gitRepo).Build(),
		}
		cs = &v1alpha1.CommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: "gate", Namespace: "default"},
			Spec: v1alpha1.CommitStatusSpec{
				RepositoryReference: v1alpha1.ObjectReference{Name: "repo"},
				Sha:                 sha,
				Name:                "promoter-gate",
				Description:         "checked by the promoter",
				Phase:               v1alpha1.CommitPhaseSuccess,
				Url:                 "https://ci.example.com/gate",
			},
		}
	})

	// The Required Builds merge check reads only the repository builds resource, not the deprecated
	// /rest/build-status resource, and matches builds by their parent key.
	It("reports the build through the repository builds resource, with the status name as its parent key", func() {
		updated, err := provider.Set(context.Background(), cs)
		Expect(err).NotTo(HaveOccurred())
		Expect(path).To(Equal("POST /rest/api/1.0/projects/PROJ/repos/my%20repo/commits/" + sha + "/builds"))
		Expect(body).To(Equal(map[string]any{
			"key": "promoter-gate", "parent": "promoter-gate", "name": "promoter-gate", "state": "SUCCESSFUL",
			"url": "https://ci.example.com/gate", "description": "checked by the promoter",
		}))
		Expect(updated.Status.Phase).To(Equal(v1alpha1.CommitPhaseSuccess))
		Expect(updated.Status.Sha).To(Equal(sha))
	})

	It("links the commit in the Bitbucket UI when the status has no URL", func() {
		cs.Spec.Url = ""
		_, err := provider.Set(context.Background(), cs)
		Expect(err).NotTo(HaveOccurred())
		Expect(body).To(HaveKeyWithValue("url", server.URL+"/projects/PROJ/repos/my%20repo/commits/"+sha))
	})

	It("returns an error with the response when the build is rejected", func() {
		status = http.StatusBadRequest
		_, err := provider.Set(context.Background(), cs)
		Expect(err).To(MatchError(ContainSubstring("400")))
	})
})
