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

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	cfg              *rest.Config
	cfgDev           *rest.Config
	cfgStaging       *rest.Config
	k8sClient        client.Client
	k8sClientDev     client.Client
	k8sClientStaging client.Client
	testEnv          *envtest.Environment
	testEnvDev       *envtest.Environment
	testEnvStaging   *envtest.Environment
	gitServer        *http.Server
	gitStoragePath   string
	cancel           context.CancelFunc
	ctx              context.Context
	gitServerPort    string
	scheme           = utils.GetScheme()
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
		Scheme:                scheme,
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
		},
	}
	Expect(k8sClient.Create(ctx, controllerConfiguration)).To(Succeed())

	settingsMgr := settings.NewManager(k8sManager.GetClient(), k8sManager.GetAPIReader(), settings.ManagerConfig{
		ControllerNamespace: "default",
	})

	err = (&CommitStatusReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		Recorder:    k8sManager.GetEventRecorderFor("CommitStatus"),
		SettingsMgr: settingsMgr,
	}).SetupWithManager(ctx, k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&PromotionStrategyReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		Recorder:    k8sManager.GetEventRecorderFor("PromotionStrategy"),
		SettingsMgr: settingsMgr,
	}).SetupWithManager(ctx, k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&ChangeTransferPolicyReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		Recorder:    k8sManager.GetEventRecorderFor("ChangeTransferPolicy"),
		SettingsMgr: settingsMgr,
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
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
		// Recorder: k8sManager.GetEventRecorderFor("GitRepository"),
	}).SetupWithManager(ctx, k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&ArgoCDCommitStatusReconciler{
		Manager:            multiClusterManager,
		SettingsMgr:        settingsMgr,
		KubeConfigProvider: kubeconfigProvider,
		Recorder:           k8sManager.GetEventRecorderFor("ArgoCDCommitStatus"),
	}).SetupWithManager(ctx, multiClusterManager)
	Expect(err).ToNot(HaveOccurred())

	webhookReceiverPort := constants.WebhookReceiverPort + GinkgoParallelProcess()
	whr := webhookreceiver.NewWebhookReceiver(k8sManager)
	go func() {
		err = whr.Start(ctx, fmt.Sprintf(":%d", webhookReceiverPort))
		Expect(err).ToNot(HaveOccurred(), "failed to start webhook receiver")
	}()

	go func() {
		defer GinkgoRecover()
		err = multiClusterManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()

	Eventually(kubeconfigProvider.ListClusters, constants.EventuallyTimeout).Should(HaveLen(2))
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

	_, err = runGitCmd(gitPath, "clone", fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPort, owner, name), ".")
	Expect(err).NotTo(HaveOccurred())

	_, err = runGitCmd(gitPath, "config", "user.name", "testuser")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(gitPath, "config", "user.email", "testemail@test.com")
	Expect(err).NotTo(HaveOccurred())

	_, err = runGitCmd(gitPath, "commit", "--allow-empty", "-m", "init commit")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(gitPath, "push")
	Expect(err).NotTo(HaveOccurred())

	defaultBranch, err := runGitCmd(gitPath, "rev-parse", "--abbrev-ref", "HEAD")
	Expect(err).NotTo(HaveOccurred())
	defaultBranch = strings.TrimSpace(defaultBranch)

	sha, err := runGitCmd(gitPath, "rev-parse", defaultBranch)
	Expect(err).NotTo(HaveOccurred())

	for _, environment := range []string{"environment/development", "environment/staging", "environment/production"} {
		_, err = runGitCmd(gitPath, "checkout", "--orphan", environment)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "rm", "-rf", "--ignore-unmatch", ".")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "commit", "--allow-empty", "-m", "initial commit")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "push", "-u", "origin", environment)
		Expect(err).NotTo(HaveOccurred())

		// Sleep one seconds to differentiate the commits to prevent same hash
		time.Sleep(1 * time.Second)

		_, err = runGitCmd(gitPath, "checkout", "-b", environment+"-next")
		Expect(err).NotTo(HaveOccurred())
		f, err := os.Create(path.Join(gitPath, "hydrator.metadata"))
		Expect(err).NotTo(HaveOccurred())
		str := fmt.Sprintf("{\"drySHA\": \"%s\"}", strings.TrimSpace(sha))
		_, err = f.WriteString(str)
		Expect(err).NotTo(HaveOccurred())
		err = f.Close()
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "add", "hydrator.metadata")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "commit", "-m", "initial commit next")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "push", "-u", "origin", environment+"-next")
		Expect(err).NotTo(HaveOccurred())

		// Sleep one seconds to differentiate the commits to prevent same hash
		time.Sleep(1 * time.Second)
	}
}

