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
	"net/http"
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

func TestHTTPClient(t *testing.T) {
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
		namespace = "test-namespace"

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
		req, err := http.NewRequest("GET", "http://example.com", nil)
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
		req, err := http.NewRequest("GET", "http://example.com", nil)
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
		req, err := http.NewRequest("GET", "http://example.com", nil)
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

		req, err := http.NewRequest("GET", "http://example.com", nil)
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
		namespace = "test-namespace"

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

		req, err := http.NewRequest("GET", "http://example.com", nil)
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

		req, err := http.NewRequest("GET", "http://example.com", nil)
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

		req, err := http.NewRequest("GET", "http://example.com", nil)
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
		namespace = "test-namespace"

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

		req, err := http.NewRequest("GET", "http://example.com", nil)
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

		req, err := http.NewRequest("GET", "http://example.com", nil)
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
		req, err := http.NewRequest("GET", "http://example.com", nil)
		Expect(err).NotTo(HaveOccurred())

		// Empty auth should not error and should not set any headers
		auth := &promoterv1alpha1.HttpAuthentication{}
		err = httpclient.ApplyAuth(ctx, k8sClient, req, auth, namespace)
		Expect(err).NotTo(HaveOccurred())
		Expect(req.Header.Get("Authorization")).To(BeEmpty())
	})
})
