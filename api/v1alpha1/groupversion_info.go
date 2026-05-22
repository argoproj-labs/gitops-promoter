/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package v1alpha1 contains API Schema definitions for the promoter v1alpha1 API group
// +kubebuilder:object:generate=true
// +kubebuilder:ac:generate=true
// +kubebuilder:ac:output:package=../../applyconfiguration
// +groupName=promoter.argoproj.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: "promoter.argoproj.io", Version: "v1alpha1"}

	// SchemeGroupVersion is an alias for GroupVersion for compatibility with code generators
	SchemeGroupVersion = GroupVersion

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
