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

package httpclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils/httpclient"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testNamespace = "test-namespace"

func TestHTTPClient(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)
	RunSpecs(t, "HTTPClient Auth Suite")
}

var _ = Describe("ApplyBasicAuth", func() {
	var (
		ctx       context.Context
		k8sClient client.Client
		scheme    *runtime.Scheme
		namespace string
	)

	BeforeEach(func() {
		ctx = context.Background()
		namespace = testNamespace

		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		k8sClient = fake.NewClientBuilder().
			WithScheme(scheme).
			Build()
	})

	It("should apply basic auth with default keys", func() {
		// Create a secret with default keys
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"username": []byte("testuser"),
				"password": []byte("testpass"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		// Create a request
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		// Apply basic auth
		auth := &promoterv1alpha1.BasicAuth{
			SecretRef: promoterv1alpha1.BasicAuthSecretRef{
				Name: "test-secret",
			},
		}
		err = httpclient.ApplyBasicAuth(ctx, k8sClient, req, auth, namespace)
		Expect(err).NotTo(HaveOccurred())

		// Verify Authorization header was set
		authHeader := req.Header.Get("Authorization")
		Expect(authHeader).To(HavePrefix("Basic "))
	})

	It("should apply basic auth with custom keys", func() {
		// Create a secret with custom keys
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"custom-user": []byte("testuser"),
				"custom-pass": []byte("testpass"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		// Create a request
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		// Apply basic auth with custom keys
		auth := &promoterv1alpha1.BasicAuth{
			SecretRef: promoterv1alpha1.BasicAuthSecretRef{
				Name:        "test-secret",
				UsernameKey: "custom-user",
				PasswordKey: "custom-pass",
			},
		}
		err = httpclient.ApplyBasicAuth(ctx, k8sClient, req, auth, namespace)
		Expect(err).NotTo(HaveOccurred())

		// Verify Authorization header was set
		authHeader := req.Header.Get("Authorization")
		Expect(authHeader).To(HavePrefix("Basic "))
	})

	It("should return error when secret is missing", func() {
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		auth := &promoterv1alpha1.BasicAuth{
			SecretRef: promoterv1alpha1.BasicAuthSecretRef{
				Name: "missing-secret",
			},
		}
		err = httpclient.ApplyBasicAuth(ctx, k8sClient, req, auth, namespace)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to get secret"))
	})

	It("should return error when username is empty", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"password": []byte("testpass"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		auth := &promoterv1alpha1.BasicAuth{
			SecretRef: promoterv1alpha1.BasicAuthSecretRef{
				Name: "test-secret",
			},
		}
		err = httpclient.ApplyBasicAuth(ctx, k8sClient, req, auth, namespace)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("must contain"))
	})
})

var _ = Describe("ApplyBearerAuth", func() {
	var (
		ctx       context.Context
		k8sClient client.Client
		scheme    *runtime.Scheme
		namespace string
	)

	BeforeEach(func() {
		ctx = context.Background()
		namespace = testNamespace

		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		k8sClient = fake.NewClientBuilder().
			WithScheme(scheme).
			Build()
	})

	It("should apply bearer auth with default key", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"token": []byte("test-token-123"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		auth := &promoterv1alpha1.BearerAuth{
			SecretRef: promoterv1alpha1.BearerAuthSecretRef{
				Name: "test-secret",
			},
		}
		err = httpclient.ApplyBearerAuth(ctx, k8sClient, req, auth, namespace)
		Expect(err).NotTo(HaveOccurred())

		authHeader := req.Header.Get("Authorization")
		Expect(authHeader).To(Equal("Bearer test-token-123"))
	})

	It("should apply bearer auth with custom key", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"custom-token": []byte("test-token-456"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		auth := &promoterv1alpha1.BearerAuth{
			SecretRef: promoterv1alpha1.BearerAuthSecretRef{
				Name: "test-secret",
				Key:  "custom-token",
			},
		}
		err = httpclient.ApplyBearerAuth(ctx, k8sClient, req, auth, namespace)
		Expect(err).NotTo(HaveOccurred())

		authHeader := req.Header.Get("Authorization")
		Expect(authHeader).To(Equal("Bearer test-token-456"))
	})

	It("should return error when token is empty", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"other-key": []byte("value"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		auth := &promoterv1alpha1.BearerAuth{
			SecretRef: promoterv1alpha1.BearerAuthSecretRef{
				Name: "test-secret",
			},
		}
		err = httpclient.ApplyBearerAuth(ctx, k8sClient, req, auth, namespace)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("must contain"))
	})
})

