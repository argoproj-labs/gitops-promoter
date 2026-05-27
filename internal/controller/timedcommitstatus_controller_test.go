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
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// withTimerActiveStatus configures a PromotionStrategy to require the default
// timer commit status, so the TimedCommitStatus controller reconciles its
// environments. Matches the previous suite-wide BeforeAll behavior.
func withTimerActiveStatus(ps *promoterv1alpha1.PromotionStrategy) {
	ps.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
		{Key: "timer"},
	}
}

var _ = Describe("TimedCommitStatus Controller", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("Time Requirement Not Met", func() {
		It("should report pending status when time requirement is not met", func() {
			name, _, _, _ := commitStatusFixture(ctx, "timed-tcs-pending", withTimerActiveStatus)

			By("Creating a TimedCommitStatus resource with 1 hour requirement")
			timedCommitStatus := &promoterv1alpha1.TimedCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-pending",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.TimedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.TimedCommitStatusEnvironments{
						{
							Branch:   testBranchDevelopment,
							Duration: metav1.Duration{Duration: 1 * time.Hour},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, timedCommitStatus)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, timedCommitStatus) })

			By("Waiting for TimedCommitStatus to process the environment")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-pending",
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(tcs.Status.Environments).To(HaveLen(1))
				g.Expect(tcs.Status.Environments[0].Branch).To(Equal(testBranchDevelopment))
				g.Expect(tcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)))

				g.Expect(tcs.Status.Environments[0].Sha).ToNot(BeEmpty(), "Sha should be populated")
				g.Expect(tcs.Status.Environments[0].CommitTime.Time).ToNot(BeZero(), "CommitTime should be populated")
				g.Expect(tcs.Status.Environments[0].RequiredDuration.Duration).To(Equal(1*time.Hour), "RequiredDuration should match spec")
				g.Expect(tcs.Status.Environments[0].AtMostDurationRemaining.Duration).To(BeNumerically(">", 0), "AtMostDurationRemaining should be > 0 when pending")

				commitStatusName := utils.CommitStatusResourceName(ctx, &tcs, testBranchDevelopment)
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				expectedDuration := 1 * time.Hour
				g.Expect(cs.Spec.Description).To(ContainSubstring(expectedDuration.String()), "Description should include the required duration")
				g.Expect(cs.Spec.Description).To(ContainSubstring("duration gate to complete on " + testBranchDevelopment))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Time-Based Gate Met", func() {
		It("should report success status when time requirement is met and no pending promotion", func() {
			name, _, _, _ := commitStatusFixture(ctx, "timed-tcs-met", withTimerActiveStatus)

			By("Creating a TimedCommitStatus resource with very short duration requirement")
			timedCommitStatus := &promoterv1alpha1.TimedCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-time-met",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.TimedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.TimedCommitStatusEnvironments{
						{
							Branch:   testBranchDevelopment,
							Duration: metav1.Duration{Duration: 1 * time.Second}, // Very short so it's already met
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, timedCommitStatus)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, timedCommitStatus) })

			By("Waiting for TimedCommitStatus to transition to success phase after duration is met")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-time-met",
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(tcs.Status.Environments).To(HaveLen(1))
				g.Expect(tcs.Status.Environments[0].Branch).To(Equal(testBranchDevelopment))

				g.Expect(tcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
					"Phase should be success when required duration is met")

				g.Expect(tcs.Status.Environments[0].Sha).ToNot(BeEmpty(), "Sha should be populated")
				g.Expect(tcs.Status.Environments[0].CommitTime.Time).ToNot(BeZero(), "CommitTime should be populated")
				g.Expect(tcs.Status.Environments[0].RequiredDuration.Duration).To(Equal(1*time.Second), "RequiredDuration should match spec")

				g.Expect(tcs.Status.Environments[0].AtMostDurationRemaining.Duration).To(Equal(time.Duration(0)),
					"AtMostDurationRemaining must be 0 for success phase")

				commitStatusName := utils.CommitStatusResourceName(ctx, &tcs, testBranchDevelopment)
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess),
					"CommitStatus phase should be success when gate is met")
				g.Expect(cs.Spec.Description).To(ContainSubstring("Time-based gate requirement met"))
				g.Expect(cs.Labels[promoterv1alpha1.CommitStatusLabel]).To(Equal(promoterv1alpha1.TimedCommitStatusDefaultKey))
				g.Expect(cs.Spec.Name).To(Equal(promoterv1alpha1.TimedCommitStatusDefaultKey + "/" + testBranchDevelopment))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying phase remains success for 5 seconds (doesn't flip back to pending)")
			Consistently(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-time-met",
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(tcs.Status.Environments).To(HaveLen(1))

				g.Expect(tcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
					"Phase must remain success once duration requirement is met")

				g.Expect(tcs.Status.Environments[0].AtMostDurationRemaining.Duration).To(Equal(time.Duration(0)),
					"AtMostDurationRemaining should remain 0")

				commitStatusName := utils.CommitStatusResourceName(ctx, &tcs, testBranchDevelopment)
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

	Describe("Time-Based Gate Not Met (Long Duration)", func() {
		It("should report pending status when time requirement is not met", func() {
			name, _, _, _ := commitStatusFixture(ctx, "timed-tcs-not-met-long", withTimerActiveStatus)

			By("Creating a TimedCommitStatus resource with very long duration requirement")
			timedCommitStatus := &promoterv1alpha1.TimedCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-time-not-met",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.TimedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.TimedCommitStatusEnvironments{
						{
							Branch:   testBranchDevelopment,
							Duration: metav1.Duration{Duration: 24 * time.Hour}, // Very long, won't be met
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, timedCommitStatus)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, timedCommitStatus) })

			By("Waiting for initial TimedCommitStatus to be in pending phase")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-time-not-met",
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(tcs.Status.Environments).To(HaveLen(1))
				g.Expect(tcs.Status.Environments[0].Branch).To(Equal(testBranchDevelopment))

				g.Expect(tcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)),
					"Phase should be pending when required duration not met")

				g.Expect(tcs.Status.Environments[0].Sha).ToNot(BeEmpty(), "Sha should be populated")
				g.Expect(tcs.Status.Environments[0].CommitTime.Time).ToNot(BeZero(), "CommitTime should be populated")
				g.Expect(tcs.Status.Environments[0].RequiredDuration.Duration).To(Equal(24*time.Hour), "RequiredDuration should match spec")

				g.Expect(tcs.Status.Environments[0].AtMostDurationRemaining.Duration).To(BeNumerically(">", 0),
					"AtMostDurationRemaining must be > 0 for pending phase")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying phase stays pending for 10 seconds (doesn't incorrectly switch to success)")
			Consistently(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-time-not-met",
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(tcs.Status.Environments).To(HaveLen(1))

				g.Expect(tcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)),
					"Phase must remain pending while time remaining > 0")

				g.Expect(tcs.Status.Environments[0].AtMostDurationRemaining.Duration).To(BeNumerically(">", 0),
					"AtMostDurationRemaining should still be > 0 (24 hours not elapsed)")

				commitStatusName := utils.CommitStatusResourceName(ctx, &tcs, testBranchDevelopment)
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending),
					"CommitStatus phase must remain pending while gate isn't met")
				expectedDuration := 24 * time.Hour
				g.Expect(cs.Spec.Description).To(ContainSubstring(expectedDuration.String()), "Description should include the required duration")
				g.Expect(cs.Spec.Description).To(ContainSubstring("duration gate to complete on " + testBranchDevelopment))
			}, 10*time.Second, 1*time.Second).Should(Succeed())
		})
	})

	Describe("Time Gate Transition with Open PR", func() {
		It("should trigger ChangeTransferPolicy reconciliation when time gate transitions to success", func() {
			name, gitRepo, _, promotionStrategy := commitStatusFixture(ctx, "timed-tcs-touch-ps", withTimerActiveStatus)

			By("Creating a pending promotion in staging environment")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() {
				_ = os.RemoveAll(gitPath)
			}()
			makeChangeAndHydrateRepo(gitPath, gitRepo, "pending change in staging", "pending change")

			// Trigger webhook to create PR in staging
			var ctpStaging promoterv1alpha1.ChangeTransferPolicy
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: "default",
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

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
			timedCommitStatus := &promoterv1alpha1.TimedCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-touch-ps",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.TimedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.TimedCommitStatusEnvironments{
						{
							Branch:   testBranchDevelopment,
							Duration: metav1.Duration{Duration: 2 * time.Hour}, // Start with long duration
						},
						{
							Branch:   testBranchStaging,
							Duration: metav1.Duration{Duration: 2 * time.Hour},
						},
						{
							Branch:   testBranchProduction,
							Duration: metav1.Duration{Duration: 2 * time.Hour},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, timedCommitStatus)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, timedCommitStatus) })

			By("Waiting for TimedCommitStatus to initially report pending status")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-touch-ps",
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(tcs.Status.Environments).To(HaveLen(3))
				g.Expect(tcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Updating TimedCommitStatus to use 10 second duration to trigger transition")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-touch-ps",
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				tcs.Spec.Environments[0].Duration = metav1.Duration{Duration: 10 * time.Second}
				tcs.Spec.Environments[1].Duration = metav1.Duration{Duration: 10 * time.Second}
				tcs.Spec.Environments[2].Duration = metav1.Duration{Duration: 10 * time.Second}

				err = k8sClient.Update(ctx, &tcs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Waiting for the time gate to transition to success (after 10 seconds)")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-touch-ps",
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(tcs.Status.Environments).To(HaveLen(3))
				g.Expect(tcs.Status.Environments[0].Branch).To(Equal(testBranchDevelopment))
				g.Expect(tcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
					"Dev environment should transition to success after duration")
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Environment Cleanup", func() {
		It("should cleanup orphaned CommitStatus resources when environments are removed", func() {
			name, _, _, _ := commitStatusFixture(ctx, "timed-tcs-cleanup", withTimerActiveStatus)

			By("Creating a TimedCommitStatus resource tracking all three environments")
			timedCommitStatus := &promoterv1alpha1.TimedCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-cleanup",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.TimedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.TimedCommitStatusEnvironments{
						{
							Branch:   testBranchDevelopment,
							Duration: metav1.Duration{Duration: 1 * time.Second},
						},
						{
							Branch:   testBranchStaging,
							Duration: metav1.Duration{Duration: 1 * time.Second},
						},
						{
							Branch:   testBranchProduction,
							Duration: metav1.Duration{Duration: 1 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, timedCommitStatus)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, timedCommitStatus) })

			By("Waiting for all three CommitStatus resources to be created")
			var oldCommitStatusDevName string
			var oldCommitStatusStagingName string
			var oldCommitStatusProdName string

			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-cleanup",
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(tcs.Status.Environments).To(HaveLen(3))

				oldCommitStatusDevName = utils.CommitStatusResourceName(ctx, &tcs, testBranchDevelopment)
				oldCommitStatusStagingName = utils.CommitStatusResourceName(ctx, &tcs, testBranchStaging)
				oldCommitStatusProdName = utils.CommitStatusResourceName(ctx, &tcs, testBranchProduction)

				oldCsDev := &promoterv1alpha1.CommitStatus{}
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      oldCommitStatusDevName,
					Namespace: "default",
				}, oldCsDev)
				g.Expect(err).NotTo(HaveOccurred())

				oldCsStaging := &promoterv1alpha1.CommitStatus{}
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      oldCommitStatusStagingName,
					Namespace: "default",
				}, oldCsStaging)
				g.Expect(err).NotTo(HaveOccurred())

				oldCsProd := &promoterv1alpha1.CommitStatus{}
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      oldCommitStatusProdName,
					Namespace: "default",
				}, oldCsProd)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Updating TimedCommitStatus to only track development (removing staging and production)")
			Eventually(func(g Gomega) {
				var tcs promoterv1alpha1.TimedCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-cleanup",
					Namespace: "default",
				}, &tcs)
				g.Expect(err).NotTo(HaveOccurred())

				tcs.Spec.Environments = []promoterv1alpha1.TimedCommitStatusEnvironments{
					{
						Branch:   testBranchDevelopment,
						Duration: metav1.Duration{Duration: 1 * time.Second},
					},
				}

				err = k8sClient.Update(ctx, &tcs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying the development CommitStatus still exists")
			Eventually(func(g Gomega) {
				devCs := &promoterv1alpha1.CommitStatus{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      oldCommitStatusDevName,
					Namespace: "default",
				}, devCs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying old staging and production CommitStatus resources are deleted")
			Eventually(func(g Gomega) {
				oldCsStaging := &promoterv1alpha1.CommitStatus{}
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      oldCommitStatusStagingName,
					Namespace: "default",
				}, oldCsStaging)
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue(), "Old staging CommitStatus should be deleted")

				oldCsProd := &promoterv1alpha1.CommitStatus{}
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      oldCommitStatusProdName,
					Namespace: "default",
				}, oldCsProd)
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue(), "Old production CommitStatus should be deleted")
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("commit status key", func() {
		const customKey = "soak-time"

		It("should use custom spec.key on CommitStatus label and name", func() {
			keyName, _, _, _ := commitStatusFixture(ctx, "timed-tcs-key", func(ps *promoterv1alpha1.PromotionStrategy) {
				ps.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
					{Key: customKey},
				}
			})

			tcs := &promoterv1alpha1.TimedCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      keyName + "-custom-key",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.TimedCommitStatusSpec{
					Key: customKey,
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: keyName,
					},
					Environments: []promoterv1alpha1.TimedCommitStatusEnvironments{
						{
							Branch:   testBranchDevelopment,
							Duration: metav1.Duration{Duration: 1 * time.Second},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, tcs)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, tcs) })

			commitStatusName := utils.CommitStatusResourceName(ctx, tcs, testBranchDevelopment)

			Eventually(func(g Gomega) {
				var cs promoterv1alpha1.CommitStatus
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)).To(Succeed())
				g.Expect(cs.Labels[promoterv1alpha1.CommitStatusLabel]).To(Equal(customKey))
				g.Expect(cs.Spec.Name).To(Equal(customKey + "/" + testBranchDevelopment))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})

// Separate Describe block for the test that doesn't need infrastructure
var _ = Describe("TimedCommitStatus Controller - Missing PromotionStrategy", func() {
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
				g.Expect(tcs.Status.Environments).To(BeEmpty())
			}, 2*time.Second, 500*time.Millisecond).Should(Succeed())
		})
	})
})
