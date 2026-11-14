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

// CommitValidationPhase is the outcome of a validation request.
// +kubebuilder:validation:Enum=Recorded;Rejected
type CommitValidationPhase string

const (
	// CommitValidationRecorded means the validation was written to the repository.
	CommitValidationRecorded CommitValidationPhase = "Recorded"
	// CommitValidationRejected means the validation was not written, because the commit does not exist
	// in the repository.
	CommitValidationRejected CommitValidationPhase = "Rejected"
)

// CommitValidationSource says how a validation was made.
// +kubebuilder:validation:Enum=Resource;Automatic;Git
type CommitValidationSource string

const (
	// CommitValidationSourceResource is a validation submitted through a CommitValidation.
	CommitValidationSourceResource CommitValidationSource = "Resource"
	// CommitValidationSourceAutomatic is a validation the ValidatedCommitStatus controller wrote once
	// the upstream environments succeeded.
	CommitValidationSourceAutomatic CommitValidationSource = "Automatic"
	// CommitValidationSourceGit is a validation pushed directly as a git note.
	CommitValidationSourceGit CommitValidationSource = "Git"
)

// CommitValidationSpec defines the desired state of CommitValidation. promotionStrategyRef and branch are
// immutable: a CommitValidation stands for one environment of one strategy, which is what lets RBAC by
// resource name decide who may validate that environment. sha and reason together are one request.
// +kubebuilder:validation:XValidation:rule="self.promotionStrategyRef == oldSelf.promotionStrategyRef && self.branch == oldSelf.branch",message="promotionStrategyRef and branch are immutable; create a new CommitValidation for a different strategy or environment"
// +kubebuilder:validation:XValidation:rule="!has(self.reason) || has(self.sha)",message="reason requires sha"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.sha) || !has(self.sha) || self.sha != oldSelf.sha || (has(self.reason) == has(oldSelf.reason) && (!has(self.reason) || self.reason == oldSelf.reason))",message="reason cannot change while sha stays the same; sha and reason are one request"
type CommitValidationSpec struct {
	// PromotionStrategyRef selects the PromotionStrategy that owns the environment.
	// +required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef"`

	// Branch is the environment branch on that PromotionStrategy that this resource validates commits for.
	// Must not start with '-', contain ':', or contain '..'.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('-')",message="branch must not start with '-'"
	// +kubebuilder:validation:XValidation:rule="!self.contains(':')",message="branch must not contain ':'"
	// +kubebuilder:validation:XValidation:rule="!self.contains('..')",message="branch must not contain '..'"
	Branch string `json:"branch"`

	// Sha is the dry commit to validate for the environment. It may be any commit in the repository, on
	// or off the dry branch. Omit it to use the resource only to view the environment's validation
	// history. It cannot change until status.request reports the current value as handled.
	// +optional
	// +kubebuilder:validation:MinLength=40
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	Sha string `json:"sha,omitempty"`

	// Reason is copied into the validation, for example a change ticket. It belongs to sha and can only
	// be set or changed together with it.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	Reason string `json:"reason,omitempty"`
}

// CommitValidationRequestStatus is the outcome for the SHA currently in spec.
type CommitValidationRequestStatus struct {
	// HandledAt is when the controller recorded or rejected the request.
	// +required
	HandledAt metav1.Time `json:"handledAt"`

	// Sha is the spec.sha this outcome is for.
	// +required
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	Sha string `json:"sha"`

	// Phase is Recorded when the validation was written to the repository, and Rejected when it was not.
	// +required
	Phase CommitValidationPhase `json:"phase"`

	// Message explains a rejection.
	// +optional
	Message string `json:"message,omitempty"`
}

