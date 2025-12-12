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

// WebRequestCommitStatusSpec defines the desired state of WebRequestCommitStatus
type WebRequestCommitStatusSpec struct {
	// PromotionStrategyRef references the PromotionStrategy this applies to.
	// The controller will check commits from ALL environments in the referenced PromotionStrategy
	// where this WebRequestCommitStatus.Spec.Key matches an entry in either:
	//   - PromotionStrategy.Spec.ProposedCommitStatuses (applies to all environments), OR
	//   - PromotionStrategy.Spec.ActiveCommitStatuses (applies to all environments), OR
	//   - Environment.ProposedCommitStatuses (applies to specific environment), OR
	//   - Environment.ActiveCommitStatuses (applies to specific environment)
	// +required
	PromotionStrategyRef ObjectReference `json:"promotionStrategyRef"`

	// Key is the unique identifier for this validation rule.
	// It is used as the commit status key and in status messages.
	// This key is matched against PromotionStrategy's proposedCommitStatuses or activeCommitStatuses
	// to determine which environments this validation applies to.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
	Key string `json:"key"`

	// Description is a human-readable description of this validation that will be shown in the SCM provider
	// (GitHub, GitLab, etc.) as the commit status description.
	// If not specified, defaults to empty string.
	// +optional
	Description string `json:"description,omitempty"`

	// ReportOn specifies which commit SHA to report the CommitStatus on.
	// - "proposed": Reports on the proposed hydrated commit SHA (default)
	// - "active": Reports on the active hydrated commit SHA
	//
	// When "proposed": Polls until success, then stops polling for that SHA.
	// When "active": Polls forever, even after success (active state can change).
	//
	// +optional
	// +kubebuilder:default="proposed"
	// +kubebuilder:validation:Enum=proposed;active
	ReportOn string `json:"reportOn,omitempty"`

	// HTTPRequest defines the HTTP request configuration.
	// Supports Go templates in URL, Headers, and Body fields.
	// +required
	HTTPRequest HTTPRequestSpec `json:"httpRequest"`

	// Expression is evaluated using the expr library (github.com/expr-lang/expr) against the HTTP response.
	// The expression must return a boolean value where true indicates the validation passed.
	//
	// Available variables in the expression context:
	//   - Response.StatusCode (int): the HTTP response status code
	//   - Response.Body (any): parsed JSON as map[string]any, or raw string if not JSON
	//   - Response.Headers (map[string][]string): HTTP response headers
	//
	// Examples:
	//   - Response.StatusCode == 200
	//   - Response.StatusCode == 200 && Response.Body.approved == true
	//   - Response.StatusCode == 200 && Response.Body.status == "approved"
	//   - len(Response.Headers["X-Approval"]) > 0
	//
	// +required
	Expression string `json:"expression"`

	// Polling controls polling behavior for the HTTP request.
	// +optional
	Polling PollingSpec `json:"polling,omitempty"`
}

// PollingSpec defines polling configuration.
type PollingSpec struct {
	// Interval controls how often to retry the HTTP request while in pending state.
	// When reportOn is "proposed": stops polling after success for a given SHA.
	// When reportOn is "active": always polls at this interval.
	// +optional
	// +kubebuilder:default="1m"
	Interval metav1.Duration `json:"interval,omitempty"`
}

// HTTPRequestSpec defines the HTTP request configuration.
//
// The URL, Headers, and Body fields support Go templates with the following variables:
//   - {{ .ProposedHydratedSha }}: the proposed commit SHA
//   - {{ .ActiveHydratedSha }}: the active/deployed commit SHA
//   - {{ .Key }}: the WebRequestCommitStatus key
//   - {{ .Name }}: the WebRequestCommitStatus resource name
//   - {{ .Namespace }}: the WebRequestCommitStatus namespace
//   - {{ .Labels }}: map of labels from the WebRequestCommitStatus resource
//   - {{ .Annotations }}: map of annotations from the WebRequestCommitStatus resource
//
// Example accessing labels/annotations: {{ index .Labels "team" }} or {{ index .Annotations "slack-channel" }}
type HTTPRequestSpec struct {
	// URL is the HTTP endpoint to request.
	// Supports Go templates (see HTTPRequestSpec for available variables).
	// +required
	URL string `json:"url"`

	// Method is the HTTP method to use.
	// +required
	// +kubebuilder:validation:Enum=GET;POST;PUT;PATCH
	Method string `json:"method"`

	// Headers are additional HTTP headers to include in the request.
	// Header values support Go templates (see HTTPRequestSpec for available variables).
	// +optional
	Headers map[string]string `json:"headers,omitempty"`

	// Body is the request body to send.
	// Supports Go templates (see HTTPRequestSpec for available variables).
	// +optional
	Body string `json:"body,omitempty"`

	// Timeout is the maximum time to wait for the HTTP request to complete.
	// +optional
	// +kubebuilder:default="30s"
	Timeout metav1.Duration `json:"timeout,omitempty"`

	// AuthSecretRef references a Kubernetes Secret containing authentication credentials.
	// +optional
	AuthSecretRef *AuthSecretRef `json:"authSecretRef,omitempty"`
}

