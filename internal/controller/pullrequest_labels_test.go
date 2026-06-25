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
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
		fake.ResetLabelCallCount()
	})

	It("applies and retracts SCM labels via the fake provider", func() {
		name, scmSecret, scmProvider, gitRepo, pullRequest := pullRequestResources(ctx, "scm-labels-apply")
		typeNamespacedName := types.NamespacedName{Name: name, Namespace: "default"}

		Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
			g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
			g.Expect(pullRequest.Status.ID).NotTo(BeEmpty())
		}, constants.EventuallyTimeout).Should(Succeed())

		fake.ResetLabelCallCount()
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
			pullRequest.Spec.Labels = []string{"lgtm", "approved"}
			g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
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
			g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
			pullRequest.Spec.Labels = nil
			g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
		}, constants.EventuallyTimeout).Should(Succeed())

		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
			g.Expect(pullRequest.Status.AppliedLabels).To(BeEmpty())
		}, constants.EventuallyTimeout).Should(Succeed())

		applied, err = provider.GetAppliedLabels(ctx, *pullRequest)
		Expect(err).NotTo(HaveOccurred())
		Expect(applied).To(BeEmpty())
	})
})

var _ = Describe("PromotionStrategy pullRequest propagation", func() {
	It("copies spec.pullRequest to each ChangeTransferPolicy", func() {
		ctx := context.Background()
		var scmSecret *v1.Secret
		var scmProvider *promoterv1alpha1.ScmProvider
		var gitRepo *promoterv1alpha1.GitRepository
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var name string
		name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "labels-propagation-ps", "default")
		setupInitialTestGitRepoOnServer(ctx, gitRepo)

		typeNamespacedName := types.NamespacedName{Name: name, Namespace: "default"}
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
			g.Expect(k8sClient.Get(ctx, typeNamespacedName, promotionStrategy)).To(Succeed())
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

		_ = k8sClient.Delete(ctx, promotionStrategy)
	})
})
