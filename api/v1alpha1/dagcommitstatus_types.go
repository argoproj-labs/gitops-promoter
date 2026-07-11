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

// DAGCommitStatusSpec defines the desired state of DAGCommitStatus.
type DAGCommitStatusSpec struct {
	// PromotionStrategyRef is a reference to the promotion strategy that this DAG commit status
	// applies to. The controller watches this PromotionStrategy and, for each environment, reports
	// whether the environment's upstream dependencies (as declared in Environments) are satisfied.
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
	// and the upstream branches it depends on. An environment becomes eligible for promotion once
	// all of its dependsOn upstreams are satisfied. An entry with no dependsOn is a graph root.
	// The graph must be acyclic; cycles and references to unknown branches are rejected.
	// +required
	// +kubebuilder:validation:MinItems:=1
	// +kubebuilder:validation:MaxItems:=1000
	// +listType:=map
	// +listMapKey=branch
	Environments []DAGEnvironment `json:"environments"`
}

// DAGEnvironment is a single node in the promotion dependency graph.
type DAGEnvironment struct {
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

	// DependsOn is the list of upstream branches this environment depends on. The environment is
	// only eligible for promotion once every branch listed here is satisfied. An empty or omitted
	// list makes this environment a root of the graph.
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

// DAGCommitStatusStatus defines the observed state of DAGCommitStatus.
type DAGCommitStatusStatus struct {
	// ObservedGeneration is the .metadata.generation that this status was reconciled from.
	// Because status is written via Server-Side Apply with ForceOwnership (which has no
	// optimistic-concurrency check), this field is the canonical way to detect stale
	// status writes: compare status.observedGeneration with metadata.generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the current state of the DAGCommitStatus resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:ac:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// DAGCommitStatus is the Schema for the dagcommitstatuses API
type DAGCommitStatus struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of DAGCommitStatus
	// +required
	Spec DAGCommitStatusSpec `json:"spec"`

	// status defines the observed state of DAGCommitStatus
	// +optional
	Status DAGCommitStatusStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DAGCommitStatusList contains a list of DAGCommitStatus
type DAGCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DAGCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the DAGCommitStatus.
func (d *DAGCommitStatus) GetConditions() *[]metav1.Condition {
	return &d.Status.Conditions
}

// SetObservedGeneration records the object generation that produced the current status.
func (d *DAGCommitStatus) SetObservedGeneration(generation int64) {
	d.Status.ObservedGeneration = generation
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DAGCommitStatus{}, &DAGCommitStatusList{})
		return nil
	})
}
