/*
Copyright 2026.

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
	"context"
	"fmt"
	"path/filepath"
	goruntime "runtime"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promotercache "github.com/argoproj-labs/gitops-promoter/internal/cache"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	argocd "github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
)

// recordingAware records the clusters a provider engages.
type recordingAware struct {
	engaged []engagement
	lock    sync.Mutex
}

type engagement struct {
	ctx  context.Context //nolint:containedctx // the engagement's lifetime, asserted on by the specs
	name multicluster.ClusterName
}

func (a *recordingAware) Engage(ctx context.Context, name multicluster.ClusterName, _ cluster.Cluster) error {
	a.lock.Lock()
	defer a.lock.Unlock()
	a.engaged = append(a.engaged, engagement{ctx: ctx, name: name})
	return nil
}

func (a *recordingAware) engagements() []engagement {
	a.lock.Lock()
	defer a.lock.Unlock()
	return append([]engagement(nil), a.engaged...)
}

// startLocalApplicationsProvider starts a LocalApplicationsProvider against restConfig, stopped when the spec ends.
func startLocalApplicationsProvider(restConfig *rest.Config) (*LocalApplicationsProvider, *recordingAware) {
	GinkgoHelper()
	provider := NewLocalApplicationsProvider(restConfig, func(o *cluster.Options) {
		o.Scheme = scheme
		o.Cache = promotercache.ProviderClusterCacheOptions()
	})
	aware := &recordingAware{}
	providerCtx, stop := context.WithCancel(context.Background())
	DeferCleanup(stop)
	go func() {
		defer GinkgoRecover()
		Expect(provider.Start(providerCtx, aware)).To(Succeed())
	}()
	return provider, aware
}

// startEnvWithoutApplicationCRD starts an envtest API server with the promoter CRDs but not the Argo CD Application
// CRD (which ships in test/external_crds), reproducing a cluster without Argo CD installed.
func startEnvWithoutApplicationCRD() *rest.Config {
	GinkgoHelper()
	env := &envtest.Environment{
		UseExistingCluster: new(false),
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "config", "crd", "bases"),
		},
		ErrorIfCRDPathMissing:    true,
		ControlPlaneStopTimeout:  1 * time.Minute,
		AttachControlPlaneOutput: false,
		BinaryAssetsDirectory: filepath.Join("..", "..", "bin", "k8s",
			fmt.Sprintf("1.31.0-%s-%s", goruntime.GOOS, goruntime.GOARCH)),
	}
	envCfg, err := env.Start()
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		Expect(env.Stop()).To(Succeed())
	})
	return envCfg
}

var _ = Describe("LocalApplicationsProvider", func() {
	Context("when the Argo CD Application CRD is installed", func() {
		It("engages the local cluster, serves its Applications, and disengages", func(ctx SpecContext) {
			provider, aware := startLocalApplicationsProvider(cfg)

			_, err := provider.Get(ctx, mcmanager.LocalCluster)
			Expect(err).To(MatchError(multicluster.ErrClusterNotFound), "nothing is served before Engage")

			Expect(provider.Engage(ctx)).To(Succeed())
			Expect(provider.Engaged()).To(BeTrue())
			Expect(provider.Engage(ctx)).To(Succeed(), "Engage is idempotent")
			Expect(aware.engagements()).To(HaveLen(1), "a second Engage must not engage again")
			first := aware.engagements()[0]
			Expect(first.name).To(Equal(mcmanager.LocalCluster))

			cl, err := provider.Get(ctx, mcmanager.LocalCluster)
			Expect(err).NotTo(HaveOccurred())
			Expect(cl.GetClient().List(ctx, &argocd.ApplicationList{})).To(Succeed())
			_, err = provider.Get(ctx, "some-other-cluster")
			Expect(err).To(MatchError(multicluster.ErrClusterNotFound))

			provider.Disengage()
			Expect(provider.Engaged()).To(BeFalse())
			Expect(first.ctx.Done()).To(BeClosed(), "disengaging must end the engagement")
			_, err = provider.Get(ctx, mcmanager.LocalCluster)
			Expect(err).To(MatchError(multicluster.ErrClusterNotFound))

			Expect(provider.Engage(ctx)).To(Succeed(), "the local cluster can be engaged again")
			Expect(aware.engagements()).To(HaveLen(2))
		})

		It("waits for Start before engaging", func(ctx SpecContext) {
			provider := NewLocalApplicationsProvider(cfg)
			engageCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			Expect(provider.Engage(engageCtx)).To(MatchError(context.DeadlineExceeded))
			Expect(provider.Engaged()).To(BeFalse())
		})
	})

	Context("when the Argo CD Application CRD is not installed", func() {
		It("reports ErrApplicationCRDNotInstalled without engaging, and the host cache still starts", func(ctx SpecContext) {
			envCfg := startEnvWithoutApplicationCRD()

			// The host manager's cache never lists Application, so it builds without the CRD.
			opts := promotercache.OptionsForInstanceID(nil, "default")
			Expect(opts.ByObject).NotTo(HaveKey(promotercache.UnpartitionedApplicationObject()))
			opts.Scheme = scheme
			_, err := cache.New(envCfg, opts)
			Expect(err).NotTo(HaveOccurred(), "the host manager must start without the Argo CD Application CRD")

			provider, aware := startLocalApplicationsProvider(envCfg)
			Expect(provider.Engage(ctx)).To(MatchError(ErrApplicationCRDNotInstalled))
			Expect(provider.Engaged()).To(BeFalse())
			Expect(aware.engagements()).To(BeEmpty())
		})
	})
})

var _ = Describe("ControllerConfiguration local Applications", func() {
	It("reports Ready=False and retries while the Argo CD Application CRD is not installed", func(ctx SpecContext) {
		envCfg := startEnvWithoutApplicationCRD()
		envClient, err := client.New(envCfg, client.Options{Scheme: scheme})
		Expect(err).NotTo(HaveOccurred())

		cc, err := loadShippedControllerConfigurationForTests(settings.ControllerConfigurationName)
		Expect(err).NotTo(HaveOccurred())
		Expect(cc.Spec.ArgoCDCommitStatus.WatchLocalApplications).To(BeTrue())
		Expect(envClient.Create(ctx, cc)).To(Succeed())

		provider, _ := startLocalApplicationsProvider(envCfg)
		reconciler := &ControllerConfigurationReconciler{
			Client:              envClient,
			Scheme:              scheme,
			Recorder:            events.NewFakeRecorder(100),
			ControllerNamespace: "default",
			StartupInstanceID:   settings.ControllerInstanceID(),
			LocalApplications:   provider,
		}
		result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(cc)})
		Expect(err).NotTo(HaveOccurred(), "a missing CRD is a state to report, not a reconcile error")
		Expect(result.RequeueAfter).To(Equal(localApplicationsCRDRetryInterval))

		Expect(envClient.Get(ctx, client.ObjectKeyFromObject(cc), cc)).To(Succeed())
		ready := meta.FindStatusCondition(cc.Status.Conditions, string(promoterConditions.Ready))
		Expect(ready).NotTo(BeNil())
		Expect(ready.Status).To(Equal(metav1.ConditionFalse))
		Expect(ready.Reason).To(Equal(string(promoterConditions.ArgoCDApplicationCRDNotInstalled)))
		Expect(cc.Status.ArgoCDCommitStatus).To(Equal(&promoterv1alpha1.ArgoCDCommitStatusControllerStatus{LocalClusterEngaged: false}))
	})

	It("engages and disengages the local cluster as watchLocalApplications changes", func(ctx SpecContext) {
		key := client.ObjectKey{Namespace: "default", Name: settings.ControllerConfigurationName}
		setWatchLocalApplications := func(ctx context.Context, watch bool) {
			GinkgoHelper()
			Eventually(func(g Gomega) {
				cc := &promoterv1alpha1.ControllerConfiguration{}
				g.Expect(k8sClient.Get(ctx, key, cc)).To(Succeed())
				base := cc.DeepCopy()
				cc.Spec.ArgoCDCommitStatus.WatchLocalApplications = watch
				g.Expect(k8sClient.Patch(ctx, cc, client.MergeFrom(base))).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())
		}
		expectEngaged := func(ctx context.Context, engaged bool) {
			GinkgoHelper()
			Eventually(localApplicationsProvider.Engaged, constants.EventuallyTimeout).Should(Equal(engaged))
			Eventually(func(g Gomega) {
				cc := &promoterv1alpha1.ControllerConfiguration{}
				g.Expect(k8sClient.Get(ctx, key, cc)).To(Succeed())
				g.Expect(cc.Status.ArgoCDCommitStatus).NotTo(BeNil())
				g.Expect(cc.Status.ArgoCDCommitStatus.LocalClusterEngaged).To(Equal(engaged))
			}, constants.EventuallyTimeout).Should(Succeed())
		}

		// Other specs in this process rely on local Applications, so always leave the shipped setting in place.
		DeferCleanup(func(cleanupCtx SpecContext) {
			setWatchLocalApplications(cleanupCtx, true)
			expectEngaged(cleanupCtx, true)
		})

		expectEngaged(ctx, true)
		setWatchLocalApplications(ctx, false)
		expectEngaged(ctx, false)
		setWatchLocalApplications(ctx, true)
		expectEngaged(ctx, true)
	})
})
