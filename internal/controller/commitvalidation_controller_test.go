/*
Copyright 2026.

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
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

//go:embed testdata/CommitValidation.yaml
var testCommitValidationYAML string

const (
	testValidationShaOne   = "1111111111111111111111111111111111111111"
	testValidationShaTwo   = "2222222222222222222222222222222222222222"
	testValidationShaThree = "3333333333333333333333333333333333333333"
)

var _ = Describe("CommitValidation API", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the CommitValidation resource", func() {
			err := unmarshalYamlStrict(testCommitValidationYAML, &promoterv1alpha1.CommitValidation{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	// CRD validation only, so these run against the controller-free dev envtest cluster. With no
	// controller there, each spec plays the controller by writing status.request itself.
	Context("When validating the spec", func() {
		var ctx context.Context

		newValidation := func(sha, reason string) *promoterv1alpha1.CommitValidation {
			return &promoterv1alpha1.CommitValidation{
				Name:      "cv-validation-" + utils.KubeSafeUniqueName(randomString(10)),
				Namespace: "default",
				Spec: promoterv1alpha1.CommitValidationSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "does-not-exist"},
					Branch:               testBranchProduction,
					Sha:                  sha,
					Reason:               reason,
				},
			}
		}

		create := func(cv *promoterv1alpha1.CommitValidation) {
			GinkgoHelper()
			Expect(k8sClientDev.Create(ctx, cv)).To(Succeed())
			DeferCleanup(func() { _ = k8sClientDev.Delete(ctx, cv) })
		}

		// updateSpec applies mutate to the live object and returns the API server's verdict.
		updateSpec := func(cv *promoterv1alpha1.CommitValidation, mutate func(*promoterv1alpha1.CommitValidationSpec)) error {
			GinkgoHelper()
			Expect(k8sClientDev.Get(ctx, client.ObjectKeyFromObject(cv), cv)).To(Succeed())
			mutate(&cv.Spec)
			//nolint:wrapcheck // The spec asserts on the API server's error as returned.
			return k8sClientDev.Update(ctx, cv)
		}

		// handle records status.request for the SHA currently in spec, as the controller would.
		handle := func(cv *promoterv1alpha1.CommitValidation, phase promoterv1alpha1.CommitValidationPhase) {
			GinkgoHelper()
			Expect(k8sClientDev.Get(ctx, client.ObjectKeyFromObject(cv), cv)).To(Succeed())
			cv.Status.Request = &promoterv1alpha1.CommitValidationRequestStatus{
				Sha:       cv.Spec.Sha,
				Phase:     phase,
				HandledAt: metav1.Now(),
			}
			Expect(k8sClientDev.Status().Update(ctx, cv)).To(Succeed())
		}

		// clearRequest removes status.request, as the controller does once spec.sha is gone.
		clearRequest := func(cv *promoterv1alpha1.CommitValidation) {
			GinkgoHelper()
			Expect(k8sClientDev.Get(ctx, client.ObjectKeyFromObject(cv), cv)).To(Succeed())
			cv.Status.Request = nil
			Expect(k8sClientDev.Status().Update(ctx, cv)).To(Succeed())
		}

		expectRejected := func(err error, message string) {
			GinkgoHelper()
			Expect(errors.IsInvalid(err)).To(BeTrue(), "expected an Invalid error, got %v", err)
			Expect(err).To(MatchError(ContainSubstring(message)))
		}

		BeforeEach(func() {
			ctx = context.Background()
		})

		It("accepts a resource without a sha, to view the environment's history", func() {
			create(newValidation("", ""))
		})

		It("rejects a reason without a sha", func() {
			err := k8sClientDev.Create(ctx, newValidation("", "CHG-1 approved"))
			expectRejected(err, "reason requires sha")
		})

		It("rejects a sha that is not a full commit SHA", func() {
			err := k8sClientDev.Create(ctx, newValidation("4f2a9c", ""))
			Expect(errors.IsInvalid(err)).To(BeTrue(), "expected an Invalid error, got %v", err)
		})

		It("rejects changes to branch and promotionStrategyRef", func() {
			cv := newValidation("", "")
			create(cv)

			err := updateSpec(cv, func(spec *promoterv1alpha1.CommitValidationSpec) { spec.Branch = testBranchStaging })
			expectRejected(err, "promotionStrategyRef and branch are immutable")

			err = updateSpec(cv, func(spec *promoterv1alpha1.CommitValidationSpec) { spec.PromotionStrategyRef.Name = "another-strategy" })
			expectRejected(err, "promotionStrategyRef and branch are immutable")
		})

		It("lets a first sha be set on a resource that had none", func() {
			cv := newValidation("", "")
			create(cv)

			Expect(updateSpec(cv, func(spec *promoterv1alpha1.CommitValidationSpec) {
				spec.Sha = testValidationShaOne
				spec.Reason = "CHG-1 approved"
			})).To(Succeed())
		})

		It("blocks a second sha until the first is handled", func() {
			cv := newValidation(testValidationShaOne, "")
			create(cv)

			By("Rejecting a change while status.request is empty")
			err := updateSpec(cv, func(spec *promoterv1alpha1.CommitValidationSpec) { spec.Sha = testValidationShaTwo })
			expectRejected(err, "spec.sha cannot change until status.request reports the current SHA as handled")

			By("Rejecting the removal of an unhandled sha")
			err = updateSpec(cv, func(spec *promoterv1alpha1.CommitValidationSpec) { spec.Sha = "" })
			expectRejected(err, "spec.sha cannot change until status.request reports the current SHA as handled")

			By("Allowing the change once the current sha is handled")
			handle(cv, promoterv1alpha1.CommitValidationRecorded)
			Expect(updateSpec(cv, func(spec *promoterv1alpha1.CommitValidationSpec) { spec.Sha = testValidationShaTwo })).To(Succeed())

			By("Blocking a third sha, because status.request still reports the first")
			err = updateSpec(cv, func(spec *promoterv1alpha1.CommitValidationSpec) { spec.Sha = testValidationShaThree })
			expectRejected(err, "spec.sha cannot change until status.request reports the current SHA as handled")

			By("Allowing it once the second is handled")
			handle(cv, promoterv1alpha1.CommitValidationRecorded)
			Expect(updateSpec(cv, func(spec *promoterv1alpha1.CommitValidationSpec) { spec.Sha = testValidationShaThree })).To(Succeed())
		})

		It("treats a rejected sha as handled", func() {
			cv := newValidation(testValidationShaOne, "")
			create(cv)
			handle(cv, promoterv1alpha1.CommitValidationRejected)

			Expect(updateSpec(cv, func(spec *promoterv1alpha1.CommitValidationSpec) { spec.Sha = testValidationShaTwo })).To(Succeed())
		})

		It("keeps reason and sha together as one request", func() {
			cv := newValidation(testValidationShaOne, "CHG-1 approved")
			create(cv)

			By("Rejecting a new reason for an unhandled sha")
			err := updateSpec(cv, func(spec *promoterv1alpha1.CommitValidationSpec) { spec.Reason = "CHG-2 approved" })
			expectRejected(err, "reason cannot change while sha stays the same")

			By("Rejecting a new reason for a handled sha too, since it would not be recorded")
			handle(cv, promoterv1alpha1.CommitValidationRecorded)
			err = updateSpec(cv, func(spec *promoterv1alpha1.CommitValidationSpec) { spec.Reason = "CHG-2 approved" })
			expectRejected(err, "reason cannot change while sha stays the same")

			err = updateSpec(cv, func(spec *promoterv1alpha1.CommitValidationSpec) { spec.Reason = "" })
			expectRejected(err, "reason cannot change while sha stays the same")

			By("Allowing a new reason together with a new sha")
			Expect(updateSpec(cv, func(spec *promoterv1alpha1.CommitValidationSpec) {
				spec.Sha = testValidationShaTwo
				spec.Reason = "CHG-2 approved"
			})).To(Succeed())
		})

		It("makes a cleared sha wait for status.request to be cleared before a new one is set", func() {
			cv := newValidation(testValidationShaOne, "CHG-1 approved")
			create(cv)
			handle(cv, promoterv1alpha1.CommitValidationRecorded)

			By("Allowing a handled sha to be cleared")
			Expect(updateSpec(cv, func(spec *promoterv1alpha1.CommitValidationSpec) {
				spec.Sha = ""
				spec.Reason = ""
			})).To(Succeed())

			By("Rejecting the same sha again while status.request still reports it, since it would look handled")
			err := updateSpec(cv, func(spec *promoterv1alpha1.CommitValidationSpec) { spec.Sha = testValidationShaOne })
			expectRejected(err, "spec.sha cannot be set until status.request for the previous SHA has been cleared")

			By("Allowing it once status.request is cleared")
			clearRequest(cv)
			Expect(updateSpec(cv, func(spec *promoterv1alpha1.CommitValidationSpec) { spec.Sha = testValidationShaOne })).To(Succeed())
		})

		It("always allows status updates and metadata changes", func() {
			cv := newValidation(testValidationShaOne, "")
			create(cv)
			handle(cv, promoterv1alpha1.CommitValidationRecorded)

			Expect(k8sClientDev.Get(ctx, client.ObjectKeyFromObject(cv), cv)).To(Succeed())
			cv.Status.History = []promoterv1alpha1.CommitValidationHistoryEntry{{
				Sha:         testValidationShaOne,
				Source:      promoterv1alpha1.CommitValidationSourceResource,
				ValidatedBy: "CommitValidation/" + cv.Name,
				ValidatedAt: metav1.Now(),
			}}
			Expect(k8sClientDev.Status().Update(ctx, cv)).To(Succeed())

			Expect(k8sClientDev.Get(ctx, client.ObjectKeyFromObject(cv), cv)).To(Succeed())
			cv.Labels = map[string]string{"example": "value"}
			Expect(k8sClientDev.Update(ctx, cv)).To(Succeed())
		})
	})
})
