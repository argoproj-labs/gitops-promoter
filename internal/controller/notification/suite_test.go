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

package notification

import (
	"context"
	"fmt"
	"path/filepath"
	goruntime "runtime"
	"testing"
	"time"

	"go.uber.org/zap/zapcore"

	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/argoproj-labs/gitops-promoter/internal/notification/events"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// This is the envtest end-to-end suite for the notification framework. It wires the REAL
// notification pipeline against a real (etcd-backed) API server:
//
//	events.MemoryBroker (Publisher+Subscriber)
//	  -> PromoterNotificationReconciler.handleEvent (match against PromoterNotification CRs)
//	    -> internal work queue -> workerPool
//	      -> webhookDispatcher.Dispatch (render+sign, HTTP send, retry/backoff, dead-letter)
//	        -> httptest.Server (the receiver under assertion)
//	        -> PromoterNotification .status (TotalDelivered/TotalFailed/LastDelivery/Ready)
//
// Events are injected via broker.Publish, which is exactly what the CTP/PromotionStrategy
// reconcilers do in production (see publishShaTransitions). This exercises the whole
// notification framework end to end without standing up the git-server/hydrator harness; the
// upstream git transition that produces the Event is the only part stubbed.
//
// The unit-level mechanics (retry/backoff, dead-letter, header/signature merge, status writes
// on a fake client) are covered exhaustively in delivery_test.go and
// promoternotification_controller_test.go; this suite proves they hold across a real manager,
// real CRs, and real HTTP.

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	ctx       context.Context
	cancel    context.CancelFunc
	scheme    = utils.GetScheme()

	// broker is the real in-memory broker the notification controller subscribes to. Specs
	// publish events through it to drive the pipeline.
	broker *events.MemoryBroker
)

func TestNotificationE2E(t *testing.T) {
	t.Parallel()

	RegisterFailHandler(Fail)

	c, _ := GinkgoConfiguration()
	RunSpecs(t, "Notification Controller envtest Suite", c)
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true), zap.Level(zapcore.Level(-4)), func(o *zap.Options) {
		o.TimeEncoder = zapcore.RFC3339NanoTimeEncoder
	}))

	By("bootstrapping the test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "config", "crd", "bases"),
		},
		ErrorIfCRDPathMissing:   true,
		ControlPlaneStopTimeout: 1 * time.Minute,
		// BinaryAssetsDirectory pins the envtest control-plane binaries. The shared bin/k8s
		// only contains 1.36.0; the KUBEBUILDER_ASSETS env override (set by the test runner)
		// takes precedence over this default when present.
		BinaryAssetsDirectory: filepath.Join("..", "..", "..", "bin", "k8s",
			fmt.Sprintf("1.36.0-%s-%s", goruntime.GOOS, goruntime.GOARCH)),
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	//nolint:fatcontext // ctx is the suite-scoped package var, intentionally assigned in setup
	ctx, cancel = context.WithCancel(context.Background())

	By("setting up the manager and notification pipeline")
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
		// Leader election is disabled; when off, controller-runtime runs all runnables
		// (including the worker pool, which declares NeedLeaderElection()==true) regardless.
	})
	Expect(err).NotTo(HaveOccurred())

	broker = events.NewMemoryBroker()

	// The real webhook dispatcher: renders+signs via the payload seam, sends real HTTP, and
	// persists status on the real CR. recorder is nil (the dispatcher is nil-tolerant).
	dispatcher := NewWebhookDispatcher(mgr.GetClient(), nil)

	reconciler := NewPromoterNotificationReconciler(
		mgr.GetClient(),
		mgr.GetScheme(),
		nil, // events.EventRecorder is unused by Reconcile; nil is safe.
		broker,
		dispatcher,
	)
	Expect(reconciler.SetupWithManager(mgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		err := mgr.Start(ctx)
		Expect(err).NotTo(HaveOccurred(), "failed to start manager")
	}()

	By("waiting for the manager cache to sync")
	Eventually(func() bool {
		return mgr.GetCache().WaitForCacheSync(ctx)
	}, 30*time.Second).Should(BeTrue(), "manager cache should sync")
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	if cancel != nil {
		cancel()
	}
	if testEnv != nil {
		Expect(testEnv.Stop()).To(Succeed())
	}
})
