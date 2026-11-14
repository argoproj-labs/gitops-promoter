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

// DependentsQualifiedObservedDryShaField is the field index over the dry SHAs a
// DependentsQualifiedCommitStatus last observed (see ObservedDryShas). The webhook receiver uses it to find
// the gates to wake when the dry branch or an environment tag is pushed.
const DependentsQualifiedObservedDryShaField = ".status.observedDryShas"

// TagAdvanceMode says who moves an environment's tag.
// +kubebuilder:validation:Enum=Auto;Manual
type TagAdvanceMode string

const (
	// TagAdvanceAuto has the controller move the tag: to the dry branch HEAD for an environment with no
	// upstreams, and to the newest commit every upstream has qualified for any other environment.
	TagAdvanceAuto TagAdvanceMode = "Auto"
	// TagAdvanceManual leaves the tag to a person (or their CI). The controller creates the tag if it is
	// missing and never moves it afterwards.
	TagAdvanceManual TagAdvanceMode = "Manual"
)

// DependentsQualifiedCommitStatusSpec defines the desired state of DependentsQualifiedCommitStatus.
type DependentsQualifiedCommitStatusSpec struct {
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
	DrySource DependentsQualifiedDrySource `json:"drySource"`

	// Environments declares the tag each environment hydrates from. There must be exactly one entry for
	// every environment of the referenced PromotionStrategy, and every entry must name its own tag.
	// +required
	// +listType=map
	// +listMapKey=branch
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:XValidation:rule="self.all(e, self.exists_one(o, o.tag == e.tag))",message="each environment must have its own tag"
	Environments []DependentsQualifiedEnvironment `json:"environments"`

	// URL generates the URL to use on the per-environment CommitStatus (SCM details link). Optional; when
	// empty, no URL is set on the child CommitStatus. The template receives .Environment,
	// .DependentsQualifiedCommitStatus, .PromotionStrategy, .DependsOn, and .DependsOnQuery.
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

// DependentsQualifiedDrySource identifies the dry branch whose commits are promoted.
type DependentsQualifiedDrySource struct {
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

// DependentsQualifiedEnvironment declares the tag one environment hydrates from.
type DependentsQualifiedEnvironment struct {
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

	// Tag is the name of the tag in the dry repository that this environment's hydrator reads (without the
	// refs/tags/ prefix). The controller creates the tag if it does not exist.
	// Must not start with '-', contain ':', or contain '..'.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('-')",message="tag must not start with '-'"
	// +kubebuilder:validation:XValidation:rule="!self.contains(':')",message="tag must not contain ':'"
	// +kubebuilder:validation:XValidation:rule="!self.contains('..')",message="tag must not contain '..'"
	Tag string `json:"tag"`

	// Advance says who moves the tag. Auto (the default): the controller. Manual: a person; the controller
	// still verifies that what they tagged is hydrated and qualified upstream before it may be promoted.
	// +optional
	// +kubebuilder:default=Auto
	Advance TagAdvanceMode `json:"advance,omitempty"`
}

// DependentsQualifiedDryStatus is the observed state of the dry branch.
type DependentsQualifiedDryStatus struct {
	// Sha is the HEAD of the dry branch when the controller last reconciled.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	Sha string `json:"sha,omitempty"`
}

// DependentsQualifiedTagStatus is the observed state of one environment tag.
type DependentsQualifiedTagStatus struct {
	// Name is the tag name.
	// +required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Sha is the dry commit the tag points at. Empty when the tag does not exist.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	Sha string `json:"sha,omitempty"`
}

// DependentsQualifiedWatermark is the newest dry commit an environment has qualified: a commit that was live
// in the environment while every active commit status was success. Every commit before it on the dry branch
// is qualified too.
type DependentsQualifiedWatermark struct {
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

// DependentsQualifiedCommitStatusUpstreamStatus reports whether a direct upstream has qualified the commit
// this environment's tag points at.
type DependentsQualifiedCommitStatusUpstreamStatus struct {
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

// DependentsQualifiedCommitStatusEnvironmentStatus defines observed state for one environment branch.
type DependentsQualifiedCommitStatusEnvironmentStatus struct {
	// Tag is the environment's tag as last read from the dry repository.
	// +optional
	Tag *DependentsQualifiedTagStatus `json:"tag,omitempty"`

	// Qualified is the newest dry commit this environment has qualified. Omitted until the environment has
	// qualified a commit.
	// +optional
	Qualified *DependentsQualifiedWatermark `json:"qualified,omitempty"`

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
	Upstreams []DependentsQualifiedCommitStatusUpstreamStatus `json:"upstreams,omitempty"`
}

// DependentsQualifiedCommitStatusStatus defines the observed state of DependentsQualifiedCommitStatus.
type DependentsQualifiedCommitStatusStatus struct {
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
	Dry *DependentsQualifiedDryStatus `json:"dry,omitempty"`

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
	Environments []DependentsQualifiedCommitStatusEnvironmentStatus `json:"environments,omitempty"`

	// ObservedGeneration is the .metadata.generation that this status was reconciled from.
	// Because status is written via Server-Side Apply with ForceOwnership (which has no
	// optimistic-concurrency check), this field is the canonical way to detect stale
	// status writes: compare status.observedGeneration with metadata.generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:ac:generate=true
// +kubebuilder:externalDocs:url="https://gitops-promoter.readthedocs.io/en/stable/crd-specs/#dependentsqualifiedcommitstatus",description="CRD reference (examples and behavior)"
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DependentsQualifiedCommitStatus is the Schema for the dependentsqualifiedcommitstatuses API
type DependentsQualifiedCommitStatus struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of DependentsQualifiedCommitStatus
	// +required
	Spec DependentsQualifiedCommitStatusSpec `json:"spec"`

	// status defines the observed state of DependentsQualifiedCommitStatus
	// +optional
	Status DependentsQualifiedCommitStatusStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DependentsQualifiedCommitStatusList contains a list of DependentsQualifiedCommitStatus
type DependentsQualifiedCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DependentsQualifiedCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the DependentsQualifiedCommitStatus.
func (d *DependentsQualifiedCommitStatus) GetConditions() *[]metav1.Condition {
	return &d.Status.Conditions
}

// SetObservedGeneration records the object generation that produced the current status.
func (d *DependentsQualifiedCommitStatus) SetObservedGeneration(generation int64) {
	d.Status.ObservedGeneration = generation
}

// SetStatusInstanceID records the instance-id label mirrored into status on each reconcile attempt.
func (d *DependentsQualifiedCommitStatus) SetStatusInstanceID(v *string) {
	d.Status.InstanceID = v
}

// ObservedDryShas returns the dry SHAs this gate last observed: the dry branch HEAD and every environment
// tag. A push webhook's "before" SHA is one of them when the push moved a ref this gate reads.
func (d *DependentsQualifiedCommitStatus) ObservedDryShas() []string {
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
		s.AddKnownTypes(SchemeGroupVersion, &DependentsQualifiedCommitStatus{}, &DependentsQualifiedCommitStatusList{})
		return nil
	})
}
