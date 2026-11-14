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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/internal/validations"
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

// commitValidationFor creates a CommitValidation for one environment of the pipeline.
func commitValidationFor(p *validatedPipeline, branch, sha, reason string) *promoterv1alpha1.CommitValidation {
	GinkgoHelper()
	commitValidation := &promoterv1alpha1.CommitValidation{
		Name:      p.name + "-" + branch[strings.LastIndex(branch, "/")+1:],
		Namespace: "default",
		Spec: promoterv1alpha1.CommitValidationSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: p.name},
			Branch:               branch,
			Sha:                  sha,
			Reason:               reason,
		},
	}
	Expect(k8sClient.Create(p.ctx, commitValidation)).To(Succeed())
	DeferCleanup(func() { _ = k8sClient.Delete(p.ctx, commitValidation) })
	return commitValidation
}

// requestSha sets, or with "" clears, the commit a CommitValidation asks to validate. The API server
// refuses a new SHA until the controller has cleared the outcome of the last one, so this retries.
func requestSha(ctx context.Context, commitValidation *promoterv1alpha1.CommitValidation, sha, reason string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var current promoterv1alpha1.CommitValidation
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(commitValidation), &current)).To(Succeed())
		current.Spec.Sha = sha
		current.Spec.Reason = reason
		g.Expect(k8sClient.Update(ctx, &current)).To(Succeed())
	}, constants.EventuallyTimeout).Should(Succeed())
}

// expectRequest waits for a CommitValidation to report the outcome of the SHA in its spec.
func expectRequest(ctx context.Context, commitValidation *promoterv1alpha1.CommitValidation, sha string, phase promoterv1alpha1.CommitValidationPhase) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		var current promoterv1alpha1.CommitValidation
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(commitValidation), &current)).To(Succeed())
		g.Expect(current.Status.Request).NotTo(BeNil())
		g.Expect(current.Status.Request.Sha).To(Equal(sha))
		g.Expect(current.Status.Request.Phase).To(Equal(phase))
		g.Expect(current.Status.Request.HandledAt.IsZero()).To(BeFalse())
		g.Expect(current.Status.ObservedGeneration).To(Equal(current.Generation))
		ready := meta.FindStatusCondition(current.Status.Conditions, string(promoterConditions.Ready))
		g.Expect(ready).NotTo(BeNil())
		g.Expect(ready.Status).To(Equal(metav1.ConditionTrue), ready.Message)
	}, constants.EventuallyTimeout).Should(Succeed())
}

