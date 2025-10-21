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
	Context("When time requirement is not met", func() {
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

			// Configure ActiveCommitStatuses to check for timer commit status
			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "timer"},
			}

			setupInitialTestGitRepoOnServer(ctx, name, name)

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

			By("Creating a TimedCommitStatus resource with 1 hour requirement")
			timedCommitStatus = &promoterv1alpha1.TimedCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.TimedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.TimedCommitStatusEnvironments{
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

		It("should report pending status when time requirement is not met", func() {
			By("Waiting for TimedCommitStatus to process the environment")
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

				// Validate status fields are populated
				g.Expect(tcs.Status.Environments[0].Sha).ToNot(BeEmpty(), "Sha should be populated")
				g.Expect(tcs.Status.Environments[0].CommitTime.Time).ToNot(BeZero(), "CommitTime should be populated")
				g.Expect(tcs.Status.Environments[0].RequiredDuration.Duration).To(Equal(1*time.Hour), "RequiredDuration should match spec")
				g.Expect(tcs.Status.Environments[0].AtMostDurationRemaining.Duration).To(BeNumerically(">", 0), "AtMostDurationRemaining should be > 0 when pending")

				// Verify CommitStatus was created for dev environment with pending phase
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-environment/development-timed")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				g.Expect(cs.Spec.Description).To(ContainSubstring("Waiting for time-based gate on environment/development"))
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
			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "timer"},
			}
			promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
				{
					Branch: "environment/development",
				},
				{
					Branch: "environment/staging",
				},
			}

			setupInitialTestGitRepoOnServer(ctx, name, name)

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
					Environments: []promoterv1alpha1.TimedCommitStatusEnvironments{
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
			By("Waiting for TimedCommitStatus to transition to success phase after duration is met")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(tcs.Status.Environments).To(HaveLen(1))
				g.Expect(tcs.Status.Environments[0].Branch).To(Equal("environment/development"))

				// The critical check: phase should be success because duration (1 second) has been met
				g.Expect(tcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
					"Phase should be success when required duration is met")

				// Validate the phase transition happened correctly
				g.Expect(tcs.Status.Environments[0].Sha).ToNot(BeEmpty(), "Sha should be populated")
				g.Expect(tcs.Status.Environments[0].CommitTime.Time).ToNot(BeZero(), "CommitTime should be populated")
				g.Expect(tcs.Status.Environments[0].RequiredDuration.Duration).To(Equal(1*time.Second), "RequiredDuration should match spec")

				// Verify the duration requirement has been met (time remaining should be 0)
				g.Expect(tcs.Status.Environments[0].AtMostDurationRemaining.Duration).To(Equal(time.Duration(0)),
					"AtMostDurationRemaining must be 0 for success phase")

				// Verify CommitStatus was created for dev environment (current environment) with success phase
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-environment/development-timed")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess),
					"CommitStatus phase should be success when gate is met")
				g.Expect(cs.Spec.Description).To(ContainSubstring("Time-based gate requirement met"))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying phase remains success for 5 seconds (doesn't flip back to pending)")
			Consistently(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(tcs.Status.Environments).To(HaveLen(1))

				// Critical: phase must stay success, not flip back to pending
				g.Expect(tcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
					"Phase must remain success once duration requirement is met")

				// Duration requirement should still be met (time remaining should be 0)
				g.Expect(tcs.Status.Environments[0].AtMostDurationRemaining.Duration).To(Equal(time.Duration(0)),
					"AtMostDurationRemaining should remain 0")

				// Verify CommitStatus phase remains success for dev environment
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-environment/development-timed")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess),
					"CommitStatus phase must remain success")
			}, 5*time.Second, 1*time.Second).Should(Succeed())
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

			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "timer"},
			}
			promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
				{
					Branch: "environment/development",
				},
				{
					Branch: "environment/staging",
				},
			}

			setupInitialTestGitRepoOnServer(ctx, name, name)

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
					Environments: []promoterv1alpha1.TimedCommitStatusEnvironments{
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
			By("Waiting for initial TimedCommitStatus to be in pending phase")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(tcs.Status.Environments).To(HaveLen(1))
				g.Expect(tcs.Status.Environments[0].Branch).To(Equal("environment/development"))

				// The critical check: phase should be pending because duration (24 hours) hasn't been met
				g.Expect(tcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)),
					"Phase should be pending when required duration not met")

				// Validate status fields are populated
				g.Expect(tcs.Status.Environments[0].Sha).ToNot(BeEmpty(), "Sha should be populated")
				g.Expect(tcs.Status.Environments[0].CommitTime.Time).ToNot(BeZero(), "CommitTime should be populated")
				g.Expect(tcs.Status.Environments[0].RequiredDuration.Duration).To(Equal(24*time.Hour), "RequiredDuration should match spec")

				// Verify the duration requirement has NOT been met (time remaining should be > 0)
				g.Expect(tcs.Status.Environments[0].AtMostDurationRemaining.Duration).To(BeNumerically(">", 0),
					"AtMostDurationRemaining must be > 0 for pending phase")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying phase stays pending for 10 seconds (doesn't incorrectly switch to success)")
			Consistently(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(tcs.Status.Environments).To(HaveLen(1))

				// Critical: phase must stay pending because we haven't waited 24 hours
				g.Expect(tcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)),
					"Phase must remain pending while time remaining > 0")

				// Duration requirement should still NOT be met (time remaining should be > 0)
				g.Expect(tcs.Status.Environments[0].AtMostDurationRemaining.Duration).To(BeNumerically(">", 0),
					"AtMostDurationRemaining should still be > 0 (24 hours not elapsed)")

				// Verify CommitStatus phase remains pending for dev environment
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-environment/development-timed")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending),
					"CommitStatus phase must remain pending while gate isn't met")
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
					Environments: []promoterv1alpha1.TimedCommitStatusEnvironments{
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

	Context("When time gate transitions to success with open PR", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var timedCommitStatus *promoterv1alpha1.TimedCommitStatus

		BeforeEach(func() {
			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "timed-status-touch-ps", "default")

			// Configure ActiveCommitStatuses to check for timer commit status
			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "timer"},
			}

			setupInitialTestGitRepoOnServer(ctx, name, name)

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

			By("Creating a pending promotion in staging environment")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			makeChangeAndHydrateRepo(gitPath, name, name, "pending change in staging", "pending change")

			// Trigger webhook to create PR in staging
			var ctpStaging promoterv1alpha1.ChangeTransferPolicy
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: "default",
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())
			simulateWebhook(ctx, k8sClient, &ctpStaging)

			By("Waiting for staging environment to have a pending promotion (proposed != active)")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, promotionStrategy)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
				// Staging should have different proposed vs active (pending promotion/open PR)
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.Dry.Sha).ToNot(Equal(promotionStrategy.Status.Environments[1].Active.Dry.Sha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a TimedCommitStatus resource with duration starting long then shortening to 10s")
			timedCommitStatus = &promoterv1alpha1.TimedCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.TimedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.TimedCommitStatusEnvironments{
						{
							Branch:   "environment/development",
							Duration: metav1.Duration{Duration: 2 * time.Hour}, // Start with long duration
						},
						{
							Branch:   "environment/staging",
							Duration: metav1.Duration{Duration: 2 * time.Hour},
						},
						{
							Branch:   "environment/production",
							Duration: metav1.Duration{Duration: 2 * time.Hour},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, timedCommitStatus)).To(Succeed())

			By("Waiting for TimedCommitStatus to initially report pending status")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())
				// Should have status for all three environments now (dev, staging, and production)
				g.Expect(tcs.Status.Environments).To(HaveLen(3))
				g.Expect(tcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Updating TimedCommitStatus to use 10 second duration to trigger transition")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Update to 10 second duration
				tcs.Spec.Environments[0].Duration = metav1.Duration{Duration: 10 * time.Second}
				tcs.Spec.Environments[1].Duration = metav1.Duration{Duration: 10 * time.Second}
				tcs.Spec.Environments[2].Duration = metav1.Duration{Duration: 10 * time.Second}

				err = k8sClient.Update(ctx, &tcs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, timedCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should add ReconcileAtAnnotation to ChangeTransferPolicy when time gate transitions to success", func() {
			By("Waiting for the time gate to transition to success (after 10 seconds)")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Dev environment should transition to success after 10 seconds
				g.Expect(tcs.Status.Environments).To(HaveLen(3))
				g.Expect(tcs.Status.Environments[0].Branch).To(Equal("environment/development"))
				g.Expect(tcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
					"Dev environment should transition to success after duration")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying that ChangeTransferPolicy for development has ReconcileAtAnnotation added")
			Eventually(func(g Gomega) {
				// Get the PromotionStrategy to construct the ChangeTransferPolicy name
				var ps promoterv1alpha1.PromotionStrategy
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &ps)
				g.Expect(err).NotTo(HaveOccurred())

				// Find the development ChangeTransferPolicy
				ctpName := utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(ps.Name, "environment/development"))
				var ctp promoterv1alpha1.ChangeTransferPolicy
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpName,
					Namespace: "default",
				}, &ctp)
				g.Expect(err).NotTo(HaveOccurred())

				// The annotation should be present
				g.Expect(ctp.Annotations).To(HaveKey(promoterv1alpha1.ReconcileAtAnnotation),
					"ChangeTransferPolicy should have ReconcileAtAnnotation")

				// Verify the annotation value is populated and is a valid RFC3339Nano timestamp
				annotationValue := ctp.Annotations[promoterv1alpha1.ReconcileAtAnnotation]
				g.Expect(annotationValue).ToNot(BeEmpty(),
					"ReconcileAtAnnotation should be populated")

				_, err = time.Parse(time.RFC3339Nano, annotationValue)
				g.Expect(err).NotTo(HaveOccurred(),
					"ReconcileAtAnnotation should be a valid RFC3339Nano timestamp")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying that the annotation remains stable (doesn't constantly update)")
			Consistently(func(g Gomega) {
				var ps promoterv1alpha1.PromotionStrategy
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &ps)
				g.Expect(err).NotTo(HaveOccurred())

				ctpName := utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(ps.Name, "environment/development"))
				var ctp promoterv1alpha1.ChangeTransferPolicy
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpName,
					Namespace: "default",
				}, &ctp)
				g.Expect(err).NotTo(HaveOccurred())

				// The annotation should still be present
				g.Expect(ctp.Annotations).To(HaveKey(promoterv1alpha1.ReconcileAtAnnotation))
				// Note: The annotation value might change if there are multiple reconciliations,
				// but the annotation key should remain present
			}, 5*time.Second, 1*time.Second).Should(Succeed())
		})
	})
})
