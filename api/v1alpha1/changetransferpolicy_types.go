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

// ChangeTransferPolicySpec defines the desired state of ChangeTransferPolicy
type ChangeTransferPolicySpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// RepositoryReference what repository to open the PR on.
	// +kubebuilder:validation:Required
	RepositoryReference ObjectReference `json:"gitRepositoryRef"`

	// ProposedBranch staging hydrated branch
	// +kubebuilder:validation:Required
	ProposedBranch string `json:"proposedBranch"`

	// ActiveBranch staging hydrated branch
	// +kubebuilder:validation:Required
	ActiveBranch string `json:"activeBranch"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=true
	AutoMerge *bool `json:"autoMerge,omitempty"`

	// ActiveCommitStatuses lists the statuses to be monitored on the active branch
	// +kubebuilder:validation:Optional
	// +listType:=map
	// +listMapKey=key
	ActiveCommitStatuses []CommitStatusSelector `json:"activeCommitStatuses"`

	// ProposedCommitStatuses lists the statuses to be monitored on the proposed branch
	// +kubebuilder:validation:Optional
	// +listType:=map
	// +listMapKey=key
	ProposedCommitStatuses []CommitStatusSelector `json:"proposedCommitStatuses"`
}

type ChangeRequestPolicyCommitStatusPhase struct {
	// Key staging hydrated branch
	// +kubebuilder:validation:Required
	Key string `json:"key"`

	// Phase what phase is the status in
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum:=pending;success;failure
	Phase string `json:"phase"`

	// Url is the URL of the commit status
	Url string `json:"url,omitempty"`
}

type CommitBranchState struct {
	Dry      CommitShaState `json:"dry,omitempty"`
	Hydrated CommitShaState `json:"hydrated,omitempty"`
	// +kubebuilder:validation:Optional
	// +listType:=map
	// +listMapKey=key
	CommitStatuses []ChangeRequestPolicyCommitStatusPhase `json:"commitStatuses,omitempty"`
}

type CommitShaState struct {
	// Sha is the SHA of the commit in the branch
	Sha string `json:"sha,omitempty"`
	// CommitTime is the time the commit was made
	CommitTime metav1.Time `json:"commitTime,omitempty"`
	// RepoURL is the URL of the repository where the commit is located
	RepoURL string `json:"repoURL,omitempty"`
	// Author is the author of the commit
	Author string `json:"author,omitempty"`
	// Subject is the subject line of the commit message
	Subject string `json:"subject,omitempty"`
	// Body is the body of the commit message without the subject line
	Body string `json:"body,omitempty"`
	// References are the references to other commits, that went into the hydration of the branch
	References []RevisionReference `json:"references,omitempty"`
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

// ChangeTransferPolicyStatus defines the observed state of ChangeTransferPolicy
type ChangeTransferPolicyStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	Proposed CommitBranchState `json:"proposed,omitempty"`
	Active   CommitBranchState `json:"active,omitempty"`

	// PullRequest is the state of the pull request that was created for this ChangeTransferPolicy.
	PullRequest *PullRequestCommonStatus `json:"pullRequest,omitempty"`

	// Conditions Represents the observations of the current state.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

type PullRequestCommonStatus struct {
	ID string `json:"id,omitempty"`
	// +kubebuilder:validation:Enum=closed;merged;open
	State          PullRequestState `json:"state,omitempty"`
	PRCreationTime metav1.Time      `json:"prCreationTime,omitempty"`
	// Url is the URL of the pull request.
	Url string `json:"url,omitempty"`
}

func (ps *ChangeTransferPolicy) GetConditions() *[]metav1.Condition {
	return &ps.Status.Conditions
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// +kubebuilder:printcolumn:name="Active Dry Sha",type=string,JSONPath=`.status.active.dry.sha`
// +kubebuilder:printcolumn:name="Proposed Dry Sha",type=string,JSONPath=`.status.proposed.dry.sha`
// ChangeTransferPolicy is the Schema for the changetransferpolicies API
type ChangeTransferPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ChangeTransferPolicySpec   `json:"spec,omitempty"`
	Status ChangeTransferPolicyStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ChangeTransferPolicyList contains a list of ChangeTransferPolicy
type ChangeTransferPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ChangeTransferPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ChangeTransferPolicy{}, &ChangeTransferPolicyList{})
}
