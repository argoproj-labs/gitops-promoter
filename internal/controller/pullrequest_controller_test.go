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
	"slices"
	"strings"
	"time"

	"github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"k8s.io/apimachinery/pkg/api/meta"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

//go:embed testdata/PullRequest.yaml
var testPullRequestYAML string

var _ = Describe("PullRequest Controller", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Context("When unmarshalling the test data", func() {
		It("should unmarshal the PullRequest resource", func() {
			err := unmarshalYamlStrict(testPullRequestYAML, &promoterv1alpha1.PullRequest{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("When reconciling a resource", func() {
		var name string
		var scmSecret *v1.Secret
		var scmProvider *promoterv1alpha1.ScmProvider
		var gitRepo *promoterv1alpha1.GitRepository
		var pullRequest *promoterv1alpha1.PullRequest
		var typeNamespacedName types.NamespacedName

		Context("When updating title then merging", func() {
			BeforeEach(func() {
				By("Creating test resources")
				name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "update-title-merge")

				// Override branches to use ones that exist in the test git server setup
				pullRequest.Spec.TargetBranch = testBranchDevelopment
				pullRequest.Spec.SourceBranch = testBranchDevelopmentNext

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

			It("should successfully reconcile the resource when updating title then merging", func() {
				By("Reconciling updating of the PullRequest")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					pullRequest.Spec.Title = "Updated Title"
					g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					g.Expect(pullRequest.Spec.Title).To(Equal("Updated Title"))
					readyCond := meta.FindStatusCondition(pullRequest.Status.Conditions, string(conditions.Ready))
					g.Expect(readyCond).NotTo(BeNil())
					g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
					g.Expect(readyCond.ObservedGeneration).To(Equal(pullRequest.Generation))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Reconciling merging of the PullRequest")
				mergeSha := getGitBranchSHA(ctx, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, pullRequest.Spec.SourceBranch)
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					pullRequest.Spec.MergeSha = mergeSha
					pullRequest.Spec.State = promoterv1alpha1.PullRequestMerged
					g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
					g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		Context("When the PR is already open and nothing has changed", func() {
			BeforeEach(func() {
				// Must be set BEFORE the PR's first reconcile: controller-runtime's workqueue
				// only applies a new RequeueAfter on each Reconcile's own return value, so a CC
				// patch made after the first (5m-default) requeue is already scheduled has no
				// effect until that 5-minute timer expires.
				By("Shortening the PullRequest requeue duration so periodic reconciles happen quickly")
				setPullRequestRequeueDuration(ctx, 200*time.Millisecond)

				By("Creating test resources")
				name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "steady-state-no-update")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

				By("Waiting for PullRequest to be open and successfully reconciled at least once")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
					g.Expect(pullRequest.Status.ID).NotTo(BeEmpty())
					readyCond := meta.FindStatusCondition(pullRequest.Status.Conditions, string(conditions.Ready))
					g.Expect(readyCond).NotTo(BeNil())
					g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
					g.Expect(readyCond.ObservedGeneration).To(Equal(pullRequest.Generation))
				}, constants.EventuallyTimeout).Should(Succeed())
			})

			AfterEach(func() {
				Expect(k8sClient.Delete(ctx, pullRequest)).To(Succeed())
			})

			It("should not repeat SCM Update calls on every periodic requeue once synced", func() {
				fake.ResetPullRequestSCMCallCounts()

				By("Waiting for several periodic reconciles to occur (proven via FindOpen, which must always run)")
				Eventually(func(g Gomega) {
					g.Expect(fake.FindOpenCallCount()).To(BeNumerically(">=", 3))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Verifying Update was not repeated on any of those periodic reconciles")
				// With the fix, a PR that is already open with no spec change since the last
				// successful sync produces 0 Update calls no matter how many periodic
				// reconciles occur. Without it, Update fires once per periodic reconcile.
				Expect(fake.UpdateCallCount()).To(BeZero(),
					"Update should not be called again for a generation that was already successfully synced")
			})
		})

		Context("When SCM-irrelevant spec changes occur on an open PR", func() {
			BeforeEach(func() {
				// Keep periodic reconciles out of the Consistently window below.
				setPullRequestRequeueDuration(ctx, time.Hour)

				By("Creating test resources")
				name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "scm-irrelevant-spec")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
					g.Expect(pullRequest.Status.ID).NotTo(BeEmpty())
					readyCond := meta.FindStatusCondition(pullRequest.Status.Conditions, string(conditions.Ready))
					g.Expect(readyCond).NotTo(BeNil())
					g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
					g.Expect(readyCond.ObservedGeneration).To(Equal(pullRequest.Generation))
				}, constants.EventuallyTimeout).Should(Succeed())
			})

			AfterEach(func() {
				Expect(k8sClient.Delete(ctx, pullRequest)).To(Succeed())
			})

			It("should not hit SCM when only spec.commit.message changes", func() {
				fake.ResetPullRequestSCMCallCounts()

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					pullRequest.Spec.Commit.Message = "updated merge commit message with trailers"
					g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				Consistently(func(g Gomega) {
					g.Expect(fake.FindOpenCallCount()).To(BeZero())
					g.Expect(fake.UpdateCallCount()).To(BeZero())
					g.Expect(fake.PullRequestSCMCallCount()).To(BeZero())
				}, 3*time.Second, 100*time.Millisecond).Should(Succeed())

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					readyCond := meta.FindStatusCondition(pullRequest.Status.Conditions, string(conditions.Ready))
					g.Expect(readyCond).NotTo(BeNil())
					g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
					g.Expect(readyCond.ObservedGeneration).To(Equal(pullRequest.Generation))
				}, constants.EventuallyTimeout).Should(Succeed())
			})

			It("should not hit SCM when only spec.mergeSha changes while open", func() {
				fake.ResetPullRequestSCMCallCounts()

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					pullRequest.Spec.MergeSha = "fedcba9876543210fedcba9876543210fedcba98"
					g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				Consistently(func(g Gomega) {
					g.Expect(fake.FindOpenCallCount()).To(BeZero())
					g.Expect(fake.UpdateCallCount()).To(BeZero())
					g.Expect(fake.PullRequestSCMCallCount()).To(BeZero())
				}, 3*time.Second, 100*time.Millisecond).Should(Succeed())

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					readyCond := meta.FindStatusCondition(pullRequest.Status.Conditions, string(conditions.Ready))
					g.Expect(readyCond).NotTo(BeNil())
					g.Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
					g.Expect(readyCond.ObservedGeneration).To(Equal(pullRequest.Generation))
				}, constants.EventuallyTimeout).Should(Succeed())
			})

			It("should still hit SCM when spec.title changes", func() {
				fake.ResetPullRequestSCMCallCounts()
				baselineFindOpen := fake.FindOpenCallCount()
				baselineUpdate := fake.UpdateCallCount()

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					pullRequest.Spec.Title = pullRequest.Spec.Title + "-updated"
					g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					g.Expect(fake.FindOpenCallCount()).To(BeNumerically(">", baselineFindOpen))
					g.Expect(fake.UpdateCallCount()).To(BeNumerically(">", baselineUpdate))
				}, constants.EventuallyTimeout).Should(Succeed())
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

				By("Verifying a PullRequestClosed event was emitted")
				Eventually(func(g Gomega) {
					var eventList v1.EventList
					g.Expect(k8sClient.List(ctx, &eventList, client.InNamespace("default"))).To(Succeed())
					g.Expect(hasEventWithReason(eventList, name, constants.PullRequestClosedReason)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})
	})

	Context("When reconciling a resource with a bad configuration", func() {
		var name string
		var scmSecret *v1.Secret
		var scmProvider *promoterv1alpha1.ScmProvider
		var gitRepo *promoterv1alpha1.GitRepository
		var pullRequest *promoterv1alpha1.PullRequest
		var typeNamespacedName types.NamespacedName

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

				By("Verifying a PullRequestMergeFailed event was emitted")
				Eventually(func(g Gomega) {
					var eventList v1.EventList
					g.Expect(k8sClient.List(ctx, &eventList, client.InNamespace("default"))).To(Succeed())
					g.Expect(hasEventWithReason(eventList, name, constants.PullRequestMergeFailedReason)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})
	})

	Context("When attempting to create a PullRequest with invalid initial state", func() {
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

	Context("When status.mergedTargetSha is already recorded", func() {
		// These specs exercise CRD CEL validation only, so they run against the controller-free dev
		// envtest cluster. On the main cluster the PullRequest controller would reconcile a merged
		// PullRequest and delete it out from under the assertions.
		const firstSha = "1111111111111111111111111111111111111111"
		const secondSha = "2222222222222222222222222222222222222222"
		var mergedPR *promoterv1alpha1.PullRequest

		BeforeEach(func() {
			prName := utils.KubeSafeUniqueName("merged-target-sha-" + randomString(15))
			mergedPR = &promoterv1alpha1.PullRequest{
				ObjectMeta: metav1.ObjectMeta{Name: prName, Namespace: "default"},
				Spec: promoterv1alpha1.PullRequestSpec{
					RepositoryReference: promoterv1alpha1.ObjectReference{Name: prName},
					Title:               "Initial Title",
					TargetBranch:        "development",
					SourceBranch:        "development-next",
					MergeSha:            "abc123def456789012345678901234567890abcd",
					State:               promoterv1alpha1.PullRequestOpen,
				},
			}
			Expect(k8sClientDev.Create(ctx, mergedPR)).To(Succeed())
			DeferCleanup(func() { _ = k8sClientDev.Delete(ctx, mergedPR) })

			mergedPR.Status.ID = "1"
			mergedPR.Status.State = promoterv1alpha1.PullRequestMerged
			mergedPR.Status.MergedTargetSha = firstSha
			Expect(k8sClientDev.Status().Update(ctx, mergedPR)).To(Succeed())
		})

		It("should reject replacing it with a different SHA", func() {
			mergedPR.Status.MergedTargetSha = secondSha
			err := k8sClientDev.Status().Update(ctx, mergedPR)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("mergedTargetSha is immutable once set"))
		})

		It("should allow a status write that carries the same SHA", func() {
			mergedPR.Status.Url = "https://example.com/pr/1"
			Expect(k8sClientDev.Status().Update(ctx, mergedPR)).To(Succeed())
		})

		It("should reject clearing it", func() {
			mergedPR.Status.MergedTargetSha = ""
			err := k8sClientDev.Status().Update(ctx, mergedPR)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("mergedTargetSha is immutable once set"))
		})
	})

	Context("When deleting a PullRequest that never created a PR on SCM", func() {
		var name string
		var scmSecret *v1.Secret
		var scmProvider *promoterv1alpha1.ScmProvider
		var gitRepo *promoterv1alpha1.GitRepository
		var pullRequest *promoterv1alpha1.PullRequest
		var typeNamespacedName types.NamespacedName

		BeforeEach(func() {
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

				By("Waiting for GitRepository finalizer and PullRequest to be ready")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, gitRepo)).To(Succeed())
					g.Expect(gitRepo.Finalizers).To(ContainElement(promoterv1alpha1.GitRepositoryFinalizer))
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
					g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				}, constants.EventuallyTimeout).Should(Succeed())
			})

			It("should prevent deletion of GitRepository while PullRequest exists", func() {
				By("Attempting to delete GitRepository while PullRequest exists")
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())

				By("Verifying GitRepository is not deleted while PullRequest exists")
				Consistently(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, gitRepo)).To(Succeed(),
						"GitRepository was deleted while a referencing PullRequest still exists")
					g.Expect(gitRepo.DeletionTimestamp).ToNot(BeNil())
					g.Expect(gitRepo.Finalizers).To(ContainElement(promoterv1alpha1.GitRepositoryFinalizer),
						"GitRepository finalizer should remain until all referencing PullRequests are gone")
				}, "5s", "1s").Should(Succeed())

				By("Deleting the PullRequest")
				Expect(k8sClient.Delete(ctx, pullRequest)).To(Succeed())

				By("Verifying PullRequest is deleted")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
					g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Verifying GitRepository is now deleted")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, gitRepo)
					g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
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

				By("Waiting for ScmProvider and GitRepository finalizers")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, scmProvider)).To(Succeed())
					g.Expect(scmProvider.Finalizers).To(ContainElement(promoterv1alpha1.ScmProviderFinalizer))
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, gitRepo)).To(Succeed())
					g.Expect(gitRepo.Finalizers).To(ContainElement(promoterv1alpha1.GitRepositoryFinalizer))
				}, constants.EventuallyTimeout).Should(Succeed())
			})

			It("should prevent deletion of ScmProvider while GitRepository exists", func() {
				By("Attempting to delete ScmProvider while GitRepository exists")
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())

				By("Verifying ScmProvider is not deleted while GitRepository exists")
				Consistently(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, scmProvider)).To(Succeed(),
						"ScmProvider was deleted while a referencing GitRepository still exists")
					g.Expect(scmProvider.DeletionTimestamp).ToNot(BeNil())
					g.Expect(scmProvider.Finalizers).To(ContainElement(promoterv1alpha1.ScmProviderFinalizer),
						"ScmProvider finalizer should remain until all referencing GitRepositories are gone")
				}, "5s", "1s").Should(Succeed())

				By("Deleting the GitRepository")
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())

				By("Verifying GitRepository is deleted")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, gitRepo)
					g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Verifying ScmProvider is now deleted")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, scmProvider)
					g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
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
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Verifying ScmProvider has its own finalizer")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, scmProvider)).To(Succeed())
					g.Expect(scmProvider.Finalizers).To(ContainElement(promoterv1alpha1.ScmProviderFinalizer))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Deleting the ScmProvider")
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())

				By("Verifying ScmProvider is deleted and Secret finalizer is removed")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, scmProvider)
					g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, scmSecret)).To(Succeed())
					g.Expect(scmSecret.Finalizers).ToNot(ContainElement(promoterv1alpha1.ScmProviderSecretFinalizer))
				}, constants.EventuallyTimeout).Should(Succeed())
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
				}, constants.EventuallyTimeout).Should(Succeed())
			})

			It("should allow deletion of entire resource hierarchy when deleting from top down", func() {
				By("Deleting from top down: PullRequest, GitRepository, ScmProvider, Secret")
				Expect(k8sClient.Delete(ctx, pullRequest)).To(Succeed())
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
					g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())

				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, gitRepo)
					g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())

				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, scmProvider)
					g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())

				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, scmSecret)
					g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
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
				}, constants.EventuallyTimeout).Should(Succeed())

				// Get the actual SHA of the source branch for the merge
				mergeSha = getGitBranchSHA(ctx, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, pullRequest.Spec.SourceBranch)
			})

			It("should persist merged status and the SCM-reported merged target sha before deletion via defer", func() {
				// Start polling for merged status in a goroutine BEFORE we request the merge.
				// We poll very frequently (1ms) to catch the narrow window where:
				//   1. Status has been persisted as "merged"
				//   2. But PR hasn't been deleted yet
				// This proves the two-step process works correctly.
				mergedStatusObserved := make(chan promoterv1alpha1.PullRequestStatus, 1)
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
								mergedStatusObserved <- currentPR.Status
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
				var observedStatus promoterv1alpha1.PullRequestStatus
				Eventually(mergedStatusObserved, constants.EventuallyTimeout).Should(Receive(&observedStatus),
					"Should have observed merged status before deletion")

				close(stopPolling)

				By("Verifying the merged target sha from the merge response was persisted alongside the merged state")
				// The fake provider reports the resulting target-branch commit in its merge response, so the
				// sha must land in the same status write as state=merged rather than waiting for a Get-by-ID recovery.
				Expect(observedStatus.MergedTargetSha).To(Equal(
					getGitBranchSHA(ctx, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, pullRequest.Spec.TargetBranch)))

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

	Context("When a controller-initiated merge/close already completed on the provider but status was not persisted", func() {
		var name string
		var scmSecret *v1.Secret
		var scmProvider *promoterv1alpha1.ScmProvider
		var gitRepo *promoterv1alpha1.GitRepository
		var pullRequest *promoterv1alpha1.PullRequest
		var typeNamespacedName types.NamespacedName

		BeforeEach(func() {
			By("Creating test resources")
			name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "lost-status-recovery")

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

		It("should recover mergedTargetSha via Get-by-ID before deleting when the merge response omitted it", func() {
			mergedTargetSha := pullRequest.Spec.MergeSha

			By("Marking the PR merged on the fake SCM without going through the merge API (async providers omit CommitSHA from Merge)")
			fakeProvider := fake.NewFakePullRequestProvider(k8sClient)
			Expect(fakeProvider.MarkMergedExternally(ctx, *pullRequest, mergedTargetSha)).To(Succeed())

			By("Setting spec.state to merged while status still reflects the pre-sync open state")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				pullRequest.Spec.State = promoterv1alpha1.PullRequestMerged
				g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			triggerPRReconcile(ctx, typeNamespacedName, pullRequest)

			By("Verifying Get-by-ID records mergedTargetSha before the PullRequest is deleted")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestMerged))
				g.Expect(pullRequest.Status.MergedTargetSha).To(Equal(mergedTargetSha))
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should recover and delete the PR when spec.state=closed but the PR is already gone from the provider", func() {
			By("Removing the PR from the fake provider to simulate it was already closed on the SCM (e.g. close happened but status update was lost)")
			fakeProvider := fake.NewFakePullRequestProvider(k8sClient)
			Expect(fakeProvider.DeletePullRequest(ctx, *pullRequest)).To(Succeed())

			By("Setting spec.state to closed (as the CTP controller would have done)")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				pullRequest.Spec.State = promoterv1alpha1.PullRequestClosed
				g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying the PullRequest is cleaned up (not stuck in an error loop trying to re-close)")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("pullrequests.promoter.argoproj.io \"" + name + "\" not found"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When a PullRequest is externally merged or closed", func() {
		var name string
		var scmSecret *v1.Secret
		var scmProvider *promoterv1alpha1.ScmProvider
		var gitRepo *promoterv1alpha1.GitRepository
		var pullRequest *promoterv1alpha1.PullRequest
		var typeNamespacedName types.NamespacedName

		BeforeEach(func() {
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
			triggerPRReconcile(ctx, typeNamespacedName, pullRequest)

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

			By("Verifying a PullRequestExternallyMergedOrClosed event was emitted")
			Eventually(func(g Gomega) {
				var eventList v1.EventList
				g.Expect(k8sClient.List(ctx, &eventList, client.InNamespace("default"))).To(Succeed())
				g.Expect(hasEventWithReason(eventList, name, constants.PullRequestExternallyMergedOrClosedReason)).To(BeTrue())
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

		It("should keep the PullRequest when FindOpen misses but Get confirms it is still open", func() {
			By("Simulating SCM list lag: Get-by-ID finds the PR open but FindOpen does not")
			fakeProvider := fake.NewFakePullRequestProvider(k8sClient)
			Expect(fakeProvider.SetHideFromFindOpen(ctx, *pullRequest, true)).To(Succeed())

			By("Triggering reconciliation by updating the PR spec")
			triggerPRReconcile(ctx, typeNamespacedName, pullRequest)

			By("Verifying the PullRequest is not deleted or marked externally merged/closed")
			Consistently(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				if pullRequest.Status.ExternallyMergedOrClosed != nil {
					g.Expect(*pullRequest.Status.ExternallyMergedOrClosed).To(BeFalse())
				}
			}, "5s", "500ms").Should(Succeed())
		})

		It("should clear a stale ExternallyMergedOrClosed and keep the PullRequest when FindOpen still lists it open", func() {
			By("Recording ExternallyMergedOrClosed while the PR is in fact still open on the SCM")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				pullRequest.Status.ExternallyMergedOrClosed = new(true)
				pullRequest.Status.State = ""
				g.Expect(k8sClient.Status().Update(ctx, pullRequest)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Triggering reconciliation by updating the PR spec")
			triggerPRReconcile(ctx, typeNamespacedName, pullRequest)

			By("Verifying the flag is retracted and the PullRequest is not cleaned up as terminal")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				if pullRequest.Status.ExternallyMergedOrClosed != nil {
					g.Expect(*pullRequest.Status.ExternallyMergedOrClosed).To(BeFalse())
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			Consistently(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.DeletionTimestamp.IsZero()).To(BeTrue())
			}, "3s", "250ms").Should(Succeed())
		})

		It("should set state merged and mergedTargetSha when externally merged on provider", func() {
			mergedTargetSha := pullRequest.Spec.MergeSha

			// The reconcile that learns about the external merge persists status and the NEXT one deletes the
			// object, so the merged status is only observable in a narrow window. Poll for it from a goroutine
			// started before the merge, the same way the controller-initiated merge spec above does.
			mergedStatusObserved := make(chan promoterv1alpha1.PullRequestStatus, 1)
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
							mergedStatusObserved <- currentPR.Status
							return
						}
					case <-stopPolling:
						return
					case <-timeout:
						return
					}
				}
			}()

			By("Simulating external merge on the fake SCM")
			fakeProvider := fake.NewFakePullRequestProvider(k8sClient)
			Expect(fakeProvider.MarkMergedExternally(ctx, *pullRequest, mergedTargetSha)).To(Succeed())

			By("Triggering reconciliation by updating the PR spec")
			triggerPRReconcile(ctx, typeNamespacedName, pullRequest)

			By("Verifying state merged and the SCM-reported mergedTargetSha were persisted before deletion")
			var observedStatus promoterv1alpha1.PullRequestStatus
			Eventually(mergedStatusObserved, constants.EventuallyTimeout).Should(Receive(&observedStatus),
				"Should have observed merged status before deletion")

			close(stopPolling)

			// A Get-by-ID that reports the PR merged is authoritative, so the sha is recorded and the
			// ambiguous externallyMergedOrClosed flag is left unset.
			Expect(observedStatus.MergedTargetSha).To(Equal(mergedTargetSha))
			Expect(observedStatus.ExternallyMergedOrClosed).To(BeNil())

			By("Verifying the PullRequest is deleted after mergedTargetSha is persisted")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("pullrequests.promoter.argoproj.io \"" + name + "\" not found"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should record the merge sha when the resource is deleted before the external merge is observed", func() {
			// The deletion reconcile is the first one to run after the external merge, so the merge sha is
			// only recoverable if deletion finalization asks the SCM before releasing the finalizer.
			mergedTargetSha := pullRequest.Spec.MergeSha

			// The resource disappears as soon as finalization completes, so sample from a goroutine started
			// before the delete rather than polling after it.
			mergedStatusObserved := make(chan promoterv1alpha1.PullRequestStatus, 1)
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
							mergedStatusObserved <- currentPR.Status
							return
						}
					case <-stopPolling:
						return
					case <-timeout:
						return
					}
				}
			}()

			By("Simulating external merge on the fake SCM without letting the controller observe it")
			fakeProvider := fake.NewFakePullRequestProvider(k8sClient)
			Expect(fakeProvider.MarkMergedExternally(ctx, *pullRequest, mergedTargetSha)).To(Succeed())

			By("Deleting the PullRequest resource, with no other finalizer holding it")
			Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pullRequest)).To(Succeed())

			By("Verifying deletion finalization recorded the merged state and sha before releasing the object")
			var observedStatus promoterv1alpha1.PullRequestStatus
			Eventually(mergedStatusObserved, constants.EventuallyTimeout).Should(Receive(&observedStatus),
				"deletion finalization must learn the terminal SCM outcome before the resource goes away")

			close(stopPolling)

			Expect(observedStatus.MergedTargetSha).To(Equal(mergedTargetSha))
			Expect(observedStatus.ExternallyMergedOrClosed).To(BeNil())

			By("Verifying the PullRequest is then deleted")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When deleting a PullRequest that already has an SCM PR but is blocked by another finalizer", func() {
		const blockingFinalizer = "promoter.argoproj.io/test-will-not-remove"

		var name string
		var scmSecret *v1.Secret
		var scmProvider *promoterv1alpha1.ScmProvider
		var gitRepo *promoterv1alpha1.GitRepository
		var pullRequest *promoterv1alpha1.PullRequest
		var typeNamespacedName types.NamespacedName

		BeforeEach(func() {
			name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "delete-blocked-finalizer")

			typeNamespacedName = types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				g.Expect(pullRequest.Status.ID).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Finalizers).To(ContainElement(promoterv1alpha1.PullRequestFinalizer))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Adding a finalizer that will not be removed, simulating another controller retaining the object")
			Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
			base := pullRequest.DeepCopy()
			pullRequest.Finalizers = append(pullRequest.Finalizers, blockingFinalizer)
			Expect(k8sClient.Patch(ctx, pullRequest, client.MergeFrom(base))).To(Succeed())
		})

		AfterEach(func() {
			var pr promoterv1alpha1.PullRequest
			if err := k8sClient.Get(ctx, typeNamespacedName, &pr); err != nil {
				return
			}
			if pr.DeletionTimestamp == nil {
				return
			}
			var kept []string
			for _, f := range pr.Finalizers {
				if f != blockingFinalizer {
					kept = append(kept, f)
				}
			}
			if len(kept) == len(pr.Finalizers) {
				return
			}
			base := pr.DeepCopy()
			pr.Finalizers = kept
			_ = k8sClient.Patch(ctx, &pr, client.MergeFrom(base))
		})

		It("should close the SCM PR, set status closed when status syncs, and not spam FindOpen while terminating", func() {
			fake.ResetFindOpenCallCount()

			// Status must be persisted as closed before the promoter finalizer is released so the
			// owning ChangeTransferPolicy can read the terminal outcome while the object still exists.
			closedStatusObserved := make(chan promoterv1alpha1.PullRequestStatus, 1)
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
						if err == nil &&
							currentPR.Status.State == promoterv1alpha1.PullRequestClosed &&
							controllerutil.ContainsFinalizer(&currentPR, promoterv1alpha1.PullRequestFinalizer) {
							closedStatusObserved <- currentPR.Status
							return
						}
					case <-stopPolling:
						return
					case <-timeout:
						return
					}
				}
			}()

			By("Deleting the PullRequest (object remains until the blocking finalizer is cleared)")
			Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
			Expect(k8sClient.Delete(ctx, pullRequest)).To(Succeed())

			fakeProvider := fake.NewFakePullRequestProvider(k8sClient)

			By("Waiting for closed status to be persisted while the promoter finalizer is still held")
			var observedStatus promoterv1alpha1.PullRequestStatus
			Eventually(closedStatusObserved, constants.EventuallyTimeout).Should(Receive(&observedStatus),
				"closed status should be persisted before the promoter finalizer is released")
			close(stopPolling)
			Expect(observedStatus.ExternallyMergedOrClosed).To(BeNil())

			By("Waiting for the promoter's finalizer to run (SCM close + remove promoter finalizer)")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.DeletionTimestamp).ToNot(BeNil())
				g.Expect(pullRequest.Finalizers).NotTo(ContainElement(promoterv1alpha1.PullRequestFinalizer))
				g.Expect(pullRequest.Finalizers).To(ContainElement(blockingFinalizer))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying the SCM-side PR was closed by deletion finalization logic")
			Eventually(func(g Gomega) {
				exists, state, _, err := fakeProvider.GetRecordedState(ctx, *pullRequest)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(exists).To(BeTrue())
				g.Expect(state).To(Equal(promoterv1alpha1.PullRequestClosed))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Checking the number of FindOpen calls")

			findOpenBeforeBump := fake.FindOpenCallCount()
			time.Sleep(150 * time.Millisecond)

			By("Verifying FindOpen is not invoked in a tight loop while the object is stuck terminating")
			findOpenAfterWindow := fake.FindOpenCallCount()
			Expect(findOpenAfterWindow-findOpenBeforeBump).To(BeNumerically("<", 100),
				"FindOpen should not be polled repeatedly after the SCM PR is closed during deletion")

			snapshot := fake.FindOpenCallCount()
			Consistently(func(g Gomega) {
				// Allow a small bump from a stray requeue; a regression still produces thousands of FindOpen calls.
				g.Expect(fake.FindOpenCallCount()).To(BeNumerically("<=", snapshot+5))
			}, 2*time.Second, 50*time.Millisecond).Should(Succeed())

			By("Verifying status reflects closed after the SCM PR was closed during deletion")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestClosed))
				g.Expect(pullRequest.Status.ExternallyMergedOrClosed).To(BeNil())
			}, constants.EventuallyTimeout).Should(Succeed())

			// Once the promoter finalizer is released this controller is done with the object, even
			// though the blocking finalizer keeps it around and reconciles keep arriving.
			By("Verifying a spec change on the terminating object no longer reaches the SCM")
			afterRelease := fake.FindOpenCallCount()
			Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
			base := pullRequest.DeepCopy()
			pullRequest.Spec.Title = pullRequest.Spec.Title + "-bumped"
			Expect(k8sClient.Patch(ctx, pullRequest, client.MergeFrom(base))).To(Succeed())
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Spec.Title).To(HaveSuffix("-bumped"))
			}, constants.EventuallyTimeout).Should(Succeed())
			Consistently(func(g Gomega) {
				g.Expect(fake.FindOpenCallCount()).To(Equal(afterRelease))
			}, 2*time.Second, 50*time.Millisecond).Should(Succeed())
		})
	})

	Context("When deleting a PullRequest after GitRepository is removed", func() {
		var name string
		var scmSecret *v1.Secret
		var scmProvider *promoterv1alpha1.ScmProvider
		var gitRepo *promoterv1alpha1.GitRepository
		var pullRequest *promoterv1alpha1.PullRequest
		var typeNamespacedName types.NamespacedName

		BeforeEach(func() {
			name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "delete-missing-gitrepo")

			typeNamespacedName = types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				g.Expect(pullRequest.Status.ID).ToNot(BeEmpty())
				g.Expect(pullRequest.Finalizers).To(ContainElement(promoterv1alpha1.PullRequestFinalizer))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		AfterEach(func() {
			var pr promoterv1alpha1.PullRequest
			if err := k8sClient.Get(ctx, typeNamespacedName, &pr); err == nil {
				if pr.DeletionTimestamp != nil && controllerutil.ContainsFinalizer(&pr, promoterv1alpha1.PullRequestFinalizer) {
					base := pr.DeepCopy()
					controllerutil.RemoveFinalizer(&pr, promoterv1alpha1.PullRequestFinalizer)
					_ = k8sClient.Patch(ctx, &pr, client.MergeFrom(base))
				}
				_ = client.IgnoreNotFound(k8sClient.Delete(ctx, &pr))
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, &pr)
					g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
			}

			_ = client.IgnoreNotFound(k8sClient.Delete(ctx, gitRepo))
			_ = client.IgnoreNotFound(k8sClient.Delete(ctx, scmProvider))
			_ = client.IgnoreNotFound(k8sClient.Delete(ctx, scmSecret))
		})

		It("should keep the finalizer and report which dependency is missing", func() {
			By("Removing GitRepository finalizer so the object can be deleted while the PullRequest remains")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, gitRepo)).To(Succeed())
				g.Expect(gitRepo.Finalizers).To(ContainElement(promoterv1alpha1.GitRepositoryFinalizer))
			}, constants.EventuallyTimeout).Should(Succeed())

			Expect(k8sClient.Get(ctx, typeNamespacedName, gitRepo)).To(Succeed())
			base := gitRepo.DeepCopy()
			controllerutil.RemoveFinalizer(gitRepo, promoterv1alpha1.GitRepositoryFinalizer)
			Expect(k8sClient.Patch(ctx, gitRepo, client.MergeFrom(base))).To(Succeed())
			Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, gitRepo)
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Deleting the PullRequest")
			Expect(k8sClient.Delete(ctx, pullRequest)).To(Succeed())

			By("Verifying deletion is blocked with an operator-facing dependency error")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.DeletionTimestamp).ToNot(BeNil())
				g.Expect(pullRequest.Finalizers).To(ContainElement(promoterv1alpha1.PullRequestFinalizer))

				ready := meta.FindStatusCondition(pullRequest.Status.Conditions, string(conditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ready.Reason).To(Equal(string(conditions.ReconciliationError)))

				msg := ready.Message
				g.Expect(msg).To(ContainSubstring("Reconciliation failed"))
				g.Expect(msg).To(ContainSubstring("cannot close its SCM pull request"))
				g.Expect(msg).To(ContainSubstring("promoter.argoproj.io/GitRepository"))
				g.Expect(msg).To(ContainSubstring(name))
				g.Expect(msg).To(ContainSubstring(promoterv1alpha1.PullRequestFinalizer))
			}, constants.EventuallyTimeout).Should(Succeed())

			Consistently(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Finalizers).To(ContainElement(promoterv1alpha1.PullRequestFinalizer))
			}, "3s", "500ms").Should(Succeed())
		})
	})
})

