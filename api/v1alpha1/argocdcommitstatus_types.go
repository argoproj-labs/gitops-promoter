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

// ArgoCDCommitStatusSpec defines the desired state of ArgoCDCommitStatus.
type ArgoCDCommitStatusSpec struct {
	// +kubebuilder:validation:Required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef,omitempty"`

	// +kubebuilder:validation:Required
	ApplicationSelector *metav1.LabelSelector `json:"applicationSelector,omitempty"`

	// URLTemplate generates the URL to use in the CommitStatus, for example a link to the Argo CD UI. The template
	// is a go text template and receives a single variable called `apps`, which contains a list of the Application
	// objects associated with the commit status.
	//
	// Example:
	//
	// {{ if eq (.apps | len) 1 -}}
	// https://argocd.example.com/applications/{{ .app.metadata.namespace | mustRegexFind '^[0-9a-z-]+$' }}/{{ .app.metadata.name | mustRegexFind '^[0-9a-z-]+$'}}
	// {{- end }}
	//
	// Regex validation might not be necessary if the values from the Application object are trusted.
	// +kubebuilder:validation:Optional
	URLTemplate string `json:"urlTemplate,omitempty"`
}

// ArgoCDCommitStatusStatus defines the observed state of ArgoCDCommitStatus.
type ArgoCDCommitStatusStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
	ApplicationsSelected []ApplicationsSelected `json:"applicationsSelected,omitempty"`
}

type ApplicationsSelected struct {
	Namespace string            `json:"namespace"`
	Name      string            `json:"name"`
	Phase     CommitStatusPhase `json:"phase"`
	Sha       string            `json:"sha"`
	// +kubebuilder:validation:Optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ArgoCDCommitStatus is the Schema for the argocdcommitstatuses API.
type ArgoCDCommitStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ArgoCDCommitStatusSpec   `json:"spec,omitempty"`
	Status ArgoCDCommitStatusStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ArgoCDCommitStatusList contains a list of ArgoCDCommitStatus.
type ArgoCDCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ArgoCDCommitStatus `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ArgoCDCommitStatus{}, &ArgoCDCommitStatusList{})
}
