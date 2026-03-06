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

package httpauth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/internal/utils/httpauth"
)

var _ = Describe("GetSecretValue", func() {
	It("returns the value when the key exists", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "test-secret"},
			Data: map[string][]byte{
				"username": []byte("alice"),
				"password": []byte("secret"),
			},
		}

		val, err := httpauth.GetSecretValue(secret, "username")
		Expect(err).NotTo(HaveOccurred())
		Expect(val).To(Equal("alice"))

		val, err = httpauth.GetSecretValue(secret, "password")
		Expect(err).NotTo(HaveOccurred())
		Expect(val).To(Equal("secret"))
	})

	It("returns an error when the key is missing", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "test-secret"},
			Data:       map[string][]byte{"foo": []byte("bar")},
		}

		_, err := httpauth.GetSecretValue(secret, "missing")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("key \"missing\" not found"))
		Expect(err.Error()).To(ContainSubstring("test-secret"))
	})
})

var _ = Describe("ApplyBasicAuth", func() {
	It("sets the Authorization header with Basic credentials", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "basic-secret"},
			Data: map[string][]byte{
				httpauth.UsernameKey: []byte("user"),
				httpauth.PasswordKey: []byte("pass"),
			},
		}
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)
		ctx := context.Background()

		err := httpauth.ApplyBasicAuth(ctx, secret, req)
		Expect(err).NotTo(HaveOccurred())

		auth := req.Header.Get("Authorization")
		Expect(auth).To(HavePrefix("Basic "))
		Expect(auth).To(Equal("Basic dXNlcjpwYXNz")) // base64("user:pass")
	})

	It("returns an error when username is missing from secret", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "basic-secret"},
			Data:       map[string][]byte{httpauth.PasswordKey: []byte("pass")},
		}
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)

		err := httpauth.ApplyBasicAuth(context.Background(), secret, req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("username"))
	})

	It("returns an error when password is missing from secret", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "basic-secret"},
			Data:       map[string][]byte{httpauth.UsernameKey: []byte("user")},
		}
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)

		err := httpauth.ApplyBasicAuth(context.Background(), secret, req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("password"))
	})
})

var _ = Describe("ApplyBearerAuth", func() {
	It("sets the Authorization header with Bearer token", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "bearer-secret"},
			Data:       map[string][]byte{httpauth.TokenKey: []byte("my-token-123")},
		}
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)

		err := httpauth.ApplyBearerAuth(context.Background(), secret, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Header.Get("Authorization")).To(Equal("Bearer my-token-123"))
	})

	It("returns an error when token key is missing from secret", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "bearer-secret"},
			Data:       map[string][]byte{},
		}
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)

		err := httpauth.ApplyBearerAuth(context.Background(), secret, req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("token"))
	})
})

var _ = Describe("ApplyOAuth2Auth", func() {
	var tokenServer *httptest.Server
	var tokenURL string

	BeforeEach(func() {
		tokenServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// OAuth2 client credentials flow: library may send client_id/client_secret in form body or Basic auth
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "oauth2-access-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		}))
		tokenURL = tokenServer.URL + "/token"
	})

	AfterEach(func() {
		if tokenServer != nil {
			tokenServer.Close()
		}
	})

	It("exchanges client credentials and sets Authorization header", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "oauth-secret"},
			Data: map[string][]byte{
				httpauth.ClientIDKey:     []byte("client-id"),
				httpauth.ClientSecretKey: []byte("client-secret"),
			},
		}
		config := &httpauth.OAuth2Config{
			TokenURL: tokenURL,
			Scopes:   []string{"read", "write"},
		}
		req := httptest.NewRequest(http.MethodGet, "https://api.example.com/", nil)

		err := httpauth.ApplyOAuth2Auth(context.Background(), secret, config, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Header.Get("Authorization")).To(Equal("Bearer oauth2-access-token"))
	})

	It("returns an error when config is nil", func() {
		secret := &corev1.Secret{Data: map[string][]byte{}}
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)

		err := httpauth.ApplyOAuth2Auth(context.Background(), secret, nil, req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("OAuth2 config is required"))
	})

	It("returns an error when clientID is missing from secret", func() {
		secret := &corev1.Secret{
			Data: map[string][]byte{httpauth.ClientSecretKey: []byte("secret")},
		}
		config := &httpauth.OAuth2Config{TokenURL: tokenURL}
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)

		err := httpauth.ApplyOAuth2Auth(context.Background(), secret, config, req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("client ID"))
	})
})

