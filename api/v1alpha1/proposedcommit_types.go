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

// ProposedCommitSpec defines the desired state of ProposedCommit
type ProposedCommitSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// RepositoryReference what repository to open the PR on.
	// +kubebuilder:validation:Required
	RepositoryReference *NamespacedObjectReference `json:"gitRepositoryRef"`

	// ProposedBranch staging hydrated branch
	// +kubebuilder:validation:Required
	ProposedBranch string `json:"proposedBranch"`

	// ActiveBranch staging hydrated branch
	// +kubebuilder:validation:Required
	ActiveBranch string `json:"activeBranch"`

	// ActiveCommitStatuses lists the statuses to be monitored on the active branch
	// +kubebuilder:validation:Optional
	ActiveCommitStatuses []CommitStatusSelector `json:"activeCommitStatuses"`

	// ProposedCommitStatuses lists the statuses to be monitored on the proposed branch
	// +kubebuilder:validation:Optional
	ProposedCommitStatuses []CommitStatusSelector `json:"proposedCommitStatuses"`
}

type ProposedCommitCommitStatusPhase struct {
	// Key staging hydrated branch
	// +kubebuilder:validation:Required
	Key string `json:"key"`

	// Phase what phase is the status in
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum:=pending;success;failure
	Phase string `json:"phase"`
}

type CommitBranchState struct {
	Dry      CommitShaState `json:"dry,omitempty"`
	Hydrated CommitShaState `json:"hydrated,omitempty"`
	// +kubebuilder:validation:Optional
	CommitStatuses []ProposedCommitCommitStatusPhase `json:"commitStatuses,omitempty"`
}

type CommitShaState struct {
	Sha        string      `json:"sha,omitempty"`
	CommitTime metav1.Time `json:"commitTime,omitempty"`
}

func (b *CommitBranchState) DryShaShort() string {
	if b == nil {
		return ""
	}

	if len(b.Dry.Sha) < 7 {
		return b.Dry.Sha
	}

	return b.Dry.Sha[:7]
}

// ProposedCommitStatus defines the observed state of ProposedCommit
type ProposedCommitStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Proposed CommitBranchState `json:"proposed,omitempty"`
	Active   CommitBranchState `json:"active,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// +kubebuilder:printcolumn:name="Active Dry Sha",type=string,JSONPath=`.status.active.dry.sha`
// +kubebuilder:printcolumn:name="Proposed Dry Sha",type=string,JSONPath=`.status.proposed.dry.sha`
// ProposedCommit is the Schema for the proposedcommits API
type ProposedCommit struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ProposedCommitSpec   `json:"spec,omitempty"`
	Status ProposedCommitStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ProposedCommitList contains a list of ProposedCommit
type ProposedCommitList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ProposedCommit `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ProposedCommit{}, &ProposedCommitList{})
}
