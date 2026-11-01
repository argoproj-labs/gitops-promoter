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
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/yaml"

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
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "config", "crd", "bases"),
			// Argo Workflows' full (typed) Workflow CRD and Tekton's PipelineRun CRD.
			filepath.Join("..", "..", "..", "test", "external_crds"),
		},
		// Argo Workflows' minimal (loose) Workflow CRD has the same name as the full one, so it is installed
		// under a test-only group.
		CRDs:                  []*apiextensionsv1.CustomResourceDefinition{minimalWorkflowCRD()},
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
	// The API server publishes a newly installed CRD's OpenAPI schema shortly after the CRD is established (the
	// 2.8 MB full Workflow schema takes longest). Until then the webhook rejects gates for that kind, so wait.
	for _, gvk := range []schema.GroupVersionKind{workflowGVK, minimalWorkflowGVK, pipelineRunGVK} {
		Eventually(func() error {
			_, err := templates.Validate(ctx, map[string]any{"apiVersion": gvk.GroupVersion().String(), "kind": gvk.Kind}, gvk)
			return err //nolint:wrapcheck // test helper
		}, 60*time.Second, 250*time.Millisecond).Should(Succeed(), "OpenAPI schema of %s not published", gvk)
	}

	configured := map[schema.GroupVersionKind]struct{}{
		{Group: "batch", Version: "v1", Kind: "Job"}: {},
		{Version: "v1", Kind: "ConfigMap"}:           {},
		workflowGVK:                                  {},
		minimalWorkflowGVK:                           {},
		pipelineRunGVK:                               {},
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

// minimalWorkflowGroup is the test-only group Argo Workflows' minimal Workflow CRD is installed under.
const minimalWorkflowGroup = "minimal.workflows.test"

var (
	workflowGVK        = schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Workflow"}
	minimalWorkflowGVK = schema.GroupVersionKind{Group: minimalWorkflowGroup, Version: "v1alpha1", Kind: "Workflow"}
	pipelineRunGVK     = schema.GroupVersionKind{Group: "tekton.dev", Version: "v1", Kind: "PipelineRun"}
)

// minimalWorkflowCRD reads Argo Workflows' minimal Workflow CRD and moves it to minimalWorkflowGroup.
func minimalWorkflowCRD() *apiextensionsv1.CustomResourceDefinition {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "test", "crd_variants", "argoproj.io_workflows_minimal.yaml"))
	Expect(err).NotTo(HaveOccurred())
	var crd apiextensionsv1.CustomResourceDefinition
	Expect(yaml.Unmarshal(raw, &crd)).To(Succeed())
	crd.Spec.Group = minimalWorkflowGroup
	crd.Name = crd.Spec.Names.Plural + "." + minimalWorkflowGroup
	return &crd
}
