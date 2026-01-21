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

package httpclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"golang.org/x/oauth2/clientcredentials"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// BuildTLSConfig builds TLS configuration from secrets.
func BuildTLSConfig(ctx context.Context, k8sClient client.Client, namespace string, auth *promoterv1alpha1.TLSAuth) (*tls.Config, error) {
	certKey := auth.SecretRef.CertKey
	if certKey == "" {
		certKey = "tls.crt"
	}
	keyKey := auth.SecretRef.KeyKey
	if keyKey == "" {
		keyKey = "tls.key"
	}
	caKey := auth.SecretRef.CAKey
	if caKey == "" {
		caKey = "ca.crt"
	}

	var secret corev1.Secret
	if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: auth.SecretRef.Name}, &secret); err != nil {
		return nil, fmt.Errorf("failed to get secret %q: %w", auth.SecretRef.Name, err)
	}

	// Get certificate and key
	certPEM := secret.Data[certKey]
	keyPEM := secret.Data[keyKey]
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		return nil, fmt.Errorf("TLS secret must contain %q and %q keys", certKey, keyKey)
	}

	// Parse certificate
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}

	// Optionally add CA certificate
	caPEM := secret.Data[caKey]
	if len(caPEM) > 0 {
		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caPEM) {
			return nil, errors.New("failed to parse CA certificate")
		}
		tlsConfig.RootCAs = caCertPool
	}

	return tlsConfig, nil
}

// ApplyAuth applies authentication to the request based on the Authentication configuration.
func ApplyAuth(ctx context.Context, k8sClient client.Client, req *http.Request, auth *promoterv1alpha1.HttpAuthentication, namespace string) error {
	if auth.Basic != nil {
		return ApplyBasicAuth(ctx, k8sClient, req, auth.Basic, namespace)
	}
	if auth.Bearer != nil {
		return ApplyBearerAuth(ctx, k8sClient, req, auth.Bearer, namespace)
	}
	if auth.OAuth2 != nil {
		return ApplyOAuth2Auth(ctx, k8sClient, req, auth.OAuth2, namespace)
	}
	// TLS auth is handled separately via BuildTLSConfig
	return nil
}

// ApplyBasicAuth applies HTTP Basic Authentication.
// The secret must contain keys "username" and "password".
func ApplyBasicAuth(ctx context.Context, k8sClient client.Client, req *http.Request, auth *promoterv1alpha1.BasicAuth, namespace string) error {
	var secret corev1.Secret
	if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: auth.SecretRef.Name}, &secret); err != nil {
		return fmt.Errorf("failed to get secret %q: %w", auth.SecretRef.Name, err)
	}

	username := string(secret.Data["username"])
	password := string(secret.Data["password"])
	if username == "" || password == "" {
		return fmt.Errorf("basic auth secret must contain \"username\" and \"password\" keys")
	}

	credentials := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	req.Header.Set("Authorization", "Basic "+credentials)
	return nil
}

// ApplyBearerAuth applies Bearer token authentication.
// The secret must contain a key "token".
func ApplyBearerAuth(ctx context.Context, k8sClient client.Client, req *http.Request, auth *promoterv1alpha1.BearerAuth, namespace string) error {
	var secret corev1.Secret
	if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: auth.SecretRef.Name}, &secret); err != nil {
		return fmt.Errorf("failed to get secret %q: %w", auth.SecretRef.Name, err)
	}

	token := string(secret.Data["token"])
	if token == "" {
		return fmt.Errorf("bearer auth secret must contain \"token\" key")
	}

	req.Header.Set("Authorization", "Bearer "+token)
	return nil
}

// ApplyOAuth2Auth applies OAuth2 client credentials authentication.
func ApplyOAuth2Auth(ctx context.Context, k8sClient client.Client, req *http.Request, auth *promoterv1alpha1.OAuth2Auth, namespace string) error {
	clientIDKey := auth.SecretRef.ClientIDKey
	if clientIDKey == "" {
		clientIDKey = "clientID"
	}
	clientSecretKey := auth.SecretRef.ClientSecretKey
	if clientSecretKey == "" {
		clientSecretKey = "clientSecret"
	}

	var secret corev1.Secret
	if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: auth.SecretRef.Name}, &secret); err != nil {
		return fmt.Errorf("failed to get secret %q: %w", auth.SecretRef.Name, err)
	}

	clientID := string(secret.Data[clientIDKey])
	clientSecret := string(secret.Data[clientSecretKey])
	if clientID == "" || clientSecret == "" {
		return fmt.Errorf("OAuth2 auth secret must contain %q and %q keys", clientIDKey, clientSecretKey)
	}

	// Create OAuth2 config
	config := clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     auth.TokenURL,
		Scopes:       auth.Scopes,
	}

	// Get token
	token, err := config.Token(ctx)
	if err != nil {
		return fmt.Errorf("failed to get OAuth2 token: %w", err)
	}

	// Set Authorization header
	token.SetAuthHeader(req)
	return nil
}
