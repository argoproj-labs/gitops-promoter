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
)

// RequiredCheckCommitStatusSpec defines the desired state of RequiredCheckCommitStatus
type RequiredCheckCommitStatusSpec struct {
	// PromotionStrategyRef references the PromotionStrategy that owns this controller
	// +required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef"`
}

// RequiredCheckCommitStatusStatus defines the observed state of RequiredCheckCommitStatus
type RequiredCheckCommitStatusStatus struct {
	// Environments contains status for each environment's required checks
	// +listType=map
	// +listMapKey=branch
	// +optional
	Environments []RequiredCheckEnvironmentStatus `json:"environments,omitempty"`

	// Conditions represent the latest available observations of the resource's state
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// RequiredCheckEnvironmentStatus defines the observed required check status for a specific environment.
type RequiredCheckEnvironmentStatus struct {
	// Branch is the target branch being monitored
	// +required
	Branch string `json:"branch"`

	// Sha is the commit SHA being checked
	// +required
	Sha string `json:"sha"`

	// RequiredChecks lists all required checks discovered from SCM protection rules.
	// Each check has a unique Key field that serves as the identifier.
	// +listType=map
	// +listMapKey=key
	// +optional
	RequiredChecks []RequiredCheckStatus `json:"requiredChecks,omitempty"`

	// Phase is the aggregated phase of all required checks
	// +kubebuilder:validation:Enum=pending;success;failure
	// +required
	Phase CommitStatusPhase `json:"phase"`
}

// RequiredCheckStatus defines the status of a single required check.
type RequiredCheckStatus struct {
	// Name is the raw check identifier from the SCM (e.g., "smoke", "lint", "e2e-test")
	// +required
	Name string `json:"name"`

	// Key is the computed label key in the format "{provider}-{name}" or "{provider}-{name}-{appID}".
	// This is the value used as the CommitStatus label and selector key.
	// Examples: "github-smoke", "github-smoke-15368", "github-lint"
	// +required
	Key string `json:"key"`

	// Phase is the current phase of this check
	// +kubebuilder:validation:Enum=pending;success;failure
	// +required
	Phase CommitStatusPhase `json:"phase"`

	// LastPolledAt is the timestamp when this check was last queried from the SCM provider.
	// Used to optimize polling - terminal checks (success/failure) are polled less frequently.
	// +optional
	LastPolledAt *metav1.Time `json:"lastPolledAt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// RequiredCheckCommitStatus is the Schema for the requiredcheckcommitstatuses API
// +kubebuilder:printcolumn:name="PromotionStrategy",type=string,JSONPath=`.spec.promotionStrategyRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type RequiredCheckCommitStatus struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of RequiredCheckCommitStatus
	// +required
	Spec RequiredCheckCommitStatusSpec `json:"spec"`

	// status defines the observed state of RequiredCheckCommitStatus
	// +optional
	Status RequiredCheckCommitStatusStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RequiredCheckCommitStatusList contains a list of RequiredCheckCommitStatus
type RequiredCheckCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RequiredCheckCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the RequiredCheckCommitStatus.
func (rccs *RequiredCheckCommitStatus) GetConditions() *[]metav1.Condition {
	return &rccs.Status.Conditions
}

func init() {
	SchemeBuilder.Register(&RequiredCheckCommitStatus{}, &RequiredCheckCommitStatusList{})
}
