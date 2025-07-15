package utils

import (
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	argocd "github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
)

// GetScheme returns a scheme with all the necessary types added to it.
// This is used to use same scheme in both controller and test suite.
func GetScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()

	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(promoterv1alpha1.AddToScheme(scheme))
	utilruntime.Must(argocd.AddToScheme(scheme))

	return scheme
}
