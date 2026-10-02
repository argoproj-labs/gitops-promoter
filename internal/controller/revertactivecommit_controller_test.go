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

//go:embed testdata/RevertActiveCommit.yaml
var testRevertActiveCommitYAML string

var _ = Describe("RevertActiveCommit Controller", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the RevertActiveCommit resource", func() {
			Expect(unmarshalYamlStrict(testRevertActiveCommitYAML, &promoterv1alpha1.RevertActiveCommit{})).To(Succeed())
		})
	})

	Context("When the PromotionStrategy does not exist", func() {
		It("reports the missing strategy on the Ready condition", func() {
			ctx := context.Background()
			name := "revert-missing-" + utils.KubeSafeUniqueName(randomString(10))
			rc := &promoterv1alpha1.RevertActiveCommit{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: promoterv1alpha1.RevertActiveCommitSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "does-not-exist"},
					Branch:               testBranchDevelopment,
					Sha:                  "abcdef1234567890abcdef1234567890abcdef12",
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

	Context("When the branch is not on the PromotionStrategy", func() {
		It("reports the missing branch on the Ready condition", func() {
			ctx := context.Background()
			name := "revert-branch-" + utils.KubeSafeUniqueName(randomString(10))
			ps := promotionStrategyForRevert(name+"-ps", name+"-gr", testBranchStaging)
			Expect(k8sClient.Create(ctx, ps)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, ps) })

			rc := &promoterv1alpha1.RevertActiveCommit{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: promoterv1alpha1.RevertActiveCommitSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: ps.Name},
					Branch:               testBranchDevelopment,
					Sha:                  "abcdef1234567890abcdef1234567890abcdef12",
				},
			}
			Expect(k8sClient.Create(ctx, rc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, rc) })

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: "default"}, rc)).To(Succeed())
				cond := meta.FindStatusCondition(rc.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Message).To(ContainSubstring(testBranchDevelopment))
				g.Expect(cond.Message).To(ContainSubstring(ps.Name))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When the spec is updated", func() {
		It("rejects changes to spec.sha, spec.promotionStrategyRef, and spec.branch", func() {
			ctx := context.Background()
			name := "revert-immutable-" + utils.KubeSafeUniqueName(randomString(10))
			rc := &promoterv1alpha1.RevertActiveCommit{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
				Spec: promoterv1alpha1.RevertActiveCommitSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "does-not-exist"},
					Branch:               testBranchDevelopment,
					Sha:                  "abcdef1234567890abcdef1234567890abcdef12",
				},
			}
			Expect(k8sClient.Create(ctx, rc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, rc) })
			key := types.NamespacedName{Name: name, Namespace: "default"}

			// The controller patches ownerReferences and status right after create, so retry
			// resourceVersion conflicts until the update reaches the validation rule.
			updateSpec := func(mutate func(*promoterv1alpha1.RevertActiveCommitSpec)) error {
				return retry.RetryOnConflict(retry.DefaultRetry, func() error {
					var live promoterv1alpha1.RevertActiveCommit
					Expect(k8sClient.Get(ctx, key, &live)).To(Succeed())
					mutate(&live.Spec)
					if err := k8sClient.Update(ctx, &live); err != nil {
						return fmt.Errorf("update RevertActiveCommit spec: %w", err)
					}
					return nil
				})
			}

			err := updateSpec(func(spec *promoterv1alpha1.RevertActiveCommitSpec) {
				spec.Sha = "1234567890abcdef1234567890abcdef12345678"
			})
			Expect(err).To(MatchError(ContainSubstring("spec is immutable")))

			err = updateSpec(func(spec *promoterv1alpha1.RevertActiveCommitSpec) {
				spec.PromotionStrategyRef.Name = "another-strategy"
			})
			Expect(err).To(MatchError(ContainSubstring("spec is immutable")))

			err = updateSpec(func(spec *promoterv1alpha1.RevertActiveCommitSpec) {
				spec.Branch = testBranchStaging
			})
			Expect(err).To(MatchError(ContainSubstring("spec is immutable")))

			By("still allowing metadata changes")
			Eventually(func(g Gomega) {
				var live promoterv1alpha1.RevertActiveCommit
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
			strategyName := name + "-ps"
			ctp.Name = utils.ChangeTransferPolicyNameForEnvironment(strategyName, testBranchDevelopment)
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

			gitPath, err := os.MkdirTemp("", "revert-active-commit-*")
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

			ps := promotionStrategyForRevert(strategyName, gitRepo.Name, testBranchDevelopment)
			Expect(k8sClient.Create(ctx, ps)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, ps) })

			rcName := name + "-rc"
			rc := &promoterv1alpha1.RevertActiveCommit{
				ObjectMeta: metav1.ObjectMeta{Name: rcName, Namespace: "default"},
				Spec: promoterv1alpha1.RevertActiveCommitSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: strategyName},
					Branch:               testBranchDevelopment,
					Sha:                  v1Sha,
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
				g.Expect(rc.Finalizers).To(ContainElement(promoterv1alpha1.RevertActiveCommitFinalizer))
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

			By("Deleting the RevertActiveCommit stamps Promoter-revert-unblocked-at and releases the finalizer")
			Expect(k8sClient.Delete(ctx, rc)).To(Succeed())
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: rcName, Namespace: "default"}, &promoterv1alpha1.RevertActiveCommit{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			mustRun("fetch", "origin", "+"+git.PromoterHistoryNotesRef+":"+git.PromoterHistoryNotesRef)
			rawNote = mustRun("notes", "--ref="+git.PromoterHistoryNotesRef, "show", activeSha)
			Expect(json.Unmarshal([]byte(rawNote), &got)).To(Succeed())
			Expect(got[constants.TrailerRevertUnblockedAt]).To(HaveLen(1))
			_, err = time.Parse(time.RFC3339, got[constants.TrailerRevertUnblockedAt][0])
			Expect(err).NotTo(HaveOccurred())
		})

		It("releases the finalizer when the ChangeTransferPolicy is already gone", func() {
			ctx := context.Background()
			name, scmSecret, scmProvider, gitRepo, _, ctp := changeTransferPolicyResources(ctx, "revert-gone-ctp", "default")
			strategyName := name + "-ps"
			ctp.Name = utils.ChangeTransferPolicyNameForEnvironment(strategyName, testBranchDevelopment)
			ctp.Spec.ActiveBranch = testBranchDevelopment
			ctp.Spec.ProposedBranch = testBranchDevelopmentNext
			autoMerge := false
			ctp.Spec.AutoMerge = &autoMerge

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, ctp)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, gitRepo)
				_ = k8sClient.Delete(ctx, scmProvider)
				_ = k8sClient.Delete(ctx, scmSecret)
			})

			gitPath, err := os.MkdirTemp("", "revert-gone-ctp-*")
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

			Expect(os.WriteFile(path.Join(gitPath, "version.txt"), []byte("v2\n"), 0o644)).To(Succeed())
			mustRun("add", "version.txt")
			mustRun("commit", "-m", "version v2")
			mustRun("push", "origin", "HEAD:refs/heads/"+testBranchDevelopment)

			ps := promotionStrategyForRevert(strategyName, gitRepo.Name, testBranchDevelopment)
			Expect(k8sClient.Create(ctx, ps)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, ps) })

			rc := &promoterv1alpha1.RevertActiveCommit{
				ObjectMeta: metav1.ObjectMeta{Name: name + "-rc", Namespace: "default"},
				Spec: promoterv1alpha1.RevertActiveCommitSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: strategyName},
					Branch:               testBranchDevelopment,
					Sha:                  v1Sha,
				},
			}
			Expect(k8sClient.Create(ctx, rc)).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rc.Name, Namespace: "default"}, rc)).To(Succeed())
				g.Expect(rc.Status.RestoredFrom).To(Equal(v1Sha))
				g.Expect(rc.Finalizers).To(ContainElement(promoterv1alpha1.RevertActiveCommitFinalizer))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Removing the owner reference and deleting the ChangeTransferPolicy so unblock cannot resolve it")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rc.Name, Namespace: "default"}, rc)).To(Succeed())
				rc.OwnerReferences = nil
				g.Expect(k8sClient.Update(ctx, rc)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(k8sClient.Delete(ctx, ctp)).To(Succeed())
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: ctp.Name, Namespace: "default"}, &promoterv1alpha1.ChangeTransferPolicy{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Deleting the RevertActiveCommit still releases the finalizer")
			Expect(k8sClient.Delete(ctx, rc)).To(Succeed())
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: rc.Name, Namespace: "default"}, &promoterv1alpha1.RevertActiveCommit{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When a PromotionStrategy owns the policy", func() {
		It("restores one environment after a real promotion and holds the next one", func() {
			ctx := context.Background()
			s := setupRestoredPromotionStrategy()

			s.mustRun("fetch", "origin", testBranchDevelopment, testBranchDevelopmentNext)
			s.mustRun("fetch", "origin", "+"+git.PromoterHistoryNotesRef+":"+git.PromoterHistoryNotesRef)
			activeSha := s.mustRun("rev-parse", "origin/"+testBranchDevelopment)
			Expect(activeSha).To(Equal(s.rc.Status.ActiveSha))
			Expect(s.mustRun("rev-parse", activeSha+"^")).To(Equal(s.rolledOff))
			Expect(s.mustRun("rev-parse", activeSha+"^{tree}")).To(Equal(s.mustRun("rev-parse", s.restoreTo+"^{tree}")))
			Expect(s.mustRun("rev-parse", "origin/"+testBranchDevelopmentNext)).To(Equal(s.proposedTip))

			rawNote := s.mustRun("notes", "--ref="+git.PromoterHistoryNotesRef, "show", activeSha)
			var got map[string][]string
			Expect(json.Unmarshal([]byte(rawNote), &got)).To(Succeed())
			Expect(got[constants.TrailerRestoredFrom]).To(Equal([]string{s.restoreTo}))
			Expect(got[constants.TrailerPullRequestID]).To(Equal(s.firstNote[constants.TrailerPullRequestID]))
			Expect(got[constants.TrailerPullRequestMergeTime]).To(Equal(s.firstNote[constants.TrailerPullRequestMergeTime]))

			By("Leaving no pull request open: the reverted dry SHA is already contained in active")
			prKey := s.developmentPRKey()
			Consistently(func(g Gomega) {
				err := k8sClient.Get(ctx, prKey, &promoterv1alpha1.PullRequest{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, 3*time.Second, 100*time.Millisecond).Should(Succeed())

			By("Hydrating a later dry SHA, which opens a development pull request but does not auto-merge")
			laterDrySha := s.hydrate("a later dry commit")
			var pr promoterv1alpha1.PullRequest
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, s.devKey, &s.ctpDev)).To(Succeed())
				g.Expect(s.ctpDev.Status.Proposed.Dry.Sha).To(Equal(laterDrySha))
				g.Expect(k8sClient.Get(ctx, prKey, &pr)).To(Succeed())
				g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
			}, constants.EventuallyTimeout).Should(Succeed())

			Consistently(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, prKey, &pr)).To(Succeed())
				g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: s.promotionStrategy.Name, Namespace: "default"}, s.promotionStrategy)).To(Succeed())
				expectPromotionStrategyActiveDry(g, s.promotionStrategy, s.drySha1, s.drySha2)
			}, 3*time.Second, 100*time.Millisecond).Should(Succeed())

			By("Deleting the RevertActiveCommit so the later dry SHA can promote through every environment")
			Expect(k8sClient.Delete(ctx, s.rc)).To(Succeed())
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: s.rc.Name, Namespace: s.rc.Namespace}, &promoterv1alpha1.RevertActiveCommit{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			s.mustRun("fetch", "origin", "+"+git.PromoterHistoryNotesRef+":"+git.PromoterHistoryNotesRef)
			rawNote = s.mustRun("notes", "--ref="+git.PromoterHistoryNotesRef, "show", activeSha)
			Expect(json.Unmarshal([]byte(rawNote), &got)).To(Succeed())
			Expect(got[constants.TrailerRevertUnblockedAt]).To(HaveLen(1))

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: s.promotionStrategy.Name, Namespace: "default"}, s.promotionStrategy)).To(Succeed())
				g.Expect(s.promotionStrategy.Status.Environments).To(HaveLen(3))
				for _, env := range s.promotionStrategy.Status.Environments {
					g.Expect(env.Active.Dry.Sha).To(Equal(laterDrySha))
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})
		It("does not promote the reverted dry SHA after the RevertActiveCommit is deleted", func() {
			ctx := context.Background()
			s := setupRestoredPromotionStrategy()

			By("Confirming the proposed branch is still the reverted dry SHA")
			Expect(k8sClient.Get(ctx, s.devKey, &s.ctpDev)).To(Succeed())
			Expect(s.ctpDev.Status.Proposed.Dry.Sha).To(Equal(s.drySha2))
			Expect(s.ctpDev.Status.Active.Dry.Sha).To(Equal(s.drySha1))

			By("Deleting the RevertActiveCommit without hydrating a new proposed commit")
			Expect(k8sClient.Delete(ctx, s.rc)).To(Succeed())
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: s.rc.Name, Namespace: s.rc.Namespace}, &promoterv1alpha1.RevertActiveCommit{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Leaving that dry SHA unpromoted, because the proposed commit is already contained in the restore")
			prKey := s.developmentPRKey()
			Consistently(func(g Gomega) {
				err := k8sClient.Get(ctx, prKey, &promoterv1alpha1.PullRequest{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
				g.Expect(k8sClient.Get(ctx, s.devKey, &s.ctpDev)).To(Succeed())
				g.Expect(s.ctpDev.Status.Proposed.Dry.Sha).To(Equal(s.drySha2))
				g.Expect(s.ctpDev.Status.Active.Dry.Sha).To(Equal(s.drySha1))
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: s.promotionStrategy.Name, Namespace: "default"}, s.promotionStrategy)).To(Succeed())
				expectPromotionStrategyActiveDry(g, s.promotionStrategy, s.drySha1, s.drySha2)
			}, 3*time.Second, 100*time.Millisecond).Should(Succeed())

			s.mustRun("fetch", "origin", testBranchDevelopment, testBranchDevelopmentNext)
			Expect(s.mustRun("rev-parse", "origin/"+testBranchDevelopment)).To(Equal(s.rc.Status.ActiveSha))
			Expect(s.mustRun("rev-parse", "origin/"+testBranchDevelopmentNext)).To(Equal(s.proposedTip))
		})
	})
})

