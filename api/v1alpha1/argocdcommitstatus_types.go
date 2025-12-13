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
	// PromotionStrategyRef is a reference to the promotion strategy that this commit status applies to.
	// +kubebuilder:validation:Required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef,omitempty"`

	// ApplicationSelector is a label selector that selects the Argo CD applications to which this commit status applies.
	// +kubebuilder:validation:Required
	ApplicationSelector *metav1.LabelSelector `json:"applicationSelector,omitempty"`

	// URL generates the URL to use in the CommitStatus, for example a link to the Argo CD UI.
	// +kubebuilder:validation:Optional
	URL URLConfig `json:"url,omitempty"`
}

// URLConfig is a template that can be rendered using the Go template engine.
type URLConfig struct {
	// Template is a go text template and receives .Environment and .ArgoCDCommitStatus variables. A function called urlQueryEscape
	// is available to escape url query parameters. The template can be configured with options to control the behavior
	// during execution if a variable is not present.
	//
	// Example:
	//
	//   {{- $baseURL := "https://dev.argocd.local" -}}
	//   {{- if eq .Environment "environment/development" -}}
	//   {{- $baseURL = "https://dev.argocd.local" -}}
	//   {{- else if eq .Environment "environment/staging" -}}
	//   {{- $baseURL = "https://staging.argocd.local" -}}
	//   {{- else if eq .Environment "environment/production" -}}
	//   {{- $baseURL = "https://prod.argocd.local" -}}
	//   {{- end -}}
	//   {{- $labels := "" -}}
	//   {{- range $key, $value := .ArgoCDCommitStatus.Spec.ApplicationSelector.MatchLabels -}}
	//   {{- $labels = printf "%s%s=%s," $labels $key $value -}}
	//   {{- end -}}
	//   {{ printf "%s/applications?labels=%s" $baseURL (urlQueryEscape $labels) }}
	//
	// +kubebuilder:validation:Optional
	Template string `json:"template,omitempty"`

	// Options sets options for the template. Options are described by
	// strings, either a simple string or "key=value". There can be at
	// most one equals sign in an option string. If the option string
	// is unrecognized or otherwise invalid, Option panics.
	//
	// Known options:
	//
	// missingkey: Control the behavior during execution if a map is
	// indexed with a key that is not present in the map.
	//
	//	"missingkey=default" or "missingkey=invalid"
	//		The default behavior: Do nothing and continue execution.
	//		If printed, the result of the index operation is the string
	//		"<no value>".
	//	"missingkey=zero"
	//		The operation returns the zero value for the map type's element.
	//	"missingkey=error"
	//		Execution stops immediately with an error.
	//
	// +kubebuilder:validation:Optional
	Options []string `json:"options,omitempty"`
}

// ArgoCDCommitStatusStatus defines the observed state of ArgoCDCommitStatus.
type ArgoCDCommitStatusStatus struct {
	// ApplicationsSelected represents the Argo CD applications that are selected by the commit status.
	// This field is sorted by environment (same order as the referenced PromotionStrategy), then namespace, then name.
	ApplicationsSelected []ApplicationsSelected `json:"applicationsSelected,omitempty"`

	// Conditions Represents the observations of the current state.
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// GetConditions returns the conditions of the ArgoCDCommitStatus.
func (cs *ArgoCDCommitStatus) GetConditions() *[]metav1.Condition {
	return &cs.Status.Conditions
}

// ApplicationsSelected represents the Argo CD applications that are selected by the commit status.
type ApplicationsSelected struct {
	// Namespace is the namespace of the Argo CD application.
	Namespace string `json:"namespace"`
	// Name is the name of the Argo CD application.
	Name string `json:"name"`
	// Phase is the current phase of the commit status.
	Phase CommitStatusPhase `json:"phase"`
	// Sha is the commit SHA that this status is associated with.
	Sha string `json:"sha"`
	// LastTransitionTime is the last time the phase transitioned.
	// +kubebuilder:validation:Optional
	LastTransitionTime *metav1.Time `json:"lastTransitionTime"`
	// Environment is the syncSource.targetBranch of the Argo CD application (in effect, its environment).
	// +required
	// +kubebuilder:validation:MinLength=1
	Environment string `json:"environment,omitempty"`
	// ClusterName is the name of the cluster that the application manifest is deployed to. An empty string indicates
	// the local cluster.
	ClusterName string `json:"clusterName"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// ArgoCDCommitStatus is the Schema for the argocdcommitstatuses API.
// +kubebuilder:printcolumn:name="PromotionStrategy",type=string,JSONPath=`.spec.promotionStrategyRef.name`,priority=1
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
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
