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

// TagOrderedReleasePolicy controls when a newer DRY commit may be released to an environment
// that is still working on the commit it already has.
// +kubebuilder:validation:Enum=WhenSettled;Immediately
type TagOrderedReleasePolicy string

const (
	// TagOrderedReleasePolicyWhenSettled holds a newer commit back while the environment is still verifying
	// the commit it has (merged, with active commit statuses pending and not yet proven). The newest eligible
	// commit is released once the environment settles, whether it ends up healthy or failing.
	TagOrderedReleasePolicyWhenSettled TagOrderedReleasePolicy = "WhenSettled"

	// TagOrderedReleasePolicyImmediately releases a newer commit as soon as it is eligible, replacing
	// whatever the environment is working on.
	TagOrderedReleasePolicyImmediately TagOrderedReleasePolicy = "Immediately"
)

// TagOrderedEnvironmentState describes where an environment is with the DRY commit its tag points at.
// +kubebuilder:validation:Enum=Unreleased;Hydrating;Promoting;Verifying;Healthy;Failed
type TagOrderedEnvironmentState string

const (
	// TagOrderedEnvironmentStateUnreleased means the environment's tag does not exist yet.
	TagOrderedEnvironmentStateUnreleased TagOrderedEnvironmentState = "Unreleased"
	// TagOrderedEnvironmentStateHydrating means the tag points at a commit the hydrator has not processed yet.
	TagOrderedEnvironmentStateHydrating TagOrderedEnvironmentState = "Hydrating"
	// TagOrderedEnvironmentStatePromoting means the released commit is hydrated and proposed, but not merged.
	TagOrderedEnvironmentStatePromoting TagOrderedEnvironmentState = "Promoting"
	// TagOrderedEnvironmentStateVerifying means the released commit is live and its active commit statuses are pending.
	TagOrderedEnvironmentStateVerifying TagOrderedEnvironmentState = "Verifying"
	// TagOrderedEnvironmentStateHealthy means the released commit is live and every active commit status is passing.
	TagOrderedEnvironmentStateHealthy TagOrderedEnvironmentState = "Healthy"
	// TagOrderedEnvironmentStateFailed means the released commit is live and an active commit status is failing.
	TagOrderedEnvironmentStateFailed TagOrderedEnvironmentState = "Failed"
)

