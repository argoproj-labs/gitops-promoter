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
	_ "embed"

	"github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"k8s.io/apimachinery/pkg/api/meta"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

//go:embed testdata/PullRequest.yaml
var testPullRequestYAML string

var _ = Describe("PullRequest Controller", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the PullRequest resource", func() {
			err := unmarshalYamlStrict(testPullRequestYAML, &promoterv1alpha1.PullRequest{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("When reconciling a resource", func() {
		ctx := context.Background()

		It("should successfully reconcile the resource when updating title then merging", func() {
			By("Reconciling the created resource")

			name, scmSecret, scmProvider, gitRepo, pullRequest := pullRequestResources(ctx, "update-title-merge")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
			}, constants.EventuallyTimeout)

			By("Reconciling updating of the PullRequest")
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				pullRequest.Spec.Title = "Updated Title"
				g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
			}, constants.EventuallyTimeout)

			Eventually(func(g Gomega) {
				Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Spec.Title).To(Equal("Updated Title"))
			}, constants.EventuallyTimeout)

			By("Reconciling merging of the PullRequest")
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				pullRequest.Spec.State = "merged"
				g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("pullrequests.promoter.argoproj.io \"" + name + "\" not found"))
			}, constants.EventuallyTimeout)
		})
		It("should successfully reconcile the resource when closing", func() {
			By("Reconciling the created resource")

			name, scmSecret, scmProvider, gitRepo, pullRequest := pullRequestResources(ctx, "update-title-close")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Reconciling closing of the PullRequest")
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				pullRequest.Spec.State = "closed"
				g.Expect(k8sClient.Update(ctx, pullRequest)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("pullrequests.promoter.argoproj.io \"" + name + "\" not found"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When reconciling a resource with a bad configuration", func() {
		ctx := context.Background()

		It("should successfully reconcile the resource and update conditions with the error", func() {
			By("Reconciling the created resource")

			name, scmSecret, scmProvider, gitRepo, pullRequest := pullRequestResources(ctx, "bad-configuration-no-scm-secret")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			scmProvider.Spec.SecretRef = &v1.LocalObjectReference{Name: "non-existing-secret"}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

			By("Checking the PullRequest status conditions have an error condition")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Status.Conditions).To(HaveLen(1))
				g.Expect(pullRequest.Status.Conditions[0].Type).To(Equal(string(conditions.Ready)))
				g.Expect(meta.IsStatusConditionFalse(pullRequest.Status.Conditions, string(conditions.Ready))).To(BeTrue())
				g.Expect(pullRequest.Status.Conditions[0].Reason).To(Equal(string(conditions.ReconciliationError)))
				g.Expect(pullRequest.Status.Conditions[0].Message).To(ContainSubstring("secret from ScmProvider not found"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When attempting to create a PullRequest with invalid initial state", func() {
		ctx := context.Background()

		It("should fail to create a PullRequest with spec.state set to 'merged'", func() {
			By("Attempting to create a PullRequest with spec.state='merged' and empty status.id")

			_, scmSecret, scmProvider, gitRepo, pullRequest := pullRequestResources(ctx, "create-merged")

			pullRequest.Spec.State = promoterv1alpha1.PullRequestMerged

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())

			By("Verifying the create operation fails due to CEL validation")
			err := k8sClient.Create(ctx, pullRequest)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Cannot transition to 'closed' or 'merged' state when status.id is empty"))
		})

		It("should fail to create a PullRequest with spec.state set to 'closed'", func() {
			By("Attempting to create a PullRequest with spec.state='closed' and empty status.id")

			_, scmSecret, scmProvider, gitRepo, pullRequest := pullRequestResources(ctx, "create-closed")

			pullRequest.Spec.State = promoterv1alpha1.PullRequestClosed

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())

			By("Verifying the create operation fails due to CEL validation")
			err := k8sClient.Create(ctx, pullRequest)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Cannot transition to 'closed' or 'merged' state when status.id is empty"))
		})
	})

	Context("When deleting a PullRequest that never created a PR on SCM", func() {
		ctx := context.Background()

		It("should successfully delete a PullRequest with empty status.id without getting stuck", func() {
			By("Creating a PullRequest but preventing it from creating a PR on SCM")

			name, scmSecret, scmProvider, gitRepo, pullRequest := pullRequestResources(ctx, "delete-without-scm-pr")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			// Create a bad SCM provider configuration to prevent PR creation
			scmProvider.Spec.SecretRef = &v1.LocalObjectReference{Name: "non-existing-secret"}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

			By("Waiting for PullRequest to be reconciled but not create a PR on SCM")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				// Should have an error condition but no status.id
				g.Expect(pullRequest.Status.ID).To(BeEmpty())
				g.Expect(pullRequest.Status.Conditions).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Deleting the PullRequest")
			Expect(k8sClient.Delete(ctx, pullRequest)).To(Succeed())

			By("Verifying the PullRequest is deleted successfully without getting stuck")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("pullrequests.promoter.argoproj.io \"" + name + "\" not found"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

	})

	Context("When deleting resources with finalizers", func() {
		ctx := context.Background()

		It("should prevent deletion of GitRepository while PullRequest exists", func() {
			By("Creating the resource hierarchy")

			name, scmSecret, scmProvider, gitRepo, pullRequest := pullRequestResources(ctx, "finalizer-test-gitrepo")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

			By("Waiting for PullRequest to be ready")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
			}, constants.EventuallyTimeout)

			By("Attempting to delete GitRepository while PullRequest exists")
			Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())

			By("Verifying GitRepository is not deleted while PullRequest exists")
			Consistently(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, gitRepo)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(gitRepo.DeletionTimestamp).ToNot(BeNil())
			}, "5s", "1s").Should(Succeed())

			By("Deleting the PullRequest")
			Expect(k8sClient.Delete(ctx, pullRequest)).To(Succeed())

			By("Verifying PullRequest is deleted")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("not found"))
			}, constants.EventuallyTimeout)

			By("Verifying GitRepository is now deleted")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, gitRepo)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("not found"))
			}, constants.EventuallyTimeout)
		})

		It("should prevent deletion of ScmProvider while GitRepository exists", func() {
			By("Creating the resource hierarchy")

			name, scmSecret, scmProvider, gitRepo, _ := pullRequestResources(ctx, "finalizer-test-scmprovider")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())

			By("Attempting to delete ScmProvider while GitRepository exists")
			Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())

			By("Verifying ScmProvider is not deleted while GitRepository exists")
			Consistently(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, scmProvider)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(scmProvider.DeletionTimestamp).ToNot(BeNil())
			}, "5s", "1s").Should(Succeed())

			By("Deleting the GitRepository")
			Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())

			By("Verifying GitRepository is deleted")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, gitRepo)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("not found"))
			}, constants.EventuallyTimeout)

			By("Verifying ScmProvider is now deleted")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, scmProvider)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("not found"))
			}, constants.EventuallyTimeout)
		})

		It("should add finalizer to Secret when ScmProvider is created", func() {
			By("Creating the resource hierarchy")

			name, scmSecret, scmProvider, _, _ := pullRequestResources(ctx, "finalizer-test-secret")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())

			By("Waiting for ScmProvider to add finalizer to Secret")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, scmSecret)).To(Succeed())
				g.Expect(scmSecret.Finalizers).To(ContainElement("scmprovider.promoter.argoproj.io/secret-finalizer"))
			}, constants.EventuallyTimeout)

			By("Verifying ScmProvider has its own finalizer")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, scmProvider)).To(Succeed())
				g.Expect(scmProvider.Finalizers).To(ContainElement("scmprovider.promoter.argoproj.io/finalizer"))
			}, constants.EventuallyTimeout)

			By("Deleting the ScmProvider")
			Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())

			By("Verifying ScmProvider is deleted and Secret finalizer is removed")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, scmProvider)
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("not found"))
			}, constants.EventuallyTimeout)

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, scmSecret)).To(Succeed())
				g.Expect(scmSecret.Finalizers).ToNot(ContainElement("scmprovider.promoter.argoproj.io/secret-finalizer"))
			}, constants.EventuallyTimeout)

			By("Cleaning up Secret")
			Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
		})

		It("should allow deletion of entire resource hierarchy when deleting from top down", func() {
			By("Creating the complete resource hierarchy")

			name, scmSecret, scmProvider, gitRepo, pullRequest := pullRequestResources(ctx, "finalizer-test-complete")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, pullRequest)).To(Succeed())

			By("Waiting for finalizers to be added")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, scmSecret)).To(Succeed())
				g.Expect(scmSecret.Finalizers).To(ContainElement(promoterv1alpha1.ScmProviderSecretFinalizer))
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, scmProvider)).To(Succeed())
				g.Expect(scmProvider.Finalizers).To(ContainElement(promoterv1alpha1.ScmProviderFinalizer))
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, gitRepo)).To(Succeed())
				g.Expect(gitRepo.Finalizers).To(ContainElement(promoterv1alpha1.GitRepositoryFinalizer))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Waiting for PullRequest to be ready")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
			}, constants.EventuallyTimeout)

			By("Deleting from top down: PullRequest, GitRepository, ScmProvider, Secret")
			Expect(k8sClient.Delete(ctx, pullRequest)).To(Succeed())
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				g.Expect(err).To(HaveOccurred())
			}, constants.EventuallyTimeout)

			Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, gitRepo)
				g.Expect(err).To(HaveOccurred())
			}, constants.EventuallyTimeout)

			Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, scmProvider)
				g.Expect(err).To(HaveOccurred())
			}, constants.EventuallyTimeout)

			Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, scmSecret)
				g.Expect(err).To(HaveOccurred())
			}, constants.EventuallyTimeout)
		})
	})
})

func pullRequestResources(ctx context.Context, name string) (string, *v1.Secret, *promoterv1alpha1.ScmProvider, *promoterv1alpha1.GitRepository, *promoterv1alpha1.PullRequest) {
	name = name + "-" + utils.KubeSafeUniqueName(ctx, randomString(15))
	setupInitialTestGitRepoOnServer(ctx, name, name)

	scmSecret := &v1.Secret{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
		},
		Data: nil,
	}

	scmProvider := &promoterv1alpha1.ScmProvider{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
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
			Namespace: "default",
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
			Namespace: "default",
		},
		Spec: promoterv1alpha1.PullRequestSpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: name,
			},
			Title:        "Initial Title",
			TargetBranch: "development",
			SourceBranch: "development-next",
			Description:  "Initial Description",
			MergeSha:     "abc123def456789012345678901234567890abcd", // Dummy SHA for testing
			State:        "open",
		},
		Status: promoterv1alpha1.PullRequestStatus{},
	}

	return name, scmSecret, scmProvider, gitRepo, pullRequest
}
