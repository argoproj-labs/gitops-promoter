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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
)

// This spec runs the whole of tag-driven promotion together: the PromotionStrategy, its
// ChangeTransferPolicies and pull requests, the ValidatedCommitStatus that owns the environment tags, a
// CommitValidation for the environment a person approves, and hydrators that read the tags. Development
// follows the dry branch, staging is validated by the controller, and production by a person.
var _ = Describe("Tag-driven promotion from end to end", func() {
	It("takes commits through development and staging by themselves, and to production when a person approves them", func() {
		ctx := context.Background()
		p := newValidatedPipeline(ctx, "tag-driven-promotion")
		p.gate.Spec.Environments = []promoterv1alpha1.ValidatedCommitStatusEnvironment{
			{Branch: testBranchStaging, Advance: promoterv1alpha1.AdvanceWhenIdle},
			{Branch: testBranchProduction, Validation: promoterv1alpha1.ValidationManual},
		}
		p.create()
		approvals := commitValidationFor(p, testBranchProduction, "", "")
		first := p.dryHead()
		live := p.dryParent(first)

		readyForProduction := func(drySha string) {
			GinkgoHelper()
			Eventually(func(g Gomega) {
				cleared := p.environmentStatus(g, testBranchProduction).UpstreamCleared
				g.Expect(cleared).NotTo(BeNil())
				g.Expect(cleared.Sha).To(Equal(drySha))
			}, constants.EventuallyTimeout).Should(Succeed())
		}

		By("Taking the commit at the head of the dry branch into development, and then into staging")
		p.expectTag(testBranchDevelopment, first)
		p.promote(testBranchDevelopment)
		p.expectTag(testBranchStaging, first)
		p.promote(testBranchStaging)

		By("Reporting the commit as ready for production, and hydrating nothing for production until it is approved")
		readyForProduction(first)
		Consistently(func(g Gomega) {
			g.Expect(p.tag(testBranchProduction)).To(Equal(live))
			g.Expect(p.observed(g, testBranchProduction).Proposed.Dry.Sha).To(Equal(live))
		}, 3*time.Second, 500*time.Millisecond).Should(Succeed())

		By("Taking a second commit through development and staging while the first waits for approval")
		second := p.commitDry()
		p.expectTag(testBranchDevelopment, second)
		p.promote(testBranchDevelopment)
		p.expectTag(testBranchStaging, second)
		p.promote(testBranchStaging)
		readyForProduction(second)
		Expect(p.tag(testBranchProduction)).To(Equal(live))

		By("Approving the first commit for production: an approval is for a commit, not for whatever is newest")
		requestSha(ctx, approvals, first, "CHG-1 approved")
		expectRequest(ctx, approvals, first, promoterv1alpha1.CommitValidationRecorded)
		p.expectTag(testBranchProduction, first)
		p.expectTagSource(testBranchProduction, promoterv1alpha1.ValidatedTagSourceManual)
		p.promote(testBranchProduction)

		By("Approving the second")
		requestSha(ctx, approvals, second, "CHG-2 approved")
		expectRequest(ctx, approvals, second, promoterv1alpha1.CommitValidationRecorded)
		p.expectTag(testBranchProduction, second)
		p.promote(testBranchProduction)

		By("Keeping the record of both approvals, newest first")
		Eventually(func(g Gomega) {
			var current promoterv1alpha1.CommitValidation
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(approvals), &current)).To(Succeed())
			g.Expect(current.Status.History).To(HaveLen(2))
			g.Expect(current.Status.History[0].Sha).To(Equal(second))
			g.Expect(current.Status.History[0].Reason).To(Equal("CHG-2 approved"))
			g.Expect(current.Status.History[1].Sha).To(Equal(first))
			g.Expect(current.Status.History[1].Reason).To(Equal("CHG-1 approved"))
		}, constants.EventuallyTimeout).Should(Succeed())

		By("Ending with every environment on the second commit and everything Ready")
		for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
			p.expectLive(branch, second)
		}
		p.expectReady()
		Eventually(func(g Gomega) {
			var ps promoterv1alpha1.PromotionStrategy
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(p.promotionStrategy), &ps)).To(Succeed())
			ready := meta.FindStatusCondition(ps.Status.Conditions, string(promoterConditions.Ready))
			g.Expect(ready).NotTo(BeNil())
			g.Expect(ready.Status).To(Equal(metav1.ConditionTrue), ready.Message)
		}, constants.EventuallyTimeout).Should(Succeed())
	})
})
