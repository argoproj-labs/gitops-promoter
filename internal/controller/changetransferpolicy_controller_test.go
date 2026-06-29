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
	"os"
	"path"
	"slices"
	"strings"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

//go:embed testdata/ChangeTransferPolicy.yaml
var testChangeTransferPolicyYAML string

const healthCheckCSKey = "health-check"

var _ = Describe("ChangeTransferPolicy Controller", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Context("When unmarshalling the test data", func() {
		It("should unmarshal the ChangeTransferPolicy resource", func() {
			err := unmarshalYamlStrict(testChangeTransferPolicyYAML, &promoterv1alpha1.ChangeTransferPolicy{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("When reconciling a resource", func() {
		Context("When no commit status checks are configured", func() {
			var name string
			var gitRepo *promoterv1alpha1.GitRepository
			var changeTransferPolicy *promoterv1alpha1.ChangeTransferPolicy
			var typeNamespacedName types.NamespacedName
			var pr promoterv1alpha1.PullRequest
			var prName string

			BeforeEach(func() {
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				name, scmSecret, scmProvider, gitRepo, _, changeTransferPolicy = changeTransferPolicyResources(ctx, "ctp-without-commit-checks", "default")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default", // TODO(user):Modify as needed
				}

				changeTransferPolicy.Spec.ProposedBranch = testBranchDevelopmentNext
				changeTransferPolicy.Spec.ActiveBranch = testBranchDevelopment
				// We set auto merge to false to avoid the PR being merged automatically so we can run checks on it
				changeTransferPolicy.Spec.AutoMerge = new(false)

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

				prName = utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, changeTransferPolicy.Spec.ProposedBranch, changeTransferPolicy.Spec.ActiveBranch)
			})

			AfterEach(func() {
				By("Cleaning up resources")
				_ = k8sClient.Delete(ctx, changeTransferPolicy)
			})

			It("should successfully reconcile the resource - with a pending commit and no commit status checks", func() {
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())

				By("Adding a pending commit")
				fullSha, shortSha := makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")

				By("Reconciling the created resource")

				Eventually(func(g Gomega) {
					err = k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
					g.Expect(err).To(Succeed())
					g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha).To(Equal(fullSha))
					g.Expect(changeTransferPolicy.Status.Active.Hydrated.Sha).ToNot(Equal(""))
					g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).ToNot(Equal(""))
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					typeNamespacedNamePR := types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(prName),
						Namespace: "default",
					}
					err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
					g.Expect(err).To(Succeed())
					g.Expect(pr.Spec.Title).To(Equal(fmt.Sprintf("Promote (%s) to `%s`", shortSha, testBranchDevelopment)))
					g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
					g.Expect(pr.Name).To(Equal(utils.KubeSafeUniqueName(prName)))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Adding another pending commit")
				_, shortSha = makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(prName),
						Namespace: "default",
					}, &pr)
					g.Expect(err).To(Succeed())
					g.Expect(pr.Spec.Title).To(Equal(fmt.Sprintf("Promote (%s) to `%s`", shortSha, testBranchDevelopment)))
					g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
					g.Expect(pr.Name).To(Equal(utils.KubeSafeUniqueName(prName)))
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					err = k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
					Expect(err).To(Succeed())
					// We now have a PR so we can set it to true and then check that it gets merged
					changeTransferPolicy.Spec.AutoMerge = new(true)
					err = k8sClient.Update(ctx, changeTransferPolicy)
					g.Expect(err).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
					g.Expect(err).To(Succeed())
					g.Expect(changeTransferPolicy.Status.PullRequest).ToNot(BeNil(), "CTP should have PR status")
					g.Expect(changeTransferPolicy.Status.PullRequest.State).To(Equal(promoterv1alpha1.PullRequestMerged), "CTP status should show PR state as merged when controller merges it")
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					typeNamespacedNamePR := types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(prName),
						Namespace: "default",
					}
					err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		Context("When using commit status checks", func() {
			var name string
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			var commitStatus *promoterv1alpha1.CommitStatus
			var changeTransferPolicy *promoterv1alpha1.ChangeTransferPolicy
			var typeNamespacedName types.NamespacedName
			var gitPath string
			var err error
			var pr promoterv1alpha1.PullRequest
			var prName string

			BeforeEach(func() {
				name, scmSecret, scmProvider, gitRepo, commitStatus, changeTransferPolicy = changeTransferPolicyResources(ctx, "ctp-with-commit-checks", "default")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				changeTransferPolicy.Spec.ProposedBranch = testBranchDevelopmentNext
				changeTransferPolicy.Spec.ActiveBranch = testBranchDevelopment
				// We set auto merge to false to avoid the PR being merged automatically so we can run checks on it
				changeTransferPolicy.Spec.AutoMerge = new(false)

				changeTransferPolicy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
					{
						Key: healthCheckCSKey,
					},
				}

				commitStatus.Spec.Name = healthCheckCSKey
				commitStatus.Labels = map[string]string{
					promoterv1alpha1.CommitStatusLabel: healthCheckCSKey,
				}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

				prName = utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, changeTransferPolicy.Spec.ProposedBranch, changeTransferPolicy.Spec.ActiveBranch)

				gitPath, err = os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				By("Cleaning up resources")
				Expect(k8sClient.Delete(ctx, changeTransferPolicy)).To(Succeed())
				Expect(k8sClient.Delete(ctx, commitStatus)).To(Succeed())
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			})

			It("should successfully reconcile the resource", func() {
				By("Adding a pending commit")
				makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")

				By("Checking commit status before CommitStatus resource is created")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
					g.Expect(err).To(Succeed())
					g.Expect(changeTransferPolicy.Status.Active.CommitStatuses).To(HaveLen(1))
					g.Expect(changeTransferPolicy.Status.Active.CommitStatuses[0].Key).To(Equal(healthCheckCSKey))
					g.Expect(changeTransferPolicy.Status.Active.CommitStatuses[0].Phase).To(Equal("pending"))
					g.Expect(changeTransferPolicy.Status.Active.CommitStatuses[0].Description).To(Equal("Waiting for status to be reported"))
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					sha, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+changeTransferPolicy.Spec.ActiveBranch)
					g.Expect(err).NotTo(HaveOccurred())
					sha = strings.TrimSpace(sha)

					commitStatus.Spec.Sha = sha
					commitStatus.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
					err = k8sClient.Create(ctx, commitStatus)
					g.Expect(err).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
					g.Expect(err).To(Succeed())

					sha, err := runGitCmd(ctx, gitPath, "rev-parse", changeTransferPolicy.Spec.ActiveBranch)
					Expect(err).NotTo(HaveOccurred())
					sha = strings.TrimSpace(sha)

					g.Expect(changeTransferPolicy.Status.Active.Hydrated.Sha).To(Equal(sha))
					g.Expect(changeTransferPolicy.Status.Active.CommitStatuses[0].Key).To(Equal(healthCheckCSKey))
					g.Expect(changeTransferPolicy.Status.Active.CommitStatuses[0].Phase).To(Equal("success"))
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					err = k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
					Expect(err).To(Succeed())
					// We now have a PR so we can set it to true and then check that it gets merged
					changeTransferPolicy.Spec.AutoMerge = new(true)
					err = k8sClient.Update(ctx, changeTransferPolicy)
					g.Expect(err).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					typeNamespacedNamePR := types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(prName),
						Namespace: "default",
					}
					err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		Context("When emitting promotion lifecycle events", func() {
			var name string
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			var commitStatus *promoterv1alpha1.CommitStatus
			var changeTransferPolicy *promoterv1alpha1.ChangeTransferPolicy
			var typeNamespacedName types.NamespacedName
			var gitPath string
			var err error

			const promotionGateCSKey = "promotion-gate"

			BeforeEach(func() {
				name, scmSecret, scmProvider, gitRepo, commitStatus, changeTransferPolicy = changeTransferPolicyResources(ctx, "ctp-lifecycle-events", "default")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				changeTransferPolicy.Spec.ProposedBranch = testBranchDevelopmentNext
				changeTransferPolicy.Spec.ActiveBranch = testBranchDevelopment
				changeTransferPolicy.Spec.AutoMerge = new(true)
				changeTransferPolicy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
					{
						Key: promotionGateCSKey,
					},
				}

				commitStatus.Spec.Name = promotionGateCSKey
				commitStatus.Labels = map[string]string{
					promoterv1alpha1.CommitStatusLabel: promotionGateCSKey,
				}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

				gitPath, err = os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				By("Cleaning up resources")
				Expect(k8sClient.Delete(ctx, changeTransferPolicy)).To(Succeed())
				Expect(k8sClient.Delete(ctx, commitStatus)).To(Succeed())
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			})

			It("emits PromotionStarted, PromotionBlocked, and PromotionCompleted across the promotion flow", func() {
				By("Adding a pending commit")
				makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")

				By("Waiting for PromotionStarted and PromotionBlocked while the proposed gate is pending")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)).To(Succeed())
					g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha).NotTo(BeEmpty())
					g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha).NotTo(Equal(changeTransferPolicy.Status.Active.Dry.Sha))

					var eventList v1.EventList
					g.Expect(k8sClient.List(ctx, &eventList, ctrlclient.InNamespace("default"))).To(Succeed())
					g.Expect(hasEventWithReason(eventList, name, constants.PromotionStartedReason)).To(BeTrue())
					g.Expect(hasEventWithReason(eventList, name, constants.PromotionBlockedReason)).To(BeTrue())
					g.Expect(hasEventWithReason(eventList, name, constants.PromotionCompletedReason)).To(BeFalse())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Passing the proposed commit status gate")
				Eventually(func(g Gomega) {
					sha, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+changeTransferPolicy.Spec.ProposedBranch)
					g.Expect(err).NotTo(HaveOccurred())

					commitStatus.Spec.Sha = strings.TrimSpace(sha)
					commitStatus.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
					err = k8sClient.Create(ctx, commitStatus)
					g.Expect(err).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Waiting for the merge and PromotionCompleted")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)).To(Succeed())
					g.Expect(changeTransferPolicy.Status.Active.Dry.Sha).To(Equal(changeTransferPolicy.Status.Proposed.Dry.Sha))

					var eventList v1.EventList
					g.Expect(k8sClient.List(ctx, &eventList, ctrlclient.InNamespace("default"))).To(Succeed())
					g.Expect(hasEventWithReason(eventList, name, constants.PromotionCompletedReason)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		// Happens if the active branch does not have a hydrator.metadata such as when the branch was just created
		Context("When active branch has unknown dry sha", func() {
			var name string
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			var changeTransferPolicy *promoterv1alpha1.ChangeTransferPolicy
			var typeNamespacedName types.NamespacedName
			var gitPath string
			var err error
			var pr promoterv1alpha1.PullRequest
			var prName string

			BeforeEach(func() {
				name, scmSecret, scmProvider, gitRepo, _, changeTransferPolicy = changeTransferPolicyResources(ctx, "ctp-without-dry-sha", "default")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				changeTransferPolicy.Spec.ProposedBranch = testBranchDevelopmentNext
				changeTransferPolicy.Spec.ActiveBranch = testBranchDevelopment
				// We set auto merge to false to avoid the PR being merged automatically so we can run checks on it
				changeTransferPolicy.Spec.AutoMerge = new(false)

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

				prName = utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, changeTransferPolicy.Spec.ProposedBranch, changeTransferPolicy.Spec.ActiveBranch)

				gitPath, err = os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				By("Cleaning up resources")
				Expect(k8sClient.Delete(ctx, changeTransferPolicy)).To(Succeed())
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			})

			It("should successfully reconcile the resource", func() {
				By("Adding a pending commit")
				fullSha, shortSha := makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")

				By("Reconciling the created resource")

				Eventually(func(g Gomega) {
					err = k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
					g.Expect(err).To(Succeed())
					g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha).To(Equal(fullSha))
					g.Expect(changeTransferPolicy.Status.Active.Hydrated.Sha).ToNot(Equal(""))
					g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).ToNot(Equal(""))
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					typeNamespacedNamePR := types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(prName),
						Namespace: "default",
					}
					err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
					g.Expect(err).To(Succeed())
					g.Expect(pr.Spec.Title).To(Equal(fmt.Sprintf("Promote (%s) to `%s`", shortSha, testBranchDevelopment)))
					g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
					g.Expect(pr.Name).To(Equal(utils.KubeSafeUniqueName(prName)))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Adding another pending commit")
				_, shortSha = makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(prName),
						Namespace: "default",
					}, &pr)
					g.Expect(err).To(Succeed())
					g.Expect(pr.Spec.Title).To(Equal(fmt.Sprintf("Promote (%s) to `%s`", shortSha, testBranchDevelopment)))
					g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
					g.Expect(pr.Name).To(Equal(utils.KubeSafeUniqueName(prName)))
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					err = k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
					Expect(err).To(Succeed())
					// We now have a PR so we can set it to true and then check that it gets merged
					changeTransferPolicy.Spec.AutoMerge = new(true)
					err = k8sClient.Update(ctx, changeTransferPolicy)
					g.Expect(err).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					typeNamespacedNamePR := types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(prName),
						Namespace: "default",
					}
					err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		Context("When setting mergeSha field", func() {
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			var changeTransferPolicy *promoterv1alpha1.ChangeTransferPolicy
			var gitPath string
			var err error
			var pr promoterv1alpha1.PullRequest
			var prName string

			BeforeEach(func() {
				_, scmSecret, scmProvider, gitRepo, _, changeTransferPolicy = changeTransferPolicyResources(ctx, "ctp-merge-sha", "default")

				changeTransferPolicy.Spec.ProposedBranch = testBranchDevelopmentNext
				changeTransferPolicy.Spec.ActiveBranch = testBranchDevelopment
				changeTransferPolicy.Spec.AutoMerge = new(false)

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

				prName = utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, changeTransferPolicy.Spec.ProposedBranch, changeTransferPolicy.Spec.ActiveBranch)

				gitPath, err = os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				By("Cleaning up resources")
				Expect(k8sClient.Delete(ctx, changeTransferPolicy)).To(Succeed())
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			})

			It("should set mergeSha to proposed hydrated SHA", func() {
				By("Adding a pending commit")
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")

				By("Reconciling and waiting for PR creation")

				// Verify mergeSha is set and matches the current proposed hydrated SHA
				Eventually(func(g Gomega) {
					typeNamespacedNamePR := types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(prName),
						Namespace: "default",
					}
					err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
					g.Expect(err).To(Succeed())
					g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
					// Verify mergeSha is set (not empty)
					g.Expect(pr.Spec.MergeSha).ToNot(BeEmpty())

					// Get the current hydrated SHA from the proposed branch
					currentHydratedSha, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+changeTransferPolicy.Spec.ProposedBranch)
					g.Expect(err).NotTo(HaveOccurred())
					currentHydratedSha = strings.TrimSpace(currentHydratedSha)

					// Verify mergeSha matches the current HEAD of the proposed branch
					// This ensures that the PR will only merge if the branch head hasn't changed
					g.Expect(pr.Spec.MergeSha).To(Equal(currentHydratedSha))
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		Context("When reading commit status phase", func() {
			var name string
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			var commitStatus *promoterv1alpha1.CommitStatus
			var changeTransferPolicy *promoterv1alpha1.ChangeTransferPolicy
			var typeNamespacedName types.NamespacedName
			var gitPath string
			var err error

			BeforeEach(func() {
				name, scmSecret, scmProvider, gitRepo, commitStatus, changeTransferPolicy = changeTransferPolicyResources(ctx, "ctp-spec-phase", "default")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				changeTransferPolicy.Spec.ProposedBranch = testBranchDevelopmentNext
				changeTransferPolicy.Spec.ActiveBranch = testBranchDevelopment
				changeTransferPolicy.Spec.AutoMerge = new(false)

				changeTransferPolicy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
					{
						Key: healthCheckCSKey,
					},
				}

				commitStatus.Spec.Name = healthCheckCSKey
				commitStatus.Labels = map[string]string{
					promoterv1alpha1.CommitStatusLabel: healthCheckCSKey,
				}
				// Point at a non-existent GitRepository so the CommitStatus controller errors at
				// getCommitStatusProvider before reaching the fake provider's Set(), which would
				// otherwise overwrite status.phase from spec.phase on every reconcile and race
				// the Status().Update below that deliberately sets a spec/status mismatch.
				commitStatus.Spec.RepositoryReference = promoterv1alpha1.ObjectReference{Name: "nonexistent-gitrepo-flake-guard"}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

				gitPath, err = os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				By("Cleaning up resources")
				Expect(k8sClient.Delete(ctx, changeTransferPolicy)).To(Succeed())
				Expect(k8sClient.Delete(ctx, commitStatus)).To(Succeed())
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			})

			It("should read phase from spec instead of status to avoid stale reads", func() {
				By("Adding a pending commit")
				makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")

				By("Creating CommitStatus with success in spec")
				Eventually(func(g Gomega) {
					sha, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+changeTransferPolicy.Spec.ActiveBranch)
					g.Expect(err).NotTo(HaveOccurred())
					sha = strings.TrimSpace(sha)

					// Create with spec.phase = success
					commitStatus.Spec.Sha = sha
					commitStatus.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
					err = k8sClient.Create(ctx, commitStatus)
					g.Expect(err).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Setting status.phase to pending (creating spec/status mismatch)")
				Eventually(func(g Gomega) {
					csKey := types.NamespacedName{
						Name:      commitStatus.Name,
						Namespace: commitStatus.Namespace,
					}
					err := k8sClient.Get(ctx, csKey, commitStatus)
					g.Expect(err).To(Succeed())

					// Intentionally set status to pending while spec is success
					commitStatus.Status.Phase = promoterv1alpha1.CommitPhasePending
					err = k8sClient.Status().Update(ctx, commitStatus)
					g.Expect(err).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Confirming spec.phase=success but status.phase=pending (mismatch)")
				csKey := types.NamespacedName{
					Name:      commitStatus.Name,
					Namespace: commitStatus.Namespace,
				}
				err = k8sClient.Get(ctx, csKey, commitStatus)
				Expect(err).To(Succeed())
				Expect(commitStatus.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess), "spec should be success")
				Expect(commitStatus.Status.Phase).To(Equal(promoterv1alpha1.CommitPhasePending), "status should be pending")

				By("Enqueuing ChangeTransferPolicy reconcile (CommitStatus controller does not enqueue CTP when provider lookup fails)")
				enqueueCTP(typeNamespacedName.Namespace, typeNamespacedName.Name)

				By("Verifying CTP reads 'success' from spec, NOT 'pending' from status")
				// CRITICAL TEST: CTP MUST read "success" from spec.phase
				// If it reads from status.phase, it would see "pending" and this test would FAIL
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
					g.Expect(err).To(Succeed())
					g.Expect(changeTransferPolicy.Status.Active.CommitStatuses).To(HaveLen(1))
					g.Expect(changeTransferPolicy.Status.Active.CommitStatuses[0].Key).To(Equal(healthCheckCSKey))
					// This MUST be "success" - proves we read from spec, not status
					g.Expect(changeTransferPolicy.Status.Active.CommitStatuses[0].Phase).To(Equal("success"))
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		Context("When handling PR lifecycle and finalizers", func() {
			var name string
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			var gitRepo *promoterv1alpha1.GitRepository
			var changeTransferPolicy *promoterv1alpha1.ChangeTransferPolicy
			var typeNamespacedName types.NamespacedName
			var gitPath string
			var err error
			var prName string

			BeforeEach(func() {
				name, scmSecret, scmProvider, gitRepo, _, changeTransferPolicy = changeTransferPolicyResources(ctx, "ctp-pr-lifecycle", "default")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				changeTransferPolicy.Spec.ProposedBranch = testBranchDevelopmentNext
				changeTransferPolicy.Spec.ActiveBranch = testBranchDevelopment
				changeTransferPolicy.Spec.AutoMerge = new(false)

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

				prName = utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, changeTransferPolicy.Spec.ProposedBranch, changeTransferPolicy.Spec.ActiveBranch)

				gitPath, err = os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				By("Cleaning up resources")
				Expect(ctrlclient.IgnoreNotFound(k8sClient.Delete(ctx, changeTransferPolicy))).To(Succeed())
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			})

			// After the ChangeTransferPolicy is deleted, either the PullRequest is gone or it no longer carries
			// ChangeTransferPolicyPullRequestFinalizer. Either outcome shows the CTP is no longer blocking PR cleanup.
			// Envtest does not run garbage collection like a real cluster, so we cannot require one branch only.
			It("should remove PullRequest finalizers when ChangeTransferPolicy is deleted", func() {
				By("Adding a pending commit")
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")

				var createdPR promoterv1alpha1.PullRequest
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(prName),
						Namespace: "default",
					}, &createdPR)
					g.Expect(err).To(Succeed())
					g.Expect(createdPR.Finalizers).To(ContainElement(promoterv1alpha1.ChangeTransferPolicyPullRequestFinalizer))
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
					g.Expect(err).To(Succeed())
					g.Expect(changeTransferPolicy.Finalizers).To(ContainElement(promoterv1alpha1.ChangeTransferPolicyPullRequestCleanupFinalizer))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Deleting ChangeTransferPolicy")
				Expect(k8sClient.Delete(ctx, changeTransferPolicy)).To(Succeed())

				By("Ensuring the PullRequest is deleted or no longer has the ChangeTransferPolicy finalizer")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      createdPR.Name,
						Namespace: createdPR.Namespace,
					}, &createdPR)
					if errors.IsNotFound(err) {
						return
					}
					g.Expect(err).To(Succeed())
					g.Expect(createdPR.Finalizers).NotTo(ContainElement(promoterv1alpha1.ChangeTransferPolicyPullRequestFinalizer))
				}, constants.EventuallyTimeout).Should(Succeed())
			})

			It("should remove CTP finalizer from PR when PR is externally closed and status is synced", func() {
				By("Adding a pending commit")
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")

				By("Waiting for PR to be created")
				var createdPR promoterv1alpha1.PullRequest
				Eventually(func(g Gomega) {
					typeNamespacedNamePR := types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(prName),
						Namespace: "default",
					}
					err := k8sClient.Get(ctx, typeNamespacedNamePR, &createdPR)
					g.Expect(err).To(Succeed())
					g.Expect(createdPR.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
					g.Expect(createdPR.Status.ID).ToNot(BeEmpty())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Simulating external PR closure by setting ExternallyMergedOrClosed")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      createdPR.Name,
						Namespace: createdPR.Namespace,
					}, &createdPR)
					g.Expect(err).To(Succeed())

					// Simulate PR controller marking it as externally closed
					createdPR.Status.ExternallyMergedOrClosed = new(true)
					createdPR.Status.State = promoterv1alpha1.PullRequestClosed
					err = k8sClient.Status().Update(ctx, &createdPR)
					g.Expect(err).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Waiting for CTP to sync the PR status")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
					g.Expect(err).To(Succeed())
					g.Expect(changeTransferPolicy.Status.PullRequest).ToNot(BeNil())
					g.Expect(changeTransferPolicy.Status.PullRequest.ID).To(Equal(createdPR.Status.ID))
					g.Expect(changeTransferPolicy.Status.PullRequest.State).To(Equal(createdPR.Status.State))
					g.Expect(changeTransferPolicy.Status.PullRequest.ExternallyMergedOrClosed).ToNot(BeNil())
					g.Expect(*changeTransferPolicy.Status.PullRequest.ExternallyMergedOrClosed).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Marking PR for deletion")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      createdPR.Name,
						Namespace: createdPR.Namespace,
					}, &createdPR)
					g.Expect(err).To(Succeed())

					err = k8sClient.Delete(ctx, &createdPR)
					g.Expect(err).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Verifying finalizer is removed and PR can be deleted")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      createdPR.Name,
						Namespace: createdPR.Namespace,
					}, &createdPR)
					// PR should be deleted (not found)
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		Context("When an open PR is in steady state", func() {
			var (
				name                 string
				scmSecret            *v1.Secret
				scmProvider          *promoterv1alpha1.ScmProvider
				gitRepo              *promoterv1alpha1.GitRepository
				changeTransferPolicy *promoterv1alpha1.ChangeTransferPolicy
				ctpKey               types.NamespacedName
				prKey                types.NamespacedName
				gitPath              string
				prName               string
			)

			BeforeEach(func() {
				var err error
				name, scmSecret, scmProvider, gitRepo, _, changeTransferPolicy = changeTransferPolicyResources(ctx, "ctp-open-pr-steady", "default")

				ctpKey = types.NamespacedName{Name: name, Namespace: "default"}
				changeTransferPolicy.Spec.ProposedBranch = testBranchDevelopmentNext
				changeTransferPolicy.Spec.ActiveBranch = testBranchDevelopment
				changeTransferPolicy.Spec.AutoMerge = new(false)

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

				prName = utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, changeTransferPolicy.Spec.ProposedBranch, changeTransferPolicy.Spec.ActiveBranch)
				prKey = types.NamespacedName{Name: utils.KubeSafeUniqueName(prName), Namespace: "default"}

				gitPath, err = os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				Expect(ctrlclient.IgnoreNotFound(k8sClient.Delete(ctx, changeTransferPolicy))).To(Succeed())
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
				_ = os.RemoveAll(gitPath)
			})

			waitForOpenPRWithID := func() (promoterv1alpha1.PullRequest, int64) {
				var pr promoterv1alpha1.PullRequest
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, prKey, &pr)).To(Succeed())
					g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
					g.Expect(pr.Status.ID).NotTo(BeEmpty())
					g.Expect(k8sClient.Get(ctx, ctpKey, changeTransferPolicy)).To(Succeed())
					g.Expect(changeTransferPolicy.Status.PullRequest).NotTo(BeNil())
					g.Expect(changeTransferPolicy.Status.PullRequest.ID).To(Equal(pr.Status.ID))
				}, constants.EventuallyTimeout).Should(Succeed())
				return pr, pr.Generation
			}

			// Regression for open-PR steady state with blocked promotion: routine PR status
			// writes used to Owns(PullRequest)-enqueue CTP, which SSA-reapplied the PR and
			// retriggered PR controller SCM sync in a CTP→PR→CTP feedback loop.
			It("should not enter a CTP→PR SCM feedback loop when PR status churns in steady state", func() {
				By("Creating a pending promotion with an open PR")
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")
				pr, stableGeneration := waitForOpenPRWithID()

				fake.ResetPullRequestSCMCallCounts()

				By("Simulating routine PR controller status-only writes that used to re-enqueue CTP")
				for poke := 0; poke < 3; poke++ {
					Eventually(func(g Gomega) {
						g.Expect(k8sClient.Get(ctx, prKey, &pr)).To(Succeed())
						pr.Status.Url = fmt.Sprintf("https://fake.example/pr/%s?poke=%d", pr.Status.ID, poke)
						g.Expect(k8sClient.Status().Update(ctx, &pr)).To(Succeed())
					}, constants.EventuallyTimeout).Should(Succeed())
				}

				By("Waiting for the last status poke to land on the PR")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, prKey, &pr)).To(Succeed())
					g.Expect(pr.Status.Url).To(Equal(fmt.Sprintf("https://fake.example/pr/%s?poke=2", pr.Status.ID)))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Verifying status churn does not drive SCM polling in a tight loop")
				// With the fix, three status-only pokes produce 0 SCM calls after reset.
				// Without it, the loop reaches 1+ FindOpen/Update pairs within ~0.5s.
				Consistently(func(g Gomega) {
					g.Expect(fake.FindOpenCallCount()).To(BeZero())
					g.Expect(fake.UpdateCallCount()).To(BeZero())
					g.Expect(fake.PullRequestSCMCallCount()).To(BeZero())
				}, 3*time.Second, 100*time.Millisecond).Should(Succeed())

				var afterPR promoterv1alpha1.PullRequest
				Expect(k8sClient.Get(ctx, prKey, &afterPR)).To(Succeed())
				Expect(afterPR.Generation).To(Equal(stableGeneration))
			})

			It("should still hit SCM when the PR spec changes", func() {
				By("Creating a pending promotion with an open PR")
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")
				pr, _ := waitForOpenPRWithID()

				fake.ResetPullRequestSCMCallCounts()
				baselineFindOpen := fake.FindOpenCallCount()

				baselineUpdate := fake.UpdateCallCount()

				By("Changing PR spec so the PR controller must sync to SCM")
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, prKey, &pr)).To(Succeed())
					pr.Spec.Title = pr.Spec.Title + "-updated"
					g.Expect(k8sClient.Update(ctx, &pr)).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					g.Expect(fake.FindOpenCallCount()).To(BeNumerically(">", baselineFindOpen))
					g.Expect(fake.UpdateCallCount()).To(BeNumerically(">", baselineUpdate))
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		// Regression guard for kubernetes/kubernetes#135841: when SSA re-applies a
		// previously-populated nested object as the empty object {}, structured-merge
		// converts the empty object to JSON null during typed merge, and OpenAPI
		// rejects null against a non-nullable type=object field. The promoter
		// triggered this whenever setCommitMetadata wrote
		//
		//	ctp.Status.Proposed.Note = &HydratorMetadata{DrySha: ""}
		//
		// after a previous reconcile populated the same field. Every HydratorMetadata
		// field is JSON omitempty, so the SSA body serialized proposed.note as {}.
		// https://github.com/kubernetes/kubernetes/issues/134902
		Context("When proposed.note transitions populated → empty across reconciles", func() {
			var (
				name                 string
				scmSecret            *v1.Secret
				scmProvider          *promoterv1alpha1.ScmProvider
				gitRepo              *promoterv1alpha1.GitRepository
				changeTransferPolicy *promoterv1alpha1.ChangeTransferPolicy
				ctpKey               types.NamespacedName
				gitPath              string
				err                  error
			)

			BeforeEach(func() {
				name, scmSecret, scmProvider, gitRepo, _, changeTransferPolicy = changeTransferPolicyResources(ctx, "ctp-note-empty-regression", "default")

				ctpKey = types.NamespacedName{Name: name, Namespace: "default"}
				changeTransferPolicy.Spec.ProposedBranch = testBranchDevelopmentNext
				changeTransferPolicy.Spec.ActiveBranch = testBranchDevelopment
				// Avoid auto-merging so the proposed branch keeps advancing across the two
				// hydrations the test drives.
				changeTransferPolicy.Spec.AutoMerge = new(false)

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

				gitPath, err = cloneTestRepo(ctx, gitRepo)
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				By("Cleaning up resources")
				Expect(k8sClient.Delete(ctx, changeTransferPolicy)).To(Succeed())
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
				_ = os.RemoveAll(gitPath)
			})

			It("should not surface a status.proposed.note SSA validation error", func() {
				By("Hydrating the proposed branch with a git note so proposed.note.drySha is populated")
				firstDrySha, err := makeDryCommit(ctx, gitPath, "first dry commit")
				Expect(err).NotTo(HaveOccurred())
				Expect(hydrateEnvironment(ctx, gitPath, testBranchDevelopmentNext, firstDrySha, "hydrate dev for first dry sha")).To(Succeed())

				By("Waiting for the controller to record the populated proposed.note.drySha")
				Eventually(func(g Gomega) {
					var ctp promoterv1alpha1.ChangeTransferPolicy
					g.Expect(k8sClient.Get(ctx, ctpKey, &ctp)).To(Succeed())
					g.Expect(ctp.Status.Proposed.Note).NotTo(BeNil())
					g.Expect(ctp.Status.Proposed.Note.DrySha).To(Equal(firstDrySha))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Hydrating again, advancing the proposed branch to a new hydrated commit with NO git note")
				// makeChangeAndHydrateRepo creates a new dry commit and hydrates each
				// environment branch with a fresh hydrated commit, but does not add a
				// git note to those commits. GetHydratorNote therefore returns
				// HydratorMetadata{} on the next reconcile, and setCommitMetadata
				// serializes the SSA body with "note":{} — the populated → empty
				// transition that triggers #135841 without the schema fix.
				secondDrySha, _ := makeChangeAndHydrateRepo(gitPath, gitRepo, "second dry commit", "")
				Expect(secondDrySha).NotTo(Equal(firstDrySha))

				By("Waiting for the controller to advance the CTP and report Ready=True without an SSA validation error")
				// Without the fix the full status SSA is rejected with 422 and the
				// fallback writes only Ready=False with reason=ReconciliationError and
				// the apiserver error in the message. With the fix the controller
				// successfully applies status and Ready=True. We assert both the
				// positive end state and the absence of the bug-specific error text in
				// the Ready condition message.
				Eventually(func(g Gomega) {
					var ctp promoterv1alpha1.ChangeTransferPolicy
					g.Expect(k8sClient.Get(ctx, ctpKey, &ctp)).To(Succeed())

					ready := meta.FindStatusCondition(ctp.Status.Conditions, string(promoterConditions.Ready))
					g.Expect(ready).NotTo(BeNil(), "Ready condition should be present")
					g.Expect(ready.Message).NotTo(ContainSubstring("status.proposed.note"),
						"Ready message should not surface the kubernetes/kubernetes#135841 SSA validation error: %s", ready.Message)
					g.Expect(ready.Message).NotTo(ContainSubstring("must be of type object"),
						"Ready message should not surface the kubernetes/kubernetes#135841 SSA validation error: %s", ready.Message)
					g.Expect(ready.Status).To(Equal(metav1.ConditionTrue),
						"Ready should be True after the populated → empty proposed.note transition, got reason=%q message=%q", ready.Reason, ready.Message)
					g.Expect(ready.Reason).To(Equal(string(promoterConditions.ReconciliationSuccess)))
					g.Expect(ctp.Status.Proposed.Dry.Sha).To(Equal(secondDrySha))
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		// Regression guard: when the proposed branch advances to a new hydrated
		// commit that has no git note yet, setCommitMetadata must clear
		// Status.Proposed.Note so it does not retain the previous reconcile's
		// drySha. PromotionStrategy.updatePreviousEnvironmentCommitStatus uses
		// getEffectiveHydratedDrySha (note-first) to compute targetDrySha for
		// the previous-environment gate; a stale Proposed.Note pointing at the
		// previous dry SHA causes the gate to compare against the wrong target
		// and mark success against an older dry, allowing e.g. production to
		// merge ahead of dev/staging before their hydrators have caught up.
		Context("When a new proposed hydrated commit arrives with no git note", func() {
			var (
				name                 string
				scmSecret            *v1.Secret
				scmProvider          *promoterv1alpha1.ScmProvider
				gitRepo              *promoterv1alpha1.GitRepository
				changeTransferPolicy *promoterv1alpha1.ChangeTransferPolicy
				ctpKey               types.NamespacedName
				gitPath              string
				err                  error
			)

			BeforeEach(func() {
				name, scmSecret, scmProvider, gitRepo, _, changeTransferPolicy = changeTransferPolicyResources(ctx, "ctp-note-stale-clear", "default")

				ctpKey = types.NamespacedName{Name: name, Namespace: "default"}
				changeTransferPolicy.Spec.ProposedBranch = testBranchDevelopmentNext
				changeTransferPolicy.Spec.ActiveBranch = testBranchDevelopment
				// Avoid auto-merging so the proposed branch keeps advancing across the two
				// hydrations the test drives.
				changeTransferPolicy.Spec.AutoMerge = new(false)

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

				gitPath, err = cloneTestRepo(ctx, gitRepo)
				Expect(err).NotTo(HaveOccurred())
			})

			AfterEach(func() {
				By("Cleaning up resources")
				Expect(k8sClient.Delete(ctx, changeTransferPolicy)).To(Succeed())
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
				_ = os.RemoveAll(gitPath)
			})

			It("should clear Status.Proposed.Note when the new proposed hydrated commit has no git note", func() {
				By("Hydrating the proposed branch with a git note so proposed.note.drySha is populated")
				firstDrySha, err := makeDryCommit(ctx, gitPath, "first dry commit")
				Expect(err).NotTo(HaveOccurred())
				Expect(hydrateEnvironment(ctx, gitPath, testBranchDevelopmentNext, firstDrySha, "hydrate dev for first dry sha")).To(Succeed())

				By("Waiting for the controller to record the populated proposed.note.drySha")
				Eventually(func(g Gomega) {
					var ctp promoterv1alpha1.ChangeTransferPolicy
					g.Expect(k8sClient.Get(ctx, ctpKey, &ctp)).To(Succeed())
					g.Expect(ctp.Status.Proposed.Note).NotTo(BeNil())
					g.Expect(ctp.Status.Proposed.Note.DrySha).To(Equal(firstDrySha))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Advancing the proposed branch to a new hydrated commit with NO git note")
				// makeChangeAndHydrateRepo creates a new dry commit and rehydrates
				// each environment's proposed branch with a fresh hydrated commit
				// but does NOT write a git note for the new hydrated commit. The
				// next CTP reconcile must therefore see no note for the new
				// proposed hydrated SHA and clear Status.Proposed.Note rather
				// than leaving the stale firstDrySha in place.
				secondDrySha, _ := makeChangeAndHydrateRepo(gitPath, gitRepo, "second dry commit", "")
				Expect(secondDrySha).NotTo(Equal(firstDrySha))

				By("Waiting for the controller to advance Proposed.Dry to the second dry SHA and clear the stale Proposed.Note")
				Eventually(func(g Gomega) {
					var ctp promoterv1alpha1.ChangeTransferPolicy
					g.Expect(k8sClient.Get(ctx, ctpKey, &ctp)).To(Succeed())

					// Confirm the reconcile actually advanced past the first dry.
					// Without this guard, a stale (still-pending) reconcile could
					// trivially satisfy the note assertion below by virtue of
					// never having moved.
					g.Expect(ctp.Status.Proposed.Dry.Sha).To(Equal(secondDrySha),
						"controller should have advanced Proposed.Dry.Sha to the new dry commit")

					// The new proposed hydrated commit has no git note, and
					// makeChangeAndHydrateRepo never writes one. setCommitMetadata
					// must therefore clear Status.Proposed.Note to nil so
					// downstream gates (getEffectiveHydratedDrySha) do not trust
					// the firstDrySha as the current env's "effective" hydrated
					// dry. Asserting nil directly (rather than guarding with an
					// if) also pins the contract that "no note" is
					// represented as nil, not &HydratorMetadata{}.
					g.Expect(ctp.Status.Proposed.Note).To(BeNil(),
						"Status.Proposed.Note must be cleared when the new proposed hydrated commit has no git note; leaving the previous reconcile's drySha (%q) lets PromotionStrategy compute targetDrySha from a stale note and merge production ahead of dev/staging", firstDrySha)
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		// Regression test for the "stale PullRequest.spec.mergeSha after auto-resolved conflict" bug.
		//
		// Reproduction shape:
		//   1. Pre-populate the git server so the active branch and the proposed branch already
		//      have a content/content conflict on the same file before any CTP exists. Both
		//      branches share a merge-base from the initial test setup; both modify
		//      manifests-fake.yaml to different content; only the proposed branch ships an
		//      updated hydrator.metadata so a real promotion is needed.
		//   2. Create the CTP with AutoMerge=true.
		//   3. The first reconcile reads the proposed branch tip S_old, detects a conflict,
		//      runs MergeWithOursStrategy (which pushes a new merge commit S_new on the
		//      proposed branch), and *in the same reconcile cycle* calls
		//      createOrUpdatePullRequest with PR.Spec.MergeSha = ctp.Status.Proposed.Hydrated.Sha
		//      = S_old (the value calculateStatus set before the merge ran). mergePullRequests
		//      then flips PR.Spec.State to merged.
		//   4. The PullRequest controller picks up state=merged and asks the SCM provider to
		//      merge. The SCM provider compares actualSha (origin/<proposed> = S_new) against
		//      PR.Spec.MergeSha (S_old) and rejects the merge. We count this in the fake SCM
		//      via fake.MergeShaMismatchCount.
		//   5. Eventually a follow-up CTP reconcile (triggered by the PR Owns watch) re-derives
		//      Status.Proposed from the now-resolved tip and updates PR.Spec.MergeSha to S_new,
		//      after which the next merge attempt succeeds and the active branch advances.
		//
		// The merge does eventually land, but the bug burns at least one extra
		// SCM merge call per conflict-resolved promotion. The fix is
		// to short-circuit the rest of this reconcile when MergeWithOursStrategy rewrites the
		// proposed branch and requeue immediately so the next reconcile creates/updates the PR
		// with the correct mergeSha on the very next attempt.
		Context("When the active and proposed branches conflict and auto-merge is on", func() {
			var name string
			var gitRepo *promoterv1alpha1.GitRepository
			var changeTransferPolicy *promoterv1alpha1.ChangeTransferPolicy
			var typeNamespacedName types.NamespacedName
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider

			BeforeEach(func() {
				name, scmSecret, scmProvider, gitRepo, _, changeTransferPolicy = changeTransferPolicyResources(ctx, "ctp-conflict-auto-merge", "default")

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				changeTransferPolicy.Spec.ProposedBranch = testBranchDevelopmentNext
				changeTransferPolicy.Spec.ActiveBranch = testBranchDevelopment
				changeTransferPolicy.Spec.AutoMerge = new(true)

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				// Intentionally do not create the CTP here — the test pushes conflicting commits
				// to the git server first so that the very first CTP reconcile observes the
				// conflict.
			})

			AfterEach(func() {
				By("Cleaning up resources")
				_ = k8sClient.Delete(ctx, changeTransferPolicy)
			})

			It("does not call SCM Merge with a stale PullRequest.spec.mergeSha", func() {
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()

				By("Pre-populating the active and proposed branches with conflicting content")
				_, err = runGitCmd(ctx, gitPath, "clone", testGitRepoCloneURL(gitRepo), ".")
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "config", "user.name", "testuser")
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "config", "user.email", "testmail@test.com")
				Expect(err).NotTo(HaveOccurred())

				// Active branch: write a manifests-fake.yaml with content X.
				_, err = runGitCmd(ctx, gitPath, "checkout", "-B", testBranchDevelopment, "origin/"+testBranchDevelopment)
				Expect(err).NotTo(HaveOccurred())
				Expect(os.WriteFile(path.Join(gitPath, "manifests-fake.yaml"), []byte("{\"side\": \"active\"}\n"), 0o644)).To(Succeed())
				_, err = runGitCmd(ctx, gitPath, "add", "manifests-fake.yaml")
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "commit", "-m", "active manifest")
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "push", "origin", testBranchDevelopment)
				Expect(err).NotTo(HaveOccurred())

				// Proposed branch: write a *different* manifests-fake.yaml (content Y) plus an
				// updated hydrator.metadata so the controller sees this as a real promotion.
				const proposedDrySha = "deadbeefcafefacefeedbabe1234567890abcdef"
				_, err = runGitCmd(ctx, gitPath, "checkout", "-B", testBranchDevelopmentNext, "origin/"+testBranchDevelopmentNext)
				Expect(err).NotTo(HaveOccurred())
				Expect(os.WriteFile(path.Join(gitPath, "manifests-fake.yaml"), []byte("{\"side\": \"proposed\"}\n"), 0o644)).To(Succeed())
				Expect(os.WriteFile(path.Join(gitPath, "hydrator.metadata"),
					fmt.Appendf(nil, "{\"drySha\": %q}", proposedDrySha), 0o644)).To(Succeed())
				_, err = runGitCmd(ctx, gitPath, "add", "manifests-fake.yaml", "hydrator.metadata")
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "commit", "-m", "proposed hydrated commit")
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "push", "origin", testBranchDevelopmentNext)
				Expect(err).NotTo(HaveOccurred())

				By("Resetting the fake SCM's merge-sha-mismatch counter")
				fake.ResetMergeShaMismatchCount()

				By("Creating the CTP so its very first reconcile sees the pre-existing conflict")
				Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

				By("Waiting for the conflict-resolved PR to merge into active")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
					g.Expect(err).To(Succeed())
					g.Expect(changeTransferPolicy.Status.Active.Dry.Sha).To(Equal(proposedDrySha),
						"active branch should be promoted to the proposed dry SHA after auto-resolved conflict")
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Asserting the SCM was never asked to merge with a stale mergeSha")
				Expect(fake.MergeShaMismatchCount()).To(BeNumerically("==", 0),
					"PR.Spec.MergeSha must not lag origin/<proposedBranch> after gitMergeStrategyOurs "+
						"rewrites the proposed branch tip; otherwise the PullRequest controller asks "+
						"the SCM to merge a sha origin no longer has on the source branch")
			})
		})
	})
})

var _ = Describe("TemplatePullRequest", func() {
	Context("PR template with ChangeTransferPolicy and optional PromotionStrategy", func() {
		It("renders description with only CTP when PromotionStrategy is absent", func() {
			ctp := &promoterv1alpha1.ChangeTransferPolicy{
				Spec: promoterv1alpha1.ChangeTransferPolicySpec{
					ActiveBranch:   testBranchDevelopment,
					ProposedBranch: testBranchDevelopmentNext,
				},
				Status: promoterv1alpha1.ChangeTransferPolicyStatus{
					Proposed: promoterv1alpha1.CommitBranchState{
						Dry: promoterv1alpha1.CommitShaState{Sha: "abc1234"},
					},
				},
			}
			template := promoterv1alpha1.PullRequestTemplate{
				Title:       "Promote {{ trunc 7 .ChangeTransferPolicy.Status.Proposed.Dry.Sha }} to `{{ .ChangeTransferPolicy.Spec.ActiveBranch }}`",
				Description: "Promote to {{ .ChangeTransferPolicy.Spec.ActiveBranch }}{{ if .PromotionStrategy }} Strategy: {{ .PromotionStrategy.Name }}{{ end }}",
			}
			data := map[string]any{"ChangeTransferPolicy": ctp}
			title, description, err := TemplatePullRequest(template, data)
			Expect(err).NotTo(HaveOccurred())
			Expect(title).To(Equal("Promote abc1234 to `" + testBranchDevelopment + "`"))
			Expect(description).To(Equal("Promote to " + testBranchDevelopment))
			Expect(description).NotTo(ContainSubstring("Strategy:"))
		})

		It("renders description with CTP and PromotionStrategy when PromotionStrategy is present", func() {
			ctp := &promoterv1alpha1.ChangeTransferPolicy{
				Spec: promoterv1alpha1.ChangeTransferPolicySpec{
					ActiveBranch:   testBranchDevelopment,
					ProposedBranch: testBranchDevelopmentNext,
				},
				Status: promoterv1alpha1.ChangeTransferPolicyStatus{
					Proposed: promoterv1alpha1.CommitBranchState{
						Dry: promoterv1alpha1.CommitShaState{Sha: "def5678"},
					},
				},
			}
			psName := "my-promotion-strategy"
			ps := &promoterv1alpha1.PromotionStrategy{
				ObjectMeta: metav1.ObjectMeta{Name: psName, Namespace: "default"},
				Spec: promoterv1alpha1.PromotionStrategySpec{
					RepositoryReference: promoterv1alpha1.ObjectReference{Name: "test-repo"},
					Environments:        []promoterv1alpha1.Environment{{Branch: testBranchDevelopment}},
				},
			}
			template := promoterv1alpha1.PullRequestTemplate{
				Title:       "Promote {{ trunc 7 .ChangeTransferPolicy.Status.Proposed.Dry.Sha }} to `{{ .ChangeTransferPolicy.Spec.ActiveBranch }}`",
				Description: "Promote to {{ .ChangeTransferPolicy.Spec.ActiveBranch }}{{ if .PromotionStrategy }} Strategy: {{ .PromotionStrategy.Name }}{{ end }}",
			}
			data := map[string]any{
				"ChangeTransferPolicy": ctp,
				"PromotionStrategy":    ps,
			}
			title, description, err := TemplatePullRequest(template, data)
			Expect(err).NotTo(HaveOccurred())
			Expect(title).To(Equal("Promote def5678 to `" + testBranchDevelopment + "`"))
			Expect(description).To(ContainSubstring("Strategy: " + psName))
			Expect(description).To(ContainSubstring("Promote to " + testBranchDevelopment))
		})
	})
})

var _ = Describe("pullRequestManagedByCTPUnchanged", func() {
	var (
		ctp      *promoterv1alpha1.ChangeTransferPolicy
		existing *promoterv1alpha1.PullRequest
	)

	BeforeEach(func() {
		ctp = &promoterv1alpha1.ChangeTransferPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name: "ctp-prod",
				Labels: map[string]string{
					promoterv1alpha1.PromotionStrategyLabel: "my-ps",
				},
			},
			Spec: promoterv1alpha1.ChangeTransferPolicySpec{
				RepositoryReference: promoterv1alpha1.ObjectReference{Name: "repo"},
				ProposedBranch:      "environment/staging-next",
				ActiveBranch:        "environment/staging",
			},
			Status: promoterv1alpha1.ChangeTransferPolicyStatus{
				Proposed: promoterv1alpha1.CommitBranchState{
					Hydrated: promoterv1alpha1.CommitShaState{Sha: "hydrated-sha"},
				},
			},
		}
		existing = &promoterv1alpha1.PullRequest{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pr-1",
				Labels: map[string]string{
					promoterv1alpha1.PromotionStrategyLabel:    utils.KubeSafeLabel("my-ps"),
					promoterv1alpha1.ChangeTransferPolicyLabel: utils.KubeSafeLabel("ctp-prod"),
					promoterv1alpha1.EnvironmentLabel:          utils.KubeSafeLabel("environment/staging"),
				},
				Finalizers: []string{promoterv1alpha1.ChangeTransferPolicyPullRequestFinalizer},
			},
			Spec: promoterv1alpha1.PullRequestSpec{
				RepositoryReference: promoterv1alpha1.ObjectReference{Name: "repo"},
				Title:               "title",
				Description:         "description",
				TargetBranch:        "environment/staging",
				SourceBranch:        "environment/staging-next",
				MergeSha:            "hydrated-sha",
				State:               promoterv1alpha1.PullRequestOpen,
				Commit:              promoterv1alpha1.CommitConfiguration{Message: "commit message"},
			},
		}
	})

	It("returns true when all managed fields match", func() {
		Expect(pullRequestManagedByCTPUnchanged(existing, "title", "description", "commit message", ctp, promoterv1alpha1.PullRequestOpen)).To(BeTrue())
	})

	It("returns false when commit message differs", func() {
		Expect(pullRequestManagedByCTPUnchanged(existing, "title", "description", "different message", ctp, promoterv1alpha1.PullRequestOpen)).To(BeFalse())
	})

	It("returns false when the CTP finalizer is missing", func() {
		withoutFinalizer := existing.DeepCopy()
		withoutFinalizer.Finalizers = nil
		Expect(pullRequestManagedByCTPUnchanged(withoutFinalizer, "title", "description", "commit message", ctp, promoterv1alpha1.PullRequestOpen)).To(BeFalse())
	})
})

var _ = Describe("pullRequestUpdateEnqueuesChangeTransferPolicyPredicate", func() {
	pred := pullRequestUpdateEnqueuesChangeTransferPolicyPredicate()

	It("ignores status-only URL updates", func() {
		oldPR := &promoterv1alpha1.PullRequest{
			ObjectMeta: metav1.ObjectMeta{Generation: 1},
			Status:     promoterv1alpha1.PullRequestStatus{State: promoterv1alpha1.PullRequestOpen, ID: "1"},
		}
		newPR := oldPR.DeepCopy()
		newPR.Status.Url = "https://example/pr/1"
		Expect(pred.Update(event.UpdateEvent{ObjectOld: oldPR, ObjectNew: newPR})).To(BeFalse())
	})

	It("enqueues on spec generation change", func() {
		oldPR := &promoterv1alpha1.PullRequest{ObjectMeta: metav1.ObjectMeta{Generation: 1}}
		newPR := oldPR.DeepCopy()
		newPR.Generation = 2
		Expect(pred.Update(event.UpdateEvent{ObjectOld: oldPR, ObjectNew: newPR})).To(BeTrue())
	})

	It("enqueues when the PR ID is first set", func() {
		oldPR := &promoterv1alpha1.PullRequest{
			ObjectMeta: metav1.ObjectMeta{Generation: 1},
			Status:     promoterv1alpha1.PullRequestStatus{State: promoterv1alpha1.PullRequestOpen},
		}
		newPR := oldPR.DeepCopy()
		newPR.Status.ID = "42"
		Expect(pred.Update(event.UpdateEvent{ObjectOld: oldPR, ObjectNew: newPR})).To(BeTrue())
	})

	It("enqueues on terminal state change", func() {
		oldPR := &promoterv1alpha1.PullRequest{
			ObjectMeta: metav1.ObjectMeta{Generation: 1},
			Status:     promoterv1alpha1.PullRequestStatus{State: promoterv1alpha1.PullRequestOpen, ID: "1"},
		}
		newPR := oldPR.DeepCopy()
		newPR.Status.State = promoterv1alpha1.PullRequestMerged
		Expect(pred.Update(event.UpdateEvent{ObjectOld: oldPR, ObjectNew: newPR})).To(BeTrue())
	})

	It("enqueues when externally merged flag changes", func() {
		oldPR := &promoterv1alpha1.PullRequest{
			ObjectMeta: metav1.ObjectMeta{Generation: 1},
			Status:     promoterv1alpha1.PullRequestStatus{State: promoterv1alpha1.PullRequestOpen, ID: "1"},
		}
		newPR := oldPR.DeepCopy()
		newPR.Status.ExternallyMergedOrClosed = ptr.To(true)
		Expect(pred.Update(event.UpdateEvent{ObjectOld: oldPR, ObjectNew: newPR})).To(BeTrue())
	})
})

var _ = Describe("tooManyPRsError", func() {
	Context("When formatting tooManyPRsError", func() {
		It("returns an error listing all PR names if 3 or fewer", func() {
			prList := &promoterv1alpha1.PullRequestList{
				Items: []promoterv1alpha1.PullRequest{
					{ObjectMeta: metav1.ObjectMeta{Name: "pr-101"}, Status: promoterv1alpha1.PullRequestStatus{ID: "101"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "pr-102"}, Status: promoterv1alpha1.PullRequestStatus{ID: "102"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "pr-103"}, Status: promoterv1alpha1.PullRequestStatus{ID: "103"}},
				},
			}
			err := tooManyPRsError(prList)
			Expect(err).To(HaveOccurred())
			msg := err.Error()
			Expect(msg).To(Equal("found more than one open PullRequest: pr-101, pr-102, pr-103"))
		})

		It("returns an error listing first 3 PR names and count of remaining if more than 3", func() {
			prList := &promoterv1alpha1.PullRequestList{
				Items: []promoterv1alpha1.PullRequest{
					{ObjectMeta: metav1.ObjectMeta{Name: "pr-201"}, Status: promoterv1alpha1.PullRequestStatus{ID: "201"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "pr-202"}, Status: promoterv1alpha1.PullRequestStatus{ID: "202"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "pr-203"}, Status: promoterv1alpha1.PullRequestStatus{ID: "203"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "pr-204"}, Status: promoterv1alpha1.PullRequestStatus{ID: "204"}},
					{ObjectMeta: metav1.ObjectMeta{Name: "pr-205"}, Status: promoterv1alpha1.PullRequestStatus{ID: "205"}},
				},
			}
			err := tooManyPRsError(prList)
			Expect(err).To(HaveOccurred())
			msg := err.Error()
			Expect(msg).To(Equal("found more than one open PullRequest: pr-201, pr-202, pr-203 and 2 more"))
		})
	})
})

var _ = Describe("emitPromotionLifecycleEvents", func() {
	var recorder *events.FakeRecorder
	var reconciler *ChangeTransferPolicyReconciler
	var ctp *promoterv1alpha1.ChangeTransferPolicy

	BeforeEach(func() {
		recorder = events.NewFakeRecorder(100)
		reconciler = &ChangeTransferPolicyReconciler{Recorder: recorder}
		ctp = &promoterv1alpha1.ChangeTransferPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "test-ctp", Namespace: "default"},
			Spec:       promoterv1alpha1.ChangeTransferPolicySpec{ActiveBranch: "environment/development"},
		}
	})

	drainEvents := func() string {
		var sb strings.Builder
		for {
			select {
			case e := <-recorder.Events:
				sb.WriteString(e)
				sb.WriteString("\n")
			default:
				return sb.String()
			}
		}
	}

	gate := func(key, phase string) promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase {
		return promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{Key: key, Phase: phase}
	}
	ctpStatus := func(activeDry, proposedDry string, proposedGates ...promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase) promoterv1alpha1.ChangeTransferPolicyStatus {
		return promoterv1alpha1.ChangeTransferPolicyStatus{
			Active:   promoterv1alpha1.CommitBranchState{Dry: promoterv1alpha1.CommitShaState{Sha: activeDry}},
			Proposed: promoterv1alpha1.CommitBranchState{Dry: promoterv1alpha1.CommitShaState{Sha: proposedDry}, CommitStatuses: proposedGates},
		}
	}

	Context("When comparing the previous and current status", func() {
		DescribeTable("emits events only on transitions",
			func(prev, cur promoterv1alpha1.ChangeTransferPolicyStatus, expected, unexpected []string) {
				ctp.Status = cur
				reconciler.emitPromotionLifecycleEvents(ctp, &prev)
				emitted := drainEvents()
				for _, want := range expected {
					Expect(emitted).To(ContainSubstring(want))
				}
				for _, dontWant := range unexpected {
					Expect(emitted).NotTo(ContainSubstring(dontWant))
				}
			},
			Entry("new proposed dry sha emits PromotionStarted",
				ctpStatus("sha-a", "sha-a"), ctpStatus("sha-a", "sha-b"),
				[]string{constants.PromotionStartedReason}, []string{constants.PromotionBlockedReason, constants.PromotionCompletedReason}),
			Entry("unchanged pending promotion emits nothing",
				ctpStatus("sha-a", "sha-b", gate("gate", "pending")), ctpStatus("sha-a", "sha-b", gate("gate", "pending")),
				nil, []string{constants.PromotionStartedReason, constants.PromotionBlockedReason, constants.PromotionCompletedReason}),
			Entry("a newer change superseding the in-flight one emits PromotionStarted again",
				ctpStatus("sha-a", "sha-b"), ctpStatus("sha-a", "sha-c"),
				[]string{constants.PromotionStartedReason}, nil),
			Entry("pending gate on a new attempt emits PromotionStarted and PromotionBlocked",
				ctpStatus("sha-a", "sha-a"), ctpStatus("sha-a", "sha-b", gate("gate", "pending")),
				[]string{constants.PromotionStartedReason, constants.PromotionBlockedReason}, []string{constants.PromotionCompletedReason}),
			Entry("gate phase change pending to failure emits a Warning PromotionBlocked",
				ctpStatus("sha-a", "sha-b", gate("gate", "pending")), ctpStatus("sha-a", "sha-b", gate("gate", "failure")),
				[]string{"Warning", constants.PromotionBlockedReason, "failure"}, []string{constants.PromotionStartedReason}),
			Entry("gate stuck in the same phase emits nothing",
				ctpStatus("sha-a", "sha-b", gate("gate", "failure")), ctpStatus("sha-a", "sha-b", gate("gate", "failure")),
				nil, []string{constants.PromotionBlockedReason}),
			Entry("successful gates do not emit PromotionBlocked",
				ctpStatus("sha-a", "sha-a"), ctpStatus("sha-a", "sha-b", gate("health-check", "success")),
				[]string{constants.PromotionStartedReason}, []string{constants.PromotionBlockedReason}),
			Entry("active dry sha advancing emits PromotionCompleted",
				ctpStatus("sha-a", "sha-b"), ctpStatus("sha-b", "sha-b"),
				[]string{constants.PromotionCompletedReason}, []string{constants.PromotionStartedReason, constants.PromotionBlockedReason}),
			Entry("first-ever status population does not emit PromotionCompleted",
				ctpStatus("", ""), ctpStatus("sha-a", "sha-a"),
				nil, []string{constants.PromotionCompletedReason, constants.PromotionStartedReason}),
		)
	})

	Describe("ChangeTransferPolicy proposed dry metadata validation", Label("e2e"), func() {
		const (
			activePathApp = "apps/app-one"
			wrongPath     = activePathApp + "/overlay/stag"
			testNamespace = "default"
		)

		var ctx context.Context

		BeforeEach(func() {
			ctx = context.Background()
		})

		Context("with activePath", func() {
			var (
				gitRepo              *promoterv1alpha1.GitRepository
				promotionStrategy    *promoterv1alpha1.PromotionStrategy
				changeTransferPolicy promoterv1alpha1.ChangeTransferPolicy
				ctpNamespacedName    types.NamespacedName
				proposedBranch       string
			)

			BeforeEach(func() {
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				_, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "ctp-proposed-dry", testNamespace)
				setupInitialTestGitRepoForActivePath(ctx, gitRepo)

				promotionStrategy.Spec.ActivePath = activePathApp
				promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
					{Branch: testBranchDevelopment, AutoMerge: new(false)},
				}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

				ctpNamespacedName = types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(utils.GetChangeTransferPolicyName(promotionStrategy.Name, testBranchDevelopment)),
					Namespace: testNamespace,
				}

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, ctpNamespacedName, &changeTransferPolicy)).To(Succeed())
					g.Expect(changeTransferPolicy.Spec.ActivePath).To(Equal(activePathApp))
					proposedBranch = changeTransferPolicy.Spec.ProposedBranch
					g.Expect(proposedBranch).To(Equal(path.Join(testBranchDevelopmentNext, activePathApp)))
				}, constants.EventuallyTimeout).Should(Succeed())
			})

			AfterEach(func() {
				_ = k8sClient.Delete(ctx, promotionStrategy)
			})

			It("opens a PR when the hydrator writes metadata at activePath", func() {
				gitPath, err := cloneTestRepo(ctx, gitRepo)
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()

				drySha, err := makeDryCommit(ctx, gitPath, "dry commit at activePath")
				Expect(err).NotTo(HaveOccurred())

				beforeSha, hydratedSha, err := pushHydratedBranchForPath(ctx, gitPath, proposedBranch,
					activePathApp, testBranchDevelopment, drySha, "hydrated at activePath")
				Expect(err).NotTo(HaveOccurred())
				Expect(pushGitNote(ctx, gitPath, hydratedSha, drySha)).To(Succeed())
				sendWebhookForPush(ctx, beforeSha, proposedBranch)

				prName := utils.KubeSafeUniqueName(utils.GetPullRequestName(
					gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name,
					changeTransferPolicy.Spec.ProposedBranch, changeTransferPolicy.Spec.ActiveBranch,
				))

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, ctpNamespacedName, &changeTransferPolicy)).To(Succeed())

					ready := meta.FindStatusCondition(changeTransferPolicy.Status.Conditions, string(promoterConditions.Ready))
					g.Expect(ready).NotTo(BeNil())
					g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))

					g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha).To(Equal(drySha))
					g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).To(Equal(hydratedSha))
					g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).NotTo(Equal(changeTransferPolicy.Status.Active.Hydrated.Sha))

					err := k8sClient.Get(ctx, types.NamespacedName{Name: prName, Namespace: testNamespace}, &promoterv1alpha1.PullRequest{})
					g.Expect(err).NotTo(HaveOccurred())
				}, constants.EventuallyTimeout).Should(Succeed())
			})

			It("reconciles successfully when proposed matches active before any new hydration", func() {
				gitPath, err := cloneTestRepo(ctx, gitRepo)
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()

				_, err = runGitCmd(ctx, gitPath, "fetch", "origin")
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "checkout", "-B", proposedBranch, "origin/"+testBranchDevelopment)
				Expect(err).NotTo(HaveOccurred())
				beforeSha, err := runGitCmd(ctx, gitPath, "rev-parse", proposedBranch)
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "push", "-u", "origin", proposedBranch)
				Expect(err).NotTo(HaveOccurred())
				sendWebhookForPush(ctx, strings.TrimSpace(beforeSha), proposedBranch)

				prName := utils.KubeSafeUniqueName(utils.GetPullRequestName(
					gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name,
					changeTransferPolicy.Spec.ProposedBranch, changeTransferPolicy.Spec.ActiveBranch,
				))

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, ctpNamespacedName, &changeTransferPolicy)).To(Succeed())

					ready := meta.FindStatusCondition(changeTransferPolicy.Status.Conditions, string(promoterConditions.Ready))
					g.Expect(ready).NotTo(BeNil())
					g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))

					g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha).To(BeEmpty())
					g.Expect(changeTransferPolicy.Status.Active.Dry.Sha).To(BeEmpty())
					g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).To(Equal(changeTransferPolicy.Status.Active.Hydrated.Sha))

					err := k8sClient.Get(ctx, types.NamespacedName{Name: prName, Namespace: testNamespace}, &promoterv1alpha1.PullRequest{})
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
			})

			It("fails reconciliation when the hydrator writes metadata outside activePath", func() {
				gitPath, err := cloneTestRepo(ctx, gitRepo)
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()

				drySha, err := makeDryCommit(ctx, gitPath, "dry commit at wrong path")
				Expect(err).NotTo(HaveOccurred())

				beforeSha, hydratedSha, err := pushHydratedBranchForPath(ctx, gitPath, proposedBranch,
					wrongPath, testBranchDevelopment, drySha, "hydrated outside activePath")
				Expect(err).NotTo(HaveOccurred())
				Expect(pushGitNote(ctx, gitPath, hydratedSha, drySha)).To(Succeed())
				sendWebhookForPush(ctx, beforeSha, proposedBranch)

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, ctpNamespacedName, &changeTransferPolicy)).To(Succeed())

					ready := meta.FindStatusCondition(changeTransferPolicy.Status.Conditions, string(promoterConditions.Ready))
					g.Expect(ready).NotTo(BeNil())
					g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
					g.Expect(ready.Reason).To(Equal(string(promoterConditions.ReconciliationError)))

					metadataPath := path.Join(activePathApp, "hydrator.metadata")
					g.Expect(ready.Message).To(Equal(fmt.Sprintf(
						"Reconciliation failed: failed to calculate ChangeTransferPolicy status: proposed branch %q has hydrated commit %s but no dry SHA from %q on that commit; ensure the hydrator writes hydrator.metadata under activePath %q (git note reports dry SHA %s, confirming hydration ran)",
						changeTransferPolicy.Spec.ProposedBranch,
						changeTransferPolicy.Status.Proposed.Hydrated.Sha,
						metadataPath,
						activePathApp,
						drySha,
					)))

					g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha).To(BeEmpty())
					g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).NotTo(BeEmpty())
					g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).NotTo(Equal(changeTransferPolicy.Status.Active.Hydrated.Sha))

					var eventList v1.EventList
					g.Expect(k8sClient.List(ctx, &eventList, ctrlclient.InNamespace(changeTransferPolicy.Namespace))).To(Succeed())
					expectedEventMsg := fmt.Sprintf(constants.MissingProposedHydratorMetadataMessage,
						changeTransferPolicy.Spec.ProposedBranch,
						changeTransferPolicy.Status.Proposed.Hydrated.Sha,
						metadataPath,
					)
					g.Expect(slices.ContainsFunc(eventList.Items, func(e v1.Event) bool {
						return e.InvolvedObject.Name == changeTransferPolicy.Name &&
							e.Reason == constants.MissingProposedHydratorMetadataReason &&
							e.Message == expectedEventMsg
					})).To(BeTrue())

					prList := promoterv1alpha1.PullRequestList{}
					g.Expect(k8sClient.List(ctx, &prList, ctrlclient.InNamespace(changeTransferPolicy.Namespace), ctrlclient.MatchingLabels(map[string]string{
						promoterv1alpha1.ChangeTransferPolicyLabel: utils.KubeSafeLabel(changeTransferPolicy.Name),
					}))).To(Succeed())
					g.Expect(prList.Items).To(BeEmpty())
				}, constants.EventuallyTimeout).Should(Succeed())
			})

			It("fails reconciliation when metadata is outside activePath and no git note exists", func() {
				gitPath, err := cloneTestRepo(ctx, gitRepo)
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()

				drySha, err := makeDryCommit(ctx, gitPath, "dry commit without note")
				Expect(err).NotTo(HaveOccurred())

				beforeSha, _, err := pushHydratedBranchForPath(ctx, gitPath, proposedBranch,
					wrongPath, testBranchDevelopment, drySha, "hydrated outside activePath without note")
				Expect(err).NotTo(HaveOccurred())
				sendWebhookForPush(ctx, beforeSha, proposedBranch)

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, ctpNamespacedName, &changeTransferPolicy)).To(Succeed())

					ready := meta.FindStatusCondition(changeTransferPolicy.Status.Conditions, string(promoterConditions.Ready))
					g.Expect(ready).NotTo(BeNil())
					g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
					g.Expect(ready.Reason).To(Equal(string(promoterConditions.ReconciliationError)))

					metadataPath := path.Join(activePathApp, "hydrator.metadata")
					g.Expect(ready.Message).To(Equal(fmt.Sprintf(
						"Reconciliation failed: failed to calculate ChangeTransferPolicy status: proposed branch %q has hydrated commit %s but no dry SHA from %q on that commit; ensure the hydrator writes hydrator.metadata under activePath %q",
						changeTransferPolicy.Spec.ProposedBranch,
						changeTransferPolicy.Status.Proposed.Hydrated.Sha,
						metadataPath,
						activePathApp,
					)))

					g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha).To(BeEmpty())
					g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).NotTo(BeEmpty())
					g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).NotTo(Equal(changeTransferPolicy.Status.Active.Hydrated.Sha))

					var eventList v1.EventList
					g.Expect(k8sClient.List(ctx, &eventList, ctrlclient.InNamespace(changeTransferPolicy.Namespace))).To(Succeed())
					expectedEventMsg := fmt.Sprintf(constants.MissingProposedHydratorMetadataMessage,
						changeTransferPolicy.Spec.ProposedBranch,
						changeTransferPolicy.Status.Proposed.Hydrated.Sha,
						metadataPath,
					)
					g.Expect(slices.ContainsFunc(eventList.Items, func(e v1.Event) bool {
						return e.InvolvedObject.Name == changeTransferPolicy.Name &&
							e.Reason == constants.MissingProposedHydratorMetadataReason &&
							e.Message == expectedEventMsg
					})).To(BeTrue())

					prList := promoterv1alpha1.PullRequestList{}
					g.Expect(k8sClient.List(ctx, &prList, ctrlclient.InNamespace(changeTransferPolicy.Namespace), ctrlclient.MatchingLabels(map[string]string{
						promoterv1alpha1.ChangeTransferPolicyLabel: utils.KubeSafeLabel(changeTransferPolicy.Name),
					}))).To(Succeed())
					g.Expect(prList.Items).To(BeEmpty())
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		Context("without activePath", func() {
			var (
				gitRepo              *promoterv1alpha1.GitRepository
				changeTransferPolicy *promoterv1alpha1.ChangeTransferPolicy
				ctpNamespacedName    types.NamespacedName
			)

			BeforeEach(func() {
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				var name string
				name, scmSecret, scmProvider, gitRepo, _, changeTransferPolicy = changeTransferPolicyResources(ctx, "ctp-proposed-dry-no-path", testNamespace)

				changeTransferPolicy.Spec.ProposedBranch = testBranchDevelopmentNext
				changeTransferPolicy.Spec.ActiveBranch = testBranchDevelopment
				changeTransferPolicy.Spec.AutoMerge = new(false)

				ctpNamespacedName = types.NamespacedName{Name: name, Namespace: testNamespace}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

				// Wait for the initial reconcile so status.proposed.hydrated.sha is set.
				// Webhook matching uses that field as the push "before" SHA.
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, ctpNamespacedName, changeTransferPolicy)).To(Succeed())
					g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).NotTo(BeEmpty())
					g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).
						To(Equal(changeTransferPolicy.Status.Active.Hydrated.Sha))
				}, constants.EventuallyTimeout).Should(Succeed())
			})

			AfterEach(func() {
				_ = k8sClient.Delete(ctx, changeTransferPolicy)
			})

			It("opens a PR when the hydrator writes metadata at the repository root", func() {
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()

				fullSha, _ := makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, ctpNamespacedName, changeTransferPolicy)).To(Succeed())

					ready := meta.FindStatusCondition(changeTransferPolicy.Status.Conditions, string(promoterConditions.Ready))
					g.Expect(ready).NotTo(BeNil())
					g.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
					g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha).To(Equal(fullSha))
				}, constants.EventuallyTimeout).Should(Succeed())
			})

			It("fails reconciliation when metadata is not at the repository root", func() {
				gitPath, err := cloneTestRepo(ctx, gitRepo)
				Expect(err).NotTo(HaveOccurred())
				defer func() { _ = os.RemoveAll(gitPath) }()

				drySha, err := makeDryCommit(ctx, gitPath, "dry commit for misplaced root metadata")
				Expect(err).NotTo(HaveOccurred())

				beforeSha, _, err := pushHydratedBranchWithMetadataAtOnly(ctx, gitPath, testBranchDevelopmentNext,
					testBranchDevelopment, "apps/nested/hydrator.metadata", drySha, "hydrated under apps/nested")
				Expect(err).NotTo(HaveOccurred())
				sendWebhookForPush(ctx, beforeSha, testBranchDevelopmentNext)

				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, ctpNamespacedName, changeTransferPolicy)).To(Succeed())

					ready := meta.FindStatusCondition(changeTransferPolicy.Status.Conditions, string(promoterConditions.Ready))
					g.Expect(ready).NotTo(BeNil())
					g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
					g.Expect(ready.Reason).To(Equal(string(promoterConditions.ReconciliationError)))

					metadataPath := "hydrator.metadata"
					g.Expect(ready.Message).To(Equal(fmt.Sprintf(
						"Reconciliation failed: failed to calculate ChangeTransferPolicy status: proposed branch %q has hydrated commit %s but no dry SHA from %q on that commit",
						changeTransferPolicy.Spec.ProposedBranch,
						changeTransferPolicy.Status.Proposed.Hydrated.Sha,
						metadataPath,
					)))

					g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha).To(BeEmpty())
					g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).NotTo(BeEmpty())
					g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).NotTo(Equal(changeTransferPolicy.Status.Active.Hydrated.Sha))

					var eventList v1.EventList
					g.Expect(k8sClient.List(ctx, &eventList, ctrlclient.InNamespace(changeTransferPolicy.Namespace))).To(Succeed())
					expectedEventMsg := fmt.Sprintf(constants.MissingProposedHydratorMetadataMessage,
						changeTransferPolicy.Spec.ProposedBranch,
						changeTransferPolicy.Status.Proposed.Hydrated.Sha,
						metadataPath,
					)
					g.Expect(slices.ContainsFunc(eventList.Items, func(e v1.Event) bool {
						return e.InvolvedObject.Name == changeTransferPolicy.Name &&
							e.Reason == constants.MissingProposedHydratorMetadataReason &&
							e.Message == expectedEventMsg
					})).To(BeTrue())

					prList := promoterv1alpha1.PullRequestList{}
					g.Expect(k8sClient.List(ctx, &prList, ctrlclient.InNamespace(changeTransferPolicy.Namespace), ctrlclient.MatchingLabels(map[string]string{
						promoterv1alpha1.ChangeTransferPolicyLabel: utils.KubeSafeLabel(changeTransferPolicy.Name),
					}))).To(Succeed())
					g.Expect(prList.Items).To(BeEmpty())
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		Context("before the hydrator creates the proposed branch", func() {
			var (
				gitRepo              *promoterv1alpha1.GitRepository
				promotionStrategy    *promoterv1alpha1.PromotionStrategy
				changeTransferPolicy promoterv1alpha1.ChangeTransferPolicy
				ctpNamespacedName    types.NamespacedName
			)

			BeforeEach(func() {
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				_, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "ctp-proposed-dry-wait", testNamespace)
				setupInitialTestGitRepoForActivePath(ctx, gitRepo)

				promotionStrategy.Spec.ActivePath = activePathApp
				promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
					{Branch: testBranchDevelopment, AutoMerge: new(false)},
				}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

				ctpNamespacedName = types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(utils.GetChangeTransferPolicyName(promotionStrategy.Name, testBranchDevelopment)),
					Namespace: testNamespace,
				}
			})

			AfterEach(func() {
				_ = k8sClient.Delete(ctx, promotionStrategy)
			})

			It("reports the missing proposed branch instead of MissingProposedHydratorMetadata", func() {
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, ctpNamespacedName, &changeTransferPolicy)).To(Succeed())

					ready := meta.FindStatusCondition(changeTransferPolicy.Status.Conditions, string(promoterConditions.Ready))
					g.Expect(ready).NotTo(BeNil())
					g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
					g.Expect(ready.Reason).To(Equal(string(promoterConditions.ReconciliationError)))
					g.Expect(ready.Message).To(ContainSubstring("branch may not exist yet"))

					var eventList v1.EventList
					g.Expect(k8sClient.List(ctx, &eventList, ctrlclient.InNamespace(testNamespace))).To(Succeed())
					g.Expect(hasEventWithReason(eventList, changeTransferPolicy.Name, constants.MissingProposedHydratorMetadataReason)).To(BeFalse())
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})
	})
})

