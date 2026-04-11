package metrics

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

func TestMetrics(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)
	RunSpecs(t, "Metrics Suite")
}

var errInjectedGetInformer = errors.New("injected get informer failure")

// stubResourceCountInformerSource implements resourceCountInformerSource for tests.
type stubResourceCountInformerSource struct {
	scheme   *runtime.Scheme
	gvkErr   map[schema.GroupVersionKind]error
	gvkInf   map[schema.GroupVersionKind]toolscache.SharedIndexInformer
	getCount *atomic.Int32
}

func (s *stubResourceCountInformerSource) GetInformer(ctx context.Context, obj client.Object, opts ...cache.InformerGetOption) (cache.Informer, error) {
	if s.getCount != nil {
		s.getCount.Add(1)
	}
	gvk, err := apiutil.GVKForObject(obj, s.scheme)
	if err != nil {
		return nil, fmt.Errorf("gvk for object: %w", err)
	}
	if e, ok := s.gvkErr[gvk]; ok {
		return nil, e
	}
	if inf, ok := s.gvkInf[gvk]; ok {
		return inf, nil
	}
	return nil, fmt.Errorf("stub: no informer for %v", gvk)
}

func informerWithExampleAndItems(example runtime.Object, items ...runtime.Object) toolscache.SharedIndexInformer {
	inf := toolscache.NewSharedIndexInformer(
		&toolscache.ListWatch{},
		example,
		0,
		toolscache.Indexers{toolscache.NamespaceIndex: toolscache.MetaNamespaceIndexFunc},
	)
	for _, it := range items {
		_ = inf.GetStore().Add(it)
	}
	return inf
}

func testMetricsScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(scheme.AddToScheme(s))
	utilruntime.Must(promoterv1alpha1.AddToScheme(s))
	return s
}

func gvkKey(sc *runtime.Scheme, obj client.Object) schema.GroupVersionKind {
	gvk, err := apiutil.GVKForObject(obj, sc)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return gvk
}

func buildStubInformerSourceWithCounts() *stubResourceCountInformerSource {
	s := testMetricsScheme()
	gvkInf := make(map[schema.GroupVersionKind]toolscache.SharedIndexInformer)
	for _, r := range promoterResources {
		gvk := gvkKey(s, r.obj)
		var items []runtime.Object
		switch r.kind {
		case "PromotionStrategy":
			items = []runtime.Object{
				&promoterv1alpha1.PromotionStrategy{ObjectMeta: metav1.ObjectMeta{Name: "one", Namespace: "ns"}},
				&promoterv1alpha1.PromotionStrategy{ObjectMeta: metav1.ObjectMeta{Name: "two", Namespace: "ns"}},
			}
		case "GitRepository":
			items = []runtime.Object{
				&promoterv1alpha1.GitRepository{ObjectMeta: metav1.ObjectMeta{Name: "repo-a", Namespace: "ns"}},
			}
		default:
			items = nil
		}
		gvkInf[gvk] = informerWithExampleAndItems(r.obj, items...)
	}
	return &stubResourceCountInformerSource{scheme: s, gvkInf: gvkInf}
}

func resetPromoterKubernetesResourceGauges() {
	for _, r := range promoterResources {
		kubernetesResources.DeleteLabelValues(r.kind)
	}
}

var _ = Describe("Resource count metrics", func() {
	BeforeEach(func() {
		resetPromoterKubernetesResourceGauges()
	})

	Describe("refreshKubernetesResourceCounts", func() {
		It("logs an error and sets the gauge to zero when getting an informer fails", func() {
			var logLines []string
			log := funcr.New(func(prefix, args string) {
				if prefix != "" {
					logLines = append(logLines, prefix+": "+args)
					return
				}
				logLines = append(logLines, args)
			}, funcr.Options{})

			stub := buildStubInformerSourceWithCounts()
			psGVK := gvkKey(stub.scheme, &promoterv1alpha1.PromotionStrategy{})
			stub.gvkErr = map[schema.GroupVersionKind]error{psGVK: errInjectedGetInformer}

			refreshKubernetesResourceCounts(context.Background(), stub, log)

			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("PromotionStrategy"))).To(Equal(0.0))
			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("GitRepository"))).To(Equal(1.0))

			combined := strings.Join(logLines, "\n")
			Expect(combined).To(And(
				ContainSubstring("counting resources for promoter_kubernetes_resources metric"),
				ContainSubstring("PromotionStrategy"),
				ContainSubstring("injected get informer failure"),
			))
		})
	})

	Describe("ResourceCountRunnable", func() {
		BeforeEach(func() {
			logf.SetLogger(logr.Discard())
		})

		It("returns an error when the cache is nil", func() {
			r := NewResourceCountRunnable(nil)
			err := r.Start(context.Background())
			Expect(err).To(MatchError("resource count runnable cache is nil"))
		})

		It("runs an immediate refresh, updates gauges, and refreshes again on the ticker until the context is cancelled", func() {
			stub := buildStubInformerSourceWithCounts()
			stub.getCount = &atomic.Int32{}
			r := &ResourceCountRunnable{Cache: stub, tickInterval: 25 * time.Millisecond}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				Expect(r.Start(ctx)).To(Succeed())
				close(done)
			}()

			minGets := 2 * len(promoterResources)
			Eventually(func() int32 { return stub.getCount.Load() }).WithTimeout(3 * time.Second).WithPolling(5 * time.Millisecond).
				Should(BeNumerically(">=", minGets))

			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("PromotionStrategy"))).To(Equal(2.0))
			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("GitRepository"))).To(Equal(1.0))
			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("PullRequest"))).To(Equal(0.0))

			cancel()
			Eventually(done).WithTimeout(2 * time.Second).Should(BeClosed())
		})
	})
})
