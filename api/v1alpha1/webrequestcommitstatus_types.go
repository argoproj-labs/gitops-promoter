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

// ContextMode represents the request-scope mode for WebRequestCommitStatus.
type ContextMode string

const (
	// ContextEnvironments is the default: one HTTP request per environment; each environment has its own phase.
	ContextEnvironments ContextMode = "environments"
	// ContextPromotionStrategy means at most one HTTP request per WebRequestCommitStatus; phase(s) are applied to all environments' CommitStatuses.
	ContextPromotionStrategy ContextMode = "promotionstrategy"
)

// WebRequestCommitStatusSpec defines the desired state of WebRequestCommitStatus.
//
// Documentation map: template variables and Sprig rules — HTTPRequestSpec; context (environments vs promotionstrategy) — ModeSpec;
// success expression after HTTP response — WhenSpec; trigger when/output expressions — WhenWithOutputSpec; persisted trigger/response maps — WebRequestCommitStatusEnvironmentStatus.
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

	// DescriptionTemplate is the human-readable commit status description in the SCM provider (GitHub, GitLab, etc.).
	// Uses Go templates; variable list and Sprig rules match HTTPRequestSpec (spec.httpRequest). Rendered with the latest
	// data after the most recent HTTP request and trigger evaluation (unlike spec.httpRequest URL/body/headers, which use pre-request data — see HTTPRequestSpec).
	// How spec.mode.context restricts template data: see ModeSpec.
	//
	// Examples: "External approval for {{ .Environment.Branch }}", "Checking deployment {{ .ReportedSha | trunc 7 }}", "{{ .Phase }} - waiting for external approval".
	//
	// If not specified, defaults to empty string.
	// +optional
	DescriptionTemplate string `json:"descriptionTemplate,omitempty"`

	// UrlTemplate is the commit status target URL in the SCM provider. Same Go templates, variables, timing, and context rules as DescriptionTemplate.
	// Typical uses: approval UI, dashboard, API, or runbook links. Examples: "https://approvals.example.com/{{ .ReportedSha }}", "https://dashboard.example.com/{{ .Environment.Branch }}/status".
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

	// HTTPRequest configures the outbound HTTP call. See HTTPRequestSpec.
	// +required
	HTTPRequest HTTPRequestSpec `json:"httpRequest"`

	// Success defines when the HTTP response counts as success for commit status phase. See SuccessSpec.
	// +required
	Success SuccessSpec `json:"success"`

	// Mode selects polling vs trigger and request scope (context). See ModeSpec.
	// +required
	Mode ModeSpec `json:"mode"`
}

// ModeSpec defines how the WebRequestCommitStatus controller issues HTTP requests.
//
// Exactly one of Polling or Trigger must be set.
//
// Context (the context field below) controls request fan-out and what data is available in templates and trigger expressions:
//
//   - "environments" (default): one HTTP request per environment; each environment has its own phase and status; success.when.expression is evaluated per response and must return a boolean (true → success, false → pending; failure is not expressible).
//
//   - "promotionstrategy": at most one HTTP request per WebRequestCommitStatus resource; CommitStatuses remain one per environment on each environment’s reportOn SHA. success.when.expression runs once on that shared response — see WhenSpec.Expression for boolean vs per-branch object return shapes.
//
// When context is "promotionstrategy", Environment, ReportedSha, and LastSuccessfulSha are not set for the shared HTTP request or for trigger when/output templates; do not reference them in spec.httpRequest (URL, headers, body), descriptionTemplate, urlTemplate, or spec.mode.trigger. Use PromotionStrategy (e.g. status environments) for branch- or SHA-specific values. For description and url templates, {{ .Phase }} is still set per environment when rendering that environment’s CommitStatus.
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

	// Context is "environments" (default) or "promotionstrategy". See the ModeSpec type documentation for behavior, template limits, and success expression rules.
	// +optional
	// +kubebuilder:default=environments
	// +kubebuilder:validation:Enum=environments;promotionstrategy
	Context ContextMode `json:"context,omitempty"`
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
	// When is evaluated against the HTTP response after each request. See WhenSpec.Expression.
	// +required
	When WhenSpec `json:"when"`
}

