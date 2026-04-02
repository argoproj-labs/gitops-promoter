package metrics

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

func TestMetrics(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)
	RunSpecs(t, "Metrics Suite")
}

var _ = Describe("Resource count metrics", func() {
	Describe("refreshKubernetesResourceCounts", func() {
		It("sets promoter_kubernetes_resources from list results per kind", func() {
			s := runtime.NewScheme()
			utilruntime.Must(scheme.AddToScheme(s))
			utilruntime.Must(promoterv1alpha1.AddToScheme(s))

			c := fake.NewClientBuilder().WithScheme(s).WithObjects(
				&promoterv1alpha1.PromotionStrategy{
					ObjectMeta: metav1.ObjectMeta{Name: "one", Namespace: "ns"},
				},
				&promoterv1alpha1.PromotionStrategy{
					ObjectMeta: metav1.ObjectMeta{Name: "two", Namespace: "ns"},
				},
				&promoterv1alpha1.GitRepository{
					ObjectMeta: metav1.ObjectMeta{Name: "repo-a", Namespace: "ns"},
				},
			).Build()

			refreshKubernetesResourceCounts(context.Background(), c, logr.Discard())

			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("PromotionStrategy"))).To(Equal(2.0))
			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("GitRepository"))).To(Equal(1.0))
			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("PullRequest"))).To(Equal(0.0))
		})
	})
})
