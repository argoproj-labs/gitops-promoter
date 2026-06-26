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
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

var _ = Describe("APICallMetrics", Serial, func() {
	runScenario := func(scenario APICallMetricsScenario) {
		gateWaitTimeout := scenarioWaitTimeout(scenario)
		promotionWaitTimeout := constants.EventuallyTimeout
		effectiveRequeue := metricsRequeue
		if scenario.RequeueDuration > 0 {
			effectiveRequeue = scenario.RequeueDuration
		}
		requeueStr := effectiveRequeue.String()
		timedGateReport := make(map[string]string, len(scenario.TimedGateByBranch))
		for branch, d := range scenario.TimedGateByBranch {
			timedGateReport[branch] = d.String()
		}
		collector := newMetricsReportCollector(scenario.Name, "", requeueStr)
		collector.waitTimeout = gateWaitTimeout.String()
		collector.timedGateByBranch = timedGateReport

		if effectiveRequeue != metricsRequeue {
			Expect(setControllerRequeueDuration(ctx, effectiveRequeue)).To(Succeed())
			DeferCleanup(func() { _ = setControllerRequeueDuration(ctx, metricsRequeue) })
		}

		plainName := strings.ReplaceAll("metrics-"+scenario.Name, "_", "-")
		psName, scmSecret, scmProvider, gitRepo, ps := promotionStrategyResource(ctx, plainName, "default")
		collector.gitRepo = gitRepo.Name
		configurePromotionStrategy(ps, scenario)

		setupInitialTestGitRepoOnServer(ctx, gitRepo)
		gitBefore := gitOperationSnapshot(gitRepo)
		scmBefore := scmCallSnapshot(gitRepo)

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Create(ctx, ps)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, ps)
			_ = k8sClient.Delete(ctx, gitRepo)
			_ = k8sClient.Delete(ctx, scmProvider)
			_ = k8sClient.Delete(ctx, scmSecret)
		})

		var (
			acs          *promoterv1alpha1.ArgoCDCommitStatus
			tcs          *promoterv1alpha1.TimedCommitStatus
			devGCS       *promoterv1alpha1.GitCommitStatus
			stagingGCS   *promoterv1alpha1.GitCommitStatus
			prodGCS      *promoterv1alpha1.GitCommitStatus
			devWRCS      *promoterv1alpha1.WebRequestCommitStatus
			stagingWRCS  *promoterv1alpha1.WebRequestCommitStatus
			prodWRCS     *promoterv1alpha1.WebRequestCommitStatus
			argoDev      argocd.Application
			argoStaging  argocd.Application
			argoProd     argocd.Application
			approvalSrvs [3]*httptest.Server
		)

		if scenario.FullGateStack {
			approvalSrvs[0] = startApprovalHTTPServer()
			approvalSrvs[1] = startApprovalHTTPServer()
			approvalSrvs[2] = startApprovalHTTPServer()
			DeferCleanup(func() {
				for _, s := range approvalSrvs {
					if s != nil {
						s.Close()
					}
				}
			})

			acs = newArgoCDCommitStatus(psName, psName, plainName)
			tcs = newTimedCommitStatus(psName+"-timer", psName, scenario.TimedGateByBranch)
			devGCS = newGitCommitStatus(psName+"-"+devGCSKey, psName, devGCSKey)
			stagingGCS = newGitCommitStatus(psName+"-"+stagingGCSKey, psName, stagingGCSKey)
			prodGCS = newGitCommitStatus(psName+"-"+prodGCSKey, psName, prodGCSKey)
			devWRCS = newApprovalWRCS(psName+"-"+wrcsDevKey, psName, wrcsDevKey, approvalSrvs[0].URL)
			stagingWRCS = newApprovalWRCS(psName+"-"+wrcsStagingKey, psName, wrcsStagingKey, approvalSrvs[1].URL)
			prodWRCS = newApprovalWRCS(psName+"-"+wrcsProdKey, psName, wrcsProdKey, approvalSrvs[2].URL)
			argoDev, argoStaging, argoProd = argocdApplications("default", plainName, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)

			Expect(k8sClient.Create(ctx, acs)).To(Succeed())
			Expect(k8sClient.Create(ctx, tcs)).To(Succeed())
			Expect(k8sClient.Create(ctx, devGCS)).To(Succeed())
			Expect(k8sClient.Create(ctx, stagingGCS)).To(Succeed())
			Expect(k8sClient.Create(ctx, prodGCS)).To(Succeed())
			Expect(k8sClient.Create(ctx, devWRCS)).To(Succeed())
			Expect(k8sClient.Create(ctx, stagingWRCS)).To(Succeed())
			Expect(k8sClient.Create(ctx, prodWRCS)).To(Succeed())
			Expect(k8sClient.Create(ctx, &argoProd)).To(Succeed())
			Expect(k8sClientDev.Create(ctx, &argoDev)).To(Succeed())
			Expect(k8sClientStaging.Create(ctx, &argoStaging)).To(Succeed())
			DeferCleanup(func() {
				for _, obj := range []client.Object{acs, tcs, devGCS, stagingGCS, prodGCS, devWRCS, stagingWRCS, prodWRCS} {
					if obj != nil {
						_ = k8sClient.Delete(ctx, obj)
					}
				}
				_ = k8sClientDev.Delete(ctx, &argoDev)
				_ = k8sClientStaging.Delete(ctx, &argoStaging)
				_ = k8sClient.Delete(ctx, &argoProd)
			})
		}

		if scenario.TriggerChange {
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath) }()
			makeChangeAndHydrateRepo(gitPath, gitRepo, "metrics test dry commit", "metrics test hydrated commit")
		}

		waitCTPsReady(ctx, psName, scenario.EnvironmentCount, promotionWaitTimeout)

		if scenario.SoakDuration > 0 {
			if scenario.PhaseSnapshots {
				collector.addPhase("soak_start",
					gitOperationDelta(gitBefore, gitOperationSnapshot(gitRepo)),
					scmCallDelta(scmBefore, scmCallSnapshot(gitRepo)),
				)
			}
			runSoakPhase(ctx, psName, scenario, promotionWaitTimeout)
		} else if scenario.FullGateStack {
			waitProposedGatesSuccess(ctx, promotionWaitTimeout,
				[]*promoterv1alpha1.GitCommitStatus{devGCS},
				[]*promoterv1alpha1.WebRequestCommitStatus{devWRCS},
				[]string{testBranchDevelopment},
			)
			waitCTPPromotionComplete(ctx, psName, testBranchDevelopment, promotionWaitTimeout)
			if scenario.PhaseSnapshots {
				collector.addPhase("after_proposed_gates",
					gitOperationDelta(gitBefore, gitOperationSnapshot(gitRepo)),
					scmCallDelta(scmBefore, scmCallSnapshot(gitRepo)),
				)
			}

			envApps := []struct {
				branch string
				app    *argocd.Application
			}{
				{testBranchDevelopment, &argoDev},
				{testBranchStaging, &argoStaging},
				{testBranchProduction, &argoProd},
			}
			for i, step := range envApps {
				waitTimedGateSuccessForBranch(ctx, tcs, step.branch, gateWaitTimeout)
				delay := scenario.ArgoAppHealthyAfterByEnv[step.branch]
				driveArgoGateSuccess(ctx, acs, step.app, psName, step.branch, delay, gateWaitTimeout)
				if i+1 < len(envApps) {
					next := envApps[i+1]
					waitProposedGatesSuccess(ctx, promotionWaitTimeout, proposedGatesForEnv(next.branch, devGCS, stagingGCS, prodGCS), proposedWRCSForEnv(next.branch, devWRCS, stagingWRCS, prodWRCS), []string{next.branch})
					waitCTPPromotionComplete(ctx, psName, next.branch, gateWaitTimeout)
				}
				if scenario.PhaseSnapshots {
					phase := "after_" + branchShortName(step.branch)
					collector.addPhase(phase,
						gitOperationDelta(gitBefore, gitOperationSnapshot(gitRepo)),
						scmCallDelta(scmBefore, scmCallSnapshot(gitRepo)),
					)
				}
			}
		} else if scenario.EnvironmentCount >= 3 {
			waitEnvironmentsPromotedInOrder(ctx, psName,
				[]string{testBranchDevelopment, testBranchStaging, testBranchProduction},
				promotionWaitTimeout,
			)
		}

		finalGitBreakdown := gitOperationDelta(gitBefore, gitOperationSnapshot(gitRepo))
		finalSCMBreakdown := scmCallDelta(scmBefore, scmCallSnapshot(gitRepo))
		report := collector.finalReport("final", finalGitBreakdown, finalSCMBreakdown)
		emitAPICallMetricsReport(report)
		Expect(report.GitCLI.Total).To(BeNumerically(">", 0))
		if scenario.FullGateStack {
			Expect(report.SCM.Total).To(BeNumerically(">", 0))
			if scenario.EnvironmentCount >= 3 && scenario.SoakDuration == 0 {
				Expect(finalSCMBreakdown["PullRequest/merge"]).To(BeNumerically(">=", 3))
			}
		}
	}

	It("collects git CLI metrics for single_env_no_gates", func() {
		runScenario(APICallMetricsScenario{
			Name:             "single_env_no_gates",
			EnvironmentCount: 1,
			RequeueDuration:  metricsRequeue,
			TriggerChange:    true,
		})
	})

	It("collects git CLI metrics for three_env_no_gates", func() {
		runScenario(APICallMetricsScenario{
			Name:             "three_env_no_gates",
			EnvironmentCount: 3,
			RequeueDuration:  metricsRequeue,
			TriggerChange:    true,
		})
	})

	It("collects git CLI metrics for full_gate_stack_three_env", func(_ context.Context) {
		scenario := defaultFullGateScenario()
		runScenario(scenario)
	}, SpecTimeout(scenarioSpecTimeout(defaultFullGateScenario())))

	It("collects git CLI metrics for full_gate_three_env_open_prs_soak", func(_ context.Context) {
		scenario := defaultFullGateOpenPRsSoakScenario()
		runScenario(scenario)
	}, SpecTimeout(soakSpecTimeout(defaultFullGateOpenPRsSoakScenario().SoakDuration)))

	It("collects git CLI metrics for full_gate_three_env_closed_prs_soak", func(_ context.Context) {
		scenario := defaultFullGateClosedPRsSoakScenario()
		runScenario(scenario)
	}, SpecTimeout(soakSpecTimeout(defaultFullGateClosedPRsSoakScenario().SoakDuration)))

	It("collects git CLI metrics for full_gate_three_env_open_prs_soak_60m_requeue", func(_ context.Context) {
		scenario := defaultFullGateOpenPRsSoak60mRequeueScenario()
		runScenario(scenario)
	}, SpecTimeout(soakSpecTimeout(defaultFullGateOpenPRsSoak60mRequeueScenario().SoakDuration)))

	It("collects git CLI metrics for full_gate_three_env_closed_prs_soak_60m_requeue", func(_ context.Context) {
		scenario := defaultFullGateClosedPRsSoak60mRequeueScenario()
		runScenario(scenario)
	}, SpecTimeout(soakSpecTimeout(defaultFullGateClosedPRsSoak60mRequeueScenario().SoakDuration)))
})

