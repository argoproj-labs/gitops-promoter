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

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// TimedCommitStatusSpec defines the desired state of TimedCommitStatus
type TimedCommitStatusSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	// PromotionStrategyRef is a reference to the promotion strategy that this timed commit status applies to.
	// +required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef"`

	// +required
	Environments []TimedCommitStatusEnvironments `json:"environments"`
}

// TimedCommitStatusEnvironments defines the branch/environment and duration to wait before reporting the gate as success.
type TimedCommitStatusEnvironments struct {
	// Branch is the name of the branch/environment you want to gate for the configured duration.
	// +required
	Branch string `json:"branch"`
	// Duration is the time duration to wait before considering the commit status as success.
	// The duration should be in a format accepted by Go's time.ParseDuration function, e.g., "5m", "1h30m".
	// +required
	Duration metav1.Duration `json:"duration"`
}

// TimedCommitStatusStatus defines the observed state of TimedCommitStatus.
type TimedCommitStatusStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Environments holds the status of each environment being tracked.
	// +listType=map
	// +listMapKey=branch
	// +optional
	Environments []TimedCommitStatusEnvironmentsStatus `json:"environments,omitempty"`

	// Conditions represent the latest available observations of an object's state
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// TimedCommitStatusEnvironmentsStatus defines the observed timing status for a specific environment.
type TimedCommitStatusEnvironmentsStatus struct {
	// Branch is the name of the branch/environment.
	// +required
	Branch string `json:"branch"`

	// Sha is the commit SHA being tracked for this environment.
	// +required
	Sha string `json:"sha"`

	// CommitTime is when the commit was deployed to the active environment.
	// +required
	CommitTime metav1.Time `json:"commitTime"`

	// RequiredDuration is the duration that must elapse before promotion is allowed.
	// If this is updated in the spec with a pending time. The new duration will be calculated from the original CommitTime.
	// Allowing you to extend the duration of an already deployed commit, if the time has not yet elapsed.
	// +required
	RequiredDuration metav1.Duration `json:"requiredDuration"`

	// Phase represents the current phase of the timed gate.
	// +kubebuilder:validation:Enum=pending;success
	// +required
	Phase string `json:"phase"`

	// AtMostDurationRemaining is the maximum amount of time remaining until the gate is satisfied.
	// This is calculated at reconciliation time. When the gate is satisfied (phase=success), this will be 0.
	// +required
	AtMostDurationRemaining metav1.Duration `json:"atMostDurationRemaining"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// TimedCommitStatus is the Schema for the timedcommitstatuses API
// +kubebuilder:printcolumn:name="PromotionStrategy",type=string,JSONPath=`.spec.promotionStrategyRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type TimedCommitStatus struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of TimedCommitStatus
	// +required
	Spec TimedCommitStatusSpec `json:"spec"`

	// status defines the observed state of TimedCommitStatus
	// +optional
	Status TimedCommitStatusStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TimedCommitStatusList contains a list of TimedCommitStatus
type TimedCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TimedCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the TimedCommitStatus.
func (tcs *TimedCommitStatus) GetConditions() *[]metav1.Condition {
	return &tcs.Status.Conditions
}

func init() {
	SchemeBuilder.Register(&TimedCommitStatus{}, &TimedCommitStatusList{})
}
