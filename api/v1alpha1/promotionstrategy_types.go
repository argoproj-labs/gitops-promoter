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

// PromotionStrategySpec defines the desired state of PromotionStrategy
type PromotionStrategySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:Required
	RepositoryReference ObjectReference `json:"gitRepositoryRef"`

	// +kubebuilder:validation:Required
	DryBanch string `json:"dryBranch"`

	// +kubebuilder:validation:Optional
	ActiveCommitStatuses []CommitStatusSelector `json:"activeCommitStatuses"`

	// +kubebuilder:validation:Optional
	ProposedCommitStatuses []CommitStatusSelector `json:"proposedCommitStatuses"`

	// +kubebuilder:validation:MinItems:=1
	Environments []Environment `json:"environments"`
}

type Environment struct {
	// +kubebuilder:validation:Required
	Branch string `json:"branch"`
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=true
	AutoMerge *bool `json:"autoMerge,omitempty"`
	// +kubebuilder:validation:Optional
	ActiveCommitStatuses []CommitStatusSelector `json:"activeCommitStatuses"`
	// +kubebuilder:validation:Optional
	ProposedCommitStatuses []CommitStatusSelector `json:"proposedCommitStatuses"`
}

func (e *Environment) GetAutoMerge() bool {
	if e.AutoMerge == nil {
		return true
	}
	return *e.AutoMerge
}

type CommitStatusSelector struct {
	// +kubebuilder:validation:Pattern:=(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?
	Key string `json:"key"`
}

// PromotionStrategyStatus defines the observed state of PromotionStrategy
type PromotionStrategyStatus struct {
	Environments []EnvironmentStatus `json:"environments"`
}

type EnvironmentStatus struct {
	Branch   string                             `json:"branch"`
	Active   PromotionStrategyBranchStateStatus `json:"active"`
	Proposed PromotionStrategyBranchStateStatus `json:"proposed"`
	// +kubebuilder:validation:Optional
	LastHealthyDryShas []HealthyDryShas `json:"lastHealthyDryShas"`
}

type HealthyDryShas struct {
	Sha string `json:"sha"`
	// FIXME: docs, is this commit time, first-became-healthy time, most-recently-observed-healthy time, etc?
	Time metav1.Time `json:"time"`
}

type PromotionStrategyBranchStateStatus struct {
	Dry          CommitShaState                `json:"dry"`
	Hydrated     CommitShaState                `json:"hydrated"`
	CommitStatus PromotionStrategyCommitStatus `json:"commitStatus"`
}

type PromotionStrategyCommitStatus struct {
	Sha string `json:"sha"`
	// +kubebuilder:validation:Enum:=pending;success;failure
	Phase string `json:"phase"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// +kubebuilder:printcolumn:name="Active Dry Sha",type=string,JSONPath=`.status.active.dry.sha`
// +kubebuilder:printcolumn:name="Proposed Dry Sha",type=string,JSONPath=`.status.proposed.dry.sha`
// PromotionStrategy is the Schema for the promotionstrategies API
type PromotionStrategy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PromotionStrategySpec   `json:"spec,omitempty"`
	Status PromotionStrategyStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// PromotionStrategyList contains a list of PromotionStrategy
type PromotionStrategyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PromotionStrategy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PromotionStrategy{}, &PromotionStrategyList{})
}
