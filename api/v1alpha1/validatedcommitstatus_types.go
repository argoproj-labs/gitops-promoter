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

// ValidatedCommitStatusObservedDryShaField is the field index over the dry SHAs a
// ValidatedCommitStatus last observed (see ObservedDryShas). The webhook receiver uses it to find
// the gates to wake when the dry branch or an environment tag is pushed.
const ValidatedCommitStatusObservedDryShaField = ".status.observedDryShas"

// ValidationPolicy says who validates dry commits for an environment, and so who moves its tag.
// +kubebuilder:validation:Enum=Automatic;Manual
type ValidationPolicy string

const (
	// ValidationAutomatic has the controller move the tag: to the dry branch HEAD for an environment with no
	// upstreams, and to the newest commit every upstream has qualified for any other environment.
	ValidationAutomatic ValidationPolicy = "Automatic"
	// ValidationManual leaves the tag to a person or CI. The controller never moves it on its own.
	ValidationManual ValidationPolicy = "Manual"
)

// AdvancePolicy says when the controller may move the tag of an environment it validates for.
// +kubebuilder:validation:Enum=Immediately;WhenIdle
type AdvancePolicy string

const (
	// AdvanceImmediately moves the tag as soon as there is a newer commit to move it to.
	AdvanceImmediately AdvancePolicy = "Immediately"
	// AdvanceWhenIdle moves the tag only once the environment is idle: its hydrator has processed the tag's
	// commit, no promotion pull request is open, and no active commit status is pending.
	AdvanceWhenIdle AdvancePolicy = "WhenIdle"
)

// ValidatedTagSource says what put an environment's tag where it is.
// +kubebuilder:validation:Enum=Manual;Automatic;Seeded;External
type ValidatedTagSource string

const (
	// ValidatedTagSourceManual means a person or CI validated the commit for the environment, with a line
	// in the validation notes of the dry repository, and the validation moved the tag there. The gate does
	// not ask whether the upstream environments have qualified such a commit.
	ValidatedTagSourceManual ValidatedTagSource = "Manual"
	// ValidatedTagSourceAutomatic means the controller moved the tag there, to a commit the environments
	// this one depends on have qualified.
	ValidatedTagSourceAutomatic ValidatedTagSource = "Automatic"
	// ValidatedTagSourceSeeded means the controller created the tag where the environment already was: at
	// the dry commit that was live in it, or where an automatically validated tag was headed anyway.
	ValidatedTagSourceSeeded ValidatedTagSource = "Seeded"
	// ValidatedTagSourceExternal means the tag was pushed by hand, outside the controller.
	ValidatedTagSourceExternal ValidatedTagSource = "External"
)

// ValidatedHeldReason says why the controller has not moved a tag to the commit it would move it to.
// +kubebuilder:validation:Enum=Pinned;Busy
type ValidatedHeldReason string

const (
	// ValidatedHeldReasonPinned means the commit does not contain the commit the tag is on. A tag only
	// moves forward on its own.
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

	// DrySource is where the dry commits and the environment tags live.
	// +required
	DrySource ValidatedCommitStatusDrySource `json:"drySource"`

	// Environments overrides how individual environments are set up. An environment of the
	// PromotionStrategy that is not listed uses validation Automatic and the default tag name. Every
	// branch listed must be an environment of the PromotionStrategy.
	// +optional
	// +listType=map
	// +listMapKey=branch
	// +kubebuilder:validation:MaxItems=500
	Environments []ValidatedCommitStatusEnvironment `json:"environments,omitempty"`

	// URL generates the URL to use on the per-environment CommitStatus (SCM details link). Optional; when
	// empty, no URL is set on the child CommitStatus. The template receives .Environment,
	// .ValidatedCommitStatus, .PromotionStrategy, .DependsOn, and .DependsOnQuery.
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

// ValidatedCommitStatusDrySource identifies the dry branch whose commits are promoted.
type ValidatedCommitStatusDrySource struct {
	// Branch is the dry branch. Environments with no upstreams track its HEAD, and only commits reachable
	// from its HEAD are qualified or promoted by this gate.
	// Must not start with '-', contain ':', or contain '..'.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('-')",message="branch must not start with '-'"
	// +kubebuilder:validation:XValidation:rule="!self.contains(':')",message="branch must not contain ':'"
	// +kubebuilder:validation:XValidation:rule="!self.contains('..')",message="branch must not contain '..'"
	Branch string `json:"branch"`

	// GitRepositoryRef is the GitRepository that holds the dry branch and the environment tags. Omit it when
	// that is the PromotionStrategy's own repository, which is the usual case.
	// +optional
	GitRepositoryRef *ObjectReference `json:"gitRepositoryRef,omitempty"`
}

// ValidatedCommitStatusEnvironment sets up one environment.
// +kubebuilder:validation:XValidation:rule="!(has(self.validation) && self.validation == 'Manual' && has(self.advance) && self.advance == 'WhenIdle')",message="validation Manual cannot be combined with advance WhenIdle: the controller does not move the tag of an environment people validate for"
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

	// Tag overrides the name of the tag in the dry repository that this environment's hydrator reads
	// (without the refs/tags/ prefix). The default is <branch>-tag, or <branch>-tag/<activePath> when the
	// environment has an activePath. Set it when that name is taken, for example because several
	// PromotionStrategies with the same environment branches share one dry repository. Every environment
	// needs its own tag.
	// Must not start with '-', contain ':', or contain '..'.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('-')",message="tag must not start with '-'"
	// +kubebuilder:validation:XValidation:rule="!self.contains(':')",message="tag must not contain ':'"
	// +kubebuilder:validation:XValidation:rule="!self.contains('..')",message="tag must not contain '..'"
	Tag string `json:"tag,omitempty"`

	// Validation says who validates dry commits for this environment. Automatic (the default): the
	// controller moves the tag to the newest commit every environment this one depends on has qualified.
	// Manual: a person or CI moves the tag; the controller still verifies that what they tagged is
	// hydrated and qualified upstream before it may be promoted.
	// +optional
	// +kubebuilder:default=Automatic
	Validation ValidationPolicy `json:"validation,omitempty"`

	// Advance says when the controller may move this environment's tag. Immediately (the default), or
	// WhenIdle to wait until the environment has finished what it is promoting, so that a promotion that
	// is under way is not replaced by the next one. A validation by a person always applies immediately.
	// +optional
	// +kubebuilder:default=Immediately
	Advance AdvancePolicy `json:"advance,omitempty"`
}

