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
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("PullRequest Controller", func() {
	Context("When reconciling a resource", func() {
		ctx := context.Background()

		It("should successfully reconcile the resource when updating title then merging", func() {
			By("Reconciling the created resource")

			name, scmSecret, scmProvider, gitRepo, pullRequest := pullRequestResources(ctx, "update-title-merge", "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			pullRequest.Spec.Title = "Initial Title"
			pullRequest.Spec.TargetBranch = "development"
			pullRequest.Spec.SourceBranch = "development-next"
			pullRequest.Spec.Description = "Initial Description"

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
			}, EventuallyTimeout)

			By("Reconciling updating of the PullRequest")
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				pullRequest.Spec.Title = "Updated Title"
				g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
			}, EventuallyTimeout)

			Eventually(func(g Gomega) {
				Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Spec.Title).To(Equal("Updated Title"))
			}, EventuallyTimeout)

			By("Reconciling merging of the PullRequest")
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				pullRequest.Spec.State = "merged"
				g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
			}, EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("pullrequests.promoter.argoproj.io \"" + name + "\" not found"))
			}, EventuallyTimeout)
		})
		It("should successfully reconcile the resource when closing", func() {
			By("Reconciling the created resource")

			name, scmSecret, scmProvider, gitRepo, pullRequest := pullRequestResources(ctx, "update-title-close", "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			pullRequest.Spec.Title = "Initial Title"
			pullRequest.Spec.TargetBranch = "staging"
			pullRequest.Spec.SourceBranch = "staging-next"
			pullRequest.Spec.Description = "Initial Description"

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
			}, EventuallyTimeout).Should(Succeed())

			By("Reconciling closing of the PullRequest")
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				pullRequest.Spec.State = "closed"
				g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
			}, EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("pullrequests.promoter.argoproj.io \"" + name + "\" not found"))
			}, EventuallyTimeout).Should(Succeed())
		})
	})
})

func pullRequestResources(ctx context.Context, name, namespace string) (string, *v1.Secret, *promoterv1alpha1.ScmProvider, *promoterv1alpha1.GitRepository, *promoterv1alpha1.PullRequest) {
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

	pullRequest := &promoterv1alpha1.PullRequest{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.PullRequestSpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: name,
			},
			Title:        "",
			TargetBranch: "",
			SourceBranch: "",
			Description:  "",
			State:        "open",
		},
		Status: promoterv1alpha1.PullRequestStatus{},
	}

	return name, scmSecret, scmProvider, gitRepo, pullRequest
}
