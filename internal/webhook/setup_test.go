/*
Copyright 2026.

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

package webhook

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	crwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/argoproj-labs/gitops-promoter/internal/controller"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// TestSetup checks the self-managed certificate: the rotator fills the shipped Secret and injects
// the CA into the ValidatingWebhookConfiguration, and readiness passes only once the certificate is in the cert
// directory (where the Deployment mounts the Secret) and the webhook server has started with it.
func TestSetup(t *testing.T) {
	t.Parallel()
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is not set")
	}
	env := &envtest.Environment{CRDDirectoryPaths: []string{filepath.Join("..", "..", "config", "crd", "bases")}}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env.Stop() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	scheme := utils.GetScheme()
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}

	const namespace = "default"
	opts := Options{
		Enabled:           true,
		CertDir:           t.TempDir(),
		SecretName:        "promoter-webhook-server-cert",
		ServiceName:       "promoter-webhook-service",
		ConfigurationName: "promoter-validating-webhook-configuration",
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: opts.SecretName, Namespace: namespace}}
	if err := c.Create(ctx, secret); err != nil {
		t.Fatal(err)
	}
	failurePolicy := admissionregistrationv1.Ignore
	sideEffects := admissionregistrationv1.SideEffectClassNone
	path := "/validate"
	vwc := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: opts.ConfigurationName},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{{
			Name: "vkubernetesresourcecommitstatus.promoter.argoproj.io",
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				Service: &admissionregistrationv1.ServiceReference{Namespace: namespace, Name: opts.ServiceName, Path: &path},
			},
			FailurePolicy:           &failurePolicy,
			SideEffects:             &sideEffects,
			AdmissionReviewVersions: []string{"v1"},
		}},
	}
	if err := c.Create(ctx, vwc); err != nil {
		t.Fatal(err)
	}

	server := crwebhook.NewServer(crwebhook.Options{Host: "127.0.0.1", Port: 0, CertDir: opts.CertDir})
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: "0"},
		WebhookServer:          server,
		HealthProbeBindAddress: "0",
	})
	if err != nil {
		t.Fatal(err)
	}
	templates, err := controller.NewTemplateValidator(cfg)
	if err != nil {
		t.Fatal(err)
	}
	allKinds := func(schema.GroupVersionKind) bool { return true }
	check, err := Setup(ctx, mgr, server, opts, namespace, templates, allKinds)
	if err != nil {
		t.Fatal(err)
	}
	readyz := &healthz.Handler{Checks: map[string]healthz.Checker{"webhook": check}}
	if code := probe(readyz); code == http.StatusOK {
		t.Fatal("expected readiness to fail before the certificate exists")
	}

	go func() { _ = mgr.Start(ctx) }()

	deadline := time.Now().Add(60 * time.Second)
	for {
		var gotSecret corev1.Secret
		var gotVWC admissionregistrationv1.ValidatingWebhookConfiguration
		errSecret := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: opts.SecretName}, &gotSecret)
		errVWC := c.Get(ctx, types.NamespacedName{Name: opts.ConfigurationName}, &gotVWC)
		// Stand in for the kubelet, which syncs the Secret into the mounted volume.
		if errSecret == nil {
			for name, data := range gotSecret.Data {
				_ = os.WriteFile(filepath.Join(opts.CertDir, name), data, 0o600)
			}
		}
		_, errCert := os.Stat(filepath.Join(opts.CertDir, "tls.crt"))
		if errSecret == nil && errVWC == nil && errCert == nil &&
			len(gotSecret.Data["ca.crt"]) > 0 && len(gotSecret.Data["tls.crt"]) > 0 &&
			len(gotVWC.Webhooks[0].ClientConfig.CABundle) > 0 && probe(readyz) == http.StatusOK {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("certificate not ready: secret keys=%d, caBundle=%d, cert file err=%v, readyz=%d",
				len(gotSecret.Data), len(gotVWC.Webhooks[0].ClientConfig.CABundle), errCert, probe(readyz))
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func probe(h http.Handler) int {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))
	return rec.Code
}
