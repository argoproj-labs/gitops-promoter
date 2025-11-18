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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("GitCommitStatus Controller", func() {
	Context("When evaluating expression with commit data", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var gitCommitStatus *promoterv1alpha1.GitCommitStatus

		BeforeEach(func() {
			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "git-commit-status", "default")

			// Configure ProposedCommitStatuses to check for git commit status
			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "author-check"},
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

			By("Creating a GitCommitStatus resource with a simple expression")
			gitCommitStatus = &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Name:       "author-check",
					Expression: `Commit.Author == Commit.Committer`, // Simple expression that should pass
				},
			}
			Expect(k8sClient.Create(ctx, gitCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, gitCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should evaluate expression successfully when condition is met", func() {
			By("Waiting for GitCommitStatus to process the environment")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have status for all three environments (from PromotionStrategy)
				g.Expect(gcs.Status.Environments).To(HaveLen(3))
				// All environments should have the same validation result
				for _, env := range gcs.Status.Environments {
					g.Expect(env.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
					g.Expect(env.Sha).ToNot(BeEmpty(), "Sha should be populated")
					g.Expect(env.ExpressionResult).ToNot(BeNil(), "ExpressionResult should be set")
					g.Expect(*env.ExpressionResult).To(BeTrue(), "Expression should evaluate to true")
				}

				// Verify CommitStatus was created for dev environment with success phase
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-"+testEnvironmentDevelopment+"-author-check")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				g.Expect(cs.Spec.Description).To(ContainSubstring("Expression evaluated to true"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When expression evaluates to false", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var gitCommitStatus *promoterv1alpha1.GitCommitStatus

		BeforeEach(func() {
			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "git-commit-status-false", "default")

			// Configure ProposedCommitStatuses
			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "always-fail"},
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
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a GitCommitStatus resource with an expression that evaluates to false")
			gitCommitStatus = &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Name:       "always-fail",
					Expression: `false`, // Expression that always evaluates to false
				},
			}
			Expect(k8sClient.Create(ctx, gitCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, gitCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should report failure status when expression evaluates to false", func() {
			By("Waiting for GitCommitStatus to process the environment with failure phase")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(gcs.Status.Environments).To(HaveLen(3))

				// All environments should have failure phase
				for _, env := range gcs.Status.Environments {
					g.Expect(env.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseFailure)))
					g.Expect(env.Sha).ToNot(BeEmpty())
					g.Expect(env.ExpressionResult).ToNot(BeNil())
					g.Expect(*env.ExpressionResult).To(BeFalse(), "Expression should evaluate to false")
				}

				// Verify CommitStatus was created with failure phase
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-"+testEnvironmentDevelopment+"-always-fail")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseFailure))
				g.Expect(cs.Spec.Description).To(ContainSubstring("Expression evaluated to false"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When expression has syntax errors", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var gitCommitStatus *promoterv1alpha1.GitCommitStatus

		BeforeEach(func() {
			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "git-commit-status-error", "default")

			// Configure ProposedCommitStatuses
			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "invalid-expr"},
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
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a GitCommitStatus resource with an invalid expression")
			gitCommitStatus = &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Name:       "invalid-expr",
					Expression: `Commit.Invalid..Field`, // Invalid syntax
				},
			}
			Expect(k8sClient.Create(ctx, gitCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, gitCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should report failure status when expression has compilation errors", func() {
			By("Waiting for GitCommitStatus to process with compilation error")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())

				g.Expect(gcs.Status.Environments).To(HaveLen(3))

				// All environments should have failure phase
				for _, env := range gcs.Status.Environments {
					g.Expect(env.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseFailure)))
					g.Expect(env.Message).To(ContainSubstring("Expression compilation failed"))
					g.Expect(env.ExpressionResult).To(BeNil(), "ExpressionResult should be nil on compilation error")
				}

				// Verify CommitStatus reflects the error
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-"+testEnvironmentDevelopment+"-invalid-expr")
				var cs promoterv1alpha1.CommitStatus
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseFailure))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When evaluating multiple environments", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var gitCommitStatus *promoterv1alpha1.GitCommitStatus

		BeforeEach(func() {
			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "git-commit-multi-env", "default")

			// Configure ProposedCommitStatuses
			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "multi-env-check"},
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
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a GitCommitStatus resource with expression for all environments")
			gitCommitStatus = &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Name:       "multi-env-check",
					Expression: `Commit.SHA != ""`, // Should pass for all
				},
			}
			Expect(k8sClient.Create(ctx, gitCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, gitCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should evaluate expression independently for each environment", func() {
			By("Waiting for GitCommitStatus to process all environments")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())

				// Should have status for all three environments
				g.Expect(gcs.Status.Environments).To(HaveLen(3))

				// All environments should have success phase (expression passes)
				for _, env := range gcs.Status.Environments {
					g.Expect(env.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
					g.Expect(env.ExpressionResult).ToNot(BeNil())
					g.Expect(*env.ExpressionResult).To(BeTrue())
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When PromotionStrategy is not found", func() {
		const resourceName = "git-commit-status-no-ps"

		ctx := context.Background()

		var gitCommitStatus *promoterv1alpha1.GitCommitStatus

		BeforeEach(func() {
			By("Creating only a GitCommitStatus resource without PromotionStrategy")
			gitCommitStatus = &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: "non-existent",
					},
					Name:       "test-validation",
					Expression: `true`,
				},
			}
			Expect(k8sClient.Create(ctx, gitCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, gitCommitStatus)
		})

		It("should handle missing PromotionStrategy gracefully", func() {
			By("Verifying the GitCommitStatus exists but doesn't process environments")
			Consistently(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName,
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())
				// Status should be empty since PromotionStrategy doesn't exist
				g.Expect(gcs.Status.Environments).To(BeEmpty())
			}, 2*time.Second, 500*time.Millisecond).Should(Succeed())
		})
	})

	Context("When SHA changes and transitions environments", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var gitCommitStatus *promoterv1alpha1.GitCommitStatus

		BeforeEach(func() {
			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "git-commit-sha-change", "default")

			// Configure ProposedCommitStatuses
			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "sha-check"},
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
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating a GitCommitStatus resource")
			gitCommitStatus = &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Name:       "sha-check",
					Expression: `Commit.SHA != ""`,
				},
			}
			Expect(k8sClient.Create(ctx, gitCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, gitCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should update CommitStatus when environment transitions to success", func() {
			var initialSha string

			By("Waiting for initial GitCommitStatus to be processed")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())
				// Should process all 3 environments now
				g.Expect(gcs.Status.Environments).To(HaveLen(3))
				// Get the development environment SHA
				for _, env := range gcs.Status.Environments {
					if env.Branch == testEnvironmentDevelopment {
						g.Expect(env.Sha).ToNot(BeEmpty())
						initialSha = env.Sha
						break
					}
				}
				g.Expect(initialSha).ToNot(BeEmpty(), "Should have found development environment")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Making a change to trigger new SHA in development")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			makeChangeAndHydrateRepo(gitPath, name, name, "new commit for sha test", "new content")

			By("Waiting for PromotionStrategy to reflect the new SHA")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, promotionStrategy)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).ToNot(Equal(initialSha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Waiting for GitCommitStatus to update with new SHA")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &gcs)
				g.Expect(err).NotTo(HaveOccurred())
				// Should process all 3 environments
				g.Expect(gcs.Status.Environments).To(HaveLen(3))
				// Check that the development environment has been updated with new SHA
				for _, env := range gcs.Status.Environments {
					if env.Branch == testEnvironmentDevelopment {
						g.Expect(env.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
						g.Expect(env.Sha).ToNot(Equal(initialSha), "SHA should have changed")
						break
					}
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When GitCommitStatus is deleted", func() {
		ctx := context.Background()

		var name string
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var gitCommitStatus *promoterv1alpha1.GitCommitStatus

		BeforeEach(func() {
			By("Creating the test resources")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "git-commit-delete", "default")

			// Configure ProposedCommitStatuses
			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "delete-test"},
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
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
			}, constants.EventuallyTimeout).Should(Succeed())

			gitCommitStatus = &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Name:       "delete-test",
					Expression: `true`,
				},
			}
			Expect(k8sClient.Create(ctx, gitCommitStatus)).To(Succeed())

			By("Waiting for CommitStatus to be created")
			Eventually(func(g Gomega) {
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-"+testEnvironmentDevelopment+"-delete-test")
				var cs promoterv1alpha1.CommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      commitStatusName,
					Namespace: "default",
				}, &cs)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should clean up associated CommitStatus resources", func() {
			commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-"+testEnvironmentDevelopment+"-delete-test")

			By("Deleting the GitCommitStatus resource")
			err := k8sClient.Delete(ctx, gitCommitStatus)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the GitCommitStatus is deleted")
			Eventually(func(g Gomega) {
				var gcs promoterv1alpha1.GitCommitStatus
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}, &gcs)
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying the CommitStatus still exists (not deleted by GitCommitStatus)")
			// CommitStatus is owned by the PromotionStrategy reconciliation, not GitCommitStatus
			// So it should remain until PromotionStrategy decides to clean it up
			var cs promoterv1alpha1.CommitStatus
			err = k8sClient.Get(ctx, types.NamespacedName{
				Name:      commitStatusName,
				Namespace: "default",
			}, &cs)
			// We expect it to still exist since it's managed by the lifecycle of commits
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
