package utils

import (
	"sync"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	argocd "github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
)

var (
	scheme     *runtime.Scheme
	schemeOnce sync.Once
)

// GetScheme returns a process-wide scheme with promoter (and core) types registered.
// Used by the manager, tests, and apiutil.GVKForObject when TypeMeta.Kind is empty.
func GetScheme() *runtime.Scheme {
	schemeOnce.Do(func() {
		scheme = runtime.NewScheme()
		utilruntime.Must(clientgoscheme.AddToScheme(scheme))
		utilruntime.Must(promoterv1alpha1.AddToScheme(scheme))
		utilruntime.Must(argocd.AddToScheme(scheme))
		// CustomResourceDefinition is read and written by the KubernetesResourceCommitStatus template CRD generator.
		utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	})
	return scheme
}
