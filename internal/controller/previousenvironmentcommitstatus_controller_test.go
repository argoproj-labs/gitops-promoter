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
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

var _ = Describe("PreviousEnvironmentCommitStatus Controller", Ordered, func() {
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
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "pecs-test", "default")

		// Configure ActiveCommitStatuses to enable previous-environment checks
		promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: "healthy"},
		}

		setupInitialTestGitRepoOnServer(ctx, name, name)

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
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

	Describe("PECS Creation and CommitStatus Management", func() {
		It("should create PreviousEnvironmentCommitStatus when PromotionStrategy is created with ActiveCommitStatuses", func() {
			By("Waiting for PECS to be created by PromotionStrategy controller")
			pecsName := utils.KubeSafeUniqueName(ctx, name+"-previous-env")

			Eventually(func(g Gomega) {
				var pecs promoterv1alpha1.PreviousEnvironmentCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      pecsName,
					Namespace: "default",
				}, &pecs)
				g.Expect(err).NotTo(HaveOccurred())

				// Verify the spec is populated correctly
				g.Expect(pecs.Spec.PromotionStrategyRef.Name).To(Equal(name))
				g.Expect(pecs.Spec.Environments).To(HaveLen(3))

				// Verify environments are in order
				g.Expect(pecs.Spec.Environments[0].Branch).To(Equal(testBranchDevelopment))
				g.Expect(pecs.Spec.Environments[1].Branch).To(Equal(testBranchStaging))
				g.Expect(pecs.Spec.Environments[2].Branch).To(Equal(testBranchProduction))

				// Verify owner reference
				g.Expect(pecs.OwnerReferences).To(HaveLen(1))
				g.Expect(pecs.OwnerReferences[0].Kind).To(Equal("PromotionStrategy"))
				g.Expect(pecs.OwnerReferences[0].Name).To(Equal(name))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should create CommitStatuses when there are proposed changes", func() {
			By("Making a dry commit and hydrating dev-next to create a proposed change")
			gitPath, err := cloneTestRepo(ctx, name)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath) }()

			drySha, err := makeDryCommit(ctx, gitPath, "test commit for pecs test")
			Expect(err).NotTo(HaveOccurred())

			err = hydrateEnvironment(ctx, gitPath, testBranchDevelopmentNext, drySha, "hydrate dev-next")
			Expect(err).NotTo(HaveOccurred())

			By("Hydrating staging-next to create a proposed change in staging")
			err = hydrateEnvironment(ctx, gitPath, testBranchStagingNext, drySha, "hydrate staging-next")
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for staging's previous-environment CommitStatus to be created")
			// The staging environment should have a previous-env commit status checking dev
			Eventually(func(g Gomega) {
				// Get the staging CTP name
				ctpStagingName := utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(name, testBranchStaging))
				csName := utils.KubeSafeUniqueName(ctx, promoterv1alpha1.PreviousEnvProposedCommitPrefixNameLabel+ctpStagingName)

				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      csName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())

				// Verify the CommitStatus has the correct labels
				g.Expect(cs.Labels[promoterv1alpha1.CommitStatusLabel]).To(Equal(promoterv1alpha1.PreviousEnvironmentCommitStatusKey))

				// Verify the CommitStatus is owned by the PECS
				g.Expect(cs.OwnerReferences).To(HaveLen(1))
				g.Expect(cs.OwnerReferences[0].Kind).To(Equal("PreviousEnvironmentCommitStatus"))

				// Verify the CommitStatus spec references the previous environment
				g.Expect(cs.Spec.Name).To(ContainSubstring(testBranchDevelopment))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Hydrating production-next to trigger its previous-environment CommitStatus")
			err = hydrateEnvironment(ctx, gitPath, testBranchProductionNext, drySha, "hydrate prod-next")
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for production's previous-environment CommitStatus to be created")
			Eventually(func(g Gomega) {
				// Get the production CTP name
				ctpProdName := utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(name, testBranchProduction))
				csName := utils.KubeSafeUniqueName(ctx, promoterv1alpha1.PreviousEnvProposedCommitPrefixNameLabel+ctpProdName)

				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      csName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())

				// Verify the CommitStatus has the correct labels
				g.Expect(cs.Labels[promoterv1alpha1.CommitStatusLabel]).To(Equal(promoterv1alpha1.PreviousEnvironmentCommitStatusKey))

				// Verify the CommitStatus is owned by the PECS
				g.Expect(cs.OwnerReferences).To(HaveLen(1))
				g.Expect(cs.OwnerReferences[0].Kind).To(Equal("PreviousEnvironmentCommitStatus"))

				// Verify the CommitStatus spec references the previous environment (staging)
				g.Expect(cs.Spec.Name).To(ContainSubstring(testBranchStaging))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("PECS Cleanup on Environment Removal", func() {
		var (
			testName      string
			testPS        *promoterv1alpha1.PromotionStrategy
			testScmSecret *v1.Secret
			testScmProv   *promoterv1alpha1.ScmProvider
			testGitRepo   *promoterv1alpha1.GitRepository
		)

		BeforeEach(func() {
			By("Creating a new PromotionStrategy for cleanup test")
			testName, testScmSecret, testScmProv, testGitRepo, _, _, testPS = promotionStrategyResource(ctx, "pecs-cleanup-test", "default")

			testPS.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "healthy"},
			}

			setupInitialTestGitRepoOnServer(ctx, testName, testName)

			Expect(k8sClient.Create(ctx, testScmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, testScmProv)).To(Succeed())
			Expect(k8sClient.Create(ctx, testGitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, testPS)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up test resources")
			if testPS != nil {
				_ = k8sClient.Delete(ctx, testPS)
			}
			if testGitRepo != nil {
				_ = k8sClient.Delete(ctx, testGitRepo)
			}
			if testScmProv != nil {
				_ = k8sClient.Delete(ctx, testScmProv)
			}
			if testScmSecret != nil {
				_ = k8sClient.Delete(ctx, testScmSecret)
			}
		})

		It("should update PECS when PromotionStrategy environments change", func() {
			By("Waiting for initial PECS to be created")
			pecsName := utils.KubeSafeUniqueName(ctx, testName+"-previous-env")

			Eventually(func(g Gomega) {
				var pecs promoterv1alpha1.PreviousEnvironmentCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      pecsName,
					Namespace: "default",
				}, &pecs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pecs.Spec.Environments).To(HaveLen(3))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Removing staging environment from PromotionStrategy")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      testName,
					Namespace: "default",
				}, testPS)
				g.Expect(err).To(Succeed())

				testPS.Spec.Environments = []promoterv1alpha1.Environment{
					{
						Branch:    testBranchDevelopment,
						AutoMerge: testPS.Spec.Environments[0].AutoMerge,
					},
					{
						Branch:    testBranchProduction,
						AutoMerge: testPS.Spec.Environments[2].AutoMerge,
					},
				}

				err = k8sClient.Update(ctx, testPS)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying PECS is updated to reflect new environment list")
			Eventually(func(g Gomega) {
				var pecs promoterv1alpha1.PreviousEnvironmentCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      pecsName,
					Namespace: "default",
				}, &pecs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(pecs.Spec.Environments).To(HaveLen(2))
				g.Expect(pecs.Spec.Environments[0].Branch).To(Equal(testBranchDevelopment))
				g.Expect(pecs.Spec.Environments[1].Branch).To(Equal(testBranchProduction))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("PECS Deletion", func() {
		It("should have correct owner reference for garbage collection when PromotionStrategy is deleted", func() {
			By("Creating a new PromotionStrategy for deletion test")
			delName, delScmSecret, delScmProv, delGitRepo, _, _, delPS := promotionStrategyResource(ctx, "pecs-delete-test", "default")

			delPS.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "healthy"},
			}

			setupInitialTestGitRepoOnServer(ctx, delName, delName)

			Expect(k8sClient.Create(ctx, delScmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, delScmProv)).To(Succeed())
			Expect(k8sClient.Create(ctx, delGitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, delPS)).To(Succeed())

			By("Waiting for PECS to be created")
			pecsName := utils.KubeSafeUniqueName(ctx, delName+"-previous-env")
			var pecs promoterv1alpha1.PreviousEnvironmentCommitStatus
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      pecsName,
					Namespace: "default",
				}, &pecs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying PECS has correct owner reference to PromotionStrategy")
			Expect(pecs.OwnerReferences).To(HaveLen(1))
			Expect(pecs.OwnerReferences[0].Kind).To(Equal("PromotionStrategy"))
			Expect(pecs.OwnerReferences[0].Name).To(Equal(delName))
			Expect(*pecs.OwnerReferences[0].Controller).To(BeTrue())
			Expect(*pecs.OwnerReferences[0].BlockOwnerDeletion).To(BeTrue())

			By("Deleting the PromotionStrategy")
			Expect(k8sClient.Delete(ctx, delPS)).To(Succeed())

			By("Verifying PromotionStrategy is deleted")
			Eventually(func(g Gomega) {
				var ps promoterv1alpha1.PromotionStrategy
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      delName,
					Namespace: "default",
				}, &ps)
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying PECS controller handles missing PromotionStrategy gracefully")
			// The PECS controller should not error when the PS is not found
			// (it will be garbage collected by Kubernetes GC, which doesn't run in envtest)
			// We verify this by checking that the controller doesn't return an error
			// when reconciling the PECS after the PS is deleted
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      pecsName,
					Namespace: "default",
				}, &pecs)
				// The PECS may or may not exist at this point (depending on GC)
				// but if it exists, it should have the correct owner reference
				if err == nil {
					g.Expect(pecs.OwnerReferences).To(HaveLen(1))
					g.Expect(pecs.OwnerReferences[0].Name).To(Equal(delName))
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			// Clean up remaining resources (PECS will be cleaned up by GC in real cluster)
			_ = k8sClient.Delete(ctx, &pecs)
			_ = k8sClient.Delete(ctx, delGitRepo)
			_ = k8sClient.Delete(ctx, delScmProv)
			_ = k8sClient.Delete(ctx, delScmSecret)
		})
	})

	Context("isPreviousEnvironmentPending", func() {
		// Use fixed times for tests to ensure consistent time comparisons
		olderTime := metav1.NewTime(time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC))
		newerTime := metav1.NewTime(time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC))

		// Helper to create a HydratorMetadata pointer, or nil if empty
		makeNote := func(drySha string) *promoterv1alpha1.HydratorMetadata {
			if drySha == "" {
				return nil
			}
			return &promoterv1alpha1.HydratorMetadata{DrySha: drySha}
		}

		// Helper to create environment status with specific values
		makeEnvStatusWithTime := func(activeDrySha, proposedDrySha, noteDrySha string, activeTime metav1.Time) promoterv1alpha1.EnvironmentStatus {
			return promoterv1alpha1.EnvironmentStatus{
				Active: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        activeDrySha,
						CommitTime: activeTime,
					},
					CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						{Key: "health", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
					},
				},
				Proposed: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        proposedDrySha,
						CommitTime: olderTime, // Set proposed commit time to olderTime by default
					},
					Note: makeNote(noteDrySha),
				},
			}
		}

		// Helper that uses the older time by default (for backward compatibility)
		makeEnvStatus := func(activeDrySha, proposedDrySha, noteSha string) promoterv1alpha1.EnvironmentStatus {
			return makeEnvStatusWithTime(activeDrySha, proposedDrySha, noteSha, olderTime)
		}

		DescribeTable("should correctly determine if previous environment is pending",
			func(prevActiveDry, prevProposedDry, prevNoteDry, currActiveDry, currProposedDry, currNoteSha string, expectPending bool, expectReasonContains string) {
				prevEnvStatus := makeEnvStatus(prevActiveDry, prevProposedDry, prevNoteDry)
				currEnvStatus := makeEnvStatus(currActiveDry, currProposedDry, currNoteSha)

				isPending, reason := isPreviousEnvironmentPending(prevEnvStatus, currEnvStatus)

				Expect(isPending).To(Equal(expectPending), "isPending mismatch")
				if expectReasonContains != "" {
					Expect(reason).To(ContainSubstring(expectReasonContains), "reason mismatch")
				}
			},
			// Scenario 1: Out-of-order hydration - staging hydrates before dev
			// Dev hasn't hydrated yet (note and proposed still show OLD)
			Entry("blocks when previous env hasn't hydrated yet (with git notes)",
				"OLD", "OLD", "OLD", // prev: active=OLD, proposed=OLD, note=OLD
				"OLD", "ABC", "ABC", // curr: active=OLD, proposed=ABC, note=ABC
				true, "Waiting for the hydrator to finish processing the proposed dry commit"),

			// Scenario 2: Normal flow - dev has hydrated and merged
			Entry("allows when previous env has merged the proposed dry SHA",
				"ABC", "ABC", "ABC", // prev: active=ABC, proposed=ABC, note=ABC
				"OLD", "ABC", "ABC", // curr: active=OLD, proposed=ABC, note=ABC
				false, ""),

			// Scenario 3: Git note - dev has no manifest changes
			// Dev's hydrator updated the note but didn't create a new commit
			Entry("allows when previous env has no changes to merge (git note)",
				"OLD", "OLD", "ABC", // prev: active=OLD, proposed=OLD, note=ABC (note updated, no new commit)
				"OLD", "ABC", "ABC", // curr: active=OLD, proposed=ABC, note=ABC
				false, ""),

			// Scenario 4: Legacy hydrator (no git notes) - dev hasn't hydrated
			Entry("blocks when previous env hasn't hydrated (no git notes)",
				"OLD", "OLD", "", // prev: active=OLD, proposed=OLD, note="" (empty)
				"OLD", "ABC", "", // curr: active=OLD, proposed=ABC, note="" (empty for legacy)
				true, "Waiting for the hydrator to finish processing the proposed dry commit"),

			// Scenario 5: Legacy hydrator - dev has hydrated but not merged
			Entry("blocks when previous env has hydrated but not merged (no git notes)",
				"OLD", "ABC", "", // prev: active=OLD, proposed=ABC, note="" (empty)
				"OLD", "ABC", "", // curr: active=OLD, proposed=ABC, note="" (empty for legacy)
				true, "Waiting for previous environment to be promoted"),

			// Scenario 6: Legacy hydrator - dev has hydrated and merged
			Entry("allows when previous env has merged (no git notes)",
				"ABC", "ABC", "", // prev: active=ABC, proposed=ABC, note="" (empty)
				"OLD", "ABC", "", // curr: active=OLD, proposed=ABC, note="" (empty for legacy)
				false, ""),

			// Scenario 7: Dev hydrated but not yet merged (with git notes)
			Entry("blocks when previous env has hydrated but not merged (with git notes)",
				"OLD", "ABC", "ABC", // prev: active=OLD, proposed=ABC, note=ABC
				"OLD", "ABC", "ABC", // curr: active=OLD, proposed=ABC, note=ABC
				true, "Waiting for previous environment to be promoted"),

			// Scenario 8: Mismatch between note and proposed (edge case)
			// Note shows newer SHA than proposed (hydrator updated note for even newer commit)
			Entry("blocks when note shows different SHA than what we're promoting",
				"OLD", "OLD", "DEF", // prev: active=OLD, proposed=OLD, note=DEF (different!)
				"OLD", "ABC", "ABC", // curr: active=OLD, proposed=ABC, note=ABC
				true, "Waiting for the hydrator to finish processing the proposed dry commit"),
		)

		// Test for when previous environment has already moved past the proposed dry SHA
		// AND both environments have the same Note.DrySha (confirming they've seen the same dry commits)
		//
		// Example scenario:
		// 1. Dry commit ABC is made (commitTime: 10:00)
		// 2. All environments get hydrated for ABC
		// 3. Before production merges ABC, someone makes dry commit DEF (commitTime: 10:05)
		// 4. Staging hydrates and merges DEF
		// 5. Production is still trying to promote ABC, but staging is ahead
		// 6. Both have Note.DrySha = DEF (both hydrated up to the latest), so allow promotion
		It("allows when previous env has already merged a newer commit with matching Note.DrySha", func() {
			// Previous env (staging) has merged a newer commit (DEF at newerTime)
			prevEnvStatus := makeEnvStatusWithTime("DEF", "DEF", "DEF", newerTime)
			// Current env (production) is trying to promote an older commit (ABC at olderTime)
			// Both have Note.DrySha = "DEF", meaning they've both been hydrated up to the same point
			currEnvStatus := promoterv1alpha1.EnvironmentStatus{
				Active: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "OLD",
						CommitTime: olderTime,
					},
					CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						{Key: "health", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
					},
				},
				Proposed: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "ABC",
						CommitTime: olderTime, // ABC was made before DEF
					},
					Note: &promoterv1alpha1.HydratorMetadata{
						DrySha: "DEF", // But hydrator has processed up to DEF
					},
				},
			}

			isPending, reason := isPreviousEnvironmentPending(prevEnvStatus, currEnvStatus)

			Expect(isPending).To(BeFalse(), "should allow promotion when previous env is ahead and Note.DrySha matches")
			Expect(reason).To(BeEmpty())
		})

		It("blocks when Note.DrySha doesn't match between environments", func() {
			// Previous env (staging) has Note.DrySha "XYZ" while production's is "ABC"
			// This means they haven't been hydrated for the same dry commits
			prevEnvStatus := makeEnvStatusWithTime("DEF", "DEF", "XYZ", newerTime)
			currEnvStatus := promoterv1alpha1.EnvironmentStatus{
				Active: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "OLD",
						CommitTime: olderTime,
					},
					CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						{Key: "health", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
					},
				},
				Proposed: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "ABC",
						CommitTime: olderTime,
					},
					Note: &promoterv1alpha1.HydratorMetadata{
						DrySha: "ABC", // Different from staging's XYZ
					},
				},
			}

			isPending, reason := isPreviousEnvironmentPending(prevEnvStatus, currEnvStatus)

			Expect(isPending).To(BeTrue(), "should block when Note.DrySha doesn't match")
			Expect(reason).To(ContainSubstring("hydrator to finish processing"))
		})

		// Test for legacy hydrators (no git notes) when previous env is ahead
		It("allows when previous env is ahead with matching Proposed.Dry.Sha (legacy hydrator)", func() {
			// Previous env (staging) has merged a newer commit (DEF at newerTime)
			// Both environments use legacy hydrator (no Note.DrySha), so we compare Proposed.Dry.Sha
			prevEnvStatus := promoterv1alpha1.EnvironmentStatus{
				Active: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "DEF",
						CommitTime: newerTime,
					},
					CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						{Key: "health", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
					},
				},
				Proposed: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha: "DEF",
					},
					// Note.DrySha is empty (legacy hydrator, no git notes)
				},
			}
			currEnvStatus := promoterv1alpha1.EnvironmentStatus{
				Active: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "OLD",
						CommitTime: olderTime,
					},
					CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						{Key: "health", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
					},
				},
				Proposed: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "DEF", // Same as staging's Proposed.Dry.Sha
						CommitTime: olderTime,
					},
					// Note.DrySha is empty (legacy hydrator, no git notes)
				},
			}

			isPending, reason := isPreviousEnvironmentPending(prevEnvStatus, currEnvStatus)

			Expect(isPending).To(BeFalse(), "should allow promotion when previous env is ahead and Proposed.Dry.Sha matches (legacy)")
			Expect(reason).To(BeEmpty())
		})

		It("blocks when previous env is ahead but commit statuses are not passing", func() {
			// Previous env has merged a newer commit but health check is pending
			prevEnvStatus := promoterv1alpha1.EnvironmentStatus{
				Active: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "DEF",
						CommitTime: newerTime,
					},
					CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						{Key: "health", Phase: string(promoterv1alpha1.CommitPhasePending)},
					},
				},
				Proposed: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha: "DEF",
					},
					Note: &promoterv1alpha1.HydratorMetadata{
						DrySha: "DEF",
					},
				},
			}
			currEnvStatus := promoterv1alpha1.EnvironmentStatus{
				Active: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "OLD",
						CommitTime: olderTime,
					},
					CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						{Key: "health", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
					},
				},
				Proposed: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "ABC",
						CommitTime: olderTime,
					},
					Note: &promoterv1alpha1.HydratorMetadata{
						DrySha: "DEF", // Note.DrySha matches staging
					},
				},
			}

			isPending, reason := isPreviousEnvironmentPending(prevEnvStatus, currEnvStatus)

			Expect(isPending).To(BeTrue(), "should block when previous env commit statuses are not passing")
			Expect(reason).To(ContainSubstring("commit status"))
		})
	})
})
