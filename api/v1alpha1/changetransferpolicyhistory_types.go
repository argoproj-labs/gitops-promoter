/*
Copyright 2026.

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

// ChangeTransferPolicyHistorySpec defines the desired state of ChangeTransferPolicyHistory.
type ChangeTransferPolicyHistorySpec struct {
	// RepositoryReference is the repository whose active branch is inspected to reconstruct
	// the promotion history.
	// +kubebuilder:validation:Required
	RepositoryReference ObjectReference `json:"gitRepositoryRef"`

	// ActiveBranch is the hydrated active branch whose merged changes are reconstructed into history.
	// Must not start with '-', contain ':', or contain '..'.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('-')",message="branch must not start with '-'"
	// +kubebuilder:validation:XValidation:rule="!self.contains(':')",message="branch must not contain ':'"
	// +kubebuilder:validation:XValidation:rule="!self.contains('..')",message="branch must not contain '..'"
	ActiveBranch string `json:"activeBranch"`

	// ActivePath is an optional repository subpath for this environment's active state.
	// When set, hydrator metadata is read from <activePath>/hydrator.metadata.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	ActivePath string `json:"activePath,omitempty"`
}

// ChangeTransferPolicyHistoryStatus defines the observed state of ChangeTransferPolicyHistory.
type ChangeTransferPolicyHistoryStatus struct {
	// ObservedGeneration is the .metadata.generation that this status was reconciled from.
	// Because status is written via Server-Side Apply with ForceOwnership (which has no
	// optimistic-concurrency check), this field is the canonical way to detect stale
	// status writes: compare status.observedGeneration with metadata.generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// History defines the history of promoted changes for this environment. You can think of
	// it as a list of PRs merged by GitOps Promoter. It will not include changes that were manually merged.
	// The history length is at most 20 entries.
	// History is constructed on a best-effort basis and should be used for informational purposes only.
	// History is in reverse chronological order (newest is first).
	// +kubebuilder:validation:MaxItems=20
	History []History `json:"history,omitempty"`

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

// MaxPromotionHistory is the maximum number of promotion history entries stored on
// ChangeTransferPolicyHistory.status.history.
const MaxPromotionHistory = 20

// History describes a particular change that was promoted into an environment's active branch.
type History struct {
	// Proposed is the state of the proposed branch at the time the PR was merged.
	Proposed CommitBranchStateHistoryProposed `json:"proposed,omitempty"`
	// Active is the state of the active branch at the time the PR was merged. Its dry state is read back from
	// <activePath>/hydrator.metadata on the merge commit and its hydrated state from that commit itself, so both
	// describe what actually merged regardless of merge style. Its commitStatuses, by contrast, come from the
	// snapshot trailers and may be stale when mergeCommitSnapshotMismatch is true.
	Active CommitBranchState `json:"active,omitempty"`
	// PullRequest is the state of the pull request that promoted this change.
	PullRequest *PullRequestCommonStatus `json:"pullRequest,omitempty"`
	// MergeCommitSnapshotMismatch indicates hydrator metadata on the SCM-reported merge commit disagreed with
	// the promoter's last snapshot (typically an external merge after the proposed branch advanced). When true,
	// the fields this entry rebuilds from the snapshot trailers — proposed.commitStatuses and
	// active.commitStatuses, plus proposed.hydrated when the merge was a squash (a single-parent squash commit
	// gives the controller nothing to reconstruct the hydrated sha from) — may describe the earlier proposed
	// revision rather than what actually merged.
	MergeCommitSnapshotMismatch bool `json:"mergeCommitSnapshotMismatch,omitempty"`
	// RestoredFrom is set when this entry describes a manual restore of the active branch rather than a merged
	// pull request. Its value is the hydrated SHA the branch was restored to. A restore reuses that version's
	// tree, so active.dry repeats an earlier entry's dry SHA; this field is what distinguishes the two. The
	// pull request and commit status fields are copied from the restored version and describe the original
	// promotion, not the restore.
	// +optional
	// +kubebuilder:validation:MinLength=40
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	RestoredFrom string `json:"restoredFrom,omitempty"`
}

// CommitBranchStateHistoryProposed is identical to CommitBranchState minus the Dry state. In the context of History, the Dry state is not relevant as
// the proposed dry side at merge becomes the Active.
type CommitBranchStateHistoryProposed struct {
	// Hydrated is the hydrated state of the branch, which is the commit that is currently being worked on.
	// Read from the snapshot trailers. On a regular merge it is corrected to the merge commit's second parent,
	// but a squash commit has only one parent, so when the entry's mergeCommitSnapshotMismatch is true and the
	// merge was a squash the stale snapshot value is kept.
	Hydrated CommitShaState `json:"hydrated,omitempty"`
	// CommitStatuses is a list of commit statuses that were being monitored for this branch.
	// This contains the state frozen at the moment the PR was merged. When the entry's
	// mergeCommitSnapshotMismatch is true, these phases come from snapshot trailers describing the proposed
	// revision the promoter last saw, which is not necessarily the revision that merged.
	// +kubebuilder:validation:MaxItems=100
	CommitStatuses []ChangeRequestPolicyCommitStatusPhase `json:"commitStatuses,omitempty"`
}

// +kubebuilder:ac:generate=true
// +kubebuilder:externalDocs:url="https://gitops-promoter.readthedocs.io/en/stable/crd-specs/#changetransferpolicyhistory",description="CRD reference (examples and behavior)"
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ChangeTransferPolicyHistory is the Schema for the changetransferpolicyhistories API.
// +kubebuilder:printcolumn:name="Active Branch",type=string,JSONPath=`.spec.activeBranch`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type ChangeTransferPolicyHistory struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of ChangeTransferPolicyHistory
	// +required
	Spec ChangeTransferPolicyHistorySpec `json:"spec"`

	// status defines the observed state of ChangeTransferPolicyHistory
	// +optional
	Status ChangeTransferPolicyHistoryStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ChangeTransferPolicyHistoryList contains a list of ChangeTransferPolicyHistory.
type ChangeTransferPolicyHistoryList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ChangeTransferPolicyHistory `json:"items"`
}

// GetConditions returns the conditions of the ChangeTransferPolicyHistory.
func (ctph *ChangeTransferPolicyHistory) GetConditions() *[]metav1.Condition {
	return &ctph.Status.Conditions
}

// SetObservedGeneration records the object generation that produced the current status.
func (ctph *ChangeTransferPolicyHistory) SetObservedGeneration(generation int64) {
	ctph.Status.ObservedGeneration = generation
}

// SetStatusInstanceID records the instance-id label mirrored into status on each reconcile attempt.
func (ctph *ChangeTransferPolicyHistory) SetStatusInstanceID(v *string) {
	ctph.Status.InstanceID = v
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ChangeTransferPolicyHistory{}, &ChangeTransferPolicyHistoryList{})
		return nil
	})
}
