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
	"strings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("ProposedCommit Controller", func() {

	Context("When reconciling a resource", func() {
		ctx := context.Background()

		BeforeEach(func() {
		})

		AfterEach(func() {
		})

		It("should successfully reconcile the resource - with a pending commit and no commit status checks", func() {

			name, scmSecret, scmProvider, _, proposedCommit := proposedCommitResources(ctx, "pc-without-commit-checks", "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default", // TODO(user):Modify as needed
			}

			proposedCommit.Spec.ProposedBranch = "environment/development-next"
			proposedCommit.Spec.ActiveBranch = "environment/development"

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, proposedCommit)).To(Succeed())

			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())

			By("Adding a pending commit")
			fullSha, shortSha := makeChangeAndHydrateRepo(gitPath, proposedCommit.Spec.RepositoryReference.Owner, proposedCommit.Spec.RepositoryReference.Name)

			By("Reconciling the created resource")

			//var proposedCommit promoterv1alpha1.ProposedCommit
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, typeNamespacedName, proposedCommit)
				g.Expect(proposedCommit.Status.Proposed.Dry.Sha, fullSha)
				g.Expect(proposedCommit.Status.Active.Hydrated.Sha, Not(Equal("")))
				g.Expect(proposedCommit.Status.Proposed.Hydrated.Sha, Not(Equal("")))

			}, EventuallyTimeout).Should(Succeed())

			var pr promoterv1alpha1.PullRequest
			prName := fmt.Sprintf("%s-%s-%s-%s", proposedCommit.Spec.RepositoryReference.Owner, proposedCommit.Spec.RepositoryReference.Name, proposedCommit.Spec.ProposedBranch, proposedCommit.Spec.ActiveBranch)
			Eventually(func(g Gomega) {

				var typeNamespacedNamePR types.NamespacedName = types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, prName),
					Namespace: "default",
				}
				err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
				g.Expect(err).To(Succeed())
				g.Expect(pr.Spec.Title).To(Equal(fmt.Sprintf("Promote %s to `environment/development`", shortSha)))
				g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				g.Expect(pr.Name).To(Equal(utils.KubeSafeUniqueName(ctx, prName)))
			}, EventuallyTimeout).Should(Succeed())

			By("Adding another pending commit")
			_, shortSha = makeChangeAndHydrateRepo(gitPath, proposedCommit.Spec.RepositoryReference.Owner, proposedCommit.Spec.RepositoryReference.Name)

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, prName),
					Namespace: "default",
				}, &pr)
				g.Expect(err).To(Succeed())
				g.Expect(pr.Spec.Title).To(Equal(fmt.Sprintf("Promote %s to `environment/development`", shortSha)))
				g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				g.Expect(pr.Name).To(Equal(utils.KubeSafeUniqueName(ctx, prName)))

			}, EventuallyTimeout).Should(Succeed())
		})

		It("should successfully reconcile the resource - with a pending commit with commit status checks", func() {

			name, scmSecret, scmProvider, commitStatus, proposedCommit := proposedCommitResources(ctx, "pc-with-commit-checks", "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default", // TODO(user):Modify as needed
			}

			proposedCommit.Spec.ProposedBranch = "environment/development-next"
			proposedCommit.Spec.ActiveBranch = "environment/development"

			proposedCommit.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{
					Key: "health-check",
				},
			}

			commitStatus.Spec.Name = "health-check"
			commitStatus.Labels = map[string]string{
				promoterv1alpha1.CommitStatusLabel: "health-check",
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, commitStatus)).To(Succeed())
			Expect(k8sClient.Create(ctx, proposedCommit)).To(Succeed())

			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())

			By("Adding a pending commit")
			makeChangeAndHydrateRepo(gitPath, proposedCommit.Spec.RepositoryReference.Owner, proposedCommit.Spec.RepositoryReference.Name)

			Eventually(func(g Gomega) {

				err := k8sClient.Get(ctx, typeNamespacedName, commitStatus)
				g.Expect(err).To(Succeed())

				sha, err := runGitCmd(gitPath, "git", "rev-parse", proposedCommit.Spec.ActiveBranch)
				Expect(err).NotTo(HaveOccurred())
				sha = strings.TrimSpace(sha)

				commitStatus.Spec.Sha = sha
				commitStatus.Spec.Phase = "success"
				err = k8sClient.Update(ctx, commitStatus)
				g.Expect(err).To(Succeed())

			}, EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, proposedCommit)
				g.Expect(err).To(Succeed())

				sha, err := runGitCmd(gitPath, "git", "rev-parse", proposedCommit.Spec.ActiveBranch)
				Expect(err).NotTo(HaveOccurred())
				sha = strings.TrimSpace(sha)

				g.Expect(proposedCommit.Status.Active.Hydrated.Sha).To(Equal(sha))
				g.Expect(proposedCommit.Status.Active.CommitStatuses[0].Key).To(Equal("health-check"))
				g.Expect(proposedCommit.Status.Active.CommitStatuses[0].Phase).To(Equal("success"))
			}, EventuallyTimeout).Should(Succeed())

		})
	})
})

func proposedCommitResources(ctx context.Context, name, namespace string) (string, *v1.Secret, *promoterv1alpha1.ScmProvider, *promoterv1alpha1.CommitStatus, *promoterv1alpha1.ProposedCommit) {
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

	commitStatus := &promoterv1alpha1.CommitStatus{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: &promoterv1alpha1.Repository{
				Owner: name,
				Name:  name,
				ScmProviderRef: promoterv1alpha1.NamespacedObjectReference{
					Name:      name,
					Namespace: namespace,
				},
			},
			Sha:         "",
			Name:        "",
			Description: "",
			Phase:       "pending",
			Url:         "",
		},
	}

	proposedCommit := &promoterv1alpha1.ProposedCommit{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.ProposedCommitSpec{
			RepositoryReference: &promoterv1alpha1.Repository{
				Owner: name,
				Name:  name,
				ScmProviderRef: promoterv1alpha1.NamespacedObjectReference{
					Name:      name,
					Namespace: namespace,
				},
			},
		},
	}

	return name, scmSecret, scmProvider, commitStatus, proposedCommit
}