// hasEventWithReason reports whether eventList contains an event for the named involved object
// with the given reason.
func hasEventWithReason(eventList v1.EventList, involvedName, reason string) bool {
	return hasEventWithReasonAndMessage(eventList, involvedName, reason, "")
}

// hasEventWithReasonAndMessage is hasEventWithReason narrowed to events whose message contains
// the given substring.
func hasEventWithReasonAndMessage(eventList v1.EventList, involvedName, reason, messageSubstring string) bool {
	return slices.ContainsFunc(eventList.Items, func(e v1.Event) bool {
		return e.InvolvedObject.Name == involvedName && e.Reason == reason && strings.Contains(e.Message, messageSubstring)
	})
}

//nolint:unparam // namespace is always "default" in tests but kept for consistency with other test helpers
func changeTransferPolicyResources(ctx context.Context, name, namespace string) (string, *v1.Secret, *promoterv1alpha1.ScmProvider, *promoterv1alpha1.GitRepository, *promoterv1alpha1.CommitStatus, *promoterv1alpha1.ChangeTransferPolicy) {
	name = name + "-" + utils.KubeSafeUniqueName(randomString(15))
	gitRepo := &promoterv1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
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
			Namespace: namespace,
		},
		Data: nil,
	}

	scmProvider := &promoterv1alpha1.ScmProvider{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.ScmProviderSpec{
			SecretRef: &v1.LocalObjectReference{Name: name},
			Fake:      &promoterv1alpha1.Fake{},
		},
		Status: promoterv1alpha1.ScmProviderStatus{},
	}

	commitStatus := &promoterv1alpha1.CommitStatus{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: name,
			},
			Sha:         "",
			Name:        "",
			Description: "",
			Phase:       promoterv1alpha1.CommitPhasePending,
			Url:         "",
		},
	}

	changeTransferPolicy := &promoterv1alpha1.ChangeTransferPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.ChangeTransferPolicySpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: name,
			},
		},
	}

	return name, scmSecret, scmProvider, gitRepo, commitStatus, changeTransferPolicy
}
