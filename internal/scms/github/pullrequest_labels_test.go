package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"time"

	"github.com/google/go-github/v93/github"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
)

var _ = Describe("repository label listing", func() {
	It("records each ListLabels page on scm_calls_total and the rate-limit gauges", func() {
		const (
			repoName     = "label-list-pages-gr"
			providerName = "label-list-pages-scm"
		)
		resetAtSec := time.Now().Add(2 * time.Hour).Unix()
		var calls int
		srv := labelListServer(func(page int, w http.ResponseWriter) {
			calls++
			writeLabelPage(w, page, resetAtSec, []labelPage{
				{names: []string{"bug"}, remaining: 4999, hasNext: true},
				{names: []string{"ship"}, remaining: 4998, hasNext: false},
			})
		})
		DeferCleanup(srv.Close)

		pr := newLabelListPullRequest(srv.URL)
		gitRepo := labelListGitRepo(repoName, providerName)

		before := scmCallCount(repoName, providerName, "200")
		Expect(pr.ensureRepositoryLabels(context.Background(), gitRepo, []string{"ship"})).To(Succeed())
		Expect(calls).To(Equal(2))
		Expect(scmCallCount(repoName, providerName, "200") - before).To(Equal(2.0))
		Expect(scmRateLimitGauge("scm_calls_rate_limit_limit", providerName)).To(Equal(5000.0))
		Expect(scmRateLimitGauge("scm_calls_rate_limit_remaining", providerName)).To(Equal(4998.0))
		Expect(scmRateLimitGauge("scm_calls_rate_limit_reset_remaining_seconds", providerName)).To(BeNumerically("~", 2*time.Hour.Seconds(), 5))
	})

	It("records the ListLabels page fetched while checking whether a label exists", func() {
		const (
			repoName     = "label-list-stop-gr"
			providerName = "label-list-stop-scm"
		)
		resetAtSec := time.Now().Add(time.Hour).Unix()
		var calls int
		srv := labelListServer(func(page int, w http.ResponseWriter) {
			calls++
			writeLabelPage(w, page, resetAtSec, []labelPage{
				{names: []string{"ship"}, remaining: 100, hasNext: true},
				{names: []string{"other"}, remaining: 99, hasNext: false},
			})
		})
		DeferCleanup(srv.Close)

		pr := newLabelListPullRequest(srv.URL)
		gitRepo := labelListGitRepo(repoName, providerName)
		before := scmCallCount(repoName, providerName, "200")
		found, err := pr.repositoryHasLabel(context.Background(), gitRepo, "acme", "widgets", "ship")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(calls).To(Equal(1))
		Expect(scmCallCount(repoName, providerName, "200") - before).To(Equal(1.0))
		Expect(scmRateLimitGauge("scm_calls_rate_limit_remaining", providerName)).To(Equal(100.0))
	})

	It("records a failed ListLabels response", func() {
		const (
			repoName     = "label-list-error-gr"
			providerName = "label-list-error-scm"
		)
		resetAtSec := time.Now().Add(30 * time.Minute).Unix()
		srv := labelListServer(func(_ int, w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(github.HeaderRateLimit, "5000")
			w.Header().Set(github.HeaderRateRemaining, "10")
			w.Header().Set(github.HeaderRateReset, strconv.FormatInt(resetAtSec, 10))
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"message":"unavailable"}`))
		})
		DeferCleanup(srv.Close)

		pr := newLabelListPullRequest(srv.URL)
		gitRepo := labelListGitRepo(repoName, providerName)
		before := scmCallCount(repoName, providerName, "503")
		Expect(pr.ensureRepositoryLabels(context.Background(), gitRepo, []string{"ship"})).NotTo(Succeed())
		Expect(scmCallCount(repoName, providerName, "503") - before).To(Equal(1.0))
		Expect(scmRateLimitGauge("scm_calls_rate_limit_remaining", providerName)).To(Equal(10.0))
	})
})

var _ = Describe("check run metrics", func() {
	newCheckRunCommitStatus := func(repoName, providerName string, status int, remaining string) *CommitStatus {
		GinkgoHelper()
		mux := http.NewServeMux()
		handler := func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set(github.HeaderRateLimit, "5000")
			w.Header().Set(github.HeaderRateRemaining, remaining)
			w.Header().Set(github.HeaderRateReset, strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10))
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"message":"denied"}`))
		}
		mux.HandleFunc("/api/v3/repos/acme/widgets/check-runs", handler)
		mux.HandleFunc("/api/v3/repos/acme/widgets/check-runs/{id}", handler)
		srv := httptest.NewTLSServer(mux)
		DeferCleanup(srv.Close)

		ghClient, err := github.NewClient(github.WithEnterpriseURLs(srv.URL, srv.URL))
		Expect(err).NotTo(HaveOccurred())
		scheme := runtime.NewScheme()
		Expect(v1alpha1.AddToScheme(scheme)).To(Succeed())
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(labelListGitRepo(repoName, providerName)).Build()
		return &CommitStatus{client: ghClient, k8sClient: k8sClient}
	}

	checkRunCallCount := func(repoName, providerName string, operation metrics.SCMOperation, responseCode string) float64 {
		GinkgoHelper()
		return prometheusSample("scm_calls_total", map[string]string{
			"git_repository":    repoName,
			"scm_provider":      providerName,
			"scm_provider_kind": v1alpha1.ScmProviderKind,
			"api":               string(metrics.SCMAPICommitStatus),
			"operation":         string(operation),
			"response_code":     responseCode,
		})
	}

	commitStatusFor := func(repoName, id string) *v1alpha1.CommitStatus {
		cs := &v1alpha1.CommitStatus{}
		cs.Namespace = "default"
		cs.Spec.RepositoryReference.Name = repoName
		cs.Spec.Name = "check"
		cs.Spec.Sha = "abc123"
		cs.Spec.Phase = v1alpha1.CommitPhasePending
		if id != "" {
			cs.Status.Id = id
			cs.Status.Sha = "abc123"
			cs.Status.Phase = v1alpha1.CommitPhasePending
		}
		return cs
	}

	It("records a failed CreateCheckRun call", func() {
		const (
			repoName     = "check-run-create-error-gr"
			providerName = "check-run-create-error-scm"
		)
		cs := newCheckRunCommitStatus(repoName, providerName, http.StatusForbidden, "7")
		before := checkRunCallCount(repoName, providerName, metrics.SCMOperationCreate, "403")
		_, err := cs.Set(context.Background(), commitStatusFor(repoName, ""))
		Expect(err).To(HaveOccurred())
		Expect(checkRunCallCount(repoName, providerName, metrics.SCMOperationCreate, "403") - before).To(Equal(1.0))
		Expect(scmRateLimitGauge("scm_calls_rate_limit_remaining", providerName)).To(Equal(7.0))
	})

	It("records a failed UpdateCheckRun call", func() {
		const (
			repoName     = "check-run-update-error-gr"
			providerName = "check-run-update-error-scm"
		)
		cs := newCheckRunCommitStatus(repoName, providerName, http.StatusUnprocessableEntity, "8")
		before := checkRunCallCount(repoName, providerName, metrics.SCMOperationUpdate, "422")
		_, err := cs.Set(context.Background(), commitStatusFor(repoName, "42"))
		Expect(err).To(HaveOccurred())
		Expect(checkRunCallCount(repoName, providerName, metrics.SCMOperationUpdate, "422") - before).To(Equal(1.0))
		Expect(scmRateLimitGauge("scm_calls_rate_limit_remaining", providerName)).To(Equal(8.0))
	})
})