func setupInitialTestGitRepoOnServer(owner string, name string) {
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

	_, err = runGitCmd(gitPath, "clone", fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPort, owner, name), ".")
	Expect(err).NotTo(HaveOccurred())

	_, err = runGitCmd(gitPath, "config", "user.name", "testuser")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(gitPath, "config", "user.email", "testemail@test.com")
	Expect(err).NotTo(HaveOccurred())

	f, err := os.Create(path.Join(gitPath, "hydrator.metadata"))
	Expect(err).NotTo(HaveOccurred())
	_, err = f.WriteString("{\"drySHA\": \"n/a\"}")
	Expect(err).NotTo(HaveOccurred())
	err = f.Close()
	Expect(err).NotTo(HaveOccurred())

	_, err = runGitCmd(gitPath, "add", "hydrator.metadata")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(gitPath, "commit", "-m", "init commit dry side n/a dry sha")
	Expect(err).NotTo(HaveOccurred())

	defaultBranch, err := runGitCmd(gitPath, "rev-parse", "--abbrev-ref", "HEAD")
	Expect(err).NotTo(HaveOccurred())
	defaultBranch = strings.TrimSpace(defaultBranch)

	sha, err := runGitCmd(gitPath, "rev-parse", defaultBranch)
	Expect(err).NotTo(HaveOccurred())
	f, err = os.Create(path.Join(gitPath, "hydrator.metadata"))
	Expect(err).NotTo(HaveOccurred())
	str := fmt.Sprintf("{\"drySHA\": \"%s\"}", strings.TrimSpace(sha))
	_, err = f.WriteString(str)
	Expect(err).NotTo(HaveOccurred())
	err = f.Close()
	Expect(err).NotTo(HaveOccurred())

	_, err = runGitCmd(gitPath, "add", "hydrator.metadata")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(gitPath, "commit", "-m", "second commit with real dry sha")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(gitPath, "push")
	Expect(err).NotTo(HaveOccurred())

	for _, environment := range []string{"environment/development-next", "environment/staging-next", "environment/production-next"} {
		_, err = runGitCmd(gitPath, "checkout", "--orphan", environment)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "commit", "--allow-empty", "-m", "initial empty commit for "+environment)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "push", "-u", "origin", environment)
		Expect(err).NotTo(HaveOccurred())

		// Sleep one seconds to differentiate the commits to prevent same hash
		time.Sleep(1 * time.Second)

		activeB, _ := strings.CutSuffix(environment, "-next")
		_, err = runGitCmd(gitPath, "checkout", "-b", activeB)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "push", "-u", "origin", activeB)
		Expect(err).NotTo(HaveOccurred())

		// Sleep one seconds to differentiate the commits to prevent same hash
		time.Sleep(1 * time.Second)
	}
	GinkgoLogr.Info("Git repository initialized", "path", gitPath)
}

