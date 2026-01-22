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

// RequiredStatusCheckCommitStatusSpec defines the desired state of RequiredStatusCheckCommitStatus
type RequiredStatusCheckCommitStatusSpec struct {
	// PromotionStrategyRef references the PromotionStrategy that owns this controller
	// +required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef"`
}

// RequiredStatusCheckCommitStatusStatus defines the observed state of RequiredStatusCheckCommitStatus
type RequiredStatusCheckCommitStatusStatus struct {
	// Environments contains status for each environment's branch protection checks
	// +listType=map
	// +listMapKey=branch
	// +optional
	Environments []RequiredStatusCheckEnvironmentStatus `json:"environments,omitempty"`

	// Conditions represent the latest available observations of the resource's state
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// RequiredStatusCheckEnvironmentStatus defines the observed required check status for a specific environment.
type RequiredStatusCheckEnvironmentStatus struct {
	// Branch is the target branch being monitored
	// +required
	Branch string `json:"branch"`

	// Sha is the commit SHA being checked
	// +required
	Sha string `json:"sha"`

	// RequiredChecks lists all required checks discovered from SCM branch protection rules
	// +listType=map
	// +listMapKey=name
	// +optional
	RequiredChecks []RequiredCheckStatus `json:"requiredChecks,omitempty"`

	// Phase is the aggregated phase of all required checks
	// +kubebuilder:validation:Enum=pending;success;failure
	// +required
	Phase CommitStatusPhase `json:"phase"`
}

// RequiredCheckStatus defines the status of a single required check.
type RequiredCheckStatus struct {
	// Name is the check name
	// +required
	Name string `json:"name"`

	// Phase is the current phase of this check
	// +kubebuilder:validation:Enum=pending;success;failure
	// +required
	Phase CommitStatusPhase `json:"phase"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// RequiredStatusCheckCommitStatus is the Schema for the requiredstatuscheckcommitstatuses API
// +kubebuilder:printcolumn:name="PromotionStrategy",type=string,JSONPath=`.spec.promotionStrategyRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type RequiredStatusCheckCommitStatus struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of RequiredStatusCheckCommitStatus
	// +required
	Spec RequiredStatusCheckCommitStatusSpec `json:"spec"`

	// status defines the observed state of RequiredStatusCheckCommitStatus
	// +optional
	Status RequiredStatusCheckCommitStatusStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RequiredStatusCheckCommitStatusList contains a list of RequiredStatusCheckCommitStatus
type RequiredStatusCheckCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RequiredStatusCheckCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the RequiredStatusCheckCommitStatus.
func (rsccs *RequiredStatusCheckCommitStatus) GetConditions() *[]metav1.Condition {
	return &rsccs.Status.Conditions
}

func init() {
	SchemeBuilder.Register(&RequiredStatusCheckCommitStatus{}, &RequiredStatusCheckCommitStatusList{})
}