// restoredPromotionStrategy is a PromotionStrategy whose development environment has been promoted
// twice and then restored to the first promotion. drySha2 is the dry SHA the restore moved off
// development; staging and production are still running it. The proposed branch was left in place,
// so it still carries drySha2 and that commit is an ancestor of the restore commit.
type restoredPromotionStrategy struct {
	ctpDev            promoterv1alpha1.ChangeTransferPolicy
	hydrate           func(message string) string
	promotionStrategy *promoterv1alpha1.PromotionStrategy
	mustRun           func(args ...string) string
	gitRepo           *promoterv1alpha1.GitRepository
	firstNote         map[string][]string
	rc                *promoterv1alpha1.RevertActiveCommit
	devKey            types.NamespacedName
	drySha1           string
	drySha2           string
	restoreTo         string
	rolledOff         string
	proposedTip       string
}

func (s restoredPromotionStrategy) developmentPRKey() types.NamespacedName {
	return types.NamespacedName{
		Name: utils.KubeSafeUniqueName(utils.GetPullRequestName(
			s.gitRepo.Spec.Fake.Owner, s.gitRepo.Spec.Fake.Name, s.ctpDev.Spec.ProposedBranch, s.ctpDev.Spec.ActiveBranch)),
		Namespace: "default",
	}
}

