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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/zachaller/promoter/api/v1alpha1"
)

var _ = Describe("ProposedCommit Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource-pc"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		proposedcommit := &promoterv1alpha1.ProposedCommit{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ProposedCommit")

			setupInitialTestGitRepo("test", "test")

			err := k8sClient.Get(ctx, typeNamespacedName, proposedcommit)
			if err != nil && errors.IsNotFound(err) {
				resource := &promoterv1alpha1.ProposedCommit{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: promoterv1alpha1.ProposedCommitSpec{
						RepositoryReference: &promoterv1alpha1.Repository{
							Owner: "test",
							Name:  "test",
							ScmProviderRef: promoterv1alpha1.NamespacedObjectReference{
								Name:      resourceName,
								Namespace: "default",
							},
						},
						ProposedBranch: "environment/development-next",
						ActiveBranch:   "environment/development",
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
			resource := &promoterv1alpha1.ProposedCommit{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			scmProvider := &promoterv1alpha1.ScmProvider{}
			err = k8sClient.Get(ctx, typeNamespacedName, scmProvider)
			Expect(err).NotTo(HaveOccurred())

			secret := &v1.Secret{}
			err = k8sClient.Get(ctx, typeNamespacedName, secret)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ProposedCommit")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
			Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).To(Succeed())
			deleteRepo("test", "test")
		})

		It("should successfully reconcile the resource - with no git change", func() {
			By("Reconciling the created resource")
			controllerReconciler := &ProposedCommitReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
		It("should successfully reconcile the resource - with a pending commit", func() {
			By("Adding a pending commit")
			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())

			addPendingCommit(gitPath, "5468b78dfef356739559abf1f883cd713794fd97")

			By("Reconciling the created resource")
			controllerReconciler := &ProposedCommitReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
