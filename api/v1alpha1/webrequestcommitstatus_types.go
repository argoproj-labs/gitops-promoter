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

	// DescriptionTemplate is a human-readable description of this validation that will be shown in the SCM provider
	// (GitHub, GitLab, etc.) as the commit status description.
	// Supports Go templates with environment-specific variables.
	//
	// Available template variables:
	//   - {{ .Branch }}: the environment branch name (e.g., "environment/staging")
	//   - {{ .ProposedHydratedSha }}: proposed commit SHA for this environment
	//   - {{ .ActiveHydratedSha }}: active/deployed commit SHA for this environment
	//   - {{ .ReportedSha }}: the commit SHA being reported on
	//   - {{ .LastSuccessfulSha }}: last SHA that achieved success for this environment
	//   - {{ .Phase }}: current phase (success/pending/failure)
	//   - {{ .NamespaceMetadata.Labels }}: map of labels from the namespace
	//   - {{ .NamespaceMetadata.Annotations }}: map of annotations from the namespace
	//
	// Examples:
	//   - "External approval for {{ .Branch }}"
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
	//   - "https://dashboard.example.com/{{ .Branch }}/status"
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
//   - {{ .Branch }}: the environment branch name (e.g., "environment/staging")
//   - {{ .ProposedHydratedSha }}: the proposed commit SHA
//   - {{ .ActiveHydratedSha }}: the active/deployed commit SHA
//   - {{ .ReportedSha }}: the commit SHA being reported on (based on reportOn setting)
//   - {{ .LastSuccessfulSha }}: last SHA that achieved success (empty until first success)
//   - {{ .Phase }}: phase from previous reconcile (success/pending/failure, defaults to pending)
//   - {{ .NamespaceMetadata.Labels }}: map of labels from the namespace
//   - {{ .NamespaceMetadata.Annotations }}: map of annotations from the namespace
//
// Example: "https://api.example.com/validate/{{ .Branch }}/{{ .ReportedSha }}"
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
	// The map key is the header name (supports Go templates) and the value is the header value (supports Go templates).
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

// HttpAuthentication defines authentication options for HTTP requests.
//
// Only one authentication method should be specified. If multiple methods are provided,
// the first one found in this order will be used: Basic, Bearer, OAuth2, TLS.
//
// Authentication methods:
//
//  1. Basic Auth - Traditional username/password authentication
//     Applied as: Authorization: Basic <base64(username:password)>
//
//  2. Bearer Token - Token-based authentication
//     Applied as: Authorization: Bearer <token>
//
//  3. OAuth2 - Automatically obtains and refreshes access tokens using client credentials flow
//     Applied as: Authorization: Bearer <access-token>
//
//  4. TLS - Mutual TLS authentication using client certificates
//     Applied at: Transport layer (not as HTTP header)
//
// +kubebuilder:validation:AtMostOneOf=basic;bearer;oauth2;tls
type HttpAuthentication struct {
	// Basic specifies HTTP Basic Authentication.
	// Credentials can be provided inline (with secret references) or via secretRef.
	// +optional
	Basic *BasicAuth `json:"basic,omitempty"`

	// Bearer specifies Bearer token authentication.
	// Token can be provided inline (with secret reference) or via secretRef.
	// +optional
	Bearer *BearerAuth `json:"bearer,omitempty"`

	// OAuth2 specifies OAuth2 client credentials authentication.
	// The controller will automatically obtain access tokens from the specified tokenURL.
	// +optional
	OAuth2 *OAuth2Auth `json:"oauth2,omitempty"`

	// TLS specifies TLS client certificate authentication (mutual TLS).
	// Requires a secret containing the client certificate and private key.
	// +optional
	TLS *TLSAuth `json:"tls,omitempty"`
}

// BasicAuth defines HTTP Basic Authentication.
//
// HTTP Basic Auth encodes the username and password as base64 and sends them
// in the Authorization header: "Authorization: Basic <base64(username:password)>"
//
// Credentials must be stored in a Kubernetes secret and referenced via secretRef:
//
//	basic:
//	  secretRef:
//	    name: my-basic-auth-secret  # secret must contain keys "username" and "password"
type BasicAuth struct {
	// SecretRef references a secret containing username and password.
	// +required
	SecretRef BasicAuthSecretRef `json:"secretRef"`
}

// BasicAuthSecretRef references a secret for basic authentication.
// The secret must contain keys "username" and "password".
type BasicAuthSecretRef struct {
	// Name of the secret.
	// +required
	Name string `json:"name"`
}

// BearerAuth defines Bearer token authentication.
//
// Bearer tokens are commonly used for API authentication. The token is sent
// in the Authorization header: "Authorization: Bearer <token>"
//
// Common use cases:
// - API keys
// - JWT tokens
// - Personal access tokens
// - Static authentication tokens
//
// The token must be stored in a Kubernetes secret and referenced via secretRef:
//
//	bearer:
//	  secretRef:
//	    name: my-bearer-token-secret  # secret must contain key "token"
type BearerAuth struct {
	// SecretRef references a secret containing the bearer token.
	// +required
	SecretRef BearerAuthSecretRef `json:"secretRef"`
}

