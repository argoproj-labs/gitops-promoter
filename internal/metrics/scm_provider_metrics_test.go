package metrics

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("RecordSCMCall", func() {
	It("records scm_calls_total with empty git_repository for provider-only scope", func() {
		provider := &promoterv1alpha1.ClusterScmProvider{
			ObjectMeta: metav1.ObjectMeta{Name: "scm-provider-metrics-test"},
		}
		RecordSCMCall(context.Background(), provider, SCMAPIPullRequest, SCMOperationListInstallations, 200, 50*time.Millisecond, nil)
		Expect(testutil.ToFloat64(scmCallsTotal.WithLabelValues("", "scm-provider-metrics-test", "ClusterScmProvider", "PullRequest", "list-installations", "200"))).To(Equal(1.0))

		RecordSCMCall(context.Background(), provider, SCMAPIPullRequest, SCMOperationListInstallations, 200, 25*time.Millisecond, nil)
		Expect(testutil.ToFloat64(scmCallsTotal.WithLabelValues("", "scm-provider-metrics-test", "ClusterScmProvider", "PullRequest", "list-installations", "200"))).To(Equal(2.0))
	})
})
