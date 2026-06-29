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

package apicallmetrics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	"gopkg.in/yaml.v3"
	"sigs.k8s.io/controller-runtime/pkg/cluster"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"

	"github.com/argoproj-labs/gitops-promoter/internal/controller"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/internal/webhookreceiver"
	"github.com/sosedoff/gitkit"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	kubeconfigprovider "sigs.k8s.io/multicluster-runtime/providers/kubeconfig"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

func metricsGitServerPort() int {
	return intFromEnv("PROMOTER_API_METRICS_GIT_PORT", 5000+GinkgoParallelProcess())
}

func metricsWebhookReceiverPort() int {
	return intFromEnv("PROMOTER_API_METRICS_WEBHOOK_PORT", constants.WebhookReceiverPort+GinkgoParallelProcess())
}

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
	metricsRequeue      time.Duration
)

func TestAPICallMetrics(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "API Call Metrics Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true), zap.Level(zapcore.Level(-4)), func(o *zap.Options) {
		o.TimeEncoder = zapcore.RFC3339NanoTimeEncoder
	}))

	metricsRequeue = metricsSuiteRequeueDuration()

	gitStoragePath, _ = os.MkdirTemp("", "*")
	gitServerPort, gitServer = startGitServer(gitStoragePath)

	testEnv, cfg, k8sClient = createAndStartTestEnv()
	testEnvDev, cfgDev, k8sClientDev = createAndStartTestEnv()
	testEnvStaging, cfgStaging, k8sClientStaging = createAndStartTestEnv()

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

	ctx, cancel = context.WithCancel(context.Background())

	Expect(createKubeconfigSecret(ctx, "testenv-dev", constants.KubeconfigSecretNamespace, cfgDev, k8sClient)).To(Succeed())
	Expect(createKubeconfigSecret(ctx, "testenv-staging", constants.KubeconfigSecretNamespace, cfgStaging, k8sClient)).To(Succeed())

	multiClusterManager, err := mcmanager.New(cfg, kubeconfigProvider, ctrl.Options{
		Scheme:  scheme,
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	Expect(err).NotTo(HaveOccurred())

	Expect(kubeconfigProvider.SetupWithManager(ctx, multiClusterManager)).To(Succeed())
	k8sManager := multiClusterManager.GetLocalManager()

	controllerConfiguration, err := loadControllerConfigurationForMetricsSuite("default", settings.ControllerConfigurationName, metricsRequeue)
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient.Create(ctx, controllerConfiguration)).To(Succeed())

	settingsMgr := settings.NewManager(k8sManager.GetClient(), k8sManager.GetAPIReader(), settings.ManagerConfig{
		ControllerNamespace: "default",
	})

	ctpReconciler := &controller.ChangeTransferPolicyReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		Recorder:    k8sManager.GetEventRecorder("ChangeTransferPolicy"),
		SettingsMgr: settingsMgr,
	}
	Expect(ctpReconciler.SetupWithManager(ctx, k8sManager)).To(Succeed())

	Expect((&controller.CommitStatusReconciler{
		Client: k8sManager.GetClient(), Scheme: k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorder("CommitStatus"), SettingsMgr: settingsMgr,
		EnqueueCTP: ctpReconciler.GetEnqueueFunc(),
	}).SetupWithManager(ctx, k8sManager)).To(Succeed())

	Expect((&controller.TimedCommitStatusReconciler{
		Client: k8sManager.GetClient(), Scheme: k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorder("TimedCommitStatus"), SettingsMgr: settingsMgr,
		EnqueueCTP: ctpReconciler.GetEnqueueFunc(),
	}).SetupWithManager(ctx, k8sManager)).To(Succeed())

	Expect((&controller.PromotionStrategyReconciler{
		Client: k8sManager.GetClient(), Scheme: k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorder("PromotionStrategy"), SettingsMgr: settingsMgr,
		EnqueueCTP: ctpReconciler.GetEnqueueFunc(),
	}).SetupWithManager(ctx, k8sManager)).To(Succeed())

	Expect((&controller.PullRequestReconciler{
		Client: k8sManager.GetClient(), Scheme: k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorder("PullRequest"), SettingsMgr: settingsMgr,
	}).SetupWithManager(ctx, k8sManager)).To(Succeed())

	Expect((&controller.ArgoCDCommitStatusReconciler{
		Manager: multiClusterManager, SettingsMgr: settingsMgr,
		KubeConfigProvider: kubeconfigProvider,
		Recorder:           k8sManager.GetEventRecorder("ArgoCDCommitStatus"),
	}).SetupWithManager(ctx, multiClusterManager)).To(Succeed())

	Expect((&controller.GitCommitStatusReconciler{
		Client: k8sManager.GetClient(), Scheme: k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorder("GitCommitStatus"), SettingsMgr: settingsMgr,
		EnqueueCTP: ctpReconciler.GetEnqueueFunc(),
	}).SetupWithManager(ctx, k8sManager)).To(Succeed())

	Expect((&controller.WebRequestCommitStatusReconciler{
		Client: k8sManager.GetClient(), Scheme: k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorder("WebRequestCommitStatus"), SettingsMgr: settingsMgr,
		EnqueueCTP: ctpReconciler.GetEnqueueFunc(),
	}).SetupWithManager(ctx, k8sManager)).To(Succeed())

	webhookReceiverPort = metricsWebhookReceiverPort()
	whr := webhookreceiver.NewWebhookReceiver(k8sManager, webhookreceiver.EnqueueFunc(ctpReconciler.GetEnqueueFunc()))
	go func() {
		err = whr.Start(ctx, fmt.Sprintf(":%d", webhookReceiverPort))
		Expect(err).NotTo(HaveOccurred())
	}()

	go func() {
		defer GinkgoRecover()
		Expect(multiClusterManager.Start(ctx)).To(Succeed())
	}()

	Eventually(func() bool { return k8sManager.GetCache().WaitForCacheSync(ctx) }, constants.EventuallyTimeout).Should(BeTrue())
	Eventually(kubeconfigProvider.ListClusters, constants.EventuallyTimeout).Should(HaveLen(2))
	for _, clusterName := range kubeconfigProvider.ListClusters() {
		cluster, err := multiClusterManager.GetCluster(ctx, clusterName)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() bool { return cluster.GetCache().WaitForCacheSync(ctx) }, constants.EventuallyTimeout).Should(BeTrue())
	}
	Eventually(func() error {
		list := &promoterv1alpha1.ArgoCDCommitStatusList{}
		return multiClusterManager.GetLocalManager().GetClient().List(ctx, list)
	}, constants.EventuallyTimeout, 100*time.Millisecond).Should(Succeed())
})

