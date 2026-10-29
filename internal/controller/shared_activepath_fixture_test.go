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
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

const (
	sharedActivePathOne = "apps/app-one"
	sharedActivePathTwo = "apps/app-two"
)

// sharedActivePathStrategies is two PromotionStrategies whose single development environment shares one
// active branch and differ only by spec.activePath, as in the "one branch per environment" layout.
type sharedActivePathStrategies struct {
	gitRepo    *promoterv1alpha1.GitRepository
	psOne      *promoterv1alpha1.PromotionStrategy
	psTwo      *promoterv1alpha1.PromotionStrategy
	gitPath    string
	namespaces string
}

func setupSharedActivePathStrategies(ctx context.Context) sharedActivePathStrategies {
	GinkgoHelper()
	_, scmSecret, scmProvider, gitRepo, _, _, base := promotionStrategyResource(ctx, "restore-shared-active-path", "default")
	setupInitialTestGitRepoForActivePath(ctx, gitRepo)
	Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
	Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
	Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())

	s := sharedActivePathStrategies{gitRepo: gitRepo, namespaces: "default"}
	for _, app := range []struct {
		suffix, activePath string
		ps                 **promoterv1alpha1.PromotionStrategy
	}{{"-app-one", sharedActivePathOne, &s.psOne}, {"-app-two", sharedActivePathTwo, &s.psTwo}} {
		ps := base.DeepCopy()
		ps.Name = base.Name + app.suffix
		ps.Spec.ActivePath = app.activePath
		ps.Spec.Environments = []promoterv1alpha1.Environment{{Branch: testBranchDevelopment, AutoMerge: new(true)}}
		declareDependentsSuccessfulGate(ps)
		Expect(k8sClient.Create(ctx, ps)).To(Succeed())
		createDependentsSuccessfulCommitStatus(ctx, ps)
		*app.ps = ps
	}
	DeferCleanup(func() {
		for _, ps := range []*promoterv1alpha1.PromotionStrategy{s.psOne, s.psTwo} {
			deleteRestoreActiveCommitsForPolicy(ctx, s.ctpKey(ps).Name, ps.Namespace)
			_ = k8sClient.Delete(ctx, ps)
			_ = k8sClient.Delete(ctx, &promoterv1alpha1.DependentsSuccessfulCommitStatus{Name: ps.Name, Namespace: ps.Namespace})
		}
		_ = k8sClient.Delete(ctx, gitRepo)
		_ = k8sClient.Delete(ctx, scmProvider)
		_ = k8sClient.Delete(ctx, scmSecret)
	})

	Eventually(func(g Gomega) {
		g.Expect(s.ctp(ctx, g, s.psOne).Spec.ProposedBranch).To(Equal(path.Join(testBranchDevelopmentNext, sharedActivePathOne)))
		g.Expect(s.ctp(ctx, g, s.psTwo).Spec.ProposedBranch).To(Equal(path.Join(testBranchDevelopmentNext, sharedActivePathTwo)))
	}, constants.EventuallyTimeout).Should(Succeed())

	gitPath, err := cloneTestRepo(ctx, gitRepo)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(gitPath) })
	s.gitPath = gitPath
	return s
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
