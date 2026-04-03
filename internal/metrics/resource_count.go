package metrics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	toolscache "k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

const resourceCountInterval = 30 * time.Second

// kubernetesResources counts promoter.argoproj.io custom resources in the local cluster (see refreshKubernetesResourceCounts).
var kubernetesResources = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "promoter_kubernetes_resources",
		Help: "Current count of promoter.argoproj.io custom resources in the local Kubernetes cluster, by API kind. " +
			"Updated on an interval from the controller informer stores (no per-tick list/deep-copy); does not include resources on remote clusters " +
			"reconciled via multicluster setup.",
	},
	[]string{"kind"},
)

func init() {
	crmetrics.Registry.MustRegister(kubernetesResources)
}

// resourceCountInformerSource is the cache subset used for promoter_kubernetes_resources. It matches cache.Cache.
type resourceCountInformerSource interface {
	GetInformer(ctx context.Context, obj client.Object, opts ...cache.InformerGetOption) (cache.Informer, error)
}

type promoterResource struct {
	kind string
	obj  client.Object
}

// promoterResources lists each root CRD kind matching config/rbac/role.yaml (single source for kinds and count targets).
var promoterResources = []promoterResource{
	{"ArgoCDCommitStatus", &promoterv1alpha1.ArgoCDCommitStatus{}},
	{"ChangeTransferPolicy", &promoterv1alpha1.ChangeTransferPolicy{}},
	{"ClusterScmProvider", &promoterv1alpha1.ClusterScmProvider{}},
	{"CommitStatus", &promoterv1alpha1.CommitStatus{}},
	{"ControllerConfiguration", &promoterv1alpha1.ControllerConfiguration{}},
	{"GitCommitStatus", &promoterv1alpha1.GitCommitStatus{}},
	{"GitRepository", &promoterv1alpha1.GitRepository{}},
	{"PromotionStrategy", &promoterv1alpha1.PromotionStrategy{}},
	{"PullRequest", &promoterv1alpha1.PullRequest{}},
	{"RevertCommit", &promoterv1alpha1.RevertCommit{}},
	{"ScmProvider", &promoterv1alpha1.ScmProvider{}},
	{"TimedCommitStatus", &promoterv1alpha1.TimedCommitStatus{}},
	{"WebRequestCommitStatus", &promoterv1alpha1.WebRequestCommitStatus{}},
}

func countFromInformer(ctx context.Context, c resourceCountInformerSource, obj client.Object) (int, error) {
	informer, err := c.GetInformer(ctx, obj)
	if err != nil {
		return 0, fmt.Errorf("getting informer: %w", err)
	}
	si, ok := informer.(toolscache.SharedIndexInformer)
	if !ok {
		return 0, fmt.Errorf("informer does not implement SharedIndexInformer (got %T)", informer)
	}
	return len(si.GetStore().List()), nil
}

func refreshKubernetesResourceCounts(ctx context.Context, c resourceCountInformerSource, log logr.Logger) {
	for _, r := range promoterResources {
		n, err := countFromInformer(ctx, c, r.obj)
		if err != nil {
			log.Error(err, "counting resources for promoter_kubernetes_resources metric", "kind", r.kind)
			kubernetesResources.WithLabelValues(r.kind).Set(0)
			continue
		}
		kubernetesResources.WithLabelValues(r.kind).Set(float64(n))
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