var _ = AfterSuite(func() {
	cancel()
	Expect(testEnv.Stop()).To(Succeed())
	Expect(testEnvDev.Stop()).To(Succeed())
	Expect(testEnvStaging.Stop()).To(Succeed())
	_ = gitServer.Shutdown(context.Background())
	_ = os.RemoveAll(gitStoragePath)
})

func startGitServer(gitStoragePath string) (string, *http.Server) {
	service := gitkit.New(gitkit.Config{
		Dir: gitStoragePath, AutoCreate: true, AutoHooks: true,
		Hooks: &gitkit.HookScripts{PreReceive: `echo "Hello World!"`},
	})
	Expect(service.Setup()).To(Succeed())
	port := metricsGitServerPort()
	portStr := strconv.Itoa(port)
	server := &http.Server{Addr: ":" + portStr, Handler: service}
	log.SetOutput(&filterLogger{})
	go func() { _ = server.ListenAndServe() }()
	return portStr, server
}

type filterLogger struct{}

func (f *filterLogger) Write(p []byte) (n int, err error) {
	if strings.Contains(string(p), "request:") {
		return len(p), nil
	}
	_, _ = os.Stdout.Write(p)
	return len(p), nil
}

func testGitRepoCloneURL(repo *promoterv1alpha1.GitRepository) string {
	return fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPort, repo.Spec.Fake.Owner, repo.Spec.Fake.Name)
}

