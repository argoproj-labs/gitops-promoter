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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/authorization/authorizerfactory"
	"k8s.io/client-go/dynamic"
	clientrest "k8s.io/client-go/rest"
)

// promotionStrategyDetailsGVR is the served resource the dynamic client targets.
var promotionStrategyDetailsGVR = schema.GroupVersionResource{
	Group:    "view.promoter.argoproj.io",
	Version:  "v1alpha1",
	Resource: "promotionstrategydetails",
}

// freeLocalPort asks the OS for an unused TCP port on the loopback interface.
func freeLocalPort() int {
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	defer func() { _ = l.Close() }()
	addr, ok := l.Addr().(*net.TCPAddr)
	Expect(ok).To(BeTrue())
	return addr.Port
}

// startInProcessAPIServer boots the real extension apiserver (secure serving with
// self-signed certs on a random loopback port) backed by the given provider, and
// returns a loopback *rest.Config plus a cancel func that stops the server.
//
// Authentication uses the genericapiserver loopback token; authorization is forced to
// allow-all so the test needs no real kube-apiserver to delegate SubjectAccessReviews to.
func startInProcessAPIServer(provider *BundleProvider) (*clientrest.Config, context.CancelFunc) {
	opts := NewOptions()
	opts.RecommendedOptions.SecureServing.BindAddress = net.ParseIP("127.0.0.1")
	opts.RecommendedOptions.SecureServing.BindPort = freeLocalPort()
	// Write the generated self-signed serving cert to a throwaway dir instead of the
	// default ./apiserver.local.config in the working tree.
	opts.RecommendedOptions.SecureServing.ServerCert.CertDirectory = GinkgoT().TempDir()
	// No real kube-apiserver in the test: don't try to read the in-cluster
	// extension-apiserver-authentication configmap, and tolerate the missing remote.
	opts.RecommendedOptions.Authentication.SkipInClusterLookup = true
	opts.RecommendedOptions.Authentication.RemoteKubeConfigFileOptional = true
	opts.RecommendedOptions.Authorization.RemoteKubeConfigFileOptional = true

	config, err := opts.Config(provider)
	Expect(err).NotTo(HaveOccurred())
	// Allow-all so the loopback client is authorized without a delegated SAR backend.
	config.GenericConfig.Authorization.Authorizer = authorizerfactory.NewAlwaysAllowAuthorizer()

	server, err := config.Complete().New()
	Expect(err).NotTo(HaveOccurred())

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		defer GinkgoRecover()
		_ = server.GenericAPIServer.PrepareRun().RunWithContext(ctx)
	}()

	Expect(server.GenericAPIServer.LoopbackClientConfig).NotTo(BeNil())
	return clientrest.CopyConfig(server.GenericAPIServer.LoopbackClientConfig), cancel
}

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
		return fmt.Errorf("list promotionstrategydetails: %w", listErr)
	}, 30*time.Second, 200*time.Millisecond).Should(Succeed())

	return cancel, doneCh, dyn
}

var _ = Describe("APIServer integration (in-process server + dynamic client)", Ordered, func() {
	var (
		provider *BundleProvider
		dyn      dynamic.Interface
		cancel   context.CancelFunc
	)

	BeforeAll(func() {
		provider = newProviderWithReader(newFakeReader(seedObjects()...))
		cfg, c := startInProcessAPIServer(provider)
		cancel = c

		var err error
		dyn, err = dynamic.NewForConfig(cfg)
		Expect(err).NotTo(HaveOccurred())

		// Wait until the server is actually serving the aggregated resource.
		Eventually(func() error {
			_, listErr := dyn.Resource(promotionStrategyDetailsGVR).Namespace(testNamespace).
				List(context.Background(), metav1.ListOptions{})
			if listErr != nil {
				return fmt.Errorf("list promotionstrategydetails: %w", listErr)
			}
			return nil
		}, 30*time.Second, 200*time.Millisecond).Should(Succeed())
	})

	AfterAll(func() {
		if cancel != nil {
			cancel()
		}
	})

	It("lists bundles in a namespace", func() {
		list, err := dyn.Resource(promotionStrategyDetailsGVR).Namespace(testNamespace).
			List(context.Background(), metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(list.Items).To(HaveLen(1))
		Expect(list.Items[0].GetName()).To(Equal(testPSName))
		Expect(list.Items[0].GetNamespace()).To(Equal(testNamespace))
	})

	It("is namespace scoped on list", func() {
		list, err := dyn.Resource(promotionStrategyDetailsGVR).Namespace("other-namespace").
			List(context.Background(), metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(list.Items).To(BeEmpty())
	})

	It("gets a single bundle by name", func() {
		obj, err := dyn.Resource(promotionStrategyDetailsGVR).Namespace(testNamespace).
			Get(context.Background(), testPSName, metav1.GetOptions{})
		Expect(err).NotTo(HaveOccurred())
		Expect(obj.GetName()).To(Equal(testPSName))

		By("serving the joined children and never a Secret")
		// The embedded promotionStrategy / changeTransferPolicies are present.
		_, found, err := unstructured.NestedMap(obj.Object, "promotionStrategy")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		ctps, found, err := unstructured.NestedSlice(obj.Object, "changeTransferPolicies")
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(ctps).To(HaveLen(1))
	})

	It("returns NotFound for a missing bundle", func() {
		_, err := dyn.Resource(promotionStrategyDetailsGVR).Namespace(testNamespace).
			Get(context.Background(), "does-not-exist", metav1.GetOptions{})
		Expect(err).To(HaveOccurred())
		Expect(apierrors.IsNotFound(err)).To(BeTrue())
	})

	It("streams an initial snapshot then a delta over watch", func() {
		w, err := dyn.Resource(promotionStrategyDetailsGVR).Namespace(testNamespace).
			Watch(context.Background(), metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred())
		defer w.Stop()

		// nextDataEvent returns the next non-Bookmark event, skipping any bookmarks the
		// client/apiserver may interleave (e.g. when client-go promotes the request to a
		// watch-list). It fails the spec if no data event arrives in time.
		nextDataEvent := func() watch.Event {
			var out watch.Event
			Eventually(func(g Gomega) {
				var ev watch.Event
				g.Expect(w.ResultChan()).To(Receive(&ev))
				g.Expect(ev.Type).NotTo(Equal(watch.Bookmark))
				out = ev
			}, 10*time.Second, 10*time.Millisecond).Should(Succeed())
			return out
		}

		By("receiving the initial ADDED snapshot")
		added := nextDataEvent()
		Expect(added.Type).To(Equal(watch.Added))
		addedObj, ok := added.Object.(*unstructured.Unstructured)
		Expect(ok).To(BeTrue())
		Expect(addedObj.GetName()).To(Equal(testPSName))

		By("receiving a delta after a child change is reconciled")
		provider.reconcileKey(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: testPSName})

		delta := nextDataEvent()
		Expect([]watch.EventType{watch.Added, watch.Modified}).To(ContainElement(delta.Type))
		deltaObj, ok := delta.Object.(*unstructured.Unstructured)
		Expect(ok).To(BeTrue())
		Expect(deltaObj.GetName()).To(Equal(testPSName))
	})
})

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
