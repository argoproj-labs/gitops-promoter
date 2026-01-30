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

// HttpAuthentication defines authentication options for HTTP requests.
//
// Only one authentication method should be specified.
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
// The secret must contain keys "clientID" and "clientSecret".
type OAuth2AuthSecretRef struct {
	// Name of the secret.
	// +required
	Name string `json:"name"`
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
// - Client certificate (key: "tls.crt")
// - Private key (key: "tls.key")
// - Optional CA certificate for custom CAs (key: "ca.crt")
//
// Example:
//
//	tls:
//	  secretRef:
//	    name: my-client-cert
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
// The secret must contain keys "tls.crt" and "tls.key", and optionally "ca.crt".
type TLSAuthSecretRef struct {
	// Name of the secret.
	// +required
	Name string `json:"name"`
}