var _ = Describe("ApplyAuth", func() {
	var (
		ctx       context.Context
		k8sClient client.Client
		scheme    *runtime.Scheme
		namespace string
	)

	BeforeEach(func() {
		ctx = context.Background()
		namespace = testNamespace

		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		k8sClient = fake.NewClientBuilder().
			WithScheme(scheme).
			Build()
	})

	It("should apply basic auth when specified", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"username": []byte("user"),
				"password": []byte("pass"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		auth := &promoterv1alpha1.HttpAuthentication{
			Basic: &promoterv1alpha1.BasicAuth{
				SecretRef: promoterv1alpha1.BasicAuthSecretRef{
					Name: "test-secret",
				},
			},
		}
		err = httpclient.ApplyAuth(ctx, k8sClient, req, auth, namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Header.Get("Authorization")).To(HavePrefix("Basic "))
	})

	It("should apply bearer auth when specified", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"token": []byte("test-token"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		auth := &promoterv1alpha1.HttpAuthentication{
			Bearer: &promoterv1alpha1.BearerAuth{
				SecretRef: promoterv1alpha1.BearerAuthSecretRef{
					Name: "test-secret",
				},
			},
		}
		err = httpclient.ApplyAuth(ctx, k8sClient, req, auth, namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Header.Get("Authorization")).To(Equal("Bearer test-token"))
	})

	It("should handle nil auth without error", func() {
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		// Empty auth should not error and should not set any headers
		auth := &promoterv1alpha1.HttpAuthentication{}
		err = httpclient.ApplyAuth(ctx, k8sClient, req, auth, namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Header.Get("Authorization")).To(BeEmpty())
	})
})

