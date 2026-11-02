package metrics

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// histogramSampleCount returns the number of observations on the series with the given label values.
func histogramSampleCount(vec *prometheus.HistogramVec, labelValues ...string) uint64 {
	metric, ok := vec.WithLabelValues(labelValues...).(prometheus.Metric)
	Expect(ok).To(BeTrue())
	var m dto.Metric
	Expect(metric.Write(&m)).To(Succeed())
	return m.GetHistogram().GetSampleCount()
}

var _ = Describe("SetGitRepositoryLabels", func() {
	repo := func(name string) *promoterv1alpha1.GitRepository {
		return &promoterv1alpha1.GitRepository{
			Name: name, Namespace: "repo-labels-test",
			Spec: promoterv1alpha1.GitRepositorySpec{
				ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{Name: "repo-labels-provider"},
			},
		}
	}
	record := func() {
		for _, name := range []string{"repo-a", "repo-b"} {
			RecordGitOperation(repo(name), GitOperationFetch, GitOperationResultSuccess, time.Millisecond)
			RecordSCMCall(context.Background(), repo(name), SCMAPIPullRequest, SCMOperationGet, 200, time.Millisecond, nil)
		}
	}
	gitLabels := func(name, namespace string) []string {
		return []string{name, namespace, "repo-labels-provider", "repo-labels-test", "ScmProvider", "fetch", "success"}
	}
	scmLabels := func(name, namespace string) []string {
		return []string{name, namespace, "repo-labels-provider", "repo-labels-test", "ScmProvider", "PullRequest", "get", "200"}
	}

	BeforeEach(func() {
		gitOperationsTotal.Reset()
		gitOperationsDurationSeconds.Reset()
		scmCallsTotal.Reset()
		scmCallsDurationSeconds.Reset()
		DeferCleanup(SetGitRepositoryLabels, promoterv1alpha1.MetricLabelModeAll)
	})

	It("keeps repository labels on counters and aggregates histograms with Counters", func() {
		SetGitRepositoryLabels(promoterv1alpha1.MetricLabelModeCounters)
		record()

		Expect(testutil.ToFloat64(gitOperationsTotal.WithLabelValues(gitLabels("repo-a", "repo-labels-test")...))).To(Equal(1.0))
		Expect(testutil.ToFloat64(gitOperationsTotal.WithLabelValues(gitLabels("repo-b", "repo-labels-test")...))).To(Equal(1.0))
		Expect(testutil.ToFloat64(scmCallsTotal.WithLabelValues(scmLabels("repo-a", "repo-labels-test")...))).To(Equal(1.0))
		Expect(testutil.ToFloat64(scmCallsTotal.WithLabelValues(scmLabels("repo-b", "repo-labels-test")...))).To(Equal(1.0))

		Expect(testutil.CollectAndCount(gitOperationsDurationSeconds)).To(Equal(1))
		Expect(testutil.CollectAndCount(scmCallsDurationSeconds)).To(Equal(1))
		Expect(histogramSampleCount(gitOperationsDurationSeconds, gitLabels("", "")...)).To(Equal(uint64(2)))
		Expect(histogramSampleCount(scmCallsDurationSeconds, scmLabels("", "")...)).To(Equal(uint64(2)))
	})

	It("aggregates counters and histograms with None", func() {
		SetGitRepositoryLabels(promoterv1alpha1.MetricLabelModeNone)
		record()

		Expect(testutil.CollectAndCount(gitOperationsTotal)).To(Equal(1))
		Expect(testutil.CollectAndCount(scmCallsTotal)).To(Equal(1))
		Expect(testutil.ToFloat64(gitOperationsTotal.WithLabelValues(gitLabels("", "")...))).To(Equal(2.0))
		Expect(testutil.ToFloat64(scmCallsTotal.WithLabelValues(scmLabels("", "")...))).To(Equal(2.0))
		Expect(testutil.CollectAndCount(gitOperationsDurationSeconds)).To(Equal(1))
		Expect(testutil.CollectAndCount(scmCallsDurationSeconds)).To(Equal(1))
	})

	It("resets only the metrics whose labels change", func() {
		SetGitRepositoryLabels(promoterv1alpha1.MetricLabelModeCounters)
		record()

		SetGitRepositoryLabels(promoterv1alpha1.MetricLabelModeAll)

		Expect(testutil.CollectAndCount(gitOperationsDurationSeconds)).To(Equal(0))
		Expect(testutil.CollectAndCount(scmCallsDurationSeconds)).To(Equal(0))
		Expect(testutil.ToFloat64(gitOperationsTotal.WithLabelValues(gitLabels("repo-a", "repo-labels-test")...))).To(Equal(1.0))
		Expect(testutil.ToFloat64(scmCallsTotal.WithLabelValues(scmLabels("repo-a", "repo-labels-test")...))).To(Equal(1.0))

		record()
		Expect(histogramSampleCount(gitOperationsDurationSeconds, gitLabels("repo-a", "repo-labels-test")...)).To(Equal(uint64(1)))
		Expect(histogramSampleCount(scmCallsDurationSeconds, scmLabels("repo-b", "repo-labels-test")...)).To(Equal(uint64(1)))
	})
})
