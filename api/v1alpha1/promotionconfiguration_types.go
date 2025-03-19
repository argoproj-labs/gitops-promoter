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

// PromotionConfigurationSpec defines the desired state of PromotionConfiguration.
type PromotionConfigurationSpec struct {
	PullRequest PullRequestConfiguration `json:"pullRequest,omitempty"`
}

type PullRequestConfiguration struct {
	Template PullRequestTemplate `json:"template,omitempty"`
}

type PullRequestTemplate struct {
	// Template used to generate the title of the pull request.
	// Uses Go template syntax and Sprig functions are available.
	Title string `json:"title,omitempty"`
	// Template used to generate the description of the pull request.
	// Uses Go template syntax and Sprig functions are available.
	Description string `json:"description,omitempty"`
}

// PromotionConfigurationStatus defines the observed state of PromotionConfiguration.
type PromotionConfigurationStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// PromotionConfiguration is the Schema for the promotionconfigurations API.
type PromotionConfiguration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PromotionConfigurationSpec   `json:"spec,omitempty"`
	Status PromotionConfigurationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PromotionConfigurationList contains a list of PromotionConfiguration.
type PromotionConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PromotionConfiguration `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PromotionConfiguration{}, &PromotionConfigurationList{})
}