var _ = Describe("ApplyOAuth2Auth", func() {
	var (
		ctx         context.Context
		k8sClient   client.Client
		scheme      *runtime.Scheme
		namespace   string
		mockServer  *httptest.Server
		tokenCalled bool
	)

	BeforeEach(func() {
		ctx = context.Background()
		namespace = testNamespace
		tokenCalled = false

		scheme = runtime.NewScheme()
		Expect(corev1.AddToScheme(scheme)).To(Succeed())

		k8sClient = fake.NewClientBuilder().
			WithScheme(scheme).
			Build()

		// Create a mock OAuth2 token endpoint
		mockServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenCalled = true

			// Verify it's a token request
			Expect(r.URL.Path).To(Equal("/token"))
			Expect(r.Method).To(Equal(http.MethodPost))
			Expect(r.Header.Get("Content-Type")).To(Equal("application/x-www-form-urlencoded"))

			// Parse form to verify credentials
			err := r.ParseForm()
			Expect(err).NotTo(HaveOccurred())

			// Return a mock token response
			response := map[string]any{
				"access_token": "mock-oauth2-token",
				"token_type":   "Bearer",
				"expires_in":   3600,
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			err = json.NewEncoder(w).Encode(response)
			Expect(err).NotTo(HaveOccurred())
		}))
	})

	AfterEach(func() {
		if mockServer != nil {
			mockServer.Close()
		}
	})

	It("should apply OAuth2 auth with default keys", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"clientID":     []byte("test-client-id"),
				"clientSecret": []byte("test-client-secret"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		auth := &promoterv1alpha1.OAuth2Auth{
			SecretRef: promoterv1alpha1.OAuth2AuthSecretRef{
				Name: "test-secret",
			},
			TokenURL: mockServer.URL + "/token",
		}
		err = httpclient.ApplyOAuth2Auth(ctx, k8sClient, req, auth, namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(tokenCalled).To(BeTrue(), "OAuth2 token endpoint should have been called")

		authHeader := req.Header.Get("Authorization")
		Expect(authHeader).To(Equal("Bearer mock-oauth2-token"))
	})

	It("should apply OAuth2 auth with custom keys", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"custom-id":     []byte("custom-client-id"),
				"custom-secret": []byte("custom-client-secret"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		auth := &promoterv1alpha1.OAuth2Auth{
			SecretRef: promoterv1alpha1.OAuth2AuthSecretRef{
				Name:            "test-secret",
				ClientIDKey:     "custom-id",
				ClientSecretKey: "custom-secret",
			},
			TokenURL: mockServer.URL + "/token",
		}
		err = httpclient.ApplyOAuth2Auth(ctx, k8sClient, req, auth, namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(tokenCalled).To(BeTrue())

		authHeader := req.Header.Get("Authorization")
		Expect(authHeader).To(Equal("Bearer mock-oauth2-token"))
	})

	It("should apply OAuth2 auth with scopes", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"clientID":     []byte("test-client-id"),
				"clientSecret": []byte("test-client-secret"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		auth := &promoterv1alpha1.OAuth2Auth{
			SecretRef: promoterv1alpha1.OAuth2AuthSecretRef{
				Name: "test-secret",
			},
			TokenURL: mockServer.URL + "/token",
			Scopes:   []string{"read", "write"},
		}
		err = httpclient.ApplyOAuth2Auth(ctx, k8sClient, req, auth, namespace)
		Expect(err).NotTo(HaveOccurred())

		authHeader := req.Header.Get("Authorization")
		Expect(authHeader).To(Equal("Bearer mock-oauth2-token"))
	})

	It("should return error when secret is missing", func() {
		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		auth := &promoterv1alpha1.OAuth2Auth{
			SecretRef: promoterv1alpha1.OAuth2AuthSecretRef{
				Name: "missing-secret",
			},
			TokenURL: mockServer.URL + "/token",
		}
		err = httpclient.ApplyOAuth2Auth(ctx, k8sClient, req, auth, namespace)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to get secret"))
		Expect(tokenCalled).To(BeFalse(), "OAuth2 token endpoint should not have been called")
	})

	It("should return error when clientID is empty", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"clientSecret": []byte("test-client-secret"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		auth := &promoterv1alpha1.OAuth2Auth{
			SecretRef: promoterv1alpha1.OAuth2AuthSecretRef{
				Name: "test-secret",
			},
			TokenURL: mockServer.URL + "/token",
		}
		err = httpclient.ApplyOAuth2Auth(ctx, k8sClient, req, auth, namespace)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("must contain"))
		Expect(tokenCalled).To(BeFalse())
	})

	It("should return error when clientSecret is empty", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"clientID": []byte("test-client-id"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		auth := &promoterv1alpha1.OAuth2Auth{
			SecretRef: promoterv1alpha1.OAuth2AuthSecretRef{
				Name: "test-secret",
			},
			TokenURL: mockServer.URL + "/token",
		}
		err = httpclient.ApplyOAuth2Auth(ctx, k8sClient, req, auth, namespace)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("must contain"))
		Expect(tokenCalled).To(BeFalse())
	})

	It("should return error when token endpoint fails", func() {
		// Create a server that returns an error
		errorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": "invalid_client"}`))
		}))
		defer errorServer.Close()

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"clientID":     []byte("test-client-id"),
				"clientSecret": []byte("test-client-secret"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		auth := &promoterv1alpha1.OAuth2Auth{
			SecretRef: promoterv1alpha1.OAuth2AuthSecretRef{
				Name: "test-secret",
			},
			TokenURL: errorServer.URL + "/token",
		}
		err = httpclient.ApplyOAuth2Auth(ctx, k8sClient, req, auth, namespace)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to get OAuth2 token"))
	})

	It("should apply OAuth2 auth via ApplyAuth wrapper", func() {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: namespace,
			},
			Data: map[string][]byte{
				"clientID":     []byte("test-client-id"),
				"clientSecret": []byte("test-client-secret"),
			},
		}
		Expect(k8sClient.Create(ctx, secret)).To(Succeed())

		req, err := http.NewRequest(http.MethodGet, "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		auth := &promoterv1alpha1.HttpAuthentication{
			OAuth2: &promoterv1alpha1.OAuth2Auth{
				SecretRef: promoterv1alpha1.OAuth2AuthSecretRef{
					Name: "test-secret",
				},
				TokenURL: mockServer.URL + "/token",
			},
		}
		err = httpclient.ApplyAuth(ctx, k8sClient, req, auth, namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(tokenCalled).To(BeTrue())
		Expect(req.Header.Get("Authorization")).To(Equal("Bearer mock-oauth2-token"))
	})
})
