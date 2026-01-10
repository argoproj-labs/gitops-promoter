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

package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/cluster"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/utils/ptr"

	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/internal/webhookreceiver"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	kubeconfigprovider "sigs.k8s.io/multicluster-runtime/providers/kubeconfig"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sosedoff/gitkit"
	"gopkg.in/yaml.v3"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	//+kubebuilder:scaffold:imports
)

// Shared test constants for branch names used across multiple test files
const (
	testBranchDevelopment     = "environment/development"
	testBranchDevelopmentNext = "environment/development-next"
	testBranchStaging         = "environment/staging"
	testBranchStagingNext     = "environment/staging-next"
	testBranchProduction      = "environment/production"
	testBranchProductionNext  = "environment/production-next"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	cfg                 *rest.Config
	cfgDev              *rest.Config
	cfgStaging          *rest.Config
	k8sClient           client.Client
	k8sClientDev        client.Client
	k8sClientStaging    client.Client
	testEnv             *envtest.Environment
	testEnvDev          *envtest.Environment
	testEnvStaging      *envtest.Environment
	gitServer           *http.Server
	gitStoragePath      string
	cancel              context.CancelFunc
	ctx                 context.Context
	gitServerPort       string
	webhookReceiverPort int
	scheme              = utils.GetScheme()
	enqueueCTP          CTPEnqueueFunc // Function to enqueue CTP reconciliation requests
)

