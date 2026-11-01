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
	"crypto/tls"
	"fmt"
	"net"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/cel/openapi/resolver"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/argoproj-labs/gitops-promoter/internal/resourcetemplate"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

var (
	testEnv   *envtest.Environment
	k8sClient client.Client
	// warnClient is k8sClient with admission warnings recorded in warnings.
	warnClient client.Client
	warnings   *warningRecorder
	validator  *KubernetesResourceCommitStatusValidator
	ctx        context.Context
	cancel     context.CancelFunc
)

var (
	jobGVK       = schema.GroupVersionKind{Group: "batch", Version: "v1", Kind: "Job"}
	configMapGVK = schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}
	// widgetGVK is configured but not served, as when a CRD was removed after the controller started.
	widgetGVK = schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
)

// warningRecorder collects the admission warnings the API server relays.
type warningRecorder struct {
	mu       sync.Mutex
	messages []string
}

func (w *warningRecorder) HandleWarningHeaderWithContext(_ context.Context, _ int, _ string, text string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.messages = append(w.messages, text)
}

func (w *warningRecorder) drain() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.messages
	w.messages = nil
	return out
}

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
		CRDDirectoryPaths:       []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing:   true,
		ControlPlaneStopTimeout: time.Minute,
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{filepath.Join("..", "..", "config", "webhook", "base")},
		},
		// Used when KUBEBUILDER_ASSETS is unset; make test sets it.
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.31.0-%s-%s", goruntime.GOOS, goruntime.GOARCH)),
	}
	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())

	scheme := utils.GetScheme()
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	warnings = &warningRecorder{}
	warnCfg := rest.CopyConfig(cfg)
	warnCfg.WarningHandlerWithContext = warnings
	warnClient, err = client.New(warnCfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	opts := testEnv.WebhookInstallOptions
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

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	Expect(err).NotTo(HaveOccurred())
	compiler, err := resourcetemplate.NewCompiler(&resolver.ClientDiscoveryResolver{Discovery: discoveryClient}, logf.Log)
	Expect(err).NotTo(HaveOccurred())
	validator = &KubernetesResourceCommitStatusValidator{
		Compiler:   compiler,
		Configured: map[schema.GroupVersionKind]struct{}{jobGVK: {}, configMapGVK: {}, widgetGVK: {}},
	}
	Expect(validator.SetupWithManager(mgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()

	addr := net.JoinHostPort(opts.LocalServingHost, strconv.Itoa(opts.LocalServingPort))
	Eventually(func() error {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", addr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // local test server
		if err != nil {
			return fmt.Errorf("webhook server not ready: %w", err)
		}
		return conn.Close()
	}, 30*time.Second).Should(Succeed())
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
})