var _ = Describe("CommitValidation Controller", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	history := func(g Gomega, commitValidation *promoterv1alpha1.CommitValidation) []promoterv1alpha1.CommitValidationHistoryEntry {
		var current promoterv1alpha1.CommitValidation
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(commitValidation), &current)).To(Succeed())
		return current.Status.History
	}

	Context("When a commit is validated for an environment", func() {
		It("records the validation in git, has it applied at once, and shows the environment's history", func() {
			p := newValidatedPipeline(ctx, "commit-validation")
			p.gate.Spec.Environments = []promoterv1alpha1.ValidatedCommitStatusEnvironment{
				{Branch: testBranchProduction, Validation: promoterv1alpha1.ValidationManual},
			}
			p.create()
			first := p.dryHead()
			live := p.dryParent(first)
			p.expectTag(testBranchDevelopment, first)
			p.promote(testBranchDevelopment)
			p.expectTag(testBranchStaging, first)
			p.expectTag(testBranchProduction, live)

			By("Asking for the commit to be validated for production")
			production := commitValidationFor(p, testBranchProduction, first, "CHG-1234 approved")
			expectRequest(ctx, production, first, promoterv1alpha1.CommitValidationRecorded)

			By("Finding the validation on the dry commit, under the name of the resource")
			Expect(p.validationsOn(first)).To(ContainElement(And(
				HaveField("Target", "environment/production-tag"),
				HaveField("Source", validations.SourceResource),
				HaveField("By", "CommitValidation/"+production.Name),
				HaveField("Reason", "CHG-1234 approved"),
			)))

			By("Moving production's tag without waiting for the gate to poll")
			p.expectTag(testBranchProduction, first)
			p.expectTagSource(testBranchProduction, promoterv1alpha1.ValidatedTagSourceManual)

			By("Showing the validation in the history")
			Eventually(func(g Gomega) {
				entries := history(g, production)
				g.Expect(entries).To(HaveLen(1))
				g.Expect(entries[0].Sha).To(Equal(first))
				g.Expect(entries[0].Source).To(Equal(promoterv1alpha1.CommitValidationSourceResource))
				g.Expect(entries[0].ValidatedBy).To(Equal("CommitValidation/" + production.Name))
				g.Expect(entries[0].Reason).To(Equal("CHG-1234 approved"))
				g.Expect(entries[0].ValidatedAt.IsZero()).To(BeFalse())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Adding a validation pushed as a git note to the history, newest first, with its author")
			p.validateByHand(testBranchProduction, live, "roll back")
			p.expectTag(testBranchProduction, live)
			Eventually(func(g Gomega) {
				entries := history(g, production)
				g.Expect(entries).To(HaveLen(2))
				g.Expect(entries[0].Sha).To(Equal(live))
				g.Expect(entries[0].Source).To(Equal(promoterv1alpha1.CommitValidationSourceGit))
				g.Expect(entries[0].ValidatedBy).To(Equal("testmail@test.com"))
				g.Expect(entries[0].Reason).To(Equal("roll back"))
				g.Expect(entries[1].Sha).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Clearing the request once the SHA is taken out of the spec")
			requestSha(ctx, production, "", "")
			Eventually(func(g Gomega) {
				var current promoterv1alpha1.CommitValidation
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(production), &current)).To(Succeed())
				g.Expect(current.Status.Request).To(BeNil())
				g.Expect(current.Status.History).To(HaveLen(2), "the history is the environment's, not the request's")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Validating the same commit a second time, which is a new validation")
			requestSha(ctx, production, first, "CHG-1240 approved")
			expectRequest(ctx, production, first, promoterv1alpha1.CommitValidationRecorded)
			p.expectTag(testBranchProduction, first)
			Eventually(func(g Gomega) {
				entries := history(g, production)
				g.Expect(entries).To(HaveLen(3))
				g.Expect(entries[0].Reason).To(Equal("CHG-1240 approved"))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("shows the history of an environment without asking for anything", func() {
			p := newValidatedPipeline(ctx, "commit-validation-viewer")
			p.create()
			first := p.dryHead()
			live := p.dryParent(first)
			p.expectTag(testBranchDevelopment, first)
			p.promote(testBranchDevelopment)
			p.expectTag(testBranchStaging, first)

			staging := commitValidationFor(p, testBranchStaging, "", "")
			Eventually(func(g Gomega) {
				var current promoterv1alpha1.CommitValidation
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(staging), &current)).To(Succeed())
				g.Expect(current.Status.Request).To(BeNil())
				ready := meta.FindStatusCondition(current.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionTrue), ready.Message)

				g.Expect(current.Status.History).To(HaveLen(2))
				g.Expect(current.Status.History[0].Sha).To(Equal(first))
				g.Expect(current.Status.History[0].Source).To(Equal(promoterv1alpha1.CommitValidationSourceAutomatic))
				g.Expect(current.Status.History[0].ValidatedBy).To(Equal("environment/development successful"))
				g.Expect(current.Status.History[1].Sha).To(Equal(live))
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(p.tag(testBranchStaging)).To(Equal(first))
		})
	})

	Context("When the commit does not exist in the repository", func() {
		It("rejects the request, writes nothing, and takes the next request", func() {
			p := newValidatedPipeline(ctx, "commit-validation-rejected")
			p.gate.Spec.Environments = []promoterv1alpha1.ValidatedCommitStatusEnvironment{
				{Branch: testBranchProduction, Validation: promoterv1alpha1.ValidationManual},
			}
			p.create()
			first := p.dryHead()
			live := p.dryParent(first)
			p.expectTag(testBranchProduction, live)

			missing := strings.Repeat("ab", 20)
			production := commitValidationFor(p, testBranchProduction, missing, "mistyped")
			expectRequest(ctx, production, missing, promoterv1alpha1.CommitValidationRejected)
			Eventually(func(g Gomega) {
				var current promoterv1alpha1.CommitValidation
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(production), &current)).To(Succeed())
				g.Expect(current.Status.Request.Message).To(Equal("commit " + missing + " does not exist in the repository"))
				g.Expect(current.Status.History).To(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(p.validationsOn(missing)).To(BeEmpty())
			Expect(p.tag(testBranchProduction)).To(Equal(live))

			By("Asking for a commit that does exist")
			requestSha(ctx, production, first, "the right one")
			expectRequest(ctx, production, first, promoterv1alpha1.CommitValidationRecorded)
			p.expectTag(testBranchProduction, first)
		})
	})

	Context("When the PromotionStrategy has no ValidatedCommitStatus", func() {
		It("keeps the request, lets nobody replace it, and records it once there is a gate", func() {
			p := newValidatedPipeline(ctx, "commit-validation-no-gate")
			declareDependentsSuccessfulGate(p.promotionStrategy)
			old := &promoterv1alpha1.DependentsSuccessfulCommitStatus{
				Name: p.name, Namespace: "default",
				Spec: promoterv1alpha1.DependentsSuccessfulCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: p.name},
					Key:                  promoterv1alpha1.DependentsSuccessfulCommitStatusKey,
				},
			}
			Expect(k8sClient.Create(ctx, old)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, old) })
			Expect(k8sClient.Create(ctx, p.scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, p.scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, p.gitRepo)).To(Succeed())
			Expect(k8sClient.Create(ctx, p.promotionStrategy)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, p.promotionStrategy)
				_ = k8sClient.Delete(ctx, p.gate)
				_ = k8sClient.Delete(ctx, p.gitRepo)
				_ = k8sClient.Delete(ctx, p.scmProvider)
				_ = k8sClient.Delete(ctx, p.scmSecret)
			})
			first := p.dryHead()
			live := p.dryParent(first)

			production := commitValidationFor(p, testBranchProduction, first, "early")
			Eventually(func(g Gomega) {
				var current promoterv1alpha1.CommitValidation
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(production), &current)).To(Succeed())
				ready := meta.FindStatusCondition(current.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ready.Reason).To(Equal(string(promoterConditions.ValidatedCommitStatusNotFound)))
				g.Expect(current.Status.Request).To(BeNil(), "the request is neither recorded nor rejected")
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(p.validationsOn(first)).To(BeEmpty())

			By("Refusing a second SHA while the first has not been handled")
			var current promoterv1alpha1.CommitValidation
			Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(production), &current)).To(Succeed())
			current.Spec.Sha = live
			err := k8sClient.Update(ctx, &current)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.sha cannot change until status.request reports the current SHA as handled"))

			By("Recording the request when a ValidatedCommitStatus is created")
			Expect(k8sClient.Create(ctx, p.gate)).To(Succeed())
			expectRequest(ctx, production, first, promoterv1alpha1.CommitValidationRecorded)
			p.expectTag(testBranchProduction, first)
			p.expectTagSource(testBranchProduction, promoterv1alpha1.ValidatedTagSourceManual)
		})
	})

	Context("When its branch is not an environment of the PromotionStrategy", func() {
		It("says so, and records nothing", func() {
			p := newValidatedPipeline(ctx, "commit-validation-bad-branch")
			p.create()
			first := p.dryHead()
			p.expectTag(testBranchDevelopment, first)

			stray := commitValidationFor(p, "environment/qa", first, "")
			Eventually(func(g Gomega) {
				var current promoterv1alpha1.CommitValidation
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(stray), &current)).To(Succeed())
				ready := meta.FindStatusCondition(current.Status.Conditions, string(promoterConditions.Ready))
				g.Expect(ready).NotTo(BeNil())
				g.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(ready.Message).To(ContainSubstring(`spec.branch "environment/qa" is not an environment of PromotionStrategy`))
				g.Expect(current.Status.Request).To(BeNil())
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})