func createAndStartTestEnv() (*envtest.Environment, *rest.Config, client.Client) {
	env := &envtest.Environment{
		UseExistingCluster: new(false),
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "..", "test", "external_crds"),
		},
		ErrorIfCRDPathMissing:    true,
		ControlPlaneStopTimeout:  1 * time.Minute,
		AttachControlPlaneOutput: false,
		BinaryAssetsDirectory: filepath.Join("..", "..", "..", "bin", "k8s",
			fmt.Sprintf("1.31.0-%s-%s", goruntime.GOOS, goruntime.GOARCH)),
	}
	cfg, err := env.Start()
	Expect(err).NotTo(HaveOccurred())
	cl, err := client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	return env, cfg, cl
}

func createKubeConfig(cfg *rest.Config) ([]byte, error) {
	name := "cluster"
	apiConfig := api.Config{
		Clusters: map[string]*api.Cluster{name: {Server: cfg.Host, CertificateAuthorityData: cfg.CAData}},
		AuthInfos: map[string]*api.AuthInfo{name: {
			ClientCertificateData: cfg.CertData, ClientKeyData: cfg.KeyData, Token: cfg.BearerToken,
		}},
		Contexts:       map[string]*api.Context{name: {Cluster: name, AuthInfo: name}},
		CurrentContext: name,
	}
	return clientcmd.Write(apiConfig)
}

func createKubeconfigSecret(ctx context.Context, name, namespace string, cfg *rest.Config, cl client.Client) error {
	kubeconfigData, err := createKubeConfig(cfg)
	if err != nil {
		return err
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: namespace,
			Labels: map[string]string{constants.KubeconfigSecretLabel: "true"},
		},
		Data: map[string][]byte{constants.KubeconfigSecretKey: kubeconfigData},
	}
	return cl.Create(ctx, secret)
}

func unmarshalYamlStrict(yamlData string, target any) error {
	d := map[string]any{}
	if err := yaml.Unmarshal([]byte(yamlData), &d); err != nil {
		return fmt.Errorf("failed to unmarshal YAML: %w", err)
	}
	jsonData, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML to JSON: %w", err)
	}
	u := json.NewDecoder(bytes.NewBuffer(jsonData))
	u.DisallowUnknownFields()
	if err := u.Decode(target); err != nil {
		return fmt.Errorf("failed to unmarshal JSON to target: %w", err)
	}
	return nil
}

func setupInitialTestGitRepoOnServer(ctx context.Context, repo *promoterv1alpha1.GitRepository) {
	gitPath, err := os.MkdirTemp("", "*")
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = os.RemoveAll(gitPath) }()

	_, err = runGitCmd(ctx, gitPath, "clone", testGitRepoCloneURL(repo), ".")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, gitPath, "config", "user.name", "testuser")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, gitPath, "config", "user.email", "testemail@test.com")
	Expect(err).NotTo(HaveOccurred())

	f, err := os.Create(path.Join(gitPath, "hydrator.metadata"))
	Expect(err).NotTo(HaveOccurred())
	_, err = f.WriteString("{\"drySHA\": \"n/a\"}")
	Expect(err).NotTo(HaveOccurred())
	Expect(f.Close()).To(Succeed())
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
	_, err = f.WriteString(fmt.Sprintf("{\"drySHA\": \"%s\"}", strings.TrimSpace(sha)))
	Expect(err).NotTo(HaveOccurred())
	Expect(f.Close()).To(Succeed())
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
		time.Sleep(1 * time.Second)
		activeB, _ := strings.CutSuffix(environment, "-next")
		_, err = runGitCmd(ctx, gitPath, "checkout", "-b", activeB)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(ctx, gitPath, "push", "-u", "origin", activeB)
		Expect(err).NotTo(HaveOccurred())
		time.Sleep(1 * time.Second)
	}
}

