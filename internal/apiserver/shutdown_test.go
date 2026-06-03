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
	"net"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
	"k8s.io/client-go/dynamic"
)

// startInProcessAPIServerWithDone is like startInProcessAPIServer but also returns
// a channel that is closed when RunWithContext returns (i.e. graceful shutdown is
// fully complete), so tests can measure how long shutdown takes.
func startInProcessAPIServerWithDone(provider *BundleProvider) (cfgCancel context.CancelFunc, done <-chan struct{}, dyn dynamic.Interface) {
	opts := NewOptions()
	opts.RecommendedOptions.SecureServing.BindAddress = net.ParseIP("127.0.0.1")
	opts.RecommendedOptions.SecureServing.BindPort = freeLocalPort()
	opts.RecommendedOptions.SecureServing.ServerCert.CertDirectory = GinkgoT().TempDir()
	opts.RecommendedOptions.Authentication.SkipInClusterLookup = true
	opts.RecommendedOptions.Authentication.RemoteKubeConfigFileOptional = true
	opts.RecommendedOptions.Authorization.RemoteKubeConfigFileOptional = true

	config, err := opts.Config(provider)
	Expect(err).NotTo(HaveOccurred())
	config.GenericConfig.Authorization.Authorizer = authorizerfactory.NewAlwaysAllowAuthorizer()

	server, err := config.Complete().New()
	Expect(err).NotTo(HaveOccurred())

	ctx, cancel := context.WithCancel(context.Background())
	doneCh := make(chan struct{})
	go func() {
		defer GinkgoRecover()
		defer close(doneCh)
		_ = server.GenericAPIServer.PrepareRun().RunWithContext(ctx)
	}()

	Expect(server.GenericAPIServer.LoopbackClientConfig).NotTo(BeNil())
	dyn, err = dynamic.NewForConfig(server.GenericAPIServer.LoopbackClientConfig)
	Expect(err).NotTo(HaveOccurred())

	// Wait until the server is actually serving the aggregated resource.
	Eventually(func() error {
		_, listErr := dyn.Resource(promotionStrategyDetailsGVR).Namespace(testNamespace).
			List(context.Background(), metav1.ListOptions{})
		return listErr
	}, 30*time.Second, 200*time.Millisecond).Should(Succeed())

	return cancel, doneCh, dyn
}

var _ = Describe("APIServer graceful shutdown", func() {
	It("shuts down promptly even with an active watch connection", func() {
		provider := newProviderWithReader(newFakeReader(seedObjects()...))
		cancel, done, dyn := startInProcessAPIServerWithDone(provider)

		By("opening a long-running watch against the aggregated resource")
		w, err := dyn.Resource(promotionStrategyDetailsGVR).Namespace(testNamespace).
			Watch(context.Background(), metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer w.Stop()

		// Drain the initial ADDED event so the watch is fully established and the
		// HTTP/2 stream is actively held open by the apiserver.
		Eventually(w.ResultChan()).Should(Receive())

		By("cancelling the server context (simulating a single Ctrl-C)")
		start := time.Now()
		cancel()

		// With a positive ShutdownWatchTerminationGracePeriod the server actively
		// terminates the in-flight watch, so http.Server.Shutdown returns quickly.
		// Without it, Shutdown blocks until the 60s ShutdownTimeout expires.
		Eventually(done, 30*time.Second, 100*time.Millisecond).Should(BeClosed())
		elapsed := time.Since(start)
		GinkgoWriter.Printf("graceful shutdown took %s\n", elapsed)
		Expect(elapsed).To(BeNumerically("<", 20*time.Second),
			"server should shut down promptly on a single context cancel, not hang until ShutdownTimeout")
	})
})
