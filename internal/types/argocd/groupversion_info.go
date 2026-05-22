// Package argocd contains API Schema definitions for the argoproj.io v1alpha1 API group
// +kubebuilder:object:generate=true
// +groupName=argoproj.io
package argocd

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "argoproj.io", Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = newSchemeBuilder(GroupVersion)

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

type schemeBuilder struct {
	groupVersion schema.GroupVersion
	runtime.SchemeBuilder
}

func newSchemeBuilder(gv schema.GroupVersion) *schemeBuilder {
	return &schemeBuilder{
		groupVersion:  gv,
		SchemeBuilder: runtime.NewSchemeBuilder(),
	}
}

// Register adds one or more objects to the SchemeBuilder so they can be added to a Scheme.
func (b *schemeBuilder) Register(object ...runtime.Object) *schemeBuilder {
	b.SchemeBuilder.Register(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(b.groupVersion, object...)
		metav1.AddToGroupVersion(scheme, b.groupVersion)
		return nil
	})
	return b
}
