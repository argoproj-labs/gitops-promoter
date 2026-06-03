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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
)

//go:embed testdata/GitRepository.yaml
var testGitRepositoryYAML string

var _ = Describe("GitRepository Controller", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the GitRepository resource", func() {
			err := unmarshalYamlStrict(testGitRepositoryYAML, &promoterv1alpha1.GitRepository{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		gitrepository := &promoterv1alpha1.GitRepository{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind GitRepository")
			err := k8sClient.Get(ctx, typeNamespacedName, gitrepository)
			if err != nil && errors.IsNotFound(err) {
				resource := &promoterv1alpha1.GitRepository{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: promoterv1alpha1.GitRepositorySpec{
						Fake: &promoterv1alpha1.FakeRepo{
							Owner: "test-owner",
							Name:  "test-repo",
						},
						ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
							Kind: promoterv1alpha1.ScmProviderKind,
							Name: resourceName,
						},
					},
					// TODO(user): Specify other spec details if needed.
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &promoterv1alpha1.GitRepository{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance GitRepository")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Waiting for the controller to reconcile the resource")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, gitrepository)
				g.Expect(err).NotTo(HaveOccurred())
				// Verify that the controller has added the finalizer
				g.Expect(gitrepository.Finalizers).To(ContainElement(promoterv1alpha1.GitRepositoryFinalizer))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When a referencing PullRequest is terminating but not yet removed", func() {
		const blockingFinalizer = "promoter.argoproj.io/test-will-not-remove"

		var ctx context.Context
		var name string
		var scmSecret *v1.Secret
		var scmProvider *promoterv1alpha1.ScmProvider
		var gitRepo *promoterv1alpha1.GitRepository
		var pullRequest *promoterv1alpha1.PullRequest
		var typeNamespacedName types.NamespacedName

		BeforeEach(func() {
			ctx = context.Background()

			name, scmSecret, scmProvider, gitRepo, pullRequest = pullRequestResources(ctx, "gitrepo-terminating-pr-race")

			typeNamespacedName = types.NamespacedName{
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
				g.Expect(pullRequest.Status.ID).ToNot(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, gitRepo)).To(Succeed())
				g.Expect(gitRepo.Finalizers).To(ContainElement(promoterv1alpha1.GitRepositoryFinalizer))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Adding a bogus finalizer so the PullRequest keeps a deletionTimestamp without leaving the API")
			Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
			base := pullRequest.DeepCopy()
			pullRequest.Finalizers = append(pullRequest.Finalizers, blockingFinalizer)
			Expect(k8sClient.Patch(ctx, pullRequest, client.MergeFrom(base))).To(Succeed())

			Expect(k8sClient.Delete(ctx, pullRequest)).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.DeletionTimestamp).ToNot(BeNil())
				g.Expect(pullRequest.Finalizers).NotTo(ContainElement(promoterv1alpha1.PullRequestFinalizer))
				g.Expect(pullRequest.Finalizers).To(ContainElement(blockingFinalizer))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		AfterEach(func() {
			var pr promoterv1alpha1.PullRequest
			if err := k8sClient.Get(ctx, typeNamespacedName, &pr); err == nil && pr.DeletionTimestamp != nil {
				var kept []string
				for _, f := range pr.Finalizers {
					if f != blockingFinalizer {
						kept = append(kept, f)
					}
				}
				if len(kept) != len(pr.Finalizers) {
					base := pr.DeepCopy()
					pr.Finalizers = kept
					_ = k8sClient.Patch(ctx, &pr, client.MergeFrom(base))
				}
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, typeNamespacedName, &pr)
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
			}

			_ = client.IgnoreNotFound(k8sClient.Delete(ctx, gitRepo))
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, gitRepo)
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			_ = client.IgnoreNotFound(k8sClient.Delete(ctx, scmProvider))
			_ = client.IgnoreNotFound(k8sClient.Delete(ctx, scmSecret))
		})

		It("should block GitRepository deletion until the PullRequest is gone, then finish deleting", func() {
			Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, typeNamespacedName, gitRepo)).To(Succeed())
				g.Expect(gitRepo.DeletionTimestamp).ToNot(BeNil())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Reproducing the race: GitRepository must not disappear while a terminating PullRequest remains in etcd")
			Consistently(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, gitRepo)
				g.Expect(err).ToNot(HaveOccurred(),
					"GitRepository was deleted while a referencing PullRequest still exists (deletionTimestamp carveout bug)")
				g.Expect(gitRepo.Finalizers).To(ContainElement(promoterv1alpha1.GitRepositoryFinalizer),
					"GitRepository finalizer should remain until all referencing PullRequests are gone")

				g.Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
				g.Expect(pullRequest.DeletionTimestamp).ToNot(BeNil())
			}, 5*time.Second, 200*time.Millisecond).Should(Succeed())

			By("Removing the blocking finalizer so the PullRequest object can leave the API")
			Expect(k8sClient.Get(ctx, typeNamespacedName, pullRequest)).To(Succeed())
			base := pullRequest.DeepCopy()
			var kept []string
			for _, f := range pullRequest.Finalizers {
				if f != blockingFinalizer {
					kept = append(kept, f)
				}
			}
			pullRequest.Finalizers = kept
			Expect(k8sClient.Patch(ctx, pullRequest, client.MergeFrom(base))).To(Succeed())

			By("Verifying the PullRequest is fully removed, not only marked for deletion")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, pullRequest)
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying the GitRepository deletion completes after the PullRequest object is gone")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, gitRepo)
				g.Expect(errors.IsNotFound(err)).To(BeTrue(),
					"GitRepository should be deleted after the last referencing PullRequest is removed from the API")
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})