func expectPromotionStrategyActiveDry(g Gomega, ps *promoterv1alpha1.PromotionStrategy, development, others string) {
	g.Expect(ps.Status.Environments).To(HaveLen(3))
	for _, env := range ps.Status.Environments {
		switch env.Branch {
		case testBranchDevelopment:
			g.Expect(env.Active.Dry.Sha).To(Equal(development))
		case testBranchStaging, testBranchProduction:
			g.Expect(env.Active.Dry.Sha).To(Equal(others))
		default:
			g.Expect(env.Branch).To(BeElementOf(testBranchDevelopment, testBranchStaging, testBranchProduction))
		}
	}
}

func setupRestoredPromotionStrategy() restoredPromotionStrategy {
	GinkgoHelper()
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
			Name:      utils.ChangeTransferPolicyNameForEnvironment(promotionStrategy.Name, branch),
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
		firstNote, err = fetchPromotionHistoryNote(ctx, gitPath, restoreTo)
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
	rc := &promoterv1alpha1.RevertActiveCommit{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-rc", Namespace: "default"},
		Spec: promoterv1alpha1.RevertActiveCommitSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: promotionStrategy.Name},
			Branch:               testBranchDevelopment,
			Sha:                  restoreTo,
		},
	}
	Expect(k8sClient.Create(ctx, rc)).To(Succeed())
	DeferCleanup(func() { _ = k8sClient.Delete(ctx, rc) })

	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rc.Name, Namespace: "default"}, rc)).To(Succeed())
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
		expectPromotionStrategyActiveDry(g, promotionStrategy, drySha1, drySha2)
	}, constants.EventuallyTimeout).Should(Succeed())

	return restoredPromotionStrategy{
		gitRepo:           gitRepo,
		promotionStrategy: promotionStrategy,
		ctpDev:            ctpDev,
		devKey:            devKey,
		mustRun:           mustRun,
		hydrate:           hydrate,
		drySha1:           drySha1,
		drySha2:           drySha2,
		restoreTo:         restoreTo,
		firstNote:         firstNote,
		rolledOff:         rolledOff,
		proposedTip:       proposedTip,
		rc:                rc,
	}
}

// promotionStrategyForRevert is a PromotionStrategy the RevertActiveCommit controller can resolve, whose
// orderCommitStatusRef does not exist. The PromotionStrategy controller stops before it upserts a
// ChangeTransferPolicy, so a test can own that policy itself.
func promotionStrategyForRevert(name, repoName, branch string) *promoterv1alpha1.PromotionStrategy {
	return &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: promoterv1alpha1.PromotionStrategySpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{Name: repoName},
			OrderCommitStatusRef: promoterv1alpha1.OrderCommitStatusRef{
				Group: promoterv1alpha1.SchemeGroupVersion.Group,
				Kind:  "DependentsSuccessfulCommitStatus",
				Name:  "missing-order-gate",
			},
			Environments: []promoterv1alpha1.Environment{{Branch: branch}},
		},
	}
}