var _ = Describe("BuildTLSClient", func() {
	var certPEM, keyPEM []byte

	BeforeEach(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).NotTo(HaveOccurred())

		template := &x509.Certificate{
			SerialNumber:          big.NewInt(1),
			NotBefore:             time.Now(),
			NotAfter:              time.Now().Add(24 * time.Hour),
			KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
			BasicConstraintsValid: true,
		}
		certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		Expect(err).NotTo(HaveOccurred())

		certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	})

	It("builds an HTTP client with TLS config from secret", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "tls-secret"},
			Data: map[string][]byte{
				httpauth.TLSCertKey: certPEM,
				httpauth.TLSKeyKey:  keyPEM,
			},
		}

		client, err := httpauth.BuildTLSClient(context.Background(), secret, 10*time.Second)
		Expect(err).NotTo(HaveOccurred())
		Expect(client).NotTo(BeNil())
		Expect(client.Transport).NotTo(BeNil())
		Expect(client.Timeout).To(Equal(10 * time.Second))
	})

	It("includes RootCAs when ca.crt is present in secret", func() {
		caCert := certPEM // reuse same cert as CA for test
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "tls-secret"},
			Data: map[string][]byte{
				httpauth.TLSCertKey: certPEM,
				httpauth.TLSKeyKey:  keyPEM,
				httpauth.TLSCAKey:   caCert,
			},
		}

		client, err := httpauth.BuildTLSClient(context.Background(), secret, 5*time.Second)
		Expect(err).NotTo(HaveOccurred())
		Expect(client).NotTo(BeNil())
	})

	It("returns an error when tls.crt is missing", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "tls-secret"},
			Data:       map[string][]byte{httpauth.TLSKeyKey: keyPEM},
		}

		_, err := httpauth.BuildTLSClient(context.Background(), secret, time.Second)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("certificate"))
	})

	It("returns an error when tls.key is missing", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "tls-secret"},
			Data:       map[string][]byte{httpauth.TLSCertKey: certPEM},
		}

		_, err := httpauth.BuildTLSClient(context.Background(), secret, time.Second)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("private key"))
	})
})

var _ = Describe("ApplyBasicAuthFromSecret", func() {
	It("fetches the secret and applies Basic auth to the request", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "basic-creds", Namespace: "default"},
			Data: map[string][]byte{
				httpauth.UsernameKey: []byte("from-secret"),
				httpauth.PasswordKey: []byte("secret-pass"),
			},
		}
		scheme := utils.GetScheme()
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)

		err := httpauth.ApplyBasicAuthFromSecret(context.Background(), k8sClient, "default", "basic-creds", req)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Header.Get("Authorization")).To(Equal("Basic ZnJvbS1zZWNyZXQ6c2VjcmV0LXBhc3M=")) // base64("from-secret:secret-pass")
	})

	It("returns an error when the secret does not exist", func() {
		scheme := utils.GetScheme()
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)

		err := httpauth.ApplyBasicAuthFromSecret(context.Background(), k8sClient, "default", "missing-secret", req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("basic auth secret"))
		Expect(err.Error()).To(ContainSubstring("missing-secret"))
	})
})