// BearerAuthSecretRef references a secret for bearer token authentication.
// The secret must contain a key "token".
type BearerAuthSecretRef struct {
	// Name of the secret.
	// +required
	Name string `json:"name"`
}

// OAuth2Auth defines OAuth2 client credentials authentication.
//
// The OAuth2 client credentials flow is used for server-to-server authentication.
// The controller automatically:
// 1. Requests an access token from the tokenURL using client credentials
// 2. Caches the token until it expires
// 3. Automatically refreshes the token when needed
// 4. Adds the token to requests as: "Authorization: Bearer <access-token>"
//
// This is ideal for:
// - Service-to-service authentication
// - APIs that require OAuth2 but don't involve user interaction
// - Systems that provide machine credentials (client ID/secret)
//
// Example:
//
//	oauth2:
//	  tokenURL: "https://auth.example.com/oauth/token"
//	  scopes: ["read:api", "write:api"]
//	  secretRef:
//	    name: oauth-creds
//
// Note: This uses the OAuth2 client credentials grant type (RFC 6749 Section 4.4).
// It does NOT support authorization code flow or user-interactive flows.
type OAuth2Auth struct {
	// TokenURL is the OAuth2 token endpoint where access tokens are obtained.
	// Example: "https://auth.example.com/oauth/token"
	// +required
	TokenURL string `json:"tokenURL"`

	// Scopes to request from the OAuth2 provider.
	// Optional - some providers don't require scopes for client credentials.
	// Example: ["read:api", "write:api"]
	// +optional
	Scopes []string `json:"scopes,omitempty"`

	// SecretRef references a secret containing clientID and clientSecret.
	// +required
	SecretRef OAuth2AuthSecretRef `json:"secretRef"`
}

// OAuth2AuthSecretRef references a secret for OAuth2 authentication.
type OAuth2AuthSecretRef struct {
	// Name of the secret.
	// +required
	Name string `json:"name"`

	// ClientIDKey is the key in the secret containing the client ID.
	// Defaults to "clientID".
	// +optional
	ClientIDKey string `json:"clientIDKey,omitempty"`

	// ClientSecretKey is the key in the secret containing the client secret.
	// Defaults to "clientSecret".
	// +optional
	ClientSecretKey string `json:"clientSecretKey,omitempty"`
}

// TLSAuth defines TLS client certificate authentication (mutual TLS / mTLS).
//
// Mutual TLS authentication proves the client's identity using a certificate,
// rather than a password or token. This is configured at the TLS transport layer,
// not as an HTTP header.
//
// Use cases:
// - High-security environments requiring certificate-based authentication
// - APIs that require client certificates for access
// - Service mesh authentication
// - Zero-trust network architectures
//
// The secret must contain:
// - Client certificate (default key: "tls.crt")
// - Private key (default key: "tls.key")
// - Optional CA certificate for custom CAs (default key: "ca.crt")
//
// Example:
//
//	tls:
//	  secretRef:
//	    name: my-client-cert
//	    certKey: tls.crt  # optional, defaults to "tls.crt"
//	    keyKey: tls.key   # optional, defaults to "tls.key"
//	    caKey: ca.crt     # optional, defaults to "ca.crt"
//
// Note: TLS auth is applied at the HTTP client transport layer, unlike other
// authentication methods which are applied as HTTP headers.
type TLSAuth struct {
	// SecretRef references a secret containing TLS certificate and key.
	// The secret should be of type kubernetes.io/tls or contain the required keys.
	// +required
	SecretRef TLSAuthSecretRef `json:"secretRef"`
}

// TLSAuthSecretRef references a secret for TLS client certificate authentication.
type TLSAuthSecretRef struct {
	// Name of the secret.
	// +required
	Name string `json:"name"`

	// CertKey is the key in the secret containing the client certificate.
	// Defaults to "tls.crt".
	// +optional
	CertKey string `json:"certKey,omitempty"`

	// KeyKey is the key in the secret containing the private key.
	// Defaults to "tls.key".
	// +optional
	KeyKey string `json:"keyKey,omitempty"`

	// CAKey is the key in the secret containing the CA certificate (optional).
	// Defaults to "ca.crt".
	// +optional
	CAKey string `json:"caKey,omitempty"`
}

// WebRequestCommitStatusStatus defines the observed state of WebRequestCommitStatus.
type WebRequestCommitStatusStatus struct {
	// Environments holds the validation results for each environment where this validation applies.
	// Each entry corresponds to an environment from the PromotionStrategy where the Key matches
	// either global or environment-specific proposedCommitStatuses/activeCommitStatuses.
	// +listType=map
	// +listMapKey=branch
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
	// Branch is the environment branch name being validated.
	// +required
	Branch string `json:"branch"`

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
