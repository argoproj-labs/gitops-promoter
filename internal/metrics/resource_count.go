package metrics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
)

const resourceCountInterval = 30 * time.Second

// kubernetesResources counts promoter.argoproj.io custom resources in the local cluster (see refreshKubernetesResourceCounts).
var kubernetesResources = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "promoter_kubernetes_resources",
		Help: "Current count of promoter.argoproj.io custom resources in the local Kubernetes cluster, by API kind and readiness. " +
			"Updated on an interval from the controller informer stores (no per-tick list/deep-copy); does not include resources on remote clusters " +
			"reconciled via multicluster setup.",
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
	GetInformer(ctx context.Context, obj client.Object, opts ...cache.InformerGetOption) (cache.Informer, error)
}

type promoterResource struct {
	obj  client.Object
	kind string
}

// promoterResources lists each root CRD kind matching config/rbac/role.yaml (single source for kinds and count targets).
var promoterResources = []promoterResource{
	{kind: "ArgoCDCommitStatus", obj: &promoterv1alpha1.ArgoCDCommitStatus{}},
	{kind: "ChangeTransferPolicy", obj: &promoterv1alpha1.ChangeTransferPolicy{}},
	{kind: "ClusterScmProvider", obj: &promoterv1alpha1.ClusterScmProvider{}},
	{kind: "CommitStatus", obj: &promoterv1alpha1.CommitStatus{}},
	{kind: "ControllerConfiguration", obj: &promoterv1alpha1.ControllerConfiguration{}},
	{kind: "GitCommitStatus", obj: &promoterv1alpha1.GitCommitStatus{}},
	{kind: "GitRepository", obj: &promoterv1alpha1.GitRepository{}},
	{kind: "PromotionStrategy", obj: &promoterv1alpha1.PromotionStrategy{}},
	{kind: "PullRequest", obj: &promoterv1alpha1.PullRequest{}},
	{kind: "RevertCommit", obj: &promoterv1alpha1.RevertCommit{}},
	{kind: "ScmProvider", obj: &promoterv1alpha1.ScmProvider{}},
	{kind: "TimedCommitStatus", obj: &promoterv1alpha1.TimedCommitStatus{}},
	{kind: "WebRequestCommitStatus", obj: &promoterv1alpha1.WebRequestCommitStatus{}},
}

func countsByReadinessFromInformer(ctx context.Context, c resourceCountInformerSource, obj client.Object) (map[string]int, error) {
	informer, err := c.GetInformer(ctx, obj)
	if err != nil {
		return nil, fmt.Errorf("getting informer: %w", err)
	}
	si, ok := informer.(toolscache.SharedIndexInformer)
	if !ok {
		return nil, fmt.Errorf("informer does not implement SharedIndexInformer (got %T)", informer)
	}
	counts := make(map[string]int)
	for _, item := range si.GetStore().List() {
		readiness := readinessFromObject(item)
		counts[readiness]++
	}
	return counts, nil
}

func refreshKubernetesResourceCounts(ctx context.Context, c resourceCountInformerSource, log logr.Logger) {
	for _, r := range promoterResources {
		counts, err := countsByReadinessFromInformer(ctx, c, r.obj)
		if err != nil {
			log.Error(err, "counting resources for promoter_kubernetes_resources metric", "kind", r.kind)
			for _, readiness := range readinessBuckets {
				kubernetesResources.WithLabelValues(r.kind, readiness).Set(0)
			}
			continue
		}
		for _, readiness := range readinessBuckets {
			kubernetesResources.WithLabelValues(r.kind, readiness).Set(float64(counts[readiness]))
		}
	}
}

// ResourceCountRunnable periodically reads promoter CR counts from informer stores and updates promoter_kubernetes_resources.
type ResourceCountRunnable struct {
	Cache resourceCountInformerSource
	// tickInterval is the delay between refreshes after the initial run. Zero means resourceCountInterval.
	// Tests set a short value so the ticker path runs without long sleeps.
	tickInterval time.Duration
}

// NewResourceCountRunnable returns a manager.Runnable that refreshes promoter_kubernetes_resources.
func NewResourceCountRunnable(c cache.Cache) *ResourceCountRunnable {
	return &ResourceCountRunnable{Cache: c}
}

// Start implements manager.Runnable.
func (r *ResourceCountRunnable) Start(ctx context.Context) error {
	log := ctrl.Log.WithName("promoter-resource-counts")
	if r.Cache == nil {
		return errors.New("resource count runnable cache is nil")
	}

	refreshKubernetesResourceCounts(ctx, r.Cache, log)

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
			refreshKubernetesResourceCounts(ctx, r.Cache, log)
		}
	}
}