// ValidatedCommitStatusDryStatus is the observed state of the dry branch.
type ValidatedCommitStatusDryStatus struct {
	// Sha is the HEAD of the dry branch when the controller last reconciled.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	Sha string `json:"sha,omitempty"`
}

// ValidatedCommitStatusTagStatus is the observed state of one environment tag.
type ValidatedCommitStatusTagStatus struct {
	// Name is the tag name.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Sha is the dry commit the tag points at. Empty when the tag does not exist.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	Sha string `json:"sha,omitempty"`

	// Source says what put the tag where it is. External means it was pushed by hand. Omitted while the
	// tag does not exist.
	// +optional
	Source ValidatedTagSource `json:"source,omitempty"`

	// MovedAt is when the controller moved the tag, or noticed that it had been moved.
	// +optional
	MovedAt *metav1.Time `json:"movedAt,omitempty"`

	// ValidatedBy names who or what validated the commit the tag is on. Only set while source is Manual.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	ValidatedBy string `json:"validatedBy,omitempty"`

	// AcknowledgedTarget is set while a person decided where the tag is, by hand or with a validation.
	// It is the commit the controller would have moved the tag to at that moment (see upstreamCleared).
	// The tag stays where the person put it until the controller has a newer commit than this one to move
	// it to. Omitted for a tag the controller placed.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	AcknowledgedTarget string `json:"acknowledgedTarget,omitempty"`
}

// ValidatedCommitStatusUpstreamCleared is the newest dry commit that is cleared for an environment.
type ValidatedCommitStatusUpstreamCleared struct {
	// Sha is the dry commit.
	// +required
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	Sha string `json:"sha"`
}

// ValidatedCommitStatusHeld is a commit the controller would move an environment's tag to, and cannot yet.
type ValidatedCommitStatusHeld struct {
	// Sha is the dry commit that is waiting.
	// +required
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	Sha string `json:"sha"`

	// Reason is Pinned when the commit does not contain the commit the tag is on, and Busy when the
	// environment advances WhenIdle and is not idle.
	// +required
	Reason ValidatedHeldReason `json:"reason"`

	// Message says what the commit is waiting for.
	// +optional
	Message string `json:"message,omitempty"`
}

// ValidatedCommitStatusPending is a move of an environment's tag that a validation asked for and that
// could not be pushed yet.
type ValidatedCommitStatusPending struct {
	// Sha is the validated dry commit the tag should point at.
	// +required
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	Sha string `json:"sha"`

	// ValidatedBy names who or what validated the commit.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	ValidatedBy string `json:"validatedBy,omitempty"`

	// Message is the error from the last attempt to push the tag.
	// +optional
	Message string `json:"message,omitempty"`
}

// ValidatedCommitStatusWatermark is the newest dry commit an environment has qualified: a commit that was live
// in the environment while every active commit status was success. Every commit before it on the dry branch
// is qualified too.
type ValidatedCommitStatusWatermark struct {
	// QualifiedAt is when the controller recorded the qualification.
	// +required
	QualifiedAt metav1.Time `json:"qualifiedAt"`

	// DrySha is the newest qualified dry commit.
	// +required
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	DrySha string `json:"drySha"`

	// HydratedSha is the commit on the environment's active branch that was live at the time.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	HydratedSha string `json:"hydratedSha,omitempty"`
}