func proposedGatesForEnv(branch string, dev, staging, prod *promoterv1alpha1.GitCommitStatus) []*promoterv1alpha1.GitCommitStatus {
	switch branch {
	case testBranchDevelopment:
		return []*promoterv1alpha1.GitCommitStatus{dev}
	case testBranchStaging:
		return []*promoterv1alpha1.GitCommitStatus{staging}
	case testBranchProduction:
		return []*promoterv1alpha1.GitCommitStatus{prod}
	default:
		return nil
	}
}

func proposedWRCSForEnv(branch string, dev, staging, prod *promoterv1alpha1.WebRequestCommitStatus) []*promoterv1alpha1.WebRequestCommitStatus {
	switch branch {
	case testBranchDevelopment:
		return []*promoterv1alpha1.WebRequestCommitStatus{dev}
	case testBranchStaging:
		return []*promoterv1alpha1.WebRequestCommitStatus{staging}
	case testBranchProduction:
		return []*promoterv1alpha1.WebRequestCommitStatus{prod}
	default:
		return nil
	}
}

func argoAppClient(branch string) client.Client {
	switch branch {
	case testBranchDevelopment:
		return k8sClientDev
	case testBranchStaging:
		return k8sClientStaging
	default:
		return k8sClient
	}
}

func branchShortName(branch string) string {
	switch branch {
	case testBranchDevelopment:
		return "dev"
	case testBranchStaging:
		return "staging"
	case testBranchProduction:
		return "prod"
	default:
		return branch
	}
}

