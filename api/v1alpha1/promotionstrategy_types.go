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

	// RepositoryReference indicates what repository to promote commits in.
	// +kubebuilder:validation:Required
	RepositoryReference ObjectReference `json:"gitRepositoryRef"`

	// ActiveCommitStatuses are commit statuses describing an actively running dry commit. If an active commit status
	// is failing for an environment, subsequent environments will not deploy the failing commit.
	//
	// The commit statuses specified in this field apply to all environments in the promotion sequence. You can also
	// specify commit statuses for individual environments in the `environments` field.
	// +kubebuilder:validation:Optional
	// +listType:=map
	// +listMapKey=key
	ActiveCommitStatuses []CommitStatusSelector `json:"activeCommitStatuses,omitempty"`

	// ProposedCommitStatuses are commit statuses describing a proposed dry commit, i.e. one that is not yet running
	// in a live environment. If a proposed commit status is failing for a given environment, the dry commit will not
	// be promoted to that environment.
	//
	// The commit statuses specified in this field apply to all environments in the promotion sequence. You can also
	// specify commit statuses for individual environments in the `environments` field.
	// +kubebuilder:validation:Optional
	// +listType:=map
	// +listMapKey=key
	ProposedCommitStatuses []CommitStatusSelector `json:"proposedCommitStatuses,omitempty"`

	// Environments is the sequence of environments that a dry commit will be promoted through.
	// +kubebuilder:validation:MinItems:=1
	// +listType:=map
	// +listMapKey=branch
	Environments []Environment `json:"environments"`
}

// Environment defines a single environment in the promotion sequence.
type Environment struct {
	// Branch is the name of the active branch for the environment.
	// +kubebuilder:validation:Required
	Branch string `json:"branch"`
	// AutoMerge determines whether the dry commit should be automatically merged into the next branch in the sequence.
	// If false, the dry commit will be proposed but not merged.
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=true
	AutoMerge *bool `json:"autoMerge,omitempty"`
	// ActiveCommitStatuses are commit statuses describing an actively running dry commit. If an active commit status
	// is failing for an environment, subsequent environments will not deploy the failing commit.
	//
	// The commit statuses specified in this field apply to this environment only. You can also specify commit statuses
	// for all environments in the `spec.activeCommitStatuses` field.
	// +kubebuilder:validation:Optional
	// +listType:=map
	// +listMapKey=key
	ActiveCommitStatuses []CommitStatusSelector `json:"activeCommitStatuses,omitempty"`
	// ProposedCommitStatuses are commit statuses describing a proposed dry commit, i.e. one that is not yet running
	// in a live environment. If a proposed commit status is failing for a given environment, the dry commit will not
	// be promoted to that environment.
	//
	// The commit statuses specified in this field apply to this environment only. You can also specify commit statuses
	// for all environments in the `spec.proposedCommitStatuses` field.
	// +kubebuilder:validation:Optional
	// +listType:=map
	// +listMapKey=key
	ProposedCommitStatuses []CommitStatusSelector `json:"proposedCommitStatuses,omitempty"`
}

// GetAutoMerge returns the value of the AutoMerge field, defaulting to true if the field is nil.
func (e *Environment) GetAutoMerge() bool {
	if e.AutoMerge == nil {
		return true
	}
	return *e.AutoMerge
}

// CommitStatusSelector is used to select commit statuses by their key.
type CommitStatusSelector struct {
	// +required
	// +kubebuilder:validation:MinLength:=1
	// +kubebuilder:validation:MaxLength:=63
	// +kubebuilder:validation:Pattern:=([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9]
	Key string `json:"key"`
}

// PromotionStrategyStatus defines the observed state of PromotionStrategy
type PromotionStrategyStatus struct {
	// Environments holds the status of each environment in the promotion sequence.
	// +listType:=map
	// +listMapKey=branch
	Environments []EnvironmentStatus `json:"environments"`

	// Conditions Represents the observations of the current state.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// GetConditions returns the conditions of the PromotionStrategy.
func (ps *PromotionStrategy) GetConditions() *[]metav1.Condition {
	return &ps.Status.Conditions
}

// EnvironmentStatus defines the observed state of an environment in a PromotionStrategy.
type EnvironmentStatus struct {
	// Branch is the name of the active branch for the environment.
	Branch string `json:"branch"`
	// Proposed is the state of the proposed branch for the environment.
	Proposed CommitBranchState `json:"proposed"`
	// Active is the state of the active branch for the environment.
	Active CommitBranchState `json:"active"`

	// PullRequest is the state of the pull request that was created for this environment.
	PullRequest *PullRequestCommonStatus `json:"pullRequest,omitempty"`

	// LastHealthyDryShas is a list of dry commits that were observed to be healthy in the environment.
	// +kubebuilder:validation:Optional
	LastHealthyDryShas []HealthyDryShas `json:"lastHealthyDryShas"`

	// History defines the history of promoted changes done by the PromotionStrategy for each environment.
	// You can think of it as a list of PRs merged by GitOps Promoter. It will not include changes that were
	// manually merged. The history length is hard-coded to be at most 5 entries. This may change in the future.
	// History is constructed on a best-effort basis and should be used for informational purposes only.
	// History is in reverse chronological order (newest is first).
	History []History `json:"history,omitempty"`
}

// HealthyDryShas is a list of dry commits that were observed to be healthy in the environment.
type HealthyDryShas struct {
	// Sha is the commit SHA of the dry commit that was observed to be healthy.
	Sha string `json:"sha"`
	// Time is the time when the proposed commit for the given dry SHA was merged into the active branch.
	Time metav1.Time `json:"time"`
}

// +kubebuilder:ac:generate=true
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// PromotionStrategy is the Schema for the promotionstrategies API
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
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