func TestControllers(t *testing.T) {
	t.Parallel()

	RegisterFailHandler(Fail)

	c, _ := GinkgoConfiguration()
	// c.FocusFiles = []string{
	// 	"changetransferpolicy_controller_test.go",
	// 	"pullrequest_controller_test.go",
	// 	"promotionstrategy_controller_test.go",
	// }
	// GinkgoWriter.TeeTo(os.Stdout)

	RunSpecs(t, "Controller Suite", c)
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true), zap.Level(zapcore.Level(-4)), func(o *zap.Options) {
		o.TimeEncoder = zapcore.RFC3339NanoTimeEncoder
	}))
	var err error

	By("setting up git server")
	var errMkDir error
	gitStoragePath, errMkDir = os.MkdirTemp("", "*")
	if errMkDir != nil {
		panic("could not make temp dir for repo server")
	}
	gitServerPort, gitServer = startGitServer(gitStoragePath)
	logf.Log.Info("Git server started on port", "port", gitServerPort)
	logf.Log.Info("Git storage path", "path", gitStoragePath)

	By("bootstrapping test environments")
	// Create a local test environment to test the single cluster functionality
	testEnv, cfg, k8sClient = createAndStartTestEnv()

	// Create a dev and staging test environment to test the multi cluster functionality
	// for watching argocd applications in the other clusters
	testEnvDev, cfgDev, k8sClientDev = createAndStartTestEnv()
	testEnvStaging, cfgStaging, k8sClientStaging = createAndStartTestEnv()

	// kubeconfig provider
	kubeconfigProvider := kubeconfigprovider.New(kubeconfigprovider.Options{
		Namespace:             constants.KubeconfigSecretNamespace,
		KubeconfigSecretLabel: constants.KubeconfigSecretLabel,
		KubeconfigSecretKey:   constants.KubeconfigSecretKey,
		ClusterOptions: []cluster.Option{
			func(clusterOptions *cluster.Options) {
				clusterOptions.Scheme = scheme
			},
		},
	})

	//nolint:fatcontext
	ctx, cancel = context.WithCancel(context.Background())

	// Create kubeconfig secret for dev and staging test environments in the local cluster
	// Secrets used by the kubeconfig provider controller to access the other clusters
	err = createKubeconfigSecret(ctx, "testenv-dev", constants.KubeconfigSecretNamespace, cfgDev, k8sClient)
	Expect(err).NotTo(HaveOccurred())

	err = createKubeconfigSecret(ctx, "testenv-staging", constants.KubeconfigSecretNamespace, cfgStaging, k8sClient)
	Expect(err).NotTo(HaveOccurred())

	multiClusterManager, err := mcmanager.New(cfg, kubeconfigProvider, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
	})
	Expect(err).ToNot(HaveOccurred())

	// Setup kubeconfig provider controller with manager
	err = kubeconfigProvider.SetupWithManager(ctx, multiClusterManager)
	Expect(err).ToNot(HaveOccurred())

	k8sManager := multiClusterManager.GetLocalManager()

	controllerConfiguration := &promoterv1alpha1.ControllerConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "promoter-controller-configuration",
			Namespace: "default",
		},
		Spec: promoterv1alpha1.ControllerConfigurationSpec{
			PromotionStrategy: promoterv1alpha1.PromotionStrategyConfiguration{
				WorkQueue: promoterv1alpha1.WorkQueue{
					RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
					MaxConcurrentReconciles: 10,
					RateLimiter: promoterv1alpha1.RateLimiter{
						MaxOf: []promoterv1alpha1.RateLimiterTypes{
							{
								Bucket: &promoterv1alpha1.Bucket{
									Qps:    10,
									Bucket: 100,
								},
							},
							{
								ExponentialFailure: &promoterv1alpha1.ExponentialFailure{
									BaseDelay: metav1.Duration{Duration: time.Millisecond * 5},
									MaxDelay:  metav1.Duration{Duration: time.Minute * 1},
								},
							},
						},
					},
				},
			},
			ChangeTransferPolicy: promoterv1alpha1.ChangeTransferPolicyConfiguration{
				WorkQueue: promoterv1alpha1.WorkQueue{
					RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
					MaxConcurrentReconciles: 10,
					RateLimiter: promoterv1alpha1.RateLimiter{
						MaxOf: []promoterv1alpha1.RateLimiterTypes{
							{
								Bucket: &promoterv1alpha1.Bucket{
									Qps:    10,
									Bucket: 100,
								},
							},
							{
								ExponentialFailure: &promoterv1alpha1.ExponentialFailure{
									BaseDelay: metav1.Duration{Duration: time.Millisecond * 5},
									MaxDelay:  metav1.Duration{Duration: time.Minute * 1},
								},
							},
						},
					},
				},
			},
			PullRequest: promoterv1alpha1.PullRequestConfiguration{
				Template: promoterv1alpha1.PullRequestTemplate{
					Title:       "Promote {{ trunc 7 .ChangeTransferPolicy.Status.Proposed.Dry.Sha }} to `{{ .ChangeTransferPolicy.Spec.ActiveBranch }}`",
					Description: "This PR is promoting the environment branch `{{ .ChangeTransferPolicy.Spec.ActiveBranch }}` which is currently on dry sha {{ .ChangeTransferPolicy.Status.Active.Dry.Sha }} to dry sha {{ .ChangeTransferPolicy.Status.Proposed.Dry.Sha }}.",
				},
				WorkQueue: promoterv1alpha1.WorkQueue{
					RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
					MaxConcurrentReconciles: 10,
					RateLimiter: promoterv1alpha1.RateLimiter{
						MaxOf: []promoterv1alpha1.RateLimiterTypes{
							{
								Bucket: &promoterv1alpha1.Bucket{
									Qps:    10,
									Bucket: 100,
								},
							},
							{
								ExponentialFailure: &promoterv1alpha1.ExponentialFailure{
									BaseDelay: metav1.Duration{Duration: time.Millisecond * 5},
									MaxDelay:  metav1.Duration{Duration: time.Minute * 1},
								},
							},
						},
					},
				},
			},
			CommitStatus: promoterv1alpha1.CommitStatusConfiguration{
				WorkQueue: promoterv1alpha1.WorkQueue{
					RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
					MaxConcurrentReconciles: 10,
					RateLimiter: promoterv1alpha1.RateLimiter{
						MaxOf: []promoterv1alpha1.RateLimiterTypes{
							{
								Bucket: &promoterv1alpha1.Bucket{
									Qps:    10,
									Bucket: 100,
								},
							},
							{
								ExponentialFailure: &promoterv1alpha1.ExponentialFailure{
									BaseDelay: metav1.Duration{Duration: time.Millisecond * 5},
									MaxDelay:  metav1.Duration{Duration: time.Minute * 1},
								},
							},
						},
					},
				},
			},
			ArgoCDCommitStatus: promoterv1alpha1.ArgoCDCommitStatusConfiguration{
				WatchLocalApplications: true,
				WorkQueue: promoterv1alpha1.WorkQueue{
					RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
					MaxConcurrentReconciles: 10,
					RateLimiter: promoterv1alpha1.RateLimiter{
						MaxOf: []promoterv1alpha1.RateLimiterTypes{
							{
								Bucket: &promoterv1alpha1.Bucket{
									Qps:    10,
									Bucket: 100,
								},
							},
							{
								ExponentialFailure: &promoterv1alpha1.ExponentialFailure{
									BaseDelay: metav1.Duration{Duration: time.Millisecond * 5},
									MaxDelay:  metav1.Duration{Duration: time.Minute * 1},
								},
							},
						},
					},
				},
			},
			TimedCommitStatus: promoterv1alpha1.TimedCommitStatusConfiguration{
				WorkQueue: promoterv1alpha1.WorkQueue{
					RequeueDuration:         metav1.Duration{Duration: time.Second * 1},
					MaxConcurrentReconciles: 10,
					RateLimiter: promoterv1alpha1.RateLimiter{
						MaxOf: []promoterv1alpha1.RateLimiterTypes{
							{
								Bucket: &promoterv1alpha1.Bucket{
									Qps:    10,
									Bucket: 100,
								},
							},
							{
								ExponentialFailure: &promoterv1alpha1.ExponentialFailure{
									BaseDelay: metav1.Duration{Duration: time.Millisecond * 5},
									MaxDelay:  metav1.Duration{Duration: time.Minute * 1},
								},
							},
						},
					},
				},
			},
			GitCommitStatus: promoterv1alpha1.GitCommitStatusConfiguration{
				WorkQueue: promoterv1alpha1.WorkQueue{
					RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
					MaxConcurrentReconciles: 10,
					RateLimiter: promoterv1alpha1.RateLimiter{
						MaxOf: []promoterv1alpha1.RateLimiterTypes{
							{
								Bucket: &promoterv1alpha1.Bucket{
									Qps:    10,
									Bucket: 100,
								},
							},
							{
								ExponentialFailure: &promoterv1alpha1.ExponentialFailure{
									BaseDelay: metav1.Duration{Duration: time.Millisecond * 5},
									MaxDelay:  metav1.Duration{Duration: time.Minute * 1},
								},
							},
						},
					},
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, controllerConfiguration)).To(Succeed())

	settingsMgr := settings.NewManager(k8sManager.GetClient(), k8sManager.GetAPIReader(), settings.ManagerConfig{
		ControllerNamespace: "default",
	})

	// ChangeTransferPolicy controller must be set up first so we can
	// get the enqueue function to pass to other controllers.
	ctpReconciler := &ChangeTransferPolicyReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		Recorder:    k8sManager.GetEventRecorderFor("ChangeTransferPolicy"),
		SettingsMgr: settingsMgr,
	}
	err = ctpReconciler.SetupWithManager(ctx, k8sManager)
	Expect(err).ToNot(HaveOccurred())

	// Store the enqueue function globally so tests can trigger CTP reconciliation
	enqueueCTP = ctpReconciler.GetEnqueueFunc()

	err = (&CommitStatusReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		Recorder:    k8sManager.GetEventRecorderFor("CommitStatus"),
		SettingsMgr: settingsMgr,
		EnqueueCTP:  ctpReconciler.GetEnqueueFunc(),
	}).SetupWithManager(ctx, k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&TimedCommitStatusReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		Recorder:    k8sManager.GetEventRecorderFor("TimedCommitStatus"),
		SettingsMgr: settingsMgr,
		EnqueueCTP:  ctpReconciler.GetEnqueueFunc(),
	}).SetupWithManager(ctx, k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&PromotionStrategyReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		Recorder:    k8sManager.GetEventRecorderFor("PromotionStrategy"),
		SettingsMgr: settingsMgr,
		EnqueueCTP:  ctpReconciler.GetEnqueueFunc(),
	}).SetupWithManager(ctx, k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&PullRequestReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		Recorder:    k8sManager.GetEventRecorderFor("PullRequest"),
		SettingsMgr: settingsMgr,
	}).SetupWithManager(ctx, k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&RevertCommitReconciler{
		Client:   k8sManager.GetClient(),
		Scheme:   k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorderFor("RevertCommit"),
	}).SetupWithManager(ctx, k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&ScmProviderReconciler{
		Client:   k8sManager.GetClient(),
		Scheme:   k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorderFor("ScmProvider"),
	}).SetupWithManager(ctx, k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&GitRepositoryReconciler{
		Client:   k8sManager.GetClient(),
		Scheme:   k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorderFor("GitRepository"),
	}).SetupWithManager(ctx, k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&ClusterScmProviderReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		Recorder:    k8sManager.GetEventRecorderFor("ClusterScmProvider"),
		SettingsMgr: settingsMgr,
	}).SetupWithManager(ctx, k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&ArgoCDCommitStatusReconciler{
		Manager:            multiClusterManager,
		SettingsMgr:        settingsMgr,
		KubeConfigProvider: kubeconfigProvider,
		Recorder:           k8sManager.GetEventRecorderFor("ArgoCDCommitStatus"),
	}).SetupWithManager(ctx, multiClusterManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&GitCommitStatusReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		Recorder:    k8sManager.GetEventRecorderFor("GitCommitStatus"),
		SettingsMgr: settingsMgr,
		EnqueueCTP:  ctpReconciler.GetEnqueueFunc(),
	}).SetupWithManager(ctx, k8sManager)
	Expect(err).ToNot(HaveOccurred())

	webhookReceiverPort = constants.WebhookReceiverPort + GinkgoParallelProcess()
	whr := webhookreceiver.NewWebhookReceiver(k8sManager, webhookreceiver.EnqueueFunc(ctpReconciler.GetEnqueueFunc()))
	go func() {
		err = whr.Start(ctx, fmt.Sprintf(":%d", webhookReceiverPort))
		Expect(err).ToNot(HaveOccurred(), "failed to start webhook receiver")
	}()

	go func() {
		defer GinkgoRecover()
		err = multiClusterManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()

	// Wait for the manager's cache to sync before running tests
	// This ensures that watch handlers are ready and won't miss early resource creation events
	By("waiting for cache to sync")
	cache := k8sManager.GetCache()
	Eventually(func() bool {
		return cache.WaitForCacheSync(ctx)
	}, constants.EventuallyTimeout).Should(BeTrue(), "k8sManager cache should sync")

	cache = multiClusterManager.GetLocalManager().GetCache()
	Eventually(func() bool {
		return cache.WaitForCacheSync(ctx)
	}, constants.EventuallyTimeout).Should(BeTrue(), "local cache should sync")

	// Wait for kubeconfig provider to discover remote clusters
	Eventually(kubeconfigProvider.ListClusters, constants.EventuallyTimeout).Should(HaveLen(2))

	// Wait for remote cluster caches to sync as well
	By("waiting for remote cluster caches to sync")
	for _, clusterName := range kubeconfigProvider.ListClusters() {
		cluster, err := multiClusterManager.GetCluster(ctx, clusterName)
		Expect(err).ToNot(HaveOccurred(), "should be able to get cluster %s", clusterName)
		Eventually(func() bool {
			return cluster.GetCache().WaitForCacheSync(ctx)
		}, constants.EventuallyTimeout).Should(BeTrue(), "cache for cluster %s should sync", clusterName)
	}

	// Wait for ArgoCDCommitStatus informer to be ready
	// The general cache sync above only ensures Application informers are ready (from Watches()).
	// We need to explicitly verify the ArgoCDCommitStatus informer (from For()) is ready.
	// This informer is created during mgr.Engage(), which happens AFTER setCluster() adds
	// the cluster to ListClusters(). There's a small window where ListClusters() shows the
	// cluster but Engage() hasn't completed yet, meaning the ArgoCDCommitStatus informer
	// doesn't exist. This wait ensures Engage() has completed and the informer is usable.
	//
	// IMPORTANT: We must use the multiClusterManager's cached client here, not k8sClient.
	// k8sClient is a direct API client (created via client.New()) that bypasses caches entirely.
	// Using the cached client ensures List() only succeeds when the informer actually exists.
	By("waiting for ArgoCDCommitStatus informer to be ready")
	Eventually(func() error {
		list := &promoterv1alpha1.ArgoCDCommitStatusList{}
		return multiClusterManager.GetLocalManager().GetClient().List(ctx, list)
	}, constants.EventuallyTimeout, 100*time.Millisecond).Should(Succeed(),
		"ArgoCDCommitStatus informer should be ready before tests run")
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")

	cancel() // stops manager and anything else using the context
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())

	err = testEnvDev.Stop()
	Expect(err).NotTo(HaveOccurred())

	err = testEnvStaging.Stop()
	Expect(err).NotTo(HaveOccurred())

	_ = gitServer.Shutdown(context.Background())

	err = os.RemoveAll(gitStoragePath)
	Expect(err).NotTo(HaveOccurred())
})