var _ = Describe("pullRequestDeletionFinalizerLengthChangedPredicate", func() {
	pred := pullRequestDeletionFinalizerLengthChangedPredicate()

	It("enqueues when terminating and finalizer count changes", func() {
		now := metav1.Now()
		oldPR := &promoterv1alpha1.PullRequest{
			ObjectMeta: metav1.ObjectMeta{
				Generation:        1,
				DeletionTimestamp: &now,
				Finalizers:        []string{promoterv1alpha1.PullRequestFinalizer},
				ResourceVersion:   "1",
			},
			Spec: promoterv1alpha1.PullRequestSpec{State: promoterv1alpha1.PullRequestOpen},
		}
		newPR := oldPR.DeepCopy()
		newPR.Finalizers = nil
		Expect(pred.Update(event.UpdateEvent{ObjectOld: oldPR, ObjectNew: newPR})).To(BeTrue())
	})

	It("ignores updates when not terminating", func() {
		oldPR := &promoterv1alpha1.PullRequest{
			ObjectMeta: metav1.ObjectMeta{
				Generation: 1,
				Finalizers: []string{promoterv1alpha1.ChangeTransferPolicyPullRequestFinalizer},
			},
			Spec: promoterv1alpha1.PullRequestSpec{State: promoterv1alpha1.PullRequestOpen},
		}
		newPR := oldPR.DeepCopy()
		newPR.Finalizers = []string{
			promoterv1alpha1.ChangeTransferPolicyPullRequestFinalizer,
			promoterv1alpha1.PullRequestFinalizer,
		}
		Expect(pred.Update(event.UpdateEvent{ObjectOld: oldPR, ObjectNew: newPR})).To(BeFalse())
	})

	It("ignores terminating updates when finalizer count is unchanged", func() {
		now := metav1.Now()
		oldPR := &promoterv1alpha1.PullRequest{
			ObjectMeta: metav1.ObjectMeta{
				Generation:        1,
				DeletionTimestamp: &now,
				Finalizers:        []string{promoterv1alpha1.PullRequestFinalizer},
			},
			Spec: promoterv1alpha1.PullRequestSpec{State: promoterv1alpha1.PullRequestOpen},
		}
		newPR := oldPR.DeepCopy()
		newPR.ResourceVersion = "2"
		Expect(pred.Update(event.UpdateEvent{ObjectOld: oldPR, ObjectNew: newPR})).To(BeFalse())
	})
})

