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

	// DescriptionTemplate is a human-readable description of this validation that will be shown in the SCM provider
	// (GitHub, GitLab, etc.) as the commit status description.
	// Supports Go templates with environment-specific variables.
	//
	// Available template variables:
	//   - {{ .ReportedSha }}: the commit SHA being reported on
	//   - {{ .LastSuccessfulSha }}: last SHA that achieved success for this environment
	//   - {{ .Phase }}: current phase (success/pending/failure)
	//   - {{ .PromotionStrategy }}: full PromotionStrategy object
	//   - {{ .Environment }}: current environment status from PromotionStrategy
	//   - {{ .NamespaceMetadata.Labels }}: map of labels from the namespace
	//   - {{ .NamespaceMetadata.Annotations }}: map of annotations from the namespace
	//
	// Examples:
	//   - "External approval for {{ .Environment.Branch }}"
	//   - "Checking deployment {{ .ReportedSha | trunc 7 }}"
	//   - "{{ .Phase }} - waiting for external approval"
	//
	// If not specified, defaults to empty string.
	// +optional
	DescriptionTemplate string `json:"descriptionTemplate,omitempty"`

	// UrlTemplate is a link to more details about this validation that will be shown in the SCM provider
	// (GitHub, GitLab, etc.) as the commit status target URL.
	// Supports Go templates with the same variables as DescriptionTemplate.
	//
	// This can link to:
	//   - External approval system UI
	//   - Monitoring dashboard
	//   - API endpoint being checked
	//   - Documentation or runbook
	//
	// Examples:
	//   - "https://approvals.example.com/{{ .ReportedSha }}"
	//   - "https://dashboard.example.com/{{ .Environment.Branch }}/status"
	//   - "https://change-api.example.com/changes?asset={{ index .NamespaceMetadata.Labels \"asset-id\" }}"
	//
	// If not specified, defaults to empty string (no URL shown).
	// +optional
	UrlTemplate string `json:"urlTemplate,omitempty"`

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

	// Mode controls how the controller polls or triggers HTTP requests.
	// Exactly one of Polling or Trigger must be specified.
	// +required
	Mode ModeSpec `json:"mode"`
}

// ModeSpec defines the operating mode for the WebRequestCommitStatus controller.
// Exactly one of Polling or Trigger must be specified.
type ModeSpec struct {
	// Polling enables interval-based polling mode.
	// The controller will poll the HTTP endpoint at the specified interval.
	// +optional
	Polling *PollingModeSpec `json:"polling,omitempty"`

	// Trigger enables expression-based triggering mode.
	// The controller will evaluate the expression to determine when to make HTTP requests.
	// +optional
	Trigger *TriggerModeSpec `json:"trigger,omitempty"`
}

// PollingModeSpec defines interval-based polling configuration.
type PollingModeSpec struct {
	// Interval controls how often to retry the HTTP request while in pending state.
	// When reportOn is "proposed": stops polling after success for a given SHA.
	// When reportOn is "active": always polls at this interval.
	// +optional
	// +kubebuilder:default="1m"
	Interval metav1.Duration `json:"interval,omitempty"`
}

// TriggerModeSpec defines expression-based trigger configuration.
type TriggerModeSpec struct {
	// RequeueDuration specifies how long to wait before requeuing to re-evaluate the trigger expression.
	// +optional
	// +kubebuilder:default="1m"
	RequeueDuration metav1.Duration `json:"requeueDuration,omitempty"`

	// Expression is an expr expression that dynamically controls whether the HTTP request should be made.
	// When specified, this expression is evaluated BEFORE each HTTP request to determine if the controller should
	// make the request at all.
	//
	// The expression can return:
	//  1. Boolean: true/false to control whether to make the HTTP request
	//  2. Object with 'trigger' field: {trigger: true/false, ...customData}
	//     - The 'trigger' field controls whether to make the HTTP request
	//     - Any additional fields are stored and available in the next reconcile as 'ExpressionData'
	//
	// Available variables in the expression context:
	//   - PromotionStrategy (PromotionStrategy): the full PromotionStrategy spec and status
	//   - Environment (EnvironmentStatus): current environment's status from PromotionStrategy
	//   - Phase (string): current phase (success/pending/failure)
	//   - ReportedSha (string): the SHA being validated
	//   - LastSuccessfulSha (string): last SHA that achieved success for this environment
	//   - ExpressionData (map[string]any): custom data from previous trigger expression evaluation
	//
	// Note: PromotionStrategy.Status.Environments is an ordered array representing the promotion sequence.
	// Environments[0] is the first environment (e.g., dev), Environments[1] is second (e.g., staging), etc.
	// Changes flow through the array in order. To access the previous environment, use array indexing:
	//   currentIdx = findIndex(PromotionStrategy.Status.Environments, {.Branch == Environment.Branch})
	//   previousEnv = currentIdx > 0 ? PromotionStrategy.Status.Environments[currentIdx-1] : nil
	//
	// Examples (Boolean return):
	//   # Always trigger (equivalent to polling mode)
	//   - "true"
	//
	//   # Only trigger when SHA changes from what we last tracked
	//   - "ReportedSha != ExpressionData['lastCheckedSha']"
	//
	//   # Only trigger when previous environment is healthy
	//   - "len(filter(PromotionStrategy.Status.Environments, {.Branch == 'environment/staging'})[0].LastHealthyDryShas) > 0"
	//
	// Examples (Object return with state tracking):
	//   # Track SHA and only trigger when it changes
	//   - |
	//     {
	//       trigger: ReportedSha != ExpressionData["trackedSha"],
	//       trackedSha: ReportedSha
	//     }
	//
	// +required
	Expression string `json:"expression"`
}

