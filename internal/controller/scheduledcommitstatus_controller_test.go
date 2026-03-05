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
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

var _ = Describe("ScheduledCommitStatus Controller", Ordered, func() {
	var (
		ctx               context.Context
		name              string
		scmSecret         *v1.Secret
		scmProvider       *promoterv1alpha1.ScmProvider
		gitRepo           *promoterv1alpha1.GitRepository
		promotionStrategy *promoterv1alpha1.PromotionStrategy
	)

	BeforeAll(func() {
		ctx = context.Background()

		By("Setting up test git repository and resources")
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "scheduled-commit-status-test", "default")

		// Configure ProposedCommitStatuses to check for schedule commit status
		promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: "schedule"},
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

		By("Making a change to trigger proposed commits")
		gitPath, err := os.MkdirTemp("", "*")
		Expect(err).NotTo(HaveOccurred())
		defer func() {
			_ = os.RemoveAll(gitPath)
		}()
		makeChangeAndHydrateRepo(gitPath, name, name, "test commit for scheduled", "")

		By("Waiting for proposed commits to appear")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}, promotionStrategy)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
			for _, env := range promotionStrategy.Status.Environments {
				g.Expect(env.Proposed.Hydrated.Sha).ToNot(BeEmpty())
			}
		}, constants.EventuallyTimeout).Should(Succeed())
	})

	AfterAll(func() {
		By("Cleaning up test resources")
		if promotionStrategy != nil {
			_ = k8sClient.Delete(ctx, promotionStrategy)
		}
		if gitRepo != nil {
			_ = k8sClient.Delete(ctx, gitRepo)
		}
		if scmProvider != nil {
			_ = k8sClient.Delete(ctx, scmProvider)
		}
		if scmSecret != nil {
			_ = k8sClient.Delete(ctx, scmSecret)
		}
	})

	Describe("Window Active - Success Phase", func() {
		var scheduledCommitStatus *promoterv1alpha1.ScheduledCommitStatus

		BeforeEach(func() {
			By("Creating a ScheduledCommitStatus resource with active window")
			now := time.Now().UTC()
			// Create a window that includes the current time (started 1 hour ago, lasts 2 hours)
			cronExpr := fmt.Sprintf("%d %d * * *", now.Add(-1*time.Hour).Minute(), now.Add(-1*time.Hour).Hour())

			scheduledCommitStatus = &promoterv1alpha1.ScheduledCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-active",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ScheduledCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.ScheduledCommitStatusEnvironment{
						{
							Branch: testBranchDevelopment,
							Schedule: promoterv1alpha1.Schedule{
								Cron:     cronExpr,
								Window:   "2h",
								Timezone: "UTC",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scheduledCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up ScheduledCommitStatus")
			_ = k8sClient.Delete(ctx, scheduledCommitStatus)
		})

		It("should report success when inside deployment window", func() {
			By("Waiting for ScheduledCommitStatus to process the environment")
			Eventually(func(g Gomega) {
				var scs promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-active",
					Namespace: "default",
				}, &scs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have status for dev environment
				g.Expect(scs.Status.Environments).To(HaveLen(1))
				g.Expect(scs.Status.Environments[0].Branch).To(Equal(testBranchDevelopment))
				g.Expect(scs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

				// Validate status fields are populated
				g.Expect(scs.Status.Environments[0].Sha).ToNot(BeEmpty(), "Sha should be populated")
				g.Expect(scs.Status.Environments[0].CurrentlyInWindow).To(BeTrue(), "Should be in window")
				g.Expect(scs.Status.Environments[0].NextWindowStart).ToNot(BeNil(), "NextWindowStart should be set")
				g.Expect(scs.Status.Environments[0].NextWindowEnd).ToNot(BeNil(), "NextWindowEnd should be set")

				// Verify CommitStatus was created for dev environment with success phase
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-active-"+testBranchDevelopment+"-scheduled")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				g.Expect(cs.Spec.Description).To(ContainSubstring("Deployment window is open"))
				g.Expect(cs.Labels).To(HaveKey(promoterv1alpha1.CommitStatusLabel))
				g.Expect(cs.Labels[promoterv1alpha1.CommitStatusLabel]).To(Equal("schedule"))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying phase remains success for 5 seconds (doesn't flip back to pending)")
			Consistently(func(g Gomega) {
				var scs promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-active",
					Namespace: "default",
				}, &scs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(scs.Status.Environments).To(HaveLen(1))

				// Critical: phase must stay success, not flip back to pending
				g.Expect(scs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
					"Phase must remain success while in window")
				g.Expect(scs.Status.Environments[0].CurrentlyInWindow).To(BeTrue(),
					"Should remain in window")

				// Verify CommitStatus phase remains success
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-active-"+testBranchDevelopment+"-scheduled")
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

	Describe("Window Inactive - Pending Phase", func() {
		var scheduledCommitStatus *promoterv1alpha1.ScheduledCommitStatus

		BeforeEach(func() {
			By("Creating a ScheduledCommitStatus resource with future window")
			now := time.Now().UTC()
			// Create a window that starts 2 hours in the future
			cronExpr := fmt.Sprintf("%d %d * * *", now.Add(2*time.Hour).Minute(), now.Add(2*time.Hour).Hour())

			scheduledCommitStatus = &promoterv1alpha1.ScheduledCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-inactive",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ScheduledCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.ScheduledCommitStatusEnvironment{
						{
							Branch: testBranchDevelopment,
							Schedule: promoterv1alpha1.Schedule{
								Cron:     cronExpr,
								Window:   "1h",
								Timezone: "UTC",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scheduledCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up ScheduledCommitStatus")
			_ = k8sClient.Delete(ctx, scheduledCommitStatus)
		})

		It("should report pending when outside deployment window", func() {
			By("Waiting for ScheduledCommitStatus to process the environment")
			Eventually(func(g Gomega) {
				var scs promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-inactive",
					Namespace: "default",
				}, &scs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have status for dev environment
				g.Expect(scs.Status.Environments).To(HaveLen(1))
				g.Expect(scs.Status.Environments[0].Branch).To(Equal(testBranchDevelopment))
				g.Expect(scs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)))

				// Validate status fields
				g.Expect(scs.Status.Environments[0].Sha).ToNot(BeEmpty(), "Sha should be populated")
				g.Expect(scs.Status.Environments[0].CurrentlyInWindow).To(BeFalse(), "Should not be in window")
				g.Expect(scs.Status.Environments[0].NextWindowStart).ToNot(BeNil(), "NextWindowStart should be set")
				g.Expect(scs.Status.Environments[0].NextWindowEnd).ToNot(BeNil(), "NextWindowEnd should be set")

				// Verify CommitStatus was created for dev environment with pending phase
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-inactive-"+testBranchDevelopment+"-scheduled")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				g.Expect(cs.Spec.Description).To(ContainSubstring("Deployment window is closed"))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying phase stays pending for 10 seconds (doesn't incorrectly switch to success)")
			Consistently(func(g Gomega) {
				var scs promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-inactive",
					Namespace: "default",
				}, &scs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(scs.Status.Environments).To(HaveLen(1))

				// Critical: phase must stay pending because window is 2 hours in the future
				g.Expect(scs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)),
					"Phase must remain pending while outside window")
				g.Expect(scs.Status.Environments[0].CurrentlyInWindow).To(BeFalse(),
					"Should remain outside window")

				// Verify CommitStatus phase remains pending
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-inactive-"+testBranchDevelopment+"-scheduled")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending),
					"CommitStatus phase must remain pending")
			}, 10*time.Second, 1*time.Second).Should(Succeed())
		})
	})

	Describe("Multiple Environments with Different Schedules", func() {
		var scheduledCommitStatus *promoterv1alpha1.ScheduledCommitStatus

		BeforeEach(func() {
			By("Creating ScheduledCommitStatus with multiple environments")
			now := time.Now().UTC()
			// Dev: in window (started 1h ago, lasts 2h)
			devCron := fmt.Sprintf("%d %d * * *", now.Add(-1*time.Hour).Minute(), now.Add(-1*time.Hour).Hour())
			// Staging: outside window (starts 2h in future)
			stagingCron := fmt.Sprintf("%d %d * * *", now.Add(2*time.Hour).Minute(), now.Add(2*time.Hour).Hour())

			scheduledCommitStatus = &promoterv1alpha1.ScheduledCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-multi",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ScheduledCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.ScheduledCommitStatusEnvironment{
						{
							Branch: testBranchDevelopment,
							Schedule: promoterv1alpha1.Schedule{
								Cron:     devCron,
								Window:   "2h",
								Timezone: "UTC",
							},
						},
						{
							Branch: testBranchStaging,
							Schedule: promoterv1alpha1.Schedule{
								Cron:     stagingCron,
								Window:   "1h",
								Timezone: "UTC",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scheduledCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up ScheduledCommitStatus")
			_ = k8sClient.Delete(ctx, scheduledCommitStatus)
		})

		It("should handle different schedules for each environment", func() {
			By("Waiting for ScheduledCommitStatus to process both environments")
			Eventually(func(g Gomega) {
				var scs promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-multi",
					Namespace: "default",
				}, &scs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(scs.Status.Environments).To(HaveLen(2))

				var devEnv, stagingEnv *promoterv1alpha1.ScheduledCommitStatusEnvironmentStatus
				for i := range scs.Status.Environments {
					if scs.Status.Environments[i].Branch == testBranchDevelopment {
						devEnv = &scs.Status.Environments[i]
					}
					if scs.Status.Environments[i].Branch == testBranchStaging {
						stagingEnv = &scs.Status.Environments[i]
					}
				}

				// Dev should be in window with success
				g.Expect(devEnv).NotTo(BeNil())
				g.Expect(devEnv.CurrentlyInWindow).To(BeTrue())
				g.Expect(devEnv.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

				// Staging should be outside window with pending
				g.Expect(stagingEnv).NotTo(BeNil())
				g.Expect(stagingEnv.CurrentlyInWindow).To(BeFalse())
				g.Expect(stagingEnv.Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Timezone Handling", func() {
		var scheduledCommitStatus *promoterv1alpha1.ScheduledCommitStatus

		AfterEach(func() {
			if scheduledCommitStatus != nil {
				_ = k8sClient.Delete(ctx, scheduledCommitStatus)
			}
		})

		It("should handle America/New_York timezone correctly", func() {
			By("Creating ScheduledCommitStatus with America/New_York timezone")
			// Get current time in New York
			nyLocation, err := time.LoadLocation("America/New_York")
			Expect(err).NotTo(HaveOccurred())
			nowNY := time.Now().In(nyLocation)

			// Create a window that includes current NY time
			cronExpr := fmt.Sprintf("%d %d * * *", nowNY.Add(-1*time.Hour).Minute(), nowNY.Add(-1*time.Hour).Hour())

			scheduledCommitStatus = &promoterv1alpha1.ScheduledCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-tz",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ScheduledCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.ScheduledCommitStatusEnvironment{
						{
							Branch: testBranchDevelopment,
							Schedule: promoterv1alpha1.Schedule{
								Cron:     cronExpr,
								Window:   "2h",
								Timezone: "America/New_York",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scheduledCommitStatus)).To(Succeed())

			By("Verifying the window is calculated correctly for the timezone")
			Eventually(func(g Gomega) {
				var scs promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-tz",
					Namespace: "default",
				}, &scs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(scs.Status.Environments).To(HaveLen(1))
				// Should be in window since we created a window containing current NY time
				g.Expect(scs.Status.Environments[0].CurrentlyInWindow).To(BeTrue())
				g.Expect(scs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Schedule Update Transition", func() {
		var scheduledCommitStatus *promoterv1alpha1.ScheduledCommitStatus

		BeforeEach(func() {
			By("Creating a ScheduledCommitStatus with window far in the future (initially pending)")
			now := time.Now().UTC()
			// Create a window that starts 5 hours in the future
			futureTime := now.Add(5 * time.Hour)
			cronExpr := fmt.Sprintf("%d %d * * *", futureTime.Minute(), futureTime.Hour())

			scheduledCommitStatus = &promoterv1alpha1.ScheduledCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-transition",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ScheduledCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.ScheduledCommitStatusEnvironment{
						{
							Branch: testBranchDevelopment,
							Schedule: promoterv1alpha1.Schedule{
								Cron:     cronExpr,
								Window:   "1h",
								Timezone: "UTC",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scheduledCommitStatus)).To(Succeed())

			By("Waiting for ScheduledCommitStatus to initially report pending status")
			Eventually(func(g Gomega) {
				var scs promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-transition",
					Namespace: "default",
				}, &scs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(scs.Status.Environments).To(HaveLen(1))
				g.Expect(scs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)))
				g.Expect(scs.Status.Environments[0].CurrentlyInWindow).To(BeFalse())
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up ScheduledCommitStatus")
			_ = k8sClient.Delete(ctx, scheduledCommitStatus)
		})

		It("should transition from pending to success when schedule is updated to active window", func() {
			By("Verifying initial pending state with CommitStatus")
			commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-transition-"+testBranchDevelopment+"-scheduled")
			Eventually(func(g Gomega) {
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				g.Expect(cs.Spec.Description).To(ContainSubstring("Deployment window is closed"))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Updating ScheduledCommitStatus schedule to create an active window")
			Eventually(func(g Gomega) {
				var scs promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-transition",
					Namespace: "default",
				}, &scs)
				g.Expect(err).NotTo(HaveOccurred())

				// Update to a window that includes current time (started 1 hour ago, lasts 2 hours)
				now := time.Now().UTC()
				activeWindowCron := fmt.Sprintf("%d %d * * *", now.Add(-1*time.Hour).Minute(), now.Add(-1*time.Hour).Hour())
				scs.Spec.Environments[0].Schedule.Cron = activeWindowCron
				scs.Spec.Environments[0].Schedule.Window = "2h"

				err = k8sClient.Update(ctx, &scs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Waiting for ScheduledCommitStatus to transition to success")
			Eventually(func(g Gomega) {
				var scs promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-transition",
					Namespace: "default",
				}, &scs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(scs.Status.Environments).To(HaveLen(1))
				g.Expect(scs.Status.Environments[0].Branch).To(Equal(testBranchDevelopment))
				g.Expect(scs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
					"Phase should transition to success when window becomes active")
				g.Expect(scs.Status.Environments[0].CurrentlyInWindow).To(BeTrue(),
					"Should now be in window")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying CommitStatus was updated to success")
			Eventually(func(g Gomega) {
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess),
					"CommitStatus should transition to success")
				g.Expect(cs.Spec.Description).To(ContainSubstring("Deployment window is open"))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying the transition is stable (doesn't flip back)")
			Consistently(func(g Gomega) {
				var scs promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-transition",
					Namespace: "default",
				}, &scs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(scs.Status.Environments).To(HaveLen(1))
				g.Expect(scs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
					"Phase must remain success")
				g.Expect(scs.Status.Environments[0].CurrentlyInWindow).To(BeTrue())

				// Verify CommitStatus remains success
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, 5*time.Second, 1*time.Second).Should(Succeed())
		})
	})

	Describe("Environment Cleanup", func() {
		var scheduledCommitStatus *promoterv1alpha1.ScheduledCommitStatus

		BeforeEach(func() {
			By("Creating ScheduledCommitStatus with two environments")
			now := time.Now().UTC()
			cronExpr := fmt.Sprintf("%d %d * * *", now.Add(-1*time.Hour).Minute(), now.Add(-1*time.Hour).Hour())

			scheduledCommitStatus = &promoterv1alpha1.ScheduledCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-cleanup",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ScheduledCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.ScheduledCommitStatusEnvironment{
						{
							Branch: testBranchDevelopment,
							Schedule: promoterv1alpha1.Schedule{
								Cron:     cronExpr,
								Window:   "2h",
								Timezone: "UTC",
							},
						},
						{
							Branch: testBranchStaging,
							Schedule: promoterv1alpha1.Schedule{
								Cron:     cronExpr,
								Window:   "2h",
								Timezone: "UTC",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scheduledCommitStatus)).To(Succeed())

			By("Waiting for both CommitStatuses to be created")
			Eventually(func(g Gomega) {
				var scs promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-cleanup",
					Namespace: "default",
				}, &scs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(scs.Status.Environments).To(HaveLen(2))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up ScheduledCommitStatus")
			_ = k8sClient.Delete(ctx, scheduledCommitStatus)
		})

		It("should delete orphaned CommitStatus when environment is removed from spec", func() {
			By("Verifying both CommitStatuses exist")
			devCommitStatusName := utils.KubeSafeUniqueName(ctx, name+"-cleanup-"+testBranchDevelopment+"-scheduled")
			stagingCommitStatusName := utils.KubeSafeUniqueName(ctx, name+"-cleanup-"+testBranchStaging+"-scheduled")

			Eventually(func(g Gomega) {
				var devCS promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      devCommitStatusName,
					Namespace: "default",
				}, &devCS)
				g.Expect(err).NotTo(HaveOccurred())

				var stagingCS promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      stagingCommitStatusName,
					Namespace: "default",
				}, &stagingCS)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Removing staging environment from ScheduledCommitStatus spec")
			Eventually(func(g Gomega) {
				var scs promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-cleanup",
					Namespace: "default",
				}, &scs)
				g.Expect(err).NotTo(HaveOccurred())

				// Remove staging environment, keep only dev
				scs.Spec.Environments = []promoterv1alpha1.ScheduledCommitStatusEnvironment{
					scs.Spec.Environments[0], // Keep dev
				}

				err = k8sClient.Update(ctx, &scs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying staging CommitStatus is deleted")
			Eventually(func(g Gomega) {
				var stagingCS promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      stagingCommitStatusName,
					Namespace: "default",
				}, &stagingCS)
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue(),
					"Staging CommitStatus should be deleted")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying dev CommitStatus still exists")
			Eventually(func(g Gomega) {
				var devCS promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      devCommitStatusName,
					Namespace: "default",
				}, &devCS)
				g.Expect(err).NotTo(HaveOccurred(),
					"Dev CommitStatus should still exist")
				g.Expect(devCS.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})

// Separate Describe block for the test that doesn't need infrastructure
var _ = Describe("ScheduledCommitStatus Controller - Missing PromotionStrategy", func() {
	Context("When PromotionStrategy is not found", func() {
		const resourceName = "scheduled-status-no-ps"

		ctx := context.Background()

		var scheduledCommitStatus *promoterv1alpha1.ScheduledCommitStatus

		BeforeEach(func() {
			By("Creating only a ScheduledCommitStatus resource without PromotionStrategy")
			scheduledCommitStatus = &promoterv1alpha1.ScheduledCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ScheduledCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: "non-existent",
					},
					Environments: []promoterv1alpha1.ScheduledCommitStatusEnvironment{
						{
							Branch: "environment/dev",
							Schedule: promoterv1alpha1.Schedule{
								Cron:     "0 9 * * *",
								Window:   "1h",
								Timezone: "UTC",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, scheduledCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, scheduledCommitStatus)
		})

		It("should handle missing PromotionStrategy gracefully", func() {
			By("Verifying the ScheduledCommitStatus exists but doesn't process environments")
			Consistently(func(g Gomega) {
				var scs promoterv1alpha1.ScheduledCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName,
					Namespace: "default",
				}, &scs)
				g.Expect(err).NotTo(HaveOccurred())
				// Status should be empty since PromotionStrategy doesn't exist
				g.Expect(scs.Status.Environments).To(BeEmpty())
			}, 2*time.Second, 500*time.Millisecond).Should(Succeed())
		})
	})
})
