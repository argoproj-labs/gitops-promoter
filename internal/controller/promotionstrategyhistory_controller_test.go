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
	_ "embed"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/event"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

//go:embed testdata/PromotionStrategyHistory.yaml
var testPromotionStrategyHistoryYAML string

var _ = Describe("PromotionStrategyHistory Controller", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Context("When unmarshalling the test data", func() {
		It("should unmarshal the PromotionStrategyHistory resource", func() {
			err := unmarshalYamlStrict(testPromotionStrategyHistoryYAML, &promoterv1alpha1.PromotionStrategyHistory{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("When a PromotionStrategy manages its environments", func() {
		var name string
		var scmSecret *v1.Secret
		var scmProvider *promoterv1alpha1.ScmProvider
		var gitRepo *promoterv1alpha1.GitRepository
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var typeNamespacedName types.NamespacedName

		BeforeEach(func() {
			By("Creating the resources")
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "psh-owned-by-ps", "default")
			setupInitialTestGitRepoOnServer(ctx, gitRepo)
			// Give one environment an activePath override to verify it is propagated to the PSH spec.
			promotionStrategy.Spec.Environments[1].ActivePath = "apps/staging"

			typeNamespacedName = types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			declareDependentsSuccessfulGate(promotionStrategy)
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			createDependentsSuccessfulCommitStatus(ctx, promotionStrategy)
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, promotionStrategy)
			_ = k8sClient.Delete(ctx, gitRepo)
			_ = k8sClient.Delete(ctx, scmProvider)
			_ = k8sClient.Delete(ctx, scmSecret)
		})

		It("creates a PromotionStrategyHistory per environment and cleans up orphans", func() {
			By("Checking that a labeled, owned PromotionStrategyHistory exists per environment")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, promotionStrategy)).To(Succeed())

				for _, environment := range promotionStrategy.Spec.Environments {
					var psh promoterv1alpha1.PromotionStrategyHistory
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(utils.GetPromotionStrategyHistoryName(promotionStrategy.Name, environment.Branch)),
						Namespace: typeNamespacedName.Namespace,
					}, &psh)
					g.Expect(err).To(Succeed())

					g.Expect(psh.Labels[promoterv1alpha1.PromotionStrategyLabel]).To(Equal(utils.KubeSafeLabel(promotionStrategy.Name)))
					g.Expect(psh.Labels[promoterv1alpha1.EnvironmentLabel]).To(Equal(utils.KubeSafeLabel(environment.Branch)))
					g.Expect(metav1.IsControlledBy(&psh, promotionStrategy)).To(BeTrue())

					g.Expect(psh.Spec.RepositoryReference.Name).To(Equal(promotionStrategy.Spec.RepositoryReference.Name))
					g.Expect(psh.Spec.ActiveBranch).To(Equal(environment.Branch))
					if environment.ActivePath != "" {
						g.Expect(psh.Spec.ActivePath).To(Equal(environment.ActivePath))
					} else {
						g.Expect(psh.Spec.ActivePath).To(BeEmpty())
					}
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Removing an environment from the PromotionStrategy")
			removedBranch := promotionStrategy.Spec.Environments[2].Branch
			orphanedName := utils.KubeSafeUniqueName(utils.GetPromotionStrategyHistoryName(promotionStrategy.Name, removedBranch))
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, promotionStrategy)).To(Succeed())
				promotionStrategy.Spec.Environments = promotionStrategy.Spec.Environments[:2]
				g.Expect(k8sClient.Update(ctx, promotionStrategy)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Checking that the orphaned PromotionStrategyHistory is deleted")
			Eventually(func(g Gomega) {
				var psh promoterv1alpha1.PromotionStrategyHistory
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      orphanedName,
					Namespace: typeNamespacedName.Namespace,
				}, &psh)
				g.Expect(errors.IsNotFound(err) || !psh.DeletionTimestamp.IsZero()).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	DescribeTable("ctpUpdateEnqueuesPromotionStrategyHistoryPredicate",
		func(oldStatus, newStatus promoterv1alpha1.ChangeTransferPolicyStatus, expected bool) {
			p := ctpUpdateEnqueuesPromotionStrategyHistoryPredicate()
			e := event.UpdateEvent{
				ObjectOld: &promoterv1alpha1.ChangeTransferPolicy{Status: oldStatus},
				ObjectNew: &promoterv1alpha1.ChangeTransferPolicy{Status: newStatus},
			}
			Expect(p.Update(e)).To(Equal(expected))
		},
		Entry("filters out an unchanged status",
			promoterv1alpha1.ChangeTransferPolicyStatus{
				Active:      promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "abc"}},
				PullRequest: &promoterv1alpha1.PullRequestCommonStatus{ID: "5", State: promoterv1alpha1.PullRequestOpen},
			},
			promoterv1alpha1.ChangeTransferPolicyStatus{
				Active:      promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "abc"}},
				PullRequest: &promoterv1alpha1.PullRequestCommonStatus{ID: "5", State: promoterv1alpha1.PullRequestOpen},
			},
			false,
		),
		Entry("fires when the active hydrated sha moves",
			promoterv1alpha1.ChangeTransferPolicyStatus{
				Active: promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "abc"}},
			},
			promoterv1alpha1.ChangeTransferPolicyStatus{
				Active: promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "def"}},
			},
			true,
		),
		Entry("fires when the pull request state changes",
			promoterv1alpha1.ChangeTransferPolicyStatus{
				PullRequest: &promoterv1alpha1.PullRequestCommonStatus{ID: "5", State: promoterv1alpha1.PullRequestOpen},
			},
			promoterv1alpha1.ChangeTransferPolicyStatus{
				PullRequest: &promoterv1alpha1.PullRequestCommonStatus{ID: "5", State: promoterv1alpha1.PullRequestMerged},
			},
			true,
		),
		Entry("fires when the pull request merged target sha is reported",
			promoterv1alpha1.ChangeTransferPolicyStatus{
				PullRequest: &promoterv1alpha1.PullRequestCommonStatus{ID: "5", State: promoterv1alpha1.PullRequestMerged},
			},
			promoterv1alpha1.ChangeTransferPolicyStatus{
				PullRequest: &promoterv1alpha1.PullRequestCommonStatus{ID: "5", State: promoterv1alpha1.PullRequestMerged, MergedTargetSha: "abc"},
			},
			true,
		),
		Entry("fires when the pull request appears",
			promoterv1alpha1.ChangeTransferPolicyStatus{},
			promoterv1alpha1.ChangeTransferPolicyStatus{
				PullRequest: &promoterv1alpha1.PullRequestCommonStatus{ID: "5", State: promoterv1alpha1.PullRequestOpen},
			},
			true,
		),
		Entry("fires when the pull request disappears",
			promoterv1alpha1.ChangeTransferPolicyStatus{
				PullRequest: &promoterv1alpha1.PullRequestCommonStatus{ID: "5", State: promoterv1alpha1.PullRequestOpen},
			},
			promoterv1alpha1.ChangeTransferPolicyStatus{},
			true,
		),
		Entry("filters out proposed-side churn",
			promoterv1alpha1.ChangeTransferPolicyStatus{
				Proposed: promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "abc"}},
			},
			promoterv1alpha1.ChangeTransferPolicyStatus{
				Proposed: promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "def"}},
			},
			false,
		),
	)

	DescribeTable("shouldSkipHistoryRecalculation",
		func(history []promoterv1alpha1.History, activeSha string, expected bool) {
			Expect(shouldSkipHistoryRecalculation(history, activeSha)).To(Equal(expected))
		},
		Entry("skips when newest history entry describes the active tip",
			[]promoterv1alpha1.History{{
				Active: promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "abc"}},
				PullRequest: &promoterv1alpha1.PullRequestCommonStatus{
					ID:              "5",
					MergedTargetSha: "abc",
				},
			}},
			"abc",
			true,
		),
		Entry("recalculates when newest history entry has no pull request ID",
			// A rebuild that ran before the note was pushed holds the trailer-less entry whose SHAs match
			// the tip; skipping there would pin it over the note-derived history.
			[]promoterv1alpha1.History{{
				Active: promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "abc"}},
				PullRequest: &promoterv1alpha1.PullRequestCommonStatus{
					MergedTargetSha: "abc",
				},
			}},
			"abc",
			false,
		),
		Entry("recalculates when newest history entry targets a different commit than the active tip",
			[]promoterv1alpha1.History{{
				Active: promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "def"}},
				PullRequest: &promoterv1alpha1.PullRequestCommonStatus{
					ID:              "5",
					MergedTargetSha: "def",
				},
			}},
			"abc",
			false,
		),
		Entry("recalculates when newest history entry is only partially populated",
			[]promoterv1alpha1.History{{
				PullRequest: &promoterv1alpha1.PullRequestCommonStatus{
					MergedTargetSha: "abc",
				},
			}},
			"abc",
			false,
		),
		Entry("recalculates when history has never been populated",
			nil,
			"abc",
			false,
		),
		Entry("recalculates when active tip is not yet known",
			[]promoterv1alpha1.History{{}},
			"",
			false,
		),
	)
})
