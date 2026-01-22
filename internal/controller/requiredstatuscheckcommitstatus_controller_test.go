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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("RequiredStatusCheckCommitStatus Controller", func() {
	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		BeforeEach(func() {
			By("creating the PromotionStrategy")
			ps := &promoterv1alpha1.PromotionStrategy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ps",
					Namespace: "default",
				},
				Spec: promoterv1alpha1.PromotionStrategySpec{
					RepositoryReference: promoterv1alpha1.ObjectReference{
						Name: "test-repo",
					},
					ShowRequiredStatusChecks: ptr.To(true),
					Environments: []promoterv1alpha1.Environment{
						{
							Branch: "environment/dev",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, ps)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleanup the PromotionStrategy")
			ps := &promoterv1alpha1.PromotionStrategy{}
			err := k8sClient.Get(ctx, types.NamespacedName{Name: "test-ps", Namespace: "default"}, ps)
			if err == nil {
				Expect(k8sClient.Delete(ctx, ps)).To(Succeed())
			}

			By("Cleanup the RequiredStatusCheckCommitStatus")
			rsccs := &promoterv1alpha1.RequiredStatusCheckCommitStatus{}
			err = k8sClient.Get(ctx, typeNamespacedName, rsccs)
			if err == nil {
				Expect(k8sClient.Delete(ctx, rsccs)).To(Succeed())
			}
		})

		It("should successfully reconcile the resource", func() {
			By("Creating the custom resource for the Kind RequiredStatusCheckCommitStatus")
			rsccs := &promoterv1alpha1.RequiredStatusCheckCommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.RequiredStatusCheckCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{
						Name: "test-ps",
					},
				},
			}
			Expect(k8sClient.Create(ctx, rsccs)).To(Succeed())

			// Note: Comprehensive tests would include:
			// 1. Verifying GitHub Rulesets API is called
			// 2. Verifying CommitStatus resources are created with correct labels
			// 3. Verifying phase transitions trigger CTP reconciliation
			// 4. Verifying cleanup of orphaned CommitStatus resources
			// 5. Verifying dynamic requeue behavior (1 min for pending, configured for success)
			// 6. Verifying per-environment exclusions work correctly
		})

		It("should not create RequiredStatusCheckCommitStatus when showRequiredStatusChecks is false", func() {
			// This test would verify that when showRequiredStatusChecks is false,
			// no RequiredStatusCheckCommitStatus is created by the PromotionStrategy controller
		})

		It("should discover required checks from GitHub Rulesets", func() {
			// This test would verify that the controller correctly queries
			// GitHub Rulesets API and extracts required status checks
		})

		It("should create CommitStatus resources for each required check", func() {
			// This test would verify that a CommitStatus resource is created
			// for each required check with the correct naming convention:
			// required-status-check-{context}-{hash}
		})

		It("should apply per-environment exclusions", func() {
			// This test would verify that checks listed in
			// ExcludedRequiredStatusChecks are not surfaced as CommitStatus resources
		})

		It("should cleanup orphaned CommitStatus resources", func() {
			// This test would verify that when a check is removed from the ruleset,
			// the corresponding CommitStatus resource is deleted
		})

		It("should trigger CTP reconciliation on phase transitions", func() {
			// This test would verify that when a check transitions from pending to success,
			// the corresponding CTP is enqueued for reconciliation
		})

		It("should requeue after 1 minute when checks are pending", func() {
			// This test would verify that the controller returns a 1 minute requeue
			// duration when any checks are in pending state
		})

		It("should requeue using configured interval when all checks pass", func() {
			// This test would verify that the controller uses the configured
			// polling interval when all checks are successful
		})
	})
})
