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
	"fmt"
	"os"
	"path"
	"strings"
	"time"

	"github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

//go:embed testdata/PromotionStrategy.yaml
var testPromotionStrategyYAML string

var _ = Describe("PromotionStrategy Controller", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the PromotionStrategy resource", func() {
			err := unmarshalYamlStrict(testPromotionStrategyYAML, &promoterv1alpha1.PromotionStrategy{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("When reconciling a resource with no commit statuses", func() {
		ctx := context.Background()

		It("should successfully reconcile the resource", func() {
			By("Creating the resources")

			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy := promotionStrategyResource(ctx, "promotion-strategy-no-commit-status", "default")
			setupInitialTestGitRepoOnServer(name, name)

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// Check that ChangeTransferPolicy's are created
			ctpDev := promoterv1alpha1.ChangeTransferPolicy{}
			ctpStaging := promoterv1alpha1.ChangeTransferPolicy{}
			ctpProd := promoterv1alpha1.ChangeTransferPolicy{}

			pullRequestDev := promoterv1alpha1.PullRequest{}
			pullRequestStaging := promoterv1alpha1.PullRequest{}
			pullRequestProd := promoterv1alpha1.PullRequest{}

			By("Checking that all the ChangeTransferPolicies are created and PRs are created and in their proper state with updated statuses and proper sha's")
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, typeNamespacedName, promotionStrategy)

				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())

				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))

				// By("Checking that the ChangeTransferPolicy for development has shas that are not empty, meaning we have reconciled the resource")
				g.Expect(ctpDev.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpDev.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpDev.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(ctpDev.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				// By("Checking that the ChangeTransferPolicy for staging has shas that are not empty, meaning we have reconciled the resource")
				g.Expect(ctpStaging.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpStaging.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(ctpStaging.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				// By("Checking that the ChangeTransferPolicy for production has shas that are not empty, meaning we have reconciled the resource")
				g.Expect(ctpProd.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpProd.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpProd.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(ctpProd.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				// By("Checking that the PromotionStrategy for development environment has the correct sha values from the ChangeTransferPolicy")
				g.Expect(ctpDev.Spec.ActiveBranch).To(Equal("environment/development"))
				g.Expect(ctpDev.Spec.ProposedBranch).To(Equal("environment/development-next"))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.Sha).To(Equal(ctpDev.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).To(Equal(ctpDev.Status.Active.Hydrated.Sha))
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[0].Active.CommitStatuses)).To(BeTrue())
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Dry.Sha).To(Equal(ctpDev.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).To(Equal(ctpDev.Status.Proposed.Hydrated.Sha))
				// Success due to PromotionStrategy not having any CommitStatuses configured
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[0].Proposed.CommitStatuses)).To(BeTrue())

				// By("Checking that the PromotionStrategy for staging environment has the correct sha values from the ChangeTransferPolicy")
				g.Expect(ctpStaging.Spec.ActiveBranch).To(Equal("environment/staging"))
				g.Expect(ctpStaging.Spec.ProposedBranch).To(Equal("environment/staging-next"))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Sha).To(Equal(ctpStaging.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Sha).To(Equal(ctpStaging.Status.Active.Hydrated.Sha))
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[1].Active.CommitStatuses)).To(BeTrue())
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.Dry.Sha).To(Equal(ctpStaging.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.Hydrated.Sha).To(Equal(ctpStaging.Status.Proposed.Hydrated.Sha))
				// Success due to PromotionStrategy not having any CommitStatuses configured
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[1].Proposed.CommitStatuses)).To(BeTrue())

				// By("Checking that the PromotionStrategy for production environment has the correct sha values from the ChangeTransferPolicy")
				g.Expect(ctpProd.Spec.ActiveBranch).To(Equal("environment/production"))
				g.Expect(ctpProd.Spec.ProposedBranch).To(Equal("environment/production-next"))
				g.Expect(promotionStrategy.Status.Environments[2].Active.Dry.Sha).To(Equal(ctpProd.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Active.Hydrated.Sha).To(Equal(ctpProd.Status.Active.Hydrated.Sha))
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[2].Active.CommitStatuses)).To(BeTrue())
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.Dry.Sha).To(Equal(ctpProd.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.Hydrated.Sha).To(Equal(ctpProd.Status.Proposed.Hydrated.Sha))
				// Success due to PromotionStrategy not having any CommitStatuses configured
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[2].Proposed.CommitStatuses)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Adding a pending commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			makeChangeAndHydrateRepo(gitPath, name, name, "this is a change to bump image\n\nThis is the body\nThis is a newline", "added pending commit from dry sha")

			simulateWebhook(ctx, k8sClient, &ctpDev)
			simulateWebhook(ctx, k8sClient, &ctpStaging)
			simulateWebhook(ctx, k8sClient, &ctpProd)

			By("Checking that the pull request for the development environment is created")
			Eventually(func(g Gomega) {
				// Dev PR should exist
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())

				g.Expect(ctpDev.Status.Proposed.Dry.Subject).To(Equal("this is a change to bump image"))
				g.Expect(ctpDev.Status.Proposed.Dry.Body).To(Equal("This is the body\nThis is a newline"))
				g.Expect(ctpDev.Status.Proposed.Dry.References[0].Commit.Subject).To(Equal("This is a fix for an upstream issue"))
				g.Expect(ctpDev.Status.Proposed.Dry.References[0].Commit.Body).To(Equal("This is a body of the commit"))
				g.Expect(ctpDev.Status.Proposed.Dry.References[0].Commit.Sha).To(Equal("c4c862564afe56abf8cc8ac683eee3dc8bf96108"))

				g.Expect(ctpDev.Status.Proposed.Hydrated.Subject).To(Equal("added pending commit from dry sha"))
				g.Expect(ctpDev.Status.Proposed.Hydrated.Body).To(ContainSubstring(""))

				g.Expect(ctpDev.Status.Active.Hydrated.Subject).To(Equal("initial commit"))
				g.Expect(ctpDev.Status.Active.Hydrated.Body).To(Equal(""))
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				// Staging PR should exist
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				// Production PR should exist
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Checking that the pull request for the development, staging, and production environments are closed")
			Eventually(func(g Gomega) {
				// The PRs should eventually close because of no commit status checks configured
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			// We shouldn't check for non-nil PRs, because promotion may just happen too fast for us to catch them
			// before they're merged. We should improve this test to check something more useful, like whether the
			// commits were updated on the active branches.
			// Eventually(func(g Gomega) {
			// 	err := k8sClient.Get(ctx, types.NamespacedName{
			// 		Name:      promotionStrategy.Name,
			// 		Namespace: promotionStrategy.Namespace,
			// 	}, promotionStrategy)
			// 	g.Expect(err).To(Succeed())
			// 	g.Expect(promotionStrategy.Status.Environments[0].PullRequest).To(Not(BeNil()))
			// 	g.Expect(promotionStrategy.Status.Environments[0].PullRequest.State).To(Equal(promoterv1alpha1.PullRequestOpen))
			// 	g.Expect(promotionStrategy.Status.Environments[1].PullRequest).To(Not(BeNil()))
			// 	g.Expect(promotionStrategy.Status.Environments[1].PullRequest.State).To(Equal(promoterv1alpha1.PullRequestOpen))
			// 	g.Expect(promotionStrategy.Status.Environments[2].PullRequest).To(Not(BeNil()))
			// 	g.Expect(promotionStrategy.Status.Environments[2].PullRequest.State).To(Equal(promoterv1alpha1.PullRequestOpen))
			// }, constants.EventuallyTimeout).Should(Succeed())

			By("Checking that the pull request for the development, staging, and production environments are closed and have had their ctp statuses cleared")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.PullRequest).To(BeNil())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.PullRequest).To(BeNil())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())
				g.Expect(ctpProd.Status.PullRequest).To(BeNil())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      promotionStrategy.Name,
					Namespace: promotionStrategy.Namespace,
				}, promotionStrategy)
				g.Expect(err).To(Succeed())
				g.Expect(promotionStrategy.Status.Environments[0].PullRequest).To(BeNil())
				g.Expect(promotionStrategy.Status.Environments[1].PullRequest).To(BeNil())
				g.Expect(promotionStrategy.Status.Environments[2].PullRequest).To(BeNil())
			}, constants.EventuallyTimeout).Should(Succeed())

			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
		})

		It("should successfully reconcile the resource using a ClusterScmProvider", func() {
			By("Creating the resources")

			name, scmSecret, _, gitRepo, _, _, promotionStrategy := promotionStrategyResource(ctx, "promotion-strategy-no-commit-status", "default")
			setupInitialTestGitRepoOnServer(name, name)

			clusterScmProvider := &promoterv1alpha1.ClusterScmProvider{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ScmProviderSpec{
					SecretRef: &v1.LocalObjectReference{Name: name},
					Fake:      &promoterv1alpha1.Fake{},
				},
				Status: promoterv1alpha1.ScmProviderStatus{},
			}
			gitRepo.Spec.ScmProviderRef = promoterv1alpha1.ScmProviderObjectReference{
				Kind: promoterv1alpha1.ClusterScmProviderKind,
				Name: name,
			}

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, clusterScmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// Check that ChangeTransferPolicy's are created
			ctpDev := promoterv1alpha1.ChangeTransferPolicy{}
			ctpStaging := promoterv1alpha1.ChangeTransferPolicy{}
			ctpProd := promoterv1alpha1.ChangeTransferPolicy{}

			pullRequestDev := promoterv1alpha1.PullRequest{}
			pullRequestStaging := promoterv1alpha1.PullRequest{}
			pullRequestProd := promoterv1alpha1.PullRequest{}

			By("Checking that all the ChangeTransferPolicies are created and PRs are created and in their proper state with updated statuses and proper sha's")
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, typeNamespacedName, promotionStrategy)

				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())

				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))

				// By("Checking that the ChangeTransferPolicy for development has shas that are not empty, meaning we have reconciled the resource")
				g.Expect(ctpDev.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpDev.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpDev.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(ctpDev.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				// By("Checking that the ChangeTransferPolicy for staging has shas that are not empty, meaning we have reconciled the resource")
				g.Expect(ctpStaging.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpStaging.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(ctpStaging.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				// By("Checking that the ChangeTransferPolicy for production has shas that are not empty, meaning we have reconciled the resource")
				g.Expect(ctpProd.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpProd.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpProd.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(ctpProd.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				// By("Checking that the PromotionStrategy for development environment has the correct sha values from the ChangeTransferPolicy")
				g.Expect(ctpDev.Spec.ActiveBranch).To(Equal("environment/development"))
				g.Expect(ctpDev.Spec.ProposedBranch).To(Equal("environment/development-next"))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.Sha).To(Equal(ctpDev.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).To(Equal(ctpDev.Status.Active.Hydrated.Sha))
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[0].Active.CommitStatuses)).To(BeTrue())
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Dry.Sha).To(Equal(ctpDev.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).To(Equal(ctpDev.Status.Proposed.Hydrated.Sha))
				// Success due to PromotionStrategy not having any CommitStatuses configured
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[0].Proposed.CommitStatuses)).To(BeTrue())

				// By("Checking that the PromotionStrategy for staging environment has the correct sha values from the ChangeTransferPolicy")
				g.Expect(ctpStaging.Spec.ActiveBranch).To(Equal("environment/staging"))
				g.Expect(ctpStaging.Spec.ProposedBranch).To(Equal("environment/staging-next"))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Sha).To(Equal(ctpStaging.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Sha).To(Equal(ctpStaging.Status.Active.Hydrated.Sha))
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[1].Active.CommitStatuses)).To(BeTrue())
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.Dry.Sha).To(Equal(ctpStaging.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.Hydrated.Sha).To(Equal(ctpStaging.Status.Proposed.Hydrated.Sha))
				// Success due to PromotionStrategy not having any CommitStatuses configured
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[1].Proposed.CommitStatuses)).To(BeTrue())

				// By("Checking that the PromotionStrategy for production environment has the correct sha values from the ChangeTransferPolicy")
				g.Expect(ctpProd.Spec.ActiveBranch).To(Equal("environment/production"))
				g.Expect(ctpProd.Spec.ProposedBranch).To(Equal("environment/production-next"))
				g.Expect(promotionStrategy.Status.Environments[2].Active.Dry.Sha).To(Equal(ctpProd.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Active.Hydrated.Sha).To(Equal(ctpProd.Status.Active.Hydrated.Sha))
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[2].Active.CommitStatuses)).To(BeTrue())
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.Dry.Sha).To(Equal(ctpProd.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.Hydrated.Sha).To(Equal(ctpProd.Status.Proposed.Hydrated.Sha))
				// Success due to PromotionStrategy not having any CommitStatuses configured
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[2].Proposed.CommitStatuses)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Adding a pending commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			makeChangeAndHydrateRepo(gitPath, name, name, "", "")

			simulateWebhook(ctx, k8sClient, &ctpDev)
			simulateWebhook(ctx, k8sClient, &ctpStaging)
			simulateWebhook(ctx, k8sClient, &ctpProd)

			By("Checking that the pull request for the development environment is closed because it is the lowest level env")
			Eventually(func(g Gomega) {
				// Dev PR should exist
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				// Staging PR should exist
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				// Production PR should exist
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Checking that the pull request for the development, staging, and production environments are closed")
			Eventually(func(g Gomega) {
				// The PRs should eventually close because of no commit status checks configured
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
		})

		// Happens when there is no hydrator.metadata on the active branch such as when the branch was just initialized and is empty
		It("should successfully reconcile the resource with no active dry sha", func() {
			By("Creating the resources")

			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy := promotionStrategyResource(ctx, "promotion-strategy-no-commit-status", "default")
			setupInitialTestGitRepoWithoutActiveMetadata(name, name)

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// Check that ChangeTransferPolicy's are created
			ctpDev := promoterv1alpha1.ChangeTransferPolicy{}
			ctpStaging := promoterv1alpha1.ChangeTransferPolicy{}
			ctpProd := promoterv1alpha1.ChangeTransferPolicy{}

			pullRequestDev := promoterv1alpha1.PullRequest{}
			pullRequestStaging := promoterv1alpha1.PullRequest{}
			pullRequestProd := promoterv1alpha1.PullRequest{}

			By("Checking that all the ChangeTransferPolicies are created and PRs are created and in their proper state with updated statuses and proper sha's")
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, typeNamespacedName, promotionStrategy)

				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())

				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))

				// By("Checking that the ChangeTransferPolicy for development has shas that are not empty, meaning we have reconciled the resource")
				// Active dry sha should be the same as proposed dry sha because we should have merged the branch with hydrator.metadata.
				g.Expect(ctpDev.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpDev.Status.Active.Dry.Sha).To(Equal(ctpDev.Status.Proposed.Dry.Sha))
				g.Expect(ctpDev.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(ctpDev.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				// By("Checking that the ChangeTransferPolicy for staging has shas that are not empty, meaning we have reconciled the resource")
				// Active dry sha should be the same as proposed dry sha because we should have merged the branch with hydrator.metadata.
				g.Expect(ctpStaging.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Equal(ctpStaging.Status.Proposed.Dry.Sha))
				g.Expect(ctpStaging.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(ctpStaging.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				// By("Checking that the ChangeTransferPolicy for production has shas that are not empty, meaning we have reconciled the resource")
				// Active dry sha should be the same as proposed dry sha because we should have merged the branch with hydrator.metadata.
				g.Expect(ctpProd.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpProd.Status.Active.Dry.Sha).To(Equal(ctpProd.Status.Proposed.Dry.Sha))
				g.Expect(ctpProd.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(ctpProd.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				// By("Checking that the PromotionStrategy for development environment has the correct sha values from the ChangeTransferPolicy")
				g.Expect(ctpDev.Spec.ActiveBranch).To(Equal("environment/development"))
				g.Expect(ctpDev.Spec.ProposedBranch).To(Equal("environment/development-next"))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.Sha).To(Equal(promotionStrategy.Status.Environments[0].Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).To(Equal(ctpDev.Status.Active.Hydrated.Sha))
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[0].Active.CommitStatuses)).To(BeTrue())
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Dry.Sha).To(Equal(ctpDev.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).To(Equal(ctpDev.Status.Proposed.Hydrated.Sha))
				// Success due to PromotionStrategy not having any CommitStatuses configured
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[0].Proposed.CommitStatuses)).To(BeTrue())

				// By("Checking that the PromotionStrategy for staging environment has the correct sha values from the ChangeTransferPolicy")
				g.Expect(ctpStaging.Spec.ActiveBranch).To(Equal("environment/staging"))
				g.Expect(ctpStaging.Spec.ProposedBranch).To(Equal("environment/staging-next"))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Sha).To(Equal(promotionStrategy.Status.Environments[1].Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Sha).To(Equal(ctpStaging.Status.Active.Hydrated.Sha))
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[1].Active.CommitStatuses)).To(BeTrue())
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.Dry.Sha).To(Equal(ctpStaging.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.Hydrated.Sha).To(Equal(ctpStaging.Status.Proposed.Hydrated.Sha))
				// Success due to PromotionStrategy not having any CommitStatuses configured
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[1].Proposed.CommitStatuses)).To(BeTrue())

				// By("Checking that the PromotionStrategy for production environment has the correct sha values from the ChangeTransferPolicy")
				g.Expect(ctpProd.Spec.ActiveBranch).To(Equal("environment/production"))
				g.Expect(ctpProd.Spec.ProposedBranch).To(Equal("environment/production-next"))
				g.Expect(promotionStrategy.Status.Environments[2].Active.Dry.Sha).To(Equal(promotionStrategy.Status.Environments[2].Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Active.Hydrated.Sha).To(Equal(ctpProd.Status.Active.Hydrated.Sha))
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[2].Active.CommitStatuses)).To(BeTrue())
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.Dry.Sha).To(Equal(ctpProd.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.Hydrated.Sha).To(Equal(ctpProd.Status.Proposed.Hydrated.Sha))
				// Success due to PromotionStrategy not having any CommitStatuses configured
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[2].Proposed.CommitStatuses)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Adding a pending commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			makeChangeAndHydrateRepo(gitPath, name, name, "", "")

			simulateWebhook(ctx, k8sClient, &ctpDev)
			simulateWebhook(ctx, k8sClient, &ctpStaging)
			simulateWebhook(ctx, k8sClient, &ctpProd)

			By("Checking that the pull request for the development environment is created")
			Eventually(func(g Gomega) {
				// Dev PR should exist
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				// Staging PR should exist
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				// Production PR should exist
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Checking that the pull request for the development, staging, and production environments are closed")
			Eventually(func(g Gomega) {
				// The PRs should eventually close because of no commit status checks configured
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
		})

		It("should successfully reconcile the resource with a git conflict", func() {
			By("Creating the resources")

			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy := promotionStrategyResource(ctx, "promotion-strategy-no-commit-status-conflict", "default")
			setupInitialTestGitRepoOnServer(name, name)

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// Check that ChangeTransferPolicy's are created
			ctpDev := promoterv1alpha1.ChangeTransferPolicy{}
			ctpStaging := promoterv1alpha1.ChangeTransferPolicy{}
			ctpProd := promoterv1alpha1.ChangeTransferPolicy{}

			pullRequestDev := promoterv1alpha1.PullRequest{}
			pullRequestStaging := promoterv1alpha1.PullRequest{}
			pullRequestProd := promoterv1alpha1.PullRequest{}

			By("Checking that all the ChangeTransferPolicies are created and PRs are created and in their proper state with updated statuses and proper sha's")
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, typeNamespacedName, promotionStrategy)

				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())

				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))

				// By("Checking that the ChangeTransferPolicy for development has shas that are not empty, meaning we have reconciled the resource")
				g.Expect(ctpDev.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpDev.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpDev.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(ctpDev.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				// By("Checking that the ChangeTransferPolicy for staging has shas that are not empty, meaning we have reconciled the resource")
				g.Expect(ctpStaging.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpStaging.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(ctpStaging.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				// By("Checking that the ChangeTransferPolicy for production has shas that are not empty, meaning we have reconciled the resource")
				g.Expect(ctpProd.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpProd.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(ctpProd.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(ctpProd.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				// By("Checking that the PromotionStrategy for development environment has the correct sha values from the ChangeTransferPolicy")
				g.Expect(ctpDev.Spec.ActiveBranch).To(Equal("environment/development"))
				g.Expect(ctpDev.Spec.ProposedBranch).To(Equal("environment/development-next"))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.Sha).To(Equal(ctpDev.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).To(Equal(ctpDev.Status.Active.Hydrated.Sha))
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[0].Active.CommitStatuses)).To(BeTrue())
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Dry.Sha).To(Equal(ctpDev.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).To(Equal(ctpDev.Status.Proposed.Hydrated.Sha))
				// Success due to PromotionStrategy not having any CommitStatuses configured
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[0].Proposed.CommitStatuses)).To(BeTrue())

				// By("Checking that the PromotionStrategy for staging environment has the correct sha values from the ChangeTransferPolicy")
				g.Expect(ctpStaging.Spec.ActiveBranch).To(Equal("environment/staging"))
				g.Expect(ctpStaging.Spec.ProposedBranch).To(Equal("environment/staging-next"))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Sha).To(Equal(ctpStaging.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Sha).To(Equal(ctpStaging.Status.Active.Hydrated.Sha))
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[1].Active.CommitStatuses)).To(BeTrue())
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.Dry.Sha).To(Equal(ctpStaging.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.Hydrated.Sha).To(Equal(ctpStaging.Status.Proposed.Hydrated.Sha))
				// Success due to PromotionStrategy not having any CommitStatuses configured
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[1].Proposed.CommitStatuses)).To(BeTrue())

				// By("Checking that the PromotionStrategy for production environment has the correct sha values from the ChangeTransferPolicy")
				g.Expect(ctpProd.Spec.ActiveBranch).To(Equal("environment/production"))
				g.Expect(ctpProd.Spec.ProposedBranch).To(Equal("environment/production-next"))
				g.Expect(promotionStrategy.Status.Environments[2].Active.Dry.Sha).To(Equal(ctpProd.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Active.Hydrated.Sha).To(Equal(ctpProd.Status.Active.Hydrated.Sha))
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[2].Active.CommitStatuses)).To(BeTrue())
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.Dry.Sha).To(Equal(ctpProd.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.Hydrated.Sha).To(Equal(ctpProd.Status.Proposed.Hydrated.Sha))
				// Success due to PromotionStrategy not having any CommitStatuses configured
				g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[2].Proposed.CommitStatuses)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Adding a pending commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			makeChangeAndHydrateRepo(gitPath, name, name, "", "")

			simulateWebhook(ctx, k8sClient, &ctpDev)
			simulateWebhook(ctx, k8sClient, &ctpStaging)
			simulateWebhook(ctx, k8sClient, &ctpProd)

			// Add initial commit on active branch to cause a conflict
			_, err = runGitCmd(gitPath, "checkout", ctpDev.Spec.ActiveBranch)
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(gitPath, "pull", "origin", ctpDev.Spec.ActiveBranch)
			Expect(err).NotTo(HaveOccurred())
			f, err := os.Create(path.Join(gitPath, "manifests-fake.yaml"))
			Expect(err).NotTo(HaveOccurred())
			str := fmt.Sprintf("{\"conflict\": \"%s\"}", time.Now().Format(time.RFC3339Nano))
			_, err = f.WriteString(str)
			Expect(err).NotTo(HaveOccurred())
			err = f.Close()
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(gitPath, "add", "manifests-fake.yaml")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(gitPath, "commit", "-m", "added fake manifests commit with timestamp")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(gitPath, "push", "-u", "origin", ctpDev.Spec.ActiveBranch)
			Expect(err).NotTo(HaveOccurred())

			By("Checking that there is no previous-environment commit status created, since no active checks are configured")
			csList := promoterv1alpha1.CommitStatusList{}
			err = k8sClient.List(ctx, &csList, client.MatchingLabels{
				promoterv1alpha1.CommitStatusLabel: promoterv1alpha1.PreviousEnvironmentCommitStatusKey,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(len(csList.Items)).To(Equal(0))

			By("Checking that the pull request for the development environment is closed")
			Eventually(func(g Gomega) {
				// Dev PR should exist
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				// Staging PR should exist
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				// Production PR should exist
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Checking that the pull request for the development, staging, and production environments are closed")
			Eventually(func(g Gomega) {
				// The PRs should eventually close because of no commit status checks configured
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
		})
	})

	Context("When reconciling a resource with active commit statuses", func() {
		It("should successfully reconcile the resource", func() {
			// Skip("Skipping test because of flakiness")
			By("Creating the resource")
			name, scmSecret, scmProvider, gitRepo, activeCommitStatusDevelopment, activeCommitStatusStaging, promotionStrategy := promotionStrategyResource(ctx, "promotion-strategy-with-active-commit-status", "default")
			setupInitialTestGitRepoOnServer(name, name)

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{
					Key: healthCheckCSKey,
				},
			}
			activeCommitStatusDevelopment.Spec.Name = healthCheckCSKey
			activeCommitStatusDevelopment.Labels = map[string]string{
				promoterv1alpha1.CommitStatusLabel: healthCheckCSKey,
			}
			activeCommitStatusStaging.Spec.Name = healthCheckCSKey
			activeCommitStatusStaging.Labels = map[string]string{
				promoterv1alpha1.CommitStatusLabel: healthCheckCSKey,
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, activeCommitStatusDevelopment)).To(Succeed())
			Expect(k8sClient.Create(ctx, activeCommitStatusStaging)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// We should now get PRs created for the ChangeTransferPolicies
			// Check that ProposedCommit are created
			ctpDev := promoterv1alpha1.ChangeTransferPolicy{}
			ctpStaging := promoterv1alpha1.ChangeTransferPolicy{}
			ctpProd := promoterv1alpha1.ChangeTransferPolicy{}

			pullRequestDev := promoterv1alpha1.PullRequest{}
			pullRequestStaging := promoterv1alpha1.PullRequest{}
			pullRequestProd := promoterv1alpha1.PullRequest{}
			By("Checking that all the ChangeTransferPolicies and PRs are created and in their proper state")
			Eventually(func(g Gomega) {
				// Make sure ctp's are created and the associated PRs
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Name).To(Equal(utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch))))

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Name).To(Equal(utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch))))

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())
				g.Expect(ctpProd.Name).To(Equal(utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch))))
			}).Should(Succeed())

			By("Adding a pending commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			drySha, _ := makeChangeAndHydrateRepo(gitPath, name, name, "", "")
			simulateWebhook(ctx, k8sClient, &ctpDev)
			simulateWebhook(ctx, k8sClient, &ctpStaging)
			simulateWebhook(ctx, k8sClient, &ctpProd)

			Eventually(func(g Gomega) {
				// Dev PR should be closed because it is the lowest level environment
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Succeed())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				// Check that the ChangeTransferPolicy for development has an active dry shas that match the expected dry sha meaning the git merge/push succeeded
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpDev.Name,
					Namespace: ctpDev.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Active.Dry.Sha).To(Equal(drySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Updating the commit status for the development environment to success")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      activeCommitStatusDevelopment.Name,
					Namespace: activeCommitStatusDevelopment.Namespace,
				}, activeCommitStatusDevelopment)
				g.Expect(err).To(Succeed())

				_, err = runGitCmd(gitPath, "fetch")
				Expect(err).NotTo(HaveOccurred())
				sha, err := runGitCmd(gitPath, "rev-parse", "origin/"+ctpDev.Spec.ActiveBranch)
				Expect(err).NotTo(HaveOccurred())
				sha = strings.TrimSpace(sha)

				g.Expect(sha).To(Not(Equal("")))
				activeCommitStatusDevelopment.Spec.Sha = sha
				activeCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				err = k8sClient.Update(ctx, activeCommitStatusDevelopment)
				GinkgoLogr.Info("Updated commit status for development to sha: " + sha + " for branch " + ctpDev.Spec.ActiveBranch)
				g.Expect(err).To(Succeed())

				// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Active.Hydrated.Sha).To(Equal(sha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("By checking that the staging pull request has been merged and the production pull request is still open")
			Eventually(func(g Gomega) {
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Updating the commit status for the staging environment to success")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      activeCommitStatusStaging.Name,
					Namespace: activeCommitStatusStaging.Namespace,
				}, activeCommitStatusStaging)
				g.Expect(err).To(Succeed())

				_, err = runGitCmd(gitPath, "fetch")
				Expect(err).NotTo(HaveOccurred())
				sha, err := runGitCmd(gitPath, "rev-parse", "origin/"+ctpStaging.Spec.ActiveBranch)
				Expect(err).NotTo(HaveOccurred())
				sha = strings.TrimSpace(sha)

				// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.Active.Hydrated.Sha).To(Equal(sha))

				// Get an updated proposed commit for prod so that we can use save the sha before we update the commit status letting it merge
				_, err = runGitCmd(gitPath, "fetch")
				Expect(err).NotTo(HaveOccurred())
				shaProdProposed, err := runGitCmd(gitPath, "rev-parse", "origin/"+ctpProd.Spec.ProposedBranch)
				Expect(err).NotTo(HaveOccurred())
				shaProdProposed = strings.TrimSpace(shaProdProposed)

				// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())
				g.Expect(ctpProd.Status.Proposed.Hydrated.Sha).To(Equal(shaProdProposed))

				activeCommitStatusStaging.Spec.Sha = sha
				activeCommitStatusStaging.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				err = k8sClient.Update(ctx, activeCommitStatusStaging)
				GinkgoLogr.Info("Updated commit status for staging to sha: " + sha)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("By checking that the production pull request has been merged")
			Eventually(func(g Gomega) {
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
		})

		It("should successfully reconcile the resource with no active dry sha", func() {
			By("Creating the resource")
			name, scmSecret, scmProvider, gitRepo, activeCommitStatusDevelopment, activeCommitStatusStaging, promotionStrategy := promotionStrategyResource(ctx, "promotion-strategy-with-active-commit-status", "default")
			setupInitialTestGitRepoWithoutActiveMetadata(name, name)

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{
					Key: healthCheckCSKey,
				},
			}
			activeCommitStatusDevelopment.Spec.Name = healthCheckCSKey
			activeCommitStatusDevelopment.Labels = map[string]string{
				promoterv1alpha1.CommitStatusLabel: healthCheckCSKey,
			}
			activeCommitStatusStaging.Spec.Name = healthCheckCSKey
			activeCommitStatusStaging.Labels = map[string]string{
				promoterv1alpha1.CommitStatusLabel: healthCheckCSKey,
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, activeCommitStatusDevelopment)).To(Succeed())
			Expect(k8sClient.Create(ctx, activeCommitStatusStaging)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// We should now get PRs created for the ChangeTransferPolicies
			// Check that ProposedCommit are created
			ctpDev := promoterv1alpha1.ChangeTransferPolicy{}
			ctpStaging := promoterv1alpha1.ChangeTransferPolicy{}
			ctpProd := promoterv1alpha1.ChangeTransferPolicy{}

			pullRequestDev := promoterv1alpha1.PullRequest{}
			pullRequestStaging := promoterv1alpha1.PullRequest{}
			pullRequestProd := promoterv1alpha1.PullRequest{}
			By("Checking that all the ChangeTransferPolicies and PRs are created and in their proper state")
			Eventually(func(g Gomega) {
				// Make sure ctp's are created and the associated PRs
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Name).To(Equal(utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch))))

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Name).To(Equal(utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch))))

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())
				g.Expect(ctpProd.Name).To(Equal(utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch))))
			}).Should(Succeed())

			By("Adding a pending commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			drySha, _ := makeChangeAndHydrateRepo(gitPath, name, name, "", "")
			simulateWebhook(ctx, k8sClient, &ctpDev)
			simulateWebhook(ctx, k8sClient, &ctpStaging)
			simulateWebhook(ctx, k8sClient, &ctpProd)

			Eventually(func(g Gomega) {
				// Dev PR should be closed because it is the lowest level environment
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				// Dev CTP's active dry sha should be the one we committed.
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Active.Dry.Sha).To(Equal(drySha))

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Succeed())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Updating the commit status for the development environment to success")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      activeCommitStatusDevelopment.Name,
					Namespace: activeCommitStatusDevelopment.Namespace,
				}, activeCommitStatusDevelopment)
				g.Expect(err).To(Succeed())

				sha := ctpDev.Status.Active.Hydrated.Sha
				activeCommitStatusDevelopment.Spec.Sha = sha
				activeCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				err = k8sClient.Update(ctx, activeCommitStatusDevelopment)
				GinkgoLogr.Info("Updated commit status for development to sha: " + sha + " for branch " + ctpDev.Spec.ActiveBranch)
				g.Expect(err).To(Succeed())

				// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Active.Hydrated.Sha).To(Equal(sha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("By checking that the staging pull request has been merged and the production pull request is still open")
			Eventually(func(g Gomega) {
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Updating the commit status for the staging environment to success")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      activeCommitStatusStaging.Name,
					Namespace: activeCommitStatusStaging.Namespace,
				}, activeCommitStatusStaging)
				g.Expect(err).To(Succeed())

				_, err = runGitCmd(gitPath, "fetch")
				Expect(err).NotTo(HaveOccurred())
				sha, err := runGitCmd(gitPath, "rev-parse", "origin/"+ctpStaging.Spec.ActiveBranch)
				Expect(err).NotTo(HaveOccurred())
				sha = strings.TrimSpace(sha)

				// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.Active.Hydrated.Sha).To(Equal(sha))

				// Get an updated proposed commit for prod so that we can use save the sha before we update the commit status letting it merge
				_, err = runGitCmd(gitPath, "fetch")
				Expect(err).NotTo(HaveOccurred())
				shaProdProposed, err := runGitCmd(gitPath, "rev-parse", "origin/"+ctpProd.Spec.ProposedBranch)
				Expect(err).NotTo(HaveOccurred())
				shaProdProposed = strings.TrimSpace(shaProdProposed)

				// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())
				g.Expect(ctpProd.Status.Proposed.Hydrated.Sha).To(Equal(shaProdProposed))

				activeCommitStatusStaging.Spec.Sha = sha
				activeCommitStatusStaging.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				err = k8sClient.Update(ctx, activeCommitStatusStaging)
				GinkgoLogr.Info("Updated commit status for staging to sha: " + sha)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("By checking that the production pull request has been merged")
			Eventually(func(g Gomega) {
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
		})
	})

	Context("When reconciling a resource with a proposed commit status", func() {
		It("should successfully reconcile the resource", func() {
			// Skip("Skipping test because of flakiness")
			By("Creating the resource")
			name, scmSecret, scmProvider, gitRepo, proposedCommitStatusDevelopment, proposedCommitStatusStaging, promotionStrategy := promotionStrategyResource(ctx, "promotion-strategy-with-proposed-commit-status", "default")
			setupInitialTestGitRepoOnServer(name, name)

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{
					Key: "no-deployments-allowed",
				},
			}
			proposedCommitStatusDevelopment.Spec.Name = "no-deployments-allowed" //nolint:goconst
			proposedCommitStatusDevelopment.Labels = map[string]string{
				promoterv1alpha1.CommitStatusLabel: "no-deployments-allowed",
			}
			proposedCommitStatusDevelopment.Spec.Url = "https://example.com/dev"

			proposedCommitStatusStaging.Spec.Name = "no-deployments-allowed"
			proposedCommitStatusStaging.Labels = map[string]string{
				promoterv1alpha1.CommitStatusLabel: "no-deployments-allowed",
			}

			By("Adding a pending commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			makeChangeAndHydrateRepo(gitPath, name, name, "", "")

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, proposedCommitStatusDevelopment)).To(Succeed())
			Expect(k8sClient.Create(ctx, proposedCommitStatusStaging)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// We should now get PRs created for the ProposedCommits
			// Check that ChangeTransferPolicy are created
			ctpDev := promoterv1alpha1.ChangeTransferPolicy{}
			ctpStaging := promoterv1alpha1.ChangeTransferPolicy{}
			ctpProd := promoterv1alpha1.ChangeTransferPolicy{}

			pullRequestDev := promoterv1alpha1.PullRequest{}
			pullRequestStaging := promoterv1alpha1.PullRequest{}
			pullRequestProd := promoterv1alpha1.PullRequest{}
			By("Checking that all the ChangeTransferPolicies and PRs are created and in their proper state")
			Eventually(func(g Gomega) {
				// Make sure proposed commits are created and the associated PRs
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.PullRequest).To(Not(BeNil()))
				g.Expect(ctpDev.Status.PullRequest.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				g.Expect(ctpDev.Status.PullRequest.ID).To(Not(BeZero()))
				g.Expect(ctpDev.Status.PullRequest.Url).To(ContainSubstring("localhost"))

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.PullRequest).To(Not(BeNil()))
				g.Expect(ctpStaging.Status.PullRequest.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				g.Expect(ctpStaging.Status.PullRequest.ID).To(Not(BeZero()))
				g.Expect(ctpStaging.Status.PullRequest.Url).To(ContainSubstring("localhost"))

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())
				g.Expect(ctpProd.Status.PullRequest).To(Not(BeNil()))
				g.Expect(ctpProd.Status.PullRequest.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				g.Expect(ctpProd.Status.PullRequest.ID).To(Not(BeZero()))
				g.Expect(ctpProd.Status.PullRequest.Url).To(ContainSubstring("localhost"))

				// Dev PR should stay open because it has a proposed commit
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(Succeed())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Succeed())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Updating the commit status for the development environment to success")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      proposedCommitStatusDevelopment.Name,
					Namespace: proposedCommitStatusDevelopment.Namespace,
				}, proposedCommitStatusDevelopment)
				g.Expect(err).To(Succeed())

				_, err = runGitCmd(gitPath, "fetch")
				Expect(err).NotTo(HaveOccurred())
				sha, err := runGitCmd(gitPath, "rev-parse", "origin/"+ctpDev.Spec.ProposedBranch)
				Expect(err).NotTo(HaveOccurred())
				sha = strings.TrimSpace(sha)

				// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Proposed.Hydrated.Sha).To(Equal(sha))

				g.Expect(sha).To(Not(Equal("")))
				proposedCommitStatusDevelopment.Spec.Sha = sha
				proposedCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				err = k8sClient.Update(ctx, proposedCommitStatusDevelopment)
				GinkgoLogr.Info("Updated commit status for development to sha: " + sha)
				g.Expect(err).To(Succeed())

				g.Expect(len(ctpDev.Status.Proposed.CommitStatuses)).To(Not(BeZero()))
				g.Expect(ctpDev.Status.Proposed.CommitStatuses[0].Url).To(Equal(proposedCommitStatusDevelopment.Spec.Url))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("By checking that the development pull request has been merged and that staging, production pull request are still open")
			Eventually(func(g Gomega) {
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.PullRequest).To(BeNil())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.PullRequest).To(Not(BeNil()))

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())
				g.Expect(ctpProd.Status.PullRequest).To(Not(BeNil()))
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      promotionStrategy.Name,
					Namespace: promotionStrategy.Namespace,
				}, promotionStrategy)
				g.Expect(err).To(Succeed())

				g.Expect(promotionStrategy.Status.Environments[0].PullRequest).To(BeNil())
				g.Expect(promotionStrategy.Status.Environments[1].PullRequest).To(Not(BeNil()))
				g.Expect(promotionStrategy.Status.Environments[1].PullRequest.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				g.Expect(promotionStrategy.Status.Environments[1].PullRequest.ID).To(Not(BeZero()))
				g.Expect(promotionStrategy.Status.Environments[1].PullRequest.Url).To(ContainSubstring("localhost"))
				g.Expect(promotionStrategy.Status.Environments[2].PullRequest).To(Not(BeNil()))
				g.Expect(promotionStrategy.Status.Environments[2].PullRequest.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				g.Expect(promotionStrategy.Status.Environments[2].PullRequest.ID).To(Not(BeZero()))
				g.Expect(promotionStrategy.Status.Environments[2].PullRequest.Url).To(ContainSubstring("localhost"))

				g.Expect(len(promotionStrategy.Status.Environments) > 0).To(BeTrue())
				g.Expect(len(promotionStrategy.Status.Environments[0].Proposed.CommitStatuses) > 0).To(BeTrue())
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.CommitStatuses[0].Url).To(Equal(proposedCommitStatusDevelopment.Spec.Url))

				for _, environment := range promotionStrategy.Status.Environments {
					g.Expect(environment.Proposed.Dry.Author).To(Equal("testuser <testmail@test.com>"))
					g.Expect(environment.Proposed.Dry.Subject).To(Equal("added fake manifests commit with timestamp"))
					g.Expect(environment.Proposed.Dry.Body).To(Equal(""))

					g.Expect(environment.Proposed.Hydrated.Author).To(Equal("testuser"))
					g.Expect(environment.Proposed.Hydrated.Subject).To(ContainSubstring("added pending commit from dry sha"))
					g.Expect(environment.Proposed.Hydrated.Body).To(Equal(""))

					g.Expect(environment.Proposed.Dry.References).To(HaveLen(1))
					g.Expect(environment.Proposed.Dry.References[0].Commit.Subject).To(Equal("This is a fix for an upstream issue"))
					g.Expect(environment.Proposed.Dry.References[0].Commit.Body).To(Equal("This is a body of the commit"))
					g.Expect(environment.Proposed.Dry.References[0].Commit.RepoURL).To(Equal("https://github.com/upstream/repo"))
					g.Expect(environment.Proposed.Dry.References[0].Commit.Sha).To(Equal("c4c862564afe56abf8cc8ac683eee3dc8bf96108"))
				}

				g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.Author).To(Equal("testuser <testmail@test.com>"))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.Subject).To(Equal("added fake manifests commit with timestamp"))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.Body).To(Equal(""))

				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Author).To(Equal("testuser"))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Subject).To(ContainSubstring("Promote"))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Body).To(ContainSubstring("This PR is promoting the environment branch"))

				g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.References).To(HaveLen(1))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.References[0].Commit.Subject).To(Equal("This is a fix for an upstream issue"))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.References[0].Commit.Body).To(Equal("This is a body of the commit"))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.References[0].Commit.RepoURL).To(Equal("https://github.com/upstream/repo"))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.References[0].Commit.Sha).To(Equal("c4c862564afe56abf8cc8ac683eee3dc8bf96108"))

				g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Author).To(Equal(""))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Subject).To(Equal(""))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Body).To(Equal(""))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Author).To(Equal("testuser"))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Subject).To(ContainSubstring("initial commit"))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Body).To(Equal(""))
			}, constants.EventuallyTimeout).Should(Succeed())

			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
		})

		It("should successfully reconcile the resource", func() {
			// Skip("Skipping test because of flakiness")
			By("Creating the resource")
			name, scmSecret, scmProvider, gitRepo, proposedCommitStatusDevelopment, proposedCommitStatusStaging, promotionStrategy := promotionStrategyResource(ctx, "promotion-strategy-with-proposed-commit-status", "default")
			setupInitialTestGitRepoWithoutActiveMetadata(name, name)

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			promotionStrategy.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{
					Key: "no-deployments-allowed",
				},
			}
			proposedCommitStatusDevelopment.Spec.Name = "no-deployments-allowed"
			proposedCommitStatusDevelopment.Labels = map[string]string{
				promoterv1alpha1.CommitStatusLabel: "no-deployments-allowed",
			}
			proposedCommitStatusStaging.Spec.Name = "no-deployments-allowed"
			proposedCommitStatusStaging.Labels = map[string]string{
				promoterv1alpha1.CommitStatusLabel: "no-deployments-allowed",
			}

			By("Adding a pending commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			makeChangeAndHydrateRepo(gitPath, name, name, "", "")

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, proposedCommitStatusDevelopment)).To(Succeed())
			Expect(k8sClient.Create(ctx, proposedCommitStatusStaging)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// We should now get PRs created for the ProposedCommits
			// Check that ChangeTransferPolicy are created
			ctpDev := promoterv1alpha1.ChangeTransferPolicy{}
			ctpStaging := promoterv1alpha1.ChangeTransferPolicy{}
			ctpProd := promoterv1alpha1.ChangeTransferPolicy{}

			pullRequestDev := promoterv1alpha1.PullRequest{}
			pullRequestStaging := promoterv1alpha1.PullRequest{}
			pullRequestProd := promoterv1alpha1.PullRequest{}
			By("Checking that all the ChangeTransferPolicies and PRs are created and in their proper state")
			Eventually(func(g Gomega) {
				// Make sure proposed commits are created and the associated PRs
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())

				// Dev PR should stay open because it has a proposed commit
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(Succeed())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Succeed())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Updating the commit status for the development environment to success")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      proposedCommitStatusDevelopment.Name,
					Namespace: proposedCommitStatusDevelopment.Namespace,
				}, proposedCommitStatusDevelopment)
				g.Expect(err).To(Succeed())

				_, err = runGitCmd(gitPath, "fetch")
				Expect(err).NotTo(HaveOccurred())
				sha, err := runGitCmd(gitPath, "rev-parse", "origin/"+ctpDev.Spec.ProposedBranch)
				Expect(err).NotTo(HaveOccurred())
				sha = strings.TrimSpace(sha)

				// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Proposed.Hydrated.Sha).To(Equal(sha))

				g.Expect(sha).To(Not(Equal("")))
				proposedCommitStatusDevelopment.Spec.Sha = sha
				proposedCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				err = k8sClient.Update(ctx, proposedCommitStatusDevelopment)
				GinkgoLogr.Info("Updated commit status for development to sha: " + sha)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("By checking that the development pull request has been merged and that staging, production pull request are still open")
			Eventually(func(g Gomega) {
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Succeed())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
		})
	})

	Context("When reconciling a resource with active commit status using ArgoCDCommitStatus", Label("argocdcommitstatus"), func() {
		const argocdCSLabel = "argocd-health"
		const namespace = "default"

		It("should successfully reconcile the resource", func() {
			// Skip("Skipping test because of flakiness")
			By("Creating the resource")
			plainName := "promotion-strategy-with-active-commit-status-argocdcommitstatus"
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy := promotionStrategyResource(ctx, plainName, "default")
			setupInitialTestGitRepoOnServer(name, name)

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{
					Key: argocdCSLabel,
				},
			}

			argocdCommitStatus := promoterv1alpha1.ArgoCDCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
				Spec: promoterv1alpha1.ArgoCDCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: promotionStrategy.Name,
					},
					ApplicationSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": plainName},
					},
				},
			}

			argoCDAppDev, argoCDAppStaging, argoCDAppProduction := argocdApplications(namespace, plainName)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			Expect(k8sClient.Create(ctx, &argocdCommitStatus)).To(Succeed())
			Expect(k8sClient.Create(ctx, &argoCDAppDev)).To(Succeed())
			Expect(k8sClient.Create(ctx, &argoCDAppStaging)).To(Succeed())
			Expect(k8sClient.Create(ctx, &argoCDAppProduction)).To(Succeed())

			By("Checking that the ArgoCDCommitStatus applicationsSelected field is correct")

			Eventually(func() {
				Expect(k8sClient.Get(ctx, types.NamespacedName{
					Namespace: argocdCommitStatus.Namespace,
					Name:      argocdCommitStatus.Name,
				}, &argocdCommitStatus)).To(Succeed())
				Expect(argocdCommitStatus.Status.ApplicationsSelected).To(HaveLen(3))
				// Check that it's sorted by dev, stage, prod.
				Expect(argocdCommitStatus.Status.ApplicationsSelected[0].Name).To(Equal(argoCDAppDev.Name))
				Expect(argocdCommitStatus.Status.ApplicationsSelected[1].Name).To(Equal(argoCDAppStaging.Name))
				Expect(argocdCommitStatus.Status.ApplicationsSelected[2].Name).To(Equal(argoCDAppProduction.Name))
			})

			By("Checking that the CommitStatus for each environment is created from ArgoCDCommitStatus")

			// Expect(err).To(Succeed())
			for _, environment := range promotionStrategy.Spec.Environments {
				commitStatus := promoterv1alpha1.CommitStatus{}
				commitStatusName := environment.Branch + "/health"
				resourceName := strings.ReplaceAll(commitStatusName, "/", "-") + "-" + hash([]byte(argocdCommitStatus.Name))
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      resourceName,
						Namespace: argoCDAppDev.GetNamespace(),
					}, &commitStatus)
					g.Expect(err).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())
			}

			// We should now get PRs created for the ChangeTransferPolicies
			// Check that ProposedCommit are created
			ctpDev := promoterv1alpha1.ChangeTransferPolicy{}
			ctpStaging := promoterv1alpha1.ChangeTransferPolicy{}
			ctpProd := promoterv1alpha1.ChangeTransferPolicy{}

			By("Checking that all the ChangeTransferPolicies and PRs are created and in their proper state")
			Eventually(func(g Gomega) {
				// Make sure CTP's are created
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Name).To(Equal(utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch))))

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Name).To(Equal(utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch))))

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())
				g.Expect(ctpProd.Name).To(Equal(utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch))))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Adding a pending commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			drySha, _ := makeChangeAndHydrateRepo(gitPath, name, name, "", "")
			simulateWebhook(ctx, k8sClient, &ctpDev)
			simulateWebhook(ctx, k8sClient, &ctpStaging)
			simulateWebhook(ctx, k8sClient, &ctpProd)

			pullRequestDev := promoterv1alpha1.PullRequest{}
			pullRequestStaging := promoterv1alpha1.PullRequest{}
			pullRequestProd := promoterv1alpha1.PullRequest{}
			Eventually(func(g Gomega) {
				// Dev PR should be closed because it is the lowest level environment
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Succeed())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				// Check that the ChangeTransferPolicy for development has an active dry shas that match the expected dry sha meaning the git merge/push succeeded
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpDev.Name,
					Namespace: ctpDev.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Active.Dry.Sha).To(Equal(drySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Updating the development Argo CD application to synced and health we should close staging PR")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())

				argoCDAppDev.Status.Sync.Status = argocd.SyncStatusCodeSynced
				argoCDAppDev.Status.Health.Status = argocd.HealthStatusHealthy
				argoCDAppDev.Status.Sync.Revision = ctpDev.Status.Active.Hydrated.Sha
				argoCDAppDev.Status.Health.LastTransitionTime = &metav1.Time{Time: time.Now().Add(-(6 * time.Second))}
				err = k8sClient.Update(ctx, &argoCDAppDev)
				Expect(err).To(Succeed())

				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(HaveOccurred(), "Staging PR should be closed since the dev app is healthy")
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				// Check that the ChangeTransferPolicy for staging has an active dry shas that match the expected dry sha meaning the git merge/push succeeded
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpStaging.Name,
					Namespace: ctpStaging.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Equal(drySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Updating the staging Argo CD application to synced and health we should close production PR")

			timeDelay := time.Now().Add(10 * time.Second)
			waitedForDelay := false
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())

				argoCDAppStaging.Status.Sync.Status = argocd.SyncStatusCodeSynced
				argoCDAppStaging.Status.Health.Status = argocd.HealthStatusHealthy
				argoCDAppStaging.Status.Sync.Revision = ctpStaging.Status.Active.Hydrated.Sha
				if time.Now().After(timeDelay) {
					argoCDAppStaging.Status.Health.LastTransitionTime = &metav1.Time{Time: time.Now().Add(-(6 * time.Second))}
					waitedForDelay = true
				} else {
					argoCDAppStaging.Status.Health.LastTransitionTime = &metav1.Time{Time: time.Now()}
				}
				err = k8sClient.Update(ctx, &argoCDAppStaging)
				Expect(err).To(Succeed())

				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(waitedForDelay).To(BeTrue())

			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &argocdCommitStatus)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &argoCDAppDev)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &argoCDAppStaging)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &argoCDAppProduction)).To(Succeed())
		})

		It("should successfully reconcile the resource across clusters", Label("multicluster"), func() {
			By("Creating the resource")
			plainName := "mc-promo-strategy-with-active-commit-status-argocdcommitstatus"
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy := promotionStrategyResource(ctx, plainName, "default")
			setupInitialTestGitRepoOnServer(name, name)

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{
					Key: argocdCSLabel,
				},
			}

			testArgoCDCommitStatus := promoterv1alpha1.ArgoCDCommitStatus{}
			//nolint:musttag // Not bothering adding yaml tags since it's just for a test.
			err := yaml.Unmarshal([]byte(testArgoCDCommitStatusYAML), &testArgoCDCommitStatus)
			Expect(err).To(Succeed())

			argocdCommitStatus := promoterv1alpha1.ArgoCDCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
				Spec: promoterv1alpha1.ArgoCDCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: promotionStrategy.Name,
					},
					ApplicationSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": plainName},
					},
					URL: promoterv1alpha1.URLConfig{
						Template: testArgoCDCommitStatus.Spec.URL.Template,
					},
				},
			}

			argoCDAppDev, argoCDAppStaging, argoCDAppProduction := argocdApplications(namespace, plainName)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			Expect(k8sClient.Create(ctx, &argocdCommitStatus)).To(Succeed())
			Expect(k8sClientDev.Create(ctx, &argoCDAppDev)).To(Succeed())
			Expect(k8sClientStaging.Create(ctx, &argoCDAppStaging)).To(Succeed())
			Expect(k8sClient.Create(ctx, &argoCDAppProduction)).To(Succeed())

			By("Checking that the CommitStatus for each environment is created from ArgoCDCommitStatus")

			// Expect(err).To(Succeed())
			for _, environment := range promotionStrategy.Spec.Environments {
				commitStatus := promoterv1alpha1.CommitStatus{}
				commitStatusName := environment.Branch + "/health"
				resourceName := strings.ReplaceAll(commitStatusName, "/", "-") + "-" + hash([]byte(argocdCommitStatus.Name))
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      resourceName,
						Namespace: argoCDAppDev.GetNamespace(),
					}, &commitStatus)
					g.Expect(err).To(Succeed())
					switch environment.Branch {
					case "environment/development":
						g.Expect(commitStatus.Spec.Url).To(Equal("https://dev.argocd.local/applications?labels=app%3Dmc-promo-strategy-with-active-commit-status-argocdcommitstatus%2C"))
					case "environment/staging":
						g.Expect(commitStatus.Spec.Url).To(Equal("https://staging.argocd.local/applications?labels=app%3Dmc-promo-strategy-with-active-commit-status-argocdcommitstatus%2C"))
					case "environment/production":
						g.Expect(commitStatus.Spec.Url).To(Equal("https://prod.argocd.local/applications?labels=app%3Dmc-promo-strategy-with-active-commit-status-argocdcommitstatus%2C"))
					}
				}, constants.EventuallyTimeout).Should(Succeed())
			}

			// We should now get PRs created for the ChangeTransferPolicies
			// Check that ProposedCommit are created
			ctpDev := promoterv1alpha1.ChangeTransferPolicy{}
			ctpStaging := promoterv1alpha1.ChangeTransferPolicy{}
			ctpProd := promoterv1alpha1.ChangeTransferPolicy{}

			By("Checking that all the ChangeTransferPolicies and PRs are created and in their proper state")
			Eventually(func(g Gomega) {
				// Make sure CTP's are created
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Name).To(Equal(utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch))))

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Name).To(Equal(utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch))))

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())
				g.Expect(ctpProd.Name).To(Equal(utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch))))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Adding a pending commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			drySha, _ := makeChangeAndHydrateRepo(gitPath, name, name, "", "")
			simulateWebhook(ctx, k8sClient, &ctpDev)
			simulateWebhook(ctx, k8sClient, &ctpStaging)
			simulateWebhook(ctx, k8sClient, &ctpProd)

			pullRequestDev := promoterv1alpha1.PullRequest{}
			pullRequestStaging := promoterv1alpha1.PullRequest{}
			pullRequestProd := promoterv1alpha1.PullRequest{}
			Eventually(func(g Gomega) {
				// Dev PR should be closed because it is the lowest level environment
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Succeed())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				// Check that the ChangeTransferPolicy for development has an active dry shas that match the expected dry sha meaning the git merge/push succeeded
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpDev.Name,
					Namespace: ctpDev.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Active.Dry.Sha).To(Equal(drySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Updating the development Argo CD application to synced and health we should close staging PR")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())

				argoCDAppDev.Status.Sync.Status = argocd.SyncStatusCodeSynced
				argoCDAppDev.Status.Health.Status = argocd.HealthStatusHealthy
				argoCDAppDev.Status.Sync.Revision = ctpDev.Status.Active.Hydrated.Sha
				argoCDAppDev.Status.Health.LastTransitionTime = &metav1.Time{Time: time.Now().Add(-(6 * time.Second))}
				err = k8sClientDev.Update(ctx, &argoCDAppDev)
				Expect(err).To(Succeed())

				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				// Check that the ChangeTransferPolicy for staging has an active dry shas that match the expected dry sha meaning the git merge/push succeeded
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpStaging.Name,
					Namespace: ctpStaging.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Equal(drySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Updating the staging Argo CD application to synced and health we should close production PR")

			timeDelay := time.Now().Add(10 * time.Second)
			waitedForDelay := false
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())

				argoCDAppStaging.Status.Sync.Status = argocd.SyncStatusCodeSynced
				argoCDAppStaging.Status.Health.Status = argocd.HealthStatusHealthy
				argoCDAppStaging.Status.Sync.Revision = ctpStaging.Status.Active.Hydrated.Sha
				if time.Now().After(timeDelay) {
					argoCDAppStaging.Status.Health.LastTransitionTime = &metav1.Time{Time: time.Now().Add(-(6 * time.Second))}
					waitedForDelay = true
				} else {
					argoCDAppStaging.Status.Health.LastTransitionTime = &metav1.Time{Time: time.Now()}
				}
				err = k8sClientStaging.Update(ctx, &argoCDAppStaging)
				Expect(err).To(Succeed())

				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(waitedForDelay).To(BeTrue())

			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &argocdCommitStatus)).To(Succeed())
			Expect(k8sClientDev.Delete(ctx, &argoCDAppDev)).To(Succeed())
			Expect(k8sClientStaging.Delete(ctx, &argoCDAppStaging)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &argoCDAppProduction)).To(Succeed())
		})
	})
})

