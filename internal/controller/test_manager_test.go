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
	"context"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	kubeconfigprovider "sigs.k8s.io/multicluster-runtime/providers/kubeconfig"

	promotercache "github.com/argoproj-labs/gitops-promoter/internal/cache"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// startPartitionedManager builds a multicluster manager with instance-id cache
// partitioning matching cmd/main.go and registers controllers needed for migration tests.
func startPartitionedManager(ctx context.Context, cfg *rest.Config, namespace string, instanceID *string) context.CancelFunc {
	mgrCtx, cancel := context.WithCancel(ctx)
	stopped := make(chan struct{})
	skipNameValidation := true
	scheme := utils.GetScheme()

	provider := kubeconfigprovider.New(kubeconfigprovider.Options{
		Namespace:             constants.KubeconfigSecretNamespace,
		KubeconfigSecretLabel: constants.KubeconfigSecretLabel,
		KubeconfigSecretKey:   constants.KubeconfigSecretKey,
		ClusterOptions: []cluster.Option{
			func(clusterOptions *cluster.Options) {
				clusterOptions.Scheme = scheme
			},
		},
	})

	mcMgr, err := mcmanager.New(cfg, provider, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
		HealthProbeBindAddress: "0",
		Cache:                  promotercache.OptionsForInstanceID(instanceID),
		Controller: config.Controller{
			SkipNameValidation: &skipNameValidation,
		},
	})
	Expect(err).NotTo(HaveOccurred())

	Expect(provider.SetupWithManager(mgrCtx, mcMgr)).To(Succeed())

	localMgr := mcMgr.GetLocalManager()

	settingsMgr := settings.NewManager(localMgr.GetClient(), localMgr.GetAPIReader(), settings.ManagerConfig{
		ControllerNamespace: namespace,
	})

	ctpReconciler := &ChangeTransferPolicyReconciler{
		Client:      localMgr.GetClient(),
		Scheme:      localMgr.GetScheme(),
		Recorder:    localMgr.GetEventRecorder("ChangeTransferPolicy"),
		SettingsMgr: settingsMgr,
	}
	Expect(ctpReconciler.SetupWithManager(mgrCtx, localMgr)).To(Succeed())

	Expect((&CommitStatusReconciler{
		Client:      localMgr.GetClient(),
		Scheme:      localMgr.GetScheme(),
		Recorder:    localMgr.GetEventRecorder("CommitStatus"),
		SettingsMgr: settingsMgr,
		EnqueueCTP:  ctpReconciler.GetEnqueueFunc(),
	}).SetupWithManager(mgrCtx, localMgr)).To(Succeed())

	Expect((&TimedCommitStatusReconciler{
		Client:      localMgr.GetClient(),
		Scheme:      localMgr.GetScheme(),
		Recorder:    localMgr.GetEventRecorder("TimedCommitStatus"),
		SettingsMgr: settingsMgr,
		EnqueueCTP:  ctpReconciler.GetEnqueueFunc(),
	}).SetupWithManager(mgrCtx, localMgr)).To(Succeed())

	Expect((&GitCommitStatusReconciler{
		Client:      localMgr.GetClient(),
		Scheme:      localMgr.GetScheme(),
		Recorder:    localMgr.GetEventRecorder("GitCommitStatus"),
		SettingsMgr: settingsMgr,
		EnqueueCTP:  ctpReconciler.GetEnqueueFunc(),
	}).SetupWithManager(mgrCtx, localMgr)).To(Succeed())

	Expect((&WebRequestCommitStatusReconciler{
		Client:      localMgr.GetClient(),
		Scheme:      localMgr.GetScheme(),
		Recorder:    localMgr.GetEventRecorder("WebRequestCommitStatus"),
		SettingsMgr: settingsMgr,
		EnqueueCTP:  ctpReconciler.GetEnqueueFunc(),
	}).SetupWithManager(mgrCtx, localMgr)).To(Succeed())

	Expect((&ArgoCDCommitStatusReconciler{
		Manager:            mcMgr,
		SettingsMgr:        settingsMgr,
		KubeConfigProvider: provider,
		Recorder:           localMgr.GetEventRecorder("ArgoCDCommitStatus"),
	}).SetupWithManager(mgrCtx, mcMgr)).To(Succeed())

	Expect((&PromotionStrategyReconciler{
		Client:      localMgr.GetClient(),
		Scheme:      localMgr.GetScheme(),
		Recorder:    localMgr.GetEventRecorder("PromotionStrategy"),
		SettingsMgr: settingsMgr,
		EnqueueCTP:  ctpReconciler.GetEnqueueFunc(),
	}).SetupWithManager(mgrCtx, localMgr)).To(Succeed())

	Expect((&PullRequestReconciler{
		Client:      localMgr.GetClient(),
		Scheme:      localMgr.GetScheme(),
		Recorder:    localMgr.GetEventRecorder("PullRequest"),
		SettingsMgr: settingsMgr,
	}).SetupWithManager(mgrCtx, localMgr)).To(Succeed())

	Expect((&ScmProviderReconciler{
		Client:   localMgr.GetClient(),
		Scheme:   localMgr.GetScheme(),
		Recorder: localMgr.GetEventRecorder("ScmProvider"),
	}).SetupWithManager(mgrCtx, localMgr)).To(Succeed())

	Expect((&GitRepositoryReconciler{
		Client:   localMgr.GetClient(),
		Scheme:   localMgr.GetScheme(),
		Recorder: localMgr.GetEventRecorder("GitRepository"),
	}).SetupWithManager(mgrCtx, localMgr)).To(Succeed())

	Expect(localMgr.AddHealthzCheck("healthz", healthz.Ping)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		defer close(stopped)
		Expect(mcMgr.Start(mgrCtx)).To(Succeed())
	}()

	Eventually(func() bool {
		return localMgr.GetCache().WaitForCacheSync(mgrCtx)
	}, constants.EventuallyTimeout).Should(BeTrue(), fmt.Sprintf("partitioned manager cache should sync (instanceID=%v)", instanceID))

	stop := func() {
		cancel()
		Eventually(stopped).WithTimeout(constants.EventuallyTimeout).Should(BeClosed())
	}

	return stop
}
