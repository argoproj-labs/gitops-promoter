package cache

import (
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
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

// partitionedSecretObject is the cache.ByObject key for corev1.Secret. A package-level pointer
// is required because controller-runtime matches ByObject map keys by pointer identity.
var partitionedSecretObject client.Object = &corev1.Secret{}

// OptionsForInstanceID returns controller-runtime cache options that partition informer watches by
// instance-id label. When instanceID is nil, only resources without the label are cached. When
// set, only resources with promoter.argoproj.io/instance-id equal to *instanceID are cached.
func OptionsForInstanceID(instanceID *string) cache.Options {
	sel := instanceIDSelector(instanceID)
	objs := PartitionedObjects()
	byObject := make(map[client.Object]cache.ByObject, len(objs))
	for _, obj := range objs {
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

// PartitionedObjects returns every type whose informer cache is scoped by instance-id label,
// including Promoter CRDs and Secrets referenced for SCM, HTTP auth, and kubeconfig credentials.
// Exported for tests.
func PartitionedObjects() []client.Object {
	out := make([]client.Object, 0, len(promotorCRDObjects)+1)
	out = append(out, promotorCRDObjects...)
	out = append(out, partitionedSecretObject)
	return out
}

// PartitionedSecretObject returns the Secret type key used in cache.ByObject partitioning.
// Exported for tests.
func PartitionedSecretObject() client.Object {
	return partitionedSecretObject
}
