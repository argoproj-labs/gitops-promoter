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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	crwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/argoproj-labs/gitops-promoter/internal/controller"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// probe runs the readiness handler and returns its HTTP status code.
func probe(h http.Handler) int {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil))
	return rec.Code
}

var _ = Describe("Setup", func() {
	const namespace = "default"

	It("rotates the self-managed certificate, injects the CA, and is ready only once the webhook server started", func() {
		opts := Options{
			Enabled:           true,
			CertDir:           GinkgoT().TempDir(),
			SecretName:        "promoter-webhook-server-cert",
			ServiceName:       "promoter-webhook-service",
			ConfigurationName: "promoter-validating-webhook-configuration",
		}

		By("creating the empty Secret and the webhook configuration the manifests ship")
		Expect(k8sClient.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: opts.SecretName, Namespace: namespace}})).To(Succeed())
		failurePolicy := admissionregistrationv1.Ignore
		sideEffects := admissionregistrationv1.SideEffectClassNone
		path := "/validate"
		Expect(k8sClient.Create(ctx, &admissionregistrationv1.ValidatingWebhookConfiguration{
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
		})).To(Succeed())

		By("setting up the webhook on a manager")
		server := crwebhook.NewServer(crwebhook.Options{Host: "127.0.0.1", Port: 0, CertDir: opts.CertDir})
		mgr, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme:                 utils.GetScheme(),
			Metrics:                metricsserver.Options{BindAddress: "0"},
			WebhookServer:          server,
			HealthProbeBindAddress: "0",
		})
		Expect(err).NotTo(HaveOccurred())
		templates, err := controller.NewTemplateValidator(cfg)
		Expect(err).NotTo(HaveOccurred())
		allKinds := func(schema.GroupVersionKind) bool { return true }
		check, err := Setup(ctx, mgr, server, opts, namespace, templates, allKinds)
		Expect(err).NotTo(HaveOccurred())
		readyz := &healthz.Handler{Checks: map[string]healthz.Checker{"webhook": check}}
		Expect(probe(readyz)).NotTo(Equal(http.StatusOK), "readiness must fail before the certificate exists")

		mgrCtx, stopMgr := context.WithCancel(ctx)
		DeferCleanup(stopMgr)
		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(mgrCtx)).To(Succeed())
		}()

		By("waiting for the Secret to be filled, the CA injected, and readiness to pass")
		Eventually(func(g Gomega) {
			var secret corev1.Secret
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: opts.SecretName}, &secret)).To(Succeed())
			g.Expect(secret.Data).To(HaveKey("ca.crt"))
			g.Expect(secret.Data).To(HaveKey("tls.crt"))
			// Stand in for the kubelet, which syncs the Secret into the mounted volume.
			for name, data := range secret.Data {
				g.Expect(os.WriteFile(filepath.Join(opts.CertDir, name), data, 0o600)).To(Succeed())
			}

			var vwc admissionregistrationv1.ValidatingWebhookConfiguration
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: opts.ConfigurationName}, &vwc)).To(Succeed())
			g.Expect(vwc.Webhooks[0].ClientConfig.CABundle).NotTo(BeEmpty())

			g.Expect(probe(readyz)).To(Equal(http.StatusOK))
		}, 60*time.Second, 200*time.Millisecond).Should(Succeed())
	})
})
