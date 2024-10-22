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
	"os"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("PromotionStrategy Controller", func() {

	Context("When reconciling a resource with no commit statuses", func() {
		ctx := context.Background()

		It("should successfully reconcile the resource", func() {
			By("Creating the resources")

			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy := promotionStrategyResource(ctx, "promotion-strategy-no-commit-status", "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// Check that ChangeTransferPolicy's are created
			ctpDev := promoterv1alpha1.ChangeTransferPolicy{}
			ctpStaging := promoterv1alpha1.ChangeTransferPolicy{}
			ctpProd := promoterv1alpha1.ChangeTransferPolicy{}

			pullRequestDev := promoterv1alpha1.PullRequest{}
			pullRequestStaging := promoterv1alpha1.PullRequest{}
			pullRequestProd := promoterv1alpha1.PullRequest{}

			By("Checking that all the ChangeTransferPolicies are created and PRs are created and in their proper state with updated statuses and proper sha's")
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, typeNamespacedName, promotionStrategy)

				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())

				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				g.Expect(len(promotionStrategy.Status.Environments)).To(Equal(3))

				//By("Checking that the ChangeTransferPolicy for development has shas that are not empty, meaning we have reconciled the resource")
				g.Expect(ctpDev.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpDev.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpDev.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(ctpDev.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				//By("Checking that the ChangeTransferPolicy for staging has shas that are not empty, meaning we have reconciled the resource")
				g.Expect(ctpStaging.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpStaging.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(ctpStaging.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				//By("Checking that the ChangeTransferPolicy for production has shas that are not empty, meaning we have reconciled the resource")
				g.Expect(ctpProd.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpProd.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpProd.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(ctpProd.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				//By("Checking that the PromotionStrategy for development environment has the correct sha values from the ChangeTransferPolicy")
				g.Expect(ctpDev.Spec.ActiveBranch).To(Equal("environment/development"))
				g.Expect(ctpDev.Spec.ProposedBranch).To(Equal("environment/development-next"))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.Sha).To(Equal(ctpDev.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).To(Equal(ctpDev.Status.Active.Hydrated.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Active.CommitStatus.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Dry.Sha).To(Equal(ctpDev.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).To(Equal(ctpDev.Status.Proposed.Hydrated.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.CommitStatus.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

				//By("Checking that the PromotionStrategy for staging environment has the correct sha values from the ChangeTransferPolicy")
				g.Expect(ctpStaging.Spec.ActiveBranch).To(Equal("environment/staging"))
				g.Expect(ctpStaging.Spec.ProposedBranch).To(Equal("environment/staging-next"))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Sha).To(Equal(ctpStaging.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Sha).To(Equal(ctpStaging.Status.Active.Hydrated.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Active.CommitStatus.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.Dry.Sha).To(Equal(ctpStaging.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.Hydrated.Sha).To(Equal(ctpStaging.Status.Proposed.Hydrated.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.CommitStatus.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

				//By("Checking that the PromotionStrategy for production environment has the correct sha values from the ChangeTransferPolicy")
				g.Expect(ctpProd.Spec.ActiveBranch).To(Equal("environment/production"))
				g.Expect(ctpProd.Spec.ProposedBranch).To(Equal("environment/production-next"))
				g.Expect(promotionStrategy.Status.Environments[2].Active.Dry.Sha).To(Equal(ctpProd.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Active.Hydrated.Sha).To(Equal(ctpProd.Status.Active.Hydrated.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Active.CommitStatus.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.Dry.Sha).To(Equal(ctpProd.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.Hydrated.Sha).To(Equal(ctpProd.Status.Proposed.Hydrated.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.CommitStatus.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

			}, EventuallyTimeout).Should(Succeed())

			By("Adding a pending commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			makeChangeAndHydrateRepo(gitPath, name, name)

			By("Checking that the pull request for the development environment is created")
			Eventually(func(g Gomega) {
				// Dev PR should exist
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(Succeed())

			}, EventuallyTimeout).Should(Succeed())

			By("Checking that the pull request for the development, staging, and production environments are closed")
			Eventually(func(g Gomega) {
				// The PRs should eventually close because of no commit status checks configured
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

			}, EventuallyTimeout).Should(Succeed())

			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
		})
	})

	Context("When reconciling a resource with active commit statuses", func() {
		It("should successfully reconcile the resource", func() {
			//Skip("Skipping test because of flakiness")
			By("Creating the resource")
			name, scmSecret, scmProvider, gitRepo, activeCommitStatusDevelopment, activeCommitStatusStaging, promotionStrategy := promotionStrategyResource(ctx, "promotion-strategy-with-active-commit-status", "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{
					Key: "health-check",
				},
			}
			activeCommitStatusDevelopment.Spec.Name = "health-check" //nolint: goconst
			activeCommitStatusDevelopment.Labels = map[string]string{
				promoterv1alpha1.CommitStatusLabel: "health-check", //nolint: goconst
			}
			activeCommitStatusStaging.Spec.Name = "health-check" //nolint: goconst
			activeCommitStatusStaging.Labels = map[string]string{
				promoterv1alpha1.CommitStatusLabel: "health-check", //nolint: goconst
			}

			By("Adding a pending commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			makeChangeAndHydrateRepo(gitPath, name, name)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, activeCommitStatusDevelopment)).To(Succeed())
			Expect(k8sClient.Create(ctx, activeCommitStatusStaging)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// We should now get PRs created for the ChangeTransferPolicies
			// Check that ProposedCommit are created
			ctpDev := promoterv1alpha1.ChangeTransferPolicy{}
			ctpStaging := promoterv1alpha1.ChangeTransferPolicy{}
			ctpProd := promoterv1alpha1.ChangeTransferPolicy{}

			pullRequestDev := promoterv1alpha1.PullRequest{}
			pullRequestStaging := promoterv1alpha1.PullRequest{}
			pullRequestProd := promoterv1alpha1.PullRequest{}
			By("Checking that all the ChangeTransferPolicies and PRs are created and in their proper state")
			Eventually(func(g Gomega) {
				// Make sure proposed commits are created and the associated PRs
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())

				// Dev PR should be closed because it is the lowest level environment
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Succeed())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())

			}, EventuallyTimeout).Should(Succeed())

			By("Updating the commit status for the development environment to success")
			Eventually(func(g Gomega) {

				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      activeCommitStatusDevelopment.Name,
					Namespace: activeCommitStatusDevelopment.Namespace,
				}, activeCommitStatusDevelopment)
				g.Expect(err).To(Succeed())

				_, err = runGitCmd(gitPath, "fetch")
				Expect(err).NotTo(HaveOccurred())
				sha, err := runGitCmd(gitPath, "rev-parse", "origin/"+ctpDev.Spec.ActiveBranch)
				Expect(err).NotTo(HaveOccurred())
				sha = strings.TrimSpace(sha)

				// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Active.Hydrated.Sha).To(Equal(sha))

				g.Expect(sha).To(Not(Equal("")))
				activeCommitStatusDevelopment.Spec.Sha = sha
				activeCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				err = k8sClient.Update(ctx, activeCommitStatusDevelopment)
				GinkgoLogr.Info("Updated commit status for development to sha: " + sha)
				g.Expect(err).To(Succeed())

			}, EventuallyTimeout).Should(Succeed())

			By("By checking that the commit status has been copied with the previous environments (development) active hydrated sha")
			Eventually(func(g Gomega) {
				var copiedCommitStatus promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, promoterv1alpha1.CopiedProposedCommitPrefixNameLabel+activeCommitStatusDevelopment.Name),
					Namespace: typeNamespacedName.Namespace,
				}, &copiedCommitStatus)
				g.Expect(err).To(Succeed())
				g.Expect(copiedCommitStatus.Labels[promoterv1alpha1.CommitStatusCopyLabel]).To(Equal("true"))
				// Need to look into why this is flakey
				g.Expect(copiedCommitStatus.Spec.Sha).To(Equal(ctpStaging.Status.Proposed.Hydrated.Sha))
			}, EventuallyTimeout).Should(Succeed())

			By("By checking that the staging pull request has been merged and the production pull request is still open")
			Eventually(func(g Gomega) {

				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())

			}, EventuallyTimeout).Should(Succeed())

			var shaProdProposedBeforePRClose string
			By("Updating the commit status for the staging environment to success")
			Eventually(func(g Gomega) {

				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      activeCommitStatusStaging.Name,
					Namespace: activeCommitStatusStaging.Namespace,
				}, activeCommitStatusStaging)
				g.Expect(err).To(Succeed())

				_, err = runGitCmd(gitPath, "fetch")
				Expect(err).NotTo(HaveOccurred())
				sha, err := runGitCmd(gitPath, "rev-parse", "origin/"+ctpStaging.Spec.ActiveBranch)
				Expect(err).NotTo(HaveOccurred())
				sha = strings.TrimSpace(sha)

				// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.Active.Hydrated.Sha).To(Equal(sha))

				// Get an updated proposed commit for prod so that we can use save the sha before we update the commit status letting it merge
				_, err = runGitCmd(gitPath, "fetch")
				Expect(err).NotTo(HaveOccurred())
				shaProdProposed, err := runGitCmd(gitPath, "rev-parse", "origin/"+ctpProd.Spec.ProposedBranch)
				Expect(err).NotTo(HaveOccurred())
				shaProdProposed = strings.TrimSpace(shaProdProposed)

				// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())
				g.Expect(ctpProd.Status.Proposed.Hydrated.Sha).To(Equal(shaProdProposed))
				shaProdProposedBeforePRClose = ctpProd.Status.Proposed.Hydrated.Sha

				activeCommitStatusStaging.Spec.Sha = sha
				activeCommitStatusStaging.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				err = k8sClient.Update(ctx, activeCommitStatusStaging)
				GinkgoLogr.Info("Updated commit status for staging to sha: " + sha)
				g.Expect(err).To(Succeed())
			}, EventuallyTimeout).Should(Succeed())

			By("By checking that the commit status has been copied with the previous environments (staging) active hydrated sha")
			Eventually(func(g Gomega) {
				// Get the copied commit status
				var copiedCommitStatus promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, promoterv1alpha1.CopiedProposedCommitPrefixNameLabel+activeCommitStatusStaging.Name),
					Namespace: typeNamespacedName.Namespace,
				}, &copiedCommitStatus)
				g.Expect(err).To(Succeed())
				g.Expect(copiedCommitStatus.Labels[promoterv1alpha1.CommitStatusCopyLabel]).To(Equal("true"))
				g.Expect(copiedCommitStatus.Spec.Sha).To(Equal(shaProdProposedBeforePRClose))
			}, EventuallyTimeout).Should(Succeed())

			By("By checking that the production pull request has been merged")
			Eventually(func(g Gomega) {

				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

			}, EventuallyTimeout).Should(Succeed())

			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
		})
	})

	Context("When reconciling a resource with a proposed commit status", func() {
		It("should successfully reconcile the resource", func() {
			//Skip("Skipping test because of flakiness")
			By("Creating the resource")
			name, scmSecret, scmProvider, gitRepo, proposedCommitStatusDevelopment, proposedCommitStatusStaging, promotionStrategy := promotionStrategyResource(ctx, "promotion-strategy-with-proposed-commit-status", "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{
					Key: "no-deployments-allowed",
				},
			}
			proposedCommitStatusDevelopment.Spec.Name = "no-deployments-allowed"
			proposedCommitStatusDevelopment.Labels = map[string]string{
				promoterv1alpha1.CommitStatusLabel: "no-deployments-allowed",
			}
			proposedCommitStatusStaging.Spec.Name = "no-deployments-allowed"
			proposedCommitStatusStaging.Labels = map[string]string{
				promoterv1alpha1.CommitStatusLabel: "no-deployments-allowed",
			}

			By("Adding a pending commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			makeChangeAndHydrateRepo(gitPath, name, name)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, proposedCommitStatusDevelopment)).To(Succeed())
			Expect(k8sClient.Create(ctx, proposedCommitStatusStaging)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// We should now get PRs created for the ProposedCommits
			// Check that ChangeTransferPolicy are created
			ctpDev := promoterv1alpha1.ChangeTransferPolicy{}
			ctpStaging := promoterv1alpha1.ChangeTransferPolicy{}
			ctpProd := promoterv1alpha1.ChangeTransferPolicy{}

			pullRequestDev := promoterv1alpha1.PullRequest{}
			pullRequestStaging := promoterv1alpha1.PullRequest{}
			pullRequestProd := promoterv1alpha1.PullRequest{}
			By("Checking that all the ChangeTransferPolicies and PRs are created and in their proper state")
			Eventually(func(g Gomega) {
				// Make sure proposed commits are created and the associated PRs
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())

				// Dev PR should stay open because it has a proposed commit
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(Succeed())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Succeed())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())

			}, EventuallyTimeout).Should(Succeed())

			By("Updating the commit status for the development environment to success")
			Eventually(func(g Gomega) {

				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      proposedCommitStatusDevelopment.Name,
					Namespace: proposedCommitStatusDevelopment.Namespace,
				}, proposedCommitStatusDevelopment)
				g.Expect(err).To(Succeed())

				_, err = runGitCmd(gitPath, "fetch")
				Expect(err).NotTo(HaveOccurred())
				sha, err := runGitCmd(gitPath, "rev-parse", "origin/"+ctpDev.Spec.ProposedBranch)
				Expect(err).NotTo(HaveOccurred())
				sha = strings.TrimSpace(sha)

				// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Proposed.Hydrated.Sha).To(Equal(sha))

				g.Expect(sha).To(Not(Equal("")))
				proposedCommitStatusDevelopment.Spec.Sha = sha
				proposedCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				err = k8sClient.Update(ctx, proposedCommitStatusDevelopment)
				GinkgoLogr.Info("Updated commit status for development to sha: " + sha)
				g.Expect(err).To(Succeed())

			}, EventuallyTimeout).Should(Succeed())

			By("By checking that the development pull request has been merged and that staging, production pull request are still open")
			Eventually(func(g Gomega) {

				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Succeed())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())

			}, EventuallyTimeout).Should(Succeed())

			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
		})
	})
})