// WhenSpec holds spec.success.when.expression only (evaluated after the HTTP response).
// Trigger guards use WhenWithOutputSpec under spec.mode.trigger.when, not this type.
type WhenSpec struct {
	// Expression uses github.com/expr-lang/expr against the HTTP response. Variables (also used by spec.mode.trigger.response.output):
	// Response.StatusCode (int), Response.Body (parsed JSON as map[string]any, or raw string if not JSON), Response.Headers (map[string][]string).
	//
	// Evaluation scope follows spec.mode.context; see ModeSpec. Return shape:
	//
	//   - "environments": must return boolean — true → success, false → pending; failure phase is not expressible.
	//
	//   - "promotionstrategy": boolean true/false applies the same phase to all applicable environments (success or pending only), or return an object with optional defaultPhase ("success"|"pending"|"failure", default "pending") and optional environments: [{ branch, phase }, ...] for per-branch phases. Example:
	//     { defaultPhase: "pending", environments: [{ branch: "dev", phase: "success" }, { branch: "staging", phase: "pending" }] }.
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
	//   - Environment (EnvironmentStatus): current environment's status from PromotionStrategy (environments context only; nil in promotionstrategy context)
	//   - Phase (string): current phase — per-environment in environments context; aggregate of all branches in promotionstrategy context (success only if all succeeded, failure if any failed, pending otherwise)
	//   - ReportedSha (string): the SHA being validated (environments context only; empty in promotionstrategy context)
	//   - LastSuccessfulSha (string): last SHA that achieved success for this environment (environments context only; empty in promotionstrategy context)
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
	// status (per-environment under environments[].triggerOutput, or under promotionStrategyContext.triggerOutput when context is promotionstrategy)
	// and is available in the next reconcile as TriggerOutput in when.expression, when.output.expression, and in templates.
	// Use it to track state such as attempt counts, last-seen SHAs, or timestamps.
	//
	// Variables are the same as for Expression (see above). The expression must return a map/object; every key is stored in TriggerOutput.
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
	// Output is evaluated after the HTTP request completes (any status). Response variables are the same as for spec.success.when.expression — see WhenSpec.Expression.
	// The result is stored in status (environments[].responseOutput or promotionStrategyContext.responseOutput) and exposed on the next reconcile as ResponseOutput in trigger expressions and templates.
	// Must return a map/object.
	// +required
	Output OutputSpec `json:"output"`
}

// HTTPRequestSpec defines the HTTP request configuration.
//
// URLTemplate, HeaderTemplates, and BodyTemplate support Go templates. Sprig functions are available except env, expandenv, and getHostByName; urlQueryEscape is also available.
// These fields are rendered with previous-attempt data: they are evaluated before the current HTTP request is made,
// so they never contain the response from the request being built. Use TriggerOutput/ResponseOutput for state from
// the previous run (trigger mode only).
//
// Template variables:
//   - {{ .ReportedSha }}: the commit SHA being reported on (environments context only; empty in promotionstrategy context)
//   - {{ .LastSuccessfulSha }}: last SHA that achieved success (environments context only; empty in promotionstrategy context)
//   - {{ .Phase }}: phase from previous reconcile (success/pending/failure, defaults to pending); in promotionstrategy context, aggregate of all branches (success only if all succeeded, failure if any failed, pending otherwise)
//   - {{ .PromotionStrategy }}: full PromotionStrategy object
//   - {{ .Environment }}: current environment status from PromotionStrategy (environments context only; nil in promotionstrategy context)
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
	// - SCM: Reuses credentials from the ScmProvider referenced by the PromotionStrategy (no extra secret needed)
	//
	// For Basic, Bearer, OAuth2, and TLS, credentials must be stored in Kubernetes secrets and referenced via secretRef fields.
	// For SCM, credentials are obtained automatically from the SCM provider; just set scm: {}.
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
	//   # SCM Provider Credentials
	//   authentication:
	//     scm: {}
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
	// At most one request is made per WebRequestCommitStatus; phase(s) are reported on each environment's CommitStatus.
	// +optional
	PromotionStrategyContext *WebRequestCommitStatusPromotionStrategyContextStatus `json:"promotionStrategyContext,omitempty"`

	// Conditions represent the latest available observations of an object's state
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// WebRequestCommitStatusPhasePerBranchItem is one branch's resolved phase in promotionstrategy context status.
type WebRequestCommitStatusPhasePerBranchItem struct {
	// Branch is the environment branch name (merge key for phasePerBranch list).
	// +required
	// +kubebuilder:validation:MinLength=1
	Branch string `json:"branch"`
	// Phase is "pending", "success", or "failure".
	// +required
	// +kubebuilder:validation:Enum=pending;success;failure
	Phase CommitStatusPhase `json:"phase"`
}

