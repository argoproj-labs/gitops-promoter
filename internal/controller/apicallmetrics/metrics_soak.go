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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

const (
	prPostureOpen   = "open"
	prPostureClosed = "closed"

	defaultSoakRequeueDuration     = 15 * time.Second
	defaultSoakLongRequeueDuration = 60 * time.Minute
	defaultSoakDuration            = 5 * time.Minute
	// Longer than default soak so timed gates stay pending for the whole soak window.
	defaultSoakTCSGateDuration = 30 * time.Minute
)

func soakRequeueDurationFromEnv() time.Duration {
	return durationFromEnv("PROMOTER_API_METRICS_SOAK_REQUEUE", defaultSoakRequeueDuration)
}

func soakDurationFromEnv() time.Duration {
	return durationFromEnv("PROMOTER_API_METRICS_SOAK_DURATION", defaultSoakDuration)
}

func loadSoakTimedGateByBranchFromEnv() map[string]time.Duration {
	defaultDur := durationFromEnv("PROMOTER_API_METRICS_SOAK_TCS_DURATION", defaultSoakTCSGateDuration)
	return map[string]time.Duration{
		testBranchDevelopment: durationFromEnv("PROMOTER_API_METRICS_SOAK_TCS_DURATION_DEV", defaultDur),
		testBranchStaging:     durationFromEnv("PROMOTER_API_METRICS_SOAK_TCS_DURATION_STAGING", defaultDur),
		testBranchProduction:  durationFromEnv("PROMOTER_API_METRICS_SOAK_TCS_DURATION_PROD", defaultDur),
	}
}

func soakSpecTimeout(soak time.Duration) time.Duration {
	return soak + constants.EventuallyTimeout*2 + 5*time.Minute
}

func defaultFullGateOpenPRsSoakScenario() APICallMetricsScenario {
	return APICallMetricsScenario{
		Name:                     "full_gate_three_env_open_prs_soak",
		EnvironmentCount:         3,
		RequeueDuration:          soakRequeueDurationFromEnv(),
		SoakDuration:             soakDurationFromEnv(),
		TimedGateByBranch:        loadSoakTimedGateByBranchFromEnv(),
		ArgoAppHealthyAfterByEnv: loadArgoAppHealthyAfterByEnvFromEnv(),
		TriggerChange:            true,
		PhaseSnapshots:           true,
		FullGateStack:            true,
		DisableAutoMerge:         true,
		PRPosture:                prPostureOpen,
	}
}

func defaultFullGateClosedPRsSoakScenario() APICallMetricsScenario {
	s := defaultFullGateOpenPRsSoakScenario()
	s.Name = "full_gate_three_env_closed_prs_soak"
	s.PRPosture = prPostureClosed
	// No dry→hydrated promotion push: CTPs stay in sync so no PullRequest CRs are created.
	s.TriggerChange = false
	return s
}

func defaultFullGateOpenPRsSoak60mRequeueScenario() APICallMetricsScenario {
	s := defaultFullGateOpenPRsSoakScenario()
	s.Name = "full_gate_three_env_open_prs_soak_60m_requeue"
	s.RequeueDuration = defaultSoakLongRequeueDuration
	return s
}

func defaultFullGateClosedPRsSoak60mRequeueScenario() APICallMetricsScenario {
	s := defaultFullGateClosedPRsSoakScenario()
	s.Name = "full_gate_three_env_closed_prs_soak_60m_requeue"
	s.RequeueDuration = defaultSoakLongRequeueDuration
	return s
}

func setControllerRequeueDuration(ctx context.Context, requeue time.Duration) error {
	var cc promoterv1alpha1.ControllerConfiguration
	key := types.NamespacedName{Namespace: "default", Name: settings.ControllerConfigurationName}
	if err := k8sClient.Get(ctx, key, &cc); err != nil {
		return err
	}
	applyRequeueDuration(&cc, requeue)
	return k8sClient.Update(ctx, &cc)
}

func establishPRPosture(ctx context.Context, psName, posture string, envCount int, timeout time.Duration) {
	switch posture {
	case prPostureOpen:
		waitOpenPullRequestsForPromotionStrategy(ctx, psName, envCount, timeout)
	case prPostureClosed:
		waitNoPullRequestsForPromotionStrategy(ctx, psName, timeout)
	default:
		Fail("unknown PR posture: " + posture)
	}
}

func promotionStrategyBranches() []string {
	return []string{testBranchDevelopment, testBranchStaging, testBranchProduction}
}

func waitNoPullRequestsForPromotionStrategy(ctx context.Context, psName string, timeout time.Duration) {
	psLabel := utils.KubeSafeLabel(psName)
	Eventually(func(g Gomega) {
		for _, branch := range promotionStrategyBranches() {
			g.Expect(pullRequestForCTP(ctx, g, psName, psLabel, branch)).To(BeNil())
		}
	}, timeout).Should(Succeed())
}

func waitOpenPullRequestsForPromotionStrategy(ctx context.Context, psName string, want int, timeout time.Duration) {
	Eventually(func(g Gomega) {
		open := listOpenPullRequestsForPromotionStrategy(ctx, g, psName)
		g.Expect(open).To(HaveLen(want))
		for _, pr := range open {
			g.Expect(pr.Status.ID).NotTo(BeEmpty())
			g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
		}
	}, timeout).Should(Succeed())
}

func listOpenPullRequestsForPromotionStrategy(ctx context.Context, g Gomega, psName string) []promoterv1alpha1.PullRequest {
	psLabel := utils.KubeSafeLabel(psName)

	var open []promoterv1alpha1.PullRequest
	for _, branch := range promotionStrategyBranches() {
		pr := pullRequestForCTP(ctx, g, psName, psLabel, branch)
		if pr != nil && pr.Status.State == promoterv1alpha1.PullRequestOpen && pr.Status.ID != "" {
			open = append(open, *pr)
		}
	}
	return open
}

func pullRequestForCTP(ctx context.Context, g Gomega, psName, psLabel, branch string) *promoterv1alpha1.PullRequest {
	ctpName := utils.KubeSafeUniqueName(utils.GetChangeTransferPolicyName(psName, branch))
	list := &promoterv1alpha1.PullRequestList{}
	g.Expect(k8sClient.List(ctx, list, &client.ListOptions{
		Namespace: "default",
		LabelSelector: labels.SelectorFromSet(map[string]string{
			promoterv1alpha1.PromotionStrategyLabel:    psLabel,
			promoterv1alpha1.ChangeTransferPolicyLabel: utils.KubeSafeLabel(ctpName),
		}),
	})).To(Succeed())
	if len(list.Items) == 0 {
		return nil
	}
	g.Expect(list.Items).To(HaveLen(1))
	return &list.Items[0]
}

func runSoakPhase(ctx context.Context, psName string, scenario APICallMetricsScenario, promotionWaitTimeout time.Duration) {
	establishPRPosture(ctx, psName, scenario.PRPosture, scenario.EnvironmentCount, promotionWaitTimeout)
	GinkgoLogr.Info("PR posture ready for soak",
		"posture", scenario.PRPosture,
		"soak_duration", scenario.SoakDuration,
		"requeue_duration", scenario.RequeueDuration,
	)
	time.Sleep(scenario.SoakDuration)
}