func makeChangeAndHydrateRepo(gitPath string, repo *promoterv1alpha1.GitRepository, dryCommitMessage, hydratedCommitMessage string) (string, string) {
	repoURL := testGitRepoCloneURL(repo)
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

	beforeSha, err := runGitCmd(ctx, gitPath, "rev-parse", defaultBranch)
	Expect(err).NotTo(HaveOccurred())
	beforeSha = strings.TrimSpace(beforeSha)

	f, err := os.Create(path.Join(gitPath, "manifests-fake.yaml"))
	Expect(err).NotTo(HaveOccurred())
	_, err = f.WriteString(fmt.Sprintf("{\"time\": \"%s\"}", time.Now().Format(time.RFC3339Nano)))
	Expect(err).NotTo(HaveOccurred())
	Expect(f.Close()).To(Succeed())
	_, err = runGitCmd(ctx, gitPath, "add", "manifests-fake.yaml")
	Expect(err).NotTo(HaveOccurred())
	if dryCommitMessage == "" {
		dryCommitMessage = "added fake manifests commit with timestamp"
	}
	_, err = runGitCmd(ctx, gitPath, "commit", "-m", dryCommitMessage)
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, gitPath, "push", "-u", "origin", defaultBranch)
	Expect(err).NotTo(HaveOccurred())
	sendWebhookForPush(ctx, beforeSha, defaultBranch)

	sha, err := runGitCmd(ctx, gitPath, "rev-parse", defaultBranch)
	Expect(err).NotTo(HaveOccurred())
	sha = strings.TrimSpace(sha)
	shortSha, err := runGitCmd(ctx, gitPath, "rev-parse", "--short=5", defaultBranch)
	Expect(err).NotTo(HaveOccurred())
	shortSha = strings.TrimSpace(shortSha)

	for _, environment := range []string{"environment/development-next", "environment/staging-next", "environment/production-next"} {
		_, err = runGitCmd(ctx, gitPath, "checkout", "-B", environment, "origin/"+environment)
		Expect(err).NotTo(HaveOccurred())
		beforeBranchSha, err := runGitCmd(ctx, gitPath, "rev-parse", environment)
		Expect(err).NotTo(HaveOccurred())
		beforeBranchSha = strings.TrimSpace(beforeBranchSha)

		subject, body, _ := strings.Cut(dryCommitMessage, "\n\n")
		metadata := git.HydratorMetadata{
			DrySha: sha, Author: "testuser <testmail@test.com>", Date: metav1.Now(),
			Subject: subject, Body: body,
		}
		m, err := json.MarshalIndent(metadata, "", "\t")
		Expect(err).NotTo(HaveOccurred())
		f, err = os.Create(path.Join(gitPath, "hydrator.metadata"))
		Expect(err).NotTo(HaveOccurred())
		_, err = f.Write(m)
		Expect(err).NotTo(HaveOccurred())
		Expect(f.Close()).To(Succeed())
		_, err = runGitCmd(ctx, gitPath, "add", "hydrator.metadata")
		Expect(err).NotTo(HaveOccurred())
		f, err = os.Create(path.Join(gitPath, "manifests-fake.yaml"))
		Expect(err).NotTo(HaveOccurred())
		_, err = f.WriteString(fmt.Sprintf("{\"time\": \"%s\"}", time.Now().Format(time.RFC3339Nano)))
		Expect(err).NotTo(HaveOccurred())
		Expect(f.Close()).To(Succeed())
		_, err = runGitCmd(ctx, gitPath, "add", "manifests-fake.yaml")
		Expect(err).NotTo(HaveOccurred())
		if hydratedCommitMessage == "" {
			_, err = runGitCmd(ctx, gitPath, "commit", "-m", "added pending commit from dry sha, "+sha+" from environment "+strings.TrimSuffix(environment, "-next"))
		} else {
			_, err = runGitCmd(ctx, gitPath, "commit", "-m", hydratedCommitMessage)
		}
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(ctx, gitPath, "push", "-u", "origin", environment)
		Expect(err).NotTo(HaveOccurred())
		sendWebhookForPush(ctx, beforeBranchSha, environment)
		time.Sleep(1 * time.Second)
	}
	return sha, shortSha
}

func runGitCmd(ctx context.Context, directory string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	cmd.Dir = directory
	cmd.Env = []string{"GIT_TERMINAL_PROMPT=0"}
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

func sendWebhookForPush(ctx context.Context, sha, branch string) {
	payload := map[string]any{
		"before": sha,
		"ref":    "refs/heads/" + branch,
		"pusher": map[string]any{"name": "test-user", "email": "test@example.com"},
	}
	payloadBytes, err := json.Marshal(payload)
	Expect(err).NotTo(HaveOccurred())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://localhost:%d/", webhookReceiverPort), bytes.NewBuffer(payloadBytes))
	Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Github-Event", "push")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		GinkgoLogr.Info("webhook send failed", "error", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
}
