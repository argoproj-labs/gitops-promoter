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

// RevertCommitSpec defines the desired state of RevertCommit. It is immutable: the restore runs
// once, and status.blockedDrySha is read from the active tip it moved off of, so pointing an
// existing RevertCommit at a different sha or policy would lose track of what it reverted. To
// restore something else, create a new RevertCommit.
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable; create a new RevertCommit to restore a different commit or policy"
type RevertCommitSpec struct {
	// ChangeTransferPolicyRef selects the ChangeTransferPolicy whose active branch is restored.
	// The policy supplies the repository, the active and proposed branches, and activePath.
	// +kubebuilder:validation:Required
	ChangeTransferPolicyRef ObjectReference `json:"changeTransferPolicyRef"`

	// Sha is the hydrated commit to restore onto the active branch. The controller writes a new
	// commit (the commit's tree, or only activePath when the policy sets one) parented on the
	// current active tip and records a promotion-history note with Promoter-restored-from.
	// The proposed branch is left as the hydrator wrote it. The ChangeTransferPolicy does not open
	// a promotion pull request that would put the active branch's dry SHA back. A pull request
	// for a different proposed dry SHA may open, but nothing is auto-merged while this
	// RevertCommit exists. Deleting it lifts that hold but does not by itself propose the
	// reverted change again; see status.blockedDrySha.
	// The restore runs once. Later promotions are left alone.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=40
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	Sha string `json:"sha"`
}

// RevertCommitStatus defines the observed state of RevertCommit.
type RevertCommitStatus struct {
	// ObservedGeneration is the .metadata.generation that this status was reconciled from.
	// Because status is written via Server-Side Apply with ForceOwnership (which has no
	// optimistic-concurrency check), this field is the canonical way to detect stale
	// status writes: compare status.observedGeneration with metadata.generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ActiveSha is the commit on the active branch after a successful restore.
	// +optional
	// +kubebuilder:validation:MinLength=40
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	ActiveSha string `json:"activeSha,omitempty"`

	// BlockedDrySha is the dry SHA read from hydrator.metadata on the active tip that this restore
	// moved off of. The ChangeTransferPolicy does not open a promotion pull request while its
	// proposed dry SHA still equals this value, so the reverted change is not put back. A different
	// proposed dry SHA may open a pull request, but nothing is auto-merged while this RevertCommit
	// exists. Empty when that active tip had no hydrator.metadata. Deleting the RevertCommit lifts
	// this block, but a promotion pull request only opens when the proposed branch has a commit the
	// active branch does not already contain. The restore commit is parented on the tip it moved
	// off of, so when that tip already contains the proposed commit (a merge-commit promotion),
	// this dry SHA is not proposed again until the hydrator writes a new commit to the proposed branch.
	// +optional
	// +kubebuilder:validation:MinLength=40
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	BlockedDrySha string `json:"blockedDrySha,omitempty"`

	// RestoredFrom is the spec.sha this status applied. It is written in the same status update as
	// activeSha and blockedDrySha, so it alone marks the restore as done. While it matches spec.sha
	// the controller does not restore again, so a later promotion is not overwritten on resync.
	// +optional
	// +kubebuilder:validation:MinLength=40
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	RestoredFrom string `json:"restoredFrom,omitempty"`

	// Conditions represent the latest available observations of an object's state.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// InstanceID mirrors metadata.labels[promoter.argoproj.io/instance-id] stamped on each
	// reconcile attempt by this install's controller, including when Ready=False; omitted
	// when the resource has no instance-id label (default install).
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`
	InstanceID *string `json:"instanceID,omitempty"`
}

// GetConditions returns the conditions of the RevertCommit.
func (r *RevertCommit) GetConditions() *[]metav1.Condition {
	return &r.Status.Conditions
}

// SetObservedGeneration records the object generation that produced the current status.
func (r *RevertCommit) SetObservedGeneration(generation int64) {
	r.Status.ObservedGeneration = generation
}

// SetStatusInstanceID records the instance-id label mirrored into status on each reconcile attempt.
func (r *RevertCommit) SetStatusInstanceID(v *string) {
	r.Status.InstanceID = v
}

// +kubebuilder:ac:generate=true
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// RevertCommit restores one environment's active branch to a previously hydrated commit and records
// the dry SHA that was on the active branch then, so that dry SHA is not promoted again.
// Creating the resource is the authorization boundary: whoever can create a RevertCommit in the
// policy's namespace can restore that environment, and the controller's git credentials perform the push.
// The controller sets the referenced ChangeTransferPolicy as owner, so deleting the policy removes its reverts.
// +kubebuilder:printcolumn:name="ChangeTransferPolicy",type=string,JSONPath=`.spec.changeTransferPolicyRef.name`
// +kubebuilder:printcolumn:name="Sha",type=string,JSONPath=`.spec.sha`,priority=1
// +kubebuilder:printcolumn:name="Active Sha",type=string,JSONPath=`.status.activeSha`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type RevertCommit struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RevertCommitSpec   `json:"spec,omitempty"`
	Status RevertCommitStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// RevertCommitList contains a list of RevertCommit
type RevertCommitList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RevertCommit `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &RevertCommit{}, &RevertCommitList{})
		return nil
	})
}
