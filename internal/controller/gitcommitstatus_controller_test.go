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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("GitCommitStatus Controller", Ordered, func() {
	/*
	 * Controller Behavior Summary:
	 * 1. Validates the ACTIVE hydrated commit (not proposed) using expressions
	 * 2. Tracks both ProposedHydratedSha and ActiveHydratedSha in status
	 * 3. Uses gcs.Spec.Key field to match against PromotionStrategy's proposedCommitStatuses
	 * 4. CommitData contains: SHA, Subject, Body, Author, Committer, Trailers
	 * 5. CommitStatus gets Description from gcs.Spec.Description (defaults to empty)
	 * 6. Creates CommitStatus resources for PROPOSED SHA but validates ACTIVE SHA
	 */

	var (
		ctx               context.Context
		name              string
		scmSecret         *v1.Secret
		scmProvider       *promoterv1alpha1.ScmProvider
		gitRepo           *promoterv1alpha1.GitRepository
		promotionStrategy *promoterv1alpha1.PromotionStrategy
		gitCommitStatus   *promoterv1alpha1.GitCommitStatus
	)

	BeforeAll(func() {
		ctx = context.Background()

		By("Setting up test git repository and resources")
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "git-commit-status-test", "default")

		// Configure ProposedCommitStatuses to check for git commit status
		promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
			{Key: "test-validation"},
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
			// Ensure active hydrated SHAs are populated
			for _, env := range promotionStrategy.Status.Environments {
				g.Expect(env.Active.Hydrated.Sha).ToNot(BeEmpty(), "Active hydrated SHA should be set for "+env.Branch)
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

	Describe("Basic Expression Evaluation", func() {
		BeforeEach(func() {
			By("Creating a GitCommitStatus with a simple passing expression")
			gitCommitStatus = &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-simple",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:         "test-validation",
					Description: "Test validation check",
					Expression:  `Commit.Author == Commit.Committer`, // Should pass - same author/committer
				},
			}
			Expect(k8sClient.Create(ctx, gitCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			if gitCommitStatus != nil {
				_ = k8sClient.Delete(ctx, gitCommitStatus)
			}
		})

		It("should evaluate the active commit and report success", func() {
			By("Waiting for GitCommitStatus to process all environments")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-simple",
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have status for all three environments
				g.Expect(gcs.Status.Environments).To(HaveLen(3))

				// Verify each environment has proper status
				for _, env := range gcs.Status.Environments {
					g.Expect(env.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)), "Environment "+env.Branch+" should succeed")
					g.Expect(env.ProposedHydratedSha).ToNot(BeEmpty(), "ProposedHydratedSha should be populated")
					g.Expect(env.ActiveHydratedSha).ToNot(BeEmpty(), "ActiveHydratedSha should be populated")
					g.Expect(env.ExpressionMessage).To(Equal("Expression evaluated to true"))
					g.Expect(env.ExpressionResult).ToNot(BeNil())
					g.Expect(*env.ExpressionResult).To(BeTrue())
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying CommitStatus was created with custom description")
			Eventually(func(g Gomega) {
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-simple-"+testEnvironmentDevelopment+"-test-validation")
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				g.Expect(cs.Spec.Description).To(Equal("Test validation check"))
				g.Expect(cs.Spec.Name).To(Equal("test-validation/" + testEnvironmentDevelopment))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Expression with Commit Subject and Body", func() {
		BeforeEach(func() {
			By("Creating a GitCommitStatus that checks commit subject")
			gitCommitStatus = &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-subject",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:        "test-validation",
					Expression: `Commit.Subject != ""`, // Check subject is not empty
				},
			}
			Expect(k8sClient.Create(ctx, gitCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			if gitCommitStatus != nil {
				_ = k8sClient.Delete(ctx, gitCommitStatus)
			}
		})

		It("should evaluate expression against commit subject field", func() {
			By("Waiting for evaluation to complete")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-subject",
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(gcs.Status.Environments).ToNot(BeEmpty())

				// The subject should not be empty - should pass for all environments
				for _, env := range gcs.Status.Environments {
					g.Expect(env.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
					g.Expect(env.ExpressionResult).ToNot(BeNil())
					g.Expect(*env.ExpressionResult).To(BeTrue())
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Failing Expression", func() {
		BeforeEach(func() {
			By("Creating a GitCommitStatus with an expression that always fails")
			gitCommitStatus = &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-fail",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:        "test-validation",
					Expression: `false`, // Always fails
				},
			}
			Expect(k8sClient.Create(ctx, gitCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			if gitCommitStatus != nil {
				_ = k8sClient.Delete(ctx, gitCommitStatus)
			}
		})

		It("should report failure status for all environments", func() {
			By("Waiting for evaluation to complete")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-fail",
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(gcs.Status.Environments).To(HaveLen(3))

				for _, env := range gcs.Status.Environments {
					g.Expect(env.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseFailure)))
					g.Expect(env.ExpressionMessage).To(Equal("Expression evaluated to false"))
					g.Expect(env.ExpressionResult).ToNot(BeNil())
					g.Expect(*env.ExpressionResult).To(BeFalse())
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying CommitStatus was created with failure phase")
			Eventually(func(g Gomega) {
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-fail-"+testEnvironmentDevelopment+"-test-validation")
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseFailure))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Invalid Expression", func() {
		BeforeEach(func() {
			By("Creating a GitCommitStatus with invalid expression syntax")
			gitCommitStatus = &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-invalid",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:        "test-validation",
					Expression: `Commit.Invalid..Field`, // Invalid syntax
				},
			}
			Expect(k8sClient.Create(ctx, gitCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			if gitCommitStatus != nil {
				_ = k8sClient.Delete(ctx, gitCommitStatus)
			}
		})

		It("should report failure with compilation error message", func() {
			By("Waiting for evaluation to complete")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-invalid",
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(gcs.Status.Environments).ToNot(BeEmpty())

				for _, env := range gcs.Status.Environments {
					g.Expect(env.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseFailure)))
					g.Expect(env.ExpressionMessage).To(ContainSubstring("Expression compilation failed"))
					g.Expect(env.ExpressionResult).To(BeNil())
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Missing PromotionStrategy", func() {
		It("should handle missing PromotionStrategy gracefully", func() {
			By("Creating a GitCommitStatus referencing non-existent PromotionStrategy")
			gcs := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-missing-ps",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: "non-existent",
					},
					Key:        "test-validation",
					Expression: `true`,
				},
			}
			Expect(k8sClient.Create(ctx, gcs)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, gcs)
			}()

			// The controller should handle this gracefully - it will error but not crash
			Consistently(func(g Gomega) {
				var retrieved promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-missing-ps",
					Namespace: "default",
				}, &retrieved)
				g.Expect(err).NotTo(HaveOccurred())
				// Status should remain empty since PromotionStrategy doesn't exist
				g.Expect(retrieved.Status.Environments).To(BeEmpty())
			}, "5s", "1s").Should(Succeed())
		})
	})

	Describe("CommitStatus Ownership and Cleanup", func() {
		BeforeEach(func() {
			By("Creating a GitCommitStatus")
			gitCommitStatus = &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-cleanup",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:        "test-validation",
					Expression: `true`,
				},
			}
			Expect(k8sClient.Create(ctx, gitCommitStatus)).To(Succeed())

			By("Waiting for CommitStatus to be created")
			Eventually(func(g Gomega) {
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-cleanup-"+testEnvironmentDevelopment+"-test-validation")
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.OwnerReferences).ToNot(BeEmpty())
				g.Expect(cs.OwnerReferences[0].Name).To(Equal(name + "-cleanup"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		// Test marked as pending due to garbage collection timing in test environment
		// The ownership relationship is correctly established (tested above),
		// but K8s GC in test env may not cleanup within reasonable timeout
		PIt("should cleanup CommitStatus resources when GitCommitStatus is deleted", func() {
			commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-cleanup-"+testEnvironmentDevelopment+"-test-validation")

			By("Deleting the GitCommitStatus")
			Expect(k8sClient.Delete(ctx, gitCommitStatus)).To(Succeed())

			By("Waiting for CommitStatus to be garbage collected")
			Eventually(func() bool {
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				return errors.IsNotFound(err)
			}, "2m", "1s").Should(BeTrue(), "CommitStatus should be deleted by garbage collector")
		})
	})

	Describe("SHA Tracking", func() {
		It("should track both proposed and active hydrated SHAs", func() {
			By("Creating a GitCommitStatus")
			gcs := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-sha-track",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:        "test-validation",
					Expression: `Commit.SHA != ""`,
				},
			}
			Expect(k8sClient.Create(ctx, gcs)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, gcs)
			}()

			By("Verifying both SHAs are tracked in status")
			Eventually(func(g Gomega) {
				var retrieved promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-sha-track",
					Namespace: "default",
				}, &retrieved)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(retrieved.Status.Environments).ToNot(BeEmpty())

				for _, env := range retrieved.Status.Environments {
					g.Expect(env.ProposedHydratedSha).ToNot(BeEmpty(), "ProposedHydratedSha should be set")
					g.Expect(env.ActiveHydratedSha).ToNot(BeEmpty(), "ActiveHydratedSha should be set")
					// The controller validates the ACTIVE SHA, so that's what Commit.SHA should be
					g.Expect(env.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Multiple Environments", func() {
		It("should evaluate independently for each environment", func() {
			By("Creating a GitCommitStatus that applies to all environments")
			gcs := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-multi-env",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:        "test-validation",
					Expression: `true`, // Always passes
				},
			}
			Expect(k8sClient.Create(ctx, gcs)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, gcs)
			}()

			By("Verifying all three environments are evaluated")
			Eventually(func(g Gomega) {
				var retrieved promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-multi-env",
					Namespace: "default",
				}, &retrieved)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(retrieved.Status.Environments).To(HaveLen(3))

				// All should succeed
				for _, env := range retrieved.Status.Environments {
					g.Expect(env.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying CommitStatus created for each environment")
			for _, envBranch := range []string{testEnvironmentDevelopment, testEnvironmentStaging, testEnvironmentProduction} {
				Eventually(func(g Gomega) {
					commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-multi-env-"+envBranch+"-test-validation")
					var cs promoterv1alpha1.CommitStatus
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      commitStatusName,
						Namespace: "default",
					}, &cs)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				}, constants.EventuallyTimeout).Should(Succeed())
			}
		})
	})

	Describe("Default Description Behavior", func() {
		It("should use empty description when not specified", func() {
			By("Creating a GitCommitStatus without description")
			gcs := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-no-desc",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:        "test-validation",
					Expression: `true`,
					// Description not set
				},
			}
			Expect(k8sClient.Create(ctx, gcs)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, gcs)
			}()

			By("Verifying CommitStatus has empty description")
			Eventually(func(g Gomega) {
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-no-desc-"+testEnvironmentDevelopment+"-test-validation")
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Description).To(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("Active vs Proposed SHA Validation", func() {
		It("should validate the active SHA not the proposed SHA", func() {
			By("Updating git repo to create a difference between active and proposed")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() {
				_ = os.RemoveAll(gitPath)
			}()

			// Make a change to create new proposed SHA
			makeChangeAndHydrateRepo(gitPath, name, name, "new commit for SHA diff", "different content")

			By("Waiting for PromotionStrategy to update with new proposed SHA")
			var developmentProposedSHA string
			var developmentActiveSHA string
			Eventually(func(g Gomega) {
				var ps promoterv1alpha1.PromotionStrategy
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &ps)
				g.Expect(err).NotTo(HaveOccurred())

				for _, env := range ps.Status.Environments {
					if env.Branch == testEnvironmentDevelopment {
						g.Expect(env.Proposed.Hydrated.Sha).ToNot(BeEmpty())
						g.Expect(env.Active.Hydrated.Sha).ToNot(BeEmpty())
						developmentProposedSHA = env.Proposed.Hydrated.Sha
						developmentActiveSHA = env.Active.Hydrated.Sha
					}
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating GitCommitStatus to validate commits")
			gcs := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-active-validate",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:        "test-validation",
					Expression: `Commit.SHA != ""`,
				},
			}
			Expect(k8sClient.Create(ctx, gcs)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, gcs)
			}()

			By("Verifying status shows both SHAs but validates active")
			Eventually(func(g Gomega) {
				var retrieved promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-active-validate",
					Namespace: "default",
				}, &retrieved)
				g.Expect(err).NotTo(HaveOccurred())

				var devEnv *promoterv1alpha1.GitCommitStatusEnvironmentStatus
				for i, env := range retrieved.Status.Environments {
					if env.Branch == testEnvironmentDevelopment {
						devEnv = &retrieved.Status.Environments[i]
						break
					}
				}
				g.Expect(devEnv).ToNot(BeNil())

				// Both should be tracked
				g.Expect(devEnv.ProposedHydratedSha).To(Equal(developmentProposedSHA))
				g.Expect(devEnv.ActiveHydratedSha).To(Equal(developmentActiveSHA))

				// Expression should succeed (validates active SHA exists)
				g.Expect(devEnv.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Describe("ValidateCommit Field", func() {
		It("should default to active mode when validateCommit is omitted", func() {
			By("Creating a GitCommitStatus without validateCommit field")
			gcs := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-default-mode",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key: "test-validation",
					// validateCommit not set - should default to "active"
					Expression: `Commit.SHA != ""`,
				},
			}
			Expect(k8sClient.Create(ctx, gcs)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, gcs)
			}()

			By("Verifying it validates the active SHA")
			Eventually(func(g Gomega) {
				var retrieved promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-default-mode",
					Namespace: "default",
				}, &retrieved)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(retrieved.Status.Environments).ToNot(BeEmpty())

				for _, env := range retrieved.Status.Environments {
					// ValidatedSha should match ActiveHydratedSha (not proposed)
					g.Expect(env.ValidatedSha).To(Equal(env.ActiveHydratedSha))
					g.Expect(env.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should validate active commit when validateCommit is explicitly set to active", func() {
			By("Creating a GitCommitStatus with validateCommit: active")
			gcs := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-explicit-active",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:            "test-validation",
					ValidateCommit: "active",
					Expression:     `Commit.SHA != ""`,
				},
			}
			Expect(k8sClient.Create(ctx, gcs)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, gcs)
			}()

			By("Verifying it validates the active SHA")
			Eventually(func(g Gomega) {
				var retrieved promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-explicit-active",
					Namespace: "default",
				}, &retrieved)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(retrieved.Status.Environments).ToNot(BeEmpty())

				for _, env := range retrieved.Status.Environments {
					g.Expect(env.ValidatedSha).To(Equal(env.ActiveHydratedSha))
					g.Expect(env.ValidatedSha).ToNot(Equal(env.ProposedHydratedSha))
					g.Expect(env.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should validate proposed commit when validateCommit is set to proposed", func() {
			By("Creating a GitCommitStatus with validateCommit: proposed")
			gcs := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-explicit-proposed",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:            "test-validation",
					ValidateCommit: "proposed",
					Expression:     `Commit.SHA != ""`,
				},
			}
			Expect(k8sClient.Create(ctx, gcs)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, gcs)
			}()

			By("Verifying it validates the proposed SHA")
			Eventually(func(g Gomega) {
				var retrieved promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-explicit-proposed",
					Namespace: "default",
				}, &retrieved)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(retrieved.Status.Environments).ToNot(BeEmpty())

				for _, env := range retrieved.Status.Environments {
					g.Expect(env.ValidatedSha).To(Equal(env.ProposedHydratedSha))
					g.Expect(env.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should populate validatedSha field correctly for both modes", func() {
			By("Creating GitCommitStatus with active mode")
			gcsActive := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-sha-tracking-active",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:            "test-validation",
					ValidateCommit: "active",
					Expression:     `true`,
				},
			}
			Expect(k8sClient.Create(ctx, gcsActive)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, gcsActive)
			}()

			By("Creating GitCommitStatus with proposed mode")
			gcsProposed := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-sha-tracking-proposed",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:            "test-validation-2",
					ValidateCommit: "proposed",
					Expression:     `true`,
				},
			}
			Expect(k8sClient.Create(ctx, gcsProposed)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, gcsProposed)
			}()

			By("Verifying active mode sets validatedSha to activeHydratedSha")
			Eventually(func(g Gomega) {
				var retrieved promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-sha-tracking-active",
					Namespace: "default",
				}, &retrieved)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(retrieved.Status.Environments).ToNot(BeEmpty())

				for _, env := range retrieved.Status.Environments {
					g.Expect(env.ValidatedSha).To(Equal(env.ActiveHydratedSha))
					g.Expect(env.ValidatedSha).ToNot(BeEmpty())
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying proposed mode sets validatedSha to proposedHydratedSha")
			Eventually(func(g Gomega) {
				var retrieved promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-sha-tracking-proposed",
					Namespace: "default",
				}, &retrieved)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(retrieved.Status.Environments).ToNot(BeEmpty())

				for _, env := range retrieved.Status.Environments {
					g.Expect(env.ValidatedSha).To(Equal(env.ProposedHydratedSha))
					g.Expect(env.ValidatedSha).ToNot(BeEmpty())
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})
