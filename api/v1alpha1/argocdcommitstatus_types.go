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

// ArgoCDCommitStatusSpec defines the desired state of ArgoCDCommitStatus.
type ArgoCDCommitStatusSpec struct {
	// PromotionStrategyRef is a reference to the promotion strategy that this commit status applies to.
	// +kubebuilder:validation:Required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef,omitempty"`

	// ApplicationSelector is a label selector that selects the Argo CD applications to which this commit status applies.
	// +kubebuilder:validation:Required
	ApplicationSelector *metav1.LabelSelector `json:"applicationSelector,omitempty"`
}

// ArgoCDCommitStatusStatus defines the observed state of ArgoCDCommitStatus.
type ArgoCDCommitStatusStatus struct {
	// ApplicationsSelected represents the Argo CD applications that are selected by the commit status.
	// This field is sorted by environment, then namespace, then name.
	ApplicationsSelected []ApplicationsSelected `json:"applicationsSelected,omitempty"`

	// Conditions Represents the observations of the current state.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// GetConditions returns the conditions of the ArgoCDCommitStatus.
func (cs *ArgoCDCommitStatus) GetConditions() *[]metav1.Condition {
	return &cs.Status.Conditions
}

// ApplicationsSelected represents the Argo CD applications that are selected by the commit status.
type ApplicationsSelected struct {
	// Namespace is the namespace of the Argo CD application.
	Namespace string `json:"namespace"`
	// Name is the name of the Argo CD application.
	Name string `json:"name"`
	// Phase is the current phase of the commit status.
	Phase CommitStatusPhase `json:"phase"`
	// Sha is the commit SHA that this status is associated with.
	Sha string `json:"sha"`
	// LastTransitionTime is the last time the phase transitioned.
	// +kubebuilder:validation:Optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime"`
	// Environment is the syncSource.targetBranch of the Argo CD application (in effect, its environment).
	Environment string `json:"environment,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ArgoCDCommitStatus is the Schema for the argocdcommitstatuses API.
type ArgoCDCommitStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ArgoCDCommitStatusSpec   `json:"spec,omitempty"`
	Status ArgoCDCommitStatusStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ArgoCDCommitStatusList contains a list of ArgoCDCommitStatus.
type ArgoCDCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ArgoCDCommitStatus `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ArgoCDCommitStatus{}, &ArgoCDCommitStatusList{})
}
