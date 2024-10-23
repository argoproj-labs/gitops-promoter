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

var _ = Describe("ChangeTransferPolicy Controller", func() {

	Context("When reconciling a resource", func() {
		ctx := context.Background()

		It("should successfully reconcile the resource - with a pending commit and no commit status checks", func() {

			name, scmSecret, scmProvider, gitRepo, _, changeTransferPolicy := changeTransferPolicyResources(ctx, "pc-without-commit-checks", "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default", // TODO(user):Modify as needed
			}

			changeTransferPolicy.Spec.ProposedBranch = "environment/development-next"
			changeTransferPolicy.Spec.ActiveBranch = "environment/development"

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())

			By("Adding a pending commit")
			fullSha, shortSha := makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Owner, gitRepo.Spec.Name)

			By("Reconciling the created resource")

			//var changeTransferPolicy promoterv1alpha1.ChangeTransferPolicy
			Eventually(func(g Gomega) {
				_ = k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
				g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha, fullSha)
				g.Expect(changeTransferPolicy.Status.Active.Hydrated.Sha, Not(Equal("")))
				g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha, Not(Equal("")))

			}, EventuallyTimeout).Should(Succeed())

			var pr promoterv1alpha1.PullRequest
			prName := utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, changeTransferPolicy.Spec.ProposedBranch, changeTransferPolicy.Spec.ActiveBranch)
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
			_, shortSha = makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Owner, gitRepo.Spec.Name)

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

			name, scmSecret, scmProvider, gitRepo, commitStatus, changeTransferPolicy := changeTransferPolicyResources(ctx, "pc-with-commit-checks", "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default", // TODO(user):Modify as needed
			}

			changeTransferPolicy.Spec.ProposedBranch = "environment/development-next"
			changeTransferPolicy.Spec.ActiveBranch = "environment/development"

			changeTransferPolicy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
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
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, commitStatus)).To(Succeed())
			Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())

			By("Adding a pending commit")
			makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Owner, gitRepo.Spec.Name)

			Eventually(func(g Gomega) {

				err := k8sClient.Get(ctx, typeNamespacedName, commitStatus)
				g.Expect(err).To(Succeed())

				sha, err := runGitCmd(gitPath, "rev-parse", changeTransferPolicy.Spec.ActiveBranch)
				Expect(err).NotTo(HaveOccurred())
				sha = strings.TrimSpace(sha)

				commitStatus.Spec.Sha = sha
				commitStatus.Spec.Phase = promoterv1alpha1.CommitPhaseSuccess
				err = k8sClient.Update(ctx, commitStatus)
				g.Expect(err).To(Succeed())

			}, EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
				g.Expect(err).To(Succeed())

				sha, err := runGitCmd(gitPath, "rev-parse", changeTransferPolicy.Spec.ActiveBranch)
				Expect(err).NotTo(HaveOccurred())
				sha = strings.TrimSpace(sha)

				g.Expect(changeTransferPolicy.Status.Active.Hydrated.Sha).To(Equal(sha))
				g.Expect(changeTransferPolicy.Status.Active.CommitStatuses[0].Key).To(Equal("health-check"))
				g.Expect(changeTransferPolicy.Status.Active.CommitStatuses[0].Phase).To(Equal("success"))
			}, EventuallyTimeout).Should(Succeed())

		})
	})
})

func changeTransferPolicyResources(ctx context.Context, name, namespace string) (string, *v1.Secret, *promoterv1alpha1.ScmProvider, *promoterv1alpha1.GitRepository, *promoterv1alpha1.CommitStatus, *promoterv1alpha1.ChangeTransferPolicy) {
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
			Owner: name,
			Name:  name,
			ScmProviderRef: promoterv1alpha1.ObjectReference{
				Name: name,
			},
		},
	}

	commitStatus := &promoterv1alpha1.CommitStatus{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: name,
			},
			Sha:         "",
			Name:        "",
			Description: "",
			Phase:       promoterv1alpha1.CommitPhasePending,
			Url:         "",
		},
	}

	changeTransferPolicy := &promoterv1alpha1.ChangeTransferPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: promoterv1alpha1.ChangeTransferPolicySpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{
				Name: name,
			},
		},
	}

	return name, scmSecret, scmProvider, gitRepo, commitStatus, changeTransferPolicy
}
