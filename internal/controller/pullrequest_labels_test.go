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
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("PullRequest SCM labels", func() {
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
		fake.ResetLabelCallCount()
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

	It("applies and retracts SCM labels via the fake provider", func() {
		var name string
		name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "scm-labels-apply")
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

		fake.ResetLabelCallCount()
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			pullRequest.Spec.Labels = []string{"lgtm", "approved"}
			g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			g.Expect(pullRequest.Status.AppliedLabels).To(ConsistOf("lgtm", "approved"))
		}, constants.EventuallyTimeout).Should(Succeed())

		provider := fake.NewFakePullRequestProvider(k8sClient)
		applied, err := provider.GetAppliedLabels(ctx, *pullRequest)
		Expect(err).NotTo(HaveOccurred())
		Expect(applied).To(ConsistOf("lgtm", "approved"))
		Expect(fake.LabelCallCount()).To(BeNumerically(">", 0))

		callsAfterApply := fake.LabelCallCount()
		Consistently(func(g Gomega) {
			g.Expect(fake.LabelCallCount()).To(Equal(callsAfterApply))
		}, "2s", "200ms").Should(Succeed())

		fake.ResetLabelCallCount()
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			pullRequest.Spec.Labels = nil
			g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			g.Expect(pullRequest.Status.AppliedLabels).To(BeEmpty())
		}, constants.EventuallyTimeout).Should(Succeed())

		applied, err = provider.GetAppliedLabels(ctx, *pullRequest)
		Expect(err).NotTo(HaveOccurred())
		Expect(applied).To(BeEmpty())
	})

	It("re-applies labels removed externally on the SCM", func() {
		var name string
		name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "scm-labels-drift")
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

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			pullRequest.Spec.Labels = []string{"lgtm", "approved"}
			g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			g.Expect(pullRequest.Status.AppliedLabels).To(ConsistOf("lgtm", "approved"))
		}, constants.EventuallyTimeout).Should(Succeed())

		Expect(fake.SetScmLabels(ctx, k8sClient, *pullRequest, []string{"approved"})).To(Succeed())

		fake.ResetLabelCallCount()
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			pullRequest.Spec.Title = pullRequest.Spec.Title + " "
			g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			g.Expect(pullRequest.Status.AppliedLabels).To(ConsistOf("lgtm", "approved"))
		}, constants.EventuallyTimeout).Should(Succeed())

		provider := fake.NewFakePullRequestProvider(k8sClient)
		applied, err := provider.GetAppliedLabels(ctx, *pullRequest)
		Expect(err).NotTo(HaveOccurred())
		Expect(applied).To(ConsistOf("lgtm", "approved"))
		Expect(fake.LabelCallCount()).To(BeNumerically(">", 0))
	})

	It("leaves third-party SCM labels alone during reconcile", func() {
		const thirdPartyLabel = "tide/merge"

		var name string
		name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "scm-labels-third-party")
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

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			pullRequest.Spec.Labels = []string{"lgtm", "approved"}
			g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			g.Expect(pullRequest.Status.AppliedLabels).To(ConsistOf("lgtm", "approved"))
		}, constants.EventuallyTimeout).Should(Succeed())

		Expect(fake.SetScmLabels(ctx, k8sClient, *pullRequest, []string{"lgtm", "approved", thirdPartyLabel})).To(Succeed())

		fake.ResetLabelCallCount()
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			pullRequest.Spec.Title = pullRequest.Spec.Title + " "
			g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		provider := fake.NewFakePullRequestProvider(k8sClient)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			g.Expect(pullRequest.Status.AppliedLabels).To(ConsistOf("lgtm", "approved"))
			g.Expect(pullRequest.Status.AppliedLabels).NotTo(ContainElement(thirdPartyLabel))

			applied, err := provider.GetAppliedLabels(ctx, *pullRequest)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(applied).To(ConsistOf("lgtm", "approved", thirdPartyLabel))
		}, constants.EventuallyTimeout).Should(Succeed())

		Expect(fake.LabelCallCount()).To(BeZero())
	})

	It("removes retracted promoter labels but leaves third-party labels on the SCM", func() {
		const thirdPartyLabel = "tide/merge"

		var name string
		name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "scm-labels-shrink-third-party")
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

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			pullRequest.Spec.Labels = []string{"lgtm", "approved"}
			g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			g.Expect(pullRequest.Status.AppliedLabels).To(ConsistOf("lgtm", "approved"))
		}, constants.EventuallyTimeout).Should(Succeed())

		Expect(fake.SetScmLabels(ctx, k8sClient, *pullRequest, []string{"lgtm", "approved", thirdPartyLabel})).To(Succeed())

		fake.ResetLabelCallCount()
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			pullRequest.Spec.Labels = []string{"lgtm"}
			g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		provider := fake.NewFakePullRequestProvider(k8sClient)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, pullRequest)).To(Succeed())
			g.Expect(pullRequest.Status.AppliedLabels).To(ConsistOf("lgtm"))

			applied, err := provider.GetAppliedLabels(ctx, *pullRequest)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(applied).To(ConsistOf("lgtm", thirdPartyLabel))
			g.Expect(applied).NotTo(ContainElement("approved"))
		}, constants.EventuallyTimeout).Should(Succeed())

		Expect(fake.LabelCallCount()).To(BeNumerically(">", 0))
	})
})

var _ = Describe("PromotionStrategy pullRequest propagation", func() {
	var (
		ctx               context.Context
		scmSecret         *v1.Secret
		scmProvider       *promoterv1alpha1.ScmProvider
		gitRepo           *promoterv1alpha1.GitRepository
		promotionStrategy *promoterv1alpha1.PromotionStrategy
		resourceName      types.NamespacedName
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	AfterEach(func() {
		if promotionStrategy != nil {
			_ = k8sClient.Delete(ctx, promotionStrategy)
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

	It("copies spec.pullRequest to each ChangeTransferPolicy", func() {
		var name string
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "labels-propagation-ps", "default")
		setupInitialTestGitRepoOnServer(ctx, gitRepo)

		resourceName = types.NamespacedName{Name: name, Namespace: "default"}
		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

		Eventually(func(g Gomega) {
			var ctpList promoterv1alpha1.ChangeTransferPolicyList
			g.Expect(k8sClient.List(ctx, &ctpList, client.InNamespace("default"), client.MatchingLabels{
				promoterv1alpha1.PromotionStrategyLabel: name,
			})).To(Succeed())
			g.Expect(ctpList.Items).NotTo(BeEmpty())
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, resourceName, promotionStrategy)).To(Succeed())
			promotionStrategy.Spec.PullRequest = &promoterv1alpha1.PullRequestPolicySpec{
				Labels: &promoterv1alpha1.ScmLabelsSpec{
					Expression: "['lgtm']",
				},
			}
			g.Expect(k8sClient.Update(ctx, promotionStrategy)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			var ctpList promoterv1alpha1.ChangeTransferPolicyList
			g.Expect(k8sClient.List(ctx, &ctpList, client.InNamespace("default"), client.MatchingLabels{
				promoterv1alpha1.PromotionStrategyLabel: name,
			})).To(Succeed())
			for _, ctp := range ctpList.Items {
				g.Expect(ctp.Spec.PullRequest).NotTo(BeNil())
				g.Expect(ctp.Spec.PullRequest.Labels).NotTo(BeNil())
				g.Expect(ctp.Spec.PullRequest.Labels.Expression).To(Equal("['lgtm']"))
			}
		}, constants.EventuallyTimeout).Should(Succeed())
	})
})