type filterLogger struct{}

func (f *filterLogger) Write(p []byte) (n int, err error) {
	if strings.Contains(string(p), "request:") {
		return len(p), nil
	}
	// Write directly to stdout instead of using log.Print to avoid recursive mutex lock
	_, _ = os.Stdout.Write(p)
	return len(p), nil
}

func startGitServer(gitStoragePath string) (string, *http.Server) {
	hooks := &gitkit.HookScripts{
		PreReceive: `echo "Hello World!"`,
	}

	// Configure git service
	service := gitkit.New(gitkit.Config{
		Dir:        gitStoragePath,
		AutoCreate: true,
		AutoHooks:  true,
		Hooks:      hooks,
	})

	if err := service.Setup(); err != nil {
		log.Fatal(err)
	}

	gitServerPort := 5000 + GinkgoParallelProcess()
	gitServerPortStr := strconv.Itoa(gitServerPort)
	server := &http.Server{Addr: ":" + gitServerPortStr, Handler: service}

	// Disables logging for gitkit
	// log.SetOutput(io.Discard)
	gitKitFilterLogger := &filterLogger{}
	log.SetOutput(gitKitFilterLogger)

	go func() {
		// Start HTTP server
		if err := server.ListenAndServe(); err != nil {
			fmt.Println(err)
		}
		fmt.Println("Git server exited")
	}()

	return gitServerPortStr, server
}

