package reviewers

import (
	"strings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func user(name string) promoterv1alpha1.PullRequestReviewer {
	return promoterv1alpha1.PullRequestReviewer{User: name}
}

func group(name string) promoterv1alpha1.PullRequestReviewer {
	return promoterv1alpha1.PullRequestReviewer{Group: name}
}

var _ = Describe("Validate", func() {
	DescribeTable("validates reviewers",
		func(list []promoterv1alpha1.PullRequestReviewer, shouldSucceed bool) {
			err := Validate(list)
			if shouldSucceed {
				Expect(err).NotTo(HaveOccurred())
			} else {
				Expect(err).To(HaveOccurred())
			}
		},
		Entry("accepts users and groups", []promoterv1alpha1.PullRequestReviewer{user("alice"), group("release-managers")}, true),
		Entry("accepts an empty list", nil, true),
		Entry("rejects a reviewer with neither field set", []promoterv1alpha1.PullRequestReviewer{{}}, false),
		Entry("rejects a reviewer with both fields set", []promoterv1alpha1.PullRequestReviewer{{User: "alice", Group: "team"}}, false),
		Entry("rejects names longer than 100 runes", []promoterv1alpha1.PullRequestReviewer{user(strings.Repeat("a", 101))}, false),
		Entry("accepts names up to 100 runes", []promoterv1alpha1.PullRequestReviewer{user(strings.Repeat("a", 100))}, true),
		Entry("rejects names with whitespace", []promoterv1alpha1.PullRequestReviewer{user("bad name")}, false),
		Entry("rejects names with NUL", []promoterv1alpha1.PullRequestReviewer{user("bad\x00name")}, false),
		Entry("rejects duplicates", []promoterv1alpha1.PullRequestReviewer{user("alice"), user("alice")}, false),
		Entry("accepts a user and a group of the same name", []promoterv1alpha1.PullRequestReviewer{user("alice"), group("alice")}, true),
		Entry("rejects more than 10 reviewers", func() []promoterv1alpha1.PullRequestReviewer {
			list := make([]promoterv1alpha1.PullRequestReviewer, 11)
			for i := range list {
				list[i] = user(strings.Repeat("a", i+1))
			}
			return list
		}(), false),
	)
})

var _ = Describe("SetsEqual", func() {
	It("ignores order", func() {
		Expect(SetsEqual(
			[]promoterv1alpha1.PullRequestReviewer{user("alice"), group("sre")},
			[]promoterv1alpha1.PullRequestReviewer{group("sre"), user("alice")},
		)).To(BeTrue())
	})

	It("distinguishes a user from a group of the same name", func() {
		Expect(SetsEqual(
			[]promoterv1alpha1.PullRequestReviewer{user("alice")},
			[]promoterv1alpha1.PullRequestReviewer{group("alice")},
		)).To(BeFalse())
	})

	It("treats nil and empty as equal", func() {
		Expect(SetsEqual(nil, []promoterv1alpha1.PullRequestReviewer{})).To(BeTrue())
	})
})

var _ = Describe("Diff", func() {
	It("returns additions and removals", func() {
		toAdd, toRemove := Diff(
			[]promoterv1alpha1.PullRequestReviewer{user("alice"), group("sre")},
			[]promoterv1alpha1.PullRequestReviewer{user("alice"), user("bob")},
		)
		Expect(toAdd).To(Equal([]promoterv1alpha1.PullRequestReviewer{group("sre")}))
		Expect(toRemove).To(Equal([]promoterv1alpha1.PullRequestReviewer{user("bob")}))
	})

	It("removes everything when the desired set is empty", func() {
		toAdd, toRemove := Diff(nil, []promoterv1alpha1.PullRequestReviewer{user("alice")})
		Expect(toAdd).To(BeEmpty())
		Expect(toRemove).To(Equal([]promoterv1alpha1.PullRequestReviewer{user("alice")}))
	})

	It("only removes reviewers the promoter applied", func() {
		toAdd, toRemove := Diff([]promoterv1alpha1.PullRequestReviewer{user("alice")}, nil)
		Expect(toAdd).To(Equal([]promoterv1alpha1.PullRequestReviewer{user("alice")}))
		Expect(toRemove).To(BeEmpty())
	})
})

var _ = Describe("Evaluator", func() {
	// The expression proposed on argoproj-labs/gitops-promoter#1881: per-branch reviewers, no
	// reviewers when the environment auto-merges, users as bare strings and groups as objects.
	const issueExpression = `
		let branch = Spec.ActiveBranch;
		let autoMerge = Spec.AutoMerge ?? true;
		autoMerge ? [] :
			branch == 'environment/production' ? [
				'alice',
				{group: 'release-managers'},
			] :
			branch == 'environment/staging' ? [
				'charlie',
			] :
			[]`

	specFor := func(branch string, autoMerge *bool) promoterv1alpha1.ChangeTransferPolicySpec {
		return promoterv1alpha1.ChangeTransferPolicySpec{ActiveBranch: branch, AutoMerge: autoMerge}
	}

	It("resolves per-branch reviewers", func() {
		e := &Evaluator{}
		got, err := e.Evaluate(issueExpression, ExpressionContext{Spec: specFor("environment/production", new(false))})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]promoterv1alpha1.PullRequestReviewer{user("alice"), group("release-managers")}))

		got, err = e.Evaluate(issueExpression, ExpressionContext{Spec: specFor("environment/staging", new(false))})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]promoterv1alpha1.PullRequestReviewer{user("charlie")}))
	})

	It("returns no reviewers for an auto-merged environment", func() {
		e := &Evaluator{}
		got, err := e.Evaluate(issueExpression, ExpressionContext{Spec: specFor("environment/production", new(true))})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
	})

	It("treats an unset autoMerge as true", func() {
		e := &Evaluator{}
		got, err := e.Evaluate(issueExpression, ExpressionContext{Spec: specFor("environment/production", nil)})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeEmpty())
	})

	It("allows PromotionStrategy to be nil at runtime", func() {
		e := &Evaluator{}
		got, err := e.Evaluate(`PromotionStrategy == nil ? ['alice'] : ['bob']`, ExpressionContext{})
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal([]promoterv1alpha1.PullRequestReviewer{user("alice")}))
	})

	It("reuses the compiled program across evaluations", func() {
		e := &Evaluator{}
		for range 2 {
			got, err := e.Evaluate(`['alice']`, ExpressionContext{})
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal([]promoterv1alpha1.PullRequestReviewer{user("alice")}))
		}
	})

	DescribeTable("rejects malformed output",
		func(expression string) {
			e := &Evaluator{}
			_, err := e.Evaluate(expression, ExpressionContext{})
			Expect(err).To(HaveOccurred())
		},
		Entry("a bare string instead of a list", `'alice'`),
		Entry("a non-string list item", `[1]`),
		Entry("an object with an unsupported key", `[{team: 'sre'}]`),
		Entry("an object with more than one key", `[{user: 'alice', group: 'sre'}]`),
		Entry("an object with a non-string value", `[{user: 1}]`),
		Entry("an empty username", `['']`),
		Entry("a name containing whitespace", `['alice smith']`),
		Entry("duplicate reviewers", `['alice', 'alice']`),
	)

	It("returns a compile error for an invalid expression", func() {
		e := &Evaluator{}
		_, err := e.Evaluate(`this is not expr`, ExpressionContext{})
		Expect(err).To(HaveOccurred())
	})
})
