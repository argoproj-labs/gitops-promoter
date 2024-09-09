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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("PullRequest Controller", func() {
	var scmProvider *promoterv1alpha1.ScmProvider
	var scmSecret *v1.Secret
	var pullRequest *promoterv1alpha1.PullRequest

	Context("When reconciling a resource", func() {
		const resourceName = "test-resource-pr"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		BeforeEach(func() {
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
					SecretRef: &v1.LocalObjectReference{Name: typeNamespacedName.Name},
					Fake:      &promoterv1alpha1.Fake{},
				},
				Status: promoterv1alpha1.ScmProviderStatus{},
			}

			pullRequest = &promoterv1alpha1.PullRequest{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      typeNamespacedName.Name,
					Namespace: typeNamespacedName.Namespace,
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
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			By("Cleanup the specific resource instance ProposedCommit")
			k8sClient.Delete(ctx, pullRequest)
			Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
		})
		It("should successfully reconcile the resource when updating title then merging", func() {
			By("Reconciling the created resource")
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

			Eventually(func(g Gomega) {
				Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				g.Expect(pullRequest.Status.SpecHash).To(Equal("e38edf1eb9ba75fe755968551d9845ba64bc8e24"))
			})

			By("Reconciling updating of the PullRequest")
			pullRequest.Spec.Title = "Updated Title"
			Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())

			Eventually(func(g Gomega) {
				Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Spec.Title).To(Equal("Updated Title"))
			})

			By("Reconciling merging of the PullRequest")
			pullRequest.Spec.State = "merged"
			Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("pullrequests.promoter.argoproj.io \"test-resource-pr\" not found"))
			})
		})
		It("should successfully reconcile the resource when closing", func() {
			By("Reconciling the created resource")
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

			Eventually(func() map[string]string {
				Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				return map[string]string{
					"statusState": string(pullRequest.Status.State),
					"specHash":    pullRequest.Status.SpecHash,
				}
			}, "5s").Should(Equal(map[string]string{
				"statusState": string(promoterv1alpha1.PullRequestOpen),
				"specHash":    "e38edf1eb9ba75fe755968551d9845ba64bc8e24",
			}))

			By("Reconciling closing of the PullRequest")
			pullRequest.Spec.State = "closed"
			Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())

			Eventually(func() map[string]string {
				err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				if err != nil {
					return map[string]string{
						"error": err.Error(),
					}
				}
				return map[string]string{
					"error": "",
				}
			}, "5s").Should(Equal(map[string]string{
				"error": "pullrequests.promoter.argoproj.io \"test-resource-pr\" not found",
			}))

		})
	})
})
