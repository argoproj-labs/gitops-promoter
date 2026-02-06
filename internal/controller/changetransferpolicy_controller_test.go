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
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
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
				fullSha, shortSha := makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, "", "")

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
				_, shortSha = makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, "", "")

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
				makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, "", "")

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
				fullSha, shortSha := makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, "", "")

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
				_, shortSha = makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, "", "")

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
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, "", "")

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
				makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, "", "")

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

		Context("When active branch is squash-merged externally", func() {
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
				name, scmSecret, scmProvider, gitRepo, _, changeTransferPolicy = changeTransferPolicyResources(ctx, "ctp-squash-merge", "default")

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
				Expect(k8sClient.Delete(ctx, changeTransferPolicy)).To(Succeed())
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			})

			It("should resolve branch divergence after squash merge and show only new commits in PR", func() {
				By("Making the first change and hydrating")
				firstDrySha, _ := makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, "", "")

				By("Waiting for PR to be created for the first change")
				var pr promoterv1alpha1.PullRequest
				Eventually(func(g Gomega) {
					typeNamespacedNamePR := types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, prName),
						Namespace: "default",
					}
					err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
					g.Expect(err).To(Succeed())
					g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Simulating a squash merge on the active branch")
				squashDir, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				repoURL := fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPort, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)
				_, err = runGitCmd(ctx, squashDir, "clone", repoURL, ".")
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, squashDir, "config", "user.name", "testuser")
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, squashDir, "config", "user.email", "testmail@test.com")
				Expect(err).NotTo(HaveOccurred())

				// Get the current active branch SHA before squash merge (for webhook)
				_, err = runGitCmd(ctx, squashDir, "fetch", "origin")
				Expect(err).NotTo(HaveOccurred())
				beforeSquashSha, err := runGitCmd(ctx, squashDir, "rev-parse", "origin/"+testBranchDevelopment)
				Expect(err).NotTo(HaveOccurred())
				beforeSquashSha = strings.TrimSpace(beforeSquashSha)

				// Checkout active branch and squash merge proposed into it
				_, err = runGitCmd(ctx, squashDir, "checkout", "-B", testBranchDevelopment, "origin/"+testBranchDevelopment)
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, squashDir, "merge", "--squash", "origin/"+testBranchDevelopmentNext)
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, squashDir, "commit", "-m", "squash merge from proposed")
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, squashDir, "push", "origin", testBranchDevelopment)
				Expect(err).NotTo(HaveOccurred())

				// Trigger CTP reconciliation to pick up the squash merge on the active branch
				sendWebhookForPush(ctx, beforeSquashSha, testBranchDevelopment)
				enqueueCTP(changeTransferPolicy.Namespace, changeTransferPolicy.Name)

				By("Waiting for CTP to see the squash merge on active (active dry sha matches first dry sha)")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
					g.Expect(err).To(Succeed())
					g.Expect(changeTransferPolicy.Status.Active.Dry.Sha).To(Equal(firstDrySha))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Making a second change on the dry branch and hydrating proposed")
				// Get the default branch name
				defaultBranch, err := runGitCmd(ctx, squashDir, "rev-parse", "--abbrev-ref", "origin/HEAD")
				Expect(err).NotTo(HaveOccurred())
				defaultBranch = strings.TrimSpace(defaultBranch)
				defaultBranch, _ = strings.CutPrefix(defaultBranch, "origin/")

				// Checkout and pull dry branch
				_, err = runGitCmd(ctx, squashDir, "checkout", defaultBranch)
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, squashDir, "pull")
				Expect(err).NotTo(HaveOccurred())

				// Make a new dry commit
				f, err := os.Create(path.Join(squashDir, "manifests-fake.yaml"))
				Expect(err).NotTo(HaveOccurred())
				_, err = f.WriteString(fmt.Sprintf(`{"time": "%s"}`, time.Now().Format(time.RFC3339Nano)))
				Expect(err).NotTo(HaveOccurred())
				err = f.Close()
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, squashDir, "add", "manifests-fake.yaml")
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, squashDir, "commit", "-m", "second dry commit")
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, squashDir, "push", "origin", defaultBranch)
				Expect(err).NotTo(HaveOccurred())

				secondDrySha, err := runGitCmd(ctx, squashDir, "rev-parse", defaultBranch)
				Expect(err).NotTo(HaveOccurred())
				secondDrySha = strings.TrimSpace(secondDrySha)
				secondDryShort, err := runGitCmd(ctx, squashDir, "rev-parse", "--short=7", defaultBranch)
				Expect(err).NotTo(HaveOccurred())
				secondDryShort = strings.TrimSpace(secondDryShort)

				// Get the current proposed branch SHA before hydration push (for webhook)
				beforeHydrateSha, err := runGitCmd(ctx, squashDir, "rev-parse", "origin/"+testBranchDevelopmentNext)
				Expect(err).NotTo(HaveOccurred())
				beforeHydrateSha = strings.TrimSpace(beforeHydrateSha)

				// Hydrate dev-next with the second dry SHA
				_, err = runGitCmd(ctx, squashDir, "checkout", "-B", testBranchDevelopmentNext, "origin/"+testBranchDevelopmentNext)
				Expect(err).NotTo(HaveOccurred())

				metadata := git.HydratorMetadata{
					DrySha:  secondDrySha,
					Author:  "testuser <testmail@test.com>",
					Date:    metav1.Now(),
					Subject: "second dry commit",
				}
				m, err := json.MarshalIndent(metadata, "", "\t")
				Expect(err).NotTo(HaveOccurred())

				f, err = os.Create(path.Join(squashDir, "hydrator.metadata"))
				Expect(err).NotTo(HaveOccurred())
				_, err = f.Write(m)
				Expect(err).NotTo(HaveOccurred())
				err = f.Close()
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, squashDir, "add", "hydrator.metadata")
				Expect(err).NotTo(HaveOccurred())

				f, err = os.Create(path.Join(squashDir, "manifests-fake.yaml"))
				Expect(err).NotTo(HaveOccurred())
				_, err = f.WriteString(fmt.Sprintf(`{"time": "%s"}`, time.Now().Format(time.RFC3339Nano)))
				Expect(err).NotTo(HaveOccurred())
				err = f.Close()
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, squashDir, "add", "manifests-fake.yaml")
				Expect(err).NotTo(HaveOccurred())

				_, err = runGitCmd(ctx, squashDir, "commit", "-m", "hydrated for second dry sha "+secondDrySha)
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, squashDir, "push", "origin", testBranchDevelopmentNext)
				Expect(err).NotTo(HaveOccurred())

				// Trigger CTP reconciliation to pick up the new hydration on proposed
				sendWebhookForPush(ctx, beforeHydrateSha, testBranchDevelopmentNext)
				enqueueCTP(changeTransferPolicy.Namespace, changeTransferPolicy.Name)

				By("Verifying CTP detects divergence, resolves it, and creates PR with the new commit")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
					g.Expect(err).To(Succeed())
					// The proposed dry SHA should be the second commit
					g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha).To(Equal(secondDrySha))
					// The active dry SHA should still be the first commit (from squash merge)
					g.Expect(changeTransferPolicy.Status.Active.Dry.Sha).To(Equal(firstDrySha))
				}, constants.EventuallyTimeout).Should(Succeed())

				// The PR should reflect only the new change (title uses secondDryShort)
				Eventually(func(g Gomega) {
					typeNamespacedNamePR := types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, prName),
						Namespace: "default",
					}
					err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
					g.Expect(err).To(Succeed())
					g.Expect(pr.Spec.Title).To(Equal(fmt.Sprintf("Promote %s to `%s`", secondDryShort, testBranchDevelopment)))
					g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
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
				Expect(k8sClient.Delete(ctx, changeTransferPolicy)).To(Succeed())
				Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			})

			It("should remove CTP finalizer from PR when PR is externally closed and status is synced", func() {
				By("Adding a pending commit")
				_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, "", "")

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

//nolint:unparam
func changeTransferPolicyResources(ctx context.Context, name, namespace string) (string, *v1.Secret, *promoterv1alpha1.ScmProvider, *promoterv1alpha1.GitRepository, *promoterv1alpha1.CommitStatus, *promoterv1alpha1.ChangeTransferPolicy) {
	name = name + "-" + utils.KubeSafeUniqueName(ctx, randomString(15))
	setupInitialTestGitRepoOnServer(ctx, name, name)

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