func makeChangeAndHydrateRepo(gitPath string, repoOwner string, repoName string, dryCommitMessage string, hydratedCommitMessage string) (string, string) {
	repoURL := fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPort, repoOwner, repoName)
	_, err := runGitCmd(gitPath, "clone", "--verbose", "--progress", "--filter=blob:none", repoURL, ".")
	Expect(err).NotTo(HaveOccurred())

	_, err = runGitCmd(gitPath, "config", "user.name", "testuser")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(gitPath, "config", "user.email", "testmail@test.com")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(gitPath, "config", "pull.rebase", "false")
	Expect(err).NotTo(HaveOccurred())

	for _, environment := range []string{"environment/development", "environment/staging", "environment/production", "environment/development-next", "environment/staging-next", "environment/production-next"} {
		_, err = runGitCmd(gitPath, "checkout", "-B", environment, "origin/"+environment)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "pull")
		Expect(err).NotTo(HaveOccurred())
	}

	defaultBranch, err := runGitCmd(gitPath, "rev-parse", "--abbrev-ref", "origin/HEAD")
	Expect(err).NotTo(HaveOccurred())
	defaultBranch, _ = strings.CutPrefix(strings.TrimSpace(defaultBranch), "origin/")

	_, err = runGitCmd(gitPath, "checkout", defaultBranch)
	Expect(err).NotTo(HaveOccurred())

	f, err := os.Create(path.Join(gitPath, "manifests-fake.yaml"))
	Expect(err).NotTo(HaveOccurred())
	str := fmt.Sprintf("{\"time\": \"%s\"}", time.Now().Format(time.RFC3339Nano))
	_, err = f.WriteString(str)
	Expect(err).NotTo(HaveOccurred())
	err = f.Close()
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(gitPath, "add", "manifests-fake.yaml")
	Expect(err).NotTo(HaveOccurred())
	if dryCommitMessage == "" {
		dryCommitMessage = "added fake manifests commit with timestamp"
	}
	_, err = runGitCmd(gitPath, "commit", "-m", dryCommitMessage)
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(gitPath, "push", "-u", "origin", defaultBranch)
	Expect(err).NotTo(HaveOccurred())

	sha, err := runGitCmd(gitPath, "rev-parse", defaultBranch)
	Expect(err).NotTo(HaveOccurred())
	sha = strings.TrimSpace(sha)
	shortSha, err := runGitCmd(gitPath, "rev-parse", "--short=7", defaultBranch)
	Expect(err).NotTo(HaveOccurred())
	shortSha = strings.TrimSpace(shortSha)

	for _, environment := range []string{"environment/development-next", "environment/staging-next", "environment/production-next"} {
		_, err = runGitCmd(gitPath, "checkout", "-B", environment, "origin/"+environment)
		Expect(err).NotTo(HaveOccurred())

		var subject string
		var body string
		parts := strings.SplitN(dryCommitMessage, "\n\n", 2)
		subject = parts[0]
		if len(parts) > 1 {
			body = parts[1]
		}

		metadata := git.HydratorMetadata{
			RepoURL: repoURL,
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
		_, err = runGitCmd(gitPath, "add", "hydrator.metadata")
		Expect(err).NotTo(HaveOccurred())

		f, err = os.Create(path.Join(gitPath, "manifests-fake.yaml"))
		Expect(err).NotTo(HaveOccurred())
		str := fmt.Sprintf("{\"time\": \"%s\"}", time.Now().Format(time.RFC3339Nano))
		_, err = f.WriteString(str)
		Expect(err).NotTo(HaveOccurred())
		err = f.Close()
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "add", "manifests-fake.yaml")
		Expect(err).NotTo(HaveOccurred())
		if hydratedCommitMessage == "" {
			_, err = runGitCmd(gitPath, "commit", "-m", "added pending commit from dry sha, "+sha+" from environment "+strings.TrimRight(environment, "-next"))
		} else {
			_, err = runGitCmd(gitPath, "commit", "-m", hydratedCommitMessage)
		}
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "push", "-u", "origin", environment)
		Expect(err).NotTo(HaveOccurred())

		// Sleep one seconds to differentiate the commits to prevent same hash
		time.Sleep(1 * time.Second)
	}

	return sha, shortSha
}

