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
	"k8s.io/client-go/util/retry"
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

// --- SSA migration bug e2e ----------------------------------------------------
//
// End-to-end reproduction of the "SSA migration" bug as it manifests in the
// CTP controller. So named because it only manifests in clusters that ran a
// pre-Server-Side-Apply version of the promoter (which used
// client.Status().Update(), defaulting to the FieldOwner "gitops-promoter")
// and were then upgraded to the SSA-based code.
//
// The bug requires a specific Server-Side Apply ownership topology in K8s:
//
//   manager `gitops-promoter`                                       Update  owns f:status.f:proposed.f:note.f:drySha
//   manager `promoter.argoproj.io/changetransferpolicy-controller`  Apply   owns f:status.f:proposed.f:note (parent only)
//
// That topology naturally arises in any cluster that ran the pre-SSA
// controller before being upgraded. Until something else writes a different
// value for note.drySha through the new SSA Apply manager, the legacy
// `gitops-promoter` Update record retains exclusive ownership of
// note.drySha. Combined with setCommitMetadata writing
// Note=&HydratorMetadata{DrySha: ""} (which serializes to `note: {}` after
// omitempty drops the empty string), every subsequent SSA Apply by the
// controller silently preserves the stale OLD value of note.drySha while
// still advancing every other field in the patch.
//
// We mimic the legacy state by issuing a manual k8sClient.Status().Update()
// with FieldOwner("gitops-promoter") right after the controller's first
// successful reconcile and before the next promotion-triggering push.

// legacyManagerName is the FieldManager string the pre-SSA controller used,
// inherited from the binary name when no FieldOwner was passed to
// client.Status().Update(). New SSA-based code uses
// "promoter.argoproj.io/changetransferpolicy-controller" instead.
//
// Defined here (rather than in the PS test) because the CTP test was the
// first to need it; the PS test's injectFullStatus reuses it via the shared
// `controller` package scope.
const legacyManagerName = "gitops-promoter"

// retrieveCTP gets a CTP and asserts the lookup succeeds. Tiny helper to avoid
// the same Get+Expect dance scattered through the eventually blocks. Shared
// with the PromotionStrategy SSA-migration-bug test via package scope.
func retrieveCTP(ctx context.Context, name string) *promoterv1alpha1.ChangeTransferPolicy {
	ctp := &promoterv1alpha1.ChangeTransferPolicy{}
	Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, ctp)).To(Succeed())
	return ctp
}

// noteDrySha returns the DrySha of a HydratorMetadata pointer, or "" if the
// pointer is nil. Lets assertions express "Note may be either nil or have
// empty drySha" with a single string compare.
func noteDrySha(n *promoterv1alpha1.HydratorMetadata) string {
	if n == nil {
		return ""
	}
	return n.DrySha
}

// injectLegacyNoteOwnership simulates a pre-SSA-migration controller having
// written Status.Proposed.Note.DrySha via an imperative client.Status().Update().
// After this call, the apiserver records that the manager `gitops-promoter`
// (operation=Update on the /status subresource) owns
// f:status.f:proposed.f:note.f:drySha with the supplied drySha value.
// Subsequent SSA Apply patches by the changetransferpolicy-controller manager
// that omit drySha will not be able to remove the field because gitops-promoter
// still owns it.
func injectLegacyNoteOwnership(ctx context.Context, key types.NamespacedName, drySha string) {
	GinkgoHelper()
	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		ctp := &promoterv1alpha1.ChangeTransferPolicy{}
		if err := k8sClient.Get(ctx, key, ctp); err != nil {
			return err //nolint:wrapcheck // retry.RetryOnConflict expects bare errors from client.Get
		}
		// Preserve whatever Status.Proposed already had so the imperative Update
		// claims ownership of those fields too (matching production where the
		// pre-migration controller wrote the entire status). Then overwrite Note
		// with the desired drySha value.
		ctp.Status.Proposed.Note = &promoterv1alpha1.HydratorMetadata{DrySha: drySha}
		return k8sClient.Status().Update(ctx, ctp, ctrlclient.FieldOwner(legacyManagerName)) //nolint:wrapcheck // retry.RetryOnConflict expects bare client errors
	})).To(Succeed())
}

