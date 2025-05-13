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

package v1alpha1

import (
	"reflect"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

var ClusterScmProviderKind = reflect.TypeOf(ClusterScmProvider{}).Name()

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

// ClusterScmProvider is the Schema for the clusterscmproviders API.
type ClusterScmProvider struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ScmProviderSpec   `json:"spec,omitempty"`
	Status ScmProviderStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterScmProviderList contains a list of ClusterScmProvider.
type ClusterScmProviderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterScmProvider `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterScmProvider{}, &ClusterScmProviderList{})
}

// +kubebuilder:object:root:false
// +kubebuilder:object:generate:false
var _ GenericScmProvider = &ClusterScmProvider{}

func (s *ClusterScmProvider) GetSpec() *ScmProviderSpec {
	return &s.Spec
}