var _ = Describe("pullRequestAwaitingMergedTargetSha", func() {
	DescribeTable("awaiting merged target sha",
		func(pr promoterv1alpha1.PullRequest, awaiting bool) {
			Expect(pullRequestAwaitingMergedTargetSha(&pr)).To(Equal(awaiting))
		},
		Entry("merged with sha",
			promoterv1alpha1.PullRequest{Status: promoterv1alpha1.PullRequestStatus{State: promoterv1alpha1.PullRequestMerged, ID: "1", MergedTargetSha: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
			false,
		),
		Entry("merged without sha but with id",
			promoterv1alpha1.PullRequest{
				Spec:   promoterv1alpha1.PullRequestSpec{State: promoterv1alpha1.PullRequestMerged},
				Status: promoterv1alpha1.PullRequestStatus{State: promoterv1alpha1.PullRequestMerged, ID: "42"},
			},
			true,
		),
		Entry("merged without sha or id",
			promoterv1alpha1.PullRequest{Status: promoterv1alpha1.PullRequestStatus{State: promoterv1alpha1.PullRequestMerged}},
			false,
		),
		Entry("open",
			promoterv1alpha1.PullRequest{Status: promoterv1alpha1.PullRequestStatus{State: promoterv1alpha1.PullRequestOpen, ID: "1"}},
			false,
		),
	)
})

var _ = Describe("pullRequestHasTerminalSCMOutcome", func() {
	DescribeTable("terminal outcomes",
		func(pr promoterv1alpha1.PullRequest, terminal bool) {
			Expect(pullRequestHasTerminalSCMOutcome(&pr)).To(Equal(terminal))
		},
		Entry("open",
			promoterv1alpha1.PullRequest{Status: promoterv1alpha1.PullRequestStatus{State: promoterv1alpha1.PullRequestOpen}},
			false,
		),
		Entry("empty state",
			promoterv1alpha1.PullRequest{Status: promoterv1alpha1.PullRequestStatus{}},
			false,
		),
		Entry("merged",
			promoterv1alpha1.PullRequest{Status: promoterv1alpha1.PullRequestStatus{State: promoterv1alpha1.PullRequestMerged, ID: "1", MergedTargetSha: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
			true,
		),
		Entry("merged without mergedTargetSha",
			promoterv1alpha1.PullRequest{Status: promoterv1alpha1.PullRequestStatus{State: promoterv1alpha1.PullRequestMerged, ID: "1"}},
			false,
		),
		Entry("merged without mergedTargetSha or id",
			promoterv1alpha1.PullRequest{Status: promoterv1alpha1.PullRequestStatus{State: promoterv1alpha1.PullRequestMerged}},
			true,
		),
		Entry("closed",
			promoterv1alpha1.PullRequest{Status: promoterv1alpha1.PullRequestStatus{State: promoterv1alpha1.PullRequestClosed}},
			true,
		),
		Entry("externally merged or closed",
			promoterv1alpha1.PullRequest{Status: promoterv1alpha1.PullRequestStatus{ExternallyMergedOrClosed: new(true)}},
			true,
		),
	)
})

var _ = Describe("shouldSkipSCMSync", func() {
	openPRWithStatus := func() *promoterv1alpha1.PullRequest {
		pr := &promoterv1alpha1.PullRequest{
			ObjectMeta: metav1.ObjectMeta{Generation: 2},
			Spec: promoterv1alpha1.PullRequestSpec{
				Title:       "title",
				Description: "description",
				State:       promoterv1alpha1.PullRequestOpen,
				Commit:      promoterv1alpha1.CommitConfiguration{Message: "trailers only"},
				MergeSha:    "fedcba9876543210fedcba9876543210fedcba98",
			},
			Status: promoterv1alpha1.PullRequestStatus{
				ID:                 "1",
				ObservedGeneration: 1,
				SCMSyncedSpecDigest: pullRequestImmediatelySyncedSpecDigest(&promoterv1alpha1.PullRequest{
					Spec: promoterv1alpha1.PullRequestSpec{
						Title:       "title",
						Description: "description",
					},
				}),
			},
		}
		return pr
	}

	It("skips SCM when only commit.message and mergeSha changed on an open PR", func() {
		Expect(shouldSkipSCMSync(openPRWithStatus())).To(BeTrue())
	})

	It("does not skip when title or description changed", func() {
		pr := openPRWithStatus()
		pr.Spec.Title = "updated title"
		Expect(shouldSkipSCMSync(pr)).To(BeFalse())
	})

	It("does not skip when labels need syncing", func() {
		pr := openPRWithStatus()
		pr.Spec.Labels = []string{"lgtm", "approved"}
		Expect(shouldSkipSCMSync(pr)).To(BeFalse())
	})

	It("does not skip when the PR has not been created in SCM yet", func() {
		pr := openPRWithStatus()
		pr.Status.ID = ""
		Expect(shouldSkipSCMSync(pr)).To(BeFalse())
	})

	It("does not skip when spec.state is merged", func() {
		pr := openPRWithStatus()
		pr.Spec.State = promoterv1alpha1.PullRequestMerged
		Expect(shouldSkipSCMSync(pr)).To(BeFalse())
	})

	It("does not skip when deleting", func() {
		now := metav1.Now()
		pr := openPRWithStatus()
		pr.DeletionTimestamp = &now
		Expect(shouldSkipSCMSync(pr)).To(BeFalse())
	})
})

func pullRequestResources(ctx context.Context, name string) (string, *v1.Secret, *promoterv1alpha1.ScmProvider, *promoterv1alpha1.GitRepository, *promoterv1alpha1.PullRequest) {
	name = name + "-" + utils.KubeSafeUniqueName(randomString(15))
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
	setupInitialTestGitRepoOnServer(ctx, gitRepo)

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

// triggerPRReconcile bumps spec.Labels so the PullRequest controller's
// GenerationChangedPredicate enqueues a reconcile. When expectedUID is set, the patch is
// skipped once the live object has a different UID (e.g. the CTP recreated the PR).
func triggerPRReconcile(ctx context.Context, key types.NamespacedName, pr *promoterv1alpha1.PullRequest, expectedUID ...types.UID) {
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, key, pr)).To(Succeed())
		if len(expectedUID) > 0 && pr.UID != expectedUID[0] {
			return
		}
		orig := pr.DeepCopy()
		if pr.Spec.Labels == nil {
			pr.Spec.Labels = []string{"trigger-reconcile"}
		} else {
			pr.Spec.Labels = append(slices.Clone(pr.Spec.Labels), "trigger-reconcile")
		}
		g.Expect(k8sClient.Patch(ctx, pr, client.MergeFrom(orig))).To(Succeed())
	}, constants.EventuallyTimeout).Should(Succeed())
}

// setPullRequestRequeueDuration patches the singleton ControllerConfiguration's
// pullRequest.workQueue.requeueDuration for the current test and registers a DeferCleanup that
// restores the shipped default afterward, so later specs aren't affected.
func setPullRequestRequeueDuration(ctx context.Context, d time.Duration) {
	var cc promoterv1alpha1.ControllerConfiguration
	key := types.NamespacedName{Namespace: "default", Name: settings.ControllerConfigurationName}
	Expect(k8sClient.Get(ctx, key, &cc)).To(Succeed())
	original := cc.Spec.PullRequest.WorkQueue.RequeueDuration
	cc.Spec.PullRequest.WorkQueue.RequeueDuration = metav1.Duration{Duration: d}
	Expect(k8sClient.Update(ctx, &cc)).To(Succeed())

	DeferCleanup(func() {
		var cc promoterv1alpha1.ControllerConfiguration
		Expect(k8sClient.Get(ctx, key, &cc)).To(Succeed())
		cc.Spec.PullRequest.WorkQueue.RequeueDuration = original
		Expect(k8sClient.Update(ctx, &cc)).To(Succeed())
	})
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
