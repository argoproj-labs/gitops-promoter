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
	"fmt"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sosedoff/gitkit"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"testing"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	promoterv1alpha1 "github.com/argoproj/promoter/api/v1alpha1"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var gitServer *http.Server
var gitStoragePath string

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("setting up git server")
	var mkDirErr error
	gitStoragePath, mkDirErr = os.MkdirTemp("", "*")
	if mkDirErr != nil {
		panic("could not make temp dir for repo server")
	}
	gitServer = startGitServer(gitStoragePath)

	//setupInitialTestGitRepo("test", "test")

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,

		// The BinaryAssetsDirectory is only required if you want to run the tests directly
		// without call the makefile target test. If not informed it will look for the
		// default path defined in controller-runtime which is /usr/local/kubebuilder/.
		// Note that you must have the required binaries setup under the bin directory to perform
		// the tests directly. When we run make test it will be setup and used automatically.
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.29.0-%s-%s", runtime.GOOS, runtime.GOARCH)),
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

})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())

	// TODO: why dose shutting down break tests
	//_ = gitServer.Shutdown(context.Background())

	err = os.RemoveAll(gitStoragePath)
	Expect(err).NotTo(HaveOccurred())
})

func startGitServer(gitStoragePath string) *http.Server {
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

	server := &http.Server{Addr: ":5000", Handler: service}

	//http.Handle("/", service)

	go func() {
		// Start HTTP server
		if err := server.ListenAndServe(); err != nil {
			log.Fatal(err)
		}
	}()

	return server
}

func setupInitialTestGitRepo(owner string, name string) {
	gitPath, err := os.MkdirTemp("", "*")
	if err != nil {
		panic("could not make temp dir for repo server")
	}

	//GinkgoWriter.TeeTo(os.Stdout)
	//GinkgoWriter.Println(gitPath)

	err = runGitCmd(gitPath, "git", "clone", fmt.Sprintf("http://localhost:5000/%s/%s", owner, name), ".")
	Expect(err).NotTo(HaveOccurred())

	f, err := os.Create(path.Join(gitPath, "hydrator.metadata"))
	Expect(err).NotTo(HaveOccurred())
	_, err = f.WriteString("{\"drySHA\": \"5468b78dfef356739559abf1f883cd713794fd96\"}")
	Expect(err).NotTo(HaveOccurred())
	err = f.Close()
	Expect(err).NotTo(HaveOccurred())

	err = runGitCmd(gitPath, "git", "config", "user.name", "testuser")
	Expect(err).NotTo(HaveOccurred())
	err = runGitCmd(gitPath, "git", "config", "user.email", "testemail@test.com")
	Expect(err).NotTo(HaveOccurred())

	err = runGitCmd(gitPath, "git", "add", "hydrator.metadata")
	Expect(err).NotTo(HaveOccurred())
	err = runGitCmd(gitPath, "git", "commit", "-m", "init commit")
	Expect(err).NotTo(HaveOccurred())
	err = runGitCmd(gitPath, "git", "push")
	Expect(err).NotTo(HaveOccurred())

	err = runGitCmd(gitPath, "git", "checkout", "-B", "environment/development")
	Expect(err).NotTo(HaveOccurred())
	err = runGitCmd(gitPath, "git", "push", "-u", "origin", "environment/development")
	Expect(err).NotTo(HaveOccurred())
	err = runGitCmd(gitPath, "git", "checkout", "-B", "environment/development-next")
	Expect(err).NotTo(HaveOccurred())
	err = runGitCmd(gitPath, "git", "push", "-u", "origin", "environment/development-next")
	Expect(err).NotTo(HaveOccurred())
}

func deleteRepo(owner, name string) {
	err := os.RemoveAll(path.Join(gitStoragePath, owner, name))
	Expect(err).NotTo(HaveOccurred())
}

func runGitCmd(directory string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	cmd.Dir = directory

	cmd.Env = []string{
		"GIT_TERMINAL_PROMPT=0",
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	if err := cmd.Wait(); err != nil {
		return err
	}

	return nil
}
