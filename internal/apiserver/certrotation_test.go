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

package apiserver

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/open-policy-agent/cert-controller/pkg/rotator"
	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	clientrest "k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

var _ = Describe("CertRotationOptions", func() {
	enabledOpts := func() CertRotationOptions {
		o := newCertRotationOptions()
		o.Enabled = true
		o.Namespace = "promoter-system"
		return o
	}

	It("registers flags with the manifest defaults and rotation disabled", func() {
		opts := NewOptions()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		opts.AddFlags(fs)

		Expect(fs.Parse([]string{"--cert-rotation"})).To(Succeed())
		Expect(opts.CertRotation.Enabled).To(BeTrue())
		Expect(opts.CertRotation.SecretName).To(Equal("promoter-apiserver-serving-cert"))
		Expect(opts.CertRotation.ServiceName).To(Equal("promoter-apiserver"))
		Expect(opts.CertRotation.APIServiceName).To(Equal("v1alpha1.view.promoter.argoproj.io"))
		Expect(opts.CertRotation.Namespace).To(BeEmpty())
		Expect(fs.Lookup("cert-rotation-namespace")).To(BeNil())

		Expect(NewOptions().CertRotation.Enabled).To(BeFalse())
	})

	It("skips validation when disabled", func() {
		o := CertRotationOptions{}
		Expect(o.Validate("", "")).To(Succeed())
	})

	It("accepts a cert and key in the same directory", func() {
		o := enabledOpts()
		Expect(o.Validate("/serving-certs/tls.crt", "/serving-certs/tls.key")).To(Succeed())
	})

	It("requires the serving cert files", func() {
		o := enabledOpts()
		Expect(o.Validate("", "")).To(MatchError(ContainSubstring("--tls-cert-file and --tls-private-key-file must be set")))
	})

	It("requires the cert and key to share a directory", func() {
		o := enabledOpts()
		Expect(o.Validate("/a/tls.crt", "/b/tls.key")).To(MatchError(ContainSubstring("same directory")))
	})

	It("requires a namespace", func() {
		o := enabledOpts()
		o.Namespace = ""
		Expect(o.Validate("/serving-certs/tls.crt", "/serving-certs/tls.key")).To(MatchError(ContainSubstring("cert rotation namespace must be set")))
	})

	It("builds a rotator for the service DNS names and APIService", func() {
		o := enabledOpts()
		cr := o.newCertRotator("/serving-certs/tls.crt", "/serving-certs/tls.key")

		Expect(cr.SecretKey).To(Equal(types.NamespacedName{Namespace: "promoter-system", Name: "promoter-apiserver-serving-cert"}))
		Expect(cr.CertDir).To(Equal("/serving-certs"))
		Expect(cr.CertName).To(Equal("tls.crt"))
		Expect(cr.KeyName).To(Equal("tls.key"))
		Expect(cr.DNSName).To(Equal("promoter-apiserver.promoter-system.svc"))
		Expect(cr.ExtraDNSNames).To(ConsistOf(
			"promoter-apiserver.promoter-system",
			"promoter-apiserver.promoter-system.svc.cluster.local",
		))
		Expect(cr.Webhooks).To(ConsistOf(rotator.WebhookInfo{
			Name: "v1alpha1.view.promoter.argoproj.io",
			Type: rotator.APIService,
		}))
		Expect(cr.IsReady).NotTo(BeNil())
	})
})

