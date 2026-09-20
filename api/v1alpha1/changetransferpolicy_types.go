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
	"k8s.io/apimachinery/pkg/runtime"
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
	// Must not start with '-', contain ':', or contain '..'.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('-')",message="branch must not start with '-'"
	// +kubebuilder:validation:XValidation:rule="!self.contains(':')",message="branch must not contain ':'"
	// +kubebuilder:validation:XValidation:rule="!self.contains('..')",message="branch must not contain '..'"
	ProposedBranch string `json:"proposedBranch"`

	// ActiveBranch staging hydrated branch
	// Must not start with '-', contain ':', or contain '..'.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('-')",message="branch must not start with '-'"
	// +kubebuilder:validation:XValidation:rule="!self.contains(':')",message="branch must not contain ':'"
	// +kubebuilder:validation:XValidation:rule="!self.contains('..')",message="branch must not contain '..'"
	ActiveBranch string `json:"activeBranch"`

	// ActivePath is an optional repository subpath for this policy's active state.
	// When set, hydrator metadata is read from <activePath>/hydrator.metadata.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	ActivePath string `json:"activePath,omitempty"`

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

	// PullRequest configures SCM pull request behavior for this change transfer policy.
	// Copied from the owning PromotionStrategy by the PromotionStrategy controller.
	// +kubebuilder:validation:Optional
	PullRequest *PullRequestPolicySpec `json:"pullRequest,omitempty"`
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

	// Description is the description of the commit status
	Description string `json:"description,omitempty"`
}

// CommitBranchState defines the state of a branch in a ChangeTransferPolicy.
type CommitBranchState struct {
	// Dry is the dry state of the branch, which is the commit that is being proposed.
	// +nullable
	Dry CommitShaState `json:"dry,omitempty"`
	// Hydrated is the hydrated state of the branch, which is the commit that is currently being worked on.
	Hydrated CommitShaState `json:"hydrated,omitempty"`
	// Note is the hydrator metadata from the git note attached to the hydrated commit.
	Note *HydratorMetadata `json:"note,omitempty"`
	// CommitStatuses is a list of commit statuses that are being monitored for this branch.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=100
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
	// Supports both SHA-1 (40 chars) and SHA-256 (64 chars) Git hash formats.
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
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
	// +kubebuilder:validation:MaxItems=100
	References []RevisionReference `json:"references,omitempty"`
}

// CommitShaState defines the state of a commit in a branch.
type CommitShaState struct {
	// Sha is the SHA of the commit in the branch
	// Supports both SHA-1 (40 chars) and SHA-256 (64 chars) Git hash formats.
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
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
	// +kubebuilder:validation:MaxItems=100
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
	// ObservedGeneration is the .metadata.generation that this status was reconciled from.
	// Because status is written via Server-Side Apply with ForceOwnership (which has no
	// optimistic-concurrency check), this field is the canonical way to detect stale
	// status writes: compare status.observedGeneration with metadata.generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Proposed is the state of the proposed branch.
	Proposed CommitBranchState `json:"proposed,omitempty"`
	// Active is the state of the active branch.
	Active CommitBranchState `json:"active,omitempty"`
	// PullRequest is the state of the pull request that was created for this ChangeTransferPolicy.
	PullRequest *PullRequestCommonStatus `json:"pullRequest,omitempty"`

	// Conditions Represents the observations of the current state.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// InstanceID mirrors metadata.labels[promoter.argoproj.io/instance-id] stamped on each
	// reconcile attempt by this install's controller, including when Ready=False; omitted
	// when the resource has no instance-id label (default install).
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`
	InstanceID *string `json:"instanceID,omitempty"`
}

const (
	// MaxEnvironments is the maximum number of environments on a PromotionStrategy spec and status.
	MaxEnvironments = 500
	// MaxCommitStatuses is the maximum number of commit statuses stored on a branch state.
	MaxCommitStatuses = 100
	// MaxRevisionReferences is the maximum number of related-commit references on a hydrated or dry commit.
	MaxRevisionReferences = 100
)

// PullRequestCommonStatus defines the common status fields for a pull request.
type PullRequestCommonStatus struct {
	// ID is the unique identifier of the pull request, set by the SCM.
	ID string `json:"id,omitempty"`
	// State is the state of the pull request.
	// +kubebuilder:validation:Enum=closed;merged;open;merged-or-closed;unknown
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
	// MergedTargetSha is the SHA that the target branch points at after the merge. It is a merge commit
	// only when the SCM created one; squash and fast-forward merges report the resulting commit on the
	// target branch instead. In the live pull request status it is mirrored from the PullRequest resource
	// and is empty until the merge is observed; in a History entry it is the active-branch commit the
	// entry describes.
	// +optional
	// +kubebuilder:validation:MinLength=40
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	MergedTargetSha string `json:"mergedTargetSha,omitempty"`
	// ExternallyMergedOrClosed indicated that the pull request was no longer open on the SCM while
	// promotion still desired it open. The PullRequest controller no longer sets this field.
	//
	// Deprecated: Use status.state merged-or-closed or unknown instead. Existing values may still
	// appear when mirrored from older PullRequest status. This field may be removed in a future API
	// revision.
	// +optional
	ExternallyMergedOrClosed *bool `json:"externallyMergedOrClosed,omitempty"`
}

// GetConditions returns the conditions of the ChangeTransferPolicy
func (ps *ChangeTransferPolicy) GetConditions() *[]metav1.Condition {
	return &ps.Status.Conditions
}

// SetObservedGeneration records the object generation that produced the current status.
func (ps *ChangeTransferPolicy) SetObservedGeneration(generation int64) {
	ps.Status.ObservedGeneration = generation
}

// SetStatusInstanceID records the instance-id label mirrored into status on each reconcile attempt.
func (ps *ChangeTransferPolicy) SetStatusInstanceID(v *string) {
	ps.Status.InstanceID = v
}

// +kubebuilder:ac:generate=true
// +kubebuilder:externalDocs:url="https://gitops-promoter.readthedocs.io/en/stable/crd-specs/#changetransferpolicy",description="CRD reference (examples and behavior)"
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// ChangeTransferPolicy is the Schema for the changetransferpolicies API
// +kubebuilder:printcolumn:name="Active Dry Sha",type=string,JSONPath=`.status.active.dry.sha`
// +kubebuilder:printcolumn:name="Proposed Dry Sha",type=string,JSONPath=`.status.proposed.dry.sha`
// +kubebuilder:printcolumn:name="Proposed Note Dry Sha",type=string,JSONPath=`.status.proposed.note.drySha`
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
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ChangeTransferPolicy{}, &ChangeTransferPolicyList{})
		return nil
	})
}
