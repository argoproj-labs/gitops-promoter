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

// ControllerConfigurationSpec defines the desired state of ControllerConfiguration.
type ControllerConfigurationSpec struct {
	PullRequest PullRequestConfiguration `json:"pullRequest,omitempty"`

	// How frequently to requeue promotion strategy resources for auto reconciliation. Default: "5m".
	// Format is go's time.Duration, e.g. "5m" for 5 minutes.
	// +optional
	PromotionStrategyRequeueDuration string `json:"promotionStrategyRequeueDuration,omitempty"`

	// How frequently to requeue proposed commit resources for auto reconciliation. Default: "5m".
	// Format is go's time.Duration, e.g. "5m" for 5 minutes.
	// +optional
	ChangeTransferPolicyRequeueDuration string `json:"changeTransferPolicyRequeueDuration,omitempty"`
}

type MetricsConfig struct {
	// The address the metric endpoint binds to. Default: ":9080".
	// +optional
	Address string `json:"metricsBindAddress,omitempty"`

	// If set, the metrics endpoint is served securely. Default: false.
	// +optional
	Secure bool `json:"metricsSecure,omitempty"`

	// If set, HTTP/2 will be enabled for the metrics and webhook servers. Default: false.
	// +optional
	EnableHTTP2 bool `json:"enableHttp2,omitempty"`
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

// ControllerConfigurationStatus defines the observed state of ControllerConfiguration.
type ControllerConfigurationStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ControllerConfiguration is the Schema for the controllerconfigurations API.
type ControllerConfiguration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ControllerConfigurationSpec   `json:"spec,omitempty"`
	Status ControllerConfigurationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ControllerConfigurationList contains a list of ControllerConfiguration.
type ControllerConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ControllerConfiguration `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ControllerConfiguration{}, &ControllerConfigurationList{})
}
