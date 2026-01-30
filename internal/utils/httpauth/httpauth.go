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

// Package httpauth provides HTTP authentication utilities for making authenticated
// requests to external APIs. It supports multiple authentication methods including
// Basic Auth, Bearer Token, OAuth2 Client Credentials, and mutual TLS (mTLS).
//
// This package is designed to be used by controllers that need to make authenticated
// HTTP requests to external services, such as approval systems, change management
// platforms, or custom validation endpoints.
//
// # Secret Formats
//
// Each authentication method expects secrets in a standardized format with fixed keys:
//
// Basic Auth secret (keys: "username", "password"):
//
//	apiVersion: v1
//	kind: Secret
//	metadata:
//	  name: basic-auth-creds
//	type: Opaque
//	stringData:
//	  username: myuser
//	  password: mypassword
//
// Bearer Token secret (key: "token"):
//
//	apiVersion: v1
//	kind: Secret
//	metadata:
//	  name: api-token
//	type: Opaque
//	stringData:
//	  token: your-api-token
//
// OAuth2 Client Credentials secret (keys: "clientID", "clientSecret"):
//
//	apiVersion: v1
//	kind: Secret
//	metadata:
//	  name: oauth-creds
//	type: Opaque
//	stringData:
//	  clientID: your-client-id
//	  clientSecret: your-client-secret
//
// TLS Client Certificate secret (keys: "tls.crt", "tls.key", optional "ca.crt"):
//
//	apiVersion: v1
//	kind: Secret
//	metadata:
//	  name: client-cert
//	type: kubernetes.io/tls
//	data:
//	  tls.crt: <base64-encoded-cert>
//	  tls.key: <base64-encoded-key>
//	  ca.crt: <base64-encoded-ca>  # optional
package httpauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// Secret keys for Basic Auth
	UsernameKey = "username"
	PasswordKey = "password"

	// Secret key for Bearer Auth
	TokenKey = "token"

	// Secret keys for OAuth2 Auth
	ClientIDKey     = "clientID"
	ClientSecretKey = "clientSecret"

	// Secret keys for TLS Auth
	TLSCertKey = "tls.crt"
	TLSKeyKey  = "tls.key"
	TLSCAKey   = "ca.crt"
)

// OAuth2Config contains configuration for OAuth2 client credentials authentication.
type OAuth2Config struct {
	// SecretName is the name of the secret containing client credentials.
	SecretName string
	// TokenURL is the OAuth2 token endpoint.
	TokenURL string
	// Scopes are the OAuth2 scopes to request.
	Scopes []string
}

// ApplyBasicAuth applies HTTP Basic Authentication to an HTTP request.
//
// It reads the username and password from the provided secret using the
// standard keys "username" and "password", then sets the Authorization header.
//
// Parameters:
//   - ctx: Context for logging
//   - secret: The Kubernetes secret containing username and password
//   - req: The HTTP request to authenticate
//
// Returns an error if the required keys are not found in the secret.
func ApplyBasicAuth(ctx context.Context, secret *corev1.Secret, req *http.Request) error {
	logger := log.FromContext(ctx)

	username, err := GetSecretValue(secret, UsernameKey)
	if err != nil {
		return fmt.Errorf("failed to get username from secret: %w", err)
	}

	password, err := GetSecretValue(secret, PasswordKey)
	if err != nil {
		return fmt.Errorf("failed to get password from secret: %w", err)
	}

	credentials := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	req.Header.Set("Authorization", "Basic "+credentials)

	logger.V(4).Info("Applied Basic authentication")
	return nil
}

