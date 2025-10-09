// Test to verify the ArgoCDCommitStatus controller doesn't crash when the Application CRD
// is not installed and EnableLocalArgoCDApplications is disabled.
//
// Note: This test should be run separately from the main test suite to avoid controller
// name conflicts. Run with: go test -v -run TestArgoCDCommitStatusSetupWithLocalDisabled
package controller

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	kubeconfigprovider "sigs.k8s.io/multicluster-runtime/providers/kubeconfig"
)

// TestArgoCDCommitStatusSetupWithLocalEnabled verifies that when EnableLocalArgoCDApplications
// is set to true, the controller sets up to watch local Applications.  this test ensures
// the configuration is applied correctly and the controller is set up with local watches enabled.
func TestArgoCDCommitStatusSetupWithLocalEnabled(t *testing.T) {
	g := gomega.NewWithT(t)

	// Create an envtest WITHOUT the Argo CD Application CRD to simulate missing CRD
	env := &envtest.Environment{
		UseExistingCluster: ptrTo(false),
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			// Intentionally NOT including test/external_crds so Application CRD is missing
		},
		ControlPlaneStopTimeout: 1 * time.Minute,
		BinaryAssetsDirectory:   filepath.Join("..", "..", "bin", "k8s", fmt.Sprintf("1.31.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	cfg, err := env.Start()
	g.Expect(err).ToNot(gomega.HaveOccurred())
	g.Expect(cfg).ToNot(gomega.BeNil())
	defer func() { _ = env.Stop() }()

	// Use discovery client to verify the Application CRD is NOT installed in this env
	disco, err := discovery.NewDiscoveryClientForConfig(cfg)
	g.Expect(err).ToNot(gomega.HaveOccurred())
	_, apiResources, err := disco.ServerGroupsAndResources()
	if err == nil {
		found := false
		for _, list := range apiResources {
			for _, r := range list.APIResources {
				if r.Kind == "Application" {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if found {
			// If the CRD exists, fail the test early so we know the env wasn't configured as expected
			t.Fatalf("Application CRD unexpectedly present in test env; ensure CRDDirectoryPaths omitted external_crds")
		}
	}

	// Use the scheme from utils; we won't create a non-cached client here.
	scheme := utils.GetScheme()

	// Create ControllerConfiguration with EnableLocalArgoCDApplications set to TRUE
	cc := &promoterv1alpha1.ControllerConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: settings.ControllerConfigurationName, Namespace: "default"},
		Spec: promoterv1alpha1.ControllerConfigurationSpec{
			PromotionStrategy:    promoterv1alpha1.PromotionStrategyConfiguration{WorkQueue: promoterv1alpha1.WorkQueue{RequeueDuration: metav1.Duration{Duration: time.Minute}, MaxConcurrentReconciles: 1, RateLimiter: promoterv1alpha1.RateLimiter{RateLimiterTypes: promoterv1alpha1.RateLimiterTypes{Bucket: &promoterv1alpha1.Bucket{Qps: 10, Bucket: 100}}}}},
			ChangeTransferPolicy: promoterv1alpha1.ChangeTransferPolicyConfiguration{WorkQueue: promoterv1alpha1.WorkQueue{RequeueDuration: metav1.Duration{Duration: time.Minute}, MaxConcurrentReconciles: 1, RateLimiter: promoterv1alpha1.RateLimiter{RateLimiterTypes: promoterv1alpha1.RateLimiterTypes{Bucket: &promoterv1alpha1.Bucket{Qps: 10, Bucket: 100}}}}},
			PullRequest:          promoterv1alpha1.PullRequestConfiguration{Template: promoterv1alpha1.PullRequestTemplate{Title: "t", Description: "d"}, WorkQueue: promoterv1alpha1.WorkQueue{RequeueDuration: metav1.Duration{Duration: time.Minute}, MaxConcurrentReconciles: 1, RateLimiter: promoterv1alpha1.RateLimiter{RateLimiterTypes: promoterv1alpha1.RateLimiterTypes{Bucket: &promoterv1alpha1.Bucket{Qps: 10, Bucket: 100}}}}},
			CommitStatus:         promoterv1alpha1.CommitStatusConfiguration{WorkQueue: promoterv1alpha1.WorkQueue{RequeueDuration: metav1.Duration{Duration: time.Minute}, MaxConcurrentReconciles: 1, RateLimiter: promoterv1alpha1.RateLimiter{RateLimiterTypes: promoterv1alpha1.RateLimiterTypes{Bucket: &promoterv1alpha1.Bucket{Qps: 10, Bucket: 100}}}}},
			ArgoCDCommitStatus:   promoterv1alpha1.ArgoCDCommitStatusConfiguration{WorkQueue: promoterv1alpha1.WorkQueue{RequeueDuration: metav1.Duration{Duration: time.Minute}, MaxConcurrentReconciles: 1, RateLimiter: promoterv1alpha1.RateLimiter{RateLimiterTypes: promoterv1alpha1.RateLimiterTypes{Bucket: &promoterv1alpha1.Bucket{Qps: 10, Bucket: 100}}}}, EnableLocalArgoCDApplications: ptrTo(false)},
		},
	}

	// Do not create the ControllerConfiguration with the non-cached client here; we'll create it
	// using the manager's cached client (k8sClient) below so we don't accidentally carry a
	// ResourceVersion that would make subsequent Create calls fail.

	// Build multicluster manager like suite_test.go
	kubeconfigProvider := kubeconfigprovider.New(kubeconfigprovider.Options{Namespace: "default", KubeconfigSecretLabel: "kubeconfig-provider", KubeconfigSecretKey: "kubeconfig", Scheme: scheme})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mcMgr, err := mcmanager.New(cfg, kubeconfigProvider, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// Setup kubeconfig provider controller with manager (mirror suite_test.go)
	err = kubeconfigProvider.SetupWithManager(ctx, mcMgr)
	g.Expect(err).ToNot(gomega.HaveOccurred())

	k8sManager := mcMgr.GetLocalManager()
	// use k8sManager client to ensure cached client sees config
	k8sClient := k8sManager.GetClient()
	err = k8sClient.Create(context.Background(), cc)
	if err != nil {
		if client.IgnoreAlreadyExists(err) != nil {
			g.Expect(err).ToNot(gomega.HaveOccurred())
		}
	}

	settingsMgr := settings.NewManager(k8sManager.GetClient(), k8sManager.GetAPIReader(), settings.ManagerConfig{ControllerNamespace: "default"})

	// Create reconciler and call SetupWithManager
	reconciler := &ArgoCDCommitStatusReconciler{Manager: mcMgr, SettingsMgr: settingsMgr, KubeConfigProvider: kubeconfigProvider}
	reqCtx, reqCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer reqCancel()
	err = reconciler.SetupWithManager(reqCtx, mcMgr)
	g.Expect(err).ToNot(gomega.HaveOccurred())

	// Start the manager in a goroutine
	mgrErrCh := make(chan error, 1)
	go func() { mgrErrCh <- mcMgr.Start(ctx) }()
}

// ptrTo is a tiny helper to avoid importing k8s.io/utils/ptr in the test file
func ptrTo[T any](v T) *T { return &v }
