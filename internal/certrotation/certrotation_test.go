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

package certrotation

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/open-policy-agent/cert-controller/pkg/rotator"
	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	clientrest "k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

var _ = Describe("Options", func() {
	enabledOpts := func() Options {
		o := NewOptions()
		o.Enabled = true
		o.Namespace = "promoter-system"
		return o
	}

	It("registers controller flags with the manifest defaults and rotation disabled", func() {
		o := NewOptions()
		fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
		o.AddFlags(fs)

		Expect(fs.Parse([]string{"--apiserver-cert-rotation"})).To(Succeed())
		Expect(o.Enabled).To(BeTrue())
		Expect(o.SecretName).To(Equal("promoter-apiserver-serving-cert"))
		Expect(o.ServiceName).To(Equal("promoter-apiserver"))
		Expect(o.APIServiceName).To(Equal("v1alpha1.view.promoter.argoproj.io"))
		Expect(o.Namespace).To(BeEmpty())
		Expect(NewOptions().Enabled).To(BeFalse())
	})

	It("skips validation when disabled", func() {
		o := Options{}
		Expect(o.Validate()).To(Succeed())
	})

	It("accepts the defaults with a namespace", func() {
		o := enabledOpts()
		Expect(o.Validate()).To(Succeed())
	})

	It("requires a namespace", func() {
		o := enabledOpts()
		o.Namespace = ""
		Expect(o.Validate()).To(MatchError(ContainSubstring("namespace must be set")))
	})

	It("builds a rotator for the service DNS names and APIService", func() {
		o := enabledOpts()
		cr := o.newCertRotator("/some/dir")

		Expect(cr.SecretKey).To(Equal(types.NamespacedName{Namespace: "promoter-system", Name: "promoter-apiserver-serving-cert"}))
		Expect(cr.CertDir).To(Equal("/some/dir"))
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
		Expect(cr.RequireLeaderElection).To(BeTrue())
		Expect(cr.FieldOwner).To(Equal(FieldManager))
	})

	It("creates a marker cert dir that satisfies cert-controller's mount check", func() {
		dir, err := markerCertDir()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, dir)
		Expect(filepath.Join(dir, certName)).To(BeAnExistingFile())
		Expect(filepath.Join(dir, keyName)).To(BeAnExistingFile())
	})
})

var _ = Describe("cert rotation integration (envtest)", func() {
	It("runs as the controller ServiceAccount, populates the Secret and injects the APIService caBundle", func() {
		if os.Getenv("KUBEBUILDER_ASSETS") == "" {
			Skip("KUBEBUILDER_ASSETS is not set; run via make test or make test-parallel")
		}
		env := &envtest.Environment{}
		cfg, err := env.Start()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(env.Stop()).To(Succeed()) })
		cl, err := client.New(cfg, client.Options{Scheme: utils.GetScheme()})
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		const ns = "promoter-system"
		const controllerSA = "promoter-controller-manager"
		Expect(cl.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})).To(Succeed())
		Expect(cl.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: DefaultSecretName, Namespace: ns}})).To(Succeed())
		Expect(cl.Create(ctx, &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "apiregistration.k8s.io/v1",
			"kind":       "APIService",
			"metadata":   map[string]any{"name": DefaultAPIServiceName},
			"spec": map[string]any{
				"group":                "view.promoter.argoproj.io",
				"version":              "v1alpha1",
				"groupPriorityMinimum": int64(1000),
				"versionPriority":      int64(15),
				"service":              map[string]any{"name": DefaultServiceName, "namespace": ns, "port": int64(443)},
			},
		}})).To(Succeed())

		By("granting the controller ServiceAccount its real RBAC: manager-role plus the cert-rotation component")
		Expect(cl.Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: controllerSA, Namespace: ns}})).To(Succeed())
		for _, obj := range loadManifests(filepath.Join("..", "..", "config", "rbac", "role.yaml")) {
			Expect(cl.Create(ctx, obj)).To(Succeed(), "creating %s %s", obj.GetKind(), obj.GetName())
		}
		Expect(cl.Create(ctx, &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "manager-rolebinding"},
			RoleRef:    rbacv1.RoleRef{APIGroup: rbacv1.GroupName, Kind: "ClusterRole", Name: "manager-role"},
			Subjects:   []rbacv1.Subject{{Kind: rbacv1.ServiceAccountKind, Name: controllerSA, Namespace: ns}},
		})).To(Succeed())
		for _, obj := range loadManifests(filepath.Join("..", "..", "config", "apiserver", "cert-rotation-controller", "rbac.yaml")) {
			Expect(cl.Create(ctx, obj)).To(Succeed(), "creating %s %s", obj.GetKind(), obj.GetName())
		}
		saCfg := clientrest.CopyConfig(cfg)
		saCfg.Impersonate = clientrest.ImpersonationConfig{UserName: "system:serviceaccount:" + ns + ":" + controllerSA}

		opts := NewOptions()
		opts.Enabled = true
		opts.Namespace = ns
		Expect(opts.Validate()).To(Succeed())
		// The test manager stands in for the controller-manager. Metrics and health stay
		// off so the spec does not bind ports; production passes the controller's manager.
		mgr, err := manager.New(saCfg, manager.Options{
			Scheme:                 utils.GetScheme(),
			Metrics:                metricsserver.Options{BindAddress: "0"},
			HealthProbeBindAddress: "0",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(AddToManager(mgr, &opts)).To(Succeed())
		errCh := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			errCh <- mgr.Start(ctx)
		}()

		By("waiting for the rotator to populate the Secret")
		secret := &corev1.Secret{}
		Eventually(func(g Gomega) {
			g.Expect(cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: DefaultSecretName}, secret)).To(Succeed())
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
		apiService := &unstructured.Unstructured{}
		apiService.SetAPIVersion("apiregistration.k8s.io/v1")
		apiService.SetKind("APIService")
		Eventually(func(g Gomega) {
			g.Expect(cl.Get(ctx, client.ObjectKey{Name: DefaultAPIServiceName}, apiService)).To(Succeed())
			caBundle, _, err := unstructured.NestedString(apiService.Object, "spec", "caBundle")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(caBundle).NotTo(BeEmpty())
		}, 30*time.Second, 200*time.Millisecond).Should(Succeed())

		By("checking the generated fields are owned by the rotator's field manager")
		Expect(cl.Get(ctx, client.ObjectKey{Namespace: ns, Name: DefaultSecretName}, secret)).To(Succeed())
		Expect(managedFieldsOf(secret.GetManagedFields(), FieldManager)).To(ContainSubstring(`"f:tls.crt"`))
		Expect(managedFieldsOf(apiService.GetManagedFields(), FieldManager)).To(ContainSubstring(`"f:caBundle"`))

		By("checking the rotator keeps running without the cert files mounted in this process")
		Consistently(errCh, 5*time.Second).ShouldNot(Receive())

		cancel()
		Eventually(errCh, 30*time.Second).Should(Receive(BeNil()))
	})
})

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
