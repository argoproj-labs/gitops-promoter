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

// PreviousEnvironmentCommitStatusSpec defines the desired state of PreviousEnvironmentCommitStatus.
// This resource is created by the PromotionStrategy controller to manage previous-environment
// commit status checks. It is an internal resource and should not be created by users directly.
type PreviousEnvironmentCommitStatusSpec struct {
	// PromotionStrategyRef is a reference to the parent PromotionStrategy.
	// The controller gets RepositoryReference from the CTPs when creating CommitStatuses.
	// +required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef"`

	// Environments lists the environments to monitor for previous-environment checks.
	// This is derived from the PromotionStrategy's environments configuration.
	// +required
	// +listType=map
	// +listMapKey=branch
	Environments []PreviousEnvEnvironment `json:"environments"`
}

// PreviousEnvEnvironment defines an environment that needs previous-environment checking.
type PreviousEnvEnvironment struct {
	// Branch is the name of the environment branch.
	// +required
	// +kubebuilder:validation:MinLength=1
	Branch string `json:"branch"`

	// ActiveCommitStatuses from the PromotionStrategy spec for this environment.
	// These are the commit statuses that must pass before promotion can proceed.
	// +optional
	// +listType=map
	// +listMapKey=key
	ActiveCommitStatuses []CommitStatusSelector `json:"activeCommitStatuses,omitempty"`
}

// PreviousEnvironmentCommitStatusStatus defines the observed state of PreviousEnvironmentCommitStatus.
type PreviousEnvironmentCommitStatusStatus struct {
	// Environments tracks the state of each environment's previous-environment check.
	// +optional
	// +listType=map
	// +listMapKey=branch
	Environments []PreviousEnvEnvironmentStatus `json:"environments,omitempty"`

	// Conditions represent the latest available observations of an object's state.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// PreviousEnvEnvironmentStatus defines the observed state of a previous-environment check for an environment.
type PreviousEnvEnvironmentStatus struct {
	// Branch is the name of the environment branch.
	// +required
	// +kubebuilder:validation:MinLength=1
	Branch string `json:"branch"`

	// CommitStatusName is the name of the CommitStatus resource created for this environment.
	// +optional
	CommitStatusName string `json:"commitStatusName,omitempty"`

	// Phase is the current phase of the previous-environment check (pending/success).
	// +optional
	// +kubebuilder:validation:Enum=pending;success
	Phase string `json:"phase,omitempty"`

	// PreviousEnvironmentBranch is the branch of the previous environment being checked.
	// +optional
	PreviousEnvironmentBranch string `json:"previousEnvironmentBranch,omitempty"`

	// Sha is the commit SHA that this status applies to.
	// Supports both SHA-1 (40 chars) and SHA-256 (64 chars) Git hash formats.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	Sha string `json:"sha,omitempty"`
}

// +kubebuilder:ac:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// PreviousEnvironmentCommitStatus is the Schema for the previousenvironmentcommitstatuses API.
// This resource is automatically created and managed by the PromotionStrategy controller.
// It manages CommitStatus resources that gate promotions based on the health of the previous environment.
// +kubebuilder:printcolumn:name="PromotionStrategy",type=string,JSONPath=`.spec.promotionStrategyRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type PreviousEnvironmentCommitStatus struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of PreviousEnvironmentCommitStatus
	// +required
	Spec PreviousEnvironmentCommitStatusSpec `json:"spec"`

	// status defines the observed state of PreviousEnvironmentCommitStatus
	// +optional
	Status PreviousEnvironmentCommitStatusStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PreviousEnvironmentCommitStatusList contains a list of PreviousEnvironmentCommitStatus
type PreviousEnvironmentCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PreviousEnvironmentCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the PreviousEnvironmentCommitStatus.
func (pecs *PreviousEnvironmentCommitStatus) GetConditions() *[]metav1.Condition {
	return &pecs.Status.Conditions
}

func init() {
	SchemeBuilder.Register(&PreviousEnvironmentCommitStatus{}, &PreviousEnvironmentCommitStatusList{})
}