// HTTPRequestSpec defines the HTTP request configuration.
//
// The URL, Headers, and Body fields support Go templates with the following variables:
//   - {{ .ReportedSha }}: the commit SHA being reported on (based on reportOn setting)
//   - {{ .LastSuccessfulSha }}: last SHA that achieved success (empty until first success)
//   - {{ .Phase }}: phase from previous reconcile (success/pending/failure, defaults to pending)
//   - {{ .PromotionStrategy }}: full PromotionStrategy object
//   - {{ .Environment }}: current environment status from PromotionStrategy
//   - {{ .NamespaceMetadata.Labels }}: map of labels from the namespace
//   - {{ .NamespaceMetadata.Annotations }}: map of annotations from the namespace
//
// Example: "https://api.example.com/validate/{{ .Environment.Branch }}/{{ .ReportedSha }}"
type HTTPRequestSpec struct {
	// URLTemplate is the HTTP endpoint to request.
	// Supports Go templates (see HTTPRequestSpec for available variables).
	// +required
	URLTemplate string `json:"urlTemplate"`

	// Method is the HTTP method to use.
	// +required
	// +kubebuilder:validation:Enum=GET;POST;PUT;PATCH
	Method string `json:"method"`

	// HeaderTemplates are additional HTTP headers to include in the request.
	// The map key is the header name and the value is the header value (supports Go templates).
	// See HTTPRequestSpec for available template variables.
	// +optional
	HeaderTemplates map[string]string `json:"headerTemplates,omitempty"`

	// BodyTemplate is the request body to send.
	// Supports Go templates (see HTTPRequestSpec for available variables).
	// +optional
	BodyTemplate string `json:"bodyTemplate,omitempty"`

	// Timeout is the maximum time to wait for the HTTP request to complete.
	// +optional
	// +kubebuilder:default="30s"
	Timeout metav1.Duration `json:"timeout,omitempty"`

	// Authentication specifies authentication configuration for the HTTP request.
	//
	// Supports multiple authentication methods:
	// - Basic Auth: HTTP Basic Authentication with username/password
	// - Bearer Token: Bearer token authentication (e.g., API keys, JWTs)
	// - OAuth2: OAuth2 client credentials flow for obtaining access tokens
	// - TLS: Mutual TLS (mTLS) with client certificates
	//
	// All credentials must be stored in Kubernetes secrets and referenced via secretRef fields.
	//
	// Examples:
	//   # Basic Auth
	//   authentication:
	//     basic:
	//       secretRef:
	//         name: my-creds
	//
	//   # Bearer Token
	//   authentication:
	//     bearer:
	//       secretRef:
	//         name: api-token
	//
	//   # OAuth2 Client Credentials
	//   authentication:
	//     oauth2:
	//       tokenURL: "https://auth.example.com/oauth/token"
	//       secretRef:
	//         name: oauth-creds
	//
	//   # TLS Client Certificate
	//   authentication:
	//     tls:
	//       secretRef:
	//         name: my-tls-cert
	//
	// +optional
	Authentication *HttpAuthentication `json:"authentication,omitempty"`
}

// WebRequestCommitStatusStatus defines the observed state of WebRequestCommitStatus.
type WebRequestCommitStatusStatus struct {
	// Environments holds the status of each environment being tracked.
	// +listType=map
	// +listMapKey=branch
	// +optional
	Environments []WebRequestCommitStatusEnvironmentStatus `json:"environments,omitempty"`

	// Conditions represent the latest available observations of an object's state
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// WebRequestCommitStatusEnvironmentStatus defines the observed status for a specific environment.
type WebRequestCommitStatusEnvironmentStatus struct {
	// Branch is the name of the branch/environment.
	// +required
	// +kubebuilder:validation:MinLength=1
	Branch string `json:"branch"`

	// ReportedSha is the commit SHA being reported on for this environment.
	// This is determined by the reportOn field (proposed or active).
	// Supports both SHA-1 (40 chars) and SHA-256 (64 chars) Git hash formats.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	ReportedSha string `json:"reportedSha,omitempty"`

	// LastSuccessfulSha is the last commit SHA that achieved success status for this environment.
	// Supports both SHA-1 (40 chars) and SHA-256 (64 chars) Git hash formats.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	LastSuccessfulSha string `json:"lastSuccessfulSha,omitempty"`

	// Phase represents the current phase of the validation.
	// +kubebuilder:validation:Enum=pending;success;failure
	// +required
	Phase string `json:"phase"`

	// LastRequestTime is when the last HTTP request was made.
	// +optional
	LastRequestTime *metav1.Time `json:"lastRequestTime,omitempty"`

	// LastResponseStatusCode is the HTTP status code from the last request.
	// +optional
	LastResponseStatusCode *int `json:"lastResponseStatusCode,omitempty"`

	// ExpressionData stores custom data returned from the trigger expression.
	// This data is passed to the next trigger expression evaluation.
	// +optional
	ExpressionData map[string]string `json:"expressionData,omitempty"`
}

// +kubebuilder:ac:generate=true
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// WebRequestCommitStatus is the Schema for the webrequestcommitstatuses API
// +kubebuilder:printcolumn:name="Key",type=string,JSONPath=`.spec.key`
// +kubebuilder:printcolumn:name="PromotionStrategy",type=string,JSONPath=`.spec.promotionStrategyRef.name`
// +kubebuilder:printcolumn:name="ReportOn",type=string,JSONPath=`.spec.reportOn`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
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
func (wrcs *WebRequestCommitStatus) GetConditions() *[]metav1.Condition {
	return &wrcs.Status.Conditions
}

func init() {
	SchemeBuilder.Register(&WebRequestCommitStatus{}, &WebRequestCommitStatusList{})
}
