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

// CommitStatusSpec defines the desired state of CommitStatus
type CommitStatusSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:Required
	RepositoryReference ObjectReference `json:"gitRepositoryRef"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^[a-fA-F0-9]+$`
	Sha string `json:"sha"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	Description string `json:"description"`

	// +kubebuilder:validation:Required
	// +kubebuilder:default:=pending
	// +kubebuilder:validation:Enum:=pending;success;failure
	Phase CommitStatusPhase `json:"phase"` // pending, success, failure
	// (Github: error, failure, pending, success)
	// (Gitlab: pending, running, success, failed, canceled)
	// (Bitbucket Cloud: INPROGRESS, STOPPED, SUCCESSFUL, FAILED)

	// Url is a URL that the user can follow to see more details about the status
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="self == '' || isURL(self)",message="must be a valid URL"
	// +kubebuilder:validation:Pattern="^(https?://.*)?$"
	Url string `json:"url,omitempty"`
}

// CommitStatusStatus defines the observed state of CommitStatus
type CommitStatusStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// Id is the unique identifier of the commit status, set by the SCM
	Id  string `json:"id"`
	Sha string `json:"sha"`
	// +kubebuilder:default:=pending
	// +kubebuilder:validation:Enum:=pending;success;failure;""
	// +kubebuilder:validation:Optional
	Phase CommitStatusPhase `json:"phase,omitempty"`

	// Conditions Represents the observations of the current state.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// GetConditions returns the conditions of the CommitStatus
func (cs *CommitStatus) GetConditions() *[]metav1.Condition {
	return &cs.Status.Conditions
}

// +kubebuilder:ac:generate=true
//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// CommitStatus is the Schema for the commitstatuses API
// +kubebuilder:printcolumn:name="Key",type=string,JSONPath=`.metadata.labels['promoter\.argoproj\.io/commit-status']`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Sha",type=string,JSONPath=`.status.sha`
// +kubebuilder:printcolumn:name="Name",type=string,JSONPath=`.spec.name`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`,priority=1
type CommitStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CommitStatusSpec   `json:"spec,omitempty"`
	Status CommitStatusStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// CommitStatusList contains a list of CommitStatus
type CommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CommitStatus `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CommitStatus{}, &CommitStatusList{})
}

// CommitStatusPhase represents the phase of a commit status.
type CommitStatusPhase string

const (
	// CommitPhaseFailure indicates that the commit status has failed.
	CommitPhaseFailure CommitStatusPhase = "failure"
	// CommitPhaseSuccess indicates that the commit status has been successfully completed.
	CommitPhaseSuccess CommitStatusPhase = "success"
	// CommitPhasePending indicates that the commit status is still being processed or has not yet been set.
	CommitPhasePending CommitStatusPhase = "pending"
)
