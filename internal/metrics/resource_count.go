package metrics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/kinds"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

const resourceCountInterval = 30 * time.Second

// kubernetesResources counts promoter.argoproj.io custom resources in the local cluster (see refreshKubernetesResourceCounts).
var kubernetesResources = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "promoter_kubernetes_resources",
		Help: "Current count of promoter.argoproj.io custom resources in the local Kubernetes cluster, by API kind and readiness. " +
			"Updated on an interval from the controller informer stores (no per-tick API server calls); does not include resources on remote clusters " +
			"reconciled via multicluster setup. In Namespace scope, namespaced kinds are counted only in the controller install namespace. " +
			"ControllerConfiguration is omitted (singleton). ClusterScmProvider is omitted when disabled.",
	},
	[]string{"kind", "readiness"},
)

// readinessBuckets enumerates all possible values for the readiness label.
var readinessBuckets = []string{"True", "False", "Unknown", ""}

// conditionsGetter is implemented by all promoter CRDs that expose status conditions.
type conditionsGetter interface {
	GetConditions() *[]metav1.Condition
}

// readinessFromObject returns the status of the Ready condition for an informer store item,
// or "" if the object does not expose conditions or the Ready condition is absent.
func readinessFromObject(obj any) string {
	cg, ok := obj.(conditionsGetter)
	if !ok {
		return ""
	}
	conditions := cg.GetConditions()
	if conditions == nil {
		return ""
	}
	for _, c := range *conditions {
		if c.Type == string(promoterConditions.Ready) {
			return string(c.Status)
		}
	}
	return ""
}

func init() {
	crmetrics.Registry.MustRegister(kubernetesResources)
}

// resourceCountInformerSource is the cache subset used for promoter_kubernetes_resources. It matches cache.Cache.
type resourceCountInformerSource interface {
	List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error
}

func countsByReadinessFromInformer(ctx context.Context, c resourceCountInformerSource, scheme *runtime.Scheme, obj client.Object) (map[string]int, error) {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return nil, fmt.Errorf("getting GVK: %w", err)
	}
	listObj, err := scheme.New(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
	if err != nil {
		return nil, fmt.Errorf("building list type: %w", err)
	}
	list, ok := listObj.(client.ObjectList)
	if !ok {
		return nil, fmt.Errorf("%T is not a client.ObjectList", listObj)
	}
	// Items are only read, never mutated, so the per-item deep copy is not needed.
	if err := c.List(ctx, list, client.UnsafeDisableDeepCopy); err != nil {
		return nil, fmt.Errorf("listing from cache: %w", err)
	}
	counts := make(map[string]int)
	if err := apimeta.EachListItem(list, func(item runtime.Object) error {
		counts[readinessFromObject(item)]++
		return nil
	}); err != nil {
		return nil, fmt.Errorf("iterating list items: %w", err)
	}
	return counts, nil
}

func refreshKubernetesResourceCounts(ctx context.Context, c resourceCountInformerSource, log logr.Logger, clusterScmProviderEnabled bool) {
	scheme := utils.GetScheme()
	for _, obj := range kinds.All(scheme) {
		kind := kinds.Kind(scheme, obj)
		// ControllerConfiguration is a singleton. Skip it.
		if kind == kinds.ControllerConfigurationKind {
			continue
		}
		// Avoid starting a cluster-scoped informer when ClusterScmProvider support is disabled.
		if kind == promoterv1alpha1.ClusterScmProviderKind && !clusterScmProviderEnabled {
			continue
		}
		counts, err := countsByReadinessFromInformer(ctx, c, scheme, obj)
		if err != nil {
			log.Error(err, "counting resources for promoter_kubernetes_resources metric", "kind", kind)
			for _, readiness := range readinessBuckets {
				kubernetesResources.WithLabelValues(kind, readiness).Set(0)
			}
			continue
		}
		for _, readiness := range readinessBuckets {
			kubernetesResources.WithLabelValues(kind, readiness).Set(float64(counts[readiness]))
		}
	}
}

// ResourceCountRunnable periodically reads promoter CR counts from informer stores and updates promoter_kubernetes_resources.
type ResourceCountRunnable struct {
	Cache resourceCountInformerSource
	// ClusterScmProviderEnabled controls whether ClusterScmProviders are counted.
	ClusterScmProviderEnabled bool
	// tickInterval is the delay between refreshes after the initial run. Zero means resourceCountInterval.
	// Tests set a short value so the ticker path runs without long sleeps.
	tickInterval time.Duration
}

// NewResourceCountRunnable returns a manager.Runnable that refreshes promoter_kubernetes_resources.
func NewResourceCountRunnable(c cache.Cache, clusterScmProviderEnabled bool) *ResourceCountRunnable {
	return &ResourceCountRunnable{Cache: c, ClusterScmProviderEnabled: clusterScmProviderEnabled}
}

// Start implements manager.Runnable.
func (r *ResourceCountRunnable) Start(ctx context.Context) error {
	log := ctrl.Log.WithName("promoter-resource-counts")
	if r.Cache == nil {
		return errors.New("resource count runnable cache is nil")
	}

	refreshKubernetesResourceCounts(ctx, r.Cache, log, r.ClusterScmProviderEnabled)

	interval := r.tickInterval
	if interval <= 0 {
		interval = resourceCountInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			refreshKubernetesResourceCounts(ctx, r.Cache, log, r.ClusterScmProviderEnabled)
		}
	}
}
