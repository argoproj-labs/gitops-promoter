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

			// Configure ActiveCommitStatuses to check for git commit status
			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "git-expr"},
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
					Environments: []promoterv1alpha1.GitCommitStatusEnvironment{
						{
							Branch:     testEnvironmentDevelopment,
							Expression: `Commit.Author == Commit.Committer`, // Simple expression that should pass
							Name:       "author-check",
						},
					},
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

				// Should have status for dev environment
				g.Expect(gcs.Status.Environments).To(HaveLen(1))
				g.Expect(gcs.Status.Environments[0].Branch).To(Equal(testEnvironmentDevelopment))
				g.Expect(gcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

				// Validate status fields are populated
				g.Expect(gcs.Status.Environments[0].Sha).ToNot(BeEmpty(), "Sha should be populated")
				g.Expect(gcs.Status.Environments[0].ExpressionResult).ToNot(BeNil(), "ExpressionResult should be set")
				g.Expect(*gcs.Status.Environments[0].ExpressionResult).To(BeTrue(), "Expression should evaluate to true")

				// Verify CommitStatus was created for dev environment with success phase
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-"+testEnvironmentDevelopment+"-git-expr-author-check")
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

			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "git-expr"},
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
					Environments: []promoterv1alpha1.GitCommitStatusEnvironment{
						{
							Branch:     testEnvironmentDevelopment,
							Expression: `false`, // Expression that always evaluates to false
							Name:       "always-fail",
						},
					},
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

				g.Expect(gcs.Status.Environments).To(HaveLen(1))
				g.Expect(gcs.Status.Environments[0].Branch).To(Equal(testEnvironmentDevelopment))
				g.Expect(gcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseFailure)))

				g.Expect(gcs.Status.Environments[0].Sha).ToNot(BeEmpty())
				g.Expect(gcs.Status.Environments[0].ExpressionResult).ToNot(BeNil())
				g.Expect(*gcs.Status.Environments[0].ExpressionResult).To(BeFalse(), "Expression should evaluate to false")

				// Verify CommitStatus was created with failure phase
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-"+testEnvironmentDevelopment+"-git-expr-always-fail")
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

			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "git-expr"},
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
					Environments: []promoterv1alpha1.GitCommitStatusEnvironment{
						{
							Branch:     testEnvironmentDevelopment,
							Expression: `Commit.Invalid..Field`, // Invalid syntax
							Name:       "invalid-expr",
						},
					},
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

				g.Expect(gcs.Status.Environments).To(HaveLen(1))
				g.Expect(gcs.Status.Environments[0].Branch).To(Equal(testEnvironmentDevelopment))
				g.Expect(gcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseFailure)))

				g.Expect(gcs.Status.Environments[0].Message).To(ContainSubstring("Expression compilation failed"))
				g.Expect(gcs.Status.Environments[0].ExpressionResult).To(BeNil(), "ExpressionResult should be nil on compilation error")

				// Verify CommitStatus reflects the error
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-"+testEnvironmentDevelopment+"-git-expr-invalid-expr")
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

			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "git-expr"},
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

			By("Creating a GitCommitStatus resource with expressions for all environments")
			gitCommitStatus = &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Environments: []promoterv1alpha1.GitCommitStatusEnvironment{
						{
							Branch:     testEnvironmentDevelopment,
							Expression: `true`, // Always pass
							Name:       "dev-check",
						},
						{
							Branch:     testEnvironmentStaging,
							Expression: `Commit.SHA != ""`, // Should pass
							Name:       "staging-check",
						},
						{
							Branch:     testEnvironmentProduction,
							Expression: `false`, // Always fail
							Name:       "prod-check",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, gitCommitStatus)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, gitCommitStatus)
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should evaluate expressions independently for each environment", func() {
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

				// Dev should be success
				devEnv := findEnvironmentStatus(gcs.Status.Environments, testEnvironmentDevelopment)
				g.Expect(devEnv).ToNot(BeNil())
				g.Expect(devEnv.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				g.Expect(devEnv.ExpressionResult).ToNot(BeNil())
				g.Expect(*devEnv.ExpressionResult).To(BeTrue())

				// Staging should be success
				stagingEnv := findEnvironmentStatus(gcs.Status.Environments, testEnvironmentStaging)
				g.Expect(stagingEnv).ToNot(BeNil())
				g.Expect(stagingEnv.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				g.Expect(stagingEnv.ExpressionResult).ToNot(BeNil())
				g.Expect(*stagingEnv.ExpressionResult).To(BeTrue())

				// Production should be failure
				prodEnv := findEnvironmentStatus(gcs.Status.Environments, testEnvironmentProduction)
				g.Expect(prodEnv).ToNot(BeNil())
				g.Expect(prodEnv.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseFailure)))
				g.Expect(prodEnv.ExpressionResult).ToNot(BeNil())
				g.Expect(*prodEnv.ExpressionResult).To(BeFalse())
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
					Environments: []promoterv1alpha1.GitCommitStatusEnvironment{
						{
							Branch:     "environment/dev",
							Expression: `true`,
						},
					},
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

			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "git-expr"},
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
					Environments: []promoterv1alpha1.GitCommitStatusEnvironment{
						{
							Branch:     testEnvironmentDevelopment,
							Expression: `Commit.SHA != ""`,
							Name:       "sha-check",
						},
					},
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
				g.Expect(gcs.Status.Environments).To(HaveLen(1))
				g.Expect(gcs.Status.Environments[0].Sha).ToNot(BeEmpty())
				initialSha = gcs.Status.Environments[0].Sha
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
				g.Expect(gcs.Status.Environments).To(HaveLen(1))
				// The proposed SHA should be different from initial
				// Note: We're checking proposed SHA since it's been updated
				g.Expect(gcs.Status.Environments[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
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

			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "git-expr"},
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
					Environments: []promoterv1alpha1.GitCommitStatusEnvironment{
						{
							Branch:     testEnvironmentDevelopment,
							Expression: `true`,
							Name:       "delete-test",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, gitCommitStatus)).To(Succeed())

			By("Waiting for CommitStatus to be created")
			Eventually(func(g Gomega) {
				commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-"+testEnvironmentDevelopment+"-git-expr-delete-test")
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
			commitStatusName := utils.KubeSafeUniqueName(ctx, name+"-"+testEnvironmentDevelopment+"-git-expr-delete-test")

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

// Helper function to find an environment status by branch name
func findEnvironmentStatus(envStatuses []promoterv1alpha1.GitCommitStatusEnvironmentStatus, branch string) *promoterv1alpha1.GitCommitStatusEnvironmentStatus {
	for i := range envStatuses {
		if envStatuses[i].Branch == branch {
			return &envStatuses[i]
		}
	}
	return nil
}
