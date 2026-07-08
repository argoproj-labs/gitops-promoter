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
	"strings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
)

const (
	staticLabelsExpression             = "['lgtm', 'approved']"
	perCommitStatusKeyLabelsExpression = `flatten([
  ['promoter'],
  map(
    Spec.ProposedCommitStatuses,
    {len(.Key) > 50 ? .Key[:50] : .Key}
  )
])`
	allGatesPassLabelsExpression = `len(Status.Proposed.CommitStatuses) > 0 &&
all(Status.Proposed.CommitStatuses, {.Phase == 'success'})
  ? ['lgtm', 'approved']
  : []`
)

var _ = Describe("ChangeTransferPolicy pull request label expressions", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
		fake.ResetLabelCallCount()
	})

	It("evaluates a static label list onto PullRequest.spec.labels", func() {
		fixtures := setupCTPLabelExpressionTest("ctp-labels-static", staticLabelsExpression, nil)
		defer fixtures.cleanup(ctx)

		makeChangeAndHydrateRepo(fixtures.gitPath, fixtures.gitRepo, "", "")

		pr := fixtures.waitForPullRequest(ctx)
		Expect(pr.Spec.Labels).To(ConsistOf("lgtm", "approved"))
	})

	It("evaluates one SCM label per configured proposed commit status key plus promoter", func() {
		fixtures := setupCTPLabelExpressionTest("ctp-labels-per-key", perCommitStatusKeyLabelsExpression, func(ctp *promoterv1alpha1.ChangeTransferPolicy) {
			ctp.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "security-scan"},
				{Key: "deployment-freeze"},
			}
		})
		defer fixtures.cleanup(ctx)

		makeChangeAndHydrateRepo(fixtures.gitPath, fixtures.gitRepo, "", "")

		pr := fixtures.waitForPullRequest(ctx)
		Expect(pr.Spec.Labels).To(ConsistOf("promoter", "security-scan", "deployment-freeze"))
	})

	It("adds and clears labels based on proposed commit status phases", func() {
		const gateKey = "label-gate"

		fixtures := setupCTPLabelExpressionTest("ctp-labels-gates-pass", allGatesPassLabelsExpression, func(ctp *promoterv1alpha1.ChangeTransferPolicy) {
			ctp.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: gateKey},
			}
		})
		defer fixtures.cleanup(ctx)

		makeChangeAndHydrateRepo(fixtures.gitPath, fixtures.gitRepo, "", "")

		pr := fixtures.waitForPullRequest(ctx)
		Expect(pr.Spec.Labels).To(BeEmpty())

		Eventually(func(g Gomega) {
			sha, err := runGitCmd(ctx, fixtures.gitPath, "rev-parse", "origin/"+fixtures.ctp.Spec.ProposedBranch)
			g.Expect(err).NotTo(HaveOccurred())

			fixtures.commitStatus.Spec.Name = gateKey
			fixtures.commitStatus.Spec.Sha = strings.TrimSpace(sha)
			fixtures.commitStatus.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
			fixtures.commitStatus.Labels = map[string]string{
				promoterv1alpha1.CommitStatusLabel: gateKey,
			}
			g.Expect(k8sClient.Create(ctx, fixtures.commitStatus)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, fixtures.ctpNamespacedName(), fixtures.ctp)).To(Succeed())
			g.Expect(fixtures.ctp.Status.Proposed.CommitStatuses).To(ContainElement(Satisfy(func(cs promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase) bool {
				return cs.Key == gateKey && cs.Phase == string(promoterv1alpha1.CommitPhaseSuccess)
			})))

			pr := fixtures.getPullRequest(ctx)
			g.Expect(pr.Spec.Labels).To(ConsistOf("lgtm", "approved"))
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: fixtures.commitStatus.Name, Namespace: fixtures.commitStatus.Namespace}, fixtures.commitStatus)).To(Succeed())
			fixtures.commitStatus.Spec.Phase = promoterv1alpha1.CommitPhaseFailure
			g.Expect(k8sClient.Update(ctx, fixtures.commitStatus)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, fixtures.ctpNamespacedName(), fixtures.ctp)).To(Succeed())
			g.Expect(fixtures.ctp.Status.Proposed.CommitStatuses).To(ContainElement(Satisfy(func(cs promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase) bool {
				return cs.Key == gateKey && cs.Phase == string(promoterv1alpha1.CommitPhaseFailure)
			})))

			pr := fixtures.getPullRequest(ctx)
			g.Expect(pr.Spec.Labels).To(BeEmpty())
		}, constants.EventuallyTimeout).Should(Succeed())
	})

	It("reports Ready=False when the label expression fails to compile", func() {
		fixtures := setupCTPLabelExpressionTest("ctp-labels-compile-fail", `Status.Invalid..Field`, nil)
		defer fixtures.cleanup(ctx)

		makeChangeAndHydrateRepo(fixtures.gitPath, fixtures.gitRepo, "", "")

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, fixtures.ctpNamespacedName(), fixtures.ctp)).To(Succeed())
			ready := meta.FindStatusCondition(fixtures.ctp.Status.Conditions, string(promoterConditions.Ready))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(ready.Reason).To(Equal(string(promoterConditions.ReconciliationError)))
			g.Expect(ready.Message).To(ContainSubstring("failed to evaluate pull request labels"))
			g.Expect(ready.Message).To(ContainSubstring("failed to compile expression"))
		}, constants.EventuallyTimeout).Should(Succeed())

		var pr promoterv1alpha1.PullRequest
		err := k8sClient.Get(ctx, fixtures.prNamespacedName(), &pr)
		gomega := NewGomegaWithT(GinkgoTB())
		gomega.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		gomega.Expect(fake.LabelCallCount()).To(BeZero())
	})

	It("reports Ready=False when the label expression fails at evaluation time", func() {
		fixtures := setupCTPLabelExpressionTest("ctp-labels-eval-fail", `Status.Proposed.CommitStatuses[0].Phase`, nil)
		defer fixtures.cleanup(ctx)

		makeChangeAndHydrateRepo(fixtures.gitPath, fixtures.gitRepo, "", "")

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, fixtures.ctpNamespacedName(), fixtures.ctp)).To(Succeed())
			ready := meta.FindStatusCondition(fixtures.ctp.Status.Conditions, string(promoterConditions.Ready))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(ready.Reason).To(Equal(string(promoterConditions.ReconciliationError)))
			g.Expect(ready.Message).To(ContainSubstring("failed to evaluate pull request labels"))
			g.Expect(ready.Message).To(ContainSubstring("failed to evaluate expression"))
		}, constants.EventuallyTimeout).Should(Succeed())

		var pr promoterv1alpha1.PullRequest
		err := k8sClient.Get(ctx, fixtures.prNamespacedName(), &pr)
		gomega := NewGomegaWithT(GinkgoTB())
		gomega.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		gomega.Expect(fake.LabelCallCount()).To(BeZero())
	})

	It("reports Ready=False when the label expression returns invalid label names", func() {
		fixtures := setupCTPLabelExpressionTest("ctp-labels-invalid-output", `['']`, nil)
		defer fixtures.cleanup(ctx)

		makeChangeAndHydrateRepo(fixtures.gitPath, fixtures.gitRepo, "", "")

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, fixtures.ctpNamespacedName(), fixtures.ctp)).To(Succeed())
			ready := meta.FindStatusCondition(fixtures.ctp.Status.Conditions, string(promoterConditions.Ready))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			g.Expect(ready.Reason).To(Equal(string(promoterConditions.ReconciliationError)))
			g.Expect(ready.Message).To(ContainSubstring("failed to evaluate pull request labels"))
			g.Expect(ready.Message).To(ContainSubstring("expression returned invalid label names"))
			g.Expect(ready.Message).To(ContainSubstring("must not be empty"))
		}, constants.EventuallyTimeout).Should(Succeed())

		var pr promoterv1alpha1.PullRequest
		err := k8sClient.Get(ctx, fixtures.prNamespacedName(), &pr)
		gomega := NewGomegaWithT(GinkgoTB())
		gomega.Expect(apierrors.IsNotFound(err)).To(BeTrue())
		gomega.Expect(fake.LabelCallCount()).To(BeZero())
	})
})

