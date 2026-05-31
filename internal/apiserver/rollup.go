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
	dashboardapi "github.com/argoproj-labs/gitops-promoter/api/dashboard/dashboard"
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// computeEnvironmentRollups builds the per-environment rollup for a PromotionStrategy,
// joining the PromotionStrategy status with its owning ChangeTransferPolicies. The
// returned slice follows the order of ps.Spec.Environments.
func computeEnvironmentRollups(ps *promoterv1alpha1.PromotionStrategy, ctps []promoterv1alpha1.ChangeTransferPolicy) []dashboardapi.EnvironmentRollup {
	if len(ps.Spec.Environments) == 0 {
		return nil
	}

	statusByBranch := make(map[string]*promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for i := range ps.Status.Environments {
		es := &ps.Status.Environments[i]
		statusByBranch[es.Branch] = es
	}

	ctpByActiveBranch := make(map[string]*promoterv1alpha1.ChangeTransferPolicy, len(ctps))
	for i := range ctps {
		ctp := &ctps[i]
		ctpByActiveBranch[ctp.Spec.ActiveBranch] = ctp
	}

	rollups := make([]dashboardapi.EnvironmentRollup, 0, len(ps.Spec.Environments))
	for i := range ps.Spec.Environments {
		env := &ps.Spec.Environments[i]
		rollup := dashboardapi.EnvironmentRollup{
			Branch: env.Branch,
		}

		if ctp, ok := ctpByActiveBranch[env.Branch]; ok {
			rollup.ChangeTransferPolicyName = ctp.Name
		}

		if es, ok := statusByBranch[env.Branch]; ok {
			rollup.Active = es.Active
			rollup.Proposed = es.Proposed
			rollup.PullRequest = es.PullRequest
			rollup.ActiveGates = summarizeGates(es.Active.CommitStatuses)
			rollup.ProposedGates = summarizeGates(es.Proposed.CommitStatuses)
			rollup.Promoted = es.Active.Dry.Sha != "" && es.Active.Dry.Sha == es.Proposed.Dry.Sha
		}

		rollups = append(rollups, rollup)
	}
	return rollups
}

// summarizeGates counts the commit-status gate phases.
func summarizeGates(gates []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase) dashboardapi.GateSummary {
	summary := dashboardapi.GateSummary{Total: len(gates)}
	for i := range gates {
		switch gates[i].Phase {
		case "success":
			summary.Success++
		case "failure":
			summary.Failure++
		case "pending":
			summary.Pending++
		default:
			// Unknown phase; counted only in Total.
		}
	}
	return summary
}