func promotionStrategyResource(ctx context.Context, name, namespace string) (string, *v1.Secret, *promoterv1alpha1.ScmProvider, *promoterv1alpha1.GitRepository, *promoterv1alpha1.CommitStatus, *promoterv1alpha1.CommitStatus, *promoterv1alpha1.PromotionStrategy) { //nolint:unparam
	name = name + "-" + utils.KubeSafeUniqueName(ctx, randomString(15))

	scmSecret := &v1.Secret{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: nil,
	}

	scmProvider := &promoterv1alpha1.ScmProvider{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.ScmProviderSpec{
			SecretRef: &v1.LocalObjectReference{Name: name},
			Fake:      &promoterv1alpha1.Fake{},
		},
		Status: promoterv1alpha1.ScmProviderStatus{},
	}

	gitRepo := &promoterv1alpha1.GitRepository{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.GitRepositorySpec{
			Fake: &promoterv1alpha1.FakeRepo{
				Owner: name,
				Name:  name,
			},
			ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
				Kind: promoterv1alpha1.ScmProviderKind,
				Name: name,
			},
		},
	}

	commitStatusDevelopment := &promoterv1alpha1.CommitStatus{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "development-" + name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: name,
			},
			Sha:         "",
			Name:        "",
			Description: "",
			Phase:       promoterv1alpha1.CommitPhasePending,
			Url:         "",
		},
	}

	commitStatusStaging := &promoterv1alpha1.CommitStatus{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "staging-" + name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: name,
			},
			Sha:         "",
			Name:        "",
			Description: "",
			Phase:       promoterv1alpha1.CommitPhasePending,
			Url:         "",
		},
	}

	promotionStrategy := &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.PromotionStrategySpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: name,
			},
			Environments: []promoterv1alpha1.Environment{
				{Branch: "environment/development"},
				{Branch: "environment/staging"},
				{Branch: "environment/production"},
			},
		},
	}

	return name, scmSecret, scmProvider, gitRepo, commitStatusDevelopment, commitStatusStaging, promotionStrategy
}

func argocdApplications(namespace string, name string) (argocd.Application, argocd.Application, argocd.Application) {
	environments := []string{"development", "staging", "production"}
	apps := make([]argocd.Application, len(environments))
	for i, environment := range environments {
		envAppName := name + "-" + environment
		envApp := argocd.Application{
			TypeMeta: metav1.TypeMeta{
				Kind:       "Application",
				APIVersion: "argoproj.io/v1alpha1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      envAppName,
				Namespace: namespace,
				Labels: map[string]string{
					"app": name,
				},
			},
			Spec: argocd.ApplicationSpec{
				SourceHydrator: &argocd.SourceHydrator{
					DrySource: argocd.DrySource{
						RepoURL: fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPort, name, name),
					},
					SyncSource: argocd.SyncSource{
						TargetBranch: "environment/" + environment,
					},
				},
			},
			Status: argocd.ApplicationStatus{
				Sync: argocd.SyncStatus{
					Status: argocd.SyncStatusCodeOutOfSync,
				},
				Health: argocd.HealthStatus{
					Status:             argocd.HealthStatusHealthy,
					LastTransitionTime: &metav1.Time{Time: time.Now()},
				},
			},
		}
		apps[i] = envApp
	}
	return apps[0], apps[1], apps[2]
}