type ctpLabelExpressionFixtures struct {
	name         string
	scmSecret    *v1.Secret
	scmProvider  *promoterv1alpha1.ScmProvider
	gitRepo      *promoterv1alpha1.GitRepository
	commitStatus *promoterv1alpha1.CommitStatus
	ctp          *promoterv1alpha1.ChangeTransferPolicy
	prName       string
	gitPath      string
}

func (f *ctpLabelExpressionFixtures) ctpNamespacedName() types.NamespacedName {
	return types.NamespacedName{Name: f.name, Namespace: "default"}
}

func (f *ctpLabelExpressionFixtures) prNamespacedName() types.NamespacedName {
	return types.NamespacedName{Name: f.prName, Namespace: "default"}
}

func (f *ctpLabelExpressionFixtures) waitForPullRequest(ctx context.Context) promoterv1alpha1.PullRequest {
	var pr promoterv1alpha1.PullRequest
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, f.prNamespacedName(), &pr)).To(Succeed())
		g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
	}, constants.EventuallyTimeout).Should(Succeed())
	return pr
}

func (f *ctpLabelExpressionFixtures) getPullRequest(ctx context.Context) promoterv1alpha1.PullRequest {
	var pr promoterv1alpha1.PullRequest
	Expect(k8sClient.Get(ctx, f.prNamespacedName(), &pr)).To(Succeed())
	return pr
}

