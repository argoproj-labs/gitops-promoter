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

// ScheduledCommitStatusSpec defines the desired state of ScheduledCommitStatus.
// +kubebuilder:validation:XValidation:rule="self.environments.all(e, size(e.allow) > 0 || size(e.exclude) > 0 || size(self.allow) > 0 || size(self.exclude) > 0)",message="each environment must have at least one allow or exclude window, either per-environment or global"
type ScheduledCommitStatusSpec struct {
	// PromotionStrategyRef is a reference to the promotion strategy that this scheduled commit status applies to.
	// +required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef"`

	// Key is the gate name referenced in the PromotionStrategy's proposedCommitStatuses.
	// Must be lowercase alphanumeric with hyphens, 1–63 characters (pattern: ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$).
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
	Key string `json:"key"`

	// Timezone is the default IANA timezone name used for evaluating cron expressions across all windows.
	// Individual windows may override this with their own timezone field.
	// Defaults to UTC.
	// +optional
	// +kubebuilder:default="UTC"
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=^[a-zA-Z0-9_/+-]*$
	Timezone string `json:"timezone,omitempty"`

	// Allow defines global allow windows applied to all listed environments in addition to
	// per-environment allow windows (OR semantics across all).
	// +optional
	// +kubebuilder:validation:MaxItems=20
	Allow []CronWindow `json:"allow,omitempty"`

	// Exclude defines global exclusion windows applied to all listed environments in addition to
	// per-environment exclusions. Exclusions take precedence over allow windows.
	// +optional
	// +kubebuilder:validation:MaxItems=20
	Exclude []CronWindow `json:"exclude,omitempty"`

	// Environments defines the list of environments to gate. Only listed environments are gated;
	// unlisted environments default to success (24/7 open). Each environment inherits global
	// allow/exclude windows and may add its own. An environment with no per-environment windows
	// is valid when global windows are defined.
	// +required
	// +listType=map
	// +listMapKey=branch
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1000
	Environments []ScheduledEnvironment `json:"environments"`
}

// ScheduledEnvironment defines the window configuration for a single environment.
type ScheduledEnvironment struct {
	// Branch is the name of the branch/environment you want to gate with time windows.
	// Must not start with '-', contain ':', or contain '..'.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('-')",message="branch must not start with '-'"
	// +kubebuilder:validation:XValidation:rule="!self.contains(':')",message="branch must not contain ':'"
	// +kubebuilder:validation:XValidation:rule="!self.contains('..')",message="branch must not contain '..'"
	Branch string `json:"branch"`

	// Allow defines the allowed promotion windows. Promotions are allowed when the current time
	// falls inside any of these windows (OR semantics). These are combined with any global
	// spec.allow windows. If no allow windows are defined (neither here nor globally), the
	// environment operates in exclusion-only mode.
	// +optional
	// +kubebuilder:validation:MaxItems=20
	Allow []CronWindow `json:"allow,omitempty"`

	// Exclude defines blackout periods during which promotions are blocked. Exclusions take
	// precedence over allow windows — if the current time falls inside any exclusion, promotions
	// are blocked regardless of allow windows. These are combined with any global spec.exclude
	// windows. If empty, no blackout periods are applied.
	// +optional
	// +kubebuilder:validation:MaxItems=20
	Exclude []CronWindow `json:"exclude,omitempty"`
}

// CronWindow defines a recurring time window using a cron expression and a duration.
type CronWindow struct {
	// Description is an optional human-readable explanation of this window
	// (e.g. "US East business hours", "Holiday deployment freeze").
	// +optional
	// +kubebuilder:validation:MaxLength=256
	Description string `json:"description,omitempty"`

	// Cron is a standard 5-field cron expression defining the start of the window
	// (minute hour day-of-month month day-of-week). For example "0 9 * * 1-5" means
	// Monday–Friday at 09:00 in the configured timezone.
	// +required
	// +kubebuilder:validation:MinLength=9
	// +kubebuilder:validation:MaxLength=256
	Cron string `json:"cron"`

	// Duration is how long the window remains open after the cron trigger.
	// The duration should be in a format accepted by Go's time.ParseDuration function, e.g., "8h", "30m", "2h30m".
	// +required
	Duration metav1.Duration `json:"duration"`

	// Timezone overrides the spec-level default timezone for this specific window.
	// If not set, the spec-level timezone (or UTC if that is also not set) is used.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=^[a-zA-Z0-9_/+-]*$
	Timezone string `json:"timezone,omitempty"`
}