// ApplyBearerAuth applies Bearer token authentication to an HTTP request.
//
// It reads the token from the provided secret using the standard key "token"
// and sets the Authorization header.
//
// Parameters:
//   - ctx: Context for logging
//   - secret: The Kubernetes secret containing the bearer token
//   - req: The HTTP request to authenticate
//
// Returns an error if the token key is not found in the secret.
func ApplyBearerAuth(ctx context.Context, secret *corev1.Secret, req *http.Request) error {
	logger := log.FromContext(ctx)

	token, err := GetSecretValue(secret, TokenKey)
	if err != nil {
		return fmt.Errorf("failed to get token from secret: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token)

	logger.V(4).Info("Applied Bearer authentication")
	return nil
}

// ApplyOAuth2Auth applies OAuth2 client credentials authentication to an HTTP request.
//
// It reads the client ID and secret from the provided secret using the standard
// keys "clientID" and "clientSecret", exchanges them for an access token at the
// specified token URL, and sets the Authorization header.
// The oauth2 library handles token caching and refresh automatically.
//
// Parameters:
//   - ctx: Context for the OAuth2 token request and logging
//   - secret: The Kubernetes secret containing client credentials
//   - config: Configuration specifying token URL and scopes
//   - req: The HTTP request to authenticate
//
// Returns an error if credentials cannot be read or token exchange fails.
func ApplyOAuth2Auth(ctx context.Context, secret *corev1.Secret, config *OAuth2Config, req *http.Request) error {
	logger := log.FromContext(ctx)

	if config == nil {
		return fmt.Errorf("OAuth2 config is required")
	}

	clientID, err := GetSecretValue(secret, ClientIDKey)
	if err != nil {
		return fmt.Errorf("failed to get client ID from secret: %w", err)
	}

	clientSecret, err := GetSecretValue(secret, ClientSecretKey)
	if err != nil {
		return fmt.Errorf("failed to get client secret from secret: %w", err)
	}

	// Create OAuth2 config for client credentials flow
	oauthConfig := &clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     config.TokenURL,
		Scopes:       config.Scopes,
	}

	// Get token using client credentials flow
	// The oauth2 library handles token caching and refresh automatically
	tokenSource := oauthConfig.TokenSource(ctx)
	token, err := tokenSource.Token()
	if err != nil {
		return fmt.Errorf("failed to obtain OAuth2 access token: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+token.AccessToken)

	logger.V(4).Info("Applied OAuth2 authentication", "tokenExpiry", token.Expiry)
	return nil
}

// BuildTLSClient creates an HTTP client with mutual TLS (mTLS) authentication.
//
// It reads the client certificate and private key from the provided secret using
// the standard keys "tls.crt" and "tls.key". Optionally loads CA certificate
// from "ca.crt" if present.
//
// Parameters:
//   - ctx: Context for logging
//   - secret: The Kubernetes secret containing TLS credentials
//   - timeout: HTTP client timeout
//
// Returns an HTTP client configured for mTLS, or an error if TLS setup fails.
func BuildTLSClient(ctx context.Context, secret *corev1.Secret, timeout time.Duration) (*http.Client, error) {
	logger := log.FromContext(ctx)

	certPEM, err := GetSecretValue(secret, TLSCertKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get certificate from secret: %w", err)
	}

	keyPEM, err := GetSecretValue(secret, TLSKeyKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get private key from secret: %w", err)
	}

	// Load the client certificate
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate: %w", err)
	}

	// Create TLS config
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	// Optionally load CA certificate
	if caPEM, exists := secret.Data[TLSCAKey]; exists && len(caPEM) > 0 {
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = caCertPool
	}

	// Create HTTP client with TLS config
	httpClient := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig: tlsConfig,
		},
	}

	logger.V(4).Info("Built TLS authenticated HTTP client")
	return httpClient, nil
}

// GetSecretValue retrieves a string value from a Kubernetes secret's data.
//
// Parameters:
//   - secret: The Kubernetes secret to read from
//   - key: The key to retrieve
//
// Returns the value as a string, or an error if the key is not found.
func GetSecretValue(secret *corev1.Secret, key string) (string, error) {
	value, exists := secret.Data[key]
	if !exists {
		return "", fmt.Errorf("key %q not found in secret %q", key, secret.Name)
	}
	return string(value), nil
}

// CreateOAuth2TokenSource creates a token source for OAuth2 client credentials flow.
// This can be used for more advanced OAuth2 scenarios where you need direct access
// to the token source.
//
// Parameters:
//   - ctx: Context for the OAuth2 requests
//   - tokenURL: The OAuth2 token endpoint
//   - clientID: The client ID
//   - clientSecret: The client secret
//   - scopes: OAuth2 scopes to request
//
// Returns an oauth2.TokenSource that handles token caching and refresh.
func CreateOAuth2TokenSource(ctx context.Context, tokenURL, clientID, clientSecret string, scopes []string) oauth2.TokenSource {
	config := &clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     tokenURL,
		Scopes:       scopes,
	}
	return config.TokenSource(ctx)
}

