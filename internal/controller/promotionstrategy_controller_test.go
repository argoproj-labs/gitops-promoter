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
	"time"

	"k8s.io/apimachinery/pkg/api/errors"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("PromotionStrategy Controller", func() {

	Context("When reconciling a resource with no commit statuses", func() {
		ctx := context.Background()

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")

			name, scmSecret, scmProvider, _, promotionStrategy := promotionStrategyResource(ctx, "promotion-strategy-no-commit-status", "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// Check that ProposedCommit are created
			proposedCommitDev := promoterv1alpha1.ProposedCommit{}
			proposedCommitStaging := promoterv1alpha1.ProposedCommit{}
			proposedCommitProd := promoterv1alpha1.ProposedCommit{}

			pullRequestDev := promoterv1alpha1.PullRequest{}
			pullRequestStaging := promoterv1alpha1.PullRequest{}
			pullRequestProd := promoterv1alpha1.PullRequest{}

			// Check that ProposedCommit are created
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, typeNamespacedName, promotionStrategy)

				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetProposedCommitName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &proposedCommitDev)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetProposedCommitName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &proposedCommitStaging)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetProposedCommitName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &proposedCommitProd)
				g.Expect(err).To(Succeed())

				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, proposedCommitDev))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, proposedCommitStaging))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, proposedCommitProd))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				//By("Checking that all the ProposedCommits are created")
				g.Expect(len(promotionStrategy.Status.Environments)).To(Equal(3))

				//By("Checking that the ProposedCommit for development has shas that are not empty, meaning we have reconciled the resource")
				g.Expect(proposedCommitDev.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitDev.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitDev.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitDev.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				//By("Checking that the ProposedCommit for staging has shas that are not empty, meaning we have reconciled the resource")
				g.Expect(proposedCommitStaging.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitStaging.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitStaging.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitStaging.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				//By("Checking that the ProposedCommit for production has shas that are not empty, meaning we have reconciled the resource")
				g.Expect(proposedCommitProd.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitProd.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitProd.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitProd.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				//By("Checking that the PromotionStrategy for development environment has the correct sha values from the ProposedCommit")
				g.Expect(proposedCommitDev.Spec.ActiveBranch).To(Equal("environment/development"))
				g.Expect(proposedCommitDev.Spec.ProposedBranch).To(Equal("environment/development-next"))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.Sha).To(Equal(proposedCommitDev.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).To(Equal(proposedCommitDev.Status.Active.Hydrated.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Active.CommitStatus.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Dry.Sha).To(Equal(proposedCommitDev.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).To(Equal(proposedCommitDev.Status.Proposed.Hydrated.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.CommitStatus.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

				//By("Checking that the PromotionStrategy for staging environment has the correct sha values from the ProposedCommit")
				g.Expect(proposedCommitStaging.Spec.ActiveBranch).To(Equal("environment/staging"))
				g.Expect(proposedCommitStaging.Spec.ProposedBranch).To(Equal("environment/staging-next"))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Sha).To(Equal(proposedCommitStaging.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Sha).To(Equal(proposedCommitStaging.Status.Active.Hydrated.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Active.CommitStatus.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.Dry.Sha).To(Equal(proposedCommitStaging.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.Hydrated.Sha).To(Equal(proposedCommitStaging.Status.Proposed.Hydrated.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.CommitStatus.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

				//By("Checking that the PromotionStrategy for production environment has the correct sha values from the ProposedCommit")
				g.Expect(proposedCommitProd.Spec.ActiveBranch).To(Equal("environment/production"))
				g.Expect(proposedCommitProd.Spec.ProposedBranch).To(Equal("environment/production-next"))
				g.Expect(promotionStrategy.Status.Environments[2].Active.Dry.Sha).To(Equal(proposedCommitProd.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Active.Hydrated.Sha).To(Equal(proposedCommitProd.Status.Active.Hydrated.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Active.CommitStatus.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.Dry.Sha).To(Equal(proposedCommitProd.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.Hydrated.Sha).To(Equal(proposedCommitProd.Status.Proposed.Hydrated.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.CommitStatus.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

				time.Sleep(30 * time.Second)
			}, EventuallyTimeout).Should(Succeed())

			By("Adding a pending commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			makeChangeAndHydrateRepo(gitPath, name, name)

			// We should now get PRs created for the ProposedCommits
			Eventually(func(g Gomega) {
				// Dev PR should exist
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, proposedCommitDev))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(Succeed())

				// Dev PR should not exists because we closed it
				//prName = utils.KubeSafeUniqueName(ctx, fmt.Sprintf("%s-%s-%s-%s", proposedCommitDev.Spec.RepositoryReference.Name, proposedCommitDev.Spec.RepositoryReference.Owner, proposedCommitDev.Spec.ProposedBranch, proposedCommitDev.Spec.ActiveBranch))
				//err = k8sClient.Get(ctx, types.NamespacedName{
				//	Name:      prName,
				//	Namespace: typeNamespacedName.Namespace,
				//}, &pullRequestDev)
				//g.Expect(err).To(Not(BeNil()))
				//g.Expect(errors.IsNotFound(err)).To(BeTrue())

				//prName = utils.KubeSafeUniqueName(ctx, fmt.Sprintf("%s-%s-%s-%s", proposedCommitStaging.Spec.RepositoryReference.Name, proposedCommitStaging.Spec.RepositoryReference.Owner, proposedCommitStaging.Spec.ProposedBranch, proposedCommitStaging.Spec.ActiveBranch))
				//err = k8sClient.Get(ctx, types.NamespacedName{
				//	Name:      prName,
				//	Namespace: typeNamespacedName.Namespace,
				//}, &pullRequestStaging)
				//g.Expect(err).To(Succeed())

				//prName = utils.KubeSafeUniqueName(ctx, fmt.Sprintf("%s-%s-%s-%s", proposedCommitProd.Spec.RepositoryReference.Name, proposedCommitProd.Spec.RepositoryReference.Owner, proposedCommitProd.Spec.ProposedBranch, proposedCommitProd.Spec.ActiveBranch))
				//err = k8sClient.Get(ctx, types.NamespacedName{
				//	Name:      prName,
				//	Namespace: typeNamespacedName.Namespace,
				//}, &pullRequestProd)
				//g.Expect(err).To(Succeed())

			}, EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				// The PRs should eventually close because of no commit status checks configured
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, proposedCommitDev))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				//g.Expect(err.Error()).To(ContainSubstring("pullrequests.promoter.argoproj.io \"" + prName + "\" not found"))

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, proposedCommitStaging))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
				//
				////g.Expect(err.Error()).To(ContainSubstring("pullrequests.promoter.argoproj.io \"" + prName + "\" not found"))
				//
				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, proposedCommitProd))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
				//
				////g.Expect(err.Error()).To(ContainSubstring("pullrequests.promoter.argoproj.io \"" + prName + "\" not found"))

			}, EventuallyTimeout).Should(Succeed())

			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
		})
	})

	Context("When reconciling a resource with a commit statuses", func() {
		It("should successfully reconcile the resource", func() {
			//Skip("Skipping test because of flakiness")
			By("Reconciling the created resource")
			name, scmSecret, scmProvider, commitStatus, promotionStrategy := promotionStrategyResource(ctx, "promotion-strategy-with-commit-status", "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{
					Key: "health-check",
				},
			}
			commitStatus.Spec.Name = "health-check"
			commitStatus.Labels = map[string]string{
				promoterv1alpha1.CommitStatusLabel: "health-check",
			}

			By("Adding a pending commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())
			makeChangeAndHydrateRepo(gitPath, name, name)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, commitStatus)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// We should now get PRs created for the ProposedCommits
			// Check that ProposedCommit are created
			proposedCommitDev := promoterv1alpha1.ProposedCommit{}
			proposedCommitStaging := promoterv1alpha1.ProposedCommit{}
			proposedCommitProd := promoterv1alpha1.ProposedCommit{}

			pullRequestDev := promoterv1alpha1.PullRequest{}
			pullRequestStaging := promoterv1alpha1.PullRequest{}
			pullRequestProd := promoterv1alpha1.PullRequest{}
			Eventually(func(g Gomega) {
				// Make sure proposed commits are created and the associated PRs
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetProposedCommitName(promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &proposedCommitDev)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetProposedCommitName(promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &proposedCommitStaging)
				g.Expect(err).To(Succeed())

				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, utils.GetProposedCommitName(promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &proposedCommitProd)
				g.Expect(err).To(Succeed())

				// Dev PR should be closed because it is the lowest level environment
				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, proposedCommitDev))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestDev)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, proposedCommitStaging))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Succeed())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, proposedCommitProd))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())

			}, EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {

				err := k8sClient.Get(ctx, typeNamespacedName, commitStatus)
				g.Expect(err).To(Succeed())

				_, err = runGitCmd(gitPath, "git", "fetch")
				Expect(err).NotTo(HaveOccurred())
				sha, err := runGitCmd(gitPath, "git", "rev-parse", "origin/"+proposedCommitDev.Spec.ActiveBranch)
				Expect(err).NotTo(HaveOccurred())
				sha = strings.TrimSpace(sha)

				commitStatus.Spec.Sha = sha
				commitStatus.Spec.Phase = "success"
				err = k8sClient.Update(ctx, commitStatus)
				g.Expect(err).To(Succeed())

				prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, proposedCommitStaging))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestStaging)
				g.Expect(err).To(Not(BeNil()))
				g.Expect(errors.IsNotFound(err)).To(BeTrue())

				prName = utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, proposedCommitProd))
				err = k8sClient.Get(ctx, types.NamespacedName{
					Name:      prName,
					Namespace: typeNamespacedName.Namespace,
				}, &pullRequestProd)
				g.Expect(err).To(Succeed())

			}, EventuallyTimeout).Should(Succeed())

		})
	})
})

func promotionStrategyResource(ctx context.Context, name, namespace string) (string, *v1.Secret, *promoterv1alpha1.ScmProvider, *promoterv1alpha1.CommitStatus, *promoterv1alpha1.PromotionStrategy) {
	name = name + "-" + utils.KubeSafeUniqueName(ctx, randomString(15))
	setupInitialTestGitRepoOnServer(name, name)

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

	commitStatus := &promoterv1alpha1.CommitStatus{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: &promoterv1alpha1.Repository{
				Owner: name,
				Name:  name,
				ScmProviderRef: promoterv1alpha1.NamespacedObjectReference{
					Name:      name,
					Namespace: namespace,
				},
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
			DryBanch: "main",
			RepositoryReference: &promoterv1alpha1.Repository{
				Owner: name,
				Name:  name,
				ScmProviderRef: promoterv1alpha1.NamespacedObjectReference{
					Name:      name,
					Namespace: namespace,
				},
			},
			Environments: []promoterv1alpha1.Environment{
				{Branch: "environment/development"},
				{Branch: "environment/staging"},
				{Branch: "environment/production"},
			},
		},
	}

	return name, scmSecret, scmProvider, commitStatus, promotionStrategy
}
