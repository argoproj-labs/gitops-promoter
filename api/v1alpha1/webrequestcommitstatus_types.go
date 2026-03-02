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
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
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
//
// +kubebuilder:validation:ExactlyOneOf=polling;trigger
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

	// Trigger configures the boolean guard expression and optional data-tracking expression that together
	// control whether the HTTP request is made on each reconcile cycle.
	// +required
	Trigger TriggerExpressionSpec `json:"trigger"`

	// Response optionally configures an expression that extracts and transforms data from the HTTP response
	// for storage in ResponseData, which persists across reconcile cycles.
	// +optional
	Response *ResponseExpressionSpec `json:"response,omitempty"`
}

// TriggerExpressionSpec holds the expressions that control when the HTTP request is fired.
type TriggerExpressionSpec struct {
	// Expression is a boolean expr expression that decides whether the HTTP request should be made.
	// It is evaluated BEFORE each potential HTTP request. When it returns true the request is made;
	// when false the controller keeps the previous phase and skips the request.
	//
	// Available variables:
	//   - PromotionStrategy (PromotionStrategy): the full PromotionStrategy spec and status
	//   - Environment (EnvironmentStatus): current environment's status from PromotionStrategy
	//   - Phase (string): current phase (success/pending/failure)
	//   - ReportedSha (string): the SHA being validated
	//   - LastSuccessfulSha (string): last SHA that achieved success for this environment
	//   - TriggerData (map[string]any): custom data from the previous dataExpression evaluation
	//   - ResponseData (map[string]any): response data from the previous HTTP request (if any)
	//
	// Note: PromotionStrategy.Status.Environments is an ordered array representing the promotion sequence.
	// Environments[0] is the first environment (e.g., dev), Environments[1] is second (e.g., staging), etc.
	// Changes flow through the array in order. To access the previous environment, use array indexing:
	//   currentIdx = findIndex(PromotionStrategy.Status.Environments, {.Branch == Environment.Branch})
	//   previousEnv = currentIdx > 0 ? PromotionStrategy.Status.Environments[currentIdx-1] : nil
	//
	// Examples:
	//   # Always trigger (equivalent to polling mode)
	//   - "true"
	//
	//   # Only trigger when SHA changes from what we last tracked
	//   - "ReportedSha != TriggerData['lastCheckedSha']"
	//
	//   # Only trigger when a particular commit status is success (e.g. argocd-health)
	//   - "size(filter(Environment.Proposed.CommitStatuses, {.Key == 'argocd-health'})) > 0 && filter(Environment.Proposed.CommitStatuses, {.Key == 'argocd-health'})[0].Phase == 'success'"
	//
	//   # Only retry if the previous response indicated we should
	//   - "ResponseData == nil || ResponseData.status == 'retry'"
	//
	// +required
	Expression string `json:"expression"`

	// DataExpression is an optional expr expression that produces a map of data to persist across
	// reconcile cycles. Its result is stored in status.environments[].triggerData and is available
	// in the next reconcile as the TriggerData variable (in both this expression and in the trigger
	// expression). Use it to track state such as attempt counts, last-seen SHAs, or timestamps.
	//
	// Available variables (same as Expression):
	//   - PromotionStrategy, Environment, Phase, ReportedSha, LastSuccessfulSha, TriggerData, ResponseData
	//
	// The expression must return a map/object. Every key in the returned map is stored in TriggerData.
	//
	// Examples:
	//   # Track SHA to detect changes
	//   - "{ trackedSha: ReportedSha }"
	//
	//   # Increment attempt counter
	//   - "{ attemptCount: (TriggerData[\"attemptCount\"] ?? 0) + 1 }"
	//
	//   # Store last request timestamp
	//   - "{ lastRequestTime: string(now()) }"
	//
	// +optional
	DataExpression string `json:"dataExpression,omitempty"`
}

// ResponseExpressionSpec holds the expression that extracts data from the HTTP response.
type ResponseExpressionSpec struct {
	// DataExpression is an expr expression that extracts and transforms data from the HTTP response
	// before storing it in ResponseData. This allows you to store only the fields you need instead
	// of the entire response body.
	//
	// The expression is evaluated after the HTTP request completes (for any response status) and its
	// result is stored in status.environments[].responseData. On the next reconcile that value is
	// available as the ResponseData variable, so trigger expressions can read it (e.g. to implement
	// retry-on-response-status logic) and description/URL templates can reference it.
	//
	// If this field is omitted no response data is stored (status.environments[].responseData is not updated).
	//
	// Available variables:
	//   - Response.StatusCode (int): HTTP response status code
	//   - Response.Body (any): parsed JSON as map[string]any, or raw string if not JSON
	//   - Response.Headers (map[string][]string): HTTP response headers
	//
	// The expression must return a map/object that will be stored as ResponseData.
	//
	// Examples:
	//   # Store only specific fields from response body
	//   - |
	//     {
	//       status: Response.Body.status,
	//       retryAfter: Response.Body.retryAfter,
	//       message: Response.Body.message
	//     }
	//
	//   # Store status code and a single field
	//   - |
	//     {
	//       statusCode: Response.StatusCode,
	//       approved: Response.Body.approved
	//     }
	//
	//   # Extract rate limit info from headers
	//   - |
	//     {
	//       statusCode: Response.StatusCode,
	//       rateLimit: {
	//         remaining: int(Response.Headers["X-RateLimit-Remaining"][0]),
	//         reset: Response.Headers["X-RateLimit-Reset"][0]
	//       }
	//     }
	//
	//   # Conditional extraction based on status code
	//   - |
	//     Response.StatusCode == 200 ?
	//       {status: "success", data: Response.Body.result} :
	//       {status: "error", error: Response.Body.error}
	//
	// +required
	DataExpression string `json:"dataExpression"`
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
	Authentication *HTTPAuthentication `json:"authentication,omitempty"`
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

	// TriggerData stores the map returned by spec.mode.trigger.trigger.dataExpression.
	// It is passed back into the next reconcile as the TriggerData variable, making it available
	// to both the trigger expression and the data expression. Use it to track state across
	// reconcile cycles (e.g. last-seen SHA, attempt counter, last request timestamp).
	// The data is preserved as arbitrary JSON.
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	TriggerData *apiextensionsv1.JSON `json:"triggerData,omitempty"`

	// ResponseData stores the map returned by spec.mode.trigger.response.dataExpression.
	// This field is ONLY populated when spec.mode.trigger.response is provided.
	// The response dataExpression is evaluated after each HTTP request and its result is stored here,
	// allowing subsequent trigger expressions to inspect data from the previous response when
	// deciding whether to issue the next request.
	//
	// Without spec.mode.trigger.response, this field will always be nil.
	//
	// Available in trigger expressions as: ResponseData.field1, ResponseData.field2, etc.
	// (where fields depend on what your response.dataExpression returns)
	//
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	ResponseData *apiextensionsv1.JSON `json:"responseData,omitempty"`
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