func waitCTPsReady(ctx context.Context, psName string, envCount int, timeout time.Duration) {
	Eventually(func(g Gomega) {
		var ps promoterv1alpha1.PromotionStrategy
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: psName, Namespace: "default"}, &ps)).To(Succeed())
		g.Expect(ps.Status.Environments).To(HaveLen(envCount))
		for _, env := range ps.Status.Environments {
			g.Expect(env.Proposed.Hydrated.Sha).ToNot(BeEmpty())
			g.Expect(env.Active.Hydrated.Sha).ToNot(BeEmpty())
		}
	}, timeout).Should(Succeed())
}

func waitProposedGatesSuccess(ctx context.Context, timeout time.Duration, gcs []*promoterv1alpha1.GitCommitStatus, wrcs []*promoterv1alpha1.WebRequestCommitStatus, branches []string) {
	Eventually(func(g Gomega) {
		for i, branch := range branches {
			if i < len(gcs) && gcs[i] != nil {
				csName := utils.CommitStatusResourceName(ctx, gcs[i], branch)
				var cs promoterv1alpha1.CommitStatus
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, &cs)).To(Succeed())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}
			if i < len(wrcs) && wrcs[i] != nil {
				csName := utils.CommitStatusResourceName(ctx, wrcs[i], branch)
				var cs promoterv1alpha1.CommitStatus
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, &cs)).To(Succeed())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}
		}
	}, timeout).Should(Succeed())
}

