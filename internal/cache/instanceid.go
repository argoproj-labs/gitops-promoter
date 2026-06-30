package cache

import (
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// promotorCRDObjects lists every promoter.argoproj.io type reconciled by the controller that
// participates in instance-id partitioning. ControllerConfiguration is excluded (source of truth).
var promotorCRDObjects = []client.Object{
	&promoterv1alpha1.PromotionStrategy{},
	&promoterv1alpha1.ChangeTransferPolicy{},
	&promoterv1alpha1.CommitStatus{},
	&promoterv1alpha1.PullRequest{},
	&promoterv1alpha1.ScmProvider{},
	&promoterv1alpha1.ClusterScmProvider{},
	&promoterv1alpha1.GitRepository{},
	&promoterv1alpha1.GitCommitStatus{},
	&promoterv1alpha1.TimedCommitStatus{},
	&promoterv1alpha1.WebRequestCommitStatus{},
	&promoterv1alpha1.ArgoCDCommitStatus{},
	&promoterv1alpha1.RevertCommit{},
}

// OptionsForInstanceID returns controller-runtime cache options that scope informer watches to
// resources carrying promoter.argoproj.io/instance-id matching instanceID. When instanceID is
// empty, returns default options (single-install: no label filter).
func OptionsForInstanceID(instanceID string) cache.Options {
	if instanceID == "" {
		return cache.Options{}
	}

	sel := labels.SelectorFromSet(labels.Set{
		promoterv1alpha1.InstanceIDLabel: instanceID,
	})
	byObject := make(map[client.Object]cache.ByObject, len(promotorCRDObjects))
	for _, obj := range promotorCRDObjects {
		byObject[obj] = cache.ByObject{Label: sel}
	}
	return cache.Options{ByObject: byObject}
}

// PromotorCRDObjects returns the slice of types included in instance-id cache filtering.
// Exported for tests.
func PromotorCRDObjects() []client.Object {
	out := make([]client.Object, len(promotorCRDObjects))
	copy(out, promotorCRDObjects)
	return out
}
