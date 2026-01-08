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
	_ "embed"
	"fmt"
	"strings"
	"time"

	"github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"k8s.io/apimachinery/pkg/api/meta"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed testdata/PullRequest.yaml
var testPullRequestYAML string

var _ = Describe("PullRequest Controller", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the PullRequest resource", func() {
			err := unmarshalYamlStrict(testPullRequestYAML, &promoterv1alpha1.PullRequest{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("When reconciling a resource", func() {
		var ctx context.Context
		var name string
		var scmSecret *v1.Secret
		var scmProvider *promoterv1alpha1.ScmProvider
		var gitRepo *promoterv1alpha1.GitRepository
		var pullRequest *promoterv1alpha1.PullRequest
		var typeNamespacedName types.NamespacedName

		BeforeEach(func() {
			ctx = context.Background()
		})

		Context("When updating title then merging", func() {
			BeforeEach(func() {
				By("Creating test resources")
				name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "update-title-merge")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

				By("Waiting for PullRequest to be open")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				}, constants.EventuallyTimeout)
			})

			It("should successfully reconcile the resource when updating title then merging", func() {
				By("Reconciling updating of the PullRequest")
				Eventually(func(g Gomega) {
					_ = k8sClient.Get(ctx, typeNamespacedName, pullRequest)
					pullRequest.Spec.Title = "Updated Title"
					g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
				}, constants.EventuallyTimeout)

				Eventually(func(g Gomega) {
					Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					g.Expect(pullRequest.Spec.Title).To(Equal("Updated Title"))
				}, constants.EventuallyTimeout)

				By("Reconciling merging of the PullRequest")
				Eventually(func(g Gomega) {
					_ = k8sClient.Get(ctx, typeNamespacedName, pullRequest)
					pullRequest.Spec.State = "merged"
					g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
					g.Expect(err).To(HaveOccurred())
					g.Expect(err.Error()).To(ContainSubstring("pullrequests.promoter.argoproj.io \"" + name + "\" not found"))
				}, constants.EventuallyTimeout)
			})
		})

		Context("When closing", func() {
			BeforeEach(func() {
				By("Creating test resources")
				name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "update-title-close")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

				By("Waiting for PullRequest to be open")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				}, constants.EventuallyTimeout).Should(Succeed())
			})

			It("should successfully reconcile the resource when closing", func() {
				By("Reconciling closing of the PullRequest")
				Eventually(func(g Gomega) {
					_ = k8sClient.Get(ctx, typeNamespacedName, pullRequest)
					pullRequest.Spec.State = "closed"
					g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
					g.Expect(err).To(HaveOccurred())
					g.Expect(err.Error()).To(ContainSubstring("pullrequests.promoter.argoproj.io \"" + name + "\" not found"))
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})
	})

	Context("When reconciling a resource with a bad configuration", func() {
		var ctx context.Context
		var name string
		var scmSecret *v1.Secret
		var scmProvider *promoterv1alpha1.ScmProvider
		var gitRepo *promoterv1alpha1.GitRepository
		var pullRequest *promoterv1alpha1.PullRequest
		var typeNamespacedName types.NamespacedName

		BeforeEach(func() {
			ctx = context.Background()
		})

		Context("When ScmProvider has missing secret", func() {
			BeforeEach(func() {
				By("Creating test resources with bad configuration")
				name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "bad-configuration-no-scm-secret")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				scmProvider.Spec.SecretRef = &v1.LocalObjectReference{Name: "non-existing-secret"}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())
			})

			It("should successfully reconcile the resource and update conditions with the error", func() {
				By("Checking the PullRequest status conditions have an error condition")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					g.Expect(pullRequest.Status.Conditions).To(HaveLen(1))
					g.Expect(pullRequest.Status.Conditions[0].Type).To(Equal(string(conditions.Ready)))
					g.Expect(meta.IsStatusConditionFalse(pullRequest.Status.Conditions, string(conditions.Ready))).To(BeTrue())
					g.Expect(pullRequest.Status.Conditions[0].Reason).To(Equal(string(conditions.ReconciliationError)))
					g.Expect(pullRequest.Status.Conditions[0].Message).To(ContainSubstring("secret from ScmProvider not found"))
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		Context("When merge SHA is invalid", func() {
			BeforeEach(func() {
				By("Creating test resources with invalid merge SHA")
				name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "merge-error-message-test")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				// Set an invalid merge SHA that won't match the actual source branch HEAD
				pullRequest.Spec.MergeSha = "0000000000000000000000000000000000000000"

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

				By("Waiting for PullRequest to be created and open")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
					g.Expect(pullRequest.Status.ID).ToNot(BeEmpty())
				}, constants.EventuallyTimeout).Should(Succeed())
			})

			AfterEach(func() {
				By("Cleaning up the PullRequest")
				Expect(k8sClient.Delete(ctx, pullRequest)).To(Succeed())
			})

			It("should report merge error without redundant wrapping", func() {
				By("Attempting to merge the PullRequest with invalid SHA")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					pullRequest.Spec.State = promoterv1alpha1.PullRequestMerged
					g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Verifying the error message is not redundantly wrapped")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					g.Expect(pullRequest.Status.Conditions).ToNot(BeEmpty())
					g.Expect(meta.IsStatusConditionFalse(pullRequest.Status.Conditions, string(conditions.Ready))).To(BeTrue())
					g.Expect(pullRequest.Status.Conditions[0].Reason).To(Equal(string(conditions.ReconciliationError)))

					// The error message should contain "Reconciliation failed" and "failed to merge pull request" only once each
					message := pullRequest.Status.Conditions[0].Message
					g.Expect(message).To(ContainSubstring("Reconciliation failed"))
					g.Expect(message).To(ContainSubstring("failed to merge pull request"))

					// Count occurrences - should not have redundant wrapping
					// The message should be: "Reconciliation failed: failed to merge pull request: <actual error>"
					// NOT: "Reconciliation failed: failed to merge pull request: failed to merge pull request: failed to merge pull request: <actual error>"
					g.Expect(message).ToNot(ContainSubstring("failed to merge pull request: failed to merge pull request"))
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})
	})

	Context("When attempting to create a PullRequest with invalid initial state", func() {
		var ctx context.Context

		BeforeEach(func() {
			ctx = context.Background()
		})

		Context("When spec.state is set to 'merged'", func() {
			It("should fail to create a PullRequest with spec.state set to 'merged'", func() {
				By("Attempting to create a PullRequest with spec.state='merged' and empty status.id")

				_, scmSecret, scmProvider, gitRepo, pullRequest := pullRequestResources(ctx, "create-merged")

				pullRequest.Spec.State = promoterv1alpha1.PullRequestMerged

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())

				By("Verifying the create operation fails due to CEL validation")
				err := k8sClient.Create(ctx, pullRequest)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Cannot transition to 'closed' or 'merged' state when status.id is empty"))
			})
		})

		Context("When spec.state is set to 'closed'", func() {
			It("should fail to create a PullRequest with spec.state set to 'closed'", func() {
				By("Attempting to create a PullRequest with spec.state='closed' and empty status.id")

				_, scmSecret, scmProvider, gitRepo, pullRequest := pullRequestResources(ctx, "create-closed")

				pullRequest.Spec.State = promoterv1alpha1.PullRequestClosed

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())

				By("Verifying the create operation fails due to CEL validation")
				err := k8sClient.Create(ctx, pullRequest)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("Cannot transition to 'closed' or 'merged' state when status.id is empty"))
			})
		})
	})

	Context("When deleting a PullRequest that never created a PR on SCM", func() {
		var ctx context.Context
		var name string
		var scmSecret *v1.Secret
		var scmProvider *promoterv1alpha1.ScmProvider
		var gitRepo *promoterv1alpha1.GitRepository
		var pullRequest *promoterv1alpha1.PullRequest
		var typeNamespacedName types.NamespacedName

		BeforeEach(func() {
			ctx = context.Background()

			By("Creating test resources with bad configuration")
			name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "delete-without-scm-pr")

			typeNamespacedName = types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			// Create a bad SCM provider configuration to prevent PR creation
			scmProvider.Spec.SecretRef = &v1.LocalObjectReference{Name: "non-existing-secret"}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

			By("Waiting for PullRequest to be reconciled but not create a PR on SCM")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				// Should have an error condition but no status.id
				g.Expect(pullRequest.Status.ID).To(BeEmpty())
				g.Expect(pullRequest.Status.Conditions).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should successfully delete a PullRequest with empty status.id without getting stuck", func() {
			By("Deleting the PullRequest")
			Expect(k8sClient.Delete(ctx, pullRequest)).To(Succeed())

			By("Verifying the PullRequest is deleted successfully without getting stuck")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("pullrequests.promoter.argoproj.io \"" + name + "\" not found"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When deleting resources with finalizers", func() {
		var ctx context.Context

		BeforeEach(func() {
			ctx = context.Background()
		})

		Context("When PullRequest depends on GitRepository", func() {
			var name string
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			var pullRequest *promoterv1alpha1.PullRequest
			var typeNamespacedName types.NamespacedName

			BeforeEach(func() {
				By("Creating the resource hierarchy")
				name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "finalizer-test-gitrepo")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

				By("Waiting for PullRequest to be ready")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				}, constants.EventuallyTimeout)
			})

			It("should prevent deletion of GitRepository while PullRequest exists", func() {
				By("Attempting to delete GitRepository while PullRequest exists")
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())

				By("Verifying GitRepository is not deleted while PullRequest exists")
				Consistently(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, gitRepo)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(gitRepo.DeletionTimestamp).ToNot(BeNil())
				}, "5s", "1s").Should(Succeed())

				By("Deleting the PullRequest")
				Expect(k8sClient.Delete(ctx, pullRequest)).To(Succeed())

				By("Verifying PullRequest is deleted")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
					g.Expect(err).To(HaveOccurred())
					g.Expect(err.Error()).To(ContainSubstring("not found"))
				}, constants.EventuallyTimeout)

				By("Verifying GitRepository is now deleted")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, gitRepo)
					g.Expect(err).To(HaveOccurred())
					g.Expect(err.Error()).To(ContainSubstring("not found"))
				}, constants.EventuallyTimeout)
			})
		})

		Context("When GitRepository depends on ScmProvider", func() {
			var name string
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			var typeNamespacedName types.NamespacedName

			BeforeEach(func() {
				By("Creating the resource hierarchy")
				name, scmSecret, scmProvider, gitRepo, _ = pullRequestResources(ctx, "finalizer-test-scmprovider")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			})

			It("should prevent deletion of ScmProvider while GitRepository exists", func() {
				By("Attempting to delete ScmProvider while GitRepository exists")
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())

				By("Verifying ScmProvider is not deleted while GitRepository exists")
				Consistently(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, scmProvider)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(scmProvider.DeletionTimestamp).ToNot(BeNil())
				}, "5s", "1s").Should(Succeed())

				By("Deleting the GitRepository")
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())

				By("Verifying GitRepository is deleted")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, gitRepo)
					g.Expect(err).To(HaveOccurred())
					g.Expect(err.Error()).To(ContainSubstring("not found"))
				}, constants.EventuallyTimeout)

				By("Verifying ScmProvider is now deleted")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, scmProvider)
					g.Expect(err).To(HaveOccurred())
					g.Expect(err.Error()).To(ContainSubstring("not found"))
				}, constants.EventuallyTimeout)
			})
		})

		Context("When ScmProvider manages Secret finalizer", func() {
			var name string
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var typeNamespacedName types.NamespacedName

			BeforeEach(func() {
				By("Creating the resource hierarchy")
				name, scmSecret, scmProvider, _, _ = pullRequestResources(ctx, "finalizer-test-secret")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			})

			AfterEach(func() {
				By("Cleaning up Secret")
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			})

			It("should add finalizer to Secret when ScmProvider is created", func() {
				By("Waiting for ScmProvider to add finalizer to Secret")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, scmSecret)).To(Succeed())
					g.Expect(scmSecret.Finalizers).To(ContainElement(promoterv1alpha1.ScmProviderSecretFinalizer))
				}, constants.EventuallyTimeout)

				By("Verifying ScmProvider has its own finalizer")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, scmProvider)).To(Succeed())
					g.Expect(scmProvider.Finalizers).To(ContainElement(promoterv1alpha1.ScmProviderFinalizer))
				}, constants.EventuallyTimeout)

				By("Deleting the ScmProvider")
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())

				By("Verifying ScmProvider is deleted and Secret finalizer is removed")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, scmProvider)
					g.Expect(err).To(HaveOccurred())
					g.Expect(err.Error()).To(ContainSubstring("not found"))
				}, constants.EventuallyTimeout)

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, scmSecret)).To(Succeed())
					g.Expect(scmSecret.Finalizers).ToNot(ContainElement(promoterv1alpha1.ScmProviderSecretFinalizer))
				}, constants.EventuallyTimeout)
			})
		})

		Context("When deleting entire resource hierarchy", func() {
			var name string
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			var pullRequest *promoterv1alpha1.PullRequest
			var typeNamespacedName types.NamespacedName

			BeforeEach(func() {
				By("Creating the complete resource hierarchy")
				name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "finalizer-test-complete")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

				By("Waiting for finalizers to be added")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, scmSecret)).To(Succeed())
					g.Expect(scmSecret.Finalizers).To(ContainElement(promoterv1alpha1.ScmProviderSecretFinalizer))
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, scmProvider)).To(Succeed())
					g.Expect(scmProvider.Finalizers).To(ContainElement(promoterv1alpha1.ScmProviderFinalizer))
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, gitRepo)).To(Succeed())
					g.Expect(gitRepo.Finalizers).To(ContainElement(promoterv1alpha1.GitRepositoryFinalizer))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Waiting for PullRequest to be ready")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				}, constants.EventuallyTimeout)
			})

			It("should allow deletion of entire resource hierarchy when deleting from top down", func() {
				By("Deleting from top down: PullRequest, GitRepository, ScmProvider, Secret")
				Expect(k8sClient.Delete(ctx, pullRequest)).To(Succeed())
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
					g.Expect(err).To(HaveOccurred())
				}, constants.EventuallyTimeout)

				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, gitRepo)
					g.Expect(err).To(HaveOccurred())
				}, constants.EventuallyTimeout)

				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, scmProvider)
					g.Expect(err).To(HaveOccurred())
				}, constants.EventuallyTimeout)

				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, scmSecret)
					g.Expect(err).To(HaveOccurred())
				}, constants.EventuallyTimeout)
			})
		})

		Context("When merge persists status before deletion", func() {
			var name string
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			var pullRequest *promoterv1alpha1.PullRequest
			var typeNamespacedName types.NamespacedName
			var mergeSha string

			BeforeEach(func() {
				By("Creating test resources with branches that exist in test setup")
				name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "status-persist-merge-test")

				// Override branches to use ones that exist in the test git server setup
				pullRequest.Spec.TargetBranch = testBranchDevelopment
				pullRequest.Spec.SourceBranch = testBranchDevelopmentNext

				// Get the actual SHA of the source branch to use as mergeSha
				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

				By("Waiting for PullRequest to be open and getting actual merge SHA")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
					g.Expect(pullRequest.Status.ID).ToNot(BeEmpty())
				}, constants.EventuallyTimeout)

				// Get the actual SHA of the source branch for the merge
				mergeSha = getGitBranchSHA(ctx, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, pullRequest.Spec.SourceBranch)
			})

			It("should persist merged status before deletion via defer", func() {
				// Start polling for merged status in a goroutine BEFORE we request the merge.
				// We poll very frequently (1ms) to catch the narrow window where:
				//   1. Status has been persisted as "merged"
				//   2. But PR hasn't been deleted yet
				// This proves the two-step process works correctly.
				mergedStatusObserved := make(chan bool, 1)
				stopPolling := make(chan bool)

				go func() {
					defer GinkgoRecover()
					ticker := time.NewTicker(1 * time.Millisecond)
					defer ticker.Stop()
					timeout := time.After(constants.EventuallyTimeout)
					for {
						select {
						case <-ticker.C:
							var currentPR promoterv1alpha1.PullRequest
							err := k8sClient.Get(ctx, typeNamespacedName, &currentPR)
							if err == nil && currentPR.Status.State == promoterv1alpha1.PullRequestMerged {
								// Success! We observed merged state while PR still exists
								GinkgoT().Logf("Observed merged status at resourceVersion %s", currentPR.ResourceVersion)
								mergedStatusObserved <- true
								return
							}
						case <-stopPolling:
							return
						case <-timeout:
							return
						}
					}
				}()

				By("Requesting merge by setting spec.state to merged with correct SHA")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					pullRequest.Spec.MergeSha = mergeSha
					pullRequest.Spec.State = promoterv1alpha1.PullRequestMerged
					g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Verifying status.state was observed as merged WHILE PR still existed")
				// This is the critical assertion: we MUST have observed status.state = merged
				// with the PR resource still present in the cluster. This proves:
				// 1. The merge reconciliation updated status in memory
				// 2. The deferred HandleReconciliationResult persisted it to etcd
				// 3. The PR was NOT deleted in that same reconciliation (done=true caused requeue)
				// 4. Our polling goroutine caught the state between persist and delete
				// If the old code (inline delete) were active, we'd never observe this state
				// because the PR would be deleted before the status could be persisted.
				Eventually(mergedStatusObserved, constants.EventuallyTimeout).Should(Receive(Equal(true)),
					"Should have observed merged status before deletion")

				close(stopPolling)

				By("Verifying the PullRequest is then deleted on next reconciliation")
				// Now that we've proven the status was persisted, the NEXT reconciliation
				// should see status.state = merged in cleanupTerminalStates and delete it.
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
					g.Expect(err).To(HaveOccurred())
					g.Expect(err.Error()).To(ContainSubstring("pullrequests.promoter.argoproj.io \"" + name + "\" not found"))
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})
	})

	Context("When a PullRequest is externally merged or closed", func() {
		var ctx context.Context
		var name string
		var scmSecret *v1.Secret
		var scmProvider *promoterv1alpha1.ScmProvider
		var gitRepo *promoterv1alpha1.GitRepository
		var pullRequest *promoterv1alpha1.PullRequest
		var typeNamespacedName types.NamespacedName

		BeforeEach(func() {
			ctx = context.Background()

			By("Creating test resources")
			name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "externally-merged-closed")

			typeNamespacedName = types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

			By("Waiting for PullRequest to be open")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				g.Expect(pullRequest.Status.ID).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should set ExternallyMergedOrClosed and delete the PR when not found on provider", func() {
			By("Simulating external deletion by removing PR from fake provider")
			// Get the fake provider and delete the PR from its internal map
			// This simulates the PR being merged/closed externally on the SCM provider
			fakeProvider := fake.NewFakePullRequestProvider(k8sClient)
			Expect(fakeProvider.DeletePullRequest(ctx, *pullRequest)).To(Succeed())

			By("Triggering reconciliation by updating the PR spec")
			// Update the spec to trigger reconciliation (controller uses GenerationChangedPredicate)
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				orig := pullRequest.DeepCopy()
				// Change description to trigger generation change
				pullRequest.Spec.Description = pullRequest.Spec.Description + " "
				g.Expect(k8sClient.Patch(ctx, pullRequest, client.MergeFrom(orig))).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Checking if PR has owner references to verify propagation to CTP and PS")
			// If the PR is owned by a CTP, verify that ExternallyMergedOrClosed propagates
			var ctp *promoterv1alpha1.ChangeTransferPolicy
			var promotionStrategy *promoterv1alpha1.PromotionStrategy
			if len(pullRequest.OwnerReferences) > 0 {
				ownerRef := pullRequest.OwnerReferences[0]
				if ownerRef.Kind == "ChangeTransferPolicy" {
					ctp = &promoterv1alpha1.ChangeTransferPolicy{}
					ctpName := types.NamespacedName{
						Name:      ownerRef.Name,
						Namespace: pullRequest.Namespace,
					}
					// Check CTP status before PR is deleted
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(ctx, ctpName, ctp)).To(Succeed())
						if ctp.Status.PullRequest != nil {
							g.Expect(ctp.Status.PullRequest.ExternallyMergedOrClosed).ToNot(BeNil())
							g.Expect(*ctp.Status.PullRequest.ExternallyMergedOrClosed).To(BeTrue())
						}
					}, constants.EventuallyTimeout).Should(Succeed())

					// Check PromotionStrategy status if CTP has owner references
					if len(ctp.OwnerReferences) > 0 {
						psOwnerRef := ctp.OwnerReferences[0]
						if psOwnerRef.Kind == "PromotionStrategy" {
							promotionStrategy = &promoterv1alpha1.PromotionStrategy{}
							psName := types.NamespacedName{
								Name:      psOwnerRef.Name,
								Namespace: ctp.Namespace,
							}
							Eventually(func(g Gomega) {
								g.Expect(k8sClient.Get(ctx, psName, promotionStrategy)).To(Succeed())
								// Find the environment that matches this CTP's active branch
								for _, envStatus := range promotionStrategy.Status.Environments {
									if envStatus.Branch == ctp.Spec.ActiveBranch && envStatus.PullRequest != nil {
										g.Expect(envStatus.PullRequest.ExternallyMergedOrClosed).ToNot(BeNil())
										g.Expect(*envStatus.PullRequest.ExternallyMergedOrClosed).To(BeTrue())
										return
									}
								}
								g.Expect(false).To(BeTrue(), "Could not find matching environment status in PromotionStrategy")
							}, constants.EventuallyTimeout).Should(Succeed())
						}
					}
				}
			}

			By("Verifying the PullRequest is deleted by cleanupTerminalStates after ExternallyMergedOrClosed is set")
			// The PR will be deleted when ExternallyMergedOrClosed is set to true and cleanupTerminalStates runs.
			// We verify deletion instead of checking the status field directly because the PR gets deleted
			// in the same reconciliation cycle, making it impossible to observe the status field.
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("pullrequests.promoter.argoproj.io \"" + name + "\" not found"))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying CTP status preserves ExternallyMergedOrClosed even after PR deletion")
			// After the PR is deleted, the CTP should still maintain the ExternallyMergedOrClosed state
			// This allows the CTP to keep a record of what happened to the PR
			if ctp != nil {
				ctpName := types.NamespacedName{
					Name:      pullRequest.OwnerReferences[0].Name,
					Namespace: pullRequest.Namespace,
				}

				// Trigger CTP reconciliation using the channel-based enqueue function
				enqueueCTP(ctpName.Namespace, ctpName.Name)

				// Verify CTP status preserved the ExternallyMergedOrClosed flag
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, ctpName, ctp)).To(Succeed())
					g.Expect(ctp.Status.PullRequest).ToNot(BeNil(), "CTP should preserve PR status after PR deletion")
					g.Expect(ctp.Status.PullRequest.ExternallyMergedOrClosed).ToNot(BeNil())
					g.Expect(*ctp.Status.PullRequest.ExternallyMergedOrClosed).To(BeTrue(), "ExternallyMergedOrClosed should be preserved in CTP status")
					g.Expect(ctp.Status.PullRequest.State).To(BeEmpty(), "State should be empty when externally merged/closed (we don't know if merged or closed)")
				}, constants.EventuallyTimeout).Should(Succeed())
			}
		})
	})
})