func waitTimedGateSuccessForBranch(ctx context.Context, tcs *promoterv1alpha1.TimedCommitStatus, branch string, timeout time.Duration) {
	Eventually(func(g Gomega) {
		var updated promoterv1alpha1.TimedCommitStatus
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: tcs.Name, Namespace: tcs.Namespace}, &updated)).To(Succeed())
		for _, env := range updated.Status.Environments {
			if env.Branch == branch {
				g.Expect(env.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				return
			}
		}
		g.Expect(updated.Status.Environments).To(ContainElement(HaveField("Branch", branch)))
	}, timeout).Should(Succeed())
	csName := utils.CommitStatusResourceName(ctx, tcs, branch)
	Eventually(func(g Gomega) {
		var cs promoterv1alpha1.CommitStatus
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, &cs)).To(Succeed())
		g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
	}, timeout).Should(Succeed())
}

func waitCTPPromotionComplete(ctx context.Context, psName, branch string, timeout time.Duration) {
	Eventually(func(g Gomega) {
		var ctp promoterv1alpha1.ChangeTransferPolicy
		g.Expect(k8sClient.Get(ctx, ctpNamespacedName(psName, branch), &ctp)).To(Succeed())
		g.Expect(ctp.Status.Proposed.Dry.Sha).NotTo(BeEmpty())
		g.Expect(ctp.Status.Active.Dry.Sha).To(Equal(ctp.Status.Proposed.Dry.Sha))
		g.Expect(ctp.Status.Active.Hydrated.Sha).NotTo(BeEmpty())

		var ps promoterv1alpha1.PromotionStrategy
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: psName, Namespace: "default"}, &ps)).To(Succeed())
		for _, env := range ps.Status.Environments {
			if env.Branch == branch {
				g.Expect(env.Active.Hydrated.Sha).To(Equal(ctp.Status.Active.Hydrated.Sha))
				return
			}
		}
		g.Expect(ps.Status.Environments).To(ContainElement(HaveField("Branch", branch)))
	}, timeout).Should(Succeed())
}

func waitEnvironmentsPromotedInOrder(ctx context.Context, psName string, branches []string, timeout time.Duration) {
	for _, branch := range branches {
		waitCTPPromotionComplete(ctx, psName, branch, timeout)
	}
}

func ctpNamespacedName(psName, branch string) types.NamespacedName {
	return types.NamespacedName{
		Name:      utils.KubeSafeUniqueName(utils.GetChangeTransferPolicyName(psName, branch)),
		Namespace: "default",
	}
}

// driveArgoGateSuccess keeps the Argo CD Application synced to the SHA the ACS gate is
// evaluating (from CommitStatus, falling back to PromotionStrategy active hydrated) and
// waits for Success. A single Eventually is required because upstream promotion can change
// the branch head while a prior env's gate is still settling; a one-shot patch leaves the
// app on a stale revision and the gate pending forever.
func driveArgoGateSuccess(ctx context.Context, acs *promoterv1alpha1.ArgoCDCommitStatus, app *argocd.Application, psName, branch string, delay, timeout time.Duration) {
	if delay > 0 {
		time.Sleep(delay)
	}
	Eventually(func(g Gomega) {
		var ps promoterv1alpha1.PromotionStrategy
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: psName, Namespace: "default"}, &ps)).To(Succeed())
		targetSha := ""
		for _, env := range ps.Status.Environments {
			if env.Branch == branch {
				targetSha = env.Active.Hydrated.Sha
				break
			}
		}
		g.Expect(targetSha).NotTo(BeEmpty())

		csName := utils.CommitStatusResourceName(ctx, acs, branch)
		var cs promoterv1alpha1.CommitStatus
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: csName, Namespace: "default"}, &cs)).To(Succeed())

		appClient := argoAppClient(branch)
		g.Expect(appClient.Get(ctx, types.NamespacedName{Name: app.Name, Namespace: app.Namespace}, app)).To(Succeed())
		if app.Status.Sync.Revision != targetSha ||
			app.Status.Sync.Status != argocd.SyncStatusCodeSynced ||
			app.Status.Health.Status != argocd.HealthStatusHealthy {
			patchArgoAppSyncedHealthy(app, targetSha)
			g.Expect(appClient.Update(ctx, app)).To(Succeed())
			return
		}

		g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
		g.Expect(cs.Spec.Sha).To(Equal(targetSha))
	}, timeout).Should(Succeed())
}
