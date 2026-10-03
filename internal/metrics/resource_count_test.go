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
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/kinds"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

func TestMetrics(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)
	RunSpecs(t, "Metrics Suite")
}

var errInjectedList = errors.New("injected list failure")

// stubResourceCountInformerSource implements resourceCountInformerSource for tests. Maps are keyed
// by the item GVK, not the List GVK.
type stubResourceCountInformerSource struct {
	scheme    *runtime.Scheme
	gvkErr    map[schema.GroupVersionKind]error
	gvkItems  map[schema.GroupVersionKind][]client.Object
	listCount *atomic.Int32
}

func (s *stubResourceCountInformerSource) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if s.listCount != nil {
		s.listCount.Add(1)
	}
	listGVK, err := apiutil.GVKForObject(list, s.scheme)
	if err != nil {
		return fmt.Errorf("gvk for list: %w", err)
	}
	gvk := listGVK.GroupVersion().WithKind(strings.TrimSuffix(listGVK.Kind, "List"))
	if e, ok := s.gvkErr[gvk]; ok {
		return e
	}
	items := make([]runtime.Object, 0, len(s.gvkItems[gvk]))
	for _, item := range s.gvkItems[gvk] {
		items = append(items, item)
	}
	if err := apimeta.SetList(list, items); err != nil {
		return fmt.Errorf("setting list items: %w", err)
	}
	return nil
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
	gvkItems := make(map[schema.GroupVersionKind][]client.Object)
	for _, obj := range kinds.All(s) {
		gvk := gvkKey(s, obj)
		kind := kinds.Kind(s, obj)
		switch kind {
		case "PromotionStrategy":
			readyTrue := metav1.ConditionTrue
			readyFalse := metav1.ConditionFalse
			gvkItems[gvk] = []client.Object{
				&promoterv1alpha1.PromotionStrategy{
					Name: "one", Namespace: "ns",
					Status: promoterv1alpha1.PromotionStrategyStatus{
						Conditions: []metav1.Condition{{Type: "Ready", Status: readyTrue}},
					},
				},
				&promoterv1alpha1.PromotionStrategy{
					Name: "two", Namespace: "ns",
					Status: promoterv1alpha1.PromotionStrategyStatus{
						Conditions: []metav1.Condition{{Type: "Ready", Status: readyFalse}},
					},
				},
			}
		case "GitRepository":
			gvkItems[gvk] = []client.Object{
				&promoterv1alpha1.GitRepository{Name: "repo-a", Namespace: "ns"},
			}
		default:
			// Other kinds list empty.
		}
	}
	return &stubResourceCountInformerSource{scheme: s, gvkItems: gvkItems}
}

func resetPromoterKubernetesResourceGauges() {
	scheme := utils.GetScheme()
	for _, obj := range kinds.All(scheme) {
		kind := kinds.Kind(scheme, obj)
		for _, readiness := range readinessBuckets {
			kubernetesResources.DeleteLabelValues(kind, readiness)
		}
	}
}

var _ = Describe("Resource count metrics", func() {
	BeforeEach(func() {
		resetPromoterKubernetesResourceGauges()
	})

	Describe("readinessFromObject", func() {
		It("returns the Ready condition status when present", func() {
			obj := &promoterv1alpha1.PromotionStrategy{
				Status: promoterv1alpha1.PromotionStrategyStatus{
					Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionTrue}},
				},
			}
			Expect(readinessFromObject(obj)).To(Equal("True"))
		})

		It("returns False when the Ready condition status is False", func() {
			obj := &promoterv1alpha1.PromotionStrategy{
				Status: promoterv1alpha1.PromotionStrategyStatus{
					Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionFalse}},
				},
			}
			Expect(readinessFromObject(obj)).To(Equal("False"))
		})

		It("returns Unknown when the Ready condition status is Unknown", func() {
			obj := &promoterv1alpha1.PromotionStrategy{
				Status: promoterv1alpha1.PromotionStrategyStatus{
					Conditions: []metav1.Condition{{Type: "Ready", Status: metav1.ConditionUnknown}},
				},
			}
			Expect(readinessFromObject(obj)).To(Equal("Unknown"))
		})

		It("returns empty string when the Ready condition is absent", func() {
			obj := &promoterv1alpha1.PromotionStrategy{}
			Expect(readinessFromObject(obj)).To(Equal(""))
		})

		It("returns empty string for objects that do not expose conditions", func() {
			Expect(readinessFromObject(struct{}{})).To(Equal(""))
		})
	})

	Describe("refreshKubernetesResourceCounts", func() {
		It("logs an error and sets the gauge to zero when listing fails", func() {
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
			stub.gvkErr = map[schema.GroupVersionKind]error{psGVK: errInjectedList}

			refreshKubernetesResourceCounts(context.Background(), stub, log)

			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("PromotionStrategy", "True"))).To(Equal(0.0))
			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("PromotionStrategy", "False"))).To(Equal(0.0))
			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("PromotionStrategy", "Unknown"))).To(Equal(0.0))
			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("PromotionStrategy", ""))).To(Equal(0.0))
			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("GitRepository", ""))).To(Equal(1.0))

			combined := strings.Join(logLines, "\n")
			Expect(combined).To(And(
				ContainSubstring("counting resources for promoter_kubernetes_resources metric"),
				ContainSubstring("PromotionStrategy"),
				ContainSubstring("injected list failure"),
			))
		})

		It("skips ControllerConfiguration without listing it", func() {
			var logLines []string
			log := funcr.New(func(prefix, args string) {
				if prefix != "" {
					logLines = append(logLines, prefix+": "+args)
					return
				}
				logLines = append(logLines, args)
			}, funcr.Options{})

			stub := buildStubInformerSourceWithCounts()
			stub.listCount = &atomic.Int32{}
			ccGVK := gvkKey(stub.scheme, &promoterv1alpha1.ControllerConfiguration{})
			// Would surface as a logged error if this kind were listed.
			stub.gvkErr = map[schema.GroupVersionKind]error{ccGVK: errInjectedList}

			refreshKubernetesResourceCounts(context.Background(), stub, log)

			Expect(stub.listCount.Load()).To(Equal(int32(len(kinds.All(stub.scheme)) - 1)))
			Expect(strings.Join(logLines, "\n")).NotTo(ContainSubstring("ControllerConfiguration"))
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
			stub.listCount = &atomic.Int32{}
			r := &ResourceCountRunnable{Cache: stub, tickInterval: 25 * time.Millisecond}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			done := make(chan struct{})
			go func() {
				defer GinkgoRecover()
				Expect(r.Start(ctx)).To(Succeed())
				close(done)
			}()

			// ControllerConfiguration is skipped, so each refresh lists one fewer kind.
			minLists := 2 * (len(kinds.All(utils.GetScheme())) - 1)
			Eventually(func() int32 { return stub.listCount.Load() }).WithTimeout(3 * time.Second).WithPolling(5 * time.Millisecond).
				Should(BeNumerically(">=", minLists))

			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("PromotionStrategy", "True"))).To(Equal(1.0))
			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("PromotionStrategy", "False"))).To(Equal(1.0))
			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("PromotionStrategy", "Unknown"))).To(Equal(0.0))
			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("PromotionStrategy", ""))).To(Equal(0.0))
			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("GitRepository", ""))).To(Equal(1.0))
			Expect(testutil.ToFloat64(kubernetesResources.WithLabelValues("PullRequest", ""))).To(Equal(0.0))

			cancel()
			Eventually(done).WithTimeout(2 * time.Second).Should(BeClosed())
		})
	})
})
