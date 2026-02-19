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

// ScheduledCommitStatusSpec defines the desired state of ScheduledCommitStatus
type ScheduledCommitStatusSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	// The following markers will use OpenAPI v3 schema to validate the value
	// More info: https://book.kubebuilder.io/reference/markers/crd-validation.html

	// PromotionStrategyRef is a reference to the promotion strategy that this scheduled commit status applies to.
	// +required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef"`

	// Environments is a list of environments with their deployment schedules.
	// +required
	Environments []ScheduledCommitStatusEnvironment `json:"environments"`
}

// ScheduledCommitStatusEnvironment defines the branch/environment and its deployment schedule.
type ScheduledCommitStatusEnvironment struct {
	// Branch is the name of the branch/environment you want to gate with the configured schedule.
	// +required
	Branch string `json:"branch"`

	// Schedule defines when deployments are allowed for this environment.
	// +required
	Schedule Schedule `json:"schedule"`
}

// Schedule defines a deployment window using cron syntax.
type Schedule struct {
	// Cron is a cron expression defining when the deployment window starts.
	// For example, "0 9 * * 1-5" means 9 AM Monday through Friday.
	// Uses standard cron syntax: minute hour day-of-month month day-of-week
	// +required
	// +kubebuilder:validation:MinLength=1
	Cron string `json:"cron"`

	// Window is the duration of the deployment window after the cron schedule triggers.
	// The window should be in a format accepted by Go's time.ParseDuration function, e.g., "2h", "8h", "30m".
	// +required
	// +kubebuilder:validation:MinLength=1
	Window string `json:"window"`

	// Timezone is the IANA timezone name to use for the cron schedule.
	// For example, "America/New_York", "Europe/London", "Asia/Tokyo".
	// If not specified, defaults to UTC.
	// +optional
	// +kubebuilder:default="UTC"
	Timezone string `json:"timezone,omitempty"`
}

// ScheduledCommitStatusStatus defines the observed state of ScheduledCommitStatus.
type ScheduledCommitStatusStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Environments holds the status of each environment being tracked.
	// +listType=map
	// +listMapKey=branch
	// +optional
	Environments []ScheduledCommitStatusEnvironmentStatus `json:"environments,omitempty"`

	// Conditions represent the latest available observations of an object's state
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ScheduledCommitStatusEnvironmentStatus defines the observed schedule status for a specific environment.
type ScheduledCommitStatusEnvironmentStatus struct {
	// Branch is the name of the branch/environment.
	// +required
	Branch string `json:"branch"`

	// Sha is the proposed commit SHA being tracked for this environment.
	// +required
	Sha string `json:"sha"`

	// Phase represents the current phase of the scheduled gate.
	// +kubebuilder:validation:Enum=pending;success
	// +required
	Phase string `json:"phase"`

	// CurrentlyInWindow indicates whether the current time is within a deployment window.
	// +required
	CurrentlyInWindow bool `json:"currentlyInWindow"`

	// NextWindowStart is when the next deployment window starts.
	// +optional
	NextWindowStart *metav1.Time `json:"nextWindowStart,omitempty"`

	// NextWindowEnd is when the next deployment window ends.
	// +optional
	NextWindowEnd *metav1.Time `json:"nextWindowEnd,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ScheduledCommitStatus is the Schema for the scheduledcommitstatuses API
// +kubebuilder:printcolumn:name="Strategy",type=string,JSONPath=`.spec.promotionStrategyRef.name`
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

// ScheduledCommitStatusList contains a list of ScheduledCommitStatus
type ScheduledCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ScheduledCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the ScheduledCommitStatus.
func (scs *ScheduledCommitStatus) GetConditions() *[]metav1.Condition {
	return &scs.Status.Conditions
}

func init() {
	SchemeBuilder.Register(&ScheduledCommitStatus{}, &ScheduledCommitStatusList{})
}