var _ = Describe("SSA migration bug e2e (CTP controller produces stale Note.DrySha after legacy Update)", Ordered, func() {
	var (
		ctx                = context.Background()
		ctpName            string
		gitRepo            *promoterv1alpha1.GitRepository
		ctp                *promoterv1alpha1.ChangeTransferPolicy
		gitPath            string
		initialDrySha      string
		typeNamespacedName types.NamespacedName
	)

	BeforeAll(func() {
		var (
			scmSecret   *v1.Secret
			scmProvider *promoterv1alpha1.ScmProvider
		)
		ctpName, scmSecret, scmProvider, gitRepo, _, ctp = changeTransferPolicyResources(ctx, "ssa-mig-bug-ctp", "default")
		typeNamespacedName = types.NamespacedName{Name: ctpName, Namespace: "default"}

		ctp.Spec.ProposedBranch = testBranchDevelopmentNext
		ctp.Spec.ActiveBranch = testBranchDevelopment
		// AutoMerge=false so the live controller doesn't merge the PR out from under
		// us between the legacy-ownership injection and the new dry SHA push.
		ctp.Spec.AutoMerge = ptr.To(false)

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Create(ctx, ctp)).To(Succeed())

		var err error
		gitPath, err = os.MkdirTemp("", "*")
		Expect(err).NotTo(HaveOccurred())

		// Push the initial dry SHA + hydrated branches via the standard test helper.
		// makeChangeAndHydrateRepo writes a hydrator.metadata FILE in the proposed
		// branch tree, but does NOT push a git note for the hydrated commit. That
		// means setCommitMetadata's GetHydratorNote always returns an empty
		// HydratorMetadata{}, and the controller will set Note.DrySha = "" in
		// memory on every reconcile - exactly the production setCommitMetadata
		// behavior when the hydrator hasn't pushed its git note yet.
		initialDrySha, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")

		By("Waiting for the CTP controller to reach baseline state on the initial dry SHA")
		Eventually(func(g Gomega) {
			ctp = retrieveCTP(ctx, ctpName)
			g.Expect(ctp.Status.Proposed.Dry.Sha).To(Equal(initialDrySha))
			g.Expect(ctp.Status.Proposed.Hydrated.Sha).ToNot(BeEmpty())
		}, constants.EventuallyTimeout).Should(Succeed())
	})

	AfterAll(func() {
		_ = k8sClient.Delete(ctx, ctp)
	})

	It("after a new dry SHA push, Note.DrySha must NOT remain stuck at the legacy Update's old value", func() {
		By("Injecting legacy gitops-promoter Update ownership of f:status.f:proposed.f:note.f:drySha")
		injectLegacyNoteOwnership(ctx, typeNamespacedName, initialDrySha)

		By("Pushing a new dry SHA + new hydrated commit (still no git note pushed by the hydrator)")
		newDrySha, _ := makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")
		Expect(newDrySha).NotTo(Equal(initialDrySha))

		By("Waiting for the CTP controller to advance Status.Proposed.Dry.Sha to the new SHA")
		Eventually(func(g Gomega) {
			ctp = retrieveCTP(ctx, ctpName)
			g.Expect(ctp.Status.Proposed.Dry.Sha).To(Equal(newDrySha))
		}, constants.EventuallyTimeout).Should(Succeed())

		By("Asserting Note.DrySha reflects the controller's in-memory write (empty), NOT the stale legacy value")
		// setCommitMetadata wrote Note=&HydratorMetadata{DrySha: ""} in memory
		// because there is no git note for the new hydrated commit. The K8s state
		// must reflect that intent.
		//
		// Today (RED): K8s preserves Note.DrySha at initialDrySha because the
		// legacy gitops-promoter Update record still owns f:note.f:drySha and the
		// controller's SSA Apply with `note: {}` releases ownership without
		// overwriting the value (per K8s SSA's child-field-preservation rule).
		//
		// Post-fix (GREEN): Note.DrySha is either "" or absent (Note=nil). The fix
		// should ensure the controller's Apply payload claims/forces ownership of
		// f:note.f:drySha (so the legacy ownership doesn't preserve it), or that
		// the legacy ownership record is migrated/evicted at controller startup.
		ctp = retrieveCTP(ctx, ctpName)
		Expect(ctp.Status.Proposed.Dry.Sha).To(Equal(newDrySha),
			"sanity: Dry.Sha must have advanced for this assertion to be meaningful")
		Expect(noteDrySha(ctp.Status.Proposed.Note)).ToNot(Equal(initialDrySha),
			"POST-FIX EXPECTATION: Note.DrySha=%q must NOT equal the stale legacy value %q. "+
				"setCommitMetadata wrote Note=&{DrySha:\"\"} in memory; the K8s state must "+
				"reflect that (Note.DrySha=\"\" or Note=nil). Today this fails because the "+
				"legacy gitops-promoter Update record retains ownership of f:note.f:drySha and "+
				"SSA's child-field-preservation rule keeps the OLD value while the rest of "+
				"Status.Proposed advances. The fix must ensure the controller's apply either "+
				"takes ownership of f:note.f:drySha (e.g. via an explicit ApplyConfiguration "+
				"that includes drySha) or evicts the legacy ownership record so the inconsistent "+
				"(Dry advanced, Note stale) state cannot form.",
			noteDrySha(ctp.Status.Proposed.Note), initialDrySha)
	})
})
