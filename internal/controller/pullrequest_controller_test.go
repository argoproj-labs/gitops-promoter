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
	controllerClient "sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj/promoter/api/v1alpha1"
)

var _ = Describe("PullRequest Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource-pr"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		pullrequest := &promoterv1alpha1.PullRequest{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind PullRequest")
			err := k8sClient.Get(ctx, typeNamespacedName, pullrequest)
			if err != nil && errors.IsNotFound(err) {
				resource := &promoterv1alpha1.PullRequest{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: promoterv1alpha1.PullRequestSpec{
						RepositoryReference: &promoterv1alpha1.Repository{
							Owner: "test",
							Name:  "test",
							ScmProviderRef: promoterv1alpha1.NamespacedObjectReference{
								Name:      resourceName,
								Namespace: "default",
							},
						},
						Title:        "test",
						TargetBranch: "test",
						SourceBranch: "test-next",
						Description:  "test",
						State:        "open",
					},
					Status: promoterv1alpha1.PullRequestStatus{},
				}

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

				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &promoterv1alpha1.PullRequest{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance PullRequest")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &PullRequestReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.

			var pr promoterv1alpha1.PullRequest
			Expect(k8sClient.Get(ctx, controllerClient.ObjectKey{
				Namespace: "default",
				Name:      resourceName,
			}, &pr)).To(Succeed())
			Expect(pr.Status.ID).To(Equal("1"))
			Expect(pr.Status.State).To(Equal("open"))
			Expect(pr.Status.SpecHash).To(Equal("2ac637b791376e069498c3c0c2d73ad13a11966f"))

			By("Reconciling updating of the PullRequest")
			pr.Spec.Title = "Updated Title"
			Expect(k8sClient.Update(ctx, &pr)).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, controllerClient.ObjectKey{
				Namespace: "default",
				Name:      resourceName,
			}, &pr)).To(Succeed())
			Expect(pr.Spec.Title).To(Equal("Updated Title"))

			By("Reconciling closing of the PullRequest")
			pr.Spec.State = "closed"
			Expect(k8sClient.Update(ctx, &pr)).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, controllerClient.ObjectKey{
				Namespace: "default",
				Name:      resourceName,
			}, &pr)).To(Succeed())
			Expect(pr.Status.State).To(Equal("closed"))

			By("Reconciling merging of the PullRequest")
			pr.Spec.State = "merged"
			Expect(k8sClient.Update(ctx, &pr)).To(Succeed())

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Get(ctx, controllerClient.ObjectKey{
				Namespace: "default",
				Name:      resourceName,
			}, &pr)).To(Succeed())
			Expect(pr.Status.State).To(Equal("merged"))

		})
	})
})
