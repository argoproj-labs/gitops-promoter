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

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// perBranchReviewersExpression is the shape proposed on the reviewers issue: no reviewers when the
// environment auto-merges, otherwise reviewers chosen by active branch, with users as bare strings
// and groups as objects.
const perBranchReviewersExpression = `let autoMerge = Spec.AutoMerge ?? true;
autoMerge ? [] :
  Spec.ActiveBranch == 'environment/development'
    ? ['alice', {group: 'release-managers'}]
    : []`

// excludedBranchReviewersExpression returns reviewers only for a branch the fixture does not use,
// so evaluation succeeds but yields an empty set for the environment under test.
const excludedBranchReviewersExpression = `Spec.ActiveBranch == 'environment/production' ? ['alice'] : []`

var _ = Describe("ChangeTransferPolicy pull request reviewer expressions", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("evaluates reviewers onto PullRequest.spec.reviewers for the active branch", func() {
		fixtures := setupCTPLabelExpressionTest("ctp-reviewers-per-branch", "[]", func(ctp *promoterv1alpha1.ChangeTransferPolicy) {
			ctp.Spec.PullRequest = &promoterv1alpha1.PullRequestPolicySpec{
				Reviewers: &promoterv1alpha1.ScmReviewersSpec{Expression: perBranchReviewersExpression},
			}
		})
		defer fixtures.cleanup(ctx)

		makeChangeAndHydrateRepo(fixtures.gitPath, fixtures.gitRepo, "", "")

		pr := fixtures.waitForPullRequest(ctx)
		Expect(pr.Spec.Reviewers).To(Equal([]promoterv1alpha1.PullRequestReviewer{
			{User: "alice"},
			{Group: "release-managers"},
		}))
	})

	It("writes no reviewers when the expression excludes this environment", func() {
		fixtures := setupCTPLabelExpressionTest("ctp-reviewers-excluded", "[]", func(ctp *promoterv1alpha1.ChangeTransferPolicy) {
			ctp.Spec.PullRequest = &promoterv1alpha1.PullRequestPolicySpec{
				Reviewers: &promoterv1alpha1.ScmReviewersSpec{Expression: excludedBranchReviewersExpression},
			}
		})
		defer fixtures.cleanup(ctx)

		makeChangeAndHydrateRepo(fixtures.gitPath, fixtures.gitRepo, "", "")

		pr := fixtures.waitForPullRequest(ctx)
		Expect(pr.Spec.Reviewers).To(BeEmpty())
	})

	It("leaves spec.reviewers unset when no reviewers are configured", func() {
		fixtures := setupCTPLabelExpressionTest("ctp-reviewers-unset", staticLabelsExpression, nil)
		defer fixtures.cleanup(ctx)

		makeChangeAndHydrateRepo(fixtures.gitPath, fixtures.gitRepo, "", "")

		pr := fixtures.waitForPullRequest(ctx)
		Expect(pr.Spec.Reviewers).To(BeEmpty())
	})
})

var _ = Describe("PullRequest SCM reviewers", func() {
	var (
		ctx          context.Context
		scmSecret    *v1.Secret
		scmProvider  *promoterv1alpha1.ScmProvider
		gitRepo      *promoterv1alpha1.GitRepository
		pullRequest  *promoterv1alpha1.PullRequest
		resourceName types.NamespacedName
	)

	BeforeEach(func() {
		ctx = context.Background()
		fake.ResetReviewerCallCount()
	})

	AfterEach(func() {
		if pullRequest != nil {
			_ = k8sClient.Delete(ctx, pullRequest)
		}
		if gitRepo != nil {
			_ = k8sClient.Delete(ctx, gitRepo)
		}
		if scmProvider != nil {
			_ = k8sClient.Delete(ctx, scmProvider)
		}
		if scmSecret != nil {
			_ = k8sClient.Delete(ctx, scmSecret)
		}
	})

	It("applies and retracts SCM reviewers via the fake provider", func() {
		var name string
		name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "scm-reviewers-apply")
		resourceName = types.NamespacedName{Name: name, Namespace: "default"}

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
			g.Expect(pullRequest.Status.ID).NotTo(BeEmpty())
		}, constants.EventuallyTimeout).Should(Succeed())

		fake.ResetReviewerCallCount()
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			pullRequest.Spec.Reviewers = []promoterv1alpha1.PullRequestReviewer{
				{User: "alice"},
				{Group: "release-managers"},
			}
			g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			g.Expect(pullRequest.Status.AppliedReviewers).To(ConsistOf(
				promoterv1alpha1.PullRequestReviewer{User: "alice"},
				promoterv1alpha1.PullRequestReviewer{Group: "release-managers"},
			))
		}, constants.EventuallyTimeout).Should(Succeed())

		provider := fake.NewFakePullRequestProvider(k8sClient)
		applied, err := provider.GetAppliedReviewers(ctx, *pullRequest)
		Expect(err).NotTo(HaveOccurred())
		Expect(applied).To(ConsistOf(
			promoterv1alpha1.PullRequestReviewer{User: "alice"},
			promoterv1alpha1.PullRequestReviewer{Group: "release-managers"},
		))

		// Wait for any in-flight reconcile to finish, then confirm the provider is left alone:
		// once spec and status agree there is nothing to add or remove.
		callsAfterApply := fake.ReviewerCallCount()
		Eventually(func(g Gomega) {
			current := fake.ReviewerCallCount()
			g.Expect(current).To(Equal(callsAfterApply))
			callsAfterApply = current
		}, constants.EventuallyTimeout, "500ms").Should(Succeed())

		Consistently(func(g Gomega) {
			g.Expect(fake.ReviewerCallCount()).To(Equal(callsAfterApply))
		}, "2s", "200ms").Should(Succeed())

		// Clearing spec.reviewers retracts the requests.
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			pullRequest.Spec.Reviewers = nil
			g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			g.Expect(pullRequest.Status.AppliedReviewers).To(BeEmpty())
		}, constants.EventuallyTimeout).Should(Succeed())

		applied, err = provider.GetAppliedReviewers(ctx, *pullRequest)
		Expect(err).NotTo(HaveOccurred())
		Expect(applied).To(BeEmpty())
	})

	It("requests only reviewers that are not already applied", func() {
		var name string
		name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "scm-reviewers-incremental")
		resourceName = types.NamespacedName{Name: name, Namespace: "default"}

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
			g.Expect(pullRequest.Status.ID).NotTo(BeEmpty())
		}, constants.EventuallyTimeout).Should(Succeed())

		// spec.reviewers changes bump metadata.generation, which is what triggers the reconcile
		// that pushes reviewers to the SCM.
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			pullRequest.Spec.Reviewers = []promoterv1alpha1.PullRequestReviewer{{User: "alice"}}
			g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			g.Expect(pullRequest.Status.AppliedReviewers).To(ConsistOf(promoterv1alpha1.PullRequestReviewer{User: "alice"}))
		}, constants.EventuallyTimeout).Should(Succeed())

		callsAfterFirstApply := fake.ReviewerCallCount()

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			pullRequest.Spec.Reviewers = []promoterv1alpha1.PullRequestReviewer{{User: "alice"}, {User: "bob"}}
			g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			g.Expect(pullRequest.Status.AppliedReviewers).To(ConsistOf(
				promoterv1alpha1.PullRequestReviewer{User: "alice"},
				promoterv1alpha1.PullRequestReviewer{User: "bob"},
			))
			g.Expect(fake.ReviewerCallCount()).To(BeNumerically(">", callsAfterFirstApply))
		}, constants.EventuallyTimeout).Should(Succeed())
	})
})