var _ = Describe("secretScopedConfig", func() {
	// rewritten sends req through the scoped config's transport and returns the request
	// that reached the underlying transport.
	rewritten := func(method, rawURL string) *http.Request {
		var got *http.Request
		cfg := secretScopedConfig(&clientrest.Config{}, "promoter-system", "promoter-apiserver-serving-cert")
		rt := cfg.WrapTransport(roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			got = r
			return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
		}))
		req, err := http.NewRequest(method, rawURL, nil)
		Expect(err).NotTo(HaveOccurred())
		_, err = rt.RoundTrip(req)
		Expect(err).NotTo(HaveOccurred())
		return got
	}
	const base = "https://kube/api/v1/namespaces/"

	It("adds a metadata.name field selector to Secret list and watch in the namespace", func() {
		for _, u := range []string{
			base + "promoter-system/secrets?limit=500",
			base + "promoter-system/secrets?watch=true&resourceVersion=1",
		} {
			q := rewritten(http.MethodGet, u).URL.Query()
			Expect(q.Get("fieldSelector")).To(Equal("metadata.name=promoter-apiserver-serving-cert"), u)
		}
		Expect(rewritten(http.MethodGet, base+"promoter-system/secrets?limit=500").URL.Query().Get("limit")).To(Equal("500"))
	})

	It("leaves other requests untouched", func() {
		for _, c := range []struct{ method, url string }{
			{http.MethodGet, base + "promoter-system/secrets/promoter-apiserver-serving-cert"},
			{http.MethodPut, base + "promoter-system/secrets/promoter-apiserver-serving-cert"},
			{http.MethodGet, base + "other/secrets"},
			{http.MethodGet, base + "promoter-system/configmaps"},
			{http.MethodGet, "https://kube/apis/apiregistration.k8s.io/v1/apiservices"},
		} {
			Expect(rewritten(c.method, c.url).URL.Query().Has("fieldSelector")).To(BeFalse(), c.method+" "+c.url)
		}
	})
})

