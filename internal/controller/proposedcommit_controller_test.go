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
	"os"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("ProposedCommit Controller", func() {

	var scmProvider *promoterv1alpha1.ScmProvider
	var scmSecret *v1.Secret
	var proposedCommit *promoterv1alpha1.ProposedCommit

	Context("When reconciling a resource", func() {
		const resourceName = "test-resource-pc"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ProposedCommit")

			setupInitialTestGitRepo("test-pc", "test-pc")

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

			proposedCommit = &promoterv1alpha1.ProposedCommit{
				ObjectMeta: metav1.ObjectMeta{
					Name:      typeNamespacedName.Name,
					Namespace: typeNamespacedName.Namespace,
				},
				Spec: promoterv1alpha1.ProposedCommitSpec{
					RepositoryReference: &promoterv1alpha1.Repository{
						Owner: "test-pc",
						Name:  "test-pc",
						ScmProviderRef: promoterv1alpha1.NamespacedObjectReference{
							Name:      resourceName,
							Namespace: "default",
						},
					},
				},
			}
		})

		AfterEach(func() {
			//TODO(user): Cleanup logic after each test, like removing the resource instance.
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(proposedCommit), proposedCommit)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, typeNamespacedName, scmProvider)
			Expect(err).NotTo(HaveOccurred())

			err = k8sClient.Get(ctx, typeNamespacedName, scmSecret)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ProposedCommit")
			Expect(k8sClient.Delete(ctx, proposedCommit)).To(Succeed())
			Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			deleteRepo("test-pc", "test-pc")
		})

		It("should successfully reconcile the resource - with a pending commit", func() {
			proposedCommit.Spec.ProposedBranch = "environment/development-next"
			proposedCommit.Spec.ActiveBranch = "environment/development"

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, proposedCommit)).To(Succeed())

			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())

			By("Adding a pending commit")
			addPendingCommit(gitPath, "5468b78dfef356739559abf1f883cd713794fd97", "test-pc", "test-pc")

			By("Reconciling the created resource")

			var proposedCommit promoterv1alpha1.ProposedCommit
			err = k8sClient.Get(ctx, typeNamespacedName, &proposedCommit)
			Expect(err).NotTo(HaveOccurred())

			// Update label to force reconcile
			proposedCommit.Labels = map[string]string{"test": "test"}
			err = k8sClient.Update(ctx, &proposedCommit)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() map[string]string {
				k8sClient.Get(ctx, typeNamespacedName, &proposedCommit)
				return map[string]string{
					"activeDrySha":   proposedCommit.Status.Active.Dry.Sha,
					"proposedDrySha": proposedCommit.Status.Proposed.Dry.Sha,
				}
			}, "5s").Should(Equal(map[string]string{
				"activeDrySha":   "5468b78dfef356739559abf1f883cd713794fd96",
				"proposedDrySha": "5468b78dfef356739559abf1f883cd713794fd97",
			}))
			Eventually(func() map[string]string {
				k8sClient.Get(ctx, typeNamespacedName, &proposedCommit)
				return map[string]string{
					"activeHydratedSha":   proposedCommit.Status.Active.Hydrated.Sha,
					"proposedHydratedSha": proposedCommit.Status.Proposed.Hydrated.Sha,
				}
			}, "5s").Should(Not(Equal(map[string]string{
				"activeHydratedSha":   "",
				"proposedHydratedSha": "",
			})))

			var pr promoterv1alpha1.PullRequest
			Eventually(func() map[string]string {
				var typeNamespacedNamePR types.NamespacedName = types.NamespacedName{
					Name:      "test-pc-test-pc-environment-development-next-environment-development",
					Namespace: "default",
				}
				err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
				if err != nil {
					return map[string]string{
						"prName": "",
						"error":  err.Error(),
					}
				}
				return map[string]string{
					"prName":  pr.Name,
					"prTitle": pr.Spec.Title,
					"state":   string(pr.Status.State),
					"error":   "",
				}
			}, "5s").Should(Equal(map[string]string{
				"prName":  "test-pc-test-pc-environment-development-next-environment-development",
				"prTitle": "Promote 5468b78 to `environment/development`",
				"state":   "open",
				"error":   "",
			}))

			By("Adding another pending commit")
			addPendingCommit(gitPath, "7568fd8dfef356739559abf1f883cd713794fd3a", "test-pc", "test-pc")

			By("Reconciling the resource")

			// Update label to force reconcile
			proposedCommit.Labels = map[string]string{"test": "test-new-pr-title"}
			err = k8sClient.Update(ctx, &proposedCommit)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() map[string]string {
				var typeNamespacedNamePR types.NamespacedName = types.NamespacedName{
					Name:      "test-pc-test-pc-environment-development-next-environment-development",
					Namespace: "default",
				}
				err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
				if err != nil {
					return map[string]string{
						"prName": "",
						"error":  err.Error(),
					}
				}
				return map[string]string{
					"prName":  pr.Name,
					"prTitle": pr.Spec.Title,
					"state":   string(pr.Status.State),
					"error":   "",
				}
			}, "5s").Should(Equal(map[string]string{
				"prName":  "test-pc-test-pc-environment-development-next-environment-development",
				"prTitle": "Promote 7568fd8 to `environment/development`",
				"state":   "open",
				"error":   "",
			}))

		})
	})
})
