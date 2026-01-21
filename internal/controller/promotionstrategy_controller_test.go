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
	"sync"
	"time"

	"github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/utils/ptr"

	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

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

		Context("When git repo is not initialized", func() {
			var name string
			var promotionStrategy *promoterv1alpha1.PromotionStrategy

			BeforeEach(func() {
				By("Creating the resources")
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				var gitRepo *promoterv1alpha1.GitRepository
				name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "promotion-strategy-no-commit-status", "default")

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			})

			AfterEach(func() {
				By("Cleaning up resources")
				_ = k8sClient.Delete(ctx, promotionStrategy)
			})

			It("should fail if we don't set up the git repo", func() {
				typeNamespacedName := types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				By("Checking that the PromotionStrategy is in an error state due to missing git repo")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, promotionStrategy)
					g.Expect(err).To(Succeed())
					cond := meta.FindStatusCondition(promotionStrategy.Status.Conditions, string(promoterConditions.Ready))
					g.Expect(cond).ToNot(BeNil())
					g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
					g.Expect(cond.Reason).To(Equal(string(promoterConditions.ChangeTransferPolicyNotReady)))
					g.Expect(cond.Message).To(ContainSubstring("failed to get SHAs"))
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		Context("When git repo is initialized", func() {
			var name string
			var gitRepo *promoterv1alpha1.GitRepository
			var promotionStrategy *promoterv1alpha1.PromotionStrategy
			var typeNamespacedName types.NamespacedName
			var ctpDev, ctpStaging, ctpProd promoterv1alpha1.ChangeTransferPolicy
			var pullRequestDev, pullRequestStaging, pullRequestProd promoterv1alpha1.PullRequest

			BeforeEach(func() {
				By("Creating the resources")
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "promotion-strategy-no-commit-status", "default")
				setupInitialTestGitRepoOnServer(ctx, name, name)

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}
				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

				// Initialize empty structs for use in tests
				ctpDev = promoterv1alpha1.ChangeTransferPolicy{}
				ctpStaging = promoterv1alpha1.ChangeTransferPolicy{}
				ctpProd = promoterv1alpha1.ChangeTransferPolicy{}
				pullRequestDev = promoterv1alpha1.PullRequest{}
				pullRequestStaging = promoterv1alpha1.PullRequest{}
				pullRequestProd = promoterv1alpha1.PullRequest{}
			})

			AfterEach(func() {
				By("Cleaning up resources")
				_ = k8sClient.Delete(ctx, promotionStrategy)
			})

			It("should successfully reconcile the resource", func() {
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
					g.Expect(ctpDev.Spec.ActiveBranch).To(Equal(testBranchDevelopment))
					g.Expect(ctpDev.Spec.ProposedBranch).To(Equal(testBranchDevelopmentNext))
					g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.Sha).To(Equal(ctpDev.Status.Active.Dry.Sha))
					g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).To(Equal(ctpDev.Status.Active.Hydrated.Sha))
					g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[0].Active.CommitStatuses)).To(BeTrue())
					g.Expect(promotionStrategy.Status.Environments[0].Proposed.Dry.Sha).To(Equal(ctpDev.Status.Proposed.Dry.Sha))
					g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).To(Equal(ctpDev.Status.Proposed.Hydrated.Sha))
					// Success due to PromotionStrategy not having any CommitStatuses configured
					g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[0].Proposed.CommitStatuses)).To(BeTrue())

					// By("Checking that the PromotionStrategy for staging environment has the correct sha values from the ChangeTransferPolicy")
					g.Expect(ctpStaging.Spec.ActiveBranch).To(Equal(testBranchStaging))
					g.Expect(ctpStaging.Spec.ProposedBranch).To(Equal("environment/staging-next"))
					g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Sha).To(Equal(ctpStaging.Status.Active.Dry.Sha))
					g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Sha).To(Equal(ctpStaging.Status.Active.Hydrated.Sha))
					g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[1].Active.CommitStatuses)).To(BeTrue())
					g.Expect(promotionStrategy.Status.Environments[1].Proposed.Dry.Sha).To(Equal(ctpStaging.Status.Proposed.Dry.Sha))
					g.Expect(promotionStrategy.Status.Environments[1].Proposed.Hydrated.Sha).To(Equal(ctpStaging.Status.Proposed.Hydrated.Sha))
					// Success due to PromotionStrategy not having any CommitStatuses configured
					g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[1].Proposed.CommitStatuses)).To(BeTrue())

					// By("Checking that the PromotionStrategy for production environment has the correct sha values from the ChangeTransferPolicy")
					g.Expect(ctpProd.Spec.ActiveBranch).To(Equal(testBranchProduction))
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

				By("Checking that the CTPs have reconciled and picked up the new commits")
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

					g.Expect(ctpDev.Status.Active.Hydrated.Subject).To(ContainSubstring("Promote"))
					g.Expect(ctpDev.Status.Active.Hydrated.Body).To(ContainSubstring("This PR is promoting the environment"))
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

				By("Checking that the pull request for the development, staging, and production environments are closed and their statuses preserved")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
						Namespace: typeNamespacedName.Namespace,
					}, &ctpDev)
					g.Expect(err).To(Succeed())
					g.Expect(ctpDev.Status.PullRequest).ToNot(BeNil(), "CTP should preserve PR status after PR is merged/deleted")
					g.Expect(ctpDev.Status.PullRequest.State).To(Equal(promoterv1alpha1.PullRequestMerged), "PR state should be 'merged'")
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
						Namespace: typeNamespacedName.Namespace,
					}, &ctpStaging)
					g.Expect(err).To(Succeed())
					g.Expect(ctpStaging.Status.PullRequest).ToNot(BeNil(), "CTP should preserve PR status after PR is merged/deleted")
					g.Expect(ctpStaging.Status.PullRequest.State).To(Equal(promoterv1alpha1.PullRequestMerged), "PR state should be 'merged'")
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
						Namespace: typeNamespacedName.Namespace,
					}, &ctpProd)
					g.Expect(err).To(Succeed())
					g.Expect(ctpProd.Status.PullRequest).ToNot(BeNil(), "CTP should preserve PR status after PR is merged/deleted")
					g.Expect(ctpProd.Status.PullRequest.State).To(Equal(promoterv1alpha1.PullRequestMerged), "PR state should be 'merged'")
				}, constants.EventuallyTimeout).Should(Succeed())

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      promotionStrategy.Name,
						Namespace: promotionStrategy.Namespace,
					}, promotionStrategy)
					g.Expect(err).To(Succeed())
					g.Expect(promotionStrategy.Status.Environments[0].PullRequest).ToNot(BeNil(), "PromotionStrategy should preserve PR status")
					g.Expect(promotionStrategy.Status.Environments[0].PullRequest.State).To(Equal(promoterv1alpha1.PullRequestMerged), "PR state should be merged")
					g.Expect(promotionStrategy.Status.Environments[1].PullRequest).ToNot(BeNil(), "PromotionStrategy should preserve PR status")
					g.Expect(promotionStrategy.Status.Environments[1].PullRequest.State).To(Equal(promoterv1alpha1.PullRequestMerged), "PR state should be merged")
					g.Expect(promotionStrategy.Status.Environments[2].PullRequest).ToNot(BeNil(), "PromotionStrategy should preserve PR status")
					g.Expect(promotionStrategy.Status.Environments[2].PullRequest.State).To(Equal(promoterv1alpha1.PullRequestMerged), "PR state should be merged")
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		Context("When using ClusterScmProvider", func() {
			var name string
			var gitRepo *promoterv1alpha1.GitRepository
			var promotionStrategy *promoterv1alpha1.PromotionStrategy
			var clusterScmProvider *promoterv1alpha1.ClusterScmProvider
			var typeNamespacedName types.NamespacedName
			var ctpDev, ctpStaging, ctpProd promoterv1alpha1.ChangeTransferPolicy
			var pullRequestDev, pullRequestStaging, pullRequestProd promoterv1alpha1.PullRequest

			BeforeEach(func() {
				By("Creating the resources")
				var scmSecret *v1.Secret
				name, scmSecret, _, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "promotion-strategy-no-commit-status", "default")
				setupInitialTestGitRepoOnServer(ctx, name, name)

				clusterScmProvider = &promoterv1alpha1.ClusterScmProvider{
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

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}
				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, clusterScmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

				// Initialize empty structs for use in tests
				ctpDev = promoterv1alpha1.ChangeTransferPolicy{}
				ctpStaging = promoterv1alpha1.ChangeTransferPolicy{}
				ctpProd = promoterv1alpha1.ChangeTransferPolicy{}
				pullRequestDev = promoterv1alpha1.PullRequest{}
				pullRequestStaging = promoterv1alpha1.PullRequest{}
				pullRequestProd = promoterv1alpha1.PullRequest{}
			})

			AfterEach(func() {
				By("Cleaning up resources")
				_ = k8sClient.Delete(ctx, promotionStrategy)
			})

			It("should successfully reconcile the resource using a ClusterScmProvider", func() {
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
					g.Expect(ctpDev.Spec.ActiveBranch).To(Equal(testBranchDevelopment))
					g.Expect(ctpDev.Spec.ProposedBranch).To(Equal(testBranchDevelopmentNext))
					g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.Sha).To(Equal(ctpDev.Status.Active.Dry.Sha))
					g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).To(Equal(ctpDev.Status.Active.Hydrated.Sha))
					g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[0].Active.CommitStatuses)).To(BeTrue())
					g.Expect(promotionStrategy.Status.Environments[0].Proposed.Dry.Sha).To(Equal(ctpDev.Status.Proposed.Dry.Sha))
					g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).To(Equal(ctpDev.Status.Proposed.Hydrated.Sha))
					// Success due to PromotionStrategy not having any CommitStatuses configured
					g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[0].Proposed.CommitStatuses)).To(BeTrue())

					// By("Checking that the PromotionStrategy for staging environment has the correct sha values from the ChangeTransferPolicy")
					g.Expect(ctpStaging.Spec.ActiveBranch).To(Equal(testBranchStaging))
					g.Expect(ctpStaging.Spec.ProposedBranch).To(Equal("environment/staging-next"))
					g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Sha).To(Equal(ctpStaging.Status.Active.Dry.Sha))
					g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Sha).To(Equal(ctpStaging.Status.Active.Hydrated.Sha))
					g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[1].Active.CommitStatuses)).To(BeTrue())
					g.Expect(promotionStrategy.Status.Environments[1].Proposed.Dry.Sha).To(Equal(ctpStaging.Status.Proposed.Dry.Sha))
					g.Expect(promotionStrategy.Status.Environments[1].Proposed.Hydrated.Sha).To(Equal(ctpStaging.Status.Proposed.Hydrated.Sha))
					// Success due to PromotionStrategy not having any CommitStatuses configured
					g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[1].Proposed.CommitStatuses)).To(BeTrue())

					// By("Checking that the PromotionStrategy for production environment has the correct sha values from the ChangeTransferPolicy")
					g.Expect(ctpProd.Spec.ActiveBranch).To(Equal(testBranchProduction))
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

				By("Checking that the pull requests were created and merged (they may auto-merge too fast to observe in open state)")
				// With automatic webhooks, PRs may be created and auto-merged before we can observe them in the open state.
				// Instead, we verify the final outcome: promotions completed and PRs are deleted.

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
			})
		})

		// Happens when there is no hydrator.metadata on the active branch such as when the branch was just initialized and is empty
		Context("When active branch has no hydrator metadata", func() {
			var name string
			var gitRepo *promoterv1alpha1.GitRepository
			var promotionStrategy *promoterv1alpha1.PromotionStrategy
			var typeNamespacedName types.NamespacedName
			var ctpDev, ctpStaging, ctpProd promoterv1alpha1.ChangeTransferPolicy
			var pullRequestDev, pullRequestStaging, pullRequestProd promoterv1alpha1.PullRequest

			BeforeEach(func() {
				By("Creating the resources")
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "promotion-strategy-no-commit-status", "default")
				setupInitialTestGitRepoWithoutActiveMetadata(name, name)

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}
				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

				// Initialize empty structs for use in tests
				ctpDev = promoterv1alpha1.ChangeTransferPolicy{}
				ctpStaging = promoterv1alpha1.ChangeTransferPolicy{}
				ctpProd = promoterv1alpha1.ChangeTransferPolicy{}
				pullRequestDev = promoterv1alpha1.PullRequest{}
				pullRequestStaging = promoterv1alpha1.PullRequest{}
				pullRequestProd = promoterv1alpha1.PullRequest{}
			})

			AfterEach(func() {
				By("Cleaning up resources")
				_ = k8sClient.Delete(ctx, promotionStrategy)
			})

			It("should successfully reconcile the resource with no active dry sha", func() {
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
					g.Expect(ctpDev.Spec.ActiveBranch).To(Equal(testBranchDevelopment))
					g.Expect(ctpDev.Spec.ProposedBranch).To(Equal(testBranchDevelopmentNext))
					g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.Sha).To(Equal(promotionStrategy.Status.Environments[0].Proposed.Dry.Sha))
					g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).To(Equal(ctpDev.Status.Active.Hydrated.Sha))
					g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[0].Active.CommitStatuses)).To(BeTrue())
					g.Expect(promotionStrategy.Status.Environments[0].Proposed.Dry.Sha).To(Equal(ctpDev.Status.Proposed.Dry.Sha))
					g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).To(Equal(ctpDev.Status.Proposed.Hydrated.Sha))
					// Success due to PromotionStrategy not having any CommitStatuses configured
					g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[0].Proposed.CommitStatuses)).To(BeTrue())

					// By("Checking that the PromotionStrategy for staging environment has the correct sha values from the ChangeTransferPolicy")
					g.Expect(ctpStaging.Spec.ActiveBranch).To(Equal(testBranchStaging))
					g.Expect(ctpStaging.Spec.ProposedBranch).To(Equal("environment/staging-next"))
					g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Sha).To(Equal(promotionStrategy.Status.Environments[1].Proposed.Dry.Sha))
					g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Sha).To(Equal(ctpStaging.Status.Active.Hydrated.Sha))
					g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[1].Active.CommitStatuses)).To(BeTrue())
					g.Expect(promotionStrategy.Status.Environments[1].Proposed.Dry.Sha).To(Equal(ctpStaging.Status.Proposed.Dry.Sha))
					g.Expect(promotionStrategy.Status.Environments[1].Proposed.Hydrated.Sha).To(Equal(ctpStaging.Status.Proposed.Hydrated.Sha))
					// Success due to PromotionStrategy not having any CommitStatuses configured
					g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[1].Proposed.CommitStatuses)).To(BeTrue())

					// By("Checking that the PromotionStrategy for production environment has the correct sha values from the ChangeTransferPolicy")
					g.Expect(ctpProd.Spec.ActiveBranch).To(Equal(testBranchProduction))
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

				By("Checking that the pull request for the development, staging, and production environments are closed")
				// With automatic webhooks, PRs may be created and auto-merged too fast to observe in open state
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

				By("Checking that the PromotionStrategy's Ready condition is True")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      promotionStrategy.Name,
						Namespace: promotionStrategy.Namespace,
					}, promotionStrategy)
					g.Expect(err).To(Succeed())
					readyCondition := meta.FindStatusCondition(promotionStrategy.Status.Conditions, string(promoterConditions.Ready))
					g.Expect(readyCondition).ToNot(BeNil())
					g.Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		// This test validates the conflict resolution flow:
		// 1. Creates initial changes via makeChangeAndHydrateRepo (adds manifests-fake.yaml with {"time": ...} to proposed branches)
		// 2. Creates a conflicting commit on the development active branch (adds manifests-fake.yaml with {"conflict": ...})
		// 3. Verifies that the CTP detects the merge conflict between proposed and active branches
		// 4. Verifies that the CTP auto-resolves the conflict using 'git merge -s ours' strategy (keeping active branch content)
		// 5. Verifies that PRs are created for environments with auto-merge disabled
		// 6. Re-enables auto-merge and verifies all PRs get merged and deleted
		Context("When git conflict occurs", func() {
			var name string
			var gitRepo *promoterv1alpha1.GitRepository
			var promotionStrategy *promoterv1alpha1.PromotionStrategy
			var typeNamespacedName types.NamespacedName
			var ctpDev, ctpStaging, ctpProd promoterv1alpha1.ChangeTransferPolicy
			var pullRequestDev, pullRequestStaging, pullRequestProd promoterv1alpha1.PullRequest

			BeforeEach(func() {
				By("Creating the resources")
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "promotion-strategy-no-commit-status-conflict", "default")

				// Disable auto-merge for all environments to prevent them from auto-merging
				// while we're asserting on their PR states. We'll re-enable it after assertions.
				promotionStrategy.Spec.Environments[0].AutoMerge = ptr.To(false) // development
				promotionStrategy.Spec.Environments[1].AutoMerge = ptr.To(false) // staging
				promotionStrategy.Spec.Environments[2].AutoMerge = ptr.To(false) // production

				setupInitialTestGitRepoOnServer(ctx, name, name)

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}
				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

				// Initialize empty structs for use in tests
				ctpDev = promoterv1alpha1.ChangeTransferPolicy{}
				ctpStaging = promoterv1alpha1.ChangeTransferPolicy{}
				ctpProd = promoterv1alpha1.ChangeTransferPolicy{}
				pullRequestDev = promoterv1alpha1.PullRequest{}
				pullRequestStaging = promoterv1alpha1.PullRequest{}
				pullRequestProd = promoterv1alpha1.PullRequest{}
			})

			AfterEach(func() {
				By("Cleaning up resources")
				_ = k8sClient.Delete(ctx, promotionStrategy)
			})

			It("should successfully reconcile the resource with a git conflict", func() {
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
					g.Expect(ctpDev.Spec.ActiveBranch).To(Equal(testBranchDevelopment))
					g.Expect(ctpDev.Spec.ProposedBranch).To(Equal(testBranchDevelopmentNext))
					g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.Sha).To(Equal(ctpDev.Status.Active.Dry.Sha))
					g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).To(Equal(ctpDev.Status.Active.Hydrated.Sha))
					g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[0].Active.CommitStatuses)).To(BeTrue())
					g.Expect(promotionStrategy.Status.Environments[0].Proposed.Dry.Sha).To(Equal(ctpDev.Status.Proposed.Dry.Sha))
					g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).To(Equal(ctpDev.Status.Proposed.Hydrated.Sha))
					// Success due to PromotionStrategy not having any CommitStatuses configured
					g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[0].Proposed.CommitStatuses)).To(BeTrue())

					// By("Checking that the PromotionStrategy for staging environment has the correct sha values from the ChangeTransferPolicy")
					g.Expect(ctpStaging.Spec.ActiveBranch).To(Equal(testBranchStaging))
					g.Expect(ctpStaging.Spec.ProposedBranch).To(Equal("environment/staging-next"))
					g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Sha).To(Equal(ctpStaging.Status.Active.Dry.Sha))
					g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Sha).To(Equal(ctpStaging.Status.Active.Hydrated.Sha))
					g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[1].Active.CommitStatuses)).To(BeTrue())
					g.Expect(promotionStrategy.Status.Environments[1].Proposed.Dry.Sha).To(Equal(ctpStaging.Status.Proposed.Dry.Sha))
					g.Expect(promotionStrategy.Status.Environments[1].Proposed.Hydrated.Sha).To(Equal(ctpStaging.Status.Proposed.Hydrated.Sha))
					// Success due to PromotionStrategy not having any CommitStatuses configured
					g.Expect(utils.AreCommitStatusesPassing(promotionStrategy.Status.Environments[1].Proposed.CommitStatuses)).To(BeTrue())

					// By("Checking that the PromotionStrategy for production environment has the correct sha values from the ChangeTransferPolicy")
					g.Expect(ctpProd.Spec.ActiveBranch).To(Equal(testBranchProduction))
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

				_, err = runGitCmd(ctx, gitPath, "checkout", ctpDev.Spec.ActiveBranch)
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "pull", "origin", ctpDev.Spec.ActiveBranch)
				Expect(err).NotTo(HaveOccurred())
				f, err := os.Create(path.Join(gitPath, "manifests-fake.yaml"))
				Expect(err).NotTo(HaveOccurred())
				str := fmt.Sprintf("{\"conflict\": \"%s\"}", time.Now().Format(time.RFC3339Nano))
				_, err = f.WriteString(str)
				Expect(err).NotTo(HaveOccurred())
				err = f.Close()
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "add", "manifests-fake.yaml")
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "commit", "-m", "added fake manifests commit with timestamp")
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "push", "-u", "origin", ctpDev.Spec.ActiveBranch)
				Expect(err).NotTo(HaveOccurred())

				By("Verifying the conflicting commit was pushed to the active branch")
				// Get the SHA of the conflicting commit we just pushed
				conflictingSha, err := runGitCmd(ctx, gitPath, "rev-parse", "HEAD")
				Expect(err).NotTo(HaveOccurred())
				conflictingSha = strings.TrimSpace(conflictingSha)
				Expect(conflictingSha).NotTo(BeEmpty())

				By("Checking that there is no previous-environment commit status created, since no active checks are configured")
				// List all CTPs owned by this test's PromotionStrategy
				ctpList := promoterv1alpha1.ChangeTransferPolicyList{}
				err = k8sClient.List(ctx, &ctpList, client.InNamespace(typeNamespacedName.Namespace))
				Expect(err).NotTo(HaveOccurred())

				// Build a set of CTP UIDs that belong to this test's PromotionStrategy, so that we can continue to run tests
				// in parallel without interfering with each other.
				ctpUIDs := make(map[string]bool)
				for _, ctp := range ctpList.Items {
					for _, ownerRef := range ctp.OwnerReferences {
						if ownerRef.Name == promotionStrategy.Name && ownerRef.UID == promotionStrategy.UID {
							ctpUIDs[string(ctp.UID)] = true
							break
						}
					}
				}

				// List all previous-environment commit statuses and filter by ownership
				csList := promoterv1alpha1.CommitStatusList{}
				err = k8sClient.List(ctx, &csList, client.MatchingLabels{
					promoterv1alpha1.CommitStatusLabel: promoterv1alpha1.PreviousEnvironmentCommitStatusKey,
				})
				Expect(err).NotTo(HaveOccurred())

				// Filter to only include commit statuses owned by this test's CTPs
				testCommitStatuses := 0
				for _, cs := range csList.Items {
					for _, ownerRef := range cs.OwnerReferences {
						if ctpUIDs[string(ownerRef.UID)] {
							testCommitStatuses++
							break
						}
					}
				}
				Expect(testCommitStatuses).To(Equal(0))

				By("Waiting for CTPs to reconcile after the conflict and verifying conflict resolution")
				Eventually(func(g Gomega) {
					// Get the latest CTP states
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

					// Dev should have reconciled with conflict resolution
					// The conflict resolution:
					// 1. Checks out the PROPOSED branch (environment/development-next)
					// 2. Merges the ACTIVE branch into it using 'git merge -s ours'
					// 3. Pushes the PROPOSED branch with the merge commit
					// The 'ours' strategy keeps the PROPOSED branch content and discards conflicting changes from active.
					// This creates a merge commit on the proposed branch that acknowledges both histories.
					g.Expect(ctpDev.Status.Active.Dry.Sha).To(Not(BeEmpty()))
					g.Expect(ctpDev.Status.Active.Dry.Sha).To(Not(Equal(ctpDev.Status.Proposed.Dry.Sha)), "Dev active should have been updated by conflict resolution merge")

					// Verify the proposed branch has a new merge commit from conflict resolution
					// The merge commit will be on the proposed branch (development-next), not the active branch
					g.Expect(ctpDev.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))

					// Verify reconciliation completed (both active and proposed have valid SHAs)
					g.Expect(ctpDev.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

					// Staging should have different proposed vs active (ready for PR)
					g.Expect(ctpStaging.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
					g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Not(BeEmpty()))
					g.Expect(ctpStaging.Status.Proposed.Dry.Sha).To(Not(Equal(ctpStaging.Status.Active.Dry.Sha)), "Staging should have different commits")

					// Production should have different proposed vs active (ready for PR)
					g.Expect(ctpProd.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
					g.Expect(ctpProd.Status.Active.Dry.Sha).To(Not(BeEmpty()))
					g.Expect(ctpProd.Status.Proposed.Dry.Sha).To(Not(Equal(ctpProd.Status.Active.Dry.Sha)), "Production should have different commits")

					// Now check PR states based on CTP reconciliation state
					// Dev PR should exist (auto-merge is disabled)
					prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpDev.Spec.ProposedBranch, ctpDev.Spec.ActiveBranch))
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      prName,
						Namespace: typeNamespacedName.Namespace,
					}, &pullRequestDev)
					g.Expect(err).To(Succeed(), "Dev PR should exist before auto-merge is re-enabled")

					// Staging PR should exist (yaml changes require PR)
					prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      prName,
						Namespace: typeNamespacedName.Namespace,
					}, &pullRequestStaging)
					g.Expect(err).To(Succeed(), "Staging PR should exist")

					// Production PR should exist (yaml changes require PR)
					prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      prName,
						Namespace: typeNamespacedName.Namespace,
					}, &pullRequestProd)
					g.Expect(err).To(Succeed(), "Production PR should exist")
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Re-enabling auto-merge for all environments to allow PRs to merge")
				// Now that we've asserted on PR states, re-enable auto-merge so PRs can merge
				Eventually(func(g Gomega) {
					var ps promoterv1alpha1.PromotionStrategy
					err := k8sClient.Get(ctx, typeNamespacedName, &ps)
					g.Expect(err).To(Succeed())

					ps.Spec.Environments[0].AutoMerge = ptr.To(true) // development
					ps.Spec.Environments[1].AutoMerge = ptr.To(true) // staging
					ps.Spec.Environments[2].AutoMerge = ptr.To(true) // production

					err = k8sClient.Update(ctx, &ps)
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
			})
		})
	})

	Context("When reconciling a resource with active commit statuses", func() {
		ctx := context.Background()

		Context("When git repo is initialized with active commit statuses", func() {
			var name string
			var gitRepo *promoterv1alpha1.GitRepository
			var promotionStrategy *promoterv1alpha1.PromotionStrategy
			var activeCommitStatusDevelopment, activeCommitStatusStaging *promoterv1alpha1.CommitStatus
			var typeNamespacedName types.NamespacedName
			var ctpDev, ctpStaging, ctpProd promoterv1alpha1.ChangeTransferPolicy
			var pullRequestDev, pullRequestStaging, pullRequestProd promoterv1alpha1.PullRequest

			BeforeEach(func() {
				By("Creating the resource")
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				name, scmSecret, scmProvider, gitRepo, activeCommitStatusDevelopment, activeCommitStatusStaging, promotionStrategy = promotionStrategyResource(ctx, "promotion-strategy-with-active-commit-status", "default")
				setupInitialTestGitRepoOnServer(ctx, name, name)

				typeNamespacedName = types.NamespacedName{
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
				Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

				// Initialize empty structs for use in tests
				ctpDev = promoterv1alpha1.ChangeTransferPolicy{}
				ctpStaging = promoterv1alpha1.ChangeTransferPolicy{}
				ctpProd = promoterv1alpha1.ChangeTransferPolicy{}
				pullRequestDev = promoterv1alpha1.PullRequest{}
				pullRequestStaging = promoterv1alpha1.PullRequest{}
				pullRequestProd = promoterv1alpha1.PullRequest{}
			})

			AfterEach(func() {
				By("Cleaning up resources")
				_ = k8sClient.Delete(ctx, promotionStrategy)
			})

			It("should successfully reconcile the resource", func() {
				// Skip("Skipping test because of flakiness")
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
					_, err = runGitCmd(ctx, gitPath, "fetch")
					Expect(err).NotTo(HaveOccurred())
					sha, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+ctpDev.Spec.ActiveBranch)
					Expect(err).NotTo(HaveOccurred())
					sha = strings.TrimSpace(sha)

					g.Expect(sha).To(Not(BeEmpty()))
					_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, activeCommitStatusDevelopment, func() error {
						activeCommitStatusDevelopment.Spec.Sha = sha
						activeCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
						return nil
					})
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
					_, err = runGitCmd(ctx, gitPath, "fetch")
					Expect(err).NotTo(HaveOccurred())
					sha, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+ctpStaging.Spec.ActiveBranch)
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
					_, err = runGitCmd(ctx, gitPath, "fetch")
					Expect(err).NotTo(HaveOccurred())
					shaProdProposed, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+ctpProd.Spec.ProposedBranch)
					Expect(err).NotTo(HaveOccurred())
					shaProdProposed = strings.TrimSpace(shaProdProposed)

					// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
						Namespace: typeNamespacedName.Namespace,
					}, &ctpProd)
					g.Expect(err).To(Succeed())
					g.Expect(ctpProd.Status.Proposed.Hydrated.Sha).To(Equal(shaProdProposed))

					// Only create if it doesn't exist yet (to handle Eventually retries)
					_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, activeCommitStatusDevelopment, func() error {
						activeCommitStatusDevelopment.Spec.Sha = sha
						activeCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
						return nil
					})
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
			})
		})

		Context("When active branch has no hydrator metadata with active commit statuses", func() {
			var name string
			var gitRepo *promoterv1alpha1.GitRepository
			var promotionStrategy *promoterv1alpha1.PromotionStrategy
			var activeCommitStatusDevelopment, activeCommitStatusStaging *promoterv1alpha1.CommitStatus
			var typeNamespacedName types.NamespacedName
			var ctpDev, ctpStaging, ctpProd promoterv1alpha1.ChangeTransferPolicy
			var pullRequestDev, pullRequestStaging, pullRequestProd promoterv1alpha1.PullRequest

			BeforeEach(func() {
				By("Creating the resource")
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				name, scmSecret, scmProvider, gitRepo, activeCommitStatusDevelopment, activeCommitStatusStaging, promotionStrategy = promotionStrategyResource(ctx, "promotion-strategy-with-active-commit-status", "default")
				setupInitialTestGitRepoWithoutActiveMetadata(name, name)

				typeNamespacedName = types.NamespacedName{
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
				Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

				// Initialize empty structs for use in tests
				ctpDev = promoterv1alpha1.ChangeTransferPolicy{}
				ctpStaging = promoterv1alpha1.ChangeTransferPolicy{}
				ctpProd = promoterv1alpha1.ChangeTransferPolicy{}
				pullRequestDev = promoterv1alpha1.PullRequest{}
				pullRequestStaging = promoterv1alpha1.PullRequest{}
				pullRequestProd = promoterv1alpha1.PullRequest{}
			})

			AfterEach(func() {
				By("Cleaning up resources")
				_ = k8sClient.Delete(ctx, promotionStrategy)
			})

			It("should successfully reconcile the resource with no active dry sha", func() {
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
						Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
						Namespace: typeNamespacedName.Namespace,
					}, &ctpDev)
					g.Expect(err).To(Succeed())

					sha := ctpDev.Status.Active.Hydrated.Sha
					// Only create if it doesn't exist yet (to handle Eventually retries)
					_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, activeCommitStatusDevelopment, func() error {
						activeCommitStatusDevelopment.Spec.Sha = sha
						activeCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
						return nil
					})
					GinkgoLogr.Info("Updated commit status for development to sha: " + sha + " for branch " + ctpDev.Spec.ActiveBranch)
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
					_, err = runGitCmd(ctx, gitPath, "fetch")
					Expect(err).NotTo(HaveOccurred())
					sha, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+ctpStaging.Spec.ActiveBranch)
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
					_, err = runGitCmd(ctx, gitPath, "fetch")
					Expect(err).NotTo(HaveOccurred())
					shaProdProposed, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+ctpProd.Spec.ProposedBranch)
					Expect(err).NotTo(HaveOccurred())
					shaProdProposed = strings.TrimSpace(shaProdProposed)

					// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
						Namespace: typeNamespacedName.Namespace,
					}, &ctpProd)
					g.Expect(err).To(Succeed())
					g.Expect(ctpProd.Status.Proposed.Hydrated.Sha).To(Equal(shaProdProposed))

					// Only create if it doesn't exist yet (to handle Eventually retries)
					_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, activeCommitStatusStaging, func() error {
						activeCommitStatusStaging.Spec.Sha = sha
						activeCommitStatusStaging.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
						return nil
					})
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
			})
		})
	})

	Context("When reconciling a resource with a proposed commit status", func() {
		ctx := context.Background()

		Context("When git repo is initialized with proposed commit statuses", func() {
			var name string
			var gitRepo *promoterv1alpha1.GitRepository
			var promotionStrategy *promoterv1alpha1.PromotionStrategy
			var proposedCommitStatusDevelopment, proposedCommitStatusStaging *promoterv1alpha1.CommitStatus
			var typeNamespacedName types.NamespacedName

			BeforeEach(func() {
				By("Creating the resource")
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				name, scmSecret, scmProvider, gitRepo, proposedCommitStatusDevelopment, proposedCommitStatusStaging, promotionStrategy = promotionStrategyResource(ctx, "promotion-strategy-with-proposed-commit-status", "default")
				setupInitialTestGitRepoOnServer(ctx, name, name)

				typeNamespacedName = types.NamespacedName{
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

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			})

			AfterEach(func() {
				By("Cleaning up resources")
				_ = k8sClient.Delete(ctx, promotionStrategy)
			})

			It("should successfully reconcile the resource", func() {
				// Skip("Skipping test because of flakiness")
				By("Adding a pending commit")
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				makeChangeAndHydrateRepo(gitPath, name, name, "", "")
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
					_, err = runGitCmd(ctx, gitPath, "fetch")
					Expect(err).NotTo(HaveOccurred())
					sha, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+ctpDev.Spec.ProposedBranch)
					Expect(err).NotTo(HaveOccurred())
					sha = strings.TrimSpace(sha)

					// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
						Namespace: typeNamespacedName.Namespace,
					}, &ctpDev)
					g.Expect(err).To(Succeed())
					g.Expect(ctpDev.Status.Proposed.Hydrated.Sha).To(Equal(sha))

					g.Expect(sha).To(Not(BeEmpty()))
					// Only create if it doesn't exist yet (to handle Eventually retries)
					_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, proposedCommitStatusDevelopment, func() error {
						proposedCommitStatusDevelopment.Spec.Sha = sha
						proposedCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
						return nil
					})
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
					g.Expect(ctpDev.Status.PullRequest).ToNot(BeNil(), "CTP should preserve PR status")
					g.Expect(ctpDev.Status.PullRequest.State).To(Equal(promoterv1alpha1.PullRequestMerged), "PR state should be merged")

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

					g.Expect(promotionStrategy.Status.Environments[0].PullRequest).ToNot(BeNil(), "PromotionStrategy should preserve PR status after PR is merged/deleted")
					g.Expect(promotionStrategy.Status.Environments[0].PullRequest.State).To(Equal(promoterv1alpha1.PullRequestMerged), "PR state should be 'merged'")
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

					g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Author).To(Equal("GitOps Promoter"))
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
					g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Subject).To(ContainSubstring("initial empty commit"))
					g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Body).To(Equal(""))
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})

		Context("When active branch has no hydrator metadata with proposed commit statuses", func() {
			var name string
			var gitRepo *promoterv1alpha1.GitRepository
			var promotionStrategy *promoterv1alpha1.PromotionStrategy
			var proposedCommitStatusDevelopment, proposedCommitStatusStaging *promoterv1alpha1.CommitStatus
			var typeNamespacedName types.NamespacedName
			var ctpDev, ctpStaging, ctpProd promoterv1alpha1.ChangeTransferPolicy

			BeforeEach(func() {
				By("Creating the resource")
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				name, scmSecret, scmProvider, gitRepo, proposedCommitStatusDevelopment, proposedCommitStatusStaging, promotionStrategy = promotionStrategyResource(ctx, "promotion-strategy-with-proposed-commit-status", "default")
				setupInitialTestGitRepoWithoutActiveMetadata(name, name)

				typeNamespacedName = types.NamespacedName{
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

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

				// Initialize empty structs for use in tests
				ctpDev = promoterv1alpha1.ChangeTransferPolicy{}
				ctpStaging = promoterv1alpha1.ChangeTransferPolicy{}
				ctpProd = promoterv1alpha1.ChangeTransferPolicy{}
			})

			AfterEach(func() {
				By("Cleaning up resources")
				_ = k8sClient.Delete(ctx, promotionStrategy)
			})

			It("should successfully reconcile the resource", func() {
				// Skip("Skipping test because of flakiness")
				By("Adding a pending commit")
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				makeChangeAndHydrateRepo(gitPath, name, name, "", "")

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
					_, err = runGitCmd(ctx, gitPath, "fetch")
					Expect(err).NotTo(HaveOccurred())
					sha, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+ctpDev.Spec.ProposedBranch)
					Expect(err).NotTo(HaveOccurred())
					sha = strings.TrimSpace(sha)

					// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
						Namespace: typeNamespacedName.Namespace,
					}, &ctpDev)
					g.Expect(err).To(Succeed())
					g.Expect(ctpDev.Status.Proposed.Hydrated.Sha).To(Equal(sha))

					g.Expect(sha).To(Not(BeEmpty()))
					// Only create if it doesn't exist yet (to handle Eventually retries)
					_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, proposedCommitStatusDevelopment, func() error {
						proposedCommitStatusDevelopment.Spec.Sha = sha
						proposedCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
						return nil
					})
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
			})
		})
	})

	Context("When reconciling a resource with active commit status using ArgoCDCommitStatus", Label("argocdcommitstatus"), func() {
		ctx := context.Background()
		const argocdCSLabel = "argocd-health"
		const namespace = "default"

		Context("When git repo is initialized with ArgoCD commit statuses", func() {
			var plainName, name string
			var gitRepo *promoterv1alpha1.GitRepository
			var promotionStrategy *promoterv1alpha1.PromotionStrategy
			var argocdCommitStatus promoterv1alpha1.ArgoCDCommitStatus
			var argoCDAppDev, argoCDAppStaging, argoCDAppProduction argocd.Application
			var typeNamespacedName types.NamespacedName

			BeforeEach(func() {
				By("Creating the resource")
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				plainName = "promotion-strategy-with-active-commit-status-argocdcommitstatus"
				name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, plainName, "default")
				setupInitialTestGitRepoOnServer(ctx, name, name)

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
					{
						Key: argocdCSLabel,
					},
				}

				argocdCommitStatus = promoterv1alpha1.ArgoCDCommitStatus{
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

				argoCDAppDev, argoCDAppStaging, argoCDAppProduction = argocdApplications(namespace, plainName)

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
				Expect(k8sClient.Create(ctx, &argocdCommitStatus)).To(Succeed())
				Expect(k8sClient.Create(ctx, &argoCDAppDev)).To(Succeed())
				Expect(k8sClient.Create(ctx, &argoCDAppStaging)).To(Succeed())
				Expect(k8sClient.Create(ctx, &argoCDAppProduction)).To(Succeed())
			})

			AfterEach(func() {
				By("Cleaning up resources")
				_ = k8sClient.Delete(ctx, promotionStrategy)
				_ = k8sClient.Delete(ctx, &argocdCommitStatus)
				_ = k8sClient.Delete(ctx, &argoCDAppDev)
				_ = k8sClient.Delete(ctx, &argoCDAppStaging)
				_ = k8sClient.Delete(ctx, &argoCDAppProduction)
			})

			It("should successfully reconcile the resource", func() {
				// Skip("Skipping test because of flakiness")

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

					if argoCDAppDev.Status.Sync.Status != argocd.SyncStatusCodeSynced ||
						argoCDAppDev.Status.Health.Status != argocd.HealthStatusHealthy ||
						argoCDAppDev.Status.Sync.Revision != ctpDev.Status.Active.Hydrated.Sha {
						// Most likely the CTP sha changed. Update the app to simulate the app having been synced to the new commit.
						argoCDAppDev.Status.Sync.Status = argocd.SyncStatusCodeSynced
						argoCDAppDev.Status.Health.Status = argocd.HealthStatusHealthy
						argoCDAppDev.Status.Sync.Revision = ctpDev.Status.Active.Hydrated.Sha
						lastTransitionTime := metav1.Now()
						argoCDAppDev.Status.Health.LastTransitionTime = &lastTransitionTime
						err = k8sClient.Update(ctx, &argoCDAppDev)
						Expect(err).To(Succeed())
					}

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

				lastTransitionTime := metav1.Now()
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
						Namespace: typeNamespacedName.Namespace,
					}, &ctpStaging)
					g.Expect(err).To(Succeed())

					if argoCDAppStaging.Status.Sync.Status != argocd.SyncStatusCodeSynced ||
						argoCDAppStaging.Status.Health.Status != argocd.HealthStatusHealthy ||
						argoCDAppStaging.Status.Sync.Revision != ctpStaging.Status.Active.Hydrated.Sha {
						// Most likely the CTP sha changed. Update the app to simulate the app having been synced to the new commit.
						argoCDAppStaging.Status.Sync.Status = argocd.SyncStatusCodeSynced
						argoCDAppStaging.Status.Health.Status = argocd.HealthStatusHealthy
						argoCDAppStaging.Status.Sync.Revision = ctpStaging.Status.Active.Hydrated.Sha
						lastTransitionTime = metav1.Now()
						argoCDAppStaging.Status.Health.LastTransitionTime = &lastTransitionTime
						err = k8sClient.Update(ctx, &argoCDAppStaging)
						Expect(err).To(Succeed())
					}

					prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      prName,
						Namespace: typeNamespacedName.Namespace,
					}, &pullRequestProd)
					g.Expect(err).To(HaveOccurred())
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
				Expect(time.Since(lastTransitionTime.Time) >= lastTransitionTimeThreshold).To(BeTrue())
			})
		})

		Context("When apps are deployed across multiple clusters", Label("multicluster"), func() {
			var plainName, name string
			var gitRepo *promoterv1alpha1.GitRepository
			var promotionStrategy *promoterv1alpha1.PromotionStrategy
			var argocdCommitStatus promoterv1alpha1.ArgoCDCommitStatus
			var argoCDAppDev, argoCDAppStaging, argoCDAppProduction argocd.Application
			var typeNamespacedName types.NamespacedName

			BeforeEach(func() {
				By("Creating the resource")
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				plainName = "mc-promo-strategy-with-active-commit-status-argocdcommitstatus"
				name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, plainName, "default")
				setupInitialTestGitRepoOnServer(ctx, name, name)

				typeNamespacedName = types.NamespacedName{
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

				argocdCommitStatus = promoterv1alpha1.ArgoCDCommitStatus{
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

				argoCDAppDev, argoCDAppStaging, argoCDAppProduction = argocdApplications(namespace, plainName)

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
				Expect(k8sClient.Create(ctx, &argocdCommitStatus)).To(Succeed())
				Expect(k8sClientDev.Create(ctx, &argoCDAppDev)).To(Succeed())
				Expect(k8sClientStaging.Create(ctx, &argoCDAppStaging)).To(Succeed())
				Expect(k8sClient.Create(ctx, &argoCDAppProduction)).To(Succeed())
			})

			AfterEach(func() {
				By("Cleaning up resources")
				_ = k8sClient.Delete(ctx, promotionStrategy)
				_ = k8sClient.Delete(ctx, &argocdCommitStatus)
				_ = k8sClientDev.Delete(ctx, &argoCDAppDev)
				_ = k8sClientStaging.Delete(ctx, &argoCDAppStaging)
				_ = k8sClient.Delete(ctx, &argoCDAppProduction)
			})

			It("should successfully reconcile the resource across clusters", func() {
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
						case testBranchDevelopment:
							g.Expect(commitStatus.Spec.Url).To(Equal("https://dev.argocd.local/applications?labels=app%3Dmc-promo-strategy-with-active-commit-status-argocdcommitstatus%2C"))
						case testBranchStaging:
							g.Expect(commitStatus.Spec.Url).To(Equal("https://staging.argocd.local/applications?labels=app%3Dmc-promo-strategy-with-active-commit-status-argocdcommitstatus%2C"))
						case testBranchProduction:
							g.Expect(commitStatus.Spec.Url).To(Equal("https://prod.argocd.local/applications?labels=app%3Dmc-promo-strategy-with-active-commit-status-argocdcommitstatus%2C"))
						default:
							Fail("Unexpected environment branch: " + environment.Branch)
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

					if argoCDAppDev.Status.Sync.Status != argocd.SyncStatusCodeSynced ||
						argoCDAppDev.Status.Health.Status != argocd.HealthStatusHealthy ||
						argoCDAppDev.Status.Sync.Revision != ctpDev.Status.Active.Hydrated.Sha {
						// Most likely the CTP sha changed. Update the app to simulate the app having been synced to the new commit.
						argoCDAppDev.Status.Sync.Status = argocd.SyncStatusCodeSynced
						argoCDAppDev.Status.Health.Status = argocd.HealthStatusHealthy
						argoCDAppDev.Status.Sync.Revision = ctpDev.Status.Active.Hydrated.Sha
						lastTransitionTime := metav1.Now()
						argoCDAppDev.Status.Health.LastTransitionTime = &lastTransitionTime
						err = k8sClientDev.Update(ctx, &argoCDAppDev)
						Expect(err).To(Succeed())
					}

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

				lastTransitionTime := metav1.Now()
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
						Namespace: typeNamespacedName.Namespace,
					}, &ctpStaging)
					g.Expect(err).To(Succeed())

					if argoCDAppStaging.Status.Sync.Status != argocd.SyncStatusCodeSynced ||
						argoCDAppStaging.Status.Health.Status != argocd.HealthStatusHealthy ||
						argoCDAppStaging.Status.Sync.Revision != ctpStaging.Status.Active.Hydrated.Sha {
						// Most likely the CTP sha changed. Update the app to simulate the app having been synced to the new commit.
						argoCDAppStaging.Status.Sync.Status = argocd.SyncStatusCodeSynced
						argoCDAppStaging.Status.Health.Status = argocd.HealthStatusHealthy
						argoCDAppStaging.Status.Sync.Revision = ctpStaging.Status.Active.Hydrated.Sha
						lastTransitionTime = metav1.Now()
						argoCDAppStaging.Status.Health.LastTransitionTime = &lastTransitionTime
						err = k8sClientStaging.Update(ctx, &argoCDAppStaging)
						Expect(err).To(Succeed())
					}

					prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpProd.Spec.ProposedBranch, ctpProd.Spec.ActiveBranch))
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      prName,
						Namespace: typeNamespacedName.Namespace,
					}, &pullRequestProd)
					g.Expect(err).To(HaveOccurred())
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
				Expect(time.Since(lastTransitionTime.Time) >= lastTransitionTimeThreshold).To(BeTrue(), fmt.Sprintf("Last transition time should be at least %s ago, but was %s ago", lastTransitionTimeThreshold, time.Since(lastTransitionTime.Time)))
			})
		})
	})

	Context("When reconciling a resource with a proposed commit status we should have history", func() {
		ctx := context.Background()

		Context("When tracking proposed commit history", func() {
			var name string
			var gitRepo *promoterv1alpha1.GitRepository
			var promotionStrategy *promoterv1alpha1.PromotionStrategy
			var proposedCommitStatusDevelopment, proposedCommitStatusStaging *promoterv1alpha1.CommitStatus
			var typeNamespacedName types.NamespacedName
			var ctpDev, ctpStaging, ctpProd promoterv1alpha1.ChangeTransferPolicy
			var pullRequestDev, pullRequestStaging, pullRequestProd promoterv1alpha1.PullRequest

			BeforeEach(func() {
				By("Creating the resource")
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				name, scmSecret, scmProvider, gitRepo, proposedCommitStatusDevelopment, proposedCommitStatusStaging, promotionStrategy = promotionStrategyResource(ctx, "promotion-strategy-with-proposed-commit-status", "default")
				setupInitialTestGitRepoOnServer(ctx, name, name)

				typeNamespacedName = types.NamespacedName{
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
				proposedCommitStatusDevelopment.Spec.Url = "https://example.com/dev"

				proposedCommitStatusStaging.Spec.Name = "no-deployments-allowed"
				proposedCommitStatusStaging.Labels = map[string]string{
					promoterv1alpha1.CommitStatusLabel: "no-deployments-allowed",
				}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())

				// Initialize empty structs for use in tests
				ctpDev = promoterv1alpha1.ChangeTransferPolicy{}
				ctpStaging = promoterv1alpha1.ChangeTransferPolicy{}
				ctpProd = promoterv1alpha1.ChangeTransferPolicy{}
				pullRequestDev = promoterv1alpha1.PullRequest{}
				pullRequestStaging = promoterv1alpha1.PullRequest{}
				pullRequestProd = promoterv1alpha1.PullRequest{}
			})

			AfterEach(func() {
				By("Cleaning up resources")
				_ = k8sClient.Delete(ctx, promotionStrategy)
			})

			It("should successfully reconcile the resource", func() {
				// Skip("Skipping test because of flakiness")
				By("Adding a pending commit")
				gitPath, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				drySha, _ := makeChangeAndHydrateRepo(gitPath, name, name, "", "")

				Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

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

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
						Namespace: typeNamespacedName.Namespace,
					}, &ctpDev)
					g.Expect(err).To(Succeed())
					g.Expect(ctpDev.Status.Proposed.Dry.Sha).To(Equal(drySha))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Updating the commit status for the development environment to success")
				Eventually(func(g Gomega) {
					_, err = runGitCmd(ctx, gitPath, "fetch")
					Expect(err).NotTo(HaveOccurred())
					sha, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+ctpDev.Spec.ProposedBranch)
					Expect(err).NotTo(HaveOccurred())
					sha = strings.TrimSpace(sha)

					// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
						Namespace: typeNamespacedName.Namespace,
					}, &ctpDev)
					g.Expect(err).To(Succeed())
					g.Expect(ctpDev.Status.Proposed.Hydrated.Sha).To(Equal(sha))

					g.Expect(sha).To(Not(BeEmpty()))
					// Only create if it doesn't exist yet (to handle Eventually retries)
					_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, proposedCommitStatusDevelopment, func() error {
						proposedCommitStatusDevelopment.Spec.Sha = sha
						proposedCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
						return nil
					})
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
					g.Expect(ctpDev.Status.PullRequest).ToNot(BeNil(), "CTP should preserve PR status")
					g.Expect(ctpDev.Status.PullRequest.State).To(Equal(promoterv1alpha1.PullRequestMerged), "PR state should be merged")

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

				By("Updating the commit status for the staging environment to success")
				Eventually(func(g Gomega) {
					_, err = runGitCmd(ctx, gitPath, "fetch")
					Expect(err).NotTo(HaveOccurred())
					sha, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+ctpStaging.Spec.ProposedBranch)
					Expect(err).NotTo(HaveOccurred())
					sha = strings.TrimSpace(sha)

					// Check that the proposed commit has the correct sha, aka it has reconciled at least once since adding new commits
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
						Namespace: typeNamespacedName.Namespace,
					}, &ctpStaging)
					g.Expect(err).To(Succeed())
					g.Expect(ctpStaging.Status.Proposed.Hydrated.Sha).To(Equal(sha))

					g.Expect(sha).To(Not(BeEmpty()))
					_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, proposedCommitStatusStaging, func() error {
						proposedCommitStatusStaging.Spec.Sha = sha
						proposedCommitStatusStaging.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
						return nil
					})
					GinkgoLogr.Info("Updated commit status for staging to sha: " + sha)
					g.Expect(err).To(Succeed())

					g.Expect(len(ctpStaging.Status.Proposed.CommitStatuses)).To(Not(BeZero()))
					g.Expect(ctpStaging.Status.Proposed.CommitStatuses[0].Url).To(Equal(proposedCommitStatusStaging.Spec.Url))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("By checking that the development and staging pull requests are closed and production pull request are still open")
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
					g.Expect(ctpDev.Status.PullRequest).ToNot(BeNil(), "CTP should preserve PR status")
					g.Expect(ctpDev.Status.PullRequest.State).To(Equal(promoterv1alpha1.PullRequestMerged), "PR state should be merged")

					prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctpStaging.Spec.ProposedBranch, ctpStaging.Spec.ActiveBranch))
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      prName,
						Namespace: typeNamespacedName.Namespace,
					}, &pullRequestStaging)
					g.Expect(err).To(HaveOccurred())
					g.Expect(errors.IsNotFound(err)).To(BeTrue())

					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
						Namespace: typeNamespacedName.Namespace,
					}, &ctpStaging)
					g.Expect(err).To(Succeed())
					g.Expect(ctpStaging.Status.PullRequest).ToNot(BeNil(), "CTP should preserve PR status")
					g.Expect(ctpStaging.Status.PullRequest.State).To(Equal(promoterv1alpha1.PullRequestMerged), "PR state should be merged")

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

					g.Expect(promotionStrategy.Status.Environments[0].PullRequest).ToNot(BeNil(), "PromotionStrategy should preserve PR status")
					g.Expect(promotionStrategy.Status.Environments[0].PullRequest.State).To(Equal(promoterv1alpha1.PullRequestMerged), "PR state should be merged")
					g.Expect(promotionStrategy.Status.Environments[1].PullRequest).ToNot(BeNil(), "PromotionStrategy should preserve PR status")
					g.Expect(promotionStrategy.Status.Environments[1].PullRequest.State).To(Equal(promoterv1alpha1.PullRequestMerged), "PR state should be merged")
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

					g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Author).To(Equal("GitOps Promoter"))
					g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Subject).To(ContainSubstring("Promote"))
					g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Body).To(ContainSubstring("This PR is promoting the environment branch"))

					g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.References).To(HaveLen(1))
					g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.References[0].Commit.Subject).To(Equal("This is a fix for an upstream issue"))
					g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.References[0].Commit.Body).To(Equal("This is a body of the commit"))
					g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.References[0].Commit.RepoURL).To(Equal("https://github.com/upstream/repo"))
					g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.References[0].Commit.Sha).To(Equal("c4c862564afe56abf8cc8ac683eee3dc8bf96108"))

					g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Author).To(Equal("testuser <testmail@test.com>"))
					g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Subject).To(Equal("added fake manifests commit with timestamp"))
					g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Body).To(Equal(""))
					g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Author).To(Equal("GitOps Promoter"))
					g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Subject).To(ContainSubstring("Promote"))
					g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Body).To(ContainSubstring("This PR is promoting the environment branch"))

					g.Expect(promotionStrategy.Status.Environments[0].PullRequest).ToNot(BeNil(), "PromotionStrategy should preserve PR status")
					g.Expect(promotionStrategy.Status.Environments[0].PullRequest.State).To(Equal(promoterv1alpha1.PullRequestMerged), "PR state should be merged")
					g.Expect(promotionStrategy.Status.Environments[1].PullRequest).ToNot(BeNil(), "PromotionStrategy should preserve PR status")
					g.Expect(promotionStrategy.Status.Environments[1].PullRequest.State).To(Equal(promoterv1alpha1.PullRequestMerged), "PR state should be merged")
					g.Expect(promotionStrategy.Status.Environments[2].PullRequest).To(Not(BeNil()))
				}, constants.EventuallyTimeout).Should(Succeed())
			})
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
				{Branch: testBranchDevelopment},
				{Branch: testBranchStaging},
				{Branch: testBranchProduction},
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