func setupInitialTestGitRepoWithoutActiveMetadata(owner string, name string) {
	gitPath, err := os.MkdirTemp("", "*")
	if err != nil {
		panic("could not make temp dir for repo server")
	}
	defer func() {
		err := os.RemoveAll(gitPath)
		if err != nil {
			fmt.Println(err, "failed to remove temp dir")
		}
	}()

	_, err = runGitCmd(ctx, gitPath, "clone", fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPort, owner, name), ".")
	Expect(err).NotTo(HaveOccurred())

	_, err = runGitCmd(ctx, gitPath, "config", "user.name", "testuser")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, gitPath, "config", "user.email", "testemail@test.com")
	Expect(err).NotTo(HaveOccurred())

	_, err = runGitCmd(ctx, gitPath, "commit", "--allow-empty", "-m", "init commit")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, gitPath, "push")
	Expect(err).NotTo(HaveOccurred())

	defaultBranch, err := runGitCmd(ctx, gitPath, "rev-parse", "--abbrev-ref", "HEAD")
	Expect(err).NotTo(HaveOccurred())
	defaultBranch = strings.TrimSpace(defaultBranch)

	sha, err := runGitCmd(ctx, gitPath, "rev-parse", defaultBranch)
	Expect(err).NotTo(HaveOccurred())

	for _, environment := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
		_, err = runGitCmd(ctx, gitPath, "checkout", "--orphan", environment)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(ctx, gitPath, "rm", "-rf", "--ignore-unmatch", ".")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(ctx, gitPath, "commit", "--allow-empty", "-m", "initial commit")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(ctx, gitPath, "push", "-u", "origin", environment)
		Expect(err).NotTo(HaveOccurred())

		// Sleep one seconds to differentiate the commits to prevent same hash
		time.Sleep(1 * time.Second)

		_, err = runGitCmd(ctx, gitPath, "checkout", "-b", environment+"-next")
		Expect(err).NotTo(HaveOccurred())
		f, err := os.Create(path.Join(gitPath, "hydrator.metadata"))
		Expect(err).NotTo(HaveOccurred())
		str := fmt.Sprintf("{\"drySHA\": \"%s\"}", strings.TrimSpace(sha))
		_, err = f.WriteString(str)
		Expect(err).NotTo(HaveOccurred())
		err = f.Close()
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(ctx, gitPath, "add", "hydrator.metadata")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(ctx, gitPath, "commit", "-m", "initial commit next")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(ctx, gitPath, "push", "-u", "origin", environment+"-next")
		Expect(err).NotTo(HaveOccurred())

		// Sleep one seconds to differentiate the commits to prevent same hash
		time.Sleep(1 * time.Second)
	}
}

