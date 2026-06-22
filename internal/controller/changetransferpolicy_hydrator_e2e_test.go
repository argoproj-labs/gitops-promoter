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

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// End-to-end tests for ChangeTransferPolicy hydration path validation. These run the
// full controller stack against a live fake git server: webhook-driven reconciliation,
// git note propagation, and PromotionStrategy-owned ChangeTransferPolicy lifecycle.
var _ = Describe("ChangeTransferPolicy hydrator e2e", Label("e2e"), func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Context("when the hydrator writes metadata outside activePath", func() {
		const (
			activePathApp  = "apps/app-one"
			hydratorPath   = activePathApp + "/overlay/stag"
			testNamespace  = "default"
		)

		var (
			scmSecret            *v1.Secret
			scmProvider          *promoterv1alpha1.ScmProvider
			gitRepo              *promoterv1alpha1.GitRepository
			promotionStrategy    *promoterv1alpha1.PromotionStrategy
			changeTransferPolicy promoterv1alpha1.ChangeTransferPolicy
			ctpNamespacedName    types.NamespacedName
		)

		BeforeEach(func() {
			_, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "ctp-hydrator-e2e", testNamespace)
			setupInitialTestGitRepoForActivePath(ctx, gitRepo)

			promotionStrategy.Spec.ActivePath = activePathApp
			promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
				{Branch: testBranchDevelopment, AutoMerge: new(false)},
			}

			ctpNamespacedName = types.NamespacedName{
				Name:      utils.KubeSafeUniqueName(utils.GetChangeTransferPolicyName(promotionStrategy.Name, testBranchDevelopment)),
				Namespace: testNamespace,
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, ctpNamespacedName, &changeTransferPolicy)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(changeTransferPolicy.Spec.ActivePath).To(Equal(activePathApp))
				g.Expect(changeTransferPolicy.Spec.ProposedBranch).To(Equal(path.Join(testBranchDevelopmentNext, activePathApp)))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("surfaces a reconciliation error on the CTP and does not open a PR", func() {
			gitPath, err := cloneTestRepo(ctx, gitRepo)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath) }()

			drySha, err := makeDryCommit(ctx, gitPath, "dry commit at overlay path")
			Expect(err).NotTo(HaveOccurred())

			proposedBranch := changeTransferPolicy.Spec.ProposedBranch
			beforeSha, hydratedSha, err := pushHydratedBranchForPath(ctx, gitPath, proposedBranch,
				hydratorPath, testBranchDevelopment, drySha, "hydrated at misconfigured path")
			Expect(err).NotTo(HaveOccurred())
			Expect(pushGitNote(ctx, gitPath, hydratedSha, drySha)).To(Succeed())
			sendWebhookForPush(ctx, beforeSha, proposedBranch)

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, ctpNamespacedName, &changeTransferPolicy)).To(Succeed())

				ready := meta.FindStatusCondition(changeTransferPolicy.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ready.Reason).To(Equal(string(promoterConditions.ReconciliationError)))
				g.Expect(ready.Message).To(ContainSubstring(path.Join(activePathApp, "hydrator.metadata")))
				g.Expect(ready.Message).To(ContainSubstring("hydrator writes hydrator.metadata"))
				g.Expect(ready.Message).To(ContainSubstring(drySha))

				g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha).To(BeEmpty())
				g.Expect(changeTransferPolicy.Status.Proposed.Note).NotTo(BeNil())
				g.Expect(changeTransferPolicy.Status.Proposed.Note.DrySha).To(Equal(drySha))
				g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).NotTo(BeEmpty())
				g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).NotTo(Equal(changeTransferPolicy.Status.Active.Hydrated.Sha))

				var eventList v1.EventList
				g.Expect(k8sClient.List(ctx, &eventList, ctrlclient.InNamespace(testNamespace))).To(Succeed())
				g.Expect(hasEventWithReason(eventList, changeTransferPolicy.Name, constants.MissingProposedDryMetadataReason)).To(BeTrue())

				prName := utils.KubeSafeUniqueName(utils.GetPullRequestName(
					gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name,
					changeTransferPolicy.Spec.ProposedBranch, changeTransferPolicy.Spec.ActiveBranch,
				))
				err := k8sClient.Get(ctx, types.NamespacedName{Name: prName, Namespace: testNamespace}, &promoterv1alpha1.PullRequest{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})