var _ = Describe("cert rotation integration (envtest)", func() {
	It("populates the Secret, injects the APIService caBundle, and becomes ready once certs are mounted", func() {
		env, cfg, cl := startRunEnvtest()
		DeferCleanup(stopRunEnvtest, env)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		const ns = "promoter-system"
		Expect(cl.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})).To(Succeed())
		Expect(cl.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: defaultCertRotationSecretName, Namespace: ns},
		})).To(Succeed())

		apiService := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apiregistration.k8s.io/v1",
			"kind":       "APIService",
			"metadata":   map[string]any{"name": defaultCertRotationAPIService},
			"spec": map[string]any{
				"group":                "view.promoter.argoproj.io",
				"version":              "v1alpha1",
				"groupPriorityMinimum": int64(1000),
				"versionPriority":      int64(15),
				"service": map[string]any{
					"name":      defaultCertRotationServiceName,
					"namespace": ns,
					"port":      int64(443),
				},
			},
		}}
		Expect(cl.Create(ctx, apiService)).To(Succeed())

		By("applying the overlay's RBAC and an unrelated Secret the apiserver must not be able to read")
		Expect(cl.Create(ctx, &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: certRotationTestServiceAccount, Namespace: ns},
		})).To(Succeed())
		for _, obj := range loadManifests(filepath.Join("..", "..", "config", "apiserver", "certs-cert-controller", "rbac.yaml")) {
			Expect(cl.Create(ctx, obj)).To(Succeed(), "creating %s %s", obj.GetKind(), obj.GetName())
		}
		Expect(cl.Create(ctx, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "scm-credentials", Namespace: ns},
			StringData: map[string]string{"token": "do-not-leak"},
		})).To(Succeed())

		// Run the rotator as the apiserver's ServiceAccount so the overlay's RBAC is enforced.
		saCfg := clientrest.CopyConfig(cfg)
		saCfg.Impersonate = clientrest.ImpersonationConfig{
			UserName: "system:serviceaccount:" + ns + ":" + certRotationTestServiceAccount,
		}
		saClient, err := client.New(saCfg, client.Options{Scheme: utils.GetScheme()})
		Expect(err).NotTo(HaveOccurred())
		Expect(apierrors.IsForbidden(saClient.List(ctx, &corev1.SecretList{}, client.InNamespace(ns)))).To(BeTrue(),
			"the apiserver ServiceAccount must not be able to list all Secrets")
		Expect(apierrors.IsForbidden(saClient.Get(ctx, client.ObjectKey{Namespace: ns, Name: "scm-credentials"}, &corev1.Secret{}))).To(BeTrue(),
			"the apiserver ServiceAccount must not be able to read other Secrets")

		opts := newCertRotationOptions()
		opts.Enabled = true
		opts.Namespace = ns
		certDir := GinkgoT().TempDir()
		certFile := filepath.Join(certDir, "tls.crt")
		keyFile := filepath.Join(certDir, "tls.key")
		Expect(opts.Validate(certFile, keyFile)).To(Succeed())

		ready, errCh, err := startCertRotation(ctx, saCfg, &opts, certFile, keyFile)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for the rotator to populate the Secret")
		secret := &corev1.Secret{}
		Eventually(func(g Gomega) {
			g.Expect(cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: defaultCertRotationSecretName}, secret)).To(Succeed())
			g.Expect(secret.Data).To(HaveKey("tls.crt"))
			g.Expect(secret.Data).To(HaveKey("tls.key"))
			g.Expect(secret.Data).To(HaveKey("ca.crt"))
		}, 30*time.Second, 200*time.Millisecond).Should(Succeed())

		block, _ := pem.Decode(secret.Data["tls.crt"])
		Expect(block).NotTo(BeNil())
		servingCert, err := x509.ParseCertificate(block.Bytes)
		Expect(err).NotTo(HaveOccurred())
		Expect(servingCert.DNSNames).To(ContainElements(
			"promoter-apiserver.promoter-system.svc",
			"promoter-apiserver.promoter-system.svc.cluster.local",
		))

		By("waiting for the CA to be injected into the APIService")
		Eventually(func(g Gomega) {
			got := &unstructured.Unstructured{}
			got.SetAPIVersion("apiregistration.k8s.io/v1")
			got.SetKind("APIService")
			g.Expect(cl.Get(ctx, client.ObjectKey{Name: defaultCertRotationAPIService}, got)).To(Succeed())
			caBundle, found, err := unstructured.NestedString(got.Object, "spec", "caBundle")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(found).To(BeTrue())
			g.Expect(caBundle).NotTo(BeEmpty())
		}, 30*time.Second, 200*time.Millisecond).Should(Succeed())

		By("checking the generated fields are owned by the rotator's field manager")
		Expect(cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: defaultCertRotationSecretName}, secret)).To(Succeed())
		Expect(managedFieldsOf(secret.GetManagedFields(), certRotationControllerName)).To(ContainSubstring(`"f:tls.crt"`))
		gotAPIService := &unstructured.Unstructured{}
		gotAPIService.SetAPIVersion("apiregistration.k8s.io/v1")
		gotAPIService.SetKind("APIService")
		Expect(cl.Get(ctx, client.ObjectKey{Name: defaultCertRotationAPIService}, gotAPIService)).To(Succeed())
		Expect(managedFieldsOf(gotAPIService.GetManagedFields(), certRotationControllerName)).To(ContainSubstring(`"f:caBundle"`))

		Consistently(ready, 500*time.Millisecond).ShouldNot(BeClosed(),
			"rotator must not report ready before the Secret is mounted")

		By("simulating kubelet projecting the Secret into the pod")
		Expect(os.WriteFile(keyFile, secret.Data["tls.key"], 0o600)).To(Succeed())
		Expect(os.WriteFile(certFile, secret.Data["tls.crt"], 0o600)).To(Succeed())

		Eventually(ready, time.Minute).Should(BeClosed())

		cancel()
		Eventually(errCh, 30*time.Second).Should(Receive(BeNil()))
	})
})

// managedFieldsOf returns the concatenated FieldsV1 owned by manager.
func managedFieldsOf(entries []metav1.ManagedFieldsEntry, manager string) string {
	var out strings.Builder
	for _, mf := range entries {
		if mf.Manager == manager && mf.FieldsV1 != nil {
			out.WriteString(mf.FieldsV1.GetRawString())
		}
	}
	return out.String()
}

const certRotationTestServiceAccount = "promoter-apiserver"

// loadManifests decodes every object in a multi-document YAML file.
func loadManifests(path string) []*unstructured.Unstructured {
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	dec := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	var objs []*unstructured.Unstructured
	for {
		obj := &unstructured.Unstructured{}
		if err := dec.Decode(&obj.Object); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			Expect(err).NotTo(HaveOccurred())
		}
		if len(obj.Object) > 0 {
			objs = append(objs, obj)
		}
	}
	return objs
}