// -----------------------------------------------------------------------------
// Convenience functions that combine secret fetching with authentication
// -----------------------------------------------------------------------------

// ApplyBasicAuthFromSecret fetches a secret and applies HTTP Basic Authentication to a request.
//
// This is a convenience function that combines secret fetching with authentication.
// The secret must contain "username" and "password" keys.
//
// Parameters:
//   - ctx: Context for the request
//   - reader: Kubernetes client for reading secrets
//   - namespace: Namespace of the secret
//   - secretName: Name of the secret
//   - req: The HTTP request to authenticate
//
// Returns an error if the secret cannot be read or authentication fails.
func ApplyBasicAuthFromSecret(ctx context.Context, reader client.Reader, namespace, secretName string, req *http.Request) error {
	secret, err := getSecret(ctx, reader, namespace, secretName)
	if err != nil {
		return fmt.Errorf("failed to get basic auth secret %q: %w", secretName, err)
	}

	return ApplyBasicAuth(ctx, secret, req)
}

// ApplyBearerAuthFromSecret fetches a secret and applies Bearer token authentication to a request.
//
// This is a convenience function that combines secret fetching with authentication.
// The secret must contain a "token" key.
//
// Parameters:
//   - ctx: Context for the request
//   - reader: Kubernetes client for reading secrets
//   - namespace: Namespace of the secret
//   - secretName: Name of the secret
//   - req: The HTTP request to authenticate
//
// Returns an error if the secret cannot be read or authentication fails.
func ApplyBearerAuthFromSecret(ctx context.Context, reader client.Reader, namespace, secretName string, req *http.Request) error {
	secret, err := getSecret(ctx, reader, namespace, secretName)
	if err != nil {
		return fmt.Errorf("failed to get bearer auth secret %q: %w", secretName, err)
	}

	return ApplyBearerAuth(ctx, secret, req)
}

// ApplyOAuth2AuthFromSecret fetches a secret and applies OAuth2 client credentials authentication to a request.
//
// This is a convenience function that combines secret fetching with authentication.
// The secret must contain "clientID" and "clientSecret" keys.
//
// Parameters:
//   - ctx: Context for the request and OAuth2 token exchange
//   - reader: Kubernetes client for reading secrets
//   - namespace: Namespace of the secret
//   - config: Configuration with secret name, token URL, and scopes (required)
//   - req: The HTTP request to authenticate
//
// Returns an error if the secret cannot be read, token exchange fails, or authentication fails.
func ApplyOAuth2AuthFromSecret(ctx context.Context, reader client.Reader, namespace string, config *OAuth2Config, req *http.Request) error {
	if config == nil {
		return fmt.Errorf("OAuth2 config is required")
	}

	secret, err := getSecret(ctx, reader, namespace, config.SecretName)
	if err != nil {
		return fmt.Errorf("failed to get OAuth2 secret %q: %w", config.SecretName, err)
	}

	return ApplyOAuth2Auth(ctx, secret, config, req)
}

// BuildTLSClientFromSecret fetches a secret and creates an HTTP client with mutual TLS authentication.
//
// This is a convenience function that combines secret fetching with TLS client creation.
// The secret must contain "tls.crt" and "tls.key" keys, and optionally "ca.crt".
//
// Parameters:
//   - ctx: Context for the request
//   - reader: Kubernetes client for reading secrets
//   - namespace: Namespace of the secret
//   - secretName: Name of the secret
//   - timeout: HTTP client timeout
//
// Returns an HTTP client configured for mTLS, or an error if setup fails.
func BuildTLSClientFromSecret(ctx context.Context, reader client.Reader, namespace, secretName string, timeout time.Duration) (*http.Client, error) {
	secret, err := getSecret(ctx, reader, namespace, secretName)
	if err != nil {
		return nil, fmt.Errorf("failed to get TLS secret %q: %w", secretName, err)
	}

	return BuildTLSClient(ctx, secret, timeout)
}

// getSecret retrieves a secret from Kubernetes.
func getSecret(ctx context.Context, reader client.Reader, namespace, name string) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, secret); err != nil {
		return nil, err
	}
	return secret, nil
}