// CommitValidationHistoryEntry is one validation of the environment, read back from the repository.
type CommitValidationHistoryEntry struct {
	// ValidatedAt is when the validation was made.
	// +required
	ValidatedAt metav1.Time `json:"validatedAt"`

	// Sha is the validated dry commit.
	// +required
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	Sha string `json:"sha"`

	// Source says how the validation was made: through a CommitValidation (Resource), by the ordering
	// controller once upstream environments succeeded (Automatic), or as a pushed git note (Git).
	// +required
	Source CommitValidationSource `json:"source"`

	// ValidatedBy names who or what validated the commit. For a pushed note without one, it is the
	// author of the notes commit.
	// +optional
	ValidatedBy string `json:"validatedBy,omitempty"`

	// Reason is the reason given with the validation, if any.
	// +optional
	Reason string `json:"reason,omitempty"`
}

// CommitValidationStatus defines the observed state of CommitValidation.
type CommitValidationStatus struct {
	// InstanceID mirrors metadata.labels[promoter.argoproj.io/instance-id] stamped on each
	// reconcile attempt by this install's controller, including when Ready=False; omitted
	// when the resource has no instance-id label (default install).
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`
	InstanceID *string `json:"instanceID,omitempty"`

	// Request is the outcome for the SHA currently in spec. Omitted while spec.sha is not set or has
	// not been handled yet.
	// +optional
	Request *CommitValidationRequestStatus `json:"request,omitempty"`

	// Conditions represent the latest available observations of an object's state
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// History lists the environment's validations, newest first, capped at 20. It is rebuilt from the
	// git notes under refs/notes/promoter.validations, which are the record.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=20
	History []CommitValidationHistoryEntry `json:"history,omitempty"`

	// ObservedGeneration is the .metadata.generation that this status was reconciled from.
	// Because status is written via Server-Side Apply with ForceOwnership (which has no
	// optimistic-concurrency check), this field is the canonical way to detect stale
	// status writes: compare status.observedGeneration with metadata.generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:ac:generate=true
// +kubebuilder:externalDocs:url="https://gitops-promoter.readthedocs.io/en/stable/crd-specs/#commitvalidation",description="CRD reference (examples and behavior)"
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// CommitValidation is the Schema for the commitvalidations API. It submits manual validations for one
// environment of a PromotionStrategy and shows that environment's validation history.
//
// The work queue coalesces events, so a second change to spec.sha before the first was handled would be
// lost. The rules below make the API server reject it instead.
// +kubebuilder:printcolumn:name="PromotionStrategy",type=string,JSONPath=`.spec.promotionStrategyRef.name`
// +kubebuilder:printcolumn:name="Branch",type=string,JSONPath=`.spec.branch`
// +kubebuilder:printcolumn:name="Sha",type=string,JSONPath=`.spec.sha`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.request.phase`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:validation:XValidation:rule=`!has(oldSelf.spec.sha) || (has(self.spec.sha) && self.spec.sha == oldSelf.spec.sha) || (has(oldSelf.status) && has(oldSelf.status.request) && oldSelf.status.request.sha == oldSelf.spec.sha)`,message="spec.sha cannot change until status.request reports the current SHA as handled"
// +kubebuilder:validation:XValidation:rule=`has(oldSelf.spec.sha) || !has(self.spec.sha) || !has(oldSelf.status) || !has(oldSelf.status.request)`,message="spec.sha cannot be set until status.request for the previous SHA has been cleared"
type CommitValidation struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of CommitValidation
	// +required
	Spec CommitValidationSpec `json:"spec"`

	// status defines the observed state of CommitValidation
	// +optional
	Status CommitValidationStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CommitValidationList contains a list of CommitValidation
type CommitValidationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CommitValidation `json:"items"`
}

// GetConditions returns the conditions of the CommitValidation.
func (c *CommitValidation) GetConditions() *[]metav1.Condition {
	return &c.Status.Conditions
}

// SetObservedGeneration records the object generation that produced the current status.
func (c *CommitValidation) SetObservedGeneration(generation int64) {
	c.Status.ObservedGeneration = generation
}

// SetStatusInstanceID records the instance-id label mirrored into status on each reconcile attempt.
func (c *CommitValidation) SetStatusInstanceID(id *string) {
	c.Status.InstanceID = id
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &CommitValidation{}, &CommitValidationList{})
		return nil
	})
}