// AuthSecretRef references a Kubernetes Secret for HTTP authentication.
type AuthSecretRef struct {
	// Name is the name of the Secret in the same namespace as the WebRequestCommitStatus.
	// +required
	Name string `json:"name"`

	// Type specifies how to use the Secret for authentication.
	// - "none": No authentication (Secret is ignored)
	// - "basic": Uses 'username' and 'password' keys from the Secret for HTTP Basic Auth
	// - "bearer": Uses 'token' key from the Secret for Bearer token authentication
	// - "header": Uses all keys from the Secret as custom HTTP headers
	// +required
	// +kubebuilder:validation:Enum=none;basic;bearer;header
	Type string `json:"type"`
}

// WebRequestCommitStatusStatus defines the observed state of WebRequestCommitStatus.
type WebRequestCommitStatusStatus struct {
	// Environments holds the validation results for each environment where this validation applies.
	// Each entry corresponds to an environment from the PromotionStrategy where the Key matches
	// either global or environment-specific proposedCommitStatuses/activeCommitStatuses.
	// +listType=map
	// +listMapKey=environment
	// +optional
	Environments []WebRequestCommitStatusEnvironmentStatus `json:"environments,omitempty"`

	// Conditions represent the latest available observations of the WebRequestCommitStatus's state.
	// Standard condition types include "Ready" which aggregates the status of all environments.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// WebRequestCommitStatusEnvironmentStatus defines the observed status for a specific environment.
type WebRequestCommitStatusEnvironmentStatus struct {
	// Environment is the environment branch name being validated.
	// +required
	Environment string `json:"environment"`

	// ProposedHydratedSha is the proposed hydrated commit SHA from the PromotionStrategy.
	// +required
	ProposedHydratedSha string `json:"proposedHydratedSha"`

	// ActiveHydratedSha is the active hydrated commit SHA from the PromotionStrategy.
	// +optional
	ActiveHydratedSha string `json:"activeHydratedSha,omitempty"`

	// ReportedSha is the SHA where the CommitStatus was reported (based on reportOn field).
	// +optional
	ReportedSha string `json:"reportedSha,omitempty"`

	// LastSuccessfulSha is the SHA that last achieved success.
	// Used to detect SHA changes - when ReportedSha != LastSuccessfulSha, polling continues.
	// When they match and phase is success, polling stops (for reportOn: proposed).
	// +optional
	LastSuccessfulSha string `json:"lastSuccessfulSha,omitempty"`

	// Phase represents the current validation state of the commit.
	// - "pending": validation has not completed, HTTP request pending, or expression returned false
	// - "success": expression evaluated to true
	// - "failure": expression compilation failed
	// +kubebuilder:validation:Enum=pending;success;failure
	// +required
	Phase string `json:"phase"`

	// LastRequestTime is when the HTTP request was last made.
	// +optional
	LastRequestTime *metav1.Time `json:"lastRequestTime,omitempty"`

	// Response contains information about the HTTP response from the last request.
	// +optional
	Response ResponseInfo `json:"response,omitempty"`

	// ExpressionResult contains the boolean result of the expression evaluation.
	// Only set when the expression successfully evaluates to a boolean.
	// +optional
	ExpressionResult *bool `json:"expressionResult,omitempty"`
}

// ResponseInfo contains information about the HTTP response.
type ResponseInfo struct {
	// StatusCode is the HTTP response status code.
	// +optional
	StatusCode *int `json:"statusCode,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Strategy",type=string,JSONPath=`.spec.promotionStrategyRef.name`
// +kubebuilder:printcolumn:name="Key",type=string,JSONPath=`.spec.key`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// WebRequestCommitStatus is the Schema for the webrequestcommitstatuses API.
//
// It makes HTTP requests to external endpoints and evaluates the response using expressions
// to create CommitStatus resources with the validation results.
//
// The controller polls the configured URL at the specified Polling.Interval.
// When reportOn is "proposed", polling stops after success for a given SHA.
// When reportOn is "active", polling continues forever.
//
// Workflow:
//  1. Controller reads PromotionStrategy to get ProposedHydratedSha and ActiveHydratedSha
//  2. Controller renders URL, Headers, and Body templates with SHA context
//  3. Controller makes HTTP request with optional authentication
//  4. Controller evaluates expression against response
//  5. Controller creates/updates CommitStatus with result attached to the SHA specified by reportOn
//  6. Controller requeues based on Polling.Interval and success state
type WebRequestCommitStatus struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec defines the desired state of WebRequestCommitStatus
	// +required
	Spec WebRequestCommitStatusSpec `json:"spec"`

	// status defines the observed state of WebRequestCommitStatus
	// +optional
	Status WebRequestCommitStatusStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// WebRequestCommitStatusList contains a list of WebRequestCommitStatus
type WebRequestCommitStatusList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WebRequestCommitStatus `json:"items"`
}

// GetConditions returns the conditions of the WebRequestCommitStatus.
func (w *WebRequestCommitStatus) GetConditions() *[]metav1.Condition {
	return &w.Status.Conditions
}

func init() {
	SchemeBuilder.Register(&WebRequestCommitStatus{}, &WebRequestCommitStatusList{})
}
