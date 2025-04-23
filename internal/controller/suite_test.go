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
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/webhookreceiver"

	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	ctrl "sigs.k8s.io/controller-runtime"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sosedoff/gitkit"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	cfg            *rest.Config
	k8sClient      client.Client
	testEnv        *envtest.Environment
	gitServer      *http.Server
	gitStoragePath string
	cancel         context.CancelFunc
	ctx            context.Context
	gitServerPort  string
)

const (
	EventuallyTimeout   = 90 * time.Second
	WebhookReceiverPort = 3333
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
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("setting up git server")
	var errMkDir error
	gitStoragePath, errMkDir = os.MkdirTemp("", "*")
	if errMkDir != nil {
		panic("could not make temp dir for repo server")
	}
	gitServerPort, gitServer = startGitServer(gitStoragePath)

	By("bootstrapping test environment")
	useExistingCluster := false
	testEnv = &envtest.Environment{
		UseExistingCluster: &useExistingCluster,
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
			fmt.Sprintf("1.31.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
	}

	var err error
	// cfg is defined in this file globally.
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = promoterv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	//nolint:fatcontext
	ctx, cancel = context.WithCancel(context.Background())
	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
	})
	Expect(err).ToNot(HaveOccurred())

	settingsMgr := settings.NewManager(k8sManager.GetClient(), settings.ManagerConfig{
		GlobalNamespace: "default",
	})

	err = (&CommitStatusReconciler{
		Client:   k8sManager.GetClient(),
		Scheme:   k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorderFor("CommitStatus"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&PromotionStrategyReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		Recorder:    k8sManager.GetEventRecorderFor("PromotionStrategy"),
		SettingsMgr: settingsMgr,
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&ChangeTransferPolicyReconciler{
		Client:      k8sManager.GetClient(),
		Scheme:      k8sManager.GetScheme(),
		Recorder:    k8sManager.GetEventRecorderFor("ChangeTransferPolicy"),
		SettingsMgr: settingsMgr,
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&PullRequestReconciler{
		Client:   k8sManager.GetClient(),
		Scheme:   k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorderFor("PullRequest"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&RevertCommitReconciler{
		Client:   k8sManager.GetClient(),
		Scheme:   k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorderFor("RevertCommit"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&ScmProviderReconciler{
		Client:   k8sManager.GetClient(),
		Scheme:   k8sManager.GetScheme(),
		Recorder: k8sManager.GetEventRecorderFor("ScmProvider"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&GitRepositoryReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
		// Recorder: k8sManager.GetEventRecorderFor("GitRepository"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&ArgoCDCommitStatusReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
		// Recorder: k8sManager.GetEventRecorderFor("ArgoCDCommitStatus"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	webhookReceiverPort := WebhookReceiverPort + GinkgoParallelProcess()
	whr := webhookreceiver.NewWebhookReceiver(k8sManager, settingsMgr)
	go func() {
		err = whr.Start(ctx, fmt.Sprintf(":%d", webhookReceiverPort))
		Expect(err).ToNot(HaveOccurred(), "failed to start webhook receiver")
	}()

	controllerConfiguration := &promoterv1alpha1.ControllerConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "promoter-controller-configuration",
			Namespace: "default",
		},
		Spec: promoterv1alpha1.ControllerConfigurationSpec{
			PullRequest: promoterv1alpha1.PullRequestConfiguration{
				Template: promoterv1alpha1.PullRequestTemplate{
					Title:       "Promote {{ trunc 7 .ChangeTransferPolicy.Status.Proposed.Dry.Sha }} to `{{ .ChangeTransferPolicy.Spec.ActiveBranch }}`",
					Description: "This PR is promoting the environment branch `{{ .ChangeTransferPolicy.Spec.ActiveBranch }}` which is currently on dry sha {{ .ChangeTransferPolicy.Status.Active.Dry.Sha }} to dry sha {{ .ChangeTransferPolicy.Status.Proposed.Dry.Sha }}.",
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, controllerConfiguration)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
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
	log.Print(string(p))
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
	gitServerPortStr := fmt.Sprintf("%d", gitServerPort)
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

	// "environment/development-next", "environment/staging-next", "environment/production-next"
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
	_, err = runGitCmd(gitPath, "commit", "-m", "init commit")
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
	_, err = runGitCmd(gitPath, "commit", "-m", "second commit with dry sha")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(gitPath, "push")
	Expect(err).NotTo(HaveOccurred())

	// "environment/development-next", "environment/staging-next", "environment/production-next"
	for _, environment := range []string{"environment/development", "environment/staging", "environment/production"} {
		_, err = runGitCmd(gitPath, "checkout", "--orphan", environment)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "commit", "--allow-empty", "-m", "initial commit")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "push", "-u", "origin", environment)
		Expect(err).NotTo(HaveOccurred())

		// Sleep one seconds to differentiate the commits to prevent same hash
		time.Sleep(1 * time.Second)

		_, err = runGitCmd(gitPath, "checkout", "-b", environment+"-next")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "commit", "--allow-empty", "-m", "initial commit next")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(gitPath, "push", "-u", "origin", environment+"-next")
		Expect(err).NotTo(HaveOccurred())

		// Sleep one seconds to differentiate the commits to prevent same hash
		time.Sleep(1 * time.Second)
	}
}

func makeChangeAndHydrateRepo(gitPath string, repoOwner string, repoName string) (string, string) {
	// gitPath, err := os.MkdirTemp("", "*")
	// Expect(err).NotTo(HaveOccurred())

	_, err := runGitCmd(gitPath, "clone", "--verbose", "--progress", "--filter=blob:none", fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPort, repoOwner, repoName), ".")
	Expect(err).NotTo(HaveOccurred())

	_, err = runGitCmd(gitPath, "config", "user.name", "testuser")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(gitPath, "config", "user.email", "testemail@test.com")
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
	_, err = runGitCmd(gitPath, "commit", "-m", "added fake manifests commit with timestamp")
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

		f, err = os.Create(path.Join(gitPath, "hydrator.metadata"))
		Expect(err).NotTo(HaveOccurred())
		str = fmt.Sprintf("{\"drySHA\": \"%s\"}", sha)
		_, err = f.WriteString(str)
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

		_, err = runGitCmd(gitPath, "commit", "-m", "added pending commit from dry sha, "+sha+" from environment "+strings.TrimRight(environment, "-next"))
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
		err := k8sClient.Get(ctx, types.NamespacedName{
			Name:      ctp.Name,
			Namespace: ctp.Namespace,
		}, ctp)
		g.Expect(err).To(Succeed())
		if ctp.Annotations == nil {
			ctp.Annotations = map[string]string{}
		}
		ctp.Annotations[promoterv1alpha1.ReconcileAtAnnotation] = metav1.Now().Format(time.RFC3339)
		err = k8sClient.Update(ctx, ctp)
		g.Expect(err).To(Succeed())
	}, EventuallyTimeout).Should(Succeed())
}
