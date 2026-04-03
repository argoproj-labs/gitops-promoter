package metrics

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

func TestMetrics(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)
	RunSpecs(t, "Metrics Suite")
}

// listCallCounter wraps a client and counts List calls (each full refresh lists once per promoter kind).
type listCallCounter struct {
	client.Client
	n atomic.Int32
}

func (w *listCallCounter) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	w.n.Add(1)
	err := w.Client.List(ctx, list, opts...)
	if err != nil {
		return fmt.Errorf("error listing %s: %w", list.GetObjectKind().GroupVersionKind().Kind, err)
	}
	return nil
}

var _ = Describe("ResourceCountRunnable", func() {
	BeforeEach(func() {
		logf.SetLogger(logr.Discard())
	})

	buildClient := func() client.Client {
		s := runtime.NewScheme()
		utilruntime.Must(scheme.AddToScheme(s))
		utilruntime.Must(promoterv1alpha1.AddToScheme(s))
		return fake.NewClientBuilder().WithScheme(s).WithObjects(
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
	}

	It("returns an error when the client is nil", func() {
		r := NewResourceCountRunnable(nil)
		err := r.Start(context.Background())
		Expect(err).To(MatchError("resource count runnable client is nil"))
	})

	It("runs an immediate refresh, updates gauges, and refreshes again on the ticker until the context is cancelled", func() {
		wrapped := &listCallCounter{Client: buildClient()}
		r := NewResourceCountRunnable(wrapped)
		r.tickInterval = 25 * time.Millisecond

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan struct{})
		go func() {
			defer GinkgoRecover()
			Expect(r.Start(ctx)).To(Succeed())
			close(done)
		}()

		minLists := 2 * len(promoterResourceKinds)
		Eventually(func() int32 { return wrapped.n.Load() }).WithTimeout(3 * time.Second).WithPolling(5 * time.Millisecond).
			Should(BeNumerically(">=", minLists))

		Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("PromotionStrategy"))).To(Equal(2.0))
		Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("GitRepository"))).To(Equal(1.0))
		Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("PullRequest"))).To(Equal(0.0))

		cancel()
		Eventually(done).WithTimeout(2 * time.Second).Should(BeClosed())
	})
})
