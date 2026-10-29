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
	"path"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

const (
	sharedActivePathOne = "apps/app-one"
	sharedActivePathTwo = "apps/app-two"
)

// sharedActivePathStrategies is a set of PromotionStrategies whose single development environment shares one
// active branch and differ only by spec.activePath, as in the "one branch per environment" layout. apps holds
// them in creation order; psOne and psTwo are the first two, for specs that only need a pair.
type sharedActivePathStrategies struct {
	gitRepo    *promoterv1alpha1.GitRepository
	apps       []*promoterv1alpha1.PromotionStrategy
	psOne      *promoterv1alpha1.PromotionStrategy
	psTwo      *promoterv1alpha1.PromotionStrategy
	gitPath    string
	namespaces string
}

// setupSharedActivePathStrategies is setupSharedActivePathApps for the two apps sharedActivePathOne and
// sharedActivePathTwo.
func setupSharedActivePathStrategies(ctx context.Context) sharedActivePathStrategies {
	GinkgoHelper()
	return setupSharedActivePathApps(ctx, sharedActivePathOne, sharedActivePathTwo)
}

// setupSharedActivePathApps creates one PromotionStrategy per activePath, all promoting onto the same
// development branch with AutoMerge on, and waits for their ChangeTransferPolicies.
func setupSharedActivePathApps(ctx context.Context, activePaths ...string) sharedActivePathStrategies {
	GinkgoHelper()
	Expect(len(activePaths)).To(BeNumerically(">=", 2), "a shared active branch needs at least two apps")
	_, scmSecret, scmProvider, gitRepo, _, _, base := promotionStrategyResource(ctx, "restore-shared-active-path", "default")
	setupInitialTestGitRepoForActivePath(ctx, gitRepo)
	Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
	Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
	Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())

	s := sharedActivePathStrategies{gitRepo: gitRepo, namespaces: "default"}
	for _, activePath := range activePaths {
		ps := base.DeepCopy()
		ps.Name = base.Name + "-" + path.Base(activePath)
		ps.Spec.ActivePath = activePath
		ps.Spec.Environments = []promoterv1alpha1.Environment{{Branch: testBranchDevelopment, AutoMerge: new(true)}}
		declareDependentsSuccessfulGate(ps)
		Expect(k8sClient.Create(ctx, ps)).To(Succeed())
		createDependentsSuccessfulCommitStatus(ctx, ps)
		s.apps = append(s.apps, ps)
	}
	s.psOne, s.psTwo = s.apps[0], s.apps[1]
	DeferCleanup(func() {
		for _, ps := range s.apps {
			deleteRestoreActiveCommitsForPolicy(ctx, s.ctpKey(ps).Name, ps.Namespace)
			_ = k8sClient.Delete(ctx, ps)
			_ = k8sClient.Delete(ctx, &promoterv1alpha1.DependentsSuccessfulCommitStatus{Name: ps.Name, Namespace: ps.Namespace})
		}
		_ = k8sClient.Delete(ctx, gitRepo)
		_ = k8sClient.Delete(ctx, scmProvider)
		_ = k8sClient.Delete(ctx, scmSecret)
	})

	Eventually(func(g Gomega) {
		for _, ps := range s.apps {
			g.Expect(s.ctp(ctx, g, ps).Spec.ProposedBranch).To(Equal(path.Join(testBranchDevelopmentNext, ps.Spec.ActivePath)))
		}
	}, constants.EventuallyTimeout).Should(Succeed())

	gitPath, err := cloneTestRepo(ctx, gitRepo)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(gitPath) })
	s.gitPath = gitPath
	return s
}

// historyFor returns ps's ChangeTransferPolicyHistory entries, newest first.
func (s sharedActivePathStrategies) historyFor(ctx context.Context, g Gomega, ps *promoterv1alpha1.PromotionStrategy) []promoterv1alpha1.History {
	var ctph promoterv1alpha1.ChangeTransferPolicyHistory
	g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: utils.GetChangeTransferPolicyHistoryName(s.ctpKey(ps).Name), Namespace: ps.Namespace}, &ctph)).To(Succeed())
	return ctph.Status.History
}

// nudge enqueues ps's ChangeTransferPolicy the way PromotionStrategy.enqueueOutOfSyncCTPs would; SCMs do
// not webhook git notes, and a sibling's merge webhook is the thing some specs deliberately do without.
func (s sharedActivePathStrategies) nudge(ps *promoterv1alpha1.PromotionStrategy) {
	key := s.ctpKey(ps)
	enqueueCTP(key.Namespace, key.Name)
}

