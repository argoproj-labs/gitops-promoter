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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("GitCommitStatus Controller", Ordered, func() {
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
					Expression:  `Commit.Author != ""`, // Should pass - author is always present
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
					g.Expect(env.ExpressionResult).ToNot(BeNil())
					g.Expect(*env.ExpressionResult).To(BeTrue())
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying CommitStatus was created with custom description")
			Eventually(func(g Gomega) {
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-simple-"+testBranchDevelopment+"-test-validation")
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				g.Expect(cs.Spec.Description).To(Equal("Test validation check"))
				g.Expect(cs.Spec.Name).To(Equal("test-validation/" + testBranchDevelopment))
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
					g.Expect(env.ExpressionResult).ToNot(BeNil())
					g.Expect(*env.ExpressionResult).To(BeFalse())
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying CommitStatus was created with failure phase")
			Eventually(func(g Gomega) {
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-fail-"+testBranchDevelopment+"-test-validation")
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
			By("Waiting for the Ready condition to show the compilation error")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-invalid",
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())

				// The controller should error out due to expression compilation failure
				// This will set Ready=False via HandleReconciliationResult
				readyCondition := meta.FindStatusCondition(gcs.Status.Conditions, "Ready")
				g.Expect(readyCondition).NotTo(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCondition.Message).To(ContainSubstring("failed to evaluate expression"))
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
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-cleanup-"+testBranchDevelopment+"-test-validation")
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
			commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-cleanup-"+testBranchDevelopment+"-test-validation")

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
			for _, envBranch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
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
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-no-desc-"+testBranchDevelopment+"-test-validation")
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
		It("should give different validation results when active and proposed commits have different properties", func() {
			By("Updating git repo to create a commit with a different subject")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() {
				_ = os.RemoveAll(gitPath)
			}()

			// Make a change with hydrated commit message that starts with "feat:"
			// Note: dryCommitMessage is for dry branch, hydratedCommitMessage is for hydrated branches (what we validate)
			makeChangeAndHydrateRepo(gitPath, name, name, "new commit", "feat: new feature content")

			By("Waiting for PromotionStrategy to update")
			var developmentProposedSHA string
			var developmentActiveSHA string
			Eventually(func(g Gomega) {
				var ps promoterv1alpha1.PromotionStrategy
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &ps)
				g.Expect(err).NotTo(HaveOccurred())

				// Check development environment
				for _, env := range ps.Status.Environments {
					if env.Branch == testBranchDevelopment {
						g.Expect(env.Proposed.Hydrated.Sha).ToNot(BeEmpty())
						g.Expect(env.Active.Hydrated.Sha).ToNot(BeEmpty())
						developmentProposedSHA = env.Proposed.Hydrated.Sha
						developmentActiveSHA = env.Active.Hydrated.Sha
					}
				}
				g.Expect(developmentProposedSHA).ToNot(BeEmpty())
				g.Expect(developmentActiveSHA).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating GitCommitStatus that checks for 'feat:' prefix with active mode")
			gcsActive := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-active-feat-check",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:    "test-validation",
					Target: "active",
					// Check if commit subject startsWith "feat:"
					Expression: `Commit.Subject startsWith "feat:"`,
				},
			}
			Expect(k8sClient.Create(ctx, gcsActive)).To(Succeed())

			By("Verifying active mode result depends on active SHA and expression fails")
			var activePhase string
			Eventually(func(g Gomega) {
				var retrieved promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-active-feat-check",
					Namespace: "default",
				}, &retrieved)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(retrieved.Status.Environments).ToNot(BeEmpty())

				var devEnv *promoterv1alpha1.GitCommitStatusEnvironmentStatus
				for i, env := range retrieved.Status.Environments {
					if env.Branch == testBranchDevelopment {
						devEnv = &retrieved.Status.Environments[i]
						break
					}
				}
				g.Expect(devEnv).ToNot(BeNil())

				// Verify it's checking the active SHA
				g.Expect(devEnv.TargetedSha).To(Equal(developmentActiveSHA))
				g.Expect(devEnv.Phase).ToNot(BeEmpty())
				activePhase = devEnv.Phase

				// Active commit doesn't have "feat:" prefix, so expression should fail
				g.Expect(devEnv.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseFailure)))
				g.Expect(devEnv.ExpressionResult).ToNot(BeNil())
				g.Expect(*devEnv.ExpressionResult).To(BeFalse())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Deleting active mode GitCommitStatus")
			Expect(k8sClient.Delete(ctx, gcsActive)).To(Succeed())
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-active-feat-check",
					Namespace: "default",
				}, &promoterv1alpha1.GitCommitStatus{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating GitCommitStatus that checks for 'feat:' prefix with proposed mode")
			gcsProposed := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-proposed-feat-check",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:    "test-validation",
					Target: "proposed",
					// Check if commit subject starts with "feat:"
					Expression: `Commit.Subject startsWith "feat:"`,
				},
			}
			Expect(k8sClient.Create(ctx, gcsProposed)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, gcsProposed)
			}()

			By("Verifying proposed mode gives success (proposed SHA has 'feat:' prefix)")
			Eventually(func(g Gomega) {
				var retrieved promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-proposed-feat-check",
					Namespace: "default",
				}, &retrieved)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(retrieved.Status.Environments).ToNot(BeEmpty())

				var devEnv *promoterv1alpha1.GitCommitStatusEnvironmentStatus
				for i, env := range retrieved.Status.Environments {
					if env.Branch == testBranchDevelopment {
						devEnv = &retrieved.Status.Environments[i]
						break
					}
				}
				g.Expect(devEnv).ToNot(BeNil())

				// Verify it's checking the proposed SHA
				g.Expect(devEnv.TargetedSha).To(Equal(developmentProposedSHA))
				// Proposed commit subject starts with "feat:", so should succeed
				g.Expect(devEnv.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				g.Expect(devEnv.ExpressionResult).ToNot(BeNil())
				g.Expect(*devEnv.ExpressionResult).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying the two modes can give different results")
			// If active and proposed SHAs are different and have different properties,
			// the validation results should differ. Here, proposed will succeed with "feat:" prefix
			// while active may or may not depending on whether it's been promoted
			Expect(activePhase).ToNot(BeEmpty(), "Active validation should have completed")
		})
	})

	Describe("Revert Detection Flow", func() {
		It("should detect reverts on staging and fail the commit status", func() {
			By("Creating a GitCommitStatus that detects 'Revert' prefix on active commits")
			// Use the existing "test-validation" key from BeforeAll setup
			gcsRevertCheck := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name + "-revert-check",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key:    "test-validation",
					Target: "active",
					// Check if commit subject does NOT start with "Revert" - if it does, fail
					Expression: `!(Commit.Subject startsWith "Revert")`,
				},
			}
			Expect(k8sClient.Create(ctx, gcsRevertCheck)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, gcsRevertCheck)
			}()

			By("Waiting for initial validation to pass on all environments (no reverts yet)")
			Eventually(func(g Gomega) {
				var retrieved promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-revert-check",
					Namespace: "default",
				}, &retrieved)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(retrieved.Status.Environments).To(HaveLen(3))

				// All environments should pass initially (no reverts)
				for _, env := range retrieved.Status.Environments {
					g.Expect(env.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
						"Environment %s should pass initially", env.Branch)
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Simulating a revert on the staging active branch using git revert")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() {
				_ = os.RemoveAll(gitPath)
			}()

			// Clone the repo
			repoURL := "http://localhost:" + gitServerPort + "/" + name + "/" + name
			_, err = runGitCmd(ctx, gitPath, "clone", "--verbose", "--progress", "--filter=blob:none", repoURL, ".")
			Expect(err).NotTo(HaveOccurred())

			_, err = runGitCmd(ctx, gitPath, "config", "user.name", "testuser")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(ctx, gitPath, "config", "user.email", "testmail@test.com")
			Expect(err).NotTo(HaveOccurred())

			// Checkout the staging active branch
			_, err = runGitCmd(ctx, gitPath, "checkout", "-B", testBranchStaging, "origin/"+testBranchStaging)
			Expect(err).NotTo(HaveOccurred())

			// First, create a commit that we can revert
			f, err := os.Create(gitPath + "/feature-to-revert.yaml")
			Expect(err).NotTo(HaveOccurred())
			_, err = f.WriteString("feature: enabled\n")
			Expect(err).NotTo(HaveOccurred())
			err = f.Close()
			Expect(err).NotTo(HaveOccurred())

			_, err = runGitCmd(ctx, gitPath, "add", "feature-to-revert.yaml")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(ctx, gitPath, "commit", "-m", "feat: add new feature")
			Expect(err).NotTo(HaveOccurred())

			// Get the SHA of the commit we just created (to revert it)
			commitToRevert, err := runGitCmd(ctx, gitPath, "rev-parse", "HEAD")
			Expect(err).NotTo(HaveOccurred())
			commitToRevert = strings.TrimSpace(commitToRevert)

			// Get the SHA before we make the revert for the webhook
			beforeSha, err := runGitCmd(ctx, gitPath, "rev-parse", testBranchStaging)
			Expect(err).NotTo(HaveOccurred())
			beforeSha = strings.TrimSpace(beforeSha)

			// Use actual git revert command - this creates a commit with "Revert" prefix automatically
			_, err = runGitCmd(ctx, gitPath, "revert", "--no-edit", commitToRevert)
			Expect(err).NotTo(HaveOccurred())

			// Push the revert commit
			_, err = runGitCmd(ctx, gitPath, "push", "-u", "origin", testBranchStaging)
			Expect(err).NotTo(HaveOccurred())

			// Send webhook to trigger reconciliation
			sendWebhookForPush(ctx, beforeSha, testBranchStaging)

			By("Verifying staging environment fails due to revert detection")
			Eventually(func(g Gomega) {
				var retrieved promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name + "-revert-check",
					Namespace: "default",
				}, &retrieved)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(retrieved.Status.Environments).To(HaveLen(3))

				var stagingEnv *promoterv1alpha1.GitCommitStatusEnvironmentStatus
				var devEnv *promoterv1alpha1.GitCommitStatusEnvironmentStatus
				var prodEnv *promoterv1alpha1.GitCommitStatusEnvironmentStatus

				for i, env := range retrieved.Status.Environments {
					switch env.Branch {
					case testBranchDevelopment:
						devEnv = &retrieved.Status.Environments[i]
					case testBranchStaging:
						stagingEnv = &retrieved.Status.Environments[i]
					case testBranchProduction:
						prodEnv = &retrieved.Status.Environments[i]
					default:
						// Ignore unknown environments
					}
				}

				g.Expect(stagingEnv).ToNot(BeNil(), "Staging environment should exist")
				g.Expect(devEnv).ToNot(BeNil(), "Development environment should exist")
				g.Expect(prodEnv).ToNot(BeNil(), "Production environment should exist")

				// Staging should fail because active commit starts with "Revert"
				g.Expect(stagingEnv.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseFailure)),
					"Staging should fail due to revert commit")
				g.Expect(stagingEnv.ExpressionResult).ToNot(BeNil())
				g.Expect(*stagingEnv.ExpressionResult).To(BeFalse(),
					"Expression should evaluate to false for revert commit")

				// Development and Production should still pass (no reverts on those branches)
				g.Expect(devEnv.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
					"Development should still pass")
				g.Expect(prodEnv.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
					"Production should still pass")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying the CommitStatus for staging shows failure")
			Eventually(func(g Gomega) {
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-revert-check-"+testBranchStaging+"-test-validation")
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

	Describe("Promotion Gating via GitCommitStatus", func() {
		// This test validates that GitCommitStatus resources can gate promotions:
		// - 3 GitCommitStatus resources (one per environment: dev, staging, production)
		// - Dev and staging have passing expressions (check author exists - always true)
		// - Production has a failing expression (check for non-existent author)
		// - The production PR should NOT be merged because the commit status is failing
		const (
			devGateKey     = "dev-gate"
			stagingGateKey = "staging-gate"
			prodGateKey    = "prod-gate"
		)

		var (
			devGCS     *promoterv1alpha1.GitCommitStatus
			stagingGCS *promoterv1alpha1.GitCommitStatus
			prodGCS    *promoterv1alpha1.GitCommitStatus
			gatingPS   *promoterv1alpha1.PromotionStrategy
			gatingName string
		)

		BeforeEach(func() {
			By("Creating a PromotionStrategy with per-environment commit status requirements")
			gatingName, scmSecret, scmProvider, gitRepo, _, _, gatingPS = promotionStrategyResource(ctx, "git-commit-gating", "default")

			// Configure per-environment ProposedCommitStatuses
			for i := range gatingPS.Spec.Environments {
				env := &gatingPS.Spec.Environments[i]
				switch env.Branch {
				case testBranchDevelopment:
					env.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
						{Key: devGateKey},
					}
				case testBranchStaging:
					env.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
						{Key: stagingGateKey},
					}
				case testBranchProduction:
					env.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
						{Key: prodGateKey},
					}
				default:
					// No commit status requirements for other environments
				}
			}

			setupInitialTestGitRepoOnServer(ctx, gatingName, gatingName)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, gatingPS)).To(Succeed())
		})

		AfterEach(func() {
			if devGCS != nil {
				_ = k8sClient.Delete(ctx, devGCS)
			}
			if stagingGCS != nil {
				_ = k8sClient.Delete(ctx, stagingGCS)
			}
			if prodGCS != nil {
				_ = k8sClient.Delete(ctx, prodGCS)
			}
			if gatingPS != nil {
				_ = k8sClient.Delete(ctx, gatingPS)
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

		It("should gate production promotion when its GitCommitStatus fails while dev and staging pass", func() {
			By("Making a change and hydrating the repo to create proposed commits")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath) }()

			drySha, shortSha := makeChangeAndHydrateRepo(gitPath, gatingName, gatingName, "test commit for gating", "hydrated commit for gating")
			GinkgoLogr.Info("Created hydrated commit", "drySha", drySha, "shortSha", shortSha)

			By("Waiting for PromotionStrategy to pick up the hydrated commits with correct SHAs")
			var devProposedSha, stagingProposedSha, prodProposedSha string
			Eventually(func(g Gomega) {
				var ps promoterv1alpha1.PromotionStrategy
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      gatingName,
					Namespace: "default",
				}, &ps)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ps.Status.Environments).To(HaveLen(3))

				for _, env := range ps.Status.Environments {
					// Verify the dry SHA matches what we committed
					g.Expect(env.Proposed.Dry.Sha).To(Equal(drySha),
						"Proposed dry SHA should match the committed dry SHA for "+env.Branch)
					g.Expect(env.Proposed.Hydrated.Sha).ToNot(BeEmpty(),
						"Proposed hydrated SHA should be set for "+env.Branch)
					g.Expect(env.Active.Hydrated.Sha).ToNot(BeEmpty(),
						"Active hydrated SHA should be set for "+env.Branch)

					switch env.Branch {
					case testBranchDevelopment:
						devProposedSha = env.Proposed.Hydrated.Sha
					case testBranchStaging:
						stagingProposedSha = env.Proposed.Hydrated.Sha
					case testBranchProduction:
						prodProposedSha = env.Proposed.Hydrated.Sha
					default:
						// Other environments not tracked
					}
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			GinkgoLogr.Info("PromotionStrategy has proposed SHAs",
				"drySha", drySha,
				"devProposedSha", devProposedSha,
				"stagingProposedSha", stagingProposedSha,
				"prodProposedSha", prodProposedSha)

			By("Creating GitCommitStatus for development - PASSING (author exists)")
			devGCS = &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      gatingName + "-" + devGateKey,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: gatingName,
					},
					Key:         devGateKey,
					Description: "Development gate - validates author exists",
					Expression:  `Commit.Author != ""`, // Passes - commits always have authors
				},
			}
			Expect(k8sClient.Create(ctx, devGCS)).To(Succeed())

			By("Creating GitCommitStatus for staging - PASSING (subject exists)")
			stagingGCS = &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      gatingName + "-" + stagingGateKey,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: gatingName,
					},
					Key:         stagingGateKey,
					Description: "Staging gate - validates subject exists",
					Expression:  `Commit.Subject != ""`, // Passes - commits always have subjects
				},
			}
			Expect(k8sClient.Create(ctx, stagingGCS)).To(Succeed())

			By("Creating GitCommitStatus for production - FAILING (requires non-existent author)")
			prodGCS = &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      gatingName + "-" + prodGateKey,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: gatingName,
					},
					Key:         prodGateKey,
					Description: "Production gate - requires approval (non-existent author)",
					Expression:  `Commit.Author == "prod-approver@example.com"`, // Fails - this author doesn't exist
				},
			}
			Expect(k8sClient.Create(ctx, prodGCS)).To(Succeed())

			By("Verifying development GitCommitStatus passes with correct SHA")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      gatingName + "-" + devGateKey,
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(gcs.Status.Environments).To(HaveLen(1))
				g.Expect(gcs.Status.Environments[0].Branch).To(Equal(testBranchDevelopment))
				g.Expect(gcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
					"Development gate should pass")
				g.Expect(gcs.Status.Environments[0].ProposedHydratedSha).To(Equal(devProposedSha),
					"Development GitCommitStatus should be evaluating the correct proposed SHA")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying staging GitCommitStatus passes with correct SHA")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      gatingName + "-" + stagingGateKey,
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(gcs.Status.Environments).To(HaveLen(1))
				g.Expect(gcs.Status.Environments[0].Branch).To(Equal(testBranchStaging))
				g.Expect(gcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
					"Staging gate should pass")
				g.Expect(gcs.Status.Environments[0].ProposedHydratedSha).To(Equal(stagingProposedSha),
					"Staging GitCommitStatus should be evaluating the correct proposed SHA")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying production GitCommitStatus FAILS with correct SHA")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      gatingName + "-" + prodGateKey,
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(gcs.Status.Environments).To(HaveLen(1))
				g.Expect(gcs.Status.Environments[0].Branch).To(Equal(testBranchProduction))
				g.Expect(gcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseFailure)),
					"Production gate should FAIL")
				g.Expect(gcs.Status.Environments[0].ProposedHydratedSha).To(Equal(prodProposedSha),
					"Production GitCommitStatus should be evaluating the correct proposed SHA")
				g.Expect(gcs.Status.Environments[0].ExpressionResult).ToNot(BeNil())
				g.Expect(*gcs.Status.Environments[0].ExpressionResult).To(BeFalse(),
					"Production expression should evaluate to false")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying CommitStatus for development is success with correct SHA")
			devCommitStatusName := utils.KubeSafeUniqueName(ctx,
				gatingName+"-"+devGateKey+"-"+testBranchDevelopment+"-"+devGateKey)
			Eventually(func(g Gomega) {
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      devCommitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess),
					"Development CommitStatus should be success")
				g.Expect(cs.Spec.Sha).To(Equal(devProposedSha),
					"Development CommitStatus should have correct SHA")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying CommitStatus for staging is success with correct SHA")
			stagingCommitStatusName := utils.KubeSafeUniqueName(ctx,
				gatingName+"-"+stagingGateKey+"-"+testBranchStaging+"-"+stagingGateKey)
			Eventually(func(g Gomega) {
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      stagingCommitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess),
					"Staging CommitStatus should be success")
				g.Expect(cs.Spec.Sha).To(Equal(stagingProposedSha),
					"Staging CommitStatus should have correct SHA")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying CommitStatus for production is FAILURE with correct SHA - this gates the promotion")
			prodCommitStatusName := utils.KubeSafeUniqueName(ctx,
				gatingName+"-"+prodGateKey+"-"+testBranchProduction+"-"+prodGateKey)
			Eventually(func(g Gomega) {
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      prodCommitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseFailure),
					"Production CommitStatus should be FAILURE - this gates the promotion")
				g.Expect(cs.Spec.Sha).To(Equal(prodProposedSha),
					"Production CommitStatus should have correct SHA")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying ChangeTransferPolicy for production has failing commit status")
			ctpProdName := utils.KubeSafeUniqueName(ctx, gatingName+"-"+testBranchProduction)
			Eventually(func(g Gomega) {
				var ctp promoterv1alpha1.ChangeTransferPolicy
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpProdName,
					Namespace: "default",
				}, &ctp)
				g.Expect(err).NotTo(HaveOccurred())

				// The CTP should have the commit status in its proposed statuses
				g.Expect(ctp.Status.Proposed.CommitStatuses).ToNot(BeEmpty(),
					"Production CTP should have proposed commit statuses")

				// Find the prod-gate status
				var foundProdGate bool
				for _, status := range ctp.Status.Proposed.CommitStatuses {
					if status.Key == prodGateKey {
						foundProdGate = true
						g.Expect(status.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseFailure)),
							"Production gate in CTP should be FAILURE")
					}
				}
				g.Expect(foundProdGate).To(BeTrue(),
					"Should find prod-gate in CTP proposed commit statuses")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying PromotionStrategy status reflects commit status phases and SHAs for all environments")
			Eventually(func(g Gomega) {
				var ps promoterv1alpha1.PromotionStrategy
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      gatingName,
					Namespace: "default",
				}, &ps)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ps.Status.Environments).To(HaveLen(3))

				for _, envStatus := range ps.Status.Environments {
					// Check Proposed commit statuses - these gate whether a PR can be merged
					switch envStatus.Branch {
					case testBranchDevelopment:
						// Verify SHA matches what we captured earlier
						g.Expect(envStatus.Proposed.Hydrated.Sha).To(Equal(devProposedSha),
							"Development proposed SHA should match captured SHA")
						// Dev should have passing commit status on proposed
						g.Expect(envStatus.Proposed.CommitStatuses).ToNot(BeEmpty(),
							"Development should have proposed commit statuses")
						var foundDevGate bool
						for _, cs := range envStatus.Proposed.CommitStatuses {
							if cs.Key == devGateKey {
								foundDevGate = true
								g.Expect(cs.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
									"Development gate in PromotionStrategy should be SUCCESS")
							}
						}
						g.Expect(foundDevGate).To(BeTrue(),
							"Should find dev-gate in PromotionStrategy development environment")

					case testBranchStaging:
						// Verify SHA matches what we captured earlier
						g.Expect(envStatus.Proposed.Hydrated.Sha).To(Equal(stagingProposedSha),
							"Staging proposed SHA should match captured SHA")
						// Staging should have passing commit status on proposed
						g.Expect(envStatus.Proposed.CommitStatuses).ToNot(BeEmpty(),
							"Staging should have proposed commit statuses")
						var foundStagingGate bool
						for _, cs := range envStatus.Proposed.CommitStatuses {
							if cs.Key == stagingGateKey {
								foundStagingGate = true
								g.Expect(cs.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
									"Staging gate in PromotionStrategy should be SUCCESS")
							}
						}
						g.Expect(foundStagingGate).To(BeTrue(),
							"Should find staging-gate in PromotionStrategy staging environment")

					case testBranchProduction:
						// Verify SHA matches what we captured earlier
						g.Expect(envStatus.Proposed.Hydrated.Sha).To(Equal(prodProposedSha),
							"Production proposed SHA should match captured SHA")
						// Production should have FAILING commit status on proposed - this gates the promotion
						g.Expect(envStatus.Proposed.CommitStatuses).ToNot(BeEmpty(),
							"Production should have proposed commit statuses")
						var foundProdGate bool
						for _, cs := range envStatus.Proposed.CommitStatuses {
							if cs.Key == prodGateKey {
								foundProdGate = true
								g.Expect(cs.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseFailure)),
									"Production gate in PromotionStrategy should be FAILURE - this gates promotion")
							}
						}
						g.Expect(foundProdGate).To(BeTrue(),
							"Should find prod-gate in PromotionStrategy production environment")
					default:
						// Other environments not checked
					}
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying production gate CONSISTENTLY stays in FAILURE for 5 seconds (gating is stable)")
			Consistently(func(g Gomega) {
				var ps promoterv1alpha1.PromotionStrategy
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      gatingName,
					Namespace: "default",
				}, &ps)
				g.Expect(err).NotTo(HaveOccurred())

				// Find production environment
				for _, envStatus := range ps.Status.Environments {
					if envStatus.Branch == testBranchProduction {
						g.Expect(envStatus.Proposed.CommitStatuses).ToNot(BeEmpty(),
							"Production should have proposed commit statuses")
						for _, cs := range envStatus.Proposed.CommitStatuses {
							if cs.Key == prodGateKey {
								g.Expect(cs.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseFailure)),
									"Production gate should CONSISTENTLY be FAILURE")
							}
						}
					}
				}
			}, 5*time.Second, 500*time.Millisecond).Should(Succeed())

			GinkgoLogr.Info("Production gate consistently failed for 5 seconds - gating is working")

			By("Updating production GitCommitStatus to use a passing expression")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      gatingName + "-" + prodGateKey,
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Change expression to one that passes
				gcs.Spec.Expression = `Commit.Author != ""` // Now passes - commits always have authors
				gcs.Spec.Description = "Production gate - now approved"
				err = k8sClient.Update(ctx, &gcs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying production GitCommitStatus now passes")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      gatingName + "-" + prodGateKey,
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(gcs.Status.Environments).To(HaveLen(1))
				g.Expect(gcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
					"Production gate should now PASS after expression update")
				g.Expect(gcs.Status.Environments[0].ExpressionResult).ToNot(BeNil())
				g.Expect(*gcs.Status.Environments[0].ExpressionResult).To(BeTrue(),
					"Production expression should now evaluate to true")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying PromotionStrategy now shows production gate as SUCCESS")
			Eventually(func(g Gomega) {
				var ps promoterv1alpha1.PromotionStrategy
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      gatingName,
					Namespace: "default",
				}, &ps)
				g.Expect(err).NotTo(HaveOccurred())

				// Find production environment and verify gate is now passing
				for _, envStatus := range ps.Status.Environments {
					if envStatus.Branch == testBranchProduction {
						g.Expect(envStatus.Proposed.CommitStatuses).ToNot(BeEmpty(),
							"Production should have proposed commit statuses")
						var foundProdGate bool
						for _, cs := range envStatus.Proposed.CommitStatuses {
							if cs.Key == prodGateKey {
								foundProdGate = true
								g.Expect(cs.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
									"Production gate in PromotionStrategy should now be SUCCESS")
							}
						}
						g.Expect(foundProdGate).To(BeTrue(),
							"Should find prod-gate in PromotionStrategy production environment")
					}
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying CommitStatus for production is now SUCCESS")
			Eventually(func(g Gomega) {
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      prodCommitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess),
					"Production CommitStatus should now be SUCCESS after gate approval")
			}, constants.EventuallyTimeout).Should(Succeed())

			GinkgoLogr.Info("Promotion gating test complete - gate transitioned from FAILURE to SUCCESS",
				"drySha", drySha,
				"shortSha", shortSha,
				"devProposedSha", devProposedSha,
				"stagingProposedSha", stagingProposedSha,
				"prodProposedSha", prodProposedSha)
		})
	})
})
