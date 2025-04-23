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
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"strings"
)

var _ = Describe("ArgoCDCommitStatus Controller", func() {
	Context("URL templating functionality", func() {

		FIt("should successfully reconcile the resource", func() {
			const argocdCSLabel = "argocd-health"
			const namespace = "default"

			// Skip("Skipping test because of flakiness")
			By("Creating the resource")
			plainName := "promotion-strategy-with-active-commit-status-argocdcommitstatus"
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy := promotionStrategyResource(ctx, plainName, "default")

			typeNamespacedName := types.NamespacedName{
				Name:      name,
				Namespace: "default",
			}

			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{
					Key: argocdCSLabel,
				},
			}

			argocdCommitStatus := promoterv1alpha1.ArgoCDCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: namespace,
				},
				Spec: promoterv1alpha1.ArgoCDCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: promotionStrategy.Name,
					},
					ApplicationSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": plainName},
					},
					URLTemplate: "https://argocd.example.com/{{ .apps | len }}",
				},
			}

			argoCDAppDev, argoCDAppStaging, argoCDAppProduction := argocdApplications(namespace, plainName)

			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			Expect(k8sClient.Create(ctx, &argocdCommitStatus)).To(Succeed())
			Expect(k8sClient.Create(ctx, &argoCDAppDev)).To(Succeed())
			Expect(k8sClient.Create(ctx, &argoCDAppStaging)).To(Succeed())
			Expect(k8sClient.Create(ctx, &argoCDAppProduction)).To(Succeed())

			By("Checking that the CommitStatus for each environment is created from ArgoCDCommitStatus")

			for _, environment := range promotionStrategy.Spec.Environments {
				commitStatus := promoterv1alpha1.CommitStatus{}
				commitStatusName := environment.Branch + "/health"
				resourceName := strings.ReplaceAll(commitStatusName, "/", "-") + "-" + hash([]byte(argocdCommitStatus.Name))
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      resourceName,
						Namespace: argoCDAppDev.GetNamespace(),
					}, &commitStatus)
					g.Expect(err).To(Succeed())
					g.Expect(commitStatus).To(Equal("https://argocd.example.com/3"))
				}, EventuallyTimeout).Should(Succeed())
			}

			By("Getting the ArgoCDCommitStatus resource and confirming the URL is set correctly")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, &argocdCommitStatus)
				g.Expect(err).To(Succeed())
			})

			Expect(k8sClient.Delete(ctx, promotionStrategy)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &argocdCommitStatus)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &argoCDAppDev)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &argoCDAppStaging)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &argoCDAppProduction)).To(Succeed())
		})
	})
})
