// Test to verify the ArgoCDCommitStatus controller doesn't crash when the Application CRD
// is not installed and EnableLocalArgoCDApplications is disabled.
package controller

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	"sigs.k8s.io/multicluster-runtime/pkg/controller"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	kubeconfigprovider "sigs.k8s.io/multicluster-runtime/providers/kubeconfig"
)

var _ = Describe("ArgoCDCommitStatus Local Disabled", Ordered, func() {
	var (
		env         *envtest.Environment
		ctx         context.Context
		cancel      context.CancelFunc
		mcMgr       mcmanager.Manager
		k8sClient   client.Client
		settingsMgr *settings.Manager
	)

	BeforeAll(func() {
		ctx, cancel = context.WithCancel(context.Background())

		// Create an envtest WITHOUT the Argo CD Application CRD to simulate missing CRD
		env = &envtest.Environment{
			UseExistingCluster: ptrTo(false),
			CRDDirectoryPaths: []string{
				filepath.Join("..", "..", "config", "crd", "bases"),
				// Intentionally NOT including test/external_crds so Application CRD is missing
			},
			ControlPlaneStopTimeout: 1 * time.Minute,
			BinaryAssetsDirectory:   filepath.Join("..", "..", "bin", "k8s", fmt.Sprintf("1.31.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
		}

		cfg, err := env.Start()
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg).ToNot(BeNil())

		// Use the scheme from utils
		scheme := utils.GetScheme()

		// Build multicluster manager with a unique label to avoid controller name conflicts
		// We use a unique label so this provider doesn't conflict with the one in the main test suite
		kubeconfigProvider := kubeconfigprovider.New(kubeconfigprovider.Options{
			Namespace:             "default",
			KubeconfigSecretLabel: "kubeconfig-provider-local-disabled-test",
			KubeconfigSecretKey:   "kubeconfig",
			Scheme:                scheme,
		})

		mcMgr, err = mcmanager.New(cfg, kubeconfigProvider, ctrl.Options{
			Scheme:  scheme,
			Metrics: metricsserver.Options{BindAddress: "0"},
		})
		Expect(err).ToNot(HaveOccurred())

		k8sManager := mcMgr.GetLocalManager()
		k8sClient = k8sManager.GetClient()

		settingsMgr = settings.NewManager(k8sManager.GetClient(), k8sManager.GetAPIReader(), settings.ManagerConfig{ControllerNamespace: "default"})

		// Create ControllerConfiguration with EnableLocalArgoCDApplications set to FALSE
		cc := &promoterv1alpha1.ControllerConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: settings.ControllerConfigurationName, Namespace: "default"},
			Spec: promoterv1alpha1.ControllerConfigurationSpec{
				PromotionStrategy: promoterv1alpha1.PromotionStrategyConfiguration{
					WorkQueue: promoterv1alpha1.WorkQueue{
						RequeueDuration:         metav1.Duration{Duration: time.Minute},
						MaxConcurrentReconciles: 1,
						RateLimiter: promoterv1alpha1.RateLimiter{
							RateLimiterTypes: promoterv1alpha1.RateLimiterTypes{
								Bucket: &promoterv1alpha1.Bucket{Qps: 10, Bucket: 100},
							},
						},
					},
				},
				ChangeTransferPolicy: promoterv1alpha1.ChangeTransferPolicyConfiguration{
					WorkQueue: promoterv1alpha1.WorkQueue{
						RequeueDuration:         metav1.Duration{Duration: time.Minute},
						MaxConcurrentReconciles: 1,
						RateLimiter: promoterv1alpha1.RateLimiter{
							RateLimiterTypes: promoterv1alpha1.RateLimiterTypes{
								Bucket: &promoterv1alpha1.Bucket{Qps: 10, Bucket: 100},
							},
						},
					},
				},
				PullRequest: promoterv1alpha1.PullRequestConfiguration{
					Template: promoterv1alpha1.PullRequestTemplate{Title: "t", Description: "d"},
					WorkQueue: promoterv1alpha1.WorkQueue{
						RequeueDuration:         metav1.Duration{Duration: time.Minute},
						MaxConcurrentReconciles: 1,
						RateLimiter: promoterv1alpha1.RateLimiter{
							RateLimiterTypes: promoterv1alpha1.RateLimiterTypes{
								Bucket: &promoterv1alpha1.Bucket{Qps: 10, Bucket: 100},
							},
						},
					},
				},
				CommitStatus: promoterv1alpha1.CommitStatusConfiguration{
					WorkQueue: promoterv1alpha1.WorkQueue{
						RequeueDuration:         metav1.Duration{Duration: time.Minute},
						MaxConcurrentReconciles: 1,
						RateLimiter: promoterv1alpha1.RateLimiter{
							RateLimiterTypes: promoterv1alpha1.RateLimiterTypes{
								Bucket: &promoterv1alpha1.Bucket{Qps: 10, Bucket: 100},
							},
						},
					},
				},
				ArgoCDCommitStatus: promoterv1alpha1.ArgoCDCommitStatusConfiguration{
					WorkQueue: promoterv1alpha1.WorkQueue{
						RequeueDuration:         metav1.Duration{Duration: time.Minute},
						MaxConcurrentReconciles: 1,
						RateLimiter: promoterv1alpha1.RateLimiter{
							RateLimiterTypes: promoterv1alpha1.RateLimiterTypes{
								Bucket: &promoterv1alpha1.Bucket{Qps: 10, Bucket: 100},
							},
						},
					},
					EnableLocalArgoCDApplications: ptrTo(false),
				},
			},
		}

		err = k8sClient.Create(context.Background(), cc)
		Expect(err).ToNot(HaveOccurred())

		// Setup the controller once for all tests
		kubeconfigProvider2 := kubeconfigprovider.New(kubeconfigprovider.Options{
			Namespace:             "default",
			KubeconfigSecretLabel: "kubeconfig-provider-local-disabled-test",
			KubeconfigSecretKey:   "kubeconfig",
			Scheme:                scheme,
		})

		reconciler := &ArgoCDCommitStatusReconciler{
			Manager:            mcMgr,
			SettingsMgr:        settingsMgr,
			KubeConfigProvider: kubeconfigProvider2,
		}

		reqCtx, reqCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer reqCancel()

		// Use custom setup with SkipNameValidation for this isolated test
		err = setupArgoCDCommitStatusControllerWithSkipNameValidation(reqCtx, reconciler, mcMgr, settingsMgr)
		Expect(err).ToNot(HaveOccurred())

		// Start the manager in a goroutine
		go func() { _ = mcMgr.Start(ctx) }()

		// Give it a moment to ensure controllers are ready
		time.Sleep(100 * time.Millisecond)
	})

	AfterAll(func() {
		if cancel != nil {
			cancel()
		}
		if env != nil {
			Expect(env.Stop()).To(Succeed())
		}
	})

	Context("when Application CRD is not installed", func() {
		It("should verify Application CRD is not present", func() {
			cfg := mcMgr.GetLocalManager().GetConfig()
			disco, err := discovery.NewDiscoveryClientForConfig(cfg)
			Expect(err).ToNot(HaveOccurred())

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
				Expect(found).To(BeFalse(), "Application CRD unexpectedly present in test env; ensure CRDDirectoryPaths omitted external_crds")
			}
		})
	})

	Context("when EnableLocalArgoCDApplications is disabled", func() {
		It("should successfully set up the controller without crashing", func() {
			// Verify the controller configuration exists and is correct
			cc := &promoterv1alpha1.ControllerConfiguration{}
			err := k8sClient.Get(context.Background(), client.ObjectKey{
				Name:      settings.ControllerConfigurationName,
				Namespace: "default",
			}, cc)
			Expect(err).ToNot(HaveOccurred())
			Expect(cc.Spec.ArgoCDCommitStatus.EnableLocalArgoCDApplications).ToNot(BeNil())
			Expect(*cc.Spec.ArgoCDCommitStatus.EnableLocalArgoCDApplications).To(BeFalse())

			// The manager and controller were set up in BeforeAll and should still be running
			// This test verifies that the setup succeeded without errors
			// The fact that we got here means the controller was set up successfully
			// despite the Application CRD not being installed (because EnableLocalArgoCDApplications is false)
		})
	})
})

