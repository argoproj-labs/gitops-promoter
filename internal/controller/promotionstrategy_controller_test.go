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
	"fmt"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("PromotionStrategy Controller", func() {

	Context("When reconciling a resource", func() {
		ctx := context.Background()

		BeforeEach(func() {
		})

		AfterEach(func() {
		})

		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")

			name, scmSecret, scmProvider, _, promotionStrategy := promotionStrategyResource(ctx, "promotion-strategy-no-commit-status", "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default", // TODO(user):Modify as needed
			}
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.

			// Check that ProposedCommit are created
			proposedCommitDev := promoterv1alpha1.ProposedCommit{}
			proposedCommitStaging := promoterv1alpha1.ProposedCommit{}
			proposedCommitProd := promoterv1alpha1.ProposedCommit{}
			// Check that ProposedCommit are created
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, typeNamespacedName, promotionStrategy)

				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, fmt.Sprintf("%s-%s", promotionStrategy.Name, promotionStrategy.Spec.Environments[0].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &proposedCommitDev)

				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, fmt.Sprintf("%s-%s", promotionStrategy.Name, promotionStrategy.Spec.Environments[1].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &proposedCommitStaging)

				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, fmt.Sprintf("%s-%s", promotionStrategy.Name, promotionStrategy.Spec.Environments[2].Branch)),
					Namespace: typeNamespacedName.Namespace,
				}, &proposedCommitProd)

				if len(promotionStrategy.Status.Environments) > 3 {
					g.Expect(len(promotionStrategy.Status.Environments)).To(Equal(3))
				}
				g.Expect(len(promotionStrategy.Status.Environments)).To(Equal(3))
				g.Expect(promotionStrategy.Status.Environments[0].Active.CommitStatus.State).To(Equal(string(promoterv1alpha1.CommitStatusSuccess)))
				g.Expect(promotionStrategy.Status.Environments[1].Active.CommitStatus.State).To(Equal(string(promoterv1alpha1.CommitStatusSuccess)))
				g.Expect(promotionStrategy.Status.Environments[2].Active.CommitStatus.State).To(Equal(string(promoterv1alpha1.CommitStatusSuccess)))

				g.Expect(promotionStrategy.Status.Environments[0].Proposed.CommitStatus.State).To(Equal(string(promoterv1alpha1.CommitStatusSuccess)))
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.CommitStatus.State).To(Equal(string(promoterv1alpha1.CommitStatusSuccess)))
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.CommitStatus.State).To(Equal(string(promoterv1alpha1.CommitStatusSuccess)))

				g.Expect(proposedCommitDev.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitDev.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitDev.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitDev.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				g.Expect(proposedCommitStaging.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitStaging.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitStaging.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitStaging.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				g.Expect(proposedCommitProd.Status.Proposed.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitProd.Status.Active.Dry.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitProd.Status.Proposed.Hydrated.Sha).To(Not(BeEmpty()))
				g.Expect(proposedCommitProd.Status.Active.Hydrated.Sha).To(Not(BeEmpty()))

				g.Expect(proposedCommitDev.Spec.ActiveBranch).To(Equal("environment/development"))
				g.Expect(proposedCommitDev.Spec.ProposedBranch).To(Equal("environment/development-next"))

				g.Expect(promotionStrategy.Status.Environments[0].Active.Dry.Sha).To(Equal(proposedCommitDev.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Active.Hydrated.Sha).To(Equal(proposedCommitDev.Status.Active.Hydrated.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Dry.Sha).To(Equal(proposedCommitDev.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[0].Proposed.Hydrated.Sha).To(Equal(proposedCommitDev.Status.Proposed.Hydrated.Sha))

				g.Expect(proposedCommitStaging.Spec.ActiveBranch).To(Equal("environment/staging"))
				g.Expect(proposedCommitStaging.Spec.ProposedBranch).To(Equal("environment/staging-next"))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Dry.Sha).To(Equal(proposedCommitStaging.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Active.Hydrated.Sha).To(Equal(proposedCommitStaging.Status.Active.Hydrated.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.Dry.Sha).To(Equal(proposedCommitStaging.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[1].Proposed.Hydrated.Sha).To(Equal(proposedCommitStaging.Status.Proposed.Hydrated.Sha))

				g.Expect(proposedCommitProd.Spec.ActiveBranch).To(Equal("environment/production"))
				g.Expect(proposedCommitProd.Spec.ProposedBranch).To(Equal("environment/production-next"))
				g.Expect(promotionStrategy.Status.Environments[2].Active.Dry.Sha).To(Equal(proposedCommitProd.Status.Active.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Active.Hydrated.Sha).To(Equal(proposedCommitProd.Status.Active.Hydrated.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.Dry.Sha).To(Equal(proposedCommitProd.Status.Proposed.Dry.Sha))
				g.Expect(promotionStrategy.Status.Environments[2].Proposed.Hydrated.Sha).To(Equal(proposedCommitProd.Status.Proposed.Hydrated.Sha))

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
			Phase:       "pending",
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
