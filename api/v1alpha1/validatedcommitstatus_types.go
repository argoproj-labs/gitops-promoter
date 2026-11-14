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

// ValidationPolicy says who validates dry commits for an environment.
// +kubebuilder:validation:Enum=Automatic;Manual
type ValidationPolicy string

const (
	// ValidationAutomatic has the controller validate a dry commit for the environment once every
	// environment it depends on has succeeded on that commit. A root environment follows the dry branch.
	ValidationAutomatic ValidationPolicy = "Automatic"
	// ValidationManual leaves validation to a person or CI, through a CommitValidation or a pushed
	// git note. The controller writes no validations for the environment.
	ValidationManual ValidationPolicy = "Manual"
)

// AdvancePolicy says when an automatic validation may move an environment's tag.
// +kubebuilder:validation:Enum=Immediately;WhenIdle
type AdvancePolicy string

const (
	// AdvanceImmediately moves the tag as soon as an automatic validation applies.
	AdvanceImmediately AdvancePolicy = "Immediately"
	// AdvanceWhenIdle moves the tag only once the environment is idle: its hydrator has processed the
	// tag's commit, no promotion pull request is open, and no active commit status is pending.
	AdvanceWhenIdle AdvancePolicy = "WhenIdle"
)

// ValidatedTagSource says what last moved an environment's tag.
// +kubebuilder:validation:Enum=Manual;Automatic;Seeded;External
type ValidatedTagSource string

const (
	// ValidatedTagSourceManual means a manual validation (a CommitValidation or a pushed note) moved the tag.
	ValidatedTagSourceManual ValidatedTagSource = "Manual"
	// ValidatedTagSourceAutomatic means an automatic validation moved the tag.
	ValidatedTagSourceAutomatic ValidatedTagSource = "Automatic"
	// ValidatedTagSourceSeeded means the controller created the tag at what was live in the environment.
	ValidatedTagSourceSeeded ValidatedTagSource = "Seeded"
	// ValidatedTagSourceExternal means the tag was moved by hand, outside the controller.
	ValidatedTagSourceExternal ValidatedTagSource = "External"
)

// ValidatedHeldReason says why an automatic validation has not been applied.
// +kubebuilder:validation:Enum=Pinned;Busy
type ValidatedHeldReason string

const (
	// ValidatedHeldReasonPinned means the validated commit does not descend from the commit the tag is on.
	ValidatedHeldReasonPinned ValidatedHeldReason = "Pinned"
	// ValidatedHeldReasonBusy means the environment advances WhenIdle and is not idle.
	ValidatedHeldReasonBusy ValidatedHeldReason = "Busy"
)

// ValidatedCommitStatusSpec defines the desired state of ValidatedCommitStatus.
type ValidatedCommitStatusSpec struct {
	// PromotionStrategyRef is a reference to the promotion strategy this gate orders. The environments and
	// their dependencies (dependsOn) come from the PromotionStrategy.
	// +required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef"`

	// Key is the commit status key this controller writes on each environment's proposed hydrated SHA.
	// The PromotionStrategy controller injects this key onto every ChangeTransferPolicy's proposedCommitStatuses.
	// Must be lowercase alphanumeric with hyphens, 1–63 characters (pattern: ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$).
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
	Key string `json:"key"`

	// DryBranch is the branch dry commits are pushed to, in the PromotionStrategy's repository. A root
	// environment with validation Automatic follows its head.
	// Must not start with '-', contain ':', or contain '..'.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('-')",message="dryBranch must not start with '-'"
	// +kubebuilder:validation:XValidation:rule="!self.contains(':')",message="dryBranch must not contain ':'"
	// +kubebuilder:validation:XValidation:rule="!self.contains('..')",message="dryBranch must not contain '..'"
	DryBranch string `json:"dryBranch"`

	// Environments overrides how individual environments are validated and advanced. An environment of
	// the PromotionStrategy that is not listed uses validation Automatic and advance Immediately. Every
	// branch listed must be an environment of the PromotionStrategy.
	// +optional
	// +listType=map
	// +listMapKey=branch
	// +kubebuilder:validation:MaxItems=500
	Environments []ValidatedCommitStatusEnvironment `json:"environments,omitempty"`

	// URL generates the URL to use on the per-environment CommitStatus (SCM details link). Optional; when
	// empty, no URL is set on the child CommitStatus. The template receives .Environment, .Tag,
	// .ValidatedCommitStatus, and .PromotionStrategy.
	// +kubebuilder:validation:Optional
	URL URLConfig `json:"url,omitempty"`

	// PublishTo is copied to spec.publishTo on each CommitStatus this controller creates.
	// - "scm": the CommitStatus controller posts them to the SCM provider
	// The default is ["scm"]. An empty list keeps them in Kubernetes and does not post them to the SCM.
	// Omitting the field uses the default. Promotion gates use the phase in every case.
	// +optional
	// +kubebuilder:default={scm}
	// +listType=set
	// +kubebuilder:validation:items:Enum=scm
	PublishTo *[]CommitStatusPublishTo `json:"publishTo,omitempty"`
}