func pullRequestResources(ctx context.Context, name string) (string, *v1.Secret, *promoterv1alpha1.ScmProvider, *promoterv1alpha1.GitRepository, *promoterv1alpha1.PullRequest) {
	name = name + "-" + utils.KubeSafeUniqueName(ctx, randomString(15))
	setupInitialTestGitRepoOnServer(ctx, name, name)

	scmSecret := &v1.Secret{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Data: nil,
	}

	scmProvider := &promoterv1alpha1.ScmProvider{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
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
			Namespace: "default",
		},
		Spec: promoterv1alpha1.GitRepositorySpec{
			Fake: &promoterv1alpha1.FakeRepo{
				Owner: name,
				Name:  name,
			},
			ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
				Kind: promoterv1alpha1.ScmProviderKind,
				Name: name,
			},
		},
	}

	pullRequest := &promoterv1alpha1.PullRequest{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Spec: promoterv1alpha1.PullRequestSpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: name,
			},
			Title:        "Initial Title",
			TargetBranch: "development",
			SourceBranch: "development-next",
			Description:  "Initial Description",
			MergeSha:     "abc123def456789012345678901234567890abcd", // Dummy SHA for testing
			State:        "open",
		},
		Status: promoterv1alpha1.PullRequestStatus{},
	}

	return name, scmSecret, scmProvider, gitRepo, pullRequest
}

func getGitBranchSHA(ctx context.Context, owner, name, branch string) string {
	gitServerPort := 5000 + GinkgoParallelProcess()
	repoURL := fmt.Sprintf("http://localhost:%d/%s/%s", gitServerPort, owner, name)

	output, err := runGitCmd(ctx, "", "ls-remote", repoURL, "refs/heads/"+branch)
	Expect(err).NotTo(HaveOccurred())

	// Output format: "<sha>\trefs/heads/<branch>"
	parts := strings.Fields(output)
	Expect(parts).To(HaveLen(2), "Expected ls-remote output to have 2 fields")

	return strings.TrimSpace(parts[0])
}
