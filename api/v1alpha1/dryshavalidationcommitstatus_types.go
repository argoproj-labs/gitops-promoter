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

// DryShaValidationCommitStatusSpec defines the desired state of DryShaValidationCommitStatus.
type DryShaValidationCommitStatusSpec struct {
	// PromotionStrategyRef is a reference to the PromotionStrategy this gate applies to. The
	// controller watches it and, for each environment, reports whether the dry commit being
	// promoted has already been validated in a lower environment.
	// +required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef"`

	// Key is the commit status key referenced in the PromotionStrategy's proposedCommitStatuses.
	// It must match a key declared there so the gate this controller produces is enforced.
	// Must be lowercase alphanumeric with hyphens, 1–63 characters (pattern: ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$).
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
	Key string `json:"key"`

	// Environments declares the promotion dependency graph. Each entry names an environment branch
	// and the lower branches it depends on; a dry commit validated in any of those (transitively)
	// satisfies this environment. An entry with no dependsOn is a graph root and always passes.
	// When omitted, a linear chain is derived from the referenced PromotionStrategy's
	// spec.environments order (each environment depends on the one before it).
	// The graph must be acyclic; cycles and references to unknown branches are rejected.
	// +optional
	// +kubebuilder:validation:MaxItems:=1000
	// +listType:=map
	// +listMapKey=branch
	Environments []DryShaValidationEnvironment `json:"environments,omitempty"`

	// LookbackLimit is how many first-parent commits to scan on each upstream environment's active
	// branch when looking for the dry commit. A dry commit promoted longer ago than this many
	// promotions in every upstream reads as unvalidated, and the gate stays pending.
	// +optional
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	LookbackLimit *int32 `json:"lookbackLimit,omitempty"`

	// URL generates the URL to use on the per-environment CommitStatus (SCM details link), for
	// example a link into the Promoter UI that highlights this environment's upstreams.
	// Optional; when empty, no URL is set on the child CommitStatus. The template receives
	// .Environment, .DryShaValidationCommitStatus, .PromotionStrategy, .DependsOn and
	// .DependsOnQuery (see controller docs).
	// +kubebuilder:validation:Optional
	URL URLConfig `json:"url,omitempty"`
}

// DryShaValidationEnvironment is a single node in the promotion dependency graph.
// +kubebuilder:validation:XValidation:rule="!has(self.dependsOn) || self.dependsOn.all(d, d != self.branch)",message="branch cannot depend on itself"
type DryShaValidationEnvironment struct {
	// Branch is the name of the active branch for the environment. It must match a branch declared
	// in the referenced PromotionStrategy's environments.
	// Must not start with '-', contain ':', or contain '..'.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('-')",message="branch must not start with '-'"
	// +kubebuilder:validation:XValidation:rule="!self.contains(':')",message="branch must not contain ':'"
	// +kubebuilder:validation:XValidation:rule="!self.contains('..')",message="branch must not contain '..'"
	Branch string `json:"branch"`

	// DependsOn is the list of lower branches this environment promotes after. The environment is
	// eligible once its target dry commit has been validated in any of them, transitively.
	// An empty or omitted list makes this environment a root of the graph.
	// Each item must not start with '-', contain ':', or contain '..'.
	// +optional
	// +listType:=set
	// +kubebuilder:validation:MaxItems=100
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=100
	// +kubebuilder:validation:items:XValidation:rule="!self.startsWith('-')",message="branch must not start with '-'"
	// +kubebuilder:validation:items:XValidation:rule="!self.contains(':')",message="branch must not contain ':'"
	// +kubebuilder:validation:items:XValidation:rule="!self.contains('..')",message="branch must not contain '..'"
	DependsOn []string `json:"dependsOn,omitempty"`
}

