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
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
)

const healthCheckCSKey = "health-check"

var _ = Describe("ChangeTransferPolicy Controller", func() {
	Context("When reconciling a resource", func() {
		ctx := context.Background()

		It("should successfully reconcile the resource - with a pending commit and no commit status checks", func() {
			name, scmSecret, scmProvider, gitRepo, _, changeTransferPolicy := changeTransferPolicyResources(ctx, "ctp-without-commit-checks", "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default", // TODO(user):Modify as needed
			}

			changeTransferPolicy.Spec.ProposedBranch = "environment/development-next" //nolint:goconst
			changeTransferPolicy.Spec.ActiveBranch = "environment/development"        //nolint:goconst
			// We set auto merge to false to avoid the PR being merged automatically so we can run checks on it
			changeTransferPolicy.Spec.AutoMerge = ptr.To(false)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())

			By("Adding a pending commit")
			fullSha, shortSha := makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)

			By("Reconciling the created resource")

			simulateWebhook(ctx, k8sClient, changeTransferPolicy)
			Eventually(func(g Gomega) {
				err = k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
				g.Expect(err).To(Succeed())
				g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha).To(Equal(fullSha))
				g.Expect(changeTransferPolicy.Status.Active.Hydrated.Sha).ToNot(Equal(""))
				g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).ToNot(Equal(""))
			}, EventuallyTimeout).Should(Succeed())

			var pr promoterv1alpha1.PullRequest
			prName := utils.GetPullRequestName(ctx, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, changeTransferPolicy.Spec.ProposedBranch, changeTransferPolicy.Spec.ActiveBranch)
			Eventually(func(g Gomega) {
				typeNamespacedNamePR := types.NamespacedName{
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
			_, shortSha = makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)

			simulateWebhook(ctx, k8sClient, changeTransferPolicy)
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

			Eventually(func(g Gomega) {
				err = k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
				Expect(err).To(Succeed())
				// We now have a PR so we can set it to true and then check that it gets merged
				changeTransferPolicy.Spec.AutoMerge = ptr.To(true)
				err = k8sClient.Update(ctx, changeTransferPolicy)
				g.Expect(err).To(Succeed())
			}, EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				typeNamespacedNamePR := types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, prName),
					Namespace: "default",
				}
				err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, EventuallyTimeout).Should(Succeed())
		})

		It("should successfully reconcile the resource - with a pending commit with commit status checks", func() {
			name, scmSecret, scmProvider, gitRepo, commitStatus, changeTransferPolicy := changeTransferPolicyResources(ctx, "ctp-with-commit-checks", "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default", // TODO(user):Modify as needed
			}

			changeTransferPolicy.Spec.ProposedBranch = "environment/development-next"
			changeTransferPolicy.Spec.ActiveBranch = "environment/development"
			// We set auto merge to false to avoid the PR being merged automatically so we can run checks on it
			changeTransferPolicy.Spec.AutoMerge = ptr.To(false)

			changeTransferPolicy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{
					Key: healthCheckCSKey,
				},
			}

			commitStatus.Spec.Name = healthCheckCSKey
			commitStatus.Labels = map[string]string{
				promoterv1alpha1.CommitStatusLabel: healthCheckCSKey,
			}

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, commitStatus)).To(Succeed())
			Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())

			By("Adding a pending commit")
			makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)

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
				g.Expect(changeTransferPolicy.Status.Active.CommitStatuses[0].Key).To(Equal(healthCheckCSKey))
				g.Expect(changeTransferPolicy.Status.Active.CommitStatuses[0].Phase).To(Equal("success"))
			}, EventuallyTimeout).Should(Succeed())

			var pr promoterv1alpha1.PullRequest
			prName := utils.GetPullRequestName(ctx, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, changeTransferPolicy.Spec.ProposedBranch, changeTransferPolicy.Spec.ActiveBranch)
			Eventually(func(g Gomega) {
				err = k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
				Expect(err).To(Succeed())
				// We now have a PR so we can set it to true and then check that it gets merged
				changeTransferPolicy.Spec.AutoMerge = ptr.To(true)
				err = k8sClient.Update(ctx, changeTransferPolicy)
				g.Expect(err).To(Succeed())
			}, EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				typeNamespacedNamePR := types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, prName),
					Namespace: "default",
				}
				err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, EventuallyTimeout).Should(Succeed())
		})

		It("webhook should modify annotation", func() {
			webhookPort := WebhookReceiverPort + GinkgoParallelProcess()
			webhookURL := fmt.Sprintf("http://localhost:%d/", webhookPort)

			name, scmSecret, scmProvider, gitRepo, _, changeTransferPolicy := changeTransferPolicyResources(ctx, "ctp-webhook", "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default", // TODO(user):Modify as needed
			}

			changeTransferPolicy.Spec.ProposedBranch = "environment/development-next"
			changeTransferPolicy.Spec.ActiveBranch = "environment/development"
			// We set auto merge to false to avoid the PR being merged automatically so we can run checks on it
			changeTransferPolicy.Spec.AutoMerge = ptr.To(false)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())

			By("Adding a pending commit")
			fullSha, shortSha := makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)

			simulateWebhook(ctx, k8sClient, changeTransferPolicy)
			Eventually(func(g Gomega) {
				err = k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
				g.Expect(err).To(Succeed())
				g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha).To(Equal(fullSha))
				g.Expect(changeTransferPolicy.Status.Active.Hydrated.Sha).ToNot(Equal(""))
				g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).ToNot(Equal(""))
			}, EventuallyTimeout).Should(Succeed())

			var pr promoterv1alpha1.PullRequest
			prName := utils.GetPullRequestName(ctx, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, changeTransferPolicy.Spec.ProposedBranch, changeTransferPolicy.Spec.ActiveBranch)
			Eventually(func(g Gomega) {
				typeNamespacedNamePR := types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, prName),
					Namespace: "default",
				}
				err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
				g.Expect(err).To(Succeed())
				g.Expect(pr.Spec.Title).To(Equal(fmt.Sprintf("Promote %s to `environment/development`", shortSha)))
				g.Expect(pr.Status.State).To(Equal(promoterv1alpha1.PullRequestOpen))
				g.Expect(pr.Name).To(Equal(utils.KubeSafeUniqueName(ctx, prName)))
			}, EventuallyTimeout).Should(Succeed())

			// Make http request
			jsonStr := []byte(fmt.Sprintf(`{"before":"%s", "pusher":""}`, changeTransferPolicy.Status.Proposed.Hydrated.Sha))
			req, err := http.NewRequest(http.MethodPost, webhookURL, bytes.NewBuffer(jsonStr))
			req.Header.Set("Content-Type", "application/json")

			client := &http.Client{}
			resp, err := client.Do(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(200))
			err = resp.Body.Close()
			Expect(err).To(Succeed())

			Eventually(func(g Gomega) {
				err = k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
				g.Expect(err).To(Succeed())
				g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha).To(Equal(fullSha))

				t, err := time.Parse(time.RFC3339Nano, changeTransferPolicy.Annotations[promoterv1alpha1.ReconcileAtAnnotation])
				g.Expect(err).To(Succeed())
				g.Expect(t).Should(BeTemporally("~", time.Now(), 3*time.Second))
			}, EventuallyTimeout).Should(Succeed())
		})

		// Happens if the active branch does not have a hydrator.metadata such as when the branch was just created
		It("should successfully reconcile the resource - with unknown dry sha", func() {
			name, scmSecret, scmProvider, gitRepo, _, changeTransferPolicy := changeTransferPolicyResources(ctx, "ctp-without-dry-sha", "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default", // TODO(user):Modify as needed
			}

			changeTransferPolicy.Spec.ProposedBranch = "environment/development-next"
			changeTransferPolicy.Spec.ActiveBranch = "environment/development"
			// We set auto merge to false to avoid the PR being merged automatically so we can run checks on it
			changeTransferPolicy.Spec.AutoMerge = ptr.To(false)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, changeTransferPolicy)).To(Succeed())

			gitPath, err := os.MkdirTemp("", "*")
			Expect(err).NotTo(HaveOccurred())

			By("Adding a pending commit")
			fullSha, shortSha := makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)

			By("Reconciling the created resource")

			simulateWebhook(ctx, k8sClient, changeTransferPolicy)
			Eventually(func(g Gomega) {
				err = k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
				g.Expect(err).To(Succeed())
				g.Expect(changeTransferPolicy.Status.Proposed.Dry.Sha).To(Equal(fullSha))
				g.Expect(changeTransferPolicy.Status.Active.Hydrated.Sha).ToNot(Equal(""))
				g.Expect(changeTransferPolicy.Status.Proposed.Hydrated.Sha).ToNot(Equal(""))
			}, EventuallyTimeout).Should(Succeed())

			var pr promoterv1alpha1.PullRequest
			prName := utils.GetPullRequestName(ctx, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, changeTransferPolicy.Spec.ProposedBranch, changeTransferPolicy.Spec.ActiveBranch)
			Eventually(func(g Gomega) {
				typeNamespacedNamePR := types.NamespacedName{
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
			_, shortSha = makeChangeAndHydrateRepo(gitPath, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)

			simulateWebhook(ctx, k8sClient, changeTransferPolicy)
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

			Eventually(func(g Gomega) {
				err = k8sClient.Get(ctx, typeNamespacedName, changeTransferPolicy)
				Expect(err).To(Succeed())
				// We now have a PR so we can set it to true and then check that it gets merged
				changeTransferPolicy.Spec.AutoMerge = ptr.To(true)
				err = k8sClient.Update(ctx, changeTransferPolicy)
				g.Expect(err).To(Succeed())
			}, EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				typeNamespacedNamePR := types.NamespacedName{
					Name:      utils.KubeSafeUniqueName(ctx, prName),
					Namespace: "default",
				}
				err := k8sClient.Get(ctx, typeNamespacedNamePR, &pr)
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, EventuallyTimeout).Should(Succeed())
		})
	})
})

//nolint:unparam
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
			Fake: &promoterv1alpha1.FakeRepo{
				Owner: name,
				Name:  name,
			},
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
