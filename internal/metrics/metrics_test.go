package metrics

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("RecordSCMCall", func() {
	var (
		repo            *v1alpha1.GitRepository
		labels          prometheus.Labels
		rateLimitLabels prometheus.Labels
	)

	BeforeEach(func() {
		repo = &v1alpha1.GitRepository{
			ObjectMeta: metav1.ObjectMeta{Name: "repo1"},
			Spec: v1alpha1.GitRepositorySpec{
				ScmProviderRef: v1alpha1.ScmProviderObjectReference{Name: "github"},
			},
		}
		labels = prometheus.Labels{
			"git_repository": "repo1",
			"scm_provider":   "github",
			"api":            string(SCMAPICommitStatus),
			"operation":      string(SCMOperationCreate),
			"response_code":  "200",
		}
		rateLimitLabels = prometheus.Labels{
			"scm_provider": "github",
		}
	})

	DescribeTable("should record SCM call metrics",
		func(rateLimit *RateLimit, countTotal, rateLimitLimit, rateLimitRemaining, rateLimitResetRemaining float64) {
			RecordSCMCall(repo, SCMAPICommitStatus, SCMOperationCreate, 200, 1*time.Second, rateLimit)
			Expect(testutil.ToFloat64(scmCallsTotal.With(labels))).To(Equal(countTotal))
			if rateLimit != nil {
				Expect(testutil.ToFloat64(scmCallsRateLimitLimit.With(rateLimitLabels))).To(Equal(rateLimitLimit))
				Expect(testutil.ToFloat64(scmCallsRateLimitRemaining.With(rateLimitLabels))).To(Equal(rateLimitRemaining))
				Expect(testutil.ToFloat64(scmCallsRateLimitResetRemainingSeconds.With(rateLimitLabels))).To(Equal(rateLimitResetRemaining))
			}
		},
		Entry("with rate limit", &RateLimit{Limit: 10, Remaining: 5, ResetRemaining: 30 * time.Second}, 1.0, 10.0, 5.0, 30.0),
		Entry("without rate limit", nil, 2.0, 0.0, 0.0, 0.0),
		Entry("without rate limit", nil, 3.0, 0.0, 0.0, 0.0),
	)
})
