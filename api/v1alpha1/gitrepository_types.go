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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// GitRepositorySpec defines the desired state of GitRepository
// +kubebuilder:validation:ExactlyOneOf=github;gitlab;forgejo;gitea;bitbucketCloud;azureDevOps;fake
type GitRepositorySpec struct {
	GitHub         *GitHubRepo         `json:"github,omitempty"`
	GitLab         *GitLabRepo         `json:"gitlab,omitempty"`
	Forgejo        *ForgejoRepo        `json:"forgejo,omitempty"`
	Gitea          *GiteaRepo          `json:"gitea,omitempty"`
	BitbucketCloud *BitbucketCloudRepo `json:"bitbucketCloud,omitempty"`
	AzureDevOps    *AzureDevOpsRepo    `json:"azureDevOps,omitempty"`
	Fake           *FakeRepo           `json:"fake,omitempty"`
	// +kubebuilder:validation:Required
	ScmProviderRef ScmProviderObjectReference `json:"scmProviderRef"`
}

// ScmProviderObjectReference is a reference to a SCM provider object.
type ScmProviderObjectReference struct {
	// Kind is the type of resource being referenced
	// +kubebuilder:validation:Required
	// +kubebuilder:default:=ScmProvider
	// +kubebuilder:validation:Enum:=ScmProvider;ClusterScmProvider
	Kind string `json:"kind"`
	// Name is the name of the resource being referenced
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// GitRepositoryStatus defines the observed state of GitRepository
type GitRepositoryStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Conditions Represents the observations of the current state.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:ac:generate=true
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// GitRepository is the Schema for the gitrepositories API
// +kubebuilder:printcolumn:name="Provider",type=string,JSONPath=`.spec.scmProviderRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type GitRepository struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GitRepositorySpec   `json:"spec,omitempty"`
	Status GitRepositoryStatus `json:"status,omitempty"`
}

// GetConditions returns the conditions of the GitRepository.
func (gr *GitRepository) GetConditions() *[]metav1.Condition {
	return &gr.Status.Conditions
}

//+kubebuilder:object:root=true

// GitRepositoryList contains a list of GitRepository
type GitRepositoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GitRepository `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GitRepository{}, &GitRepositoryList{})
}
