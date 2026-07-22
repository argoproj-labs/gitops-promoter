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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
)

//go:embed testdata/PreviousEnvironmentCommitStatus.yaml
var testPreviousEnvironmentCommitStatusYAML string

var _ = Describe("PreviousEnvironmentCommitStatus Controller", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the PreviousEnvironmentCommitStatus resource", func() {
			err := unmarshalYamlStrict(testPreviousEnvironmentCommitStatusYAML, &promoterv1alpha1.PreviousEnvironmentCommitStatus{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("When reconciling a resource", func() {
		var (
			ctx               context.Context
			name              string
			scmSecret         *v1.Secret
			scmProvider       *promoterv1alpha1.ScmProvider
			gitRepo           *promoterv1alpha1.GitRepository
			promotionStrategy *promoterv1alpha1.PromotionStrategy
			pecs              *promoterv1alpha1.PreviousEnvironmentCommitStatus
		)

		BeforeEach(func() {
			ctx = context.Background()

			By("Setting up test git repository and PromotionStrategy")
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "previous-environment-commit-status-test", "default")

			// Configure an ActiveCommitStatus so the previous-environment logic engages.
			promotionStrategy.Spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
				{Key: "test-active"},
			}

			setupInitialTestGitRepoOnServer(ctx, gitRepo)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up test resources")
			if pecs != nil {
				_ = k8sClient.Delete(ctx, pecs)
			}
			if promotionStrategy != nil {
				_ = k8sClient.Delete(ctx, promotionStrategy)
			}
			if gitRepo != nil {
				_ = k8sClient.Delete(ctx, gitRepo)
			}
			if scmProvider != nil {
				_ = k8sClient.Delete(ctx, scmProvider)
			}
			if scmSecret != nil {
				_ = k8sClient.Delete(ctx, scmSecret)
			}
		})

		It("should reconcile and become ready", func() {
			By("Creating a PreviousEnvironmentCommitStatus referencing the PromotionStrategy")
			pecs = &promoterv1alpha1.PreviousEnvironmentCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.PreviousEnvironmentCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key: promoterv1alpha1.PreviousEnvironmentCommitStatusKey,
				},
			}
			Expect(k8sClient.Create(ctx, pecs)).To(Succeed())

			By("Waiting for the resource to reconcile and report Ready")
			Eventually(func(g Gomega) {
				updated := &promoterv1alpha1.PreviousEnvironmentCommitStatus{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pecs), updated)).To(Succeed())
				readyCondition := meta.FindStatusCondition(updated.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(readyCondition).ToNot(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should pass url.template through to the owned DAGCommitStatus", func() {
			const urlTemplate = "https://example.com/ps/{{ .PromotionStrategy.Name }}?env={{ .Environment }}"

			By("Creating a PreviousEnvironmentCommitStatus with a URL template")
			pecs = &promoterv1alpha1.PreviousEnvironmentCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.PreviousEnvironmentCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: name,
					},
					Key: promoterv1alpha1.PreviousEnvironmentCommitStatusKey,
					URL: promoterv1alpha1.URLConfig{
						Template: urlTemplate,
					},
				},
			}
			Expect(k8sClient.Create(ctx, pecs)).To(Succeed())

			By("Waiting for the owned DAGCommitStatus to receive the URL template")
			Eventually(func(g Gomega) {
				dag := &promoterv1alpha1.DAGCommitStatus{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: name}, dag)).To(Succeed())
				g.Expect(dag.Spec.URL.Template).To(Equal(urlTemplate))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})