// squashMerge lands ps's pending change on the active branch the way an SCM squash merge would: a single
// commit with the proposed tree for the path, so the proposed tip is not an ancestor of the active branch.
// It removes the pull request object as the SCM closing it would, sends the push webhook, and waits for the
// policy to see the change. Returns the dry SHA.
func (s sharedActivePathStrategies) squashMerge(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, message string) string {
	GinkgoHelper()
	dry := s.hydrate(ctx, ps.Spec.ActivePath, message)
	proposed := path.Join(testBranchDevelopmentNext, ps.Spec.ActivePath)
	prKey := s.prKey(ps.Spec.ActivePath)
	Eventually(func(g Gomega) {
		s.nudge(ps)
		g.Expect(s.ctp(ctx, g, ps).Status.Proposed.Dry.Sha).To(Equal(dry))
		g.Expect(k8sClient.Get(ctx, prKey, &promoterv1alpha1.PullRequest{})).To(Succeed())
	}, constants.EventuallyTimeout).Should(Succeed())
	_, err := runGitCmd(ctx, s.gitPath, "fetch", "origin", testBranchDevelopment, proposed)
	Expect(err).NotTo(HaveOccurred())
	before, err := runGitCmd(ctx, s.gitPath, "rev-parse", "origin/"+testBranchDevelopment)
	Expect(err).NotTo(HaveOccurred())
	before = strings.TrimSpace(before)
	squash, err := runGitCmd(ctx, s.gitPath, "commit-tree", "origin/"+proposed+"^{tree}", "-p", before, "-m", "Squash-merge "+message)
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, s.gitPath, "push", "-q", "origin", strings.TrimSpace(squash)+":refs/heads/"+testBranchDevelopment)
	Expect(err).NotTo(HaveOccurred())
	Expect(ctrlclient.IgnoreNotFound(k8sClient.Delete(ctx, &promoterv1alpha1.PullRequest{ObjectMeta: metav1.ObjectMeta{Name: prKey.Name, Namespace: prKey.Namespace}}))).To(Succeed())
	sendWebhookForPush(ctx, before, testBranchDevelopment)
	Eventually(func(g Gomega) {
		s.nudge(ps)
		ctp := s.ctp(ctx, g, ps)
		g.Expect(ctp.Status.Active.Dry.Sha).To(Equal(dry))
		g.Expect(ctp.Status.Proposed.Dry.Sha).To(Equal(dry))
	}, constants.EventuallyTimeout).Should(Succeed())
	return dry
}

func (s sharedActivePathStrategies) ctpKey(ps *promoterv1alpha1.PromotionStrategy) types.NamespacedName {
	return types.NamespacedName{Name: utils.KubeSafeUniqueName(utils.GetChangeTransferPolicyName(ps.Name, testBranchDevelopment)), Namespace: ps.Namespace}
}

func (s sharedActivePathStrategies) ctp(ctx context.Context, g Gomega, ps *promoterv1alpha1.PromotionStrategy) promoterv1alpha1.ChangeTransferPolicy {
	var ctp promoterv1alpha1.ChangeTransferPolicy
	g.Expect(k8sClient.Get(ctx, s.ctpKey(ps), &ctp)).To(Succeed())
	return ctp
}

func (s sharedActivePathStrategies) prKey(activePath string) types.NamespacedName {
	return types.NamespacedName{
		Name:      utils.KubeSafeUniqueName(scmTarget.prName(s.gitRepo, path.Join(testBranchDevelopmentNext, activePath), testBranchDevelopment)),
		Namespace: s.namespaces,
	}
}

// hydrate makes a new dry commit and hydrates it onto activePath's proposed branch. It returns the dry SHA.
func (s sharedActivePathStrategies) hydrate(ctx context.Context, activePath, message string) string {
	GinkgoHelper()
	drySha, err := makeDryCommit(ctx, s.gitPath, message)
	Expect(err).NotTo(HaveOccurred())
	Expect(hydrateEnvironmentsBatchedTargets(ctx, s.gitRepo, []BatchedHydrationTarget{{
		Branch: path.Join(testBranchDevelopmentNext, activePath), ActivePath: activePath, BootstrapBranch: testBranchDevelopment, DrySha: drySha,
	}}, message, 0)).To(Succeed())
	return drySha
}

// promote hydrates a new dry commit for ps's app and waits for it to merge onto the shared active branch.
// It returns the dry SHA and the active tip the policy observed once promoted. SCMs do not webhook git
// notes, so the policy is nudged the way PromotionStrategy.enqueueOutOfSyncCTPs would.
func (s sharedActivePathStrategies) promote(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, activePath, message string) (drySha, tip string) {
	GinkgoHelper()
	drySha = s.hydrate(ctx, activePath, message)
	key := s.ctpKey(ps)
	Eventually(func(g Gomega) {
		enqueueCTP(key.Namespace, key.Name)
		ctp := s.ctp(ctx, g, ps)
		g.Expect(ctp.Status.Active.Dry.Sha).To(Equal(drySha))
		tip = ctp.Status.Active.Hydrated.Sha
	}, constants.EventuallyTimeout).Should(Succeed())
	return drySha, tip
}

// restore creates a RestoreActiveCommit for ps's development environment pointing at sha.
func (s sharedActivePathStrategies) restore(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, sha string) *promoterv1alpha1.RestoreActiveCommit {
	GinkgoHelper()
	rc := &promoterv1alpha1.RestoreActiveCommit{
		Name: utils.KubeSafeUniqueName(ps.Name + "-rc-" + sha), Namespace: ps.Namespace,
		Spec: promoterv1alpha1.RestoreActiveCommitSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: ps.Name},
			Branch:               testBranchDevelopment,
			Sha:                  sha,
		},
	}
	Expect(k8sClient.Create(ctx, rc)).To(Succeed())
	return rc
}

// mergeByHand merges activePath's proposed branch into the active branch with git, the way a person
// merging the pull request on the SCM would, and returns the active tip before the merge.
func (s sharedActivePathStrategies) mergeByHand(ctx context.Context, activePath string) string {
	GinkgoHelper()
	proposed := path.Join(testBranchDevelopmentNext, activePath)
	_, err := runGitCmd(ctx, s.gitPath, "fetch", "origin", testBranchDevelopment, proposed)
	Expect(err).NotTo(HaveOccurred())
	before, err := runGitCmd(ctx, s.gitPath, "rev-parse", "origin/"+testBranchDevelopment)
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, s.gitPath, "checkout", "-q", "-B", testBranchDevelopment, "origin/"+testBranchDevelopment)
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, s.gitPath, "merge", "--no-ff", "-X", "theirs", "-m", "merge "+proposed+" by hand", "origin/"+proposed)
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, s.gitPath, "push", "-q", "origin", testBranchDevelopment)
	Expect(err).NotTo(HaveOccurred())
	return strings.TrimSpace(before)
}