func makeChangeAndHydrateRepoNoOp(gitPath string, repoOwner string, repoName string, dryCommitMessage string, hydratedCommitMessage string) (string, string) {
	repoURL := fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPort, repoOwner, repoName)

	for _, environment := range []string{"environment/development", "environment/staging", "environment/production", "environment/development-next", "environment/staging-next", "environment/production-next"} {
		_, err := runGitCmd(gitPath, "checkout", "-B", environment, "origin/"+environment)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "pull")
		Expect(err).NotTo(HaveOccurred())
	}

	defaultBranch, err := runGitCmd(gitPath, "rev-parse", "--abbrev-ref", "origin/HEAD")
	Expect(err).NotTo(HaveOccurred())
	defaultBranch, _ = strings.CutPrefix(strings.TrimSpace(defaultBranch), "origin/")

	_, err = runGitCmd(gitPath, "checkout", defaultBranch)
	Expect(err).NotTo(HaveOccurred())

	_, err = runGitCmd(gitPath, "pull")
	Expect(err).NotTo(HaveOccurred())

	// Make a no-op commit (empty commit)
	if dryCommitMessage == "" {
		dryCommitMessage = "no-op commit"
	}
	_, err = runGitCmd(gitPath, "commit", "--allow-empty", "-m", dryCommitMessage)
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(gitPath, "push", "-u", "origin", defaultBranch)
	Expect(err).NotTo(HaveOccurred())

	sha, err := runGitCmd(gitPath, "rev-parse", defaultBranch)
	Expect(err).NotTo(HaveOccurred())
	sha = strings.TrimSpace(sha)
	shortSha, err := runGitCmd(gitPath, "rev-parse", "--short=7", defaultBranch)
	Expect(err).NotTo(HaveOccurred())
	shortSha = strings.TrimSpace(shortSha)

	for _, environment := range []string{"environment/development-next", "environment/staging-next", "environment/production-next"} {
		_, err = runGitCmd(gitPath, "checkout", "-B", environment, "origin/"+environment)
		Expect(err).NotTo(HaveOccurred())

		_, err = runGitCmd(gitPath, "pull")
		Expect(err).NotTo(HaveOccurred())

		var subject string
		var body string
		parts := strings.SplitN(dryCommitMessage, "\n\n", 2)
		subject = parts[0]
		if len(parts) > 1 {
			body = parts[1]
		}

		metadata := git.HydratorMetadata{
			RepoURL: repoURL,
			DrySha:  sha,
			Author:  "testuser <testmail@test.com>",
			Date:    metav1.Now(),
			Subject: subject,
			Body:    body,
		}
		m, err := json.MarshalIndent(metadata, "", "\t")
		Expect(err).NotTo(HaveOccurred())

		f, err := os.Create(path.Join(gitPath, "hydrator.metadata"))
		Expect(err).NotTo(HaveOccurred())
		_, err = f.Write(m)
		Expect(err).NotTo(HaveOccurred())
		err = f.Close()
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "add", "hydrator.metadata")
		Expect(err).NotTo(HaveOccurred())

		if hydratedCommitMessage == "" {
			_, err = runGitCmd(gitPath, "commit", "-m", "added no-op commit from dry sha, "+sha+" from environment "+strings.TrimRight(environment, "-next"))
		} else {
			_, err = runGitCmd(gitPath, "commit", "-m", hydratedCommitMessage)
		}
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "push", "-u", "origin", environment)
		Expect(err).NotTo(HaveOccurred())

		// Sleep one seconds to differentiate the commits to prevent same hash
		time.Sleep(1 * time.Second)
	}

	return sha, shortSha
}

func runGitCmd(directory string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
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

func randomString(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)

	for i := range result {
		result[i] = charset[rand.Intn(len(charset))]
	}

	return string(result)
}

func simulateWebhook(ctx context.Context, k8sClient client.Client, ctp *promoterv1alpha1.ChangeTransferPolicy) {
	Eventually(func(g Gomega) {
		orig := ctp.DeepCopy()
		if ctp.Annotations == nil {
			ctp.Annotations = make(map[string]string)
		}
		ctp.Annotations[promoterv1alpha1.ReconcileAtAnnotation] = metav1.Now().Format(time.RFC3339)
		err := k8sClient.Patch(ctx, ctp, client.MergeFrom(orig))
		Expect(err).To(Succeed())
	}, constants.EventuallyTimeout).Should(Succeed())
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