// ValidatedCommitStatusEnvironment sets how one environment is validated and advanced.
// +kubebuilder:validation:XValidation:rule="!(has(self.validation) && self.validation == 'Manual' && has(self.advance) && self.advance == 'WhenIdle')",message="validation Manual cannot be combined with advance WhenIdle: manual validations always apply immediately"
type ValidatedCommitStatusEnvironment struct {
	// Branch is the environment's active branch. It must match an environment of the referenced
	// PromotionStrategy.
	// Must not start with '-', contain ':', or contain '..'.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('-')",message="branch must not start with '-'"
	// +kubebuilder:validation:XValidation:rule="!self.contains(':')",message="branch must not contain ':'"
	// +kubebuilder:validation:XValidation:rule="!self.contains('..')",message="branch must not contain '..'"
	Branch string `json:"branch"`

	// Validation says who validates dry commits for this environment. Automatic (the default): the
	// controller, once every environment this one depends on has succeeded on the commit. Manual: a
	// person or CI, through a CommitValidation or a pushed git note.
	// +optional
	// +kubebuilder:default=Automatic
	Validation ValidationPolicy `json:"validation,omitempty"`

	// Advance says when an automatic validation may move this environment's tag. Immediately (the
	// default), or WhenIdle to wait until the environment has finished what it is promoting. Manual
	// validations always apply immediately.
	// +optional
	// +kubebuilder:default=Immediately
	Advance AdvancePolicy `json:"advance,omitempty"`
}

// ValidatedCommitStatusDryBranchStatus is the observed state of the dry branch.
type ValidatedCommitStatusDryBranchStatus struct {
	// Sha is the head of spec.dryBranch when the controller last reconciled.
	// +required
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	Sha string `json:"sha"`
}

// ValidatedCommitStatusTagStatus is the observed state of one environment's tag.
type ValidatedCommitStatusTagStatus struct {
	// Name is the tag's name: <environment branch>-tag, or <environment branch>-tag/<activePath> when the
	// environment has an activePath.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Sha is the dry commit the tag points at. Omitted while the tag does not exist.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	Sha string `json:"sha,omitempty"`

	// Source says what last moved the tag. External means it was moved by hand.
	// +optional
	Source ValidatedTagSource `json:"source,omitempty"`

	// MovedAt is when the controller moved the tag, or noticed that it had been moved.
	// +optional
	MovedAt *metav1.Time `json:"movedAt,omitempty"`
}

// ValidatedCommitStatusUpstreamCleared is the newest dry commit cleared for an environment.
type ValidatedCommitStatusUpstreamCleared struct {
	// Sha is the dry commit.
	// +required
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	Sha string `json:"sha"`
}

// ValidatedCommitStatusHeld is an automatic validation that has not been applied yet.
type ValidatedCommitStatusHeld struct {
	// Sha is the validated dry commit that is waiting.
	// +required
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	Sha string `json:"sha"`

	// Reason is Pinned when the commit does not descend from the commit the tag is on, and Busy when the
	// environment advances WhenIdle and is not idle.
	// +required
	Reason ValidatedHeldReason `json:"reason"`

	// Message says what the validation is waiting for.
	// +optional
	Message string `json:"message,omitempty"`
}

// ValidatedCommitStatusPending is a validated tag move that could not be pushed yet.
type ValidatedCommitStatusPending struct {
	// Sha is the dry commit the tag should point at.
	// +required
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	Sha string `json:"sha"`

	// Source is the kind of validation that asked for the move.
	// +required
	// +kubebuilder:validation:Enum=Manual;Automatic
	Source ValidatedTagSource `json:"source"`

	// Message is the error from the last attempt to push the tag.
	// +optional
	Message string `json:"message,omitempty"`
}

// ValidatedCommitStatusSuccessfulSha is a dry commit an environment succeeded on.
type ValidatedCommitStatusSuccessfulSha struct {
	// Sha is the dry commit.
	// +required
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	Sha string `json:"sha"`

	// Time is when the controller first saw the environment successful on the commit.
	// +required
	Time metav1.Time `json:"time"`
}

