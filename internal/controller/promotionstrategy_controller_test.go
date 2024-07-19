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
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("PromotionStrategy Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource-promotion-strategy"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		promotionstrategy := &promoterv1alpha1.PromotionStrategy{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind PromotionStrategy")

			setupInitialTestGitRepo("test-ps", "test-ps")

			err := k8sClient.Get(ctx, typeNamespacedName, promotionstrategy)
			if err != nil && errors.IsNotFound(err) {
				resource := &promoterv1alpha1.PromotionStrategy{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: promoterv1alpha1.PromotionStrategySpec{
						DryBanch: "main",
						RepositoryReference: &promoterv1alpha1.Repository{
							Owner: "test-ps",
							Name:  "test-ps",
							ScmProviderRef: promoterv1alpha1.NamespacedObjectReference{
								Name:      resourceName,
								Namespace: "default",
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
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())

				Expect(k8sClient.Create(ctx, &promoterv1alpha1.ScmProvider{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: promoterv1alpha1.ScmProviderSpec{
						SecretRef: &v1.LocalObjectReference{Name: resourceName},
						Fake:      &promoterv1alpha1.Fake{},
					},
					Status: promoterv1alpha1.ScmProviderStatus{},
				})).To(Succeed())

				Expect(k8sClient.Create(ctx, &v1.Secret{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Data: nil,
				})).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &promoterv1alpha1.PromotionStrategy{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			scmProvider := &promoterv1alpha1.ScmProvider{}
			err = k8sClient.Get(ctx, typeNamespacedName, scmProvider)
			Expect(err).NotTo(HaveOccurred())

			secret := &v1.Secret{}
			err = k8sClient.Get(ctx, typeNamespacedName, secret)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance PromotionStrategy")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())

			deleteRepo("test-ps", "test-ps")
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &PromotionStrategyReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})