// ScheduledCommitStatusStatus defines the observed state of ScheduledCommitStatus.
type ScheduledCommitStatusStatus struct {
	// ObservedGeneration is the .metadata.generation that this status was reconciled from.
	// Because status is written via Server-Side Apply with ForceOwnership (which has no
	// optimistic-concurrency check), this field is the canonical way to detect stale
	// status writes: compare status.observedGeneration with metadata.generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Environments holds the status of each environment being tracked.
	// +listType=map
	// +listMapKey=branch
	// +optional
	Environments []ScheduledEnvironmentStatus `json:"environments,omitempty"`

	// Conditions represent the latest available observations of an object's state
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

// ScheduledEnvironmentStatus defines the observed window state for a specific environment.
type ScheduledEnvironmentStatus struct {
	// Branch is the name of the branch/environment.
	// +required
	// +kubebuilder:validation:MinLength=1
	Branch string `json:"branch"`

	// Sha is the proposed commit SHA being tracked for this environment.
	// Supports both SHA-1 (40 chars) and SHA-256 (64 chars) Git hash formats.
	// +required
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	Sha string `json:"sha"`

	// Phase represents the current phase of the scheduled gate.
	// +kubebuilder:validation:Enum=pending;success
	// +required
	Phase string `json:"phase"`

	// Active holds details of the currently active window driving the phase.
	// Nil when no specific window is active (e.g. exclusion-only mode with no active exclusion).
	// +optional
	Active *WindowStatus `json:"active,omitempty"`

	// Next holds details of the next expected window transition.
	// Used by UIs for countdown timers and by the controller for precise requeuing.
	// +optional
	Next *WindowStatus `json:"next,omitempty"`
}

// WindowStatus holds details about a specific window state (active or upcoming).
type WindowStatus struct {
	// Allow is the cron expression of the allow window, if applicable.
	// +optional
	Allow string `json:"allow,omitempty"`

	// Exclude is the cron expression of the exclusion window, if applicable.
	// +optional
	Exclude string `json:"exclude,omitempty"`

	// Transition is when this window state is expected to change
	// (e.g. window closing for active, window opening for next).
	// +optional
	Transition *metav1.Time `json:"transition,omitempty"`
}

// +kubebuilder:ac:generate=true
// +kubebuilder:externalDocs:url="https://gitops-promoter.readthedocs.io/en/stable/crd-specs/#scheduledcommitstatus",description="CRD reference (examples and behavior)"
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ScheduledCommitStatus is the Schema for the scheduledcommitstatuses API.
// +kubebuilder:printcolumn:name="PromotionStrategy",type=string,JSONPath=`.spec.promotionStrategyRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type ScheduledCommitStatus struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of ScheduledCommitStatus
	// +required
	Spec ScheduledCommitStatusSpec `json:"spec"`

	// status defines the observed state of ScheduledCommitStatus
	// +optional
	Status ScheduledCommitStatusStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ScheduledCommitStatusList contains a list of ScheduledCommitStatus.
type ScheduledCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ScheduledCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the ScheduledCommitStatus.
func (scs *ScheduledCommitStatus) GetConditions() *[]metav1.Condition {
	return &scs.Status.Conditions
}

// SetObservedGeneration records the object generation that produced the current status.
func (scs *ScheduledCommitStatus) SetObservedGeneration(generation int64) {
	scs.Status.ObservedGeneration = generation
}

// SetStatusInstanceID records the instance-id label mirrored into status on each reconcile attempt.
func (scs *ScheduledCommitStatus) SetStatusInstanceID(v *string) {
	scs.Status.InstanceID = v
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ScheduledCommitStatus{}, &ScheduledCommitStatusList{})
		return nil
	})
}
