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
	RepositoryReference *Repository `json:"repository"`

	// +kubebuilder:validation:Required
	Sha string `json:"sha"`

	// +kubebuilder:validation:Required
	Name string `json:"name"`

	Description string `json:"description"`

	// +kubebuilder:validation:Required
	// +kubebuilder:default:=pending
	// +kubebuilder:validation:Enum:=pending;success;failure
	Phase CommitStatusPhase `json:"phase"` // pending, success, failure
	// (Github: error, failure, pending, success)
	// (Gitlab: pending, running, success, failed, canceled)
	// (Bitbucket: INPROGRESS, STOPPED, SUCCESSFUL, FAILED)

	Url string `json:"url"`
}

// CommitStatusStatus defines the observed state of CommitStatus
type CommitStatusStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	ObservedGeneration int64  `json:"observedGeneration"`
	Id                 string `json:"id"`
	Sha                string `json:"sha"`
	// +kubebuilder:validation:Enum:=pending;success;failure
	Phase CommitStatusPhase `json:"phase"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// +kubebuilder:printcolumn:name="Sha",type=string,JSONPath=`.status.sha`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// CommitStatus is the Schema for the commitstatuses API
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

type CommitStatusPhase string

const (
	CommitPhaseFailure CommitStatusPhase = "failure"
	CommitPhaseSuccess CommitStatusPhase = "success"
	CommitPhasePending CommitStatusPhase = "pending"
)