// ValidatedCommitStatusEnvironmentStatus defines observed state for one environment branch.
type ValidatedCommitStatusEnvironmentStatus struct {
	// Branch is the environment branch name.
	// +required
	// +kubebuilder:validation:MinLength=1
	Branch string `json:"branch"`

	// Gate report fields (phase, description, url, reportedSha) mirror the child CommitStatus spec.
	// When there is no in-flight proposed change, the controller copies the last child CommitStatus
	// report instead of re-evaluating the gate.
	GateEnvironmentCommitStatus `json:",inline"`

	// Tag is the environment's tag as last read from the repository.
	// +optional
	Tag *ValidatedCommitStatusTagStatus `json:"tag,omitempty"`

	// UpstreamCleared is the newest dry commit every environment this one depends on has succeeded on.
	// For an environment with no upstreams it is the head of the dry branch. It tells an approver what
	// is ready to validate for a Manual environment.
	// +optional
	UpstreamCleared *ValidatedCommitStatusUpstreamCleared `json:"upstreamCleared,omitempty"`

	// Held is set only while an automatic validation is waiting to be applied.
	// +optional
	Held *ValidatedCommitStatusHeld `json:"held,omitempty"`

	// Pending is set only while a validated tag move could not be pushed. The controller retries it.
	// +optional
	Pending *ValidatedCommitStatusPending `json:"pending,omitempty"`

	// SuccessfulShas lists the dry commits this environment succeeded on, newest first, capped at 10.
	// An environment succeeds on a commit when the commit is live in it, or changed nothing for it, and
	// every active commit status passes.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MaxItems=10
	SuccessfulShas []ValidatedCommitStatusSuccessfulSha `json:"successfulShas,omitempty"`
}

// ValidatedCommitStatusStatus defines the observed state of ValidatedCommitStatus.
type ValidatedCommitStatusStatus struct {
	// InstanceID mirrors metadata.labels[promoter.argoproj.io/instance-id] stamped on each
	// reconcile attempt by this install's controller, including when Ready=False; omitted
	// when the resource has no instance-id label (default install).
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$`
	InstanceID *string `json:"instanceID,omitempty"`

	// DryBranch is the observed state of spec.dryBranch.
	// +optional
	DryBranch *ValidatedCommitStatusDryBranchStatus `json:"dryBranch,omitempty"`

	// ValidationsCursor is the last commit of refs/notes/promoter.validations whose validations the
	// controller has applied. Omitted while that ref does not exist.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	ValidationsCursor string `json:"validationsCursor,omitempty"`

	// Conditions represent the latest available observations of an object's state
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Environments reports the tag, validation, and gate state of each environment.
	// +optional
	// +kubebuilder:validation:MaxItems=500
	// +listType=map
	// +listMapKey=branch
	Environments []ValidatedCommitStatusEnvironmentStatus `json:"environments,omitempty"`

	// ObservedGeneration is the .metadata.generation that this status was reconciled from.
	// Because status is written via Server-Side Apply with ForceOwnership (which has no
	// optimistic-concurrency check), this field is the canonical way to detect stale
	// status writes: compare status.observedGeneration with metadata.generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:ac:generate=true
// +kubebuilder:externalDocs:url="https://gitops-promoter.readthedocs.io/en/stable/crd-specs/#validatedcommitstatus",description="CRD reference (examples and behavior)"
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ValidatedCommitStatus is the Schema for the validatedcommitstatuses API
// +kubebuilder:printcolumn:name="PromotionStrategy",type=string,JSONPath=`.spec.promotionStrategyRef.name`
// +kubebuilder:printcolumn:name="Dry Branch",type=string,JSONPath=`.spec.dryBranch`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type ValidatedCommitStatus struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ValidatedCommitStatus
	// +required
	Spec ValidatedCommitStatusSpec `json:"spec"`

	// status defines the observed state of ValidatedCommitStatus
	// +optional
	Status ValidatedCommitStatusStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ValidatedCommitStatusList contains a list of ValidatedCommitStatus
type ValidatedCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ValidatedCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the ValidatedCommitStatus.
func (v *ValidatedCommitStatus) GetConditions() *[]metav1.Condition {
	return &v.Status.Conditions
}

// SetObservedGeneration records the object generation that produced the current status.
func (v *ValidatedCommitStatus) SetObservedGeneration(generation int64) {
	v.Status.ObservedGeneration = generation
}

// SetStatusInstanceID records the instance-id label mirrored into status on each reconcile attempt.
func (v *ValidatedCommitStatus) SetStatusInstanceID(id *string) {
	v.Status.InstanceID = id
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ValidatedCommitStatus{}, &ValidatedCommitStatusList{})
		return nil
	})
}
