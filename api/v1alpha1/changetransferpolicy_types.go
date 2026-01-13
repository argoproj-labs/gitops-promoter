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

// ChangeRequestPolicyCommitStatusPhase defines the phase of a commit status in a ChangeTransferPolicy.
type ChangeRequestPolicyCommitStatusPhase struct {
	// Key staging hydrated branch
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength:=1
	// +kubebuilder:validation:MaxLength:=63
	// +kubebuilder:validation:Pattern:=([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9]
	Key string `json:"key"`

	// Phase what phase is the status in
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum:=pending;success;failure
	Phase string `json:"phase"`

	// Url is the URL of the commit status
	// +kubebuilder:validation:XValidation:rule="self == '' || isURL(self)",message="must be a valid URL"
	// +kubebuilder:validation:Pattern="^(https?://.*)?$"
	Url string `json:"url,omitempty"`
}

// CommitBranchState defines the state of a branch in a ChangeTransferPolicy.
type CommitBranchState struct {
	// Dry is the dry state of the branch, which is the commit that is being proposed.
	Dry CommitShaState `json:"dry,omitempty"`
	// Hydrated is the hydrated state of the branch, which is the commit that is currently being worked on.
	Hydrated CommitShaState `json:"hydrated,omitempty"`
	// Note is the hydrator metadata from the git note attached to the hydrated commit.
	Note *HydratorMetadata `json:"note,omitempty"`
	// CommitStatuses is a list of commit statuses that are being monitored for this branch.
	// +kubebuilder:validation:Optional
	// +listType:=map
	// +listMapKey=key
	CommitStatuses []ChangeRequestPolicyCommitStatusPhase `json:"commitStatuses,omitempty"`
}

// HydratorMetadata contains metadata about the hydrated commit.
// This is extracted from the git note or metadata file.
type HydratorMetadata struct {
	// RepoURL is the URL of the repository where the commit is located.
	RepoURL string `json:"repoURL,omitempty"`
	// DrySha is the SHA of the commit that was used as the dry source for hydration.
	DrySha string `json:"drySha,omitempty"`
	// Author is the author of the dry commit that was used to hydrate the branch.
	Author string `json:"author,omitempty"`
	// Date is the date of the dry commit that was used to hydrate the branch.
	Date metav1.Time `json:"date,omitempty"`
	// Subject is the subject line of the dry commit that was used to hydrate the branch.
	Subject string `json:"subject,omitempty"`
	// Body is the body of the dry commit that was used to hydrate the branch without the subject.
	Body string `json:"body,omitempty"`
	// References are the references to other commits, that went into the hydration of the branch.
	References []RevisionReference `json:"references,omitempty"`
}

// CommitShaState defines the state of a commit in a branch.
type CommitShaState struct {
	// Sha is the SHA of the commit in the branch
	Sha string `json:"sha,omitempty"`
	// CommitTime is the time the commit was made
	CommitTime metav1.Time `json:"commitTime,omitempty"`
	// RepoURL is the URL of the repository where the commit is located
	// +kubebuilder:validation:XValidation:rule="self == '' || isURL(self)",message="must be a valid URL"
	// +kubebuilder:validation:Pattern="^(https?://.*)?$"
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

// DryShaShort returns the first 7 characters of the dry SHA, or the full SHA if it is shorter than 7 characters.
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
	// Proposed is the state of the proposed branch.
	Proposed CommitBranchState `json:"proposed,omitempty"`
	// Active is the state of the active branch.
	Active CommitBranchState `json:"active,omitempty"`
	// PullRequest is the state of the pull request that was created for this ChangeTransferPolicy.
	PullRequest *PullRequestCommonStatus `json:"pullRequest,omitempty"`

	// History defines the history of promoted changes done by the ChangeTransferPolicy. You can think of
	// it as a list of PRs merged by GitOps Promoter. It will not include changes that were manually merged.
	// The history length is hard-coded to be at most 5 entries. This may change in the future.
	// History is constructed on a best-effort basis and should be used for informational purposes only.
	// History is in reverse chronological order (newest is first).
	History []History `json:"history,omitempty"`

	// Conditions Represents the observations of the current state.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// History describes a particular change that was promoted by the ChangeTransferPolicy.
type History struct {
	// Proposed is the state of the proposed branch at the time the PR was merged.
	Proposed CommitBranchStateHistoryProposed `json:"proposed,omitempty"`
	// Active is the state of the active branch at the time the PR was merged.
	Active CommitBranchState `json:"active,omitempty"`
	// PullRequest is the state of the pull request that was created for this ChangeTransferPolicy.
	PullRequest *PullRequestCommonStatus `json:"pullRequest,omitempty"`
}

// CommitBranchStateHistoryProposed is identical to CommitBranchState minus the Dry state. In the context of History, the Dry state is not relevant as
// the proposed dry side at merge becomes the Active.
type CommitBranchStateHistoryProposed struct {
	// Hydrated is the hydrated state of the branch, which is the commit that is currently being worked on.
	Hydrated CommitShaState `json:"hydrated,omitempty"`
	// CommitStatuses is a list of commit statuses that were being monitored for this branch.
	// This contains the state frozen at the moment the PR was merged.
	CommitStatuses []ChangeRequestPolicyCommitStatusPhase `json:"commitStatuses,omitempty"`
}

// PullRequestCommonStatus defines the common status fields for a pull request.
type PullRequestCommonStatus struct {
	// ID is the unique identifier of the pull request, set by the SCM.
	ID string `json:"id,omitempty"`
	// State is the state of the pull request.
	// +kubebuilder:validation:Enum=closed;merged;open
	State PullRequestState `json:"state,omitempty"`
	// PRCreationTime is the time when the pull request was created.
	PRCreationTime metav1.Time `json:"prCreationTime,omitempty"`
	// PRMergeTime is the time when the pull request was merged. This time can vary slightly from the actual merge time because
	// it is the time when the ChangeTransferPolicy controller sets the pull requests spec to merge. In the future we plan on making
	// this time more accurate by fetching the actual merge time from the SCM via the webhook this would then be updated in the git note
	// for that commit.
	PRMergeTime metav1.Time `json:"prMergeTime,omitempty"`
	// Url is the URL of the pull request.
	// +kubebuilder:validation:XValidation:rule="self == '' || isURL(self)",message="must be a valid URL"
	// +kubebuilder:validation:Pattern="^(https?://.*)?$"
	Url string `json:"url,omitempty"`
	// ExternallyMergedOrClosed indicates that the pull request was merged or closed externally.
	// This is set to true when the pull request has an ID but is no longer found on the SCM provider.
	// When true, the State field will be empty ("") since we cannot determine if it was merged or closed.
	// This status is preserved even after the PullRequest resource is deleted, maintaining a historical
	// record of the external action until a new pull request is created for this environment.
	ExternallyMergedOrClosed *bool `json:"externallyMergedOrClosed,omitempty"`
}

// GetConditions returns the conditions of the ChangeTransferPolicy
func (ps *ChangeTransferPolicy) GetConditions() *[]metav1.Condition {
	return &ps.Status.Conditions
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// ChangeTransferPolicy is the Schema for the changetransferpolicies API
// +kubebuilder:printcolumn:name="Active Dry Sha",type=string,JSONPath=`.status.active.dry.sha`
// +kubebuilder:printcolumn:name="Proposed Dry Sha",type=string,JSONPath=`.status.proposed.dry.sha`
// +kubebuilder:printcolumn:name="PR State",type=string,JSONPath=`.status.pullRequest.state`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
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
