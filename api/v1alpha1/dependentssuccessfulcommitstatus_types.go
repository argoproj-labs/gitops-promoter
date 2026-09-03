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

// DependentsSuccessfulCommitStatusSpec defines the desired state of DependentsSuccessfulCommitStatus.
type DependentsSuccessfulCommitStatusSpec struct {
	// PromotionStrategyRef is a reference to the promotion strategy that this dependents successful commit status
	// applies to. The controller watches this PromotionStrategy and, for each environment, reports whether the
	// environment's dependent environments (as declared in Environments) are promoted and successful.
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

	// Environments declares which environments each branch depends on. An environment becomes eligible for
	// promotion once all of its dependsOn dependents are promoted and successful. An entry with no dependsOn
	// is a root. The graph must be acyclic; cycles and references to unknown branches are rejected.
	//
	// When omitted or empty, the controller infers a linear chain from the referenced
	// PromotionStrategy's spec.environments order: the first environment is a root, and each
	// subsequent environment dependsOn the one before it.
	// +optional
	// +kubebuilder:validation:MaxItems:=1000
	// +listType:=map
	// +listMapKey=branch
	Environments []DependentEnvironment `json:"environments"`

	// URL generates the URL to use on the per-environment CommitStatus (SCM details link), for
	// example a link into the Promoter UI that highlights this environment's dependsOn upstreams.
	// Optional; when empty, no URL is set on the child CommitStatus. The template receives
	// .Environment, .DependentsSuccessfulCommitStatus, .PromotionStrategy, .DependsOn, and .DependsOnQuery
	// (see controller docs).
	// +kubebuilder:validation:Optional
	URL URLConfig `json:"url,omitempty"`
}

// DependentEnvironment declares one environment branch and the other branches it depends on.
// +kubebuilder:validation:XValidation:rule="!has(self.dependsOn) || self.dependsOn.all(d, d != self.branch)",message="branch cannot depend on itself"
type DependentEnvironment struct {
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

	// DependsOn is the list of dependent environment branches this environment waits on. The environment is
	// only eligible for promotion once every branch listed here is promoted and successful. An empty or
	// omitted list makes this environment a root.
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

// DependentsSuccessfulCommitStatusStatus defines the observed state of DependentsSuccessfulCommitStatus.
type DependentsSuccessfulCommitStatusStatus struct {
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
}

// +kubebuilder:ac:generate=true
// +kubebuilder:externalDocs:url="https://gitops-promoter.readthedocs.io/en/stable/crd-specs/#dependentssuccessfulcommitstatus",description="CRD reference (examples and behavior)"
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DependentsSuccessfulCommitStatus is the Schema for the dependentssuccessfulcommitstatuses API
type DependentsSuccessfulCommitStatus struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of DependentsSuccessfulCommitStatus
	// +required
	Spec DependentsSuccessfulCommitStatusSpec `json:"spec"`

	// status defines the observed state of DependentsSuccessfulCommitStatus
	// +optional
	Status DependentsSuccessfulCommitStatusStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DependentsSuccessfulCommitStatusList contains a list of DependentsSuccessfulCommitStatus
type DependentsSuccessfulCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DependentsSuccessfulCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the DependentsSuccessfulCommitStatus.
func (d *DependentsSuccessfulCommitStatus) GetConditions() *[]metav1.Condition {
	return &d.Status.Conditions
}

// SetObservedGeneration records the object generation that produced the current status.
func (d *DependentsSuccessfulCommitStatus) SetObservedGeneration(generation int64) {
	d.Status.ObservedGeneration = generation
}

// SetStatusInstanceID records the instance-id label mirrored into status on each reconcile attempt.
func (d *DependentsSuccessfulCommitStatus) SetStatusInstanceID(v *string) {
	d.Status.InstanceID = v
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DependentsSuccessfulCommitStatus{}, &DependentsSuccessfulCommitStatusList{})
		return nil
	})
}
