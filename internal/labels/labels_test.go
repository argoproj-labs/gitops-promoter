package labels

import (
	"strings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ValidateLabelNames", func() {
	It("accepts valid labels", func() {
		Expect(ValidateLabelNames([]string{"lgtm", "approved"})).To(Succeed())
	})

	It("accepts an empty list", func() {
		Expect(ValidateLabelNames(nil)).To(Succeed())
	})

	It("rejects an empty string label", func() {
		Expect(ValidateLabelNames([]string{""})).NotTo(Succeed())
	})

	It("rejects labels longer than 50 runes", func() {
		Expect(ValidateLabelNames([]string{strings.Repeat("a", 51)})).NotTo(Succeed())
	})

	It("accepts labels up to 50 runes", func() {
		Expect(ValidateLabelNames([]string{strings.Repeat("\u00e9", 50)})).To(Succeed())
	})

	It("rejects labels with newlines or NUL", func() {
		Expect(ValidateLabelNames([]string{"bad\nlabel"})).NotTo(Succeed())
		Expect(ValidateLabelNames([]string{"bad\x00label"})).NotTo(Succeed())
	})

	It("rejects duplicate labels", func() {
		Expect(ValidateLabelNames([]string{"a", "a"})).NotTo(Succeed())
	})

	It("rejects more than 10 labels", func() {
		labels := make([]string, 11)
		for i := range labels {
			labels[i] = "label"
		}
		Expect(ValidateLabelNames(labels)).NotTo(Succeed())
	})
})

var _ = Describe("SetsEqual", func() {
	It("compares label sets regardless of order", func() {
		Expect(SetsEqual([]string{"b", "a"}, []string{"a", "b"})).To(BeTrue())
		Expect(SetsEqual([]string{"a"}, []string{"b"})).To(BeFalse())
	})
})

var _ = Describe("Diff", func() {
	It("returns labels to add and remove", func() {
		toAdd, toRemove := Diff([]string{"a", "c"}, []string{"a", "b"})
		Expect(toAdd).To(Equal([]string{"c"}))
		Expect(toRemove).To(Equal([]string{"b"}))
	})
})

var _ = Describe("ObservedManaged", func() {
	It("returns managed labels present on the SCM", func() {
		got := ObservedManaged(
			[]string{"lgtm", "approved"},
			[]string{"lgtm", "approved"},
			[]string{"approved", "tide/merge"},
		)
		Expect(got).To(Equal([]string{"approved"}))
	})

	It("keeps labels pending removal when spec shrinks", func() {
		got := ObservedManaged(
			[]string{"lgtm"},
			[]string{"lgtm", "approved"},
			[]string{"lgtm", "approved"},
		)
		Expect(got).To(Equal([]string{"approved", "lgtm"}))
	})

	It("returns nil when nothing is managed", func() {
		Expect(ObservedManaged(nil, nil, []string{"tide/merge"})).To(BeNil())
	})
})

var _ = Describe("Evaluator", func() {
	It("evaluates a conditional expression", func() {
		e := &Evaluator{}
		ctx := ExpressionContext{
			Status: promoterv1alpha1.ChangeTransferPolicyStatus{
				Proposed: promoterv1alpha1.CommitBranchState{
					CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						{Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
					},
				},
			},
		}

		got, err := e.Evaluate(
			`len(Status.Proposed.CommitStatuses) > 0 && all(Status.Proposed.CommitStatuses, {.Phase == 'success'}) ? ['lgtm', 'approved'] : []`,
			ctx,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]string{"lgtm", "approved"}))
	})

	It("allows PromotionStrategy to be nil at runtime", func() {
		e := &Evaluator{}
		got, err := e.Evaluate(`PromotionStrategy == nil ? ['lgtm'] : ['other']`, ExpressionContext{})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]string{"lgtm"}))
	})

	It("rejects label names that exceed max length", func() {
		e := &Evaluator{}
		_, err := e.Evaluate(`['`+strings.Repeat("a", 51)+`']`, ExpressionContext{})
		Expect(err).To(HaveOccurred())
	})

	It("rejects non-slice expression output", func() {
		e := &Evaluator{}
		_, err := e.Evaluate(`42`, ExpressionContext{})
		Expect(err).To(HaveOccurred())
	})
})
