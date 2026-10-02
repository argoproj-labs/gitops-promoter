package cache

import (
	"fmt"
	"maps"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/kinds"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	argocd "github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
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
// on every cache that uses OptionsForInstanceID. Argo CD Application is not in this list: it
// is added only on the host manager via WithArgoCDApplicationIfInstalled. Provider clusters get their
// own ClusterOptions and do not inherit the host ByObject map.
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
// These options do not list Argo CD Application. Apply WithArgoCDApplicationIfInstalled only to the
// host manager's Cache (ctrl.Options passed to mcmanager.New). Provider clusters are built from
// the kubeconfig provider's ClusterOptions and must not reuse this Application ByObject key:
// cache.New RESTMaps every ByObject entry, so a local-cluster CRD check must not gate remote
// Application informers.
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

// WithArgoCDApplicationIfInstalled copies opts and lists Argo CD Application in ByObject (unfiltered)
// when the Application CRD is installed on the cluster described by cfg. Call this only for the
// host manager cache. cache.New resolves every ByObject key through the RESTMapper, so listing
// Application here would make the local manager fail to start when that CRD is absent.
func WithArgoCDApplicationIfInstalled(opts cache.Options, cfg *rest.Config) (cache.Options, error) {
	include, err := applicationCRDInstalled(cfg)
	if err != nil {
		return cache.Options{}, err
	}
	if !include {
		return opts, nil
	}
	opts.ByObject = maps.Clone(opts.ByObject)
	if opts.ByObject == nil {
		opts.ByObject = make(map[client.Object]cache.ByObject, 1)
	}
	opts.ByObject[unpartitionedApplicationObject] = cache.ByObject{Label: labels.Everything()}
	return opts, nil
}

func applicationCRDInstalled(cfg *rest.Config) (bool, error) {
	httpClient, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return false, fmt.Errorf("create HTTP client to discover Argo CD Application CRD: %w", err)
	}
	mapper, err := apiutil.NewDynamicRESTMapper(cfg, httpClient)
	if err != nil {
		return false, fmt.Errorf("create RESTMapper to discover Argo CD Application CRD: %w", err)
	}
	_, err = mapper.RESTMapping(schema.GroupKind{
		Group: argocd.SchemeGroupVersion.Group,
		Kind:  "Application",
	}, argocd.SchemeGroupVersion.Version)
	if err == nil {
		return true, nil
	}
	if meta.IsNoMatchError(err) {
		return false, nil
	}
	return false, fmt.Errorf("RESTMapping for Argo CD Application: %w", err)
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
// Argo CD Application is not included; it is added only by WithArgoCDApplicationIfInstalled.
// Exported for tests.
func UnpartitionedObjects() []client.Object {
	return unpartitionedObjects()
}

// UnpartitionedApplicationObject returns the Argo CD Application type key that WithArgoCDApplicationIfInstalled
// adds to cache.ByObject when the CRD is installed on the host cluster. Exported for tests.
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