var _ = Describe("ApplyBearerAuthFromSecret", func() {
	It("fetches the secret and applies Bearer auth to the request", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "api-token", Namespace: "default"},
			Data:       map[string][]byte{httpauth.TokenKey: []byte("fetched-bearer-token")},
		}
		scheme := utils.GetScheme()
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)

		err := httpauth.ApplyBearerAuthFromSecret(context.Background(), k8sClient, "default", "api-token", req)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Header.Get("Authorization")).To(Equal("Bearer fetched-bearer-token"))
	})

	It("returns an error when the secret does not exist", func() {
		scheme := utils.GetScheme()
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)

		err := httpauth.ApplyBearerAuthFromSecret(context.Background(), k8sClient, "default", "missing", req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("bearer auth secret"))
	})
})

var _ = Describe("ApplyOAuth2AuthFromSecret", func() {
	var tokenServer *httptest.Server

	BeforeEach(func() {
		tokenServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "from-oauth2-secret",
				"token_type":   "Bearer",
				"expires_in":   3600,
			})
		}))
	})

	AfterEach(func() {
		if tokenServer != nil {
			tokenServer.Close()
		}
	})

	It("fetches the secret and applies OAuth2 auth to the request", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "oauth-creds", Namespace: "default"},
			Data: map[string][]byte{
				httpauth.ClientIDKey:     []byte("oid"),
				httpauth.ClientSecretKey: []byte("osecret"),
			},
		}
		scheme := utils.GetScheme()
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()
		config := &httpauth.OAuth2Config{
			SecretName: "oauth-creds",
			TokenURL:   tokenServer.URL + "/oauth/token",
		}
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)

		err := httpauth.ApplyOAuth2AuthFromSecret(context.Background(), k8sClient, "default", config, req)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Header.Get("Authorization")).To(Equal("Bearer from-oauth2-secret"))
	})

	It("returns an error when config is nil", func() {
		scheme := utils.GetScheme()
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)

		err := httpauth.ApplyOAuth2AuthFromSecret(context.Background(), k8sClient, "default", nil, req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("OAuth2 config is required"))
	})

	It("returns an error when the secret does not exist", func() {
		scheme := utils.GetScheme()
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		config := &httpauth.OAuth2Config{SecretName: "missing", TokenURL: "https://token.example.com"}
		req := httptest.NewRequest(http.MethodGet, "https://example.com/", nil)

		err := httpauth.ApplyOAuth2AuthFromSecret(context.Background(), k8sClient, "default", config, req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("OAuth2 secret"))
	})
})

var _ = Describe("BuildTLSClientFromSecret", func() {
	var certPEM, keyPEM []byte

	BeforeEach(func() {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		Expect(err).NotTo(HaveOccurred())
		template := &x509.Certificate{
			SerialNumber: big.NewInt(1),
			NotBefore:    time.Now(),
			NotAfter:     time.Now().Add(24 * time.Hour),
			KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		}
		certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		Expect(err).NotTo(HaveOccurred())
		certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
		keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	})

	It("fetches the secret and builds a TLS client", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "client-cert", Namespace: "default"},
			Data: map[string][]byte{
				httpauth.TLSCertKey: certPEM,
				httpauth.TLSKeyKey:  keyPEM,
			},
		}
		scheme := utils.GetScheme()
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(secret).Build()

		client, err := httpauth.BuildTLSClientFromSecret(context.Background(), k8sClient, "default", "client-cert", 15*time.Second)
		Expect(err).NotTo(HaveOccurred())
		Expect(client).NotTo(BeNil())
		Expect(client.Timeout).To(Equal(15 * time.Second))
	})

	It("returns an error when the secret does not exist", func() {
		scheme := utils.GetScheme()
		k8sClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		_, err := httpauth.BuildTLSClientFromSecret(context.Background(), k8sClient, "default", "missing-tls-secret", time.Second)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("TLS secret"))
		Expect(err.Error()).To(ContainSubstring("missing-tls-secret"))
	})
})
