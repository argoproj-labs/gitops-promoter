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

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

//go:embed testdata/RestoreActiveCommit.yaml
var testRestoreActiveCommitYAML string

var _ = Describe("RestoreActiveCommit Controller", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the RestoreActiveCommit resource", func() {
			Expect(unmarshalYamlStrict(testRestoreActiveCommitYAML, &promoterv1alpha1.RestoreActiveCommit{})).To(Succeed())
		})
	})

	Context("When the PromotionStrategy does not exist", func() {
		It("reports the missing strategy on the Ready condition", func() {
			ctx := context.Background()
			name := "restore-missing-" + utils.KubeSafeUniqueName(randomString(10))
			rc := &promoterv1alpha1.RestoreActiveCommit{
				Name: name, Namespace: "default",
				Spec: promoterv1alpha1.RestoreActiveCommitSpec{
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
			name := "restore-branch-" + utils.KubeSafeUniqueName(randomString(10))
			ps := promotionStrategyForRestore(name+"-ps", name+"-gr", testBranchStaging)
			Expect(k8sClient.Create(ctx, ps)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, ps) })

			rc := &promoterv1alpha1.RestoreActiveCommit{
				Name: name, Namespace: "default",
				Spec: promoterv1alpha1.RestoreActiveCommitSpec{
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
			name := "restore-immutable-" + utils.KubeSafeUniqueName(randomString(10))
			rc := &promoterv1alpha1.RestoreActiveCommit{
				Name: name, Namespace: "default",
				Spec: promoterv1alpha1.RestoreActiveCommitSpec{
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
			updateSpec := func(mutate func(*promoterv1alpha1.RestoreActiveCommitSpec)) error {
				return retry.RetryOnConflict(retry.DefaultRetry, func() error {
					var live promoterv1alpha1.RestoreActiveCommit
					Expect(k8sClient.Get(ctx, key, &live)).To(Succeed())
					mutate(&live.Spec)
					if err := k8sClient.Update(ctx, &live); err != nil {
						return fmt.Errorf("update RestoreActiveCommit spec: %w", err)
					}
					return nil
				})
			}

			err := updateSpec(func(spec *promoterv1alpha1.RestoreActiveCommitSpec) {
				spec.Sha = "1234567890abcdef1234567890abcdef12345678"
			})
			Expect(err).To(MatchError(ContainSubstring("promotionStrategyRef, branch, and sha are immutable")))

			err = updateSpec(func(spec *promoterv1alpha1.RestoreActiveCommitSpec) {
				spec.PromotionStrategyRef.Name = "another-strategy"
			})
			Expect(err).To(MatchError(ContainSubstring("promotionStrategyRef, branch, and sha are immutable")))

			err = updateSpec(func(spec *promoterv1alpha1.RestoreActiveCommitSpec) {
				spec.Branch = testBranchStaging
			})
			Expect(err).To(MatchError(ContainSubstring("promotionStrategyRef, branch, and sha are immutable")))

			By("still allowing metadata changes")
			Eventually(func(g Gomega) {
				var live promoterv1alpha1.RestoreActiveCommit
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
			name, scmSecret, scmProvider, gitRepo, _, ctp := changeTransferPolicyResources(ctx, "restore-active", "default")
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

			gitPath, err := os.MkdirTemp("", "restore-active-commit-*")
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

			ps := promotionStrategyForRestore(strategyName, gitRepo.Name, testBranchDevelopment)
			Expect(k8sClient.Create(ctx, ps)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, ps) })

			rcName := name + "-rc"
			rc := &promoterv1alpha1.RestoreActiveCommit{
				Name: rcName, Namespace: "default",
				Spec: promoterv1alpha1.RestoreActiveCommitSpec{
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
				g.Expect(rc.Finalizers).To(ContainElement(promoterv1alpha1.RestoreActiveCommitFinalizer))
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

			By("Setting spec.blockEnvironment to false stamps Promoter-restore-unblocked-at and deletes the RestoreActiveCommit")
			clearRevertBlockEnvironment(ctx, types.NamespacedName{Name: rcName, Namespace: "default"})
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: rcName, Namespace: "default"}, &promoterv1alpha1.RestoreActiveCommit{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			mustRun("fetch", "origin", "+"+git.PromoterHistoryNotesRef+":"+git.PromoterHistoryNotesRef)
			rawNote = mustRun("notes", "--ref="+git.PromoterHistoryNotesRef, "show", activeSha)
			Expect(json.Unmarshal([]byte(rawNote), &got)).To(Succeed())
			Expect(got[constants.TrailerRestoreUnblockedAt]).To(HaveLen(1))
			_, err = time.Parse(time.RFC3339, got[constants.TrailerRestoreUnblockedAt][0])
			Expect(err).NotTo(HaveOccurred())
		})

		It("records the restore before unblocking when blockEnvironment is false before activeSha is set", func() {
			ctx := context.Background()
			// The first reconcile sees blockEnvironment false with no activeSha yet, so it only
			// records the restore. Nothing else changes spec.generation, so the unblock pass is the
			// periodic requeue (shipped default 5m). Shorten it before the RestoreActiveCommit
			// exists so that pass runs inside the test.
			By("Shortening the RestoreActiveCommit requeue duration")
			setRestoreActiveCommitRequeueDuration(ctx, 200*time.Millisecond)

			_, scmSecret, scmProvider, gitRepo, _, _, ps := promotionStrategyResource(ctx, "restore-quick-unblock", "default")
			setupInitialTestGitRepoOnServer(ctx, gitRepo)
			autoMerge := false
			ps.Spec.Environments = []promoterv1alpha1.Environment{{
				Branch:    testBranchDevelopment,
				AutoMerge: &autoMerge,
			}}
			ctpName := utils.ChangeTransferPolicyNameForEnvironment(ps.Name, testBranchDevelopment)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			DeferCleanup(func() {
				deleteRestoreActiveCommitsForPolicy(ctx, ctpName, ps.Namespace)
				_ = k8sClient.Delete(ctx, ps)
				_ = k8sClient.Delete(ctx, &promoterv1alpha1.DependentsSuccessfulCommitStatus{
					Name: ps.Name, Namespace: ps.Namespace,
				})
				_ = k8sClient.Delete(ctx, gitRepo)
				_ = k8sClient.Delete(ctx, scmProvider)
				_ = k8sClient.Delete(ctx, scmSecret)
			})

			gitPath, err := os.MkdirTemp("", "restore-quick-unblock-*")
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

			activeDry := "5555555555555555555555555555555555555555"
			Expect(os.WriteFile(path.Join(gitPath, "version.txt"), []byte("v2\n"), 0o644)).To(Succeed())
			Expect(os.WriteFile(path.Join(gitPath, "hydrator.metadata"), []byte(`{"drySha":"`+activeDry+`"}`), 0o644)).To(Succeed())
			mustRun("add", "version.txt", "hydrator.metadata")
			mustRun("commit", "-m", "version v2")
			v2Sha := mustRun("rev-parse", "HEAD")
			mustRun("push", "origin", "HEAD:refs/heads/"+testBranchDevelopment)

			// promotionStrategyResource already sets orderCommitStatusRef. Create that gate
			// before the PromotionStrategy so the first reconcile can upsert the ChangeTransferPolicy.
			createDependentsSuccessfulCommitStatus(ctx, ps)
			Expect(k8sClient.Create(ctx, ps)).To(Succeed())

			By("Waiting for the PromotionStrategy to create the ChangeTransferPolicy")
			var ctp promoterv1alpha1.ChangeTransferPolicy
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: ctpName, Namespace: "default"}, &ctp)).To(Succeed())
				g.Expect(ctp.Spec.ActiveBranch).To(Equal(testBranchDevelopment))
				g.Expect(ctp.Spec.ProposedBranch).To(Equal(testBranchDevelopmentNext))
				g.Expect(ctp.Spec.AutoMerge).To(HaveValue(BeFalse()))
				g.Expect(ctp.OwnerReferences).To(HaveLen(1))
				g.Expect(ctp.OwnerReferences[0].Name).To(Equal(ps.Name))
				g.Expect(ctp.OwnerReferences[0].Kind).To(Equal("PromotionStrategy"))
				g.Expect(ctp.OwnerReferences[0].UID).To(Equal(ps.UID))
			}, constants.EventuallyTimeout).Should(Succeed())

			blockEnvironment := false
			rcName := ps.Name + "-rc"
			rcKey := types.NamespacedName{Name: rcName, Namespace: ps.Namespace}
			rc := &promoterv1alpha1.RestoreActiveCommit{
				Name: rcName, Namespace: ps.Namespace,
				Spec: promoterv1alpha1.RestoreActiveCommitSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: ps.Name},
					Branch:               testBranchDevelopment,
					Sha:                  v1Sha,
					BlockEnvironment:     &blockEnvironment,
				},
			}
			Expect(k8sClient.Create(ctx, rc)).To(Succeed())

			By("Recording activeSha while the object still exists, before the unblock pass deletes it")
			var activeSha string
			Eventually(func(g Gomega) {
				var live promoterv1alpha1.RestoreActiveCommit
				err := k8sClient.Get(ctx, rcKey, &live)
				if errors.IsNotFound(err) {
					g.Expect(activeSha).NotTo(BeEmpty(), "RestoreActiveCommit was deleted before status.activeSha was set")
					return
				}
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(live.Spec.BlocksEnvironment()).To(BeFalse())
				g.Expect(live.Status.ActiveSha).NotTo(BeEmpty())
				cond := meta.FindStatusCondition(live.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(live.Status.RestoredFrom).To(Equal(v1Sha))
				g.Expect(live.Status.BlockedDrySha).To(Equal(activeDry))
				activeSha = live.Status.ActiveSha
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Stamping Promoter-restore-unblocked-at on that commit and deleting the RestoreActiveCommit")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, rcKey, &promoterv1alpha1.RestoreActiveCommit{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			mustRun("fetch", "origin", testBranchDevelopment)
			mustRun("fetch", "origin", "+"+git.PromoterHistoryNotesRef+":"+git.PromoterHistoryNotesRef)

			Expect(mustRun("rev-parse", "origin/"+testBranchDevelopment)).To(Equal(activeSha))
			Expect(mustRun("rev-parse", activeSha+"^")).To(Equal(v2Sha))
			Expect(mustRun("rev-parse", activeSha+"^{tree}")).To(Equal(mustRun("rev-parse", v1Sha+"^{tree}")))

			rawNote := mustRun("notes", "--ref="+git.PromoterHistoryNotesRef, "show", activeSha)
			var got map[string][]string
			Expect(json.Unmarshal([]byte(rawNote), &got)).To(Succeed())
			Expect(got[constants.TrailerRestoredFrom]).To(Equal([]string{v1Sha}))
			Expect(got[constants.TrailerRestoreUnblockedAt]).To(HaveLen(1))
			_, err = time.Parse(time.RFC3339, got[constants.TrailerRestoreUnblockedAt][0])
			Expect(err).NotTo(HaveOccurred())

			By("Leaving no RestoreActiveCommit for the policy, because the tip is already unblocked")
			Consistently(func(g Gomega) {
				var list promoterv1alpha1.RestoreActiveCommitList
				g.Expect(k8sClient.List(ctx, &list, ctrlclient.InNamespace("default"))).To(Succeed())
				for i := range list.Items {
					g.Expect(changeTransferPolicyNameForRevert(&list.Items[i])).NotTo(Equal(ctp.Name))
				}
			}, 3*time.Second, 100*time.Millisecond).Should(Succeed())
		})

		It("releases the finalizer when the ChangeTransferPolicy is already gone", func() {
			ctx := context.Background()
			name, scmSecret, scmProvider, gitRepo, _, ctp := changeTransferPolicyResources(ctx, "restore-gone-ctp", "default")
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

			gitPath, err := os.MkdirTemp("", "restore-gone-ctp-*")
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

			ps := promotionStrategyForRestore(strategyName, gitRepo.Name, testBranchDevelopment)
			Expect(k8sClient.Create(ctx, ps)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, ps) })

			rc := &promoterv1alpha1.RestoreActiveCommit{
				Name: name + "-rc", Namespace: "default",
				Spec: promoterv1alpha1.RestoreActiveCommitSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: strategyName},
					Branch:               testBranchDevelopment,
					Sha:                  v1Sha,
				},
			}
			Expect(k8sClient.Create(ctx, rc)).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: rc.Name, Namespace: "default"}, rc)).To(Succeed())
				g.Expect(rc.Status.RestoredFrom).To(Equal(v1Sha))
				g.Expect(rc.Finalizers).To(ContainElement(promoterv1alpha1.RestoreActiveCommitFinalizer))
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

			By("Deleting the RestoreActiveCommit still releases the finalizer without stamping the note")
			Expect(k8sClient.Delete(ctx, rc)).To(Succeed())
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: rc.Name, Namespace: "default"}, &promoterv1alpha1.RestoreActiveCommit{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When a PromotionStrategy owns the policy", func() {
		It("restores one environment after a real promotion and blocks the next one", func() {
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

			By("Leaving no pull request open: the blocked dry SHA is already contained in active")
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

			By("Setting spec.blockEnvironment to false so the later dry SHA can promote through every environment")
			clearRevertBlockEnvironment(ctx, types.NamespacedName{Name: s.rc.Name, Namespace: s.rc.Namespace})
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: s.rc.Name, Namespace: s.rc.Namespace}, &promoterv1alpha1.RestoreActiveCommit{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			s.mustRun("fetch", "origin", "+"+git.PromoterHistoryNotesRef+":"+git.PromoterHistoryNotesRef)
			rawNote = s.mustRun("notes", "--ref="+git.PromoterHistoryNotesRef, "show", activeSha)
			Expect(json.Unmarshal([]byte(rawNote), &got)).To(Succeed())
			Expect(got[constants.TrailerRestoreUnblockedAt]).To(HaveLen(1))

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: s.promotionStrategy.Name, Namespace: "default"}, s.promotionStrategy)).To(Succeed())
				g.Expect(s.promotionStrategy.Status.Environments).To(HaveLen(3))
				for _, env := range s.promotionStrategy.Status.Environments {
					g.Expect(env.Active.Dry.Sha).To(Equal(laterDrySha))
				}
			}, constants.EventuallyTimeout).Should(Succeed())
		})
		It("does not promote the blocked dry SHA after the RestoreActiveCommit is deleted", func() {
			ctx := context.Background()
			s := setupRestoredPromotionStrategy()

			By("Confirming the proposed branch is still the blocked dry SHA")
			Expect(k8sClient.Get(ctx, s.devKey, &s.ctpDev)).To(Succeed())
			Expect(s.ctpDev.Status.Proposed.Dry.Sha).To(Equal(s.drySha2))
			Expect(s.ctpDev.Status.Active.Dry.Sha).To(Equal(s.drySha1))

			By("Deleting the RestoreActiveCommit without hydrating a new proposed commit")
			Expect(k8sClient.Delete(ctx, s.rc)).To(Succeed())
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: s.rc.Name, Namespace: s.rc.Namespace}, &promoterv1alpha1.RestoreActiveCommit{})
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
	rc                *promoterv1alpha1.RestoreActiveCommit
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
	name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy := promotionStrategyResource(ctx, "restore-ps", "default")
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
			Name: promotionStrategy.Name, Namespace: "default",
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

	gitPath, err := os.MkdirTemp("", "restore-ps-*")
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
		dir, err := os.MkdirTemp("", "restore-ps-hydrate-*")
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
	rc := &promoterv1alpha1.RestoreActiveCommit{
		Name: name + "-rc", Namespace: "default",
		Spec: promoterv1alpha1.RestoreActiveCommitSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: promotionStrategy.Name},
			Branch:               testBranchDevelopment,
			Sha:                  restoreTo,
		},
	}
	Expect(k8sClient.Create(ctx, rc)).To(Succeed())
	DeferCleanup(func() { _ = k8sClient.Delete(ctx, rc) })
	// Deleting the test's RestoreActiveCommit while the tip is still a blocked restore makes the
	// ChangeTransferPolicy controller adopt a replacement. That object is a different name, so the
	// cleanup above does not remove it, and it blocks deletion of the policy that owns it.
	DeferCleanup(func() {
		for _, key := range []types.NamespacedName{devKey, stagingKey, prodKey} {
			deleteRestoreActiveCommitsForPolicy(ctx, key.Name, key.Namespace)
		}
	})

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

