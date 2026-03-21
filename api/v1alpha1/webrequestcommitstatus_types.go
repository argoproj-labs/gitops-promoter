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

// Mode.Context allowed values for WebRequestCommitStatus.
const (
	// ContextEnvironments is the default: one HTTP request per environment; each environment has its own phase.
	ContextEnvironments = "environments"
	// ContextPromotionStrategy means one HTTP request per reconcile; phase(s) are applied to all environments' CommitStatuses.
	ContextPromotionStrategy = "promotionstrategy"
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
	// Must be lowercase alphanumeric with hyphens, 1–63 characters (pattern: ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$).
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=^[a-z0-9]([-a-z0-9]*[a-z0-9])?$
	Key string `json:"key"`

	// DescriptionTemplate is a human-readable description of this validation that will be shown in the SCM provider
	// (GitHub, GitLab, etc.) as the commit status description.
	// Supports Go templates; Sprig functions are available except env, expandenv, getHostByName; urlQueryEscape is also available.
	//
	// Template data is the latest: rendered after the most recent HTTP request and trigger evaluation, so description/URL reflect current status.
	//
	// Available template variables:
	//   - {{ .ReportedSha }}: the commit SHA being reported on
	//   - {{ .LastSuccessfulSha }}: last SHA that achieved success for this environment
	//   - {{ .Phase }}: current phase (success/pending/failure)
	//   - {{ .PromotionStrategy }}: full PromotionStrategy object
	//   - {{ .Environment }}: current environment status from PromotionStrategy
	//   - {{ .NamespaceMetadata.Labels }}: map of labels from the namespace
	//   - {{ .NamespaceMetadata.Annotations }}: map of annotations from the namespace
	//   - {{ index .TriggerOutput "key" }}: (trigger mode only) map from trigger.when.output.expression
	//   - {{ index .ResponseOutput "key" }}: (trigger mode only) map from response.output.expression
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
	// Supports Go templates with the same variables and timing as DescriptionTemplate (latest data; TriggerOutput/ResponseOutput in trigger mode).
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
	//
	// If not specified, defaults to empty string (no URL shown).
	// +optional
	UrlTemplate string `json:"urlTemplate,omitempty"`

	// ReportOn specifies which commit SHA to report the CommitStatus on.
	// - "proposed": Reports on the proposed hydrated commit SHA (default)
	// - "active": Reports on the active hydrated commit SHA
	//
	// When "proposed": Polls until success, then stops polling for that SHA.
	// Use "proposed" for checks that need to run just once before a change is promoted, like an approval step.
	//
	// When "active": Polls forever, even after success (active state can change).
	// Use "active" for checks that monitor the change after it's released, for example a metrics monitoring service.
	//
	// +optional
	// +kubebuilder:default="proposed"
	// +kubebuilder:validation:Enum=proposed;active
	ReportOn string `json:"reportOn,omitempty"`

	// HTTPRequest defines the HTTP request configuration.
	// Supports Go templates in URL, Headers, and Body fields.
	// +required
	HTTPRequest HTTPRequestSpec `json:"httpRequest"`

	// Success defines when the HTTP response is considered successful (commit status phase success).
	// +required
	Success SuccessSpec `json:"success"`

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

	// Context controls whether the controller makes one HTTP request per environment or one per PromotionStrategy.
	// - "environments" (default): one HTTP request per environment; each environment gets its own phase and status.
	// - "promotionstrategy": one HTTP request per reconcile; the same phase is applied to CommitStatuses for all
	//   environments, each still reporting on that environment's reportOn SHA. When context is promotionstrategy,
	//   per-environment template variables (Environment, ReportedSha, LastSuccessfulSha) are not set; do not use
	//   them in URL, body, description, or trigger templates.
	// +optional
	// +kubebuilder:default=environments
	// +kubebuilder:validation:Enum=environments;promotionstrategy
	Context string `json:"context,omitempty"`
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

// SuccessSpec defines when the HTTP response is considered successful (commit status phase success).
type SuccessSpec struct {
	// When holds the expression evaluated against the HTTP response (expr library).
	// Available variables: Response.StatusCode (int), Response.Body (parsed JSON as map[string]any, or raw string if not JSON), Response.Headers (map[string][]string).
	//
	// Return type:
	// - When mode.context is "environments": must return a boolean. true sets phase Success, false sets Pending.
	// - When mode.context is "promotionstrategy": may return:
	//   - A boolean: same phase (Success or Pending) for all environments.
	//   - An object with defaultPhase (optional) and environments (optional array):
	//     - defaultPhase: "success", "pending", or "failure" — defaults to "pending" when omitted. Used for all when environments is empty, or for branches not listed in environments.
	//     - environments: optional array of { branch (string), phase (string) }. Example: { defaultPhase: "pending", environments: [{ branch: "dev", phase: "success" }, { branch: "staging", phase: "pending" }] }.
	// +required
	When WhenSpec `json:"when"`
}

// WhenSpec holds the success expression.
type WhenSpec struct {
	// Expression is evaluated using the expr library (github.com/expr-lang/expr).
	// Must return a boolean when mode.context is "environments".
	// When mode.context is "promotionstrategy", may return a boolean or an object { defaultPhase?, environments? } as described in SuccessSpec.
	// +required
	Expression string `json:"expression"`
}

// OutputSpec holds an expression that returns a map/object to persist (e.g. TriggerOutput or ResponseOutput).
type OutputSpec struct {
	// Expression is an expr expression that must return a map/object.
	// +required
	Expression string `json:"expression"`
}

// TriggerModeSpec defines expression-based trigger configuration.
type TriggerModeSpec struct {
	// RequeueDuration specifies how long to wait before requeuing to re-evaluate the trigger expression.
	// +optional
	// +kubebuilder:default="1m"
	RequeueDuration metav1.Duration `json:"requeueDuration,omitempty"`

	// When configures the boolean guard and optional output expression that control whether the HTTP request is made.
	// +required
	When WhenWithOutputSpec `json:"when"`

	// Response optionally configures an expression that extracts data from the HTTP response into ResponseOutput.
	// +optional
	Response *ResponseOutputSpec `json:"response,omitempty"`
}

// WhenWithOutputSpec holds a when condition (boolean expression) and optional output (map expression).
type WhenWithOutputSpec struct {
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
	//   - TriggerOutput (map[string]any): custom data from the previous when.output.expression evaluation
	//   - ResponseOutput (map[string]any): response data from the previous HTTP request (if any)
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
	//   - "ReportedSha != TriggerOutput['lastCheckedSha']"
	//
	//   # Only trigger when a particular commit status is success (e.g. argocd-health)
	//   - "size(filter(Environment.Proposed.CommitStatuses, {.Key == 'argocd-health'})) > 0 && filter(Environment.Proposed.CommitStatuses, {.Key == 'argocd-health'})[0].Phase == 'success'"
	//
	//   # Only retry if the previous response indicated we should
	//   - "ResponseOutput == nil || ResponseOutput.status == 'retry'"
	//
	// +required
	Expression string `json:"expression"`

	// Output optionally holds an expression that produces a map of data to persist across reconcile cycles.
	// The expression runs on every reconcile (whether or not the HTTP request is made). Its result is stored in
	// status.environments[].triggerOutput and is available in the next reconcile as TriggerOutput (in when.expression,
	// when.output.expression, and in all templates). Use it to track state such as attempt counts, last-seen SHAs, or timestamps.
	//
	// Available variables (same as Expression):
	//   - PromotionStrategy, Environment, Phase, ReportedSha, LastSuccessfulSha, TriggerOutput, ResponseOutput
	//
	// The expression must return a map/object. Every key in the returned map is stored in TriggerOutput.
	//
	// Examples:
	//   # Track SHA to detect changes
	//   - "{ trackedSha: ReportedSha }"
	//
	//   # Increment attempt counter
	//   - "{ attemptCount: (TriggerOutput[\"attemptCount\"] ?? 0) + 1 }"
	//
	// +optional
	Output *OutputSpec `json:"output,omitempty"`
}

// ResponseOutputSpec holds the expression that extracts data from the HTTP response into ResponseOutput.
type ResponseOutputSpec struct {
	// Output holds the expression that extracts and transforms data from the HTTP response.
	// The expression is evaluated after the HTTP request completes (for any response status) and its
	// result is stored in status.environments[].responseOutput. On the next reconcile that value is
	// available as the ResponseOutput variable in trigger expressions and in all templates.
	// Available in the expression: Response.StatusCode (int), Response.Body (parsed JSON or raw string), Response.Headers (map[string][]string).
	// The expression must return a map/object.
	// +required
	Output OutputSpec `json:"output"`
}

// HTTPRequestSpec defines the HTTP request configuration.
//
// URLTemplate, HeaderTemplates, and BodyTemplate support Go templates (same Sprig/exclusions as DescriptionTemplate).
// These fields are rendered with previous-attempt data: they are evaluated before the current HTTP request is made,
// so they never contain the response from the request being built. Use TriggerOutput/ResponseOutput for state from
// the previous run (trigger mode only).
//
// Template variables:
//   - {{ .ReportedSha }}: the commit SHA being reported on (based on reportOn setting)
//   - {{ .LastSuccessfulSha }}: last SHA that achieved success (empty until first success)
//   - {{ .Phase }}: phase from previous reconcile (success/pending/failure, defaults to pending)
//   - {{ .PromotionStrategy }}: full PromotionStrategy object
//   - {{ .Environment }}: current environment status from PromotionStrategy
//   - {{ .NamespaceMetadata.Labels }}: map of labels from the namespace
//   - {{ .NamespaceMetadata.Annotations }}: map of annotations from the namespace
//   - {{ index .TriggerOutput "key" }}, {{ index .ResponseOutput "key" }}: (trigger mode only) from previous reconcile
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
	// Environments holds the status of each environment when context is "environments".
	// When context is "promotionstrategy", this slice is empty and PromotionStrategyContext is used instead.
	// +listType=map
	// +listMapKey=branch
	// +optional
	Environments []WebRequestCommitStatusEnvironmentStatus `json:"environments,omitempty"`

	// PromotionStrategyContext holds the result of the one HTTP run when context is "promotionstrategy".
	// One request is made per reconcile; phase(s) are reported on each environment's CommitStatus.
	// +optional
	PromotionStrategyContext *WebRequestCommitStatusPromotionStrategyContextStatus `json:"promotionStrategyContext,omitempty"`

	// Conditions represent the latest available observations of an object's state
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// WebRequestCommitStatusPromotionStrategyContextStatus holds the observed state for context=promotionstrategy (one request per reconcile).
type WebRequestCommitStatusPromotionStrategyContextStatus struct {
	// Phase is the validation result from the HTTP request (pending, success, or failure).
	// When PhasePerBranch is set, Phase is used as the default for any branch not listed in PhasePerBranch.
	// +kubebuilder:validation:Enum=pending;success;failure
	// +required
	Phase string `json:"phase"`

	// PhasePerBranch holds per-branch phases when the success expression returned an object with per-branch overrides.
	// Key is branch name, value is "pending", "success", or "failure". When set, each environment's CommitStatus
	// uses this phase; branches not in the map use Phase.
	// +optional
	PhasePerBranch map[string]CommitStatusPhase `json:"phasePerBranch,omitempty"`

	// LastRequestTime is when the last HTTP request was made.
	// +optional
	LastRequestTime *metav1.Time `json:"lastRequestTime,omitempty"`

	// LastResponseStatusCode is the HTTP status code from the last request.
	// +optional
	LastResponseStatusCode *int `json:"lastResponseStatusCode,omitempty"`

	// TriggerOutput stores the map returned by spec.mode.trigger.when.output.expression.
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	TriggerOutput *apiextensionsv1.JSON `json:"triggerOutput,omitempty"`

	// ResponseOutput stores the map returned by spec.mode.trigger.response.output.expression.
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	ResponseOutput *apiextensionsv1.JSON `json:"responseOutput,omitempty"`
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
	// This controller sets only "pending" or "success"; it never sets "failure" (failure is allowed by the enum for API consistency).
	// +kubebuilder:validation:Enum=pending;success;failure
	// +required
	Phase string `json:"phase"`

	// LastRequestTime is when the last HTTP request was made.
	// +optional
	LastRequestTime *metav1.Time `json:"lastRequestTime,omitempty"`

	// LastResponseStatusCode is the HTTP status code from the last request.
	// +optional
	LastResponseStatusCode *int `json:"lastResponseStatusCode,omitempty"`

	// TriggerOutput stores the map returned by spec.mode.trigger.when.output.expression.
	// It is passed back into the next reconcile as the TriggerOutput variable (in expressions and templates), making it available
	// to both the trigger when.expression and when.output.expression. Use it to track state across
	// reconcile cycles (e.g. last-seen SHA, attempt counter, last request timestamp).
	// The data is preserved as arbitrary JSON.
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	TriggerOutput *apiextensionsv1.JSON `json:"triggerOutput,omitempty"`

	// ResponseOutput stores the map returned by spec.mode.trigger.response.output.expression.
	// This field is ONLY populated when spec.mode.trigger.response is provided.
	// The response output expression is evaluated after each HTTP request and its result is stored here,
	// allowing subsequent trigger expressions to inspect data from the previous response when
	// deciding whether to issue the next request.
	//
	// Without spec.mode.trigger.response, this field will always be nil.
	//
	// Available in trigger expressions and templates as: ResponseOutput.field1, ResponseOutput.field2, etc.
	// (where fields depend on what your response.output.expression returns)
	//
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	ResponseOutput *apiextensionsv1.JSON `json:"responseOutput,omitempty"`
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
