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
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ClusterScmProviderSpec defines the desired state of ClusterScmProvider.
type ClusterScmProviderSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// SecretRef contains the credentials required to auth to a specific provider
	SecretRef *v1.ObjectReference `json:"secretRef,omitempty"`

	// GitHub required configuration for GitHub as the SCM provider
	GitHub *GitHub `json:"github,omitempty"`

	// GitLab required configuration for GitLab as the SCM provider
	GitLab *GitLab `json:"gitlab,omitempty"`

	// Fake required configuration for Fake as the SCM provider
	Fake *Fake `json:"fake,omitempty"`
}

// ClusterScmProviderStatus defines the observed state of ClusterScmProvider.
type ClusterScmProviderStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

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

func (c *ClusterScmProvider) GetObjectMeta() *metav1.ObjectMeta {
	return &c.ObjectMeta
}

func (s *ClusterScmProvider) GetTypeMeta() *metav1.TypeMeta {
	return &s.TypeMeta
}

func (s *ClusterScmProvider) GetSpec() *ScmProviderSpec {
	return &s.Spec
}

func (s *ClusterScmProvider) GetStatus() ScmProviderStatus {
	return s.Status
}

func (s *ClusterScmProvider) SetStatus(status ScmProviderStatus) {
	s.Status = status
}

func (s *ClusterScmProvider) GetKind() string {
	return ClusterScmProviderKind
}