// setRestoreActiveCommitRequeueDuration patches the singleton ControllerConfiguration's
// restoreActiveCommit.workQueue.requeueDuration for the current test and restores the previous
// value afterward. Set it before creating the RestoreActiveCommit: the controller applies
// RequeueAfter from the value it reads at the end of that reconcile.
func setRestoreActiveCommitRequeueDuration(ctx context.Context, d time.Duration) {
	GinkgoHelper()
	key := types.NamespacedName{Namespace: "default", Name: settings.ControllerConfigurationName}
	var current promoterv1alpha1.ControllerConfiguration
	Expect(k8sClient.Get(ctx, key, &current)).To(Succeed())
	original := current.Spec.RestoreActiveCommit.WorkQueue.RequeueDuration

	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var live promoterv1alpha1.ControllerConfiguration
		if err := k8sClient.Get(ctx, key, &live); err != nil {
			return fmt.Errorf("get ControllerConfiguration: %w", err)
		}
		live.Spec.RestoreActiveCommit.WorkQueue.RequeueDuration = metav1.Duration{Duration: d}
		if err := k8sClient.Update(ctx, &live); err != nil {
			return fmt.Errorf("update ControllerConfiguration: %w", err)
		}
		return nil
	})).To(Succeed())

	DeferCleanup(func() {
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			var live promoterv1alpha1.ControllerConfiguration
			if err := k8sClient.Get(ctx, key, &live); err != nil {
				return fmt.Errorf("get ControllerConfiguration: %w", err)
			}
			live.Spec.RestoreActiveCommit.WorkQueue.RequeueDuration = original
			if err := k8sClient.Update(ctx, &live); err != nil {
				return fmt.Errorf("restore ControllerConfiguration: %w", err)
			}
			return nil
		})).To(Succeed())
	})
}

// clearRevertBlockEnvironment sets spec.blockEnvironment to false so the controller stamps
// Promoter-restore-unblocked-at and deletes the RestoreActiveCommit.
func clearRevertBlockEnvironment(ctx context.Context, key types.NamespacedName) {
	GinkgoHelper()
	block := false
	Eventually(func(g Gomega) {
		rc := &promoterv1alpha1.RestoreActiveCommit{}
		g.Expect(k8sClient.Get(ctx, key, rc)).To(Succeed())
		rc.Spec.BlockEnvironment = &block
		g.Expect(k8sClient.Update(ctx, rc)).To(Succeed())
	}, constants.EventuallyTimeout).Should(Succeed())
}

// promotionStrategyForRestore is a PromotionStrategy the RestoreActiveCommit controller can resolve, whose
// orderCommitStatusRef does not exist. The PromotionStrategy controller stops before it upserts a
// ChangeTransferPolicy, so a test can own that policy itself.
func promotionStrategyForRestore(name, repoName, branch string) *promoterv1alpha1.PromotionStrategy {
	return &promoterv1alpha1.PromotionStrategy{
		Name: name, Namespace: "default",
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