func setupInitialTestGitRepoOnServer(ctx context.Context, owner string, name string) {
	gitPath, err := os.MkdirTemp("", "*")
	if err != nil {
		panic("could not make temp dir for repo server")
	}
	defer func() {
		err := os.RemoveAll(gitPath)
		if err != nil {
			fmt.Println(err, "failed to remove temp dir")
		}
	}()

	_, err = runGitCmd(ctx, gitPath, "clone", fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPort, owner, name), ".")
	Expect(err).NotTo(HaveOccurred())

	_, err = runGitCmd(ctx, gitPath, "config", "user.name", "testuser")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, gitPath, "config", "user.email", "testemail@test.com")
	Expect(err).NotTo(HaveOccurred())

	f, err := os.Create(path.Join(gitPath, "hydrator.metadata"))
	Expect(err).NotTo(HaveOccurred())
	_, err = f.WriteString("{\"drySHA\": \"n/a\"}")
	Expect(err).NotTo(HaveOccurred())
	err = f.Close()
	Expect(err).NotTo(HaveOccurred())

	_, err = runGitCmd(ctx, gitPath, "add", "hydrator.metadata")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, gitPath, "commit", "-m", "init commit dry side n/a dry sha")
	Expect(err).NotTo(HaveOccurred())

	defaultBranch, err := runGitCmd(ctx, gitPath, "rev-parse", "--abbrev-ref", "HEAD")
	Expect(err).NotTo(HaveOccurred())
	defaultBranch = strings.TrimSpace(defaultBranch)

	sha, err := runGitCmd(ctx, gitPath, "rev-parse", defaultBranch)
	Expect(err).NotTo(HaveOccurred())
	f, err = os.Create(path.Join(gitPath, "hydrator.metadata"))
	Expect(err).NotTo(HaveOccurred())
	str := fmt.Sprintf("{\"drySHA\": \"%s\"}", strings.TrimSpace(sha))
	_, err = f.WriteString(str)
	Expect(err).NotTo(HaveOccurred())
	err = f.Close()
	Expect(err).NotTo(HaveOccurred())

	_, err = runGitCmd(ctx, gitPath, "add", "hydrator.metadata")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, gitPath, "commit", "-m", "second commit with real dry sha")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, gitPath, "push")
	Expect(err).NotTo(HaveOccurred())

	for _, environment := range []string{"environment/development-next", "environment/staging-next", "environment/production-next"} {
		_, err = runGitCmd(ctx, gitPath, "checkout", "--orphan", environment)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(ctx, gitPath, "commit", "--allow-empty", "-m", "initial empty commit for "+environment)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(ctx, gitPath, "push", "-u", "origin", environment)
		Expect(err).NotTo(HaveOccurred())

		// Sleep one seconds to differentiate the commits to prevent same hash
		time.Sleep(1 * time.Second)

		activeB, _ := strings.CutSuffix(environment, "-next")
		_, err = runGitCmd(ctx, gitPath, "checkout", "-b", activeB)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(ctx, gitPath, "push", "-u", "origin", activeB)
		Expect(err).NotTo(HaveOccurred())

		// Sleep one seconds to differentiate the commits to prevent same hash
		time.Sleep(1 * time.Second)
	}
	GinkgoLogr.Info("Git repository initialized", "path", gitPath)
}

