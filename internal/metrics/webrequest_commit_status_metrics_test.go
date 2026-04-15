package metrics

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("WebRequestCommitStatus HTTP metrics", func() {
	It("records counter increments per namespace, name, and response_code", func() {
		wrcs := &promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Namespace: "wrcs-metrics-ns", Name: "wrcs-a"},
		}
		RecordWebRequestCommitStatusHTTPRequest(wrcs, 200, 100*time.Millisecond)
		Expect(testutil.ToFloat64(webRequestCommitStatusHTTPRequestsTotal.WithLabelValues("wrcs-metrics-ns", "wrcs-a", "200"))).To(Equal(1.0))

		RecordWebRequestCommitStatusHTTPRequest(wrcs, 200, 50*time.Millisecond)
		Expect(testutil.ToFloat64(webRequestCommitStatusHTTPRequestsTotal.WithLabelValues("wrcs-metrics-ns", "wrcs-a", "200"))).To(Equal(2.0))

		RecordWebRequestCommitStatusHTTPRequest(wrcs, 0, time.Millisecond)
		Expect(testutil.ToFloat64(webRequestCommitStatusHTTPRequestsTotal.WithLabelValues("wrcs-metrics-ns", "wrcs-a", "0"))).To(Equal(1.0))

		wrcsOther := &promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Namespace: "wrcs-metrics-ns", Name: "wrcs-b"},
		}
		RecordWebRequestCommitStatusHTTPRequest(wrcsOther, 404, time.Millisecond)
		Expect(testutil.ToFloat64(webRequestCommitStatusHTTPRequestsTotal.WithLabelValues("wrcs-metrics-ns", "wrcs-b", "404"))).To(Equal(1.0))
	})
})
