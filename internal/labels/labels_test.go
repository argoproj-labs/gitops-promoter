package labels

import (
	"strings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ValidateLabelNames", func() {
	DescribeTable("validates label names",
		func(labels []string, shouldSucceed bool) {
			err := ValidateLabelNames(labels)
			if shouldSucceed {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(HaveOccurred())
			}
		},
		Entry("accepts valid labels", []string{"lgtm", "approved"}, true),
		Entry("accepts an empty list", nil, true),
		Entry("rejects an empty string label", []string{""}, false),
		Entry("rejects labels longer than 50 runes", []string{strings.Repeat("a", 51)}, false),
		Entry("accepts labels up to 50 runes", []string{strings.Repeat("\u00e9", 50)}, true),
		Entry("rejects labels with newlines", []string{"bad\nlabel"}, false),
		Entry("rejects labels with NUL", []string{"bad\x00label"}, false),
		Entry("rejects duplicate labels", []string{"a", "a"}, false),
		Entry("rejects more than 10 labels", func() []string {
			labels := make([]string, 11)
			for i := range labels {
				labels[i] = "label"
			}
			return labels
		}(), false),
	)
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
		Expect(err).To(MatchError(ContainSubstring("expression returned invalid label names")))
		Expect(err).To(MatchError(ContainSubstring("exceeds maximum length")))
	})

	It("rejects empty label names", func() {
		e := &Evaluator{}
		_, err := e.Evaluate(`['']`, ExpressionContext{})
		Expect(err).To(MatchError(ContainSubstring("expression returned invalid label names")))
		Expect(err).To(MatchError(ContainSubstring("must not be empty")))
	})

	It("rejects duplicate label names", func() {
		e := &Evaluator{}
		_, err := e.Evaluate(`['a', 'a']`, ExpressionContext{})
		Expect(err).To(MatchError(ContainSubstring("expression returned invalid label names")))
		Expect(err).To(MatchError(ContainSubstring(`duplicate label "a"`)))
	})

	It("rejects non-slice expression output", func() {
		e := &Evaluator{}
		_, err := e.Evaluate(`42`, ExpressionContext{})
		Expect(err).To(HaveOccurred())
	})

	It("returns an empty slice when the expression evaluates to []", func() {
		e := &Evaluator{}
		got, err := e.Evaluate(`[]`, ExpressionContext{})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]string{}))
		Expect(got).NotTo(BeNil())
	})

	It("rejects nil expression output", func() {
		e := &Evaluator{}
		_, err := e.Evaluate(`nil`, ExpressionContext{})
		Expect(err).To(MatchError("expression must return []string, got <nil>"))
	})

	It("rejects slices containing non-string elements", func() {
		e := &Evaluator{}
		_, err := e.Evaluate(`[1, 'lgtm']`, ExpressionContext{})
		Expect(err).To(MatchError("expression must return []string, got int at index 0"))
	})

	It("rejects expressions that fail to compile", func() {
		e := &Evaluator{}
		_, err := e.Evaluate(`Status.Invalid..Field`, ExpressionContext{})
		Expect(err).To(MatchError(ContainSubstring("failed to compile expression")))
	})

	It("rejects expressions that fail at evaluation time", func() {
		e := &Evaluator{}
		_, err := e.Evaluate(`Status.Proposed.CommitStatuses[0].Phase`, ExpressionContext{
			Status: promoterv1alpha1.ChangeTransferPolicyStatus{},
		})
		Expect(err).To(MatchError(ContainSubstring("failed to evaluate expression")))
	})
})