// DryShaValidationCommitStatusStatus defines the observed state of DryShaValidationCommitStatus.
type DryShaValidationCommitStatusStatus struct {
	// ObservedGeneration is the .metadata.generation that this status was reconciled from.
	// Because status is written via Server-Side Apply with ForceOwnership (which has no
	// optimistic-concurrency check), this field is the canonical way to detect stale
	// status writes: compare status.observedGeneration with metadata.generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Environments reports, per environment, the outcome of the most recent evaluation. The gate
	// itself is enforced through the generated per-environment CommitStatus resources; this is the
	// at-a-glance record of why each one is in the phase it is, because the evidence behind the
	// decision lives in git history rather than in any API object.
	// +optional
	// +listType=map
	// +listMapKey=branch
	Environments []DryShaValidationEnvironmentStatus `json:"environments,omitempty"`

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

// DryShaValidationEnvironmentStatus is the most recent gate evaluation for one environment.
type DryShaValidationEnvironmentStatus struct {
	// Branch is the environment's active branch.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	Branch string `json:"branch"`

	// TargetDrySha is the dry commit this environment is currently promoting, resolved from the
	// hydrator note and falling back to the proposed dry SHA. Empty when nothing is in flight.
	// Supports both SHA-1 (40 chars) and SHA-256 (64 chars) Git hash formats.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	TargetDrySha string `json:"targetDrySha,omitempty"`

	// Phase mirrors the phase written to this environment's generated CommitStatus.
	// +optional
	// +kubebuilder:validation:Enum:=pending;success;failure
	Phase CommitStatusPhase `json:"phase,omitempty"`

	// ValidatedIn is the lower environment branch whose history proved TargetDrySha was active and
	// healthy there. Empty while the gate is pending.
	// +optional
	// +kubebuilder:validation:MaxLength=100
	ValidatedIn string `json:"validatedIn,omitempty"`

	// ValidatedAt is the commit time of the merge commit that made TargetDrySha active in
	// ValidatedIn. Empty while the gate is pending.
	// +optional
	ValidatedAt *metav1.Time `json:"validatedAt,omitempty"`

	// CommitsScanned is how many first-parent commits were walked in the upstream environments on
	// the last evaluation, bounded by spec.lookbackLimit. It tells "not validated yet" apart from
	// "validated, but it aged out of the lookback window".
	// +optional
	CommitsScanned int32 `json:"commitsScanned,omitempty"`

	// LastEvaluationTime is when this environment was last evaluated.
	// +optional
	LastEvaluationTime metav1.Time `json:"lastEvaluationTime,omitempty"`
}

// +kubebuilder:ac:generate=true
// +kubebuilder:externalDocs:url="https://gitops-promoter.readthedocs.io/en/stable/crd-specs/#dryshavalidationcommitstatus",description="CRD reference (examples and behavior)"
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Key",type=string,JSONPath=`.spec.key`
// +kubebuilder:printcolumn:name="Strategy",type=string,JSONPath=`.spec.promotionStrategyRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DryShaValidationCommitStatus is the Schema for the dryshavalidationcommitstatuses API
type DryShaValidationCommitStatus struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of DryShaValidationCommitStatus
	// +required
	Spec DryShaValidationCommitStatusSpec `json:"spec"`

	// status defines the observed state of DryShaValidationCommitStatus
	// +optional
	Status DryShaValidationCommitStatusStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DryShaValidationCommitStatusList contains a list of DryShaValidationCommitStatus
type DryShaValidationCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DryShaValidationCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the DryShaValidationCommitStatus.
func (d *DryShaValidationCommitStatus) GetConditions() *[]metav1.Condition {
	return &d.Status.Conditions
}

// SetObservedGeneration records the object generation that produced the current status.
func (d *DryShaValidationCommitStatus) SetObservedGeneration(generation int64) {
	d.Status.ObservedGeneration = generation
}

// SetStatusInstanceID records the instance-id label mirrored into status on each reconcile attempt.
func (d *DryShaValidationCommitStatus) SetStatusInstanceID(v *string) {
	d.Status.InstanceID = v
}

// GetLookbackLimit returns the configured lookback limit, or the default when unset.
func (d *DryShaValidationCommitStatus) GetLookbackLimit() int {
	if d.Spec.LookbackLimit == nil {
		return DryShaValidationDefaultLookbackLimit
	}
	return int(*d.Spec.LookbackLimit)
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DryShaValidationCommitStatus{}, &DryShaValidationCommitStatusList{})
		return nil
	})
}