func makeChangeAndHydrateRepo(gitPath string, repoOwner string, repoName string, dryCommitMessage string, hydratedCommitMessage string) (string, string) {
	repoURL := fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPort, repoOwner, repoName)
	_, err := runGitCmd(ctx, gitPath, "clone", "--verbose", "--progress", "--filter=blob:none", repoURL, ".")
	Expect(err).NotTo(HaveOccurred())

	_, err = runGitCmd(ctx, gitPath, "config", "user.name", "testuser")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, gitPath, "config", "user.email", "testmail@test.com")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, gitPath, "config", "pull.rebase", "false")
	Expect(err).NotTo(HaveOccurred())

	for _, environment := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction, "environment/development-next", "environment/staging-next", "environment/production-next"} {
		_, err = runGitCmd(ctx, gitPath, "checkout", "-B", environment, "origin/"+environment)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(ctx, gitPath, "pull")
		Expect(err).NotTo(HaveOccurred())
	}

	defaultBranch, err := runGitCmd(ctx, gitPath, "rev-parse", "--abbrev-ref", "origin/HEAD")
	Expect(err).NotTo(HaveOccurred())
	defaultBranch, _ = strings.CutPrefix(strings.TrimSpace(defaultBranch), "origin/")

	_, err = runGitCmd(ctx, gitPath, "checkout", defaultBranch)
	Expect(err).NotTo(HaveOccurred())

	// Get the SHA before we make changes - this is the "before" SHA for the webhook
	beforeSha, err := runGitCmd(ctx, gitPath, "rev-parse", defaultBranch)
	Expect(err).NotTo(HaveOccurred())
	beforeSha = strings.TrimSpace(beforeSha)

	f, err := os.Create(path.Join(gitPath, "manifests-fake.yaml"))
	Expect(err).NotTo(HaveOccurred())
	str := fmt.Sprintf("{\"time\": \"%s\"}", time.Now().Format(time.RFC3339Nano))
	_, err = f.WriteString(str)
	Expect(err).NotTo(HaveOccurred())
	err = f.Close()
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, gitPath, "add", "manifests-fake.yaml")
	Expect(err).NotTo(HaveOccurred())
	if dryCommitMessage == "" {
		dryCommitMessage = "added fake manifests commit with timestamp"
	}
	_, err = runGitCmd(ctx, gitPath, "commit", "-m", dryCommitMessage)
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, gitPath, "push", "-u", "origin", defaultBranch)
	Expect(err).NotTo(HaveOccurred())

	// Send webhook after push with the "before" SHA that the CTP knows about
	sendWebhookForPush(ctx, beforeSha, defaultBranch)

	sha, err := runGitCmd(ctx, gitPath, "rev-parse", defaultBranch)
	Expect(err).NotTo(HaveOccurred())
	sha = strings.TrimSpace(sha)
	shortSha, err := runGitCmd(ctx, gitPath, "rev-parse", "--short=7", defaultBranch)
	Expect(err).NotTo(HaveOccurred())
	shortSha = strings.TrimSpace(shortSha)

	for _, environment := range []string{"environment/development-next", "environment/staging-next", "environment/production-next"} {
		_, err = runGitCmd(ctx, gitPath, "checkout", "-B", environment, "origin/"+environment)
		Expect(err).NotTo(HaveOccurred())

		// Get the SHA before we make changes - this is the "before" SHA for the webhook
		beforeBranchSha, err := runGitCmd(ctx, gitPath, "rev-parse", environment)
		Expect(err).NotTo(HaveOccurred())
		beforeBranchSha = strings.TrimSpace(beforeBranchSha)

		var subject string
		var body string
		parts := strings.SplitN(dryCommitMessage, "\n\n", 2)
		subject = parts[0]
		if len(parts) > 1 {
			body = parts[1]
		}

		metadata := git.HydratorMetadata{
			RepoURL: "", // This is not used anywhere, we use the SCM provider's HTTPS URL instead
			DrySha:  sha,
			Author:  "testuser <testmail@test.com>",
			Date:    metav1.Now(),
			Subject: subject,
			Body:    body,
			References: []promoterv1alpha1.RevisionReference{
				{
					Commit: &promoterv1alpha1.CommitMetadata{
						Author:  "upstream <upstream@example.com>",
						Date:    ptr.To(metav1.Now()),
						Subject: "This is a fix for an upstream issue",
						Body:    "This is a body of the commit",
						Sha:     "c4c862564afe56abf8cc8ac683eee3dc8bf96108",
						RepoURL: "https://github.com/upstream/repo",
					},
				},
			},
		}
		m, err := json.MarshalIndent(metadata, "", "\t")
		Expect(err).NotTo(HaveOccurred())

		f, err = os.Create(path.Join(gitPath, "hydrator.metadata"))
		Expect(err).NotTo(HaveOccurred())
		_, err = f.Write(m)
		Expect(err).NotTo(HaveOccurred())
		err = f.Close()
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(ctx, gitPath, "add", "hydrator.metadata")
		Expect(err).NotTo(HaveOccurred())

		f, err = os.Create(path.Join(gitPath, "manifests-fake.yaml"))
		Expect(err).NotTo(HaveOccurred())
		str := fmt.Sprintf("{\"time\": \"%s\"}", time.Now().Format(time.RFC3339Nano))
		_, err = f.WriteString(str)
		Expect(err).NotTo(HaveOccurred())
		err = f.Close()
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(ctx, gitPath, "add", "manifests-fake.yaml")
		Expect(err).NotTo(HaveOccurred())
		if hydratedCommitMessage == "" {
			_, err = runGitCmd(ctx, gitPath, "commit", "-m", "added pending commit from dry sha, "+sha+" from environment "+strings.TrimRight(environment, "-next"))
		} else {
			_, err = runGitCmd(ctx, gitPath, "commit", "-m", hydratedCommitMessage)
		}
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(ctx, gitPath, "push", "-u", "origin", environment)
		Expect(err).NotTo(HaveOccurred())

		// Send webhook after push with the "before" SHA that the CTP knows about
		sendWebhookForPush(ctx, beforeBranchSha, environment)

		// Sleep one seconds to differentiate the commits to prevent same hash
		time.Sleep(1 * time.Second)
	}

	return sha, shortSha
}

func runGitCmd(ctx context.Context, directory string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	cmd.Dir = directory

	cmd.Env = []string{
		"GIT_TERMINAL_PROMPT=0",
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start git command: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		if strings.Contains(stderrBuf.String(), "already exists and is not an empty directory") ||
			strings.Contains(stdoutBuf.String(), "nothing to commit, working tree clean") {
			return "", nil
		}
		return "", fmt.Errorf("failed to run git command: %s", stderrBuf.String())
	}

	return stdoutBuf.String(), nil
}

//nolint:unparam // length parameter is intentionally flexible for future use
func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)

	for i := range result {
		result[i] = charset[rand.Intn(len(charset))]
	}

	return string(result)
}

// buildGitHubWebhookPayload constructs a GitHub webhook payload for push events
func buildGitHubWebhookPayload(beforeSha, ref string) string {
	payload := map[string]any{
		"before": beforeSha,
		"ref":    ref,
		"pusher": map[string]any{
			"name":  "test-user",
			"email": "test@example.com",
		},
	}
	payloadBytes, err := json.Marshal(payload)
	Expect(err).NotTo(HaveOccurred())
	return string(payloadBytes)
}

// sendWebhookForPush sends a webhook after a git push to simulate SCM provider behavior
func sendWebhookForPush(ctx context.Context, sha, branch string) {
	// Build GitHub-style webhook payload
	payload := buildGitHubWebhookPayload(sha, "refs/heads/"+branch)

	// Send the webhook request
	webhookURL := fmt.Sprintf("http://localhost:%d/", webhookReceiverPort)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewBufferString(payload))
	if err != nil {
		// Don't fail the test if webhook fails - log it instead
		fmt.Printf("Failed to create webhook request: %v\n", err)
		return
	}

	// Set GitHub webhook headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Github-Event", "push")
	req.Header.Set("X-Github-Delivery", fmt.Sprintf("test-delivery-%d", time.Now().Unix()))

	// Send the request
	httpClient := &http.Client{Timeout: 5 * time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		// Don't fail the test if webhook fails - log it instead
		fmt.Printf("Failed to send webhook request: %v\n", err)
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusNoContent {
		fmt.Printf("Webhook receiver returned unexpected status code: %d\n", resp.StatusCode)
	}
}