var _ = Describe("PromotionStrategy Bug Tests", func() {
	Context("When PR merges while previous environment has non-passing checks", func() {
		ctx := context.Background()

		Context("When PR is merged before checks pass", func() {
			var name string
			var gitRepo *promoterv1alpha1.GitRepository
			var promotionStrategy *promoterv1alpha1.PromotionStrategy
			var activeCommitStatusDevelopment, activeCommitStatusStaging *promoterv1alpha1.CommitStatus
			var typeNamespacedName types.NamespacedName
			var ctpDev, ctpStaging promoterv1alpha1.ChangeTransferPolicy

			BeforeEach(func() {
				By("Creating the resource")
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				name, scmSecret, scmProvider, gitRepo, activeCommitStatusDevelopment, activeCommitStatusStaging, promotionStrategy = promotionStrategyResource(ctx, "promotion-strategy-bug-test", "default")
				setupInitialTestGitRepoOnServer(ctx, name, name)

				typeNamespacedName = types.NamespacedName{
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
				Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

				// Initialize empty structs for use in tests
				ctpDev = promoterv1alpha1.ChangeTransferPolicy{}
				ctpStaging = promoterv1alpha1.ChangeTransferPolicy{}
			})

			AfterEach(func() {
				By("Cleaning up resources")
				_ = k8sClient.Delete(ctx, promotionStrategy)
			})

			It("should not update commit status to pending after PR is already merged", func() {
				By("Waiting for ChangeTransferPolicies to be created")
				Eventually(func(g Gomega) {
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
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Adding first commit")
				gitPath1, err := os.MkdirTemp("", "*")
				Expect(err).NotTo(HaveOccurred())
				firstDrySha, _ := makeChangeAndHydrateRepo(gitPath1, name, name, "first commit", "")

				By("Waiting for dev PR to be auto-merged (first commit)")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      ctpDev.Name,
						Namespace: ctpDev.Namespace,
					}, &ctpDev)
					g.Expect(err).To(Succeed())
					g.Expect(ctpDev.Status.Active.Dry.Sha).To(Equal(firstDrySha))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Marking dev's active commit status as success for first commit")
				var firstDevActiveSha string
				Eventually(func(g Gomega) {
					_, err = runGitCmd(ctx, gitPath1, "fetch")
					Expect(err).NotTo(HaveOccurred())
					sha, err := runGitCmd(ctx, gitPath1, "rev-parse", "origin/"+ctpDev.Spec.ActiveBranch)
					Expect(err).NotTo(HaveOccurred())
					firstDevActiveSha = strings.TrimSpace(sha)

					_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, activeCommitStatusDevelopment, func() error {
						activeCommitStatusDevelopment.Spec.Sha = firstDevActiveSha
						activeCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
						return nil
					})
					g.Expect(err).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Waiting for staging PR to be auto-merged (first commit)")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      ctpStaging.Name,
						Namespace: ctpStaging.Namespace,
					}, &ctpStaging)
					g.Expect(err).To(Succeed())
					g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Equal(firstDrySha))
					// At this point, staging's active == proposed (both are firstDrySha)
					g.Expect(ctpStaging.Status.Proposed.Dry.Sha).To(Equal(firstDrySha))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Verifying staging stays stable with active == proposed (ensure skip logic kicks in)")
				Consistently(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      ctpStaging.Name,
						Namespace: ctpStaging.Namespace,
					}, &ctpStaging)
					g.Expect(err).To(Succeed())
					g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Equal(firstDrySha))
					g.Expect(ctpStaging.Status.Proposed.Dry.Sha).To(Equal(firstDrySha))
				}, time.Second*5, time.Millisecond*500).Should(Succeed())

				By("Capturing baseline: previous-environment commit status should be at success")
				csName := utils.KubeSafeUniqueName(ctx, promoterv1alpha1.PreviousEnvProposedCommitPrefixNameLabel+ctpStaging.Name)
				commitStatus := &promoterv1alpha1.CommitStatus{}
				var commitStatusOriginalSha string
				var commitStatusOriginalPhase promoterv1alpha1.CommitStatusPhase

				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      csName,
						Namespace: ctpStaging.Namespace,
					}, commitStatus)
					g.Expect(err).To(Succeed())
					g.Expect(commitStatus.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				}, constants.EventuallyTimeout).Should(Succeed())

				// Capture baseline values
				commitStatusOriginalSha = commitStatus.Spec.Sha
				commitStatusOriginalPhase = commitStatus.Spec.Phase

				GinkgoLogr.Info("Baseline commit status captured",
					"phase", commitStatusOriginalPhase,
					"sha", commitStatusOriginalSha)

				By("Setting dev's commit status to PENDING (simulating a non-passing check)")
				Eventually(func(g Gomega) {
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      activeCommitStatusDevelopment.Name,
						Namespace: activeCommitStatusDevelopment.Namespace,
					}, activeCommitStatusDevelopment)
					g.Expect(err).To(Succeed())

					activeCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhasePending
					err = k8sClient.Update(ctx, activeCommitStatusDevelopment)
					g.Expect(err).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Triggering PromotionStrategy reconciliation multiple times to test skip logic")
				for i := 0; i < 5; i++ {
					Eventually(func(g Gomega) {
						err := k8sClient.Get(ctx, typeNamespacedName, promotionStrategy)
						g.Expect(err).To(Succeed())
						orig := promotionStrategy.DeepCopy()
						if promotionStrategy.Annotations == nil {
							promotionStrategy.Annotations = make(map[string]string)
						}
						// Use a test annotation to trigger reconciliation by modifying the object
						promotionStrategy.Annotations["test-trigger"] = metav1.Now().Format(time.RFC3339)
						err = k8sClient.Patch(ctx, promotionStrategy, client.MergeFrom(orig))
						g.Expect(err).To(Succeed())
					}, constants.EventuallyTimeout).Should(Succeed())
				}

				By("Verifying commit status was NOT updated after staging PR merged")
				// The bug would be: commit status gets updated to "pending" even though staging's active == proposed
				// Prevent this by skipping the update when active == proposed
				// Use Consistently (not Eventually) since we're checking that something DOESN'T change
				Consistently(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      csName,
						Namespace: ctpStaging.Namespace,
					}, commitStatus)
					g.Expect(err).To(Succeed())

					// Phase should stay at success
					g.Expect(commitStatus.Spec.Phase).To(Equal(commitStatusOriginalPhase),
						"CommitStatus phase changed from '%s' to '%s' after PR merged. "+
							"The skip logic should have prevented any updates.",
						commitStatusOriginalPhase, commitStatus.Spec.Phase)

					// SHA should not have changed - it should still point to the original merged commit
					g.Expect(commitStatus.Spec.Sha).To(Equal(commitStatusOriginalSha),
						"CommitStatus SHA changed from '%s' to '%s' after PR merged. "+
							"The skip logic should have kept it pointing to the original merged commit.",
						commitStatusOriginalSha, commitStatus.Spec.Sha)
				}, time.Second*5, time.Millisecond*500).Should(Succeed())

				GinkgoLogr.Info("Final commit status state (after all reconciliations)",
					"phase", commitStatus.Spec.Phase,
					"sha", commitStatus.Spec.Sha,
					"originalPhase", commitStatusOriginalPhase,
					"originalSha", commitStatusOriginalSha)

				By("Verifying staging CTP still has active == proposed")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      ctpStaging.Name,
						Namespace: ctpStaging.Namespace,
					}, &ctpStaging)
					g.Expect(err).To(Succeed())
					g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Equal(ctpStaging.Status.Proposed.Dry.Sha),
						"Staging should still have active == proposed")
				}, constants.EventuallyTimeout).Should(Succeed())
			})
		})
	})

	Context("When environment branch names are changed", func() {
		ctx := context.Background()

		Context("When cleaning up orphaned CTPs", func() {
			var name string
			var gitRepo *promoterv1alpha1.GitRepository
			var promotionStrategy *promoterv1alpha1.PromotionStrategy
			var typeNamespacedName types.NamespacedName
			var oldCtpDevName, oldCtpStagingName, oldCtpProdName string

			BeforeEach(func() {
				By("Creating initial resources with environments/dev naming")
				var scmSecret *v1.Secret
				var scmProvider *promoterv1alpha1.ScmProvider
				name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "promotion-strategy-cleanup-test", "default")

				// Setup git repo with branches using "environments" prefix (with typo)
				setupInitialTestGitRepoOnServer(ctx, name, name)

				// Modify the PromotionStrategy to use "environments" prefix (simulating typo)
				promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
					{
						Branch:    "environments/development",
						AutoMerge: ptr.To(true),
					},
					{
						Branch:    "environments/staging",
						AutoMerge: ptr.To(true),
					},
					{
						Branch:    "environments/production",
						AutoMerge: ptr.To(true),
					},
				}

				typeNamespacedName = types.NamespacedName{
					Name:      name,
					Namespace: "default",
				}

				Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
				Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
				Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
				Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			})

			AfterEach(func() {
				By("Cleaning up resources")
				_ = k8sClient.Delete(ctx, promotionStrategy)
			})

			It("should cleanup orphaned ChangeTransferPolicies", func() {
				// Wait for initial CTPs to be created with wrong names
				By("Waiting for initial ChangeTransferPolicies to be created")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, promotionStrategy)
					g.Expect(err).To(Succeed())

					oldCtpDevName = utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, "environments/development"))
					oldCtpStagingName = utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, "environments/staging"))
					oldCtpProdName = utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, "environments/production"))

					oldCtpDev := &promoterv1alpha1.ChangeTransferPolicy{}
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      oldCtpDevName,
						Namespace: "default",
					}, oldCtpDev)
					g.Expect(err).To(Succeed())

					oldCtpStaging := &promoterv1alpha1.ChangeTransferPolicy{}
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      oldCtpStagingName,
						Namespace: "default",
					}, oldCtpStaging)
					g.Expect(err).To(Succeed())

					oldCtpProd := &promoterv1alpha1.ChangeTransferPolicy{}
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      oldCtpProdName,
						Namespace: "default",
					}, oldCtpProd)
					g.Expect(err).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Updating PromotionStrategy to fix branch names (environments -> environment)")
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, promotionStrategy)
					g.Expect(err).To(Succeed())

					// Fix the typo in branch names
					promotionStrategy.Spec.Environments = []promoterv1alpha1.Environment{
						{
							Branch:    testBranchDevelopment, // "environment/development"
							AutoMerge: ptr.To(true),
						},
						{
							Branch:    testBranchStaging, // "environment/staging"
							AutoMerge: ptr.To(true),
						},
						{
							Branch:    testBranchProduction, // "environment/production"
							AutoMerge: ptr.To(true),
						},
					}

					err = k8sClient.Update(ctx, promotionStrategy)
					g.Expect(err).To(Succeed())
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Verifying new ChangeTransferPolicies are created with correct names")
				newCtpDev := &promoterv1alpha1.ChangeTransferPolicy{}
				newCtpStaging := &promoterv1alpha1.ChangeTransferPolicy{}
				newCtpProd := &promoterv1alpha1.ChangeTransferPolicy{}

				Eventually(func(g Gomega) {
					newCtpDevName := utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, testBranchDevelopment))
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      newCtpDevName,
						Namespace: "default",
					}, newCtpDev)
					g.Expect(err).To(Succeed())
					g.Expect(newCtpDev.Spec.ActiveBranch).To(Equal(testBranchDevelopment))

					newCtpStagingName := utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, testBranchStaging))
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      newCtpStagingName,
						Namespace: "default",
					}, newCtpStaging)
					g.Expect(err).To(Succeed())
					g.Expect(newCtpStaging.Spec.ActiveBranch).To(Equal(testBranchStaging))

					newCtpProdName := utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, testBranchProduction))
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      newCtpProdName,
						Namespace: "default",
					}, newCtpProd)
					g.Expect(err).To(Succeed())
					g.Expect(newCtpProd.Spec.ActiveBranch).To(Equal(testBranchProduction))
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Verifying old ChangeTransferPolicies are deleted")
				Eventually(func(g Gomega) {
					oldCtpDev := &promoterv1alpha1.ChangeTransferPolicy{}
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      oldCtpDevName,
						Namespace: "default",
					}, oldCtpDev)
					g.Expect(errors.IsNotFound(err)).To(BeTrue(), "Old dev CTP should be deleted")

					oldCtpStaging := &promoterv1alpha1.ChangeTransferPolicy{}
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      oldCtpStagingName,
						Namespace: "default",
					}, oldCtpStaging)
					g.Expect(errors.IsNotFound(err)).To(BeTrue(), "Old staging CTP should be deleted")

					oldCtpProd := &promoterv1alpha1.ChangeTransferPolicy{}
					err = k8sClient.Get(ctx, types.NamespacedName{
						Name:      oldCtpProdName,
						Namespace: "default",
					}, oldCtpProd)
					g.Expect(errors.IsNotFound(err)).To(BeTrue(), "Old prod CTP should be deleted")
				}, constants.EventuallyTimeout).Should(Succeed())

				By("Verifying only 3 ChangeTransferPolicies exist for this PromotionStrategy")
				Eventually(func(g Gomega) {
					ctpList := &promoterv1alpha1.ChangeTransferPolicyList{}
					err := k8sClient.List(ctx, ctpList, client.InNamespace("default"), client.MatchingLabels{
						promoterv1alpha1.PromotionStrategyLabel: utils.KubeSafeLabel(promotionStrategy.Name),
					})
					g.Expect(err).To(Succeed())
					g.Expect(ctpList.Items).To(HaveLen(3), "Should have exactly 3 CTPs after cleanup")
				}, constants.EventuallyTimeout).Should(Succeed())

				Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
			})
		})
	})

	Context("Out-of-order hydration protection", func() {
		// This test verifies that the system correctly blocks downstream environments
		// from promoting when upstream environments haven't been hydrated yet.
		ctx := context.Background()

		var name string
		var gitRepo *promoterv1alpha1.GitRepository
		var promotionStrategy *promoterv1alpha1.PromotionStrategy
		var activeCommitStatusDevelopment, activeCommitStatusStaging *promoterv1alpha1.CommitStatus
		var typeNamespacedName types.NamespacedName
		var ctpDev, ctpStaging promoterv1alpha1.ChangeTransferPolicy

		BeforeEach(func() {
			By("Creating the resources with active commit statuses to enable previous environment checks")
			var scmSecret *v1.Secret
			var scmProvider *promoterv1alpha1.ScmProvider
			name, scmSecret, scmProvider, gitRepo, activeCommitStatusDevelopment, activeCommitStatusStaging, promotionStrategy = promotionStrategyResource(ctx, "out-of-order-hydration-test", "default")
			setupInitialTestGitRepoOnServer(ctx, name, name)

			typeNamespacedName = types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			// Configure active commit statuses - this enables the previous environment check
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
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			ctpDev = promoterv1alpha1.ChangeTransferPolicy{}
			ctpStaging = promoterv1alpha1.ChangeTransferPolicy{}
		})

		AfterEach(func() {
			By("Cleaning up resources")
			_ = k8sClient.Delete(ctx, promotionStrategy)
		})

		It("should block staging promotion when dev hasn't been hydrated for the same dry commit", func() {
			By("Waiting for ChangeTransferPolicies to be created and reconciled")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Active.Dry.Sha).NotTo(BeEmpty())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.Active.Dry.Sha).NotTo(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Making a change and hydrating all environments (normal flow first)")
			gitPath1, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath1) }()
			firstDrySha, _ := makeChangeAndHydrateRepo(gitPath1, name, name, "first commit", "")

			By("Setting dev's commit status to success so staging can promote")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpDev.Name,
					Namespace: ctpDev.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Active.Dry.Sha).To(Equal(firstDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			// Get the active hydrated SHA for dev to set the commit status
			_, err = runGitCmd(ctx, gitPath1, "fetch")
			Expect(err).NotTo(HaveOccurred())
			devActiveSha, err := runGitCmd(ctx, gitPath1, "rev-parse", "origin/"+ctpDev.Spec.ActiveBranch)
			Expect(err).NotTo(HaveOccurred())
			devActiveSha = strings.TrimSpace(devActiveSha)

			_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, activeCommitStatusDevelopment, func() error {
				activeCommitStatusDevelopment.Spec.Sha = devActiveSha
				activeCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				return nil
			})
			Expect(err).To(Succeed())

			By("Waiting for staging to be promoted (first commit)")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpStaging.Name,
					Namespace: ctpStaging.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Equal(firstDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			// Set staging's commit status to success
			stagingActiveSha, err := runGitCmd(ctx, gitPath1, "rev-parse", "origin/"+ctpStaging.Spec.ActiveBranch)
			Expect(err).NotTo(HaveOccurred())
			stagingActiveSha = strings.TrimSpace(stagingActiveSha)
			_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, activeCommitStatusStaging, func() error {
				activeCommitStatusStaging.Spec.Sha = stagingActiveSha
				activeCommitStatusStaging.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				return nil
			})
			Expect(err).To(Succeed())

			By("Now simulating out-of-order hydration: hydrate ONLY staging for a new commit")
			gitPath2, err := cloneTestRepo(ctx, name)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath2) }()

			secondDrySha, err := makeDryCommit(ctx, gitPath2, "second commit - will be hydrated out of order")
			Expect(err).NotTo(HaveOccurred())

			By("Hydrating ONLY staging-next (simulating out-of-order hydration)")
			err = hydrateEnvironment(ctx, gitPath2, "environment/staging-next", secondDrySha, "hydrate staging for second dry sha")
			Expect(err).NotTo(HaveOccurred())

			By("Verifying staging sees the new proposed dry SHA")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpStaging.Name,
					Namespace: ctpStaging.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.Proposed.Dry.Sha).To(Equal(secondDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying dev still has the old proposed dry SHA (hasn't been hydrated)")
			Consistently(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpDev.Name,
					Namespace: ctpDev.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				// Dev's proposed should still be the first dry SHA
				g.Expect(ctpDev.Status.Proposed.Dry.Sha).To(Equal(firstDrySha))
			}, 3*time.Second, 500*time.Millisecond).Should(Succeed())

			By("Verifying the previous environment commit status is pending (blocking staging)")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, promotionStrategy)
				g.Expect(err).To(Succeed())
				g.Expect(promotionStrategy.Status.Environments).To(HaveLen(3))

				// Check that staging has a pending previous environment status
				stagingEnv := promotionStrategy.Status.Environments[1]
				g.Expect(stagingEnv.Proposed.Dry.Sha).To(Equal(secondDrySha))

				// The previous environment commit status should exist and be pending
				var prevEnvCS promoterv1alpha1.CommitStatus
				csName := utils.KubeSafeUniqueName(ctx, promoterv1alpha1.PreviousEnvProposedCommitPrefixNameLabel+ctpStaging.Name)
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      csName,
					Namespace: "default",
				}, &prevEnvCS)
				g.Expect(err).To(Succeed())
				g.Expect(prevEnvCS.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				g.Expect(prevEnvCS.Spec.Description).To(ContainSubstring("hydrator to finish processing"))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Now hydrating dev-next with the second dry SHA")
			err = hydrateEnvironment(ctx, gitPath2, testBranchDevelopmentNext, secondDrySha, "hydrate dev for second dry sha")
			Expect(err).NotTo(HaveOccurred())

			By("Verifying dev now has the new proposed dry SHA")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpDev.Name,
					Namespace: ctpDev.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Proposed.Dry.Sha).To(Equal(secondDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Waiting for dev to promote the second dry SHA")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpDev.Name,
					Namespace: ctpDev.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Active.Dry.Sha).To(Equal(secondDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Setting dev's commit status to success for the second commit")
			_, err = runGitCmd(ctx, gitPath2, "fetch")
			Expect(err).NotTo(HaveOccurred())
			devActiveSha2, err := runGitCmd(ctx, gitPath2, "rev-parse", "origin/"+ctpDev.Spec.ActiveBranch)
			Expect(err).NotTo(HaveOccurred())
			devActiveSha2 = strings.TrimSpace(devActiveSha2)

			_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, activeCommitStatusDevelopment, func() error {
				activeCommitStatusDevelopment.Spec.Sha = devActiveSha2
				activeCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				return nil
			})
			Expect(err).To(Succeed())

			By("Verifying staging can now be promoted (previous environment check passes)")
			Eventually(func(g Gomega) {
				var prevEnvCS promoterv1alpha1.CommitStatus
				csName := utils.KubeSafeUniqueName(ctx, promoterv1alpha1.PreviousEnvProposedCommitPrefixNameLabel+ctpStaging.Name)
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      csName,
					Namespace: "default",
				}, &prevEnvCS)
				g.Expect(err).To(Succeed())
				g.Expect(prevEnvCS.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying staging promotes the second dry SHA")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpStaging.Name,
					Namespace: ctpStaging.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Equal(secondDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should unblock staging when dev receives only a git note (no new commit)", func() {
			By("Waiting for ChangeTransferPolicies to be created and reconciled")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Active.Dry.Sha).NotTo(BeEmpty())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.Active.Dry.Sha).NotTo(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Making a change and hydrating all environments (normal flow first)")
			gitPath1, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath1) }()
			firstDrySha, _ := makeChangeAndHydrateRepo(gitPath1, name, name, "first commit", "")

			By("Setting dev's commit status to success so staging can promote")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpDev.Name,
					Namespace: ctpDev.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Active.Dry.Sha).To(Equal(firstDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			// Get the active hydrated SHA for dev to set the commit status
			_, err = runGitCmd(ctx, gitPath1, "fetch")
			Expect(err).NotTo(HaveOccurred())
			devActiveSha, err := runGitCmd(ctx, gitPath1, "rev-parse", "origin/"+ctpDev.Spec.ActiveBranch)
			Expect(err).NotTo(HaveOccurred())
			devActiveSha = strings.TrimSpace(devActiveSha)

			_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, activeCommitStatusDevelopment, func() error {
				activeCommitStatusDevelopment.Spec.Sha = devActiveSha
				activeCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				return nil
			})
			Expect(err).To(Succeed())

			By("Waiting for staging to be promoted (first commit)")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpStaging.Name,
					Namespace: ctpStaging.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Equal(firstDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			// Set staging's commit status to success
			stagingActiveSha, err := runGitCmd(ctx, gitPath1, "rev-parse", "origin/"+ctpStaging.Spec.ActiveBranch)
			Expect(err).NotTo(HaveOccurred())
			stagingActiveSha = strings.TrimSpace(stagingActiveSha)
			_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, activeCommitStatusStaging, func() error {
				activeCommitStatusStaging.Spec.Sha = stagingActiveSha
				activeCommitStatusStaging.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				return nil
			})
			Expect(err).To(Succeed())

			By("Now simulating out-of-order hydration: hydrate ONLY staging for a new commit")
			gitPath2, err := cloneTestRepo(ctx, name)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath2) }()

			secondDrySha, err := makeDryCommit(ctx, gitPath2, "second commit - git note only test")
			Expect(err).NotTo(HaveOccurred())

			By("Hydrating ONLY staging-next (simulating out-of-order hydration)")
			err = hydrateEnvironment(ctx, gitPath2, "environment/staging-next", secondDrySha, "hydrate staging for second dry sha")
			Expect(err).NotTo(HaveOccurred())

			By("Verifying staging sees the new proposed dry SHA")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpStaging.Name,
					Namespace: ctpStaging.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.Proposed.Dry.Sha).To(Equal(secondDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying the previous environment commit status is pending (blocking staging)")
			Eventually(func(g Gomega) {
				var prevEnvCS promoterv1alpha1.CommitStatus
				csName := utils.KubeSafeUniqueName(ctx, promoterv1alpha1.PreviousEnvProposedCommitPrefixNameLabel+ctpStaging.Name)
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      csName,
					Namespace: "default",
				}, &prevEnvCS)
				g.Expect(err).To(Succeed())
				g.Expect(prevEnvCS.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				g.Expect(prevEnvCS.Spec.Description).To(ContainSubstring("hydrator to finish processing"))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Adding ONLY a git note to dev's existing hydrated commit (no new commit)")
			// This simulates the hydrator saying "the manifests haven't changed, but I've processed this dry SHA"
			err = addNoteToEnvironment(ctx, gitPath2, testBranchDevelopmentNext, secondDrySha)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for dev CTP to pick up the git note")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpDev.Name,
					Namespace: ctpDev.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				// Dev's Note.DrySha should be the second dry SHA (from git note)
				g.Expect(ctpDev.Status.Proposed.Note.DrySha).To(Equal(secondDrySha))
				// Dev's Proposed.Dry.Sha should still be the first dry SHA (no new commit was made)
				g.Expect(ctpDev.Status.Proposed.Dry.Sha).To(Equal(firstDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying staging is now unblocked (previous env check passes due to git note)")
			Eventually(func(g Gomega) {
				var prevEnvCS promoterv1alpha1.CommitStatus
				csName := utils.KubeSafeUniqueName(ctx, promoterv1alpha1.PreviousEnvProposedCommitPrefixNameLabel+ctpStaging.Name)
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      csName,
					Namespace: "default",
				}, &prevEnvCS)
				g.Expect(err).To(Succeed())
				g.Expect(prevEnvCS.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying staging promotes the second dry SHA")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpStaging.Name,
					Namespace: ctpStaging.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Equal(secondDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should allow production to promote older commit when staging has moved ahead", func() {
			// This test verifies the scenario where:
			// 1. Dry commit A is made (commitTime: 10:00)
			// 2. All environments get hydrated for A
			// 3. Before production merges A, someone makes dry commit B (commitTime: 10:05) that only affects staging
			// 4. Staging hydrates and merges B (production's hydrated manifests are unchanged for B)
			// 5. Production should be allowed to promote A since staging has already moved on

			By("Waiting for ChangeTransferPolicies to be created and reconciled")
			var ctpProd promoterv1alpha1.ChangeTransferPolicy
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Active.Dry.Sha).NotTo(BeEmpty())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.Active.Dry.Sha).NotTo(BeEmpty())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())
				g.Expect(ctpProd.Status.Active.Dry.Sha).NotTo(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Making first commit (A) and hydrating all environments")
			gitPath1, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath1) }()
			firstDrySha, _ := makeChangeAndHydrateRepo(gitPath1, name, name, "first commit A", "")

			By("Setting dev's commit status to success")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpDev.Name,
					Namespace: ctpDev.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				g.Expect(ctpDev.Status.Active.Dry.Sha).To(Equal(firstDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			_, err = runGitCmd(ctx, gitPath1, "fetch")
			Expect(err).NotTo(HaveOccurred())
			devActiveSha, err := runGitCmd(ctx, gitPath1, "rev-parse", "origin/"+ctpDev.Spec.ActiveBranch)
			Expect(err).NotTo(HaveOccurred())
			devActiveSha = strings.TrimSpace(devActiveSha)

			_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, activeCommitStatusDevelopment, func() error {
				activeCommitStatusDevelopment.Spec.Sha = devActiveSha
				activeCommitStatusDevelopment.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				return nil
			})
			Expect(err).To(Succeed())

			By("Waiting for staging to promote first commit A")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpStaging.Name,
					Namespace: ctpStaging.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Equal(firstDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			// Set staging's commit status to success
			stagingActiveSha, err := runGitCmd(ctx, gitPath1, "rev-parse", "origin/"+ctpStaging.Spec.ActiveBranch)
			Expect(err).NotTo(HaveOccurred())
			stagingActiveSha = strings.TrimSpace(stagingActiveSha)
			_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, activeCommitStatusStaging, func() error {
				activeCommitStatusStaging.Spec.Sha = stagingActiveSha
				activeCommitStatusStaging.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				return nil
			})
			Expect(err).To(Succeed())

			By("Verifying production sees the first commit A as proposed")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpProd.Name,
					Namespace: ctpProd.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())
				g.Expect(ctpProd.Status.Proposed.Dry.Sha).To(Equal(firstDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Now making second commit (B) that only affects staging")
			gitPath2, err := cloneTestRepo(ctx, name)
			Expect(err).NotTo(HaveOccurred())
			defer func() { _ = os.RemoveAll(gitPath2) }()

			secondDrySha, err := makeDryCommit(ctx, gitPath2, "second commit B - only affects staging")
			Expect(err).NotTo(HaveOccurred())

			By("Hydrating ONLY staging-next for commit B (dev and prod manifests unchanged)")
			// Only staging gets a new hydrated commit, dev and prod just get git notes
			err = hydrateEnvironment(ctx, gitPath2, "environment/staging-next", secondDrySha, "hydrate staging for B")
			Expect(err).NotTo(HaveOccurred())

			By("Adding git note to dev's existing hydrated commit for commit B (no new commit)")
			// Dev's manifests haven't changed for B, so just update the git note
			err = addNoteToEnvironment(ctx, gitPath2, testBranchDevelopmentNext, secondDrySha)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for dev CTP to pick up the git note for B")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpDev.Name,
					Namespace: ctpDev.Namespace,
				}, &ctpDev)
				g.Expect(err).To(Succeed())
				// Dev's Note.DrySha should be the second dry SHA (from git note)
				g.Expect(ctpDev.Status.Proposed.Note.DrySha).To(Equal(secondDrySha))
				// Dev's Proposed.Dry.Sha should still be the first dry SHA (no new commit was made)
				g.Expect(ctpDev.Status.Proposed.Dry.Sha).To(Equal(firstDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Waiting for staging to promote second commit B")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpStaging.Name,
					Namespace: ctpStaging.Namespace,
				}, &ctpStaging)
				g.Expect(err).To(Succeed())
				g.Expect(ctpStaging.Status.Active.Dry.Sha).To(Equal(secondDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			// Set staging's commit status to success for B
			_, err = runGitCmd(ctx, gitPath2, "fetch")
			Expect(err).NotTo(HaveOccurred())
			stagingActiveSha2, err := runGitCmd(ctx, gitPath2, "rev-parse", "origin/"+ctpStaging.Spec.ActiveBranch)
			Expect(err).NotTo(HaveOccurred())
			stagingActiveSha2 = strings.TrimSpace(stagingActiveSha2)
			_, err = controllerutil.CreateOrUpdate(ctx, k8sClient, activeCommitStatusStaging, func() error {
				activeCommitStatusStaging.Spec.Sha = stagingActiveSha2
				activeCommitStatusStaging.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				return nil
			})
			Expect(err).To(Succeed())

			By("Adding git note to production's existing hydrated commit for commit B (no new commit)")
			// This simulates the hydrator saying "production's manifests haven't changed for B"
			err = addNoteToEnvironment(ctx, gitPath2, "environment/production-next", secondDrySha)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for production CTP to pick up the git note")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpProd.Name,
					Namespace: ctpProd.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())
				// Production's Note.DrySha should be the second dry SHA (from git note)
				g.Expect(ctpProd.Status.Proposed.Note.DrySha).To(Equal(secondDrySha))
				// Production's Proposed.Dry.Sha should still be the first dry SHA (no new commit was made)
				g.Expect(ctpProd.Status.Proposed.Dry.Sha).To(Equal(firstDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying production's previous environment check passes (staging is ahead with matching Note.DrySha)")
			Eventually(func(g Gomega) {
				var prevEnvCS promoterv1alpha1.CommitStatus
				csName := utils.KubeSafeUniqueName(ctx, promoterv1alpha1.PreviousEnvProposedCommitPrefixNameLabel+ctpProd.Name)
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      csName,
					Namespace: "default",
				}, &prevEnvCS)
				g.Expect(err).To(Succeed())
				g.Expect(prevEnvCS.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying production promotes the first dry SHA (A)")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      ctpProd.Name,
					Namespace: ctpProd.Namespace,
				}, &ctpProd)
				g.Expect(err).To(Succeed())
				g.Expect(ctpProd.Status.Active.Dry.Sha).To(Equal(firstDrySha))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("isPreviousEnvironmentPending", func() {
		// Use fixed times for tests to ensure consistent time comparisons
		olderTime := metav1.NewTime(time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC))
		newerTime := metav1.NewTime(time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC))

		// Helper to create a HydratorMetadata pointer, or nil if empty
		makeNote := func(drySha string) *promoterv1alpha1.HydratorMetadata {
			if drySha == "" {
				return nil
			}
			return &promoterv1alpha1.HydratorMetadata{DrySha: drySha}
		}

		// Helper to create environment status with specific values
		makeEnvStatusWithTime := func(activeDrySha, proposedDrySha, noteDrySha string, activeTime metav1.Time) promoterv1alpha1.EnvironmentStatus {
			return promoterv1alpha1.EnvironmentStatus{
				Active: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        activeDrySha,
						CommitTime: activeTime,
					},
					CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						{Key: "health", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
					},
				},
				Proposed: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        proposedDrySha,
						CommitTime: olderTime, // Set proposed commit time to olderTime by default
					},
					Note: makeNote(noteDrySha),
				},
			}
		}

		// Helper that uses the older time by default (for backward compatibility)
		makeEnvStatus := func(activeDrySha, proposedDrySha, noteSha string) promoterv1alpha1.EnvironmentStatus {
			return makeEnvStatusWithTime(activeDrySha, proposedDrySha, noteSha, olderTime)
		}

		DescribeTable("should correctly determine if previous environment is pending",
			func(prevActiveDry, prevProposedDry, prevNoteDry, currActiveDry, currProposedDry, currNoteSha string, expectPending bool, expectReasonContains string) {
				prevEnvStatus := makeEnvStatus(prevActiveDry, prevProposedDry, prevNoteDry)
				currEnvStatus := makeEnvStatus(currActiveDry, currProposedDry, currNoteSha)

				isPending, reason := isPreviousEnvironmentPending(prevEnvStatus, currEnvStatus)

				Expect(isPending).To(Equal(expectPending), "isPending mismatch")
				if expectReasonContains != "" {
					Expect(reason).To(ContainSubstring(expectReasonContains), "reason mismatch")
				}
			},
			// Scenario 1: Out-of-order hydration - staging hydrates before dev
			// Dev hasn't hydrated yet (note and proposed still show OLD)
			Entry("blocks when previous env hasn't hydrated yet (with git notes)",
				"OLD", "OLD", "OLD", // prev: active=OLD, proposed=OLD, note=OLD
				"OLD", "ABC", "ABC", // curr: active=OLD, proposed=ABC, note=ABC
				true, "Waiting for the hydrator to finish processing the proposed dry commit"),

			// Scenario 2: Normal flow - dev has hydrated and merged
			Entry("allows when previous env has merged the proposed dry SHA",
				"ABC", "ABC", "ABC", // prev: active=ABC, proposed=ABC, note=ABC
				"OLD", "ABC", "ABC", // curr: active=OLD, proposed=ABC, note=ABC
				false, ""),

			// Scenario 3: Git note - dev has no manifest changes
			// Dev's hydrator updated the note but didn't create a new commit
			Entry("allows when previous env has no changes to merge (git note)",
				"OLD", "OLD", "ABC", // prev: active=OLD, proposed=OLD, note=ABC (note updated, no new commit)
				"OLD", "ABC", "ABC", // curr: active=OLD, proposed=ABC, note=ABC
				false, ""),

			// Scenario 4: Legacy hydrator (no git notes) - dev hasn't hydrated
			Entry("blocks when previous env hasn't hydrated (no git notes)",
				"OLD", "OLD", "", // prev: active=OLD, proposed=OLD, note="" (empty)
				"OLD", "ABC", "", // curr: active=OLD, proposed=ABC, note="" (empty for legacy)
				true, "Waiting for the hydrator to finish processing the proposed dry commit"),

			// Scenario 5: Legacy hydrator - dev has hydrated but not merged
			Entry("blocks when previous env has hydrated but not merged (no git notes)",
				"OLD", "ABC", "", // prev: active=OLD, proposed=ABC, note="" (empty)
				"OLD", "ABC", "", // curr: active=OLD, proposed=ABC, note="" (empty for legacy)
				true, "Waiting for previous environment to be promoted"),

			// Scenario 6: Legacy hydrator - dev has hydrated and merged
			Entry("allows when previous env has merged (no git notes)",
				"ABC", "ABC", "", // prev: active=ABC, proposed=ABC, note="" (empty)
				"OLD", "ABC", "", // curr: active=OLD, proposed=ABC, note="" (empty for legacy)
				false, ""),

			// Scenario 7: Dev hydrated but not yet merged (with git notes)
			Entry("blocks when previous env has hydrated but not merged (with git notes)",
				"OLD", "ABC", "ABC", // prev: active=OLD, proposed=ABC, note=ABC
				"OLD", "ABC", "ABC", // curr: active=OLD, proposed=ABC, note=ABC
				true, "Waiting for previous environment to be promoted"),

			// Scenario 8: Mismatch between note and proposed (edge case)
			// Note shows newer SHA than proposed (hydrator updated note for even newer commit)
			Entry("blocks when note shows different SHA than what we're promoting",
				"OLD", "OLD", "DEF", // prev: active=OLD, proposed=OLD, note=DEF (different!)
				"OLD", "ABC", "ABC", // curr: active=OLD, proposed=ABC, note=ABC
				true, "Waiting for the hydrator to finish processing the proposed dry commit"),
		)

		// Test for when previous environment has already moved past the proposed dry SHA
		// AND both environments have the same Note.DrySha (confirming they've seen the same dry commits)
		//
		// Example scenario:
		// 1. Dry commit ABC is made (commitTime: 10:00)
		// 2. All environments get hydrated for ABC
		// 3. Before production merges ABC, someone makes dry commit DEF (commitTime: 10:05)
		// 4. Staging hydrates and merges DEF
		// 5. Production is still trying to promote ABC, but staging is ahead
		// 6. Both have Note.DrySha = DEF (both hydrated up to the latest), so allow promotion
		It("allows when previous env has already merged a newer commit with matching Note.DrySha", func() {
			// Previous env (staging) has merged a newer commit (DEF at newerTime)
			prevEnvStatus := makeEnvStatusWithTime("DEF", "DEF", "DEF", newerTime)
			// Current env (production) is trying to promote an older commit (ABC at olderTime)
			// Both have Note.DrySha = "DEF", meaning they've both been hydrated up to the same point
			currEnvStatus := promoterv1alpha1.EnvironmentStatus{
				Active: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "OLD",
						CommitTime: olderTime,
					},
					CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						{Key: "health", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
					},
				},
				Proposed: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "ABC",
						CommitTime: olderTime, // ABC was made before DEF
					},
					Note: &promoterv1alpha1.HydratorMetadata{
						DrySha: "DEF", // But hydrator has processed up to DEF
					},
				},
			}

			isPending, reason := isPreviousEnvironmentPending(prevEnvStatus, currEnvStatus)

			Expect(isPending).To(BeFalse(), "should allow promotion when previous env is ahead and Note.DrySha matches")
			Expect(reason).To(BeEmpty())
		})

		It("blocks when Note.DrySha doesn't match between environments", func() {
			// Previous env (staging) has Note.DrySha "XYZ" while production's is "ABC"
			// This means they haven't been hydrated for the same dry commits
			prevEnvStatus := makeEnvStatusWithTime("DEF", "DEF", "XYZ", newerTime)
			currEnvStatus := promoterv1alpha1.EnvironmentStatus{
				Active: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "OLD",
						CommitTime: olderTime,
					},
					CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						{Key: "health", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
					},
				},
				Proposed: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "ABC",
						CommitTime: olderTime,
					},
					Note: &promoterv1alpha1.HydratorMetadata{
						DrySha: "ABC", // Different from staging's XYZ
					},
				},
			}

			isPending, reason := isPreviousEnvironmentPending(prevEnvStatus, currEnvStatus)

			Expect(isPending).To(BeTrue(), "should block when Note.DrySha doesn't match")
			Expect(reason).To(ContainSubstring("hydrator to finish processing"))
		})

		// Test for legacy hydrators (no git notes) when previous env is ahead
		It("allows when previous env is ahead with matching Proposed.Dry.Sha (legacy hydrator)", func() {
			// Previous env (staging) has merged a newer commit (DEF at newerTime)
			// Both environments use legacy hydrator (no Note.DrySha), so we compare Proposed.Dry.Sha
			prevEnvStatus := promoterv1alpha1.EnvironmentStatus{
				Active: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "DEF",
						CommitTime: newerTime,
					},
					CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						{Key: "health", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
					},
				},
				Proposed: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha: "DEF",
					},
					// Note.DrySha is empty (legacy hydrator, no git notes)
				},
			}
			currEnvStatus := promoterv1alpha1.EnvironmentStatus{
				Active: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "OLD",
						CommitTime: olderTime,
					},
					CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						{Key: "health", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
					},
				},
				Proposed: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "DEF", // Same as staging's Proposed.Dry.Sha
						CommitTime: olderTime,
					},
					// Note.DrySha is empty (legacy hydrator, no git notes)
				},
			}

			isPending, reason := isPreviousEnvironmentPending(prevEnvStatus, currEnvStatus)

			Expect(isPending).To(BeFalse(), "should allow promotion when previous env is ahead and Proposed.Dry.Sha matches (legacy)")
			Expect(reason).To(BeEmpty())
		})

		It("blocks when previous env is ahead but commit statuses are not passing", func() {
			// Previous env has merged a newer commit but health check is pending
			prevEnvStatus := promoterv1alpha1.EnvironmentStatus{
				Active: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "DEF",
						CommitTime: newerTime,
					},
					CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						{Key: "health", Phase: string(promoterv1alpha1.CommitPhasePending)},
					},
				},
				Proposed: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha: "DEF",
					},
					Note: &promoterv1alpha1.HydratorMetadata{
						DrySha: "DEF",
					},
				},
			}
			currEnvStatus := promoterv1alpha1.EnvironmentStatus{
				Active: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "OLD",
						CommitTime: olderTime,
					},
					CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						{Key: "health", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
					},
				},
				Proposed: promoterv1alpha1.CommitBranchState{
					Dry: promoterv1alpha1.CommitShaState{
						Sha:        "ABC",
						CommitTime: olderTime,
					},
					Note: &promoterv1alpha1.HydratorMetadata{
						DrySha: "DEF", // Note.DrySha matches staging
					},
				},
			}

			isPending, reason := isPreviousEnvironmentPending(prevEnvStatus, currEnvStatus)

			Expect(isPending).To(BeTrue(), "should block when previous env commit statuses are not passing")
			Expect(reason).To(ContainSubstring("commit status"))
		})
	})

	// Note: Each test creates its own reconciler and state instead of using shared BeforeEach setup.
	// This ensures complete test isolation because enqueueOutOfSyncCTPs schedules background
	// timers (time.AfterFunc) that may fire during other tests. With isolated state per test,
	// background timers from one test cannot contaminate another test's assertions.
	//
	// Mutex locking pattern: After each call to enqueueOutOfSyncCTPs, tests acquire the lock,
	// read enqueuedCTPs, then immediately release the lock before calling enqueueOutOfSyncCTPs
	// again. This fine-grained locking is required because background timer goroutines need to
	// acquire the lock to append to enqueuedCTPs during time.Sleep() calls. Using defer to hold
	// the lock for an entire test would cause deadlock: the test would wait for timers to fire,
	// but timers would block waiting for the lock that won't release until the test completes.
	Context("Rate limiting for enqueueOutOfSyncCTPs", func() {
		// Helper to create out-of-sync CTP that will trigger rate limiting.
		// Creates a CTP where the git note SHA differs from the target SHA.
		makeCTP := func(name string) *promoterv1alpha1.ChangeTransferPolicy {
			return &promoterv1alpha1.ChangeTransferPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "test-ns",
				},
				Status: promoterv1alpha1.ChangeTransferPolicyStatus{
					Proposed: promoterv1alpha1.CommitBranchState{
						Dry: promoterv1alpha1.CommitShaState{
							Sha: "abc123", // Proposed dry SHA (becomes target since newest)
						},
						Hydrated: promoterv1alpha1.CommitShaState{
							CommitTime: metav1.Now(),
						},
						Note: &promoterv1alpha1.HydratorMetadata{
							DrySha: "old123", // Git note SHA (out-of-sync with target)
						},
					},
				},
			}
		}

		// Helper to create reconciler with enqueue tracking
		makeReconciler := func() (*PromotionStrategyReconciler, *[]client.ObjectKey, *sync.Mutex) {
			enqueuedCTPs := &[]client.ObjectKey{}
			mutex := &sync.Mutex{}

			reconciler := &PromotionStrategyReconciler{
				EnqueueCTP: func(namespace, name string) {
					mutex.Lock()
					defer mutex.Unlock()
					*enqueuedCTPs = append(*enqueuedCTPs, client.ObjectKey{Namespace: namespace, Name: name})
				},
			}

			return reconciler, enqueuedCTPs, mutex
		}

		It("should enqueue CTP on first call", func() {
			reconciler, enqueuedCTPs, enqueueMutex := makeReconciler()

			ctx := context.Background()
			ctps := []*promoterv1alpha1.ChangeTransferPolicy{
				makeCTP("test-ctp"),
			}

			reconciler.enqueueOutOfSyncCTPs(ctx, ctps)

			enqueueMutex.Lock()
			Expect(*enqueuedCTPs).To(HaveLen(1))
			Expect((*enqueuedCTPs)[0].Name).To(Equal("test-ctp"))
			Expect((*enqueuedCTPs)[0].Namespace).To(Equal("test-ns"))
			enqueueMutex.Unlock()
		})

		It("should rate limit second call within threshold", func() {
			reconciler, enqueuedCTPs, enqueueMutex := makeReconciler()

			ctx := context.Background()
			ctps := []*promoterv1alpha1.ChangeTransferPolicy{
				makeCTP("test-ctp"),
			}

			// First call
			reconciler.enqueueOutOfSyncCTPs(ctx, ctps)
			enqueueMutex.Lock()
			firstCallCount := len(*enqueuedCTPs)
			enqueueMutex.Unlock()
			Expect(firstCallCount).To(Equal(1))

			// Second call immediately after (within 15s threshold)
			time.Sleep(100 * time.Millisecond)
			reconciler.enqueueOutOfSyncCTPs(ctx, ctps)
			enqueueMutex.Lock()
			secondCallCount := len(*enqueuedCTPs)
			enqueueMutex.Unlock()

			// Should still be 1 - rate limited (delayed enqueue scheduled for later)
			Expect(secondCallCount).To(Equal(1), "second call should be rate limited")
		})

		It("should schedule delayed enqueue on rate limited call", func() {
			reconciler, enqueuedCTPs, enqueueMutex := makeReconciler()

			ctx := context.Background()
			ctps := []*promoterv1alpha1.ChangeTransferPolicy{
				makeCTP("test-ctp"),
			}

			// First call
			reconciler.enqueueOutOfSyncCTPs(ctx, ctps)
			enqueueMutex.Lock()
			firstCount := len(*enqueuedCTPs)
			enqueueMutex.Unlock()
			Expect(firstCount).To(Equal(1))

			// Second call - should be rate limited and schedule delayed enqueue
			reconciler.enqueueOutOfSyncCTPs(ctx, ctps)

			// Wait for delayed enqueue to fire (15s + small buffer)
			time.Sleep(16 * time.Second)

			enqueueMutex.Lock()
			finalCount := len(*enqueuedCTPs)
			enqueueMutex.Unlock()

			// Should now be 2 - original + delayed
			Expect(finalCount).To(Equal(2), "delayed enqueue should have fired")
		})

		It("should not accumulate multiple delayed enqueues", func() {
			reconciler, enqueuedCTPs, enqueueMutex := makeReconciler()

			ctx := context.Background()
			ctps := []*promoterv1alpha1.ChangeTransferPolicy{
				makeCTP("test-ctp"),
			}

			// First call
			reconciler.enqueueOutOfSyncCTPs(ctx, ctps)

			// Multiple rapid calls - should only schedule ONE delayed enqueue
			for i := 0; i < 5; i++ {
				time.Sleep(100 * time.Millisecond)
				reconciler.enqueueOutOfSyncCTPs(ctx, ctps)
			}

			// Wait for delayed enqueue to fire
			time.Sleep(16 * time.Second)

			enqueueMutex.Lock()
			finalCount := len(*enqueuedCTPs)
			enqueueMutex.Unlock()

			// Should be 2, not 6 (original + one delayed, not 5 delayed)
			Expect(finalCount).To(Equal(2), "should only have one delayed enqueue, not accumulate")
		})

		It("should rate limit multiple CTPs independently", func() {
			reconciler, enqueuedCTPs, enqueueMutex := makeReconciler()

			ctx := context.Background()
			ctps := []*promoterv1alpha1.ChangeTransferPolicy{
				makeCTP("ctp-1"),
				makeCTP("ctp-2"),
			}

			// First call - both should enqueue
			reconciler.enqueueOutOfSyncCTPs(ctx, ctps)
			enqueueMutex.Lock()
			firstCount := len(*enqueuedCTPs)
			enqueueMutex.Unlock()
			Expect(firstCount).To(Equal(2))

			// Second call immediately - both should be rate limited
			reconciler.enqueueOutOfSyncCTPs(ctx, ctps)
			enqueueMutex.Lock()
			secondCount := len(*enqueuedCTPs)
			enqueueMutex.Unlock()
			Expect(secondCount).To(Equal(2), "both should be rate limited")

			// Wait for delayed enqueues
			time.Sleep(16 * time.Second)

			enqueueMutex.Lock()
			finalCount := len(*enqueuedCTPs)
			enqueueMutex.Unlock()
			Expect(finalCount).To(Equal(4), "both delayed enqueues should fire")
		})

		It("should rate limit one CTP while allowing others through", func() {
			reconciler, enqueuedCTPs, enqueueMutex := makeReconciler()

			ctx := context.Background()
			ctp1 := makeCTP("ctp-1")
			ctp2 := makeCTP("ctp-2")

			// First call - enqueue ctp-1 only
			reconciler.enqueueOutOfSyncCTPs(ctx, []*promoterv1alpha1.ChangeTransferPolicy{ctp1})
			enqueueMutex.Lock()
			firstCount := len(*enqueuedCTPs)
			enqueueMutex.Unlock()
			Expect(firstCount).To(Equal(1), "ctp-1 should enqueue")

			// Immediately call again with both CTPs
			// ctp-1 should be rate limited, ctp-2 should enqueue (first time)
			time.Sleep(100 * time.Millisecond)
			reconciler.enqueueOutOfSyncCTPs(ctx, []*promoterv1alpha1.ChangeTransferPolicy{ctp1, ctp2})

			enqueueMutex.Lock()
			secondCount := len(*enqueuedCTPs)
			lastEnqueuedName := (*enqueuedCTPs)[len(*enqueuedCTPs)-1].Name
			enqueueMutex.Unlock()

			// Should be 2 total now (ctp-1 from first call, ctp-2 from second call)
			// ctp-1 was rate limited in the second call
			Expect(secondCount).To(Equal(2), "only ctp-2 should have enqueued in second call")
			Expect(lastEnqueuedName).To(Equal("ctp-2"), "ctp-2 should be the last enqueued")
		})
	})
})
