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
	"os"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("ProposedCommit Controller", func() {

	var scmProvider *promoterv1alpha1.ScmProvider
	var scmSecret *v1.Secret
	var proposedCommit *promoterv1alpha1.ProposedCommit
	var commitStatus *promoterv1alpha1.CommitStatus

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

			commitStatus = &promoterv1alpha1.CommitStatus{
				TypeMeta: metav1.TypeMeta{},
				ObjectMeta: metav1.ObjectMeta{
					Name:      typeNamespacedName.Name,
					Namespace: typeNamespacedName.Namespace,
				},
				Spec: promoterv1alpha1.CommitStatusSpec{
					RepositoryReference: &promoterv1alpha1.Repository{
						Owner: "test-pc",
						Name:  "test-pc",
						ScmProviderRef: promoterv1alpha1.NamespacedObjectReference{
							Name:      resourceName,
							Namespace: "default",
						},
					},
					Sha:         "",
					Name:        "",
					Description: "",
					State:       "",
					Url:         "",
				},
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
			By("Cleanup the specific resource instance ProposedCommit")
			_ = k8sClient.Delete(ctx, proposedCommit)
			_ = k8sClient.Delete(ctx, commitStatus)
			Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			deleteRepo("test-pc", "test-pc")
		})

		It("should successfully reconcile the resource - with a pending commit and no commit status checks", func() {
			proposedCommit.Spec.ProposedBranch = "environment/development-next"
			proposedCommit.Spec.ActiveBranch = "environment/development"

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, proposedCommit)).To(Succeed())

			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())

			By("Adding a pending commit")
			fullSha, shortSha := addPendingCommit(gitPath, "test-pc", "test-pc")

			By("Reconciling the created resource")

			var proposedCommit promoterv1alpha1.ProposedCommit
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, typeNamespacedName, &proposedCommit)
				g.Expect(proposedCommit.Status.Proposed.Dry.Sha, fullSha)
				g.Expect(proposedCommit.Status.Active.Hydrated.Sha, Not(Equal("")))
				g.Expect(proposedCommit.Status.Proposed.Hydrated.Sha, Not(Equal("")))

			}, "10s").Should(Succeed())

			var pr promoterv1alpha1.PullRequest
			Eventually(func(g Gomega) {
				var typeNamespacedNamePR types.NamespacedName = types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, "test-pc-test-pc-environment-development-next-environment-development"),
					Namespace: "default",
				}
				err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
				g.Expect(err).To(Succeed())
				g.Expect(pr.Spec.Title).To(Equal(fmt.Sprintf("Promote %s to `environment/development`", shortSha)))
				g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				g.Expect(pr.Name).To(Equal(utils.KubeSafeUniqueName(ctx, "test-pc-test-pc-environment-development-next-environment-development")))
			}, "10s").Should(Succeed())

			By("Adding another pending commit")
			_, shortSha = addPendingCommit(gitPath, "test-pc", "test-pc")

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, "test-pc-test-pc-environment-development-next-environment-development"),
					Namespace: "default",
				}, &pr)
				g.Expect(err).To(Succeed())
				g.Expect(pr.Spec.Title).To(Equal(fmt.Sprintf("Promote %s to `environment/development`", shortSha)))
				g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				g.Expect(pr.Name).To(Equal(utils.KubeSafeUniqueName(ctx, "test-pc-test-pc-environment-development-next-environment-development")))
				g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))

			}).Should(Succeed())
		})
		//It("should successfully reconcile the resource - with a pending commit with commit status checks", func() {
		//	proposedCommit.Spec.ProposedBranch = "environment/development-next"
		//	proposedCommit.Spec.ActiveBranch = "environment/development"
		//
		//	Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
		//	Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
		//	Expect(k8sClient.Create(ctx, commitStatus)).To(Succeed())
		//	Expect(k8sClient.Create(ctx, proposedCommit)).To(Succeed())
		//
		//	//gitPath, err := os.MkdirTemp("", "*")
		//	//Expect(err).NotTo(HaveOccurred())
		//	//
		//	//By("Adding a pending commit")
		//	//fullSha, shortSha := addPendingCommit(gitPath, "test-pc", "test-pc")
		//
		//})
	})
})
