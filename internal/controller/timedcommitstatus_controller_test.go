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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("TimedCommitStatus Controller", func() {
	Context("When a lower environment has a pending promotion", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var timedCommitStatus *promoterv1alpha1.TimedCommitStatus

		BeforeEach(func() {
			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "timed-status-pending", "default")

			// Configure ProposedCommitStatuses to check for timed commit status
			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "timed"},
			}

			setupInitialTestGitRepoOnServer(name, name)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			By("Waiting for PromotionStrategy to be reconciled with initial state")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, promotionStrategy)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a pending promotion in dev environment")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			makeChangeAndHydrateRepo(gitPath, name, name, "pending change in dev", "pending change")

			// Trigger webhook to create PR
			var ctpDev promoterv1alpha1.ChangeTransferPolicy
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: "default",
				}, &ctpDev)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())
			simulateWebhook(ctx, k8sClient, &ctpDev)

			By("Waiting for dev environment to have a pending promotion (proposed != active)")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, promotionStrategy)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
				// Dev should have different proposed vs active (pending promotion)
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Dry.Sha).ToNot(Equal(promotionStrategy.Status.Environments[0].Active.Dry.Sha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a TimedCommitStatus resource")
			timedCommitStatus = &promoterv1alpha1.TimedCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.TimedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.EnvironmentTimeCommitStatus{
						{
							Branch:   "environment/development",
							Duration: metav1.Duration{Duration: 1 * time.Hour},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, timedCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, timedCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should report pending status when lower environment has open PR", func() {
			By("Waiting for TimedCommitStatus to process the pending promotion")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have status for dev environment
				g.Expect(tcs.Status.Environments).To(HaveLen(1))
				g.Expect(tcs.Status.Environments[0].Branch).To(Equal("environment/development"))
				g.Expect(tcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)))

				// Verify CommitStatus was created for staging with pending phase
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-environment/staging-timed")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				g.Expect(cs.Spec.Description).To(ContainSubstring("Waiting for pending promotion in environment/development"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When time-based gate is met", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var timedCommitStatus *promoterv1alpha1.TimedCommitStatus

		BeforeEach(func() {
			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "timed-status-time-met", "default")

			// Only use 2 environments for this test
			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "timed"},
			}
			promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
				{
					Branch: "environment/development",
				},
				{
					Branch: "environment/staging",
				},
			}

			setupInitialTestGitRepoOnServer(name, name)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			By("Waiting for PromotionStrategy to be reconciled")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, promotionStrategy)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(2))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).ToNot(BeEmpty())
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.CommitTime.Time).ToNot(BeZero())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a TimedCommitStatus resource with very short duration requirement")
			timedCommitStatus = &promoterv1alpha1.TimedCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.TimedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.EnvironmentTimeCommitStatus{
						{
							Branch:   "environment/development",
							Duration: metav1.Duration{Duration: 1 * time.Second}, // Very short so it's already met
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, timedCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, timedCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should report success status when time requirement is met and no pending promotion", func() {
			By("Waiting for TimedCommitStatus to process")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(tcs.Status.Environments).To(HaveLen(1))
				g.Expect(tcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

				// Verify CommitStatus was created for staging with success phase
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-environment/staging-timed")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				g.Expect(cs.Spec.Description).To(ContainSubstring("Time-based gate requirement met"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When time-based gate is not met", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var timedCommitStatus *promoterv1alpha1.TimedCommitStatus

		BeforeEach(func() {
			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "timed-status-time-not-met", "default")

			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "timed"},
			}
			promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
				{
					Branch: "environment/development",
				},
				{
					Branch: "environment/staging",
				},
			}

			setupInitialTestGitRepoOnServer(name, name)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			By("Waiting for PromotionStrategy to be reconciled")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, promotionStrategy)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(2))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).ToNot(BeEmpty())
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.CommitTime.Time).ToNot(BeZero())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a TimedCommitStatus resource with very long duration requirement")
			timedCommitStatus = &promoterv1alpha1.TimedCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.TimedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.EnvironmentTimeCommitStatus{
						{
							Branch:   "environment/development",
							Duration: metav1.Duration{Duration: 24 * time.Hour}, // Very long, won't be met
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, timedCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, timedCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should report pending status when time requirement is not met", func() {
			By("Waiting for initial TimedCommitStatus to be created")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(tcs.Status.Environments).To(HaveLen(1))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying status remains pending for 10 seconds")
			Consistently(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(tcs.Status.Environments).To(HaveLen(1))
				g.Expect(tcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)))

				// Verify CommitStatus was created for staging with pending phase
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-environment/staging-timed")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				g.Expect(cs.Spec.Description).To(ContainSubstring("Waiting for time-based gate"))
			}, 10*time.Second, 1*time.Second).Should(Succeed())
		})
	})

	Context("When PromotionStrategy is not found", func() {
		const resourceName = "timed-status-no-ps"

		ctx := context.Background()

		var timedCommitStatus *promoterv1alpha1.TimedCommitStatus

		BeforeEach(func() {
			By("Creating only a TimedCommitStatus resource without PromotionStrategy")
			timedCommitStatus = &promoterv1alpha1.TimedCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.TimedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: "non-existent",
					},
					Environments: []promoterv1alpha1.EnvironmentTimeCommitStatus{
						{
							Branch:   "environment/dev",
							Duration: metav1.Duration{Duration: 1 * time.Hour},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, timedCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, timedCommitStatus)
		})

		It("should handle missing PromotionStrategy gracefully", func() {
			By("Verifying the TimedCommitStatus exists but doesn't process environments")
			Consistently(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())
				// Status should be empty since PromotionStrategy doesn't exist
				g.Expect(tcs.Status.Environments).To(BeEmpty())
			}, 2*time.Second, 500*time.Millisecond).Should(Succeed())
		})
	})
})
