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

package v1alpha1

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/argoproj-labs/gitops-promoter/internal/controller"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

var (
	ctx       context.Context
	cancel    context.CancelFunc
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
)

func TestWebhooks(t *testing.T) {
	t.Parallel()
	RegisterFailHandler(Fail)
	RunSpecs(t, "Webhook Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	//nolint:fatcontext // ctx is intentionally reassigned in test setup
	ctx, cancel = context.WithCancel(context.Background())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{filepath.Join("..", "..", "..", "config", "webhook", "manifests.yaml")},
		},
	}
	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	scheme := utils.GetScheme()
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	opts := &testEnv.WebhookInstallOptions
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    opts.LocalServingHost,
			Port:    opts.LocalServingPort,
			CertDir: opts.LocalServingCertDir,
		}),
	})
	Expect(err).NotTo(HaveOccurred())

	templates, err := controller.NewTemplateValidator(cfg)
	Expect(err).NotTo(HaveOccurred())
	configured := map[schema.GroupVersionKind]struct{}{
		{Group: "batch", Version: "v1", Kind: "Job"}: {},
		{Version: "v1", Kind: "ConfigMap"}:           {},
	}
	Expect((&KubernetesResourceCommitStatusValidator{
		Templates: templates,
		IsConfiguredKind: func(gvk schema.GroupVersionKind) bool {
			_, ok := configured[gvk]
			return ok
		},
	}).SetupWithManager(mgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()

	// Wait for the webhook server to accept TLS connections.
	dialer := &net.Dialer{Timeout: time.Second}
	addr := net.JoinHostPort(opts.LocalServingHost, strconv.Itoa(opts.LocalServingPort))
	Eventually(func() error {
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // test-only probe of the local webhook server
		if err != nil {
			return fmt.Errorf("webhook server not reachable: %w", err)
		}
		return conn.Close() //nolint:wrapcheck // test helper
	}).Should(Succeed())
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})