// cloneTestRepo clones the test repo and configures git user. Returns the temp directory path.
func cloneTestRepo(ctx context.Context, repoName string) (gitPath string, err error) {
	gitPath, err = os.MkdirTemp("", "*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}

	repoURL := fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPort, repoName, repoName)
	_, err = runGitCmd(ctx, gitPath, "clone", "--verbose", "--progress", "--filter=blob:none", repoURL, ".")
	if err != nil {
		_ = os.RemoveAll(gitPath)
		return "", fmt.Errorf("failed to clone: %w", err)
	}

	_, err = runGitCmd(ctx, gitPath, "config", "user.name", "testuser")
	if err != nil {
		_ = os.RemoveAll(gitPath)
		return "", fmt.Errorf("failed to set user.name: %w", err)
	}

	_, err = runGitCmd(ctx, gitPath, "config", "user.email", "testmail@test.com")
	if err != nil {
		_ = os.RemoveAll(gitPath)
		return "", fmt.Errorf("failed to set user.email: %w", err)
	}

	return gitPath, nil
}

// makeDryCommit creates a new commit on the default branch (main) and returns the dry SHA.
// This simulates a developer pushing a change to the dry/source branch.
func makeDryCommit(ctx context.Context, gitPath, commitMessage string) (drySha string, err error) {
	// Fetch latest
	_, err = runGitCmd(ctx, gitPath, "fetch", "origin")
	if err != nil {
		return "", fmt.Errorf("failed to fetch: %w", err)
	}

	// Get default branch
	defaultBranch, err := runGitCmd(ctx, gitPath, "rev-parse", "--abbrev-ref", "origin/HEAD")
	if err != nil {
		return "", fmt.Errorf("failed to get default branch: %w", err)
	}
	defaultBranch = strings.TrimSpace(strings.TrimPrefix(defaultBranch, "origin/"))

	_, err = runGitCmd(ctx, gitPath, "checkout", defaultBranch)
	if err != nil {
		return "", fmt.Errorf("failed to checkout %s: %w", defaultBranch, err)
	}

	// Get the SHA before we make changes - this is the "before" SHA for the webhook
	beforeSha, err := runGitCmd(ctx, gitPath, "rev-parse", defaultBranch)
	if err != nil {
		return "", fmt.Errorf("failed to get before SHA: %w", err)
	}
	beforeSha = strings.TrimSpace(beforeSha)

	// Create a unique change
	manifestContent := fmt.Sprintf("{\"time\": \"%s\"}", time.Now().Format(time.RFC3339Nano))
	manifestPath := path.Join(gitPath, "manifests-fake.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0o644); err != nil {
		return "", fmt.Errorf("failed to write manifests-fake.yaml: %w", err)
	}

	_, err = runGitCmd(ctx, gitPath, "add", "manifests-fake.yaml")
	if err != nil {
		return "", fmt.Errorf("failed to add files: %w", err)
	}

	if commitMessage == "" {
		commitMessage = "dry commit with timestamp"
	}
	_, err = runGitCmd(ctx, gitPath, "commit", "-m", commitMessage)
	if err != nil {
		return "", fmt.Errorf("failed to commit: %w", err)
	}

	_, err = runGitCmd(ctx, gitPath, "push", "-u", "origin", defaultBranch)
	if err != nil {
		return "", fmt.Errorf("failed to push: %w", err)
	}

	// Get the new dry SHA
	drySha, err = runGitCmd(ctx, gitPath, "rev-parse", defaultBranch)
	if err != nil {
		return "", fmt.Errorf("failed to get dry SHA: %w", err)
	}
	drySha = strings.TrimSpace(drySha)

	// Send webhook for the branch
	sendWebhookForPush(ctx, beforeSha, defaultBranch)

	return drySha, nil
}

// hydrateEnvironment hydrates a single environment branch with a new commit containing
// hydrator.metadata and a git note. This simulates what a hydrator does.
// Returns the hydrated commit SHA.
func hydrateEnvironment(ctx context.Context, gitPath, branch, drySha, commitMessage string) error {
	// Fetch latest and checkout the branch
	_, err := runGitCmd(ctx, gitPath, "fetch", "origin")
	if err != nil {
		return fmt.Errorf("failed to fetch: %w", err)
	}

	_, err = runGitCmd(ctx, gitPath, "checkout", "-B", branch, "origin/"+branch)
	if err != nil {
		return fmt.Errorf("failed to checkout branch %s: %w", branch, err)
	}

	// Get the SHA before we make changes - this is the "before" SHA for the webhook
	beforeSha, err := runGitCmd(ctx, gitPath, "rev-parse", branch)
	if err != nil {
		return fmt.Errorf("failed to get before SHA: %w", err)
	}
	beforeSha = strings.TrimSpace(beforeSha)

	// Create hydrator.metadata
	metadata := git.HydratorMetadata{
		DrySha:  drySha,
		Author:  "testuser <testmail@test.com>",
		Date:    metav1.Now(),
		Subject: commitMessage,
	}
	m, err := json.MarshalIndent(metadata, "", "\t")
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	metadataPath := path.Join(gitPath, "hydrator.metadata")
	if err := os.WriteFile(metadataPath, m, 0o644); err != nil {
		return fmt.Errorf("failed to write hydrator.metadata: %w", err)
	}

	// Update manifests with unique content
	manifestContent := fmt.Sprintf("{\"time\": \"%s\"}", time.Now().Format(time.RFC3339Nano))
	manifestPath := path.Join(gitPath, "manifests-fake.yaml")
	if err := os.WriteFile(manifestPath, []byte(manifestContent), 0o644); err != nil {
		return fmt.Errorf("failed to write manifests-fake.yaml: %w", err)
	}

	// Commit and push
	_, err = runGitCmd(ctx, gitPath, "add", "-A")
	if err != nil {
		return fmt.Errorf("failed to add files: %w", err)
	}

	if commitMessage == "" {
		commitMessage = fmt.Sprintf("hydrate %s for dry sha %s", branch, drySha)
	}
	_, err = runGitCmd(ctx, gitPath, "commit", "-m", commitMessage)
	if err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	_, err = runGitCmd(ctx, gitPath, "push", "-u", "origin", branch)
	if err != nil {
		return fmt.Errorf("failed to push: %w", err)
	}

	// Get the new hydrated SHA
	hydratedSha, err := runGitCmd(ctx, gitPath, "rev-parse", branch)
	if err != nil {
		return fmt.Errorf("failed to get hydrated SHA: %w", err)
	}
	hydratedSha = strings.TrimSpace(hydratedSha)

	// Add git note
	if err := pushGitNote(ctx, gitPath, hydratedSha, drySha); err != nil {
		return err
	}

	// Send webhook for the branch
	sendWebhookForPush(ctx, beforeSha, branch)

	return nil
}

