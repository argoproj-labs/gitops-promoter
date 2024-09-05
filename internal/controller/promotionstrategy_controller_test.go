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
	v1 "k8s.io/api/core/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("PromotionStrategy Controller", func() {
	var scmProvider *promoterv1alpha1.ScmProvider
	var scmSecret *v1.Secret
	var promotionStrategy *promoterv1alpha1.PromotionStrategy

	Context("When reconciling a resource", func() {
		const resourceName = "test-resource-promotion-strategy"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}

		BeforeEach(func() {
			By("creating the custom resource for the Kind PromotionStrategy")

			setupInitialTestGitRepo("test-ps", "test-ps")

			scmSecret = &v1.Secret{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      typeNamespacedName.Name,
					Namespace: typeNamespacedName.Namespace,
				},
				Data: nil,
			}

			scmProvider = &promoterv1alpha1.ScmProvider{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      typeNamespacedName.Name,
					Namespace: typeNamespacedName.Namespace,
				},
				Spec: promoterv1alpha1.ScmProviderSpec{
					SecretRef: &v1.LocalObjectReference{Name: resourceName},
					Fake:      &promoterv1alpha1.Fake{},
				},
				Status: promoterv1alpha1.ScmProviderStatus{},
			}

			promotionStrategy = &promoterv1alpha1.PromotionStrategy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      typeNamespacedName.Name,
					Namespace: typeNamespacedName.Namespace,
				},
				Spec: promoterv1alpha1.PromotionStrategySpec{
					DryBanch: "main",
					RepositoryReference: &promoterv1alpha1.Repository{
						Owner: "test-ps",
						Name:  "test-ps",
						ScmProviderRef: promoterv1alpha1.NamespacedObjectReference{
							Name:      typeNamespacedName.Name,
							Namespace: typeNamespacedName.Namespace,
						},
					},
					Environments: []promoterv1alpha1.Environment{
						{Branch: "environment/development"},
						{Branch: "environment/staging"},
						{Branch: "environment/production"},
					},
				},
				// TODO(user): Specify other spec details if needed.
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.

			By("Cleanup the specific resource instance PromotionStrategy")
			k8sClient.Delete(ctx, promotionStrategy)
			Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())

			deleteRepo("test-ps", "test-ps")
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})