// TagOrderedCommitStatusSpec defines the desired state of TagOrderedCommitStatus.
type TagOrderedCommitStatusSpec struct {
	// PromotionStrategyRef is a reference to the promotion strategy that this gate orders. The dependency graph
	// comes from the PromotionStrategy's environments (dependsOn, or a linear chain inferred from list order).
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

	// DryBranch is the branch that holds the DRY commits, for example main. The controller watches the tip of
	// this branch and releases it to the root environments. Only commits reachable from this branch are released.
	// Must not start with '-', contain ':', or contain '..'.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('-')",message="dryBranch must not start with '-'"
	// +kubebuilder:validation:XValidation:rule="!self.contains(':')",message="dryBranch must not contain ':'"
	// +kubebuilder:validation:XValidation:rule="!self.contains('..')",message="dryBranch must not contain '..'"
	DryBranch string `json:"dryBranch"`

	// TagTemplate is a Go template that renders the name of each environment's tag. The hydrator for an
	// environment must use that tag as its DRY revision instead of the DRY branch. The template receives
	// .Environment (the environment's branch name) and must render a distinct tag for every environment.
	// +optional
	// +kubebuilder:default="promoter/{{ .Environment }}"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	TagTemplate string `json:"tagTemplate,omitempty"`

	// ReleasePolicy controls when a newer commit may be released to an environment that is still working on the
	// commit it already has.
	// - "WhenSettled" (default): a newer commit waits while the environment is verifying the commit it has
	//   (merged, active commit statuses pending). It is released once the environment is healthy or failing.
	// - "Immediately": a newer commit is released as soon as it is eligible.
	// +optional
	// +kubebuilder:default=WhenSettled
	ReleasePolicy TagOrderedReleasePolicy `json:"releasePolicy,omitempty"`

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

// TagOrderedProvenSha records a DRY commit that an environment ran with every active commit status passing.
type TagOrderedProvenSha struct {
	// Sha is the DRY commit SHA.
	// Supports both SHA-1 (40 chars) and SHA-256 (64 chars) Git hash formats.
	// +required
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})$`
	Sha string `json:"sha"`

	// Time is when the controller first observed the environment healthy on this commit.
	// +required
	Time metav1.Time `json:"time"`
}

// TagOrderedCommitStatusEnvironmentStatus defines observed state for one environment branch.
type TagOrderedCommitStatusEnvironmentStatus struct {
	// Branch is the environment branch name.
	// +required
	// +kubebuilder:validation:MinLength=1
	Branch string `json:"branch"`

	// Gate report fields (phase, description, url, reportedSha) mirror the child CommitStatus spec.
	// When there is no in-flight proposed change, the controller copies the last child CommitStatus
	// report instead of re-evaluating the gate.
	GateEnvironmentCommitStatus `json:",inline"`

	// Tag is the name of the tag that selects this environment's DRY commit.
	// +optional
	Tag string `json:"tag,omitempty"`

	// ReleasedSha is the DRY commit the tag points at. Empty until a commit is released to the environment.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	ReleasedSha string `json:"releasedSha,omitempty"`

	// CandidateSha is the newest DRY commit eligible for this environment: the tip of the DRY branch for a
	// root environment, otherwise the newest commit proven by every upstream. It differs from releasedSha
	// while a release is being held back. Empty when no commit is eligible yet.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	CandidateSha string `json:"candidateSha,omitempty"`

	// State describes where the environment is with the released commit.
	// +optional
	State TagOrderedEnvironmentState `json:"state,omitempty"`

	// Message explains the state, including why a candidate commit has not been released.
	// +optional
	Message string `json:"message,omitempty"`

	// ProvenShas lists the DRY commits this environment ran with every active commit status passing, newest
	// first. Downstream environments only receive commits that all of their upstreams have proven.
	// +optional
	// +kubebuilder:validation:MaxItems=10
	// +listType=atomic
	ProvenShas []TagOrderedProvenSha `json:"provenShas,omitempty"`
}

// TagOrderedCommitStatusStatus defines the observed state of TagOrderedCommitStatus.
type TagOrderedCommitStatusStatus struct {
	// ObservedGeneration is the .metadata.generation that this status was reconciled from.
	// Because status is written via Server-Side Apply with ForceOwnership (which has no
	// optimistic-concurrency check), this field is the canonical way to detect stale
	// status writes: compare status.observedGeneration with metadata.generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

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

	// DryBranchSha is the tip of spec.dryBranch that the controller last observed.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	DryBranchSha string `json:"dryBranchSha,omitempty"`

	// Environments reports the tag, release, and gate state per environment branch.
	// +optional
	// +kubebuilder:validation:MaxItems=500
	// +listType=map
	// +listMapKey=branch
	Environments []TagOrderedCommitStatusEnvironmentStatus `json:"environments,omitempty"`
}

// +kubebuilder:ac:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Dry Branch",type=string,JSONPath=`.spec.dryBranch`

// TagOrderedCommitStatus is the Schema for the tagorderedcommitstatuses API.
//
// It orders promotions by controlling which DRY commit each environment hydrates. Every environment has a
// tag; the hydrator for that environment uses the tag as its DRY revision. The controller moves a root
// environment's tag to the tip of the DRY branch, and moves a downstream environment's tag to a commit only
// after every upstream environment has run that commit with passing active commit statuses.
type TagOrderedCommitStatus struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of TagOrderedCommitStatus
	// +required
	Spec TagOrderedCommitStatusSpec `json:"spec"`

	// status defines the observed state of TagOrderedCommitStatus
	// +optional
	Status TagOrderedCommitStatusStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TagOrderedCommitStatusList contains a list of TagOrderedCommitStatus
type TagOrderedCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TagOrderedCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the TagOrderedCommitStatus.
func (t *TagOrderedCommitStatus) GetConditions() *[]metav1.Condition {
	return &t.Status.Conditions
}

// SetObservedGeneration records the object generation that produced the current status.
func (t *TagOrderedCommitStatus) SetObservedGeneration(generation int64) {
	t.Status.ObservedGeneration = generation
}

// SetStatusInstanceID records the instance-id label mirrored into status on each reconcile attempt.
func (t *TagOrderedCommitStatus) SetStatusInstanceID(v *string) {
	t.Status.InstanceID = v
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &TagOrderedCommitStatus{}, &TagOrderedCommitStatusList{})
		return nil
	})
}