// pushGitNote adds a git note to a commit and pushes it to origin.
func pushGitNote(ctx context.Context, gitPath, commitSha, drySha string) error {
	noteContent := fmt.Sprintf(`{"drySha": "%s"}`, drySha)
	_, err := runGitCmd(ctx, gitPath, "notes", "--ref="+git.HydratorNotesRef, "add", "-f", "-m", noteContent, commitSha)
	if err != nil {
		return fmt.Errorf("failed to add git note: %w", err)
	}
	_, err = runGitCmd(ctx, gitPath, "push", "origin", git.HydratorNotesRef)
	if err != nil {
		return fmt.Errorf("failed to push git notes: %w", err)
	}
	return nil
}

// addNoteToEnvironment adds a git note to an existing hydrated commit without creating a new commit.
// This simulates the hydrator where manifests haven't changed.
// We do not send a webhook to trigger reconciliation, because github and other SCM providers do not support webhooks for git notes.
func addNoteToEnvironment(ctx context.Context, gitPath, branch, drySha string) (err error) {
	// Fetch latest
	_, err = runGitCmd(ctx, gitPath, "fetch", "origin")
	if err != nil {
		return fmt.Errorf("failed to fetch: %w", err)
	}

	// Get the current hydrated SHA from the branch
	hydratedSha, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+branch)
	if err != nil {
		return fmt.Errorf("failed to get hydrated SHA: %w", err)
	}
	hydratedSha = strings.TrimSpace(hydratedSha)

	// Add git note to the existing commit
	return pushGitNote(ctx, gitPath, hydratedSha, drySha)
}

func createKubeConfig(cfg *rest.Config) ([]byte, error) {
	name := "cluster"
	apiConfig := api.Config{
		Clusters: map[string]*api.Cluster{
			name: {
				Server:                   cfg.Host,
				CertificateAuthorityData: cfg.CAData,
			},
		},
		AuthInfos: map[string]*api.AuthInfo{
			name: {
				ClientCertificateData: cfg.CertData,
				ClientKeyData:         cfg.KeyData,
				Token:                 cfg.BearerToken,
			},
		},
		Contexts: map[string]*api.Context{
			name: {
				Cluster:  name,
				AuthInfo: name,
			},
		},
		CurrentContext: name,
	}

	data, err := clientcmd.Write(apiConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to write kubeconfig: %w", err)
	}
	return data, nil
}

func createKubeconfigSecret(ctx context.Context, name string, namespace string, cfg *rest.Config, cl client.Client) error {
	kubeconfigData, err := createKubeConfig(cfg)
	if err != nil {
		return err
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				constants.KubeconfigSecretLabel: "true",
			},
		},
	}
	secret.Data = map[string][]byte{
		constants.KubeconfigSecretKey: kubeconfigData,
	}
	if err := cl.Create(ctx, secret); err != nil {
		return fmt.Errorf("failed to create kubeconfig secret %s/%s: %w", namespace, name, err)
	}
	return nil
}

func createAndStartTestEnv() (*envtest.Environment, *rest.Config, client.Client) {
	env := &envtest.Environment{
		UseExistingCluster: ptr.To(false),
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "test", "external_crds"),
		},
		ErrorIfCRDPathMissing:    true,
		ControlPlaneStopTimeout:  1 * time.Minute,
		AttachControlPlaneOutput: false,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.31.0-%s-%s", goruntime.GOOS, goruntime.GOARCH)),
	}

	cfg, err := env.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	cl, err := client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(cl).NotTo(BeNil())

	return env, cfg, cl
}

// unmarshalYaml unmarshals a YAML string into the target struct using JSON decoding. It disallows unknown fields. Using
// JSON decoding allows us to leverage the JSON decoder's DisallowUnknownFields feature. The YAML unmarshaller would
// require yaml tags on all structs, and we'd like to avoid adding those.
func unmarshalYamlStrict(yamlData string, target any) error {
	d := map[string]any{}
	err := yaml.Unmarshal([]byte(yamlData), &d)
	if err != nil {
		return fmt.Errorf("failed to unmarshal YAML: %w", err)
	}
	jsonData, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML to JSON: %w", err)
	}
	u := json.NewDecoder(bytes.NewBuffer(jsonData))
	u.DisallowUnknownFields()
	err = u.Decode(target)
	if err != nil {
		return fmt.Errorf("failed to unmarshal JSON to target: %w", err)
	}
	return nil
}