func promotionStrategyResource(ctx context.Context, name, namespace string) (string, *v1.Secret, *promoterv1alpha1.ScmProvider, *promoterv1alpha1.GitRepository, *promoterv1alpha1.CommitStatus, *promoterv1alpha1.CommitStatus, *promoterv1alpha1.PromotionStrategy) {
	name = name + "-" + utils.KubeSafeUniqueName(ctx, randomString(15))
	setupInitialTestGitRepoOnServer(name, name)

	scmSecret := &v1.Secret{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: nil,
	}

	scmProvider := &promoterv1alpha1.ScmProvider{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.ScmProviderSpec{
			SecretRef: &v1.LocalObjectReference{Name: name},
			Fake:      &promoterv1alpha1.Fake{},
		},
		Status: promoterv1alpha1.ScmProviderStatus{},
	}

	gitRepo := &promoterv1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.GitRepositorySpec{
			Owner: name,
			Name:  name,
			ScmProviderRef: promoterv1alpha1.ObjectReference{
				Name: name,
			},
		},
	}

	commitStatusDevelopment := &promoterv1alpha1.CommitStatus{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "development-" + name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: name,
			},
			Sha:         "",
			Name:        "",
			Description: "",
			Phase:       promoterv1alpha1.CommitPhasePending,
			Url:         "",
		},
	}

	commitStatusStaging := &promoterv1alpha1.CommitStatus{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "staging-" + name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: name,
			},
			Sha:         "",
			Name:        "",
			Description: "",
			Phase:       promoterv1alpha1.CommitPhasePending,
			Url:         "",
		},
	}

	promotionStrategy := &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.PromotionStrategySpec{
			DryBanch: "main",
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: name,
			},
			Environments: []promoterv1alpha1.Environment{
				{Branch: "environment/development"},
				{Branch: "environment/staging"},
				{Branch: "environment/production"},
			},
		},
	}

	return name, scmSecret, scmProvider, gitRepo, commitStatusDevelopment, commitStatusStaging, promotionStrategy
}