// WebRequestCommitStatusLastSuccessfulShaItem records the last successful SHA for one branch in promotionstrategy context status.
type WebRequestCommitStatusLastSuccessfulShaItem struct {
	// Branch is the environment branch name (merge key for lastSuccessfulShas list).
	// +required
	// +kubebuilder:validation:MinLength=1
	Branch string `json:"branch"`
	// LastSuccessfulSha is the SHA that last achieved success for this branch.
	// +optional
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^([a-f0-9]{40}|[a-f0-9]{64})?$`
	LastSuccessfulSha string `json:"lastSuccessfulSha,omitempty"`
}

// WebRequestCommitStatusPromotionStrategyContextStatus holds the observed state for context=promotionstrategy (at most one request per WebRequestCommitStatus).
type WebRequestCommitStatusPromotionStrategyContextStatus struct {
	// PhasePerBranch holds the resolved phase for each applicable branch.
	// Every applicable branch is always present after reconciliation.
	// +listType=map
	// +listMapKey=branch
	// +optional
	PhasePerBranch []WebRequestCommitStatusPhasePerBranchItem `json:"phasePerBranch,omitempty"`

	// LastRequestTime is when the last HTTP request was made.
	// +optional
	LastRequestTime *metav1.Time `json:"lastRequestTime,omitempty"`

	// LastResponseStatusCode is the HTTP status code from the last request.
	// +optional
	LastResponseStatusCode *int `json:"lastResponseStatusCode,omitempty"`

	// TriggerOutput: same semantics as WebRequestCommitStatusEnvironmentStatus.TriggerOutput, one shared map for context=promotionstrategy.
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	TriggerOutput *apiextensionsv1.JSON `json:"triggerOutput,omitempty"`

	// ResponseOutput: same semantics as WebRequestCommitStatusEnvironmentStatus.ResponseOutput, one shared map for context=promotionstrategy.
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	ResponseOutput *apiextensionsv1.JSON `json:"responseOutput,omitempty"`

	// LastSuccessfulShas tracks the last SHA that achieved success for each branch.
	// Used with reportOn "proposed" + polling to skip HTTP requests when all environments
	// have already succeeded for their current SHAs.
	// +listType=map
	// +listMapKey=branch
	// +optional
	LastSuccessfulShas []WebRequestCommitStatusLastSuccessfulShaItem `json:"lastSuccessfulShas,omitempty"`
}

// WebRequestCommitStatusEnvironmentStatus defines the observed status for a specific environment.
//
// TriggerOutput and ResponseOutput hold JSON maps written by trigger mode: when.output.expression and response.output.expression respectively.
// They are surfaced on the next reconcile as TriggerOutput and ResponseOutput in expr and templates (see WhenWithOutputSpec and HTTPRequestSpec).
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
	Phase CommitStatusPhase `json:"phase"`

	// LastRequestTime is when the last HTTP request was made.
	// +optional
	LastRequestTime *metav1.Time `json:"lastRequestTime,omitempty"`

	// LastResponseStatusCode is the HTTP status code from the last request.
	// +optional
	LastResponseStatusCode *int `json:"lastResponseStatusCode,omitempty"`

	// TriggerOutput is the map from spec.mode.trigger.when.output.expression (arbitrary JSON keys).
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	TriggerOutput *apiextensionsv1.JSON `json:"triggerOutput,omitempty"`

	// ResponseOutput is the map from spec.mode.trigger.response.output.expression when spec.mode.trigger.response is set; otherwise nil.
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
