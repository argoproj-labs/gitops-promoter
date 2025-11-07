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

	"github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
})