// ValidatedCommitStatusUpstreamStatus reports whether a direct upstream has qualified the commit
// this environment's tag points at.
type ValidatedCommitStatusUpstreamStatus struct {
	// Branch is the upstream environment branch name.
	// +required
	// +kubebuilder:validation:MinLength=1
	Branch string `json:"branch"`

	// Reason explains why the upstream is not satisfied. Omitted when satisfied is true.
	// +optional
	Reason string `json:"reason,omitempty"`

	// Satisfied is true when the upstream has qualified the tagged commit or a newer commit that contains it.
	// +required
	Satisfied bool `json:"satisfied"`
}

// ValidatedCommitStatusEnvironmentStatus defines observed state for one environment branch.
type ValidatedCommitStatusEnvironmentStatus struct {
	// Tag is the environment's tag as last read from the dry repository.
	// +optional
	Tag *ValidatedCommitStatusTagStatus `json:"tag,omitempty"`

	// Qualified is the newest dry commit this environment has qualified. Omitted until the environment has
	// qualified a commit.
	// +optional
	Qualified *ValidatedCommitStatusWatermark `json:"qualified,omitempty"`

	// UpstreamCleared is the newest dry commit every environment this one depends on has qualified. For an
	// environment with no upstreams it is the head of the dry branch. It is where the controller moves an
	// automatically validated tag, and tells an approver what is ready for a Manual environment. Omitted
	// while an upstream has qualified nothing.
	// +optional
	UpstreamCleared *ValidatedCommitStatusUpstreamCleared `json:"upstreamCleared,omitempty"`

	// Held is set only while the controller has a commit to move the tag to and cannot move it there.
	// +optional
	Held *ValidatedCommitStatusHeld `json:"held,omitempty"`

	// Pending is set only while a validation asked for a move of the tag that could not be pushed. The
	// controller tries again, and moves the tag nowhere else meanwhile. A newer validation, or a push of
	// the tag by hand, takes its place.
	// +optional
	Pending *ValidatedCommitStatusPending `json:"pending,omitempty"`

	// Branch is the environment branch name.
	// +required
	// +kubebuilder:validation:MinLength=1
	Branch string `json:"branch"`

	// Gate report fields (phase, description, url, reportedSha) mirror the child CommitStatus spec.
	// When there is no in-flight proposed change, the controller copies the last child CommitStatus
	// report instead of re-evaluating the gate.
	GateEnvironmentCommitStatus `json:",inline"`

	// ActiveCommitStatuses is a verbatim copy of the PromotionStrategy environment's active commit statuses.
	// +optional
	// +kubebuilder:validation:MaxItems=100
	// +listType=map
	// +listMapKey=key
	ActiveCommitStatuses []ChangeRequestPolicyCommitStatusPhase `json:"activeCommitStatuses,omitempty"`

	// Upstreams lists this environment's direct upstreams and whether each has qualified the tagged commit.
	// +optional
	// +listType=atomic
	Upstreams []ValidatedCommitStatusUpstreamStatus `json:"upstreams,omitempty"`
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

	// Dry is the observed state of the dry branch.
	// +optional
	Dry *ValidatedCommitStatusDryStatus `json:"dry,omitempty"`

	// ValidationsCursor is the last commit of refs/notes/promoter.validations in the dry repository whose
	// validations the controller has applied. Omitted while that ref does not exist.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	ValidationsCursor string `json:"validationsCursor,omitempty"`

	// Conditions represent the latest available observations of an object's state
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Environments reports the tag, qualification, and gate state of each environment.
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
func (d *ValidatedCommitStatus) GetConditions() *[]metav1.Condition {
	return &d.Status.Conditions
}

// SetObservedGeneration records the object generation that produced the current status.
func (d *ValidatedCommitStatus) SetObservedGeneration(generation int64) {
	d.Status.ObservedGeneration = generation
}

// SetStatusInstanceID records the instance-id label mirrored into status on each reconcile attempt.
func (d *ValidatedCommitStatus) SetStatusInstanceID(v *string) {
	d.Status.InstanceID = v
}

// ObservedDryShas returns the dry SHAs this gate last observed: the dry branch HEAD and every environment
// tag. A push webhook's "before" SHA is one of them when the push moved a ref this gate reads.
func (d *ValidatedCommitStatus) ObservedDryShas() []string {
	var shas []string
	if d.Status.Dry != nil && d.Status.Dry.Sha != "" {
		shas = append(shas, d.Status.Dry.Sha)
	}
	for _, env := range d.Status.Environments {
		if env.Tag != nil && env.Tag.Sha != "" {
			shas = append(shas, env.Tag.Sha)
		}
	}
	return shas
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ValidatedCommitStatus{}, &ValidatedCommitStatusList{})
		return nil
	})
}
