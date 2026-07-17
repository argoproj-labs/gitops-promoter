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

// PreviousEnvironmentCommitStatusSpec defines the desired state of PreviousEnvironmentCommitStatus.
type PreviousEnvironmentCommitStatusSpec struct {
	// PromotionStrategyRef is a reference to the promotion strategy that this previous environment
	// commit status applies to. The controller watches this PromotionStrategy and, for each
	// environment, reports whether the preceding environment is synced and healthy.
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
}

// PreviousEnvironmentCommitStatusStatus defines the observed state of PreviousEnvironmentCommitStatus.
type PreviousEnvironmentCommitStatusStatus struct {
	// ObservedGeneration is the .metadata.generation that this status was reconciled from.
	// Because status is written via Server-Side Apply with ForceOwnership (which has no
	// optimistic-concurrency check), this field is the canonical way to detect stale
	// status writes: compare status.observedGeneration with metadata.generation.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// conditions represent the current state of the PreviousEnvironmentCommitStatus resource.
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
// +kubebuilder:externalDocs:url="https://gitops-promoter.readthedocs.io/en/stable/crd-specs/#previousenvironmentcommitstatus",description="CRD reference (examples and behavior)"
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// PreviousEnvironmentCommitStatus is the Schema for the previousenvironmentcommitstatuses API
type PreviousEnvironmentCommitStatus struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of PreviousEnvironmentCommitStatus
	// +required
	Spec PreviousEnvironmentCommitStatusSpec `json:"spec"`

	// status defines the observed state of PreviousEnvironmentCommitStatus
	// +optional
	Status PreviousEnvironmentCommitStatusStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// PreviousEnvironmentCommitStatusList contains a list of PreviousEnvironmentCommitStatus
type PreviousEnvironmentCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []PreviousEnvironmentCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the PreviousEnvironmentCommitStatus.
func (p *PreviousEnvironmentCommitStatus) GetConditions() *[]metav1.Condition {
	return &p.Status.Conditions
}

// SetObservedGeneration records the object generation that produced the current status.
func (p *PreviousEnvironmentCommitStatus) SetObservedGeneration(generation int64) {
	p.Status.ObservedGeneration = generation
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &PreviousEnvironmentCommitStatus{}, &PreviousEnvironmentCommitStatusList{})
		return nil
	})
}
