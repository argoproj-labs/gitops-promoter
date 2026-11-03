/*
Copyright 2026.

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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/test/scmserver"
)

// Issue #2371: on GitHub, a repository forked from a parent with the same owner and the same environment branch
// names. GitHub resolves FindOpen's "owner:branch" head filter to the parent's branch, so the promoter never finds
// the pull request it opened on the fork. It then treats the pull request as externally merged or closed, closes it,
// and the ChangeTransferPolicy opens another one, until GitHub refuses more than 100 pull requests for the head SHA.
var _ = Describe("ChangeTransferPolicy on a GitHub fork owned by its parent's owner", func() {
	var (
		scmSecret            *v1.Secret
		scmProvider          *promoterv1alpha1.ScmProvider
		gitRepo              *promoterv1alpha1.GitRepository
		changeTransferPolicy *promoterv1alpha1.ChangeTransferPolicy
		fork                 testRepo
	)

	BeforeEach(func() {
		if scmTarget.provider != scmserver.GitHub {
			Skip("needs the GitHub scmserver, the only provider that models fork networks")
		}

		name := "gh-fork-" + utils.KubeSafeUniqueName(randomString(15))
		parent := scmTarget.newRepo(ctx, name)
		By("giving the parent the environment branches the fork will also use")
		setupInitialTestGitRepoOnServer(ctx, &promoterv1alpha1.GitRepository{
			Spec: scmTarget.gitRepositorySpec(parent, promoterv1alpha1.ScmProviderObjectReference{}),
		})
		fork = scmTarget.newFork(ctx, parent)

		scmSecret = &v1.Secret{Name: name, Namespace: "default", Data: scmTarget.secretData(fork)}
		scmProvider = &promoterv1alpha1.ScmProvider{
			Name:      name,
			Namespace: "default",
			Spec:      scmTarget.scmProviderSpec(fork, name),
		}
		gitRepo = &promoterv1alpha1.GitRepository{
			Name:      name,
			Namespace: "default",
			Spec: scmTarget.gitRepositorySpec(fork, promoterv1alpha1.ScmProviderObjectReference{
				Kind: promoterv1alpha1.ScmProviderKind,
				Name: name,
			}),
		}
		changeTransferPolicy = &promoterv1alpha1.ChangeTransferPolicy{
			Name:      name,
			Namespace: "default",
			Spec: promoterv1alpha1.ChangeTransferPolicySpec{
				RepositoryReference: promoterv1alpha1.ObjectReference{Name: name},
				ProposedBranch:      testBranchDevelopmentNext,
				ActiveBranch:        testBranchDevelopment,
				AutoMerge:           new(false),
			},
		}

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())
	})

	AfterEach(func() {
		if scmTarget.provider != scmserver.GitHub {
			return
		}
		Expect(k8sClient.Delete(ctx, changeTransferPolicy)).To(Succeed())
		Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
	})

	It("keeps the pull request it opened instead of closing and recreating it", func() {
		// Any FindOpen after the create starts the loop. Nothing in this spec changes the PullRequest after it is
		// opened, so make the periodic resync (5m by default) come around within the Consistently window.
		setPullRequestRequeueDuration(ctx, time.Second)
		gitPath := GinkgoT().TempDir()
		By("hydrating a change on the fork")
		_, _ = makeChangeAndHydrateRepo(gitPath, gitRepo, "", "")

		prKey := types.NamespacedName{
			Name:      utils.KubeSafeUniqueName(scmTarget.prName(gitRepo, testBranchDevelopmentNext, testBranchDevelopment)),
			Namespace: "default",
		}
		By("waiting for the promoter to open a pull request on the fork")
		var firstID string
		Eventually(func(g Gomega) {
			var pr promoterv1alpha1.PullRequest
			g.Expect(k8sClient.Get(ctx, prKey, &pr)).To(Succeed())
			g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
			g.Expect(pr.Status.ID).NotTo(BeEmpty())
			firstID = pr.Status.ID
		}, constants.EventuallyTimeout).Should(Succeed())

		By("checking that the promoter keeps that pull request open and opens no other")
		Consistently(func(g Gomega) {
			prs, err := scmTarget.admin.PullRequests(ctx, fork.ID, "")
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(prs).To(HaveLen(1), "the promoter closed its pull request and opened another")
			g.Expect(prs[0].State).To(Equal("open"))

			var pr promoterv1alpha1.PullRequest
			g.Expect(k8sClient.Get(ctx, prKey, &pr)).To(Succeed())
			g.Expect(pr.Status.ID).To(Equal(firstID))
		}, 15*time.Second, 250*time.Millisecond).Should(Succeed())
	})
})
