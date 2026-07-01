package cache

import (
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
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

// OptionsForInstanceID returns controller-runtime cache options that partition informer watches by
// instance-id label. When instanceID is nil, only resources without the label are cached. When
// set, only resources with promoter.argoproj.io/instance-id equal to *instanceID are cached.
func OptionsForInstanceID(instanceID *string) cache.Options {
	sel := instanceIDSelector(instanceID)
	byObject := make(map[client.Object]cache.ByObject, len(promotorCRDObjects))
	for _, obj := range promotorCRDObjects {
		byObject[obj] = cache.ByObject{Label: sel}
	}
	return cache.Options{ByObject: byObject}
}

func instanceIDSelector(instanceID *string) labels.Selector {
	if instanceID == nil {
		req, err := labels.NewRequirement(promoterv1alpha1.InstanceIDLabel, selection.DoesNotExist, nil)
		if err != nil {
			panic(err)
		}
		return labels.NewSelector().Add(*req)
	}
	return labels.SelectorFromSet(labels.Set{
		promoterv1alpha1.InstanceIDLabel: *instanceID,
	})
}

// PromotorCRDObjects returns the slice of types included in instance-id cache filtering.
// Exported for tests.
func PromotorCRDObjects() []client.Object {
	out := make([]client.Object, len(promotorCRDObjects))
	copy(out, promotorCRDObjects)
	return out
}
