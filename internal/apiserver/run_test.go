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

package apiserver

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
	"k8s.io/client-go/dynamic"
	clientrest "k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

func integrationAPIServerOptions() *Options {
	opts := NewOptions()
	opts.RecommendedOptions.SecureServing.BindAddress = net.ParseIP("127.0.0.1")
	opts.RecommendedOptions.SecureServing.BindPort = freeLocalPort()
	opts.RecommendedOptions.SecureServing.ServerCert.CertDirectory = GinkgoT().TempDir()
	opts.RecommendedOptions.Authentication.SkipInClusterLookup = true
	opts.RecommendedOptions.Authentication.RemoteKubeConfigFileOptional = true
	opts.RecommendedOptions.Authorization.RemoteKubeConfigFileOptional = true
	return opts
}

func startRunEnvtest() (*envtest.Environment, *clientrest.Config, client.Client) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		Skip("KUBEBUILDER_ASSETS is not set; run via make test or make test-parallel")
	}

	env := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
			filepath.Join("..", "..", "test", "external_crds"),
		},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := env.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	cl, err := client.New(cfg, client.Options{Scheme: utils.GetScheme()})
	Expect(err).NotTo(HaveOccurred())

	return env, cfg, cl
}

func stopRunEnvtest(env *envtest.Environment) {
	if env != nil {
		Expect(env.Stop()).To(Succeed())
	}
}

var _ = Describe("Run", func() {
	It("returns a validation error for invalid options", func() {
		opts := NewOptions()
		opts.RecommendedOptions.SecureServing.BindPort = -1

		err := Run(context.Background(), &clientrest.Config{}, opts)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid apiserver options"))
	})

	It("returns an error when the context is cancelled before the read cache syncs", func() {
		env, cfg, _ := startRunEnvtest()
		defer stopRunEnvtest(env)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		err := Run(ctx, cfg, integrationAPIServerOptions())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(Equal("failed to sync read cache"))
	})
})

var _ = Describe("Run integration (envtest + in-process server)", func() {
	var (
		env *envtest.Environment
		cfg *clientrest.Config
		cl  client.Client
	)

	BeforeEach(func() {
		env, cfg, cl = startRunEnvtest()
		Expect(cl.Create(context.Background(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: testNamespace},
		})).To(Succeed())
	})

	AfterEach(func() {
		stopRunEnvtest(env)
		env = nil
	})

	It("serves promotionstrategydetails from the envtest-backed read cache", func() {
		ps := &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: testPSName, Namespace: testNamespace},
			Spec: promoterv1alpha1.PromotionStrategySpec{
				RepositoryReference: promoterv1alpha1.ObjectReference{Name: "my-repo"},
				Environments:        []promoterv1alpha1.Environment{{Branch: "environment/dev"}},
			},
		}
		Expect(cl.Create(context.Background(), ps)).To(Succeed())

		opts := integrationAPIServerOptions()
		runtime, err := newDashboardRuntime(
			context.Background(),
			cfg,
			opts,
			authorizerfactory.NewAlwaysAllowAuthorizer(),
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(runtime.server.GenericAPIServer.LoopbackClientConfig).NotTo(BeNil())

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			errCh <- runtime.run(ctx)
		}()

		dyn, err := dynamic.NewForConfig(runtime.server.GenericAPIServer.LoopbackClientConfig)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			list, listErr := dyn.Resource(promotionStrategyDetailsGVR).Namespace(testNamespace).
				List(context.Background(), metav1.ListOptions{})
			if listErr != nil {
				return fmt.Errorf("list promotionstrategydetails: %w", listErr)
			}
			if len(list.Items) != 1 {
				return fmt.Errorf("expected 1 bundle, got %d", len(list.Items))
			}
			if list.Items[0].GetName() != testPSName {
				return fmt.Errorf("expected bundle %q, got %q", testPSName, list.Items[0].GetName())
			}
			return nil
		}, 30*time.Second, 200*time.Millisecond).Should(Succeed())

		cancel()
		Eventually(errCh, 30*time.Second, 100*time.Millisecond).Should(Receive(Succeed()))
	})

	It("exits promptly when the context is cancelled with an active watch", func() {
		ps := &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: testPSName, Namespace: testNamespace},
			Spec: promoterv1alpha1.PromotionStrategySpec{
				RepositoryReference: promoterv1alpha1.ObjectReference{Name: "my-repo"},
				Environments:        []promoterv1alpha1.Environment{{Branch: "environment/dev"}},
			},
		}
		Expect(cl.Create(context.Background(), ps)).To(Succeed())

		runtime, err := newDashboardRuntime(
			context.Background(),
			cfg,
			integrationAPIServerOptions(),
			authorizerfactory.NewAlwaysAllowAuthorizer(),
		)
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithCancel(context.Background())
		errCh := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			errCh <- runtime.run(ctx)
		}()

		dyn, err := dynamic.NewForConfig(runtime.server.GenericAPIServer.LoopbackClientConfig)
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() error {
			_, listErr := dyn.Resource(promotionStrategyDetailsGVR).Namespace(testNamespace).
				List(context.Background(), metav1.ListOptions{})
			return listErr
		}, 30*time.Second, 200*time.Millisecond).Should(Succeed())

		w, err := dyn.Resource(promotionStrategyDetailsGVR).Namespace(testNamespace).
			Watch(context.Background(), metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer w.Stop()
		Eventually(w.ResultChan()).Should(Receive())

		start := time.Now()
		cancel()
		Eventually(errCh, 30*time.Second, 100*time.Millisecond).Should(Receive(Succeed()))
		Expect(time.Since(start)).To(BeNumerically("<", 20*time.Second))
	})
})