type labelPage struct {
	names     []string
	remaining int
	hasNext   bool
}

func labelListServer(handler func(page int, w http.ResponseWriter)) *httptest.Server {
	GinkgoHelper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/repos/acme/widgets/labels", func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		handler(page, w)
	})
	srv := httptest.NewTLSServer(mux)
	return srv
}

func writeLabelPage(w http.ResponseWriter, page int, resetAtSec int64, pages []labelPage) {
	GinkgoHelper()
	Expect(page).To(BeNumerically(">=", 1))
	Expect(page).To(BeNumerically("<=", len(pages)))
	current := pages[page-1]
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(github.HeaderRateLimit, "5000")
	w.Header().Set(github.HeaderRateRemaining, strconv.Itoa(current.remaining))
	w.Header().Set(github.HeaderRateReset, strconv.FormatInt(resetAtSec, 10))
	if current.hasNext {
		w.Header().Set("Link", fmt.Sprintf(`<https://example.com/api/v3/repos/acme/widgets/labels?page=%d>; rel="next"`, page+1))
	}
	body := make([]map[string]string, 0, len(current.names))
	for _, name := range current.names {
		body = append(body, map[string]string{"name": name})
	}
	encoded, err := json.Marshal(body)
	Expect(err).NotTo(HaveOccurred())
	_, err = w.Write(encoded)
	Expect(err).NotTo(HaveOccurred())
}

func newLabelListPullRequest(serverURL string) *PullRequest {
	GinkgoHelper()
	client, err := github.NewClient(github.WithEnterpriseURLs(serverURL, serverURL))
	Expect(err).NotTo(HaveOccurred())
	return &PullRequest{client: client}
}

func labelListGitRepo(name, providerName string) *v1alpha1.GitRepository {
	return &v1alpha1.GitRepository{
		Name: name, Namespace: "default",
		Spec: v1alpha1.GitRepositorySpec{
			GitHub: &v1alpha1.GitHubRepo{Owner: "acme", Name: "widgets"},
			ScmProviderRef: v1alpha1.ScmProviderObjectReference{
				Kind: v1alpha1.ScmProviderKind,
				Name: providerName,
			},
		},
	}
}

func scmCallCount(gitRepository, provider, responseCode string) float64 {
	GinkgoHelper()
	return prometheusSample("scm_calls_total", map[string]string{
		"git_repository":    gitRepository,
		"scm_provider":      provider,
		"scm_provider_kind": v1alpha1.ScmProviderKind,
		"api":               string(metrics.SCMAPIPullRequest),
		"operation":         string(metrics.SCMOperationListLabels),
		"response_code":     responseCode,
	})
}

func scmRateLimitGauge(name, provider string) float64 {
	GinkgoHelper()
	return prometheusSample(name, map[string]string{
		"scm_provider":      provider,
		"scm_provider_kind": v1alpha1.ScmProviderKind,
	})
}

func prometheusSample(name string, want map[string]string) float64 {
	GinkgoHelper()
	families, err := ctrlmetrics.Registry.Gather()
	Expect(err).NotTo(HaveOccurred())
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, metric := range family.GetMetric() {
			if !metricLabelsMatch(metric, want) {
				continue
			}
			if counter := metric.GetCounter(); counter != nil {
				return counter.GetValue()
			}
			if gauge := metric.GetGauge(); gauge != nil {
				return gauge.GetValue()
			}
		}
	}
	return 0
}

func metricLabelsMatch(metric *dto.Metric, want map[string]string) bool {
	got := make(map[string]string, len(metric.GetLabel()))
	for _, label := range metric.GetLabel() {
		got[label.GetName()] = label.GetValue()
	}
	for key, value := range want {
		if got[key] != value {
			return false
		}
	}
	return true
}
