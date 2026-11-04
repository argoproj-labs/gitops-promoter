package cache

import (
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/kinds"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	argocd "github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// partitionedSecretObject is the cache.ByObject key for corev1.Secret. A package-level pointer
// is required because controller-runtime matches ByObject map keys by pointer identity.
var partitionedSecretObject client.Object = &corev1.Secret{}

// unpartitionedNamespaceObject and unpartitionedApplicationObject are types read through the
// manager cache that never carry a promoter instance-id label, so they must opt out of
// DefaultLabelSelector. Namespaces are read by the WebRequestCommitStatus controller for template
// metadata; Argo CD Applications are watched by the ArgoCDCommitStatus controller. Package-level
// pointers are required because controller-runtime matches ByObject map keys by pointer identity.
var (
	unpartitionedNamespaceObject   client.Object = &corev1.Namespace{}
	unpartitionedApplicationObject client.Object = &argocd.Application{}
)

// unpartitionedObjects returns the ByObject keys that always opt out of instance-id filtering
// on every cache that uses OptionsForInstanceID. Argo CD Application is not in this list: the
// host manager never watches Applications. They are watched only on provider clusters (remote
// kubeconfig clusters, and the local cluster through a dedicated cluster while
// watchLocalApplications is true), which use ProviderClusterCacheOptions.
func unpartitionedObjects() []client.Object {
	return []client.Object{unpartitionedNamespaceObject}
}

// OptionsForInstanceID returns controller-runtime cache options that partition informer watches by
// instance-id label. When instanceID is nil, only resources without the label are cached. When
// set, only resources with promoter.argoproj.io/instance-id equal to *instanceID are cached.
//
// DefaultLabelSelector applies the same selector to informers started for types not listed in
// ByObject, so lazily started unstructured informers (out-of-tree orderCommitStatusRef gates) are
// partitioned too. Any cached type that does not carry the label must therefore be listed in
// unpartitionedObjects or it becomes invisible. ControllerConfiguration is scoped to
// controllerNamespace only (not instance-id partitioned). Secret informers additionally apply
// secretDataTransform so only promoter credential keys are retained in cache
// (see secret_transform.go).
//
// These options do not list Argo CD Application, so the host manager starts whether or not the
// Application CRD is installed (cache.New RESTMaps every ByObject entry).
func OptionsForInstanceID(instanceID *string, controllerNamespace string) cache.Options {
	sel := instanceIDSelector(instanceID)
	objs := PartitionedObjects()
	unpartitioned := unpartitionedObjects()
	byObject := make(map[client.Object]cache.ByObject, len(objs)+len(unpartitioned)+1)
	for _, obj := range objs {
		byObject[obj] = cache.ByObject{Label: sel}
	}
	byObject[partitionedSecretObject] = cache.ByObject{
		Label:     sel,
		Transform: secretDataTransform(),
	}
	// labels.Everything() is controller-runtime's sentinel so DefaultLabelSelector is not copied
	// onto these types during cache option defaulting (ByObject.Label nil would inherit it).
	byObject[PartitionedControllerConfigurationObject()] = cache.ByObject{
		Namespaces: map[string]cache.Config{
			controllerNamespace: {},
		},
		Field: fields.OneTermEqualSelector("metadata.name", settings.ControllerConfigurationName),
		Label: labels.Everything(),
	}
	for _, obj := range unpartitioned {
		byObject[obj] = cache.ByObject{Label: labels.Everything()}
	}
	return cache.Options{
		ByObject:             byObject,
		DefaultLabelSelector: sel,
	}
}

// ProviderClusterCacheOptions returns cache options for the clusters Argo CD Applications are
// watched on: remote kubeconfig provider clusters, and the dedicated local Applications cluster.
// Argo CD Application is the only type watched on those clusters (ArgoCDCommitStatus).
// Lists are read-only, so UnsafeDisableDeepCopy skips the per-List DeepCopy of Application
// objects. Callers must not mutate Applications returned from Get/List.
//
// This always lists Application in ByObject, so these options are only for clusters that have the
// Application CRD. Keep them separate from OptionsForInstanceID.
func ProviderClusterCacheOptions() cache.Options {
	return cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			unpartitionedApplicationObject: {
				UnsafeDisableDeepCopy: new(true),
			},
		},
	}
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
// including Promoter CRDs (every scheme kind except ControllerConfiguration) and Secrets
// referenced for SCM, HTTP auth, and kubeconfig credentials.
// Exported for tests.
func PartitionedObjects() []client.Object {
	scheme := utils.GetScheme()
	all := kinds.All(scheme)
	out := make([]client.Object, 0, len(all))
	for _, obj := range all {
		if kinds.Kind(scheme, obj) == kinds.ControllerConfigurationKind {
			continue
		}
		out = append(out, obj)
	}
	out = append(out, partitionedSecretObject)
	return out
}

// PartitionedSecretObject returns the Secret type key used in cache.ByObject partitioning.
// Exported for tests.
func PartitionedSecretObject() client.Object {
	return partitionedSecretObject
}

// UnpartitionedObjects returns the type keys that always opt out of instance-id filtering.
// Argo CD Application is not included; the host manager never watches it.
// Exported for tests.
func UnpartitionedObjects() []client.Object {
	return unpartitionedObjects()
}

// UnpartitionedApplicationObject returns the Argo CD Application type key that ProviderClusterCacheOptions
// lists in cache.ByObject. Exported for tests.
func UnpartitionedApplicationObject() client.Object {
	return unpartitionedApplicationObject
}

// PartitionedControllerConfigurationObject returns the ControllerConfiguration type key used in
// cache.ByObject install-namespace scoping. Exported for tests.
func PartitionedControllerConfigurationObject() client.Object {
	scheme := utils.GetScheme()
	for _, obj := range kinds.All(scheme) {
		if kinds.Kind(scheme, obj) == kinds.ControllerConfigurationKind {
			return obj
		}
	}
	panic("ControllerConfiguration missing from promoter scheme")
}
