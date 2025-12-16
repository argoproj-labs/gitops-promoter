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
	"strings"

	"github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

//go:embed testdata/ArgoCDCommitStatus.yaml
var testArgoCDCommitStatusYAML string

var _ = Describe("ArgoCDCommitStatus Controller", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the ArgoCDCommitStatus resource", func() {
			err := unmarshalYamlStrict(testArgoCDCommitStatusYAML, &promoterv1alpha1.ArgoCDCommitStatus{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("When reconciling a resource", func() {
		It("should fail if the application's SyncSource.TargetBranch is empty", func() {
			ctx := context.TODO()

			// Create a PromotionStrategy resource FIRST (dependency)
			promotionStrategy := &promoterv1alpha1.PromotionStrategy{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "example-promotion-strategy",
				},
				Spec: promoterv1alpha1.PromotionStrategySpec{
					RepositoryReference: promoterv1alpha1.ObjectReference{
						Name: "example-repo",
					},
					Environments: []promoterv1alpha1.Environment{
						{
							Branch: testEnvironmentStaging,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// Create ArgoCDCommitStatus SECOND (before Application!)
			// This ensures the controller's secondary watch on Applications will find this resource
			commitStatus := &promoterv1alpha1.ArgoCDCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-status",
				},
				Spec: promoterv1alpha1.ArgoCDCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "example-promotion-strategy"},
					ApplicationSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "demo"},
					},
				},
			}
			Expect(k8sClient.Create(ctx, commitStatus)).To(Succeed())

			// Create Application LAST (with empty TargetBranch to trigger validation error)
			app := &argocd.Application{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "test-app",
					Labels: map[string]string{
						"app": "demo",
					},
				},
				Spec: argocd.ApplicationSpec{
					SourceHydrator: &argocd.SourceHydrator{
						SyncSource: argocd.SyncSource{
							TargetBranch: "",
						},
						DrySource: argocd.DrySource{
							RepoURL: "https://example.com/repo.git",
						},
					},
				},
				Status: argocd.ApplicationStatus{
					Health: argocd.HealthStatus{
						Status: "Healthy",
					},
					Sync: argocd.SyncStatus{
						Status:   "Synced",
						Revision: "abc123",
					},
				},
			}
			Expect(k8sClient.Create(ctx, app)).To(Succeed())

			// Wait for reconciliation and check status condition
			Eventually(func(g Gomega) {
				updated := &promoterv1alpha1.ArgoCDCommitStatus{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "test-status", Namespace: "default"}, updated)
				g.Expect(err).ToNot(HaveOccurred())

				c := meta.FindStatusCondition(updated.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(c).ToNot(BeNil())
				g.Expect(c.Message).To(ContainSubstring("spec.sourceHydrator.syncSource.targetBranch must not be empty"))
			}, constants.EventuallyTimeout).Should(Succeed())

			// Clean up
			Expect(k8sClient.Delete(ctx, app)).To(Succeed())
			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
			Expect(k8sClient.Delete(ctx, commitStatus)).To(Succeed())
		})
	})

	Context("When multiple applications provide nondeterministic branch ordering", func() {
		It("should produce a deterministic, sorted branch list in the error message", func() {
			ctx := context.TODO()

			// Create a fake SCM provider
			scmProvider := &promoterv1alpha1.ScmProvider{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fake-scm-provider",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ScmProviderSpec{
					Fake: &promoterv1alpha1.Fake{},
					SecretRef: &v1.LocalObjectReference{
						Name: "fake-scm-secret",
					},
				},
			}
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())

			// Create a secret for the SCM provider
			scmSecret := &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "fake-scm-secret",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"token": []byte("fake-token"),
				},
			}
			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())

			// Create a GitRepository with an INVALID URL to trigger ls-remote error
			gitRepo := &promoterv1alpha1.GitRepository{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "invalid-repo",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.GitRepositorySpec{
					Fake: &promoterv1alpha1.FakeRepo{
						Owner: "nonexistent",
						Name:  "invalid-repo-12345",
					},
					ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{
						Kind: "ScmProvider",
						Name: "fake-scm-provider",
					},
				},
			}
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())

			// Create a PromotionStrategy
			promotionStrategy := &promoterv1alpha1.PromotionStrategy{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "sorting-test-strategy",
				},
				Spec: promoterv1alpha1.PromotionStrategySpec{
					RepositoryReference: promoterv1alpha1.ObjectReference{
						Name: "invalid-repo",
					},
					Environments: []promoterv1alpha1.Environment{
						{
							Branch: "env/argocd/west",
						},
						{
							Branch: "env/argocd/east",
						},
						{
							Branch: "env/argocd/north",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			// Create the ArgoCDCommitStatus
			cr := &promoterv1alpha1.ArgoCDCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sorting-test",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ArgoCDCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: "sorting-test-strategy",
					},
					ApplicationSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"argocd.com/argocd-commitstatus-selector": "sorting-test",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())

			// Create Argo CD Applications with the correct branches
			branches := []string{
				"env/argocd/west",
				"env/argocd/east",
				"env/argocd/north",
			}

			for _, branch := range branches {
				app := &argocd.Application{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "app-" + strings.ReplaceAll(branch, "/", "-"),
						Labels: map[string]string{
							"argocd.com/argocd-commitstatus-selector": "sorting-test",
						},
					},
					Spec: argocd.ApplicationSpec{
						SourceHydrator: &argocd.SourceHydrator{
							SyncSource: argocd.SyncSource{
								TargetBranch: branch,
							},
							DrySource: argocd.DrySource{
								RepoURL: "http://localhost:" + gitServerPort + "/nonexistent/invalid-repo-12345",
							},
						},
					},
					Status: argocd.ApplicationStatus{
						Health: argocd.HealthStatus{
							Status: "Healthy",
						},
						Sync: argocd.SyncStatus{
							Status:   "Synced",
							Revision: "abc123",
						},
					},
				}

				Expect(k8sClient.Create(ctx, app)).To(Succeed())
			}

			// Wait for reconcile to occur and check for ls-remote error with sorted branches
			Eventually(func(g Gomega) {
				updated := &promoterv1alpha1.ArgoCDCommitStatus{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "sorting-test", Namespace: "default"}, updated)
				g.Expect(err).ToNot(HaveOccurred())

				g.Expect(updated.Status.Conditions).ToNot(BeEmpty())
				c := meta.FindStatusCondition(updated.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(c).ToNot(BeNil())
				// The error message should contain the sorted branch list
				g.Expect(c.Message).To(ContainSubstring("env/argocd/east"))
				g.Expect(c.Message).To(ContainSubstring("env/argocd/north"))
				g.Expect(c.Message).To(ContainSubstring("env/argocd/west"))
				// Verify the branches are sorted alphabetically in the error message
				g.Expect(c.Message).To(MatchRegexp(`env/argocd/east.*env/argocd/north.*env/argocd/west`))
			}, constants.EventuallyTimeout).Should(Succeed())

			// Clean up
			Expect(k8sClient.Delete(ctx, cr)).To(Succeed())
			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
			Expect(k8sClient.Delete(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Delete(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Delete(ctx, scmSecret)).To(Succeed())
			for _, branch := range branches {
				app := &argocd.Application{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: "default",
						Name:      "app-" + strings.ReplaceAll(branch, "/", "-"),
					},
				}
				Expect(k8sClient.Delete(ctx, app)).To(Succeed())
			}
		})
	})
})
