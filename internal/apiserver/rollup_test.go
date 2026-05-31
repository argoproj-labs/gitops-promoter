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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("computeEnvironmentRollups", func() {
	It("joins PromotionStrategy status with CTPs and summarizes gates", func() {
		ps := &promoterv1alpha1.PromotionStrategy{
			Spec: promoterv1alpha1.PromotionStrategySpec{
				Environments: []promoterv1alpha1.Environment{
					{Branch: "environment/dev"},
					{Branch: "environment/prod"},
				},
			},
			Status: promoterv1alpha1.PromotionStrategyStatus{
				Environments: []promoterv1alpha1.EnvironmentStatus{
					{
						Branch: "environment/dev",
						Active: promoterv1alpha1.CommitBranchState{
							Dry: promoterv1alpha1.CommitShaState{Sha: "aaaaaaa"},
							CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
								{Key: "health", Phase: "success"},
								{Key: "argocd", Phase: "success"},
							},
						},
						Proposed: promoterv1alpha1.CommitBranchState{
							Dry: promoterv1alpha1.CommitShaState{Sha: "aaaaaaa"},
							CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
								{Key: "health", Phase: "success"},
							},
						},
					},
					{
						Branch: "environment/prod",
						Active: promoterv1alpha1.CommitBranchState{
							Dry: promoterv1alpha1.CommitShaState{Sha: "bbbbbbb"},
						},
						Proposed: promoterv1alpha1.CommitBranchState{
							Dry: promoterv1alpha1.CommitShaState{Sha: "ccccccc"},
							CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
								{Key: "health", Phase: "pending"},
								{Key: "argocd", Phase: "failure"},
							},
						},
					},
				},
			},
		}

		ctps := []promoterv1alpha1.ChangeTransferPolicy{
			{
				ObjectMeta: objectMeta("dev-ctp"),
				Spec:       promoterv1alpha1.ChangeTransferPolicySpec{ActiveBranch: "environment/dev"},
			},
			{
				ObjectMeta: objectMeta("prod-ctp"),
				Spec:       promoterv1alpha1.ChangeTransferPolicySpec{ActiveBranch: "environment/prod"},
			},
		}

		rollups := computeEnvironmentRollups(ps, ctps)
		Expect(rollups).To(HaveLen(2))

		By("preserving spec.environments order")
		Expect(rollups[0].Branch).To(Equal("environment/dev"))
		Expect(rollups[1].Branch).To(Equal("environment/prod"))

		By("joining CTP names by active branch")
		Expect(rollups[0].ChangeTransferPolicyName).To(Equal("dev-ctp"))
		Expect(rollups[1].ChangeTransferPolicyName).To(Equal("prod-ctp"))

		By("marking dev promoted (active dry == proposed dry) and summarizing gates")
		Expect(rollups[0].Promoted).To(BeTrue())
		Expect(rollups[0].ActiveGates.Total).To(Equal(2))
		Expect(rollups[0].ActiveGates.Success).To(Equal(2))
		Expect(rollups[0].ProposedGates.Total).To(Equal(1))
		Expect(rollups[0].ProposedGates.Success).To(Equal(1))

		By("marking prod not promoted with mixed gate phases")
		Expect(rollups[1].Promoted).To(BeFalse())
		Expect(rollups[1].ProposedGates.Total).To(Equal(2))
		Expect(rollups[1].ProposedGates.Pending).To(Equal(1))
		Expect(rollups[1].ProposedGates.Failure).To(Equal(1))
		Expect(rollups[1].ProposedGates.Success).To(Equal(0))
	})

	It("handles a PromotionStrategy with no status", func() {
		ps := &promoterv1alpha1.PromotionStrategy{
			Spec: promoterv1alpha1.PromotionStrategySpec{
				Environments: []promoterv1alpha1.Environment{{Branch: "environment/dev"}},
			},
		}

		rollups := computeEnvironmentRollups(ps, nil)
		Expect(rollups).To(HaveLen(1))
		Expect(rollups[0].Branch).To(Equal("environment/dev"))
		Expect(rollups[0].Promoted).To(BeFalse())
		Expect(rollups[0].ActiveGates.Total).To(Equal(0))
	})
})