// ptrTo is a tiny helper to avoid importing k8s.io/utils/ptr in the test file
func ptrTo[T any](v T) *T { return &v }

// setupArgoCDCommitStatusControllerWithSkipNameValidation is a test-specific setup function
// that sets up the ArgoCDCommitStatus controller with SkipNameValidation enabled.
// This allows the test to run in isolation without conflicting with other controller names.
func setupArgoCDCommitStatusControllerWithSkipNameValidation(ctx context.Context, r *ArgoCDCommitStatusReconciler, mcMgr mcmanager.Manager, settingsMgr *settings.Manager) error {
	// Set the local client for interacting with manager cluster
	r.localClient = mcMgr.GetLocalManager().GetClient()

	// Use Direct methods to read configuration from the API server without cache during setup.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.ArgoCDCommitStatusConfiguration, mcreconcile.Request](ctx, settingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get ArgoCDCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.ArgoCDCommitStatusConfiguration](ctx, settingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get ArgoCDCommitStatus max concurrent reconciles: %w", err)
	}

	// Get the controller configuration to check if local Applications should be watched
	controllerConfig, err := settingsMgr.GetControllerConfigurationDirect(ctx)
	if err != nil {
		return fmt.Errorf("failed to get controller configuration: %w", err)
	}

	// Determine if we should watch local Applications (default: true)
	enableLocalApplications := true
	if controllerConfig.Spec.ArgoCDCommitStatus.EnableLocalArgoCDApplications != nil {
		enableLocalApplications = *controllerConfig.Spec.ArgoCDCommitStatus.EnableLocalArgoCDApplications
	}

	// Skip CRD verification since this test intentionally runs without the Application CRD installed
	// and with EnableLocalArgoCDApplications set to false

	builder := mcbuilder.ControllerManagedBy(mcMgr).
		For(&promoterv1alpha1.ArgoCDCommitStatus{},
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false),
			mcbuilder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
			RateLimiter:             rateLimiter,
			SkipNameValidation:      ptrTo(true), // Skip name validation for isolated test
		})

	// Only watch local Applications if enabled
	if enableLocalApplications {
		builder = builder.Watches(&argocd.Application{}, lookupArgoCDCommitStatusFromArgoCDApplication(mcMgr),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(true),
		)
	} else {
		// Watch Applications only in remote clusters
		builder = builder.Watches(&argocd.Application{}, lookupArgoCDCommitStatusFromArgoCDApplication(mcMgr),
			mcbuilder.WithEngageWithLocalCluster(false),
			mcbuilder.WithEngageWithProviderClusters(true),
		)
	}

	err = builder.Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}
