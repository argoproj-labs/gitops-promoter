package metrics

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	ctrl "sigs.k8s.io/controller-runtime"
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
			"Updated on an interval from the controller cache/client; does not include resources on remote clusters " +
			"reconciled via multicluster setup.",
	},
	[]string{"kind"},
)

func init() {
	crmetrics.Registry.MustRegister(kubernetesResources)
}

// promoterResourceKinds lists each root CRD kind matching config/rbac/role.yaml.
var promoterResourceKinds = []string{
	"ArgoCDCommitStatus",
	"ChangeTransferPolicy",
	"ClusterScmProvider",
	"CommitStatus",
	"ControllerConfiguration",
	"GitCommitStatus",
	"GitRepository",
	"PromotionStrategy",
	"PullRequest",
	"RevertCommit",
	"ScmProvider",
	"TimedCommitStatus",
	"WebRequestCommitStatus",
}

func countPromoterKind(ctx context.Context, c client.Client, kind string) (int, error) {
	switch kind {
	case "ArgoCDCommitStatus":
		var list promoterv1alpha1.ArgoCDCommitStatusList
		if err := c.List(ctx, &list); err != nil {
			return 0, fmt.Errorf("listing %s: %w", kind, err)
		}
		return len(list.Items), nil
	case "ChangeTransferPolicy":
		var list promoterv1alpha1.ChangeTransferPolicyList
		if err := c.List(ctx, &list); err != nil {
			return 0, fmt.Errorf("listing %s: %w", kind, err)
		}
		return len(list.Items), nil
	case "ClusterScmProvider":
		var list promoterv1alpha1.ClusterScmProviderList
		if err := c.List(ctx, &list); err != nil {
			return 0, fmt.Errorf("listing %s: %w", kind, err)
		}
		return len(list.Items), nil
	case "CommitStatus":
		var list promoterv1alpha1.CommitStatusList
		if err := c.List(ctx, &list); err != nil {
			return 0, fmt.Errorf("listing %s: %w", kind, err)
		}
		return len(list.Items), nil
	case "ControllerConfiguration":
		var list promoterv1alpha1.ControllerConfigurationList
		if err := c.List(ctx, &list); err != nil {
			return 0, fmt.Errorf("listing %s: %w", kind, err)
		}
		return len(list.Items), nil
	case "GitCommitStatus":
		var list promoterv1alpha1.GitCommitStatusList
		if err := c.List(ctx, &list); err != nil {
			return 0, fmt.Errorf("listing %s: %w", kind, err)
		}
		return len(list.Items), nil
	case "GitRepository":
		var list promoterv1alpha1.GitRepositoryList
		if err := c.List(ctx, &list); err != nil {
			return 0, fmt.Errorf("listing %s: %w", kind, err)
		}
		return len(list.Items), nil
	case "PromotionStrategy":
		var list promoterv1alpha1.PromotionStrategyList
		if err := c.List(ctx, &list); err != nil {
			return 0, fmt.Errorf("listing %s: %w", kind, err)
		}
		return len(list.Items), nil
	case "PullRequest":
		var list promoterv1alpha1.PullRequestList
		if err := c.List(ctx, &list); err != nil {
			return 0, fmt.Errorf("listing %s: %w", kind, err)
		}
		return len(list.Items), nil
	case "RevertCommit":
		var list promoterv1alpha1.RevertCommitList
		if err := c.List(ctx, &list); err != nil {
			return 0, fmt.Errorf("listing %s: %w", kind, err)
		}
		return len(list.Items), nil
	case "ScmProvider":
		var list promoterv1alpha1.ScmProviderList
		if err := c.List(ctx, &list); err != nil {
			return 0, fmt.Errorf("listing %s: %w", kind, err)
		}
		return len(list.Items), nil
	case "TimedCommitStatus":
		var list promoterv1alpha1.TimedCommitStatusList
		if err := c.List(ctx, &list); err != nil {
			return 0, fmt.Errorf("listing %s: %w", kind, err)
		}
		return len(list.Items), nil
	case "WebRequestCommitStatus":
		var list promoterv1alpha1.WebRequestCommitStatusList
		if err := c.List(ctx, &list); err != nil {
			return 0, fmt.Errorf("listing %s: %w", kind, err)
		}
		return len(list.Items), nil
	default:
		return 0, fmt.Errorf("unknown promoter resource kind %q", kind)
	}
}

func refreshKubernetesResourceCounts(ctx context.Context, c client.Client, log logr.Logger) {
	for _, kind := range promoterResourceKinds {
		n, err := countPromoterKind(ctx, c, kind)
		if err != nil {
			log.Error(err, "listing resources for promoter_kubernetes_resources metric", "kind", kind)
			kubernetesResources.WithLabelValues(kind).Set(0)
			continue
		}
		kubernetesResources.WithLabelValues(kind).Set(float64(n))
	}
}

// ResourceCountRunnable periodically lists promoter CRs and updates promoter_kubernetes_resources.
type ResourceCountRunnable struct {
	Client client.Client
	Log    logr.Logger
}

// NewResourceCountRunnable returns a manager.Runnable that refreshes promoter_kubernetes_resources.
func NewResourceCountRunnable(c client.Client) *ResourceCountRunnable {
	return &ResourceCountRunnable{
		Client: c,
		Log:    ctrl.Log.WithName("promoter-resource-counts"),
	}
}

// Start implements manager.Runnable.
func (r *ResourceCountRunnable) Start(ctx context.Context) error {
	log := r.Log
	if log.GetSink() == nil {
		log = ctrl.Log.WithName("promoter-resource-counts")
	}
	if r.Client == nil {
		return errors.New("resource count runnable client is nil")
	}

	refreshKubernetesResourceCounts(ctx, r.Client, log)

	ticker := time.NewTicker(resourceCountInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			refreshKubernetesResourceCounts(ctx, r.Client, log)
		}
	}
}
