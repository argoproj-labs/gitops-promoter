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
	"strings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
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
	"k8s.io/utils/ptr"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
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
				changeTransferPolicy.Spec.AutoMerge = ptr.To(false)

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
						Name:      utils.KubeSafeUniqueName(ctx, prName),
						Namespace: "default",
					}
					err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
					g.Expect(err).To(Succeed())
					g.Expect(pr.Spec.Title).To(Equal(fmt.Sprintf("Promote %s to `%s`", shortSha, testBranchDevelopment)))
					g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
					g.Expect(pr.Name).To(Equal(utils.KubeSafeUniqueName(ctx, prName)))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Adding another pending commit")
				_, shortSha = makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, prName),
						Namespace: "default",
					}, &pr)
					g.Expect(err).To(Succeed())
					g.Expect(pr.Spec.Title).To(Equal(fmt.Sprintf("Promote %s to `%s`", shortSha, testBranchDevelopment)))
					g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
					g.Expect(pr.Name).To(Equal(utils.KubeSafeUniqueName(ctx, prName)))
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					err = k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
					Expect(err).To(Succeed())
					// We now have a PR so we can set it to true and then check that it gets merged
					changeTransferPolicy.Spec.AutoMerge = ptr.To(true)
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
						Name:      utils.KubeSafeUniqueName(ctx, prName),
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
				changeTransferPolicy.Spec.AutoMerge = ptr.To(false)

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
					changeTransferPolicy.Spec.AutoMerge = ptr.To(true)
					err = k8sClient.Update(ctx, changeTransferPolicy)
					g.Expect(err).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					typeNamespacedNamePR := types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, prName),
						Namespace: "default",
					}
					err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
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
				changeTransferPolicy.Spec.AutoMerge = ptr.To(false)

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
						Name:      utils.KubeSafeUniqueName(ctx, prName),
						Namespace: "default",
					}
					err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
					g.Expect(err).To(Succeed())
					g.Expect(pr.Spec.Title).To(Equal(fmt.Sprintf("Promote %s to `%s`", shortSha, testBranchDevelopment)))
					g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
					g.Expect(pr.Name).To(Equal(utils.KubeSafeUniqueName(ctx, prName)))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Adding another pending commit")
				_, shortSha = makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, prName),
						Namespace: "default",
					}, &pr)
					g.Expect(err).To(Succeed())
					g.Expect(pr.Spec.Title).To(Equal(fmt.Sprintf("Promote %s to `%s`", shortSha, testBranchDevelopment)))
					g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
					g.Expect(pr.Name).To(Equal(utils.KubeSafeUniqueName(ctx, prName)))
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					err = k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
					Expect(err).To(Succeed())
					// We now have a PR so we can set it to true and then check that it gets merged
					changeTransferPolicy.Spec.AutoMerge = ptr.To(true)
					err = k8sClient.Update(ctx, changeTransferPolicy)
					g.Expect(err).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					typeNamespacedNamePR := types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, prName),
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
				changeTransferPolicy.Spec.AutoMerge = ptr.To(false)

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
						Name:      utils.KubeSafeUniqueName(ctx, prName),
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
				changeTransferPolicy.Spec.AutoMerge = ptr.To(false)

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
				changeTransferPolicy.Spec.AutoMerge = ptr.To(false)

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
						Name:      utils.KubeSafeUniqueName(ctx, prName),
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
						Name:      utils.KubeSafeUniqueName(ctx, prName),
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
					createdPR.Status.ExternallyMergedOrClosed = ptr.To(true)
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
				changeTransferPolicy.Spec.AutoMerge = ptr.To(false)

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

			FIt("should not surface a status.proposed.note SSA validation error", func() {
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

//nolint:unparam // namespace is always "default" in tests but kept for consistency with other test helpers
func changeTransferPolicyResources(ctx context.Context, name, namespace string) (string, *v1.Secret, *promoterv1alpha1.ScmProvider, *promoterv1alpha1.GitRepository, *promoterv1alpha1.CommitStatus, *promoterv1alpha1.ChangeTransferPolicy) {
	name = name + "-" + utils.KubeSafeUniqueName(ctx, randomString(15))
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
