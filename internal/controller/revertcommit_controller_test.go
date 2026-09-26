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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

//go:embed testdata/RevertCommit.yaml
var testRevertCommitYAML string

var _ = Describe("RevertCommit Controller", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the RevertCommit resource", func() {
			Expect(unmarshalYamlStrict(testRevertCommitYAML, &promoterv1alpha1.RevertCommit{})).To(Succeed())
		})
	})

	Context("When the ChangeTransferPolicy does not exist", func() {
		It("reports the missing policy on the Ready condition", func() {
			ctx := context.Background()
			name := "revert-missing-" + utils.KubeSafeUniqueName(randomString(10))
			rc := &promoterv1alpha1.RevertCommit{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: promoterv1alpha1.RevertCommitSpec{
					ChangeTransferPolicyRef: promoterv1alpha1.ObjectReference{Name: "does-not-exist"},
					Sha:                     "abcdef1234567890abcdef1234567890abcdef12",
				},
			}
			Expect(k8sClient.Create(ctx, rc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, rc) })

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, rc)).To(Succeed())
				cond := meta.FindStatusCondition(rc.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Message).To(ContainSubstring("does-not-exist"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When the spec is updated", func() {
		It("rejects changes to spec.sha and spec.changeTransferPolicyRef", func() {
			ctx := context.Background()
			name := "revert-immutable-" + utils.KubeSafeUniqueName(randomString(10))
			rc := &promoterv1alpha1.RevertCommit{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: promoterv1alpha1.RevertCommitSpec{
					ChangeTransferPolicyRef: promoterv1alpha1.ObjectReference{Name: "does-not-exist"},
					Sha:                     "abcdef1234567890abcdef1234567890abcdef12",
				},
			}
			Expect(k8sClient.Create(ctx, rc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, rc) })
			key := types.NamespacedName{Name: name, Namespace: "default"}

			// The controller patches ownerReferences and status right after create, so retry
			// resourceVersion conflicts until the update reaches the validation rule.
			updateSpec := func(mutate func(*promoterv1alpha1.RevertCommitSpec)) error {
				return retry.RetryOnConflict(retry.DefaultRetry, func() error {
					var live promoterv1alpha1.RevertCommit
					Expect(k8sClient.Get(ctx, key, &live)).To(Succeed())
					mutate(&live.Spec)
					if err := k8sClient.Update(ctx, &live); err != nil {
						return fmt.Errorf("update RevertCommit spec: %w", err)
					}
					return nil
				})
			}

			err := updateSpec(func(spec *promoterv1alpha1.RevertCommitSpec) {
				spec.Sha = "1234567890abcdef1234567890abcdef12345678"
			})
			Expect(err).To(MatchError(ContainSubstring("spec is immutable")))

			err = updateSpec(func(spec *promoterv1alpha1.RevertCommitSpec) {
				spec.ChangeTransferPolicyRef.Name = "another-policy"
			})
			Expect(err).To(MatchError(ContainSubstring("spec is immutable")))

			By("still allowing metadata changes")
			Eventually(func(g Gomega) {
				var live promoterv1alpha1.RevertCommit
				g.Expect(k8sClient.Get(ctx, key, &live)).To(Succeed())
				if live.Labels == nil {
					live.Labels = map[string]string{}
				}
				live.Labels["example"] = "value"
				g.Expect(k8sClient.Update(ctx, &live)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When restoring an active branch", func() {
		It("pushes a restore commit and leaves the proposed branch in place", func() {
			ctx := context.Background()
			name, scmSecret, scmProvider, gitRepo, _, ctp := changeTransferPolicyResources(ctx, "revert-active", "default")
			ctp.Spec.ActiveBranch = testBranchDevelopment
			ctp.Spec.ProposedBranch = testBranchDevelopmentNext
			autoMerge := false
			ctp.Spec.AutoMerge = &autoMerge

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, ctp)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, ctp)
				_ = k8sClient.Delete(ctx, gitRepo)
				_ = k8sClient.Delete(ctx, scmProvider)
				_ = k8sClient.Delete(ctx, scmSecret)
			})

			gitPath, err := os.MkdirTemp("", "revert-commit-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = os.RemoveAll(gitPath) })

			mustRun := func(args ...string) string {
				GinkgoHelper()
				out, err := runGitCmd(ctx, gitPath, args...)
				Expect(err).NotTo(HaveOccurred())
				return strings.TrimSpace(out)
			}

			mustRun("clone", testGitRepoCloneURL(gitRepo), ".")
			mustRun("config", "user.name", "testuser")
			mustRun("config", "user.email", "testemail@test.com")
			mustRun("config", "commit.gpgsign", "false")
			mustRun("checkout", testBranchDevelopment)

			Expect(os.WriteFile(path.Join(gitPath, "version.txt"), []byte("v1\n"), 0o644)).To(Succeed())
			mustRun("add", "version.txt")
			mustRun("commit", "-m", "version v1")
			v1Sha := mustRun("rev-parse", "HEAD")
			mustRun("push", "origin", "HEAD:refs/heads/"+testBranchDevelopment)
			mustRun("push", "origin", "HEAD:refs/heads/"+testBranchDevelopmentNext)

			note := `{"Pull-request-id":["9"],"Pull-request-merge-time":["2020-01-01T00:00:00Z"]}`
			mustRun("notes", "--ref="+git.PromoterHistoryNotesRef, "add", "-m", note, v1Sha)
			mustRun("push", "origin", git.PromoterHistoryNotesRef)

			activeDry := "5555555555555555555555555555555555555555"
			Expect(os.WriteFile(path.Join(gitPath, "version.txt"), []byte("v2\n"), 0o644)).To(Succeed())
			Expect(os.WriteFile(path.Join(gitPath, "hydrator.metadata"), []byte(`{"drySha":"`+activeDry+`"}`), 0o644)).To(Succeed())
			mustRun("add", "version.txt", "hydrator.metadata")
			mustRun("commit", "-m", "version v2")
			v2Sha := mustRun("rev-parse", "HEAD")
			mustRun("push", "origin", "HEAD:refs/heads/"+testBranchDevelopment)

			mustRun("checkout", "-B", testBranchDevelopmentNext, "origin/"+testBranchDevelopmentNext)
			Expect(os.WriteFile(path.Join(gitPath, "extra.txt"), []byte("keep\n"), 0o644)).To(Succeed())
			mustRun("add", "extra.txt")
			mustRun("commit", "-m", "proposed only")
			proposedTip := mustRun("rev-parse", "HEAD")
			mustRun("push", "origin", "HEAD:refs/heads/"+testBranchDevelopmentNext)

			rcName := name + "-rc"
			rc := &promoterv1alpha1.RevertCommit{
				ObjectMeta: metav1.ObjectMeta{Name: rcName, Namespace: "default"},
				Spec: promoterv1alpha1.RevertCommitSpec{
					ChangeTransferPolicyRef: promoterv1alpha1.ObjectReference{Name: ctp.Name},
					Sha:                     v1Sha,
				},
			}
			Expect(k8sClient.Create(ctx, rc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, rc) })

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rcName, Namespace: "default"}, rc)).To(Succeed())
				cond := meta.FindStatusCondition(rc.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(rc.Status.RestoredFrom).To(Equal(v1Sha))
				g.Expect(rc.Status.ActiveSha).NotTo(BeEmpty())
				g.Expect(rc.Status.BlockedDrySha).To(Equal(activeDry))
				g.Expect(rc.OwnerReferences).To(HaveLen(1))
				g.Expect(rc.OwnerReferences[0].Name).To(Equal(ctp.Name))
				g.Expect(rc.OwnerReferences[0].UID).To(Equal(ctp.UID))
				g.Expect(rc.OwnerReferences[0].Controller).To(HaveValue(BeTrue()))
			}, constants.EventuallyTimeout).Should(Succeed())

			mustRun("fetch", "origin", testBranchDevelopment, testBranchDevelopmentNext)
			mustRun("fetch", "origin", "+"+git.PromoterHistoryNotesRef+":"+git.PromoterHistoryNotesRef)

			activeSha := mustRun("rev-parse", "origin/"+testBranchDevelopment)
			Expect(activeSha).To(Equal(rc.Status.ActiveSha))
			Expect(activeSha).NotTo(Equal(v1Sha))
			Expect(mustRun("rev-parse", activeSha+"^")).To(Equal(v2Sha))
			Expect(mustRun("rev-parse", activeSha+"^{tree}")).To(Equal(mustRun("rev-parse", v1Sha+"^{tree}")))
			Expect(mustRun("show", activeSha+":version.txt")).To(Equal("v1"))

			proposedSha := mustRun("rev-parse", "origin/"+testBranchDevelopmentNext)
			Expect(proposedSha).To(Equal(proposedTip))
			Expect(mustRun("show", proposedSha+":extra.txt")).To(Equal("keep"))

			rawNote := mustRun("notes", "--ref="+git.PromoterHistoryNotesRef, "show", activeSha)
			var got map[string][]string
			Expect(json.Unmarshal([]byte(rawNote), &got)).To(Succeed())
			Expect(got[constants.TrailerRestoredFrom]).To(Equal([]string{v1Sha}))
			Expect(got[constants.TrailerPullRequestID]).To(Equal([]string{"9"}))
			Expect(got[constants.TrailerPullRequestMergeTime]).To(Equal([]string{"2020-01-01T00:00:00Z"}))
		})
	})

	Context("When a PromotionStrategy owns the policy", func() {
		It("restores one environment after a real promotion and holds the next one", func() {
			ctx := context.Background()
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy := promotionStrategyResource(ctx, "revert-ps", "default")
			setupInitialTestGitRepoOnServer(ctx, gitRepo)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			declareDependentsSuccessfulGate(promotionStrategy)
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			createDependentsSuccessfulCommitStatus(ctx, promotionStrategy)
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, promotionStrategy)
				_ = k8sClient.Delete(ctx, &promoterv1alpha1.DependentsSuccessfulCommitStatus{
					ObjectMeta: metav1.ObjectMeta{Name: promotionStrategy.Name, Namespace: "default"},
				})
				_ = k8sClient.Delete(ctx, gitRepo)
				_ = k8sClient.Delete(ctx, scmProvider)
				_ = k8sClient.Delete(ctx, scmSecret)
			})

			ctpKey := func(branch string) types.NamespacedName {
				return types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(utils.GetChangeTransferPolicyName(promotionStrategy.Name, branch)),
					Namespace: "default",
				}
			}
			devKey := ctpKey(testBranchDevelopment)
			stagingKey := ctpKey(testBranchStaging)
			prodKey := ctpKey(testBranchProduction)

			var ctpDev, ctpStaging, ctpProd promoterv1alpha1.ChangeTransferPolicy
			By("Waiting for the PromotionStrategy to create a ChangeTransferPolicy per environment")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, devKey, &ctpDev)).To(Succeed())
				g.Expect(ctpDev.Spec.ActiveBranch).To(Equal(testBranchDevelopment))
				g.Expect(ctpDev.Spec.ProposedBranch).To(Equal(testBranchDevelopmentNext))
				g.Expect(ctpDev.OwnerReferences).To(HaveLen(1))
				g.Expect(ctpDev.OwnerReferences[0].Name).To(Equal(promotionStrategy.Name))
				g.Expect(ctpDev.OwnerReferences[0].Kind).To(Equal("PromotionStrategy"))
				g.Expect(k8sClient.Get(ctx, stagingKey, &ctpStaging)).To(Succeed())
				g.Expect(k8sClient.Get(ctx, prodKey, &ctpProd)).To(Succeed())
				g.Expect(ctpDev.Status.Active.Dry.Sha).NotTo(BeEmpty())
				g.Expect(ctpStaging.Status.Active.Dry.Sha).NotTo(BeEmpty())
				g.Expect(ctpProd.Status.Active.Dry.Sha).NotTo(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			gitPath, err := os.MkdirTemp("", "revert-ps-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = os.RemoveAll(gitPath) })

			mustRun := func(args ...string) string {
				GinkgoHelper()
				out, err := runGitCmd(ctx, gitPath, args...)
				Expect(err).NotTo(HaveOccurred())
				return strings.TrimSpace(out)
			}
			mustRun("clone", testGitRepoCloneURL(gitRepo), ".")
			mustRun("config", "user.name", "testuser")
			mustRun("config", "user.email", "testemail@test.com")
			mustRun("config", "commit.gpgsign", "false")

			hydrate := func(message string) string {
				GinkgoHelper()
				dir, err := os.MkdirTemp("", "revert-ps-hydrate-*")
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() { _ = os.RemoveAll(dir) })
				drySha, _ := makeChangeAndHydrateRepo(dir, gitRepo, message, "")
				return drySha
			}

			waitUntilActiveDry := func(drySha string) {
				GinkgoHelper()
				Eventually(func(g Gomega) {
					g.Expect(k8sClient.Get(ctx, devKey, &ctpDev)).To(Succeed())
					g.Expect(ctpDev.Status.Active.Dry.Sha).To(Equal(drySha))
					g.Expect(k8sClient.Get(ctx, stagingKey, &ctpStaging)).To(Succeed())
					g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Equal(drySha))
					g.Expect(k8sClient.Get(ctx, prodKey, &ctpProd)).To(Succeed())
					g.Expect(ctpProd.Status.Active.Dry.Sha).To(Equal(drySha))
				}, constants.EventuallyTimeout).Should(Succeed())
			}

			By("Promoting a first commit so the restore target carries a real history note")
			drySha1 := hydrate("first promotion")
			var restoreTo string
			var firstNote map[string][]string
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, devKey, &ctpDev)).To(Succeed())
				g.Expect(ctpDev.Status.Active.Dry.Sha).To(Equal(drySha1))
				restoreTo = ctpDev.Status.Active.Hydrated.Sha
				g.Expect(restoreTo).NotTo(BeEmpty())
				_, err := runGitCmd(ctx, gitPath, "fetch", "origin", testBranchDevelopment)
				g.Expect(err).NotTo(HaveOccurred())
				firstNote, err = fetchPromotionHistoryNote(gitPath, restoreTo)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(firstNote[constants.TrailerPullRequestID]).NotTo(BeEmpty())
				g.Expect(firstNote[constants.TrailerPullRequestMergeTime]).NotTo(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())
			waitUntilActiveDry(drySha1)

			By("Promoting a second commit, which is the dry SHA the restore will block")
			drySha2 := hydrate("second promotion")
			waitUntilActiveDry(drySha2)

			mustRun("fetch", "origin", testBranchDevelopment, testBranchDevelopmentNext)
			rolledOff := mustRun("rev-parse", "origin/"+testBranchDevelopment)
			proposedTip := mustRun("rev-parse", "origin/"+testBranchDevelopmentNext)
			Expect(k8sClient.Get(ctx, devKey, &ctpDev)).To(Succeed())
			Expect(ctpDev.Status.Active.Hydrated.Sha).To(Equal(rolledOff))
			Expect(rolledOff).NotTo(Equal(restoreTo))

			By("Restoring development to the first promotion")
			rcName := name + "-rc"
			rc := &promoterv1alpha1.RevertCommit{
				ObjectMeta: metav1.ObjectMeta{Name: rcName, Namespace: "default"},
				Spec: promoterv1alpha1.RevertCommitSpec{
					ChangeTransferPolicyRef: promoterv1alpha1.ObjectReference{Name: ctpDev.Name},
					Sha:                     restoreTo,
				},
			}
			Expect(k8sClient.Create(ctx, rc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, rc) })

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rcName, Namespace: "default"}, rc)).To(Succeed())
				cond := meta.FindStatusCondition(rc.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(rc.Status.RestoredFrom).To(Equal(restoreTo))
				g.Expect(rc.Status.ActiveSha).NotTo(BeEmpty())
				g.Expect(rc.Status.BlockedDrySha).To(Equal(drySha2))
				g.Expect(rc.OwnerReferences).To(HaveLen(1))
				g.Expect(rc.OwnerReferences[0].Name).To(Equal(ctpDev.Name))
				g.Expect(rc.OwnerReferences[0].UID).To(Equal(ctpDev.UID))
				g.Expect(rc.OwnerReferences[0].Controller).To(HaveValue(BeTrue()))

				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: promotionStrategy.Name, Namespace: "default"}, promotionStrategy)).To(Succeed())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
				for _, env := range promotionStrategy.Status.Environments {
					switch env.Branch {
					case testBranchDevelopment:
						g.Expect(env.Active.Dry.Sha).To(Equal(drySha1))
					case testBranchStaging, testBranchProduction:
						g.Expect(env.Active.Dry.Sha).To(Equal(drySha2))
					default:
						g.Expect(env.Branch).To(BeElementOf(testBranchDevelopment, testBranchStaging, testBranchProduction))
					}
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			mustRun("fetch", "origin", testBranchDevelopment, testBranchDevelopmentNext)
			mustRun("fetch", "origin", "+"+git.PromoterHistoryNotesRef+":"+git.PromoterHistoryNotesRef)
			activeSha := mustRun("rev-parse", "origin/"+testBranchDevelopment)
			Expect(activeSha).To(Equal(rc.Status.ActiveSha))
			Expect(mustRun("rev-parse", activeSha+"^")).To(Equal(rolledOff))
			Expect(mustRun("rev-parse", activeSha+"^{tree}")).To(Equal(mustRun("rev-parse", restoreTo+"^{tree}")))
			Expect(mustRun("rev-parse", "origin/"+testBranchDevelopmentNext)).To(Equal(proposedTip))

			rawNote := mustRun("notes", "--ref="+git.PromoterHistoryNotesRef, "show", activeSha)
			var got map[string][]string
			Expect(json.Unmarshal([]byte(rawNote), &got)).To(Succeed())
			Expect(got[constants.TrailerRestoredFrom]).To(Equal([]string{restoreTo}))
			Expect(got[constants.TrailerPullRequestID]).To(Equal(firstNote[constants.TrailerPullRequestID]))
			Expect(got[constants.TrailerPullRequestMergeTime]).To(Equal(firstNote[constants.TrailerPullRequestMergeTime]))

			By("Leaving no pull request open: the reverted dry SHA is already contained in active")
			prKey := types.NamespacedName{
				Name: utils.KubeSafeUniqueName(utils.GetPullRequestName(
					gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch)),
				Namespace: "default",
			}
			Consistently(func(g Gomega) {
				err := k8sClient.Get(ctx, prKey, &promoterv1alpha1.PullRequest{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, 3*time.Second, 100*time.Millisecond).Should(Succeed())

			By("Hydrating a later dry SHA, which opens a development pull request but does not auto-merge")
			laterDrySha := hydrate("a later dry commit")
			var pr promoterv1alpha1.PullRequest
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, devKey, &ctpDev)).To(Succeed())
				g.Expect(ctpDev.Status.Proposed.Dry.Sha).To(Equal(laterDrySha))
				g.Expect(k8sClient.Get(ctx, prKey, &pr)).To(Succeed())
				g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
			}, constants.EventuallyTimeout).Should(Succeed())

			Consistently(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, prKey, &pr)).To(Succeed())
				g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: promotionStrategy.Name, Namespace: "default"}, promotionStrategy)).To(Succeed())
				for _, env := range promotionStrategy.Status.Environments {
					switch env.Branch {
					case testBranchDevelopment:
						g.Expect(env.Active.Dry.Sha).To(Equal(drySha1))
					case testBranchStaging, testBranchProduction:
						g.Expect(env.Active.Dry.Sha).To(Equal(drySha2))
					default:
						g.Expect(env.Branch).To(BeElementOf(testBranchDevelopment, testBranchStaging, testBranchProduction))
					}
				}
			}, 3*time.Second, 100*time.Millisecond).Should(Succeed())

			By("Deleting the RevertCommit so the later dry SHA can promote through every environment")
			Expect(k8sClient.Delete(ctx, rc)).To(Succeed())
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: promotionStrategy.Name, Namespace: "default"}, promotionStrategy)).To(Succeed())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))
				for _, env := range promotionStrategy.Status.Environments {
					g.Expect(env.Active.Dry.Sha).To(Equal(laterDrySha))
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})
