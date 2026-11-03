package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// listedPull is a pull request as the stub list endpoint returns it.
type listedPull struct {
	headRepo string // full name of the head repository; empty renders a deleted fork (null repo)
	headRef  string
	number   int
}

var _ = Describe("FindOpen", func() {
	const (
		source = "environment/development-next"
		target = "environment/development"
	)

	// findOpenServer serves the pulls list of acme/widgets from pages and records each request's query.
	findOpenServer := func(pages [][]listedPull) (*httptest.Server, func() []url.Values) {
		var (
			mu      sync.Mutex
			queries []url.Values
		)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer GinkgoRecover()
			Expect(r.URL.Path).To(Equal("/api/v3/repos/acme/widgets/pulls"))
			mu.Lock()
			queries = append(queries, r.URL.Query())
			mu.Unlock()
			page := 1
			if p := r.URL.Query().Get("page"); p != "" {
				var err error
				page, err = strconv.Atoi(p)
				Expect(err).NotTo(HaveOccurred())
			}
			if page < len(pages) {
				next := *r.URL
				q := next.Query()
				q.Set("page", strconv.Itoa(page+1))
				next.RawQuery = q.Encode()
				w.Header().Set("Link", fmt.Sprintf(`<http://%s%s>; rel="next"`, r.Host, next.RequestURI()))
			}
			out := make([]map[string]any, 0, len(pages[page-1]))
			for _, p := range pages[page-1] {
				var repo any
				if p.headRepo != "" {
					repo = map[string]any{"full_name": p.headRepo}
				}
				out = append(out, map[string]any{
					"number":     p.number,
					"created_at": "2026-11-02T00:00:00Z",
					"head":       map[string]any{"ref": p.headRef, "repo": repo},
					"base":       map[string]any{"ref": target},
				})
			}
			w.Header().Set("Content-Type", "application/json")
			Expect(json.NewEncoder(w).Encode(out)).To(Succeed())
		}))
		DeferCleanup(srv.Close)
		return srv, func() []url.Values {
			mu.Lock()
			defer mu.Unlock()
			return queries
		}
	}

	findOpen := func(serverURL string) (string, bool) {
		GinkgoHelper()
		gitRepo := labelListGitRepo("find-open-gr", "find-open-scm")
		pr := newLabelListPullRequest(serverURL)
		pr.k8sClient = fake.NewClientBuilder().WithScheme(utils.GetScheme()).WithObjects(gitRepo).Build()
		res, err := pr.FindOpen(context.Background(), v1alpha1.PullRequest{
			Namespace: "default",
			Spec: v1alpha1.PullRequestSpec{
				RepositoryReference: v1alpha1.ObjectReference{Name: gitRepo.Name},
				SourceBranch:        source,
				TargetBranch:        target,
			},
		})
		Expect(err).NotTo(HaveOccurred())
		return res.ID, res.Found
	}

	It("matches the head by repository and branch instead of GitHub's owner:branch head filter", func() {
		srv, queries := findOpenServer([][]listedPull{
			{
				{number: 7, headRepo: "someone/widgets", headRef: source}, // same branch name on another fork
				{number: 8, headRepo: "acme/widgets", headRef: "environment/staging-next"},
				{number: 6, headRef: source}, // head fork deleted
			},
			{
				{number: 9, headRepo: "ACME/Widgets", headRef: source},
			},
		})

		id, found := findOpen(srv.URL)
		Expect(found).To(BeTrue())
		Expect(id).To(Equal("9"))

		Expect(queries()).To(HaveLen(2), "FindOpen follows pagination")
		for _, q := range queries() {
			Expect(q.Has("head")).To(BeFalse(), "the head filter names an owner, which is ambiguous in a fork network")
			Expect(q.Get("base")).To(Equal(target))
			Expect(q.Get("state")).To(Equal("open"))
		}
	})

	It("reports not found when only other repositories have an open pull request from the branch", func() {
		srv, _ := findOpenServer([][]listedPull{{
			{number: 7, headRepo: "someone/widgets", headRef: source},
			{number: 8, headRepo: "acme/widgets-fork", headRef: source},
		}})

		_, found := findOpen(srv.URL)
		Expect(found).To(BeFalse())
	})
})