func (f *ctpLabelExpressionFixtures) cleanup(ctx context.Context) {
	if f.ctp != nil {
		_ = k8sClient.Delete(ctx, f.ctp)
	}
	if f.commitStatus != nil {
		_ = k8sClient.Delete(ctx, f.commitStatus)
	}
	if f.gitRepo != nil {
		_ = k8sClient.Delete(ctx, f.gitRepo)
	}
	if f.scmProvider != nil {
		_ = k8sClient.Delete(ctx, f.scmProvider)
	}
	if f.scmSecret != nil {
		_ = k8sClient.Delete(ctx, f.scmSecret)
	}
	if f.gitPath != "" {
		_ = os.RemoveAll(f.gitPath)
	}
}

func setupCTPLabelExpressionTest(
	namePrefix string,
	expression string,
	configureCTP func(*promoterv1alpha1.ChangeTransferPolicy),
) ctpLabelExpressionFixtures {
	var fixtures ctpLabelExpressionFixtures
	fixtures.name, fixtures.scmSecret, fixtures.scmProvider, fixtures.gitRepo, fixtures.commitStatus, fixtures.ctp = changeTransferPolicyResources(context.Background(), namePrefix, "default")

	fixtures.ctp.Spec.ProposedBranch = testBranchDevelopmentNext
	fixtures.ctp.Spec.ActiveBranch = testBranchDevelopment
	fixtures.ctp.Spec.AutoMerge = new(false)
	fixtures.ctp.Spec.PullRequest = &promoterv1alpha1.PullRequestPolicySpec{
		Labels: &promoterv1alpha1.ScmLabelsSpec{
			Expression: expression,
		},
	}
	if configureCTP != nil {
		configureCTP(fixtures.ctp)
	}

	Expect(k8sClient.Create(context.Background(), fixtures.scmSecret)).To(Succeed())
	Expect(k8sClient.Create(context.Background(), fixtures.scmProvider)).To(Succeed())
	Expect(k8sClient.Create(context.Background(), fixtures.gitRepo)).To(Succeed())
	Expect(k8sClient.Create(context.Background(), fixtures.ctp)).To(Succeed())

	var err error
	fixtures.gitPath, err = os.MkdirTemp("", "*")
	Expect(err).NotTo(HaveOccurred())

	fixtures.prName = utils.KubeSafeUniqueName(utils.GetPullRequestName(
		fixtures.gitRepo.Spec.Fake.Owner,
		fixtures.gitRepo.Spec.Fake.Name,
		fixtures.ctp.Spec.ProposedBranch,
		fixtures.ctp.Spec.ActiveBranch,
	))

	return fixtures
}
