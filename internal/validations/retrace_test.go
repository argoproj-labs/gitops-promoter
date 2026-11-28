package validations_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/validations"
)

var _ = Describe("Retrace: a tag that is not where the controller recorded it", func() {
	var (
		ctx   context.Context
		graph commitGraph
	)

	const (
		byAutomatic = promoterv1alpha1.ValidatedTagSourceAutomatic
		byManual    = promoterv1alpha1.ValidatedTagSourceManual
	)

	retrace := func(in validations.Input, found string) (validations.Retraced, bool) {
		GinkgoHelper()
		retraced, explained, err := validations.Retrace(ctx, graph, in, found)
		Expect(err).NotTo(HaveOccurred())
		return retraced, explained
	}

	BeforeEach(func() {
		ctx = context.Background()
		graph = dryHistory()
	})

	It("was moved by the controller when the validations lead from where it was recorded to where it is", func() {
		retraced, explained := retrace(validations.Input{Tag: "x2", Validations: automatic("x3")}, "x3")
		Expect(explained).To(BeTrue())
		Expect(retraced).To(Equal(validations.Retraced{FoundBy: byAutomatic, Result: validations.Result{Tag: "x3"}}))
	})

	It("was moved by someone else when they do not", func() {
		_, explained := retrace(validations.Input{Tag: "x2", Validations: automatic("x3")}, "h2")
		Expect(explained).To(BeFalse())

		_, explained = retrace(validations.Input{Tag: "x2"}, "x3")
		Expect(explained).To(BeFalse(), "nothing asked for a move")
	})

	It("was moved by the controller when the first of the validations lead there, and goes on from there with the rest", func() {
		By("A validation having arrived after the reconcile that moved the tag")
		retraced, explained := retrace(validations.Input{Tag: "x2", Validations: automatic("x3", "x4")}, "x3")
		Expect(explained).To(BeTrue())
		Expect(retraced).To(Equal(validations.Retraced{FoundBy: byAutomatic, Result: validations.Result{Tag: "x4", Source: byAutomatic}}))
	})

	It("stops at the first point at which the tag is where it was found", func() {
		retraced, explained := retrace(validations.Input{Tag: "x2", Validations: inOrder(automatic("x3"), manual("h2"), manual("x3"), automatic("x4"))}, "x3")
		Expect(explained).To(BeTrue())
		Expect(retraced.FoundBy).To(Equal(byAutomatic))
		Expect(retraced.Result).To(Equal(validations.Result{Tag: "x4", Source: byAutomatic, ManualMove: true}),
			"the two manual validations are part of what moves the tag on")
	})

	It("says which kind of validation moved the tag, apart from what moves it on", func() {
		retraced, explained := retrace(validations.Input{Tag: "x4", Validations: inOrder(manual("h1"), automatic("h2"))}, "h1")
		Expect(explained).To(BeTrue())
		Expect(retraced).To(Equal(validations.Retraced{FoundBy: byManual, Result: validations.Result{Tag: "h2", Source: byAutomatic}}),
			"moving on from the hotfix is an automatic move, and needs no manual one")
	})

	It("was moved by the controller when a move it could not push, a commit that was waiting, or a new head leads there", func() {
		retraced, explained := retrace(validations.Input{Tag: "x2", Pending: &validations.Pending{Sha: "h2", Source: byManual}}, "h2")
		Expect(explained).To(BeTrue())
		Expect(retraced).To(Equal(validations.Retraced{FoundBy: byManual, Result: validations.Result{Tag: "h2"}}))

		retraced, explained = retrace(validations.Input{Tag: "x2", Held: pinned("x3")}, "x3")
		Expect(explained).To(BeTrue())
		Expect(retraced).To(Equal(validations.Retraced{FoundBy: byAutomatic, Result: validations.Result{Tag: "x3"}}))

		retraced, explained = retrace(validations.Input{Tag: "x3", RootCandidate: "x4"}, "x4")
		Expect(explained).To(BeTrue())
		Expect(retraced).To(Equal(validations.Retraced{FoundBy: byAutomatic, Result: validations.Result{Tag: "x4"}}))
	})

	It("was put there by the controller without a validation when it is where the replay starts", func() {
		By("The tag having been seeded, and the record of that lost")
		retraced, explained := retrace(validations.Input{Tag: "x2", Validations: automatic("x3")}, "x2")
		Expect(explained).To(BeTrue())
		Expect(retraced).To(Equal(validations.Retraced{Result: validations.Result{Tag: "x3", Source: byAutomatic}}))
	})

	It("explains a tag that did not exist", func() {
		retraced, explained := retrace(validations.Input{Validations: manual("x3")}, "x3")
		Expect(explained).To(BeTrue())
		Expect(retraced).To(Equal(validations.Retraced{FoundBy: byManual, Result: validations.Result{Tag: "x3"}}))
	})

	Context("for an environment that advances when idle", func() {
		It("takes the environment to have been idle when the tag moved", func() {
			By("The environment being busy now with the very commit the tag was moved to")
			retraced, explained := retrace(validations.Input{Tag: "x2", WhenIdle: true, Held: busy("x3")}, "x3")
			Expect(explained).To(BeTrue())
			Expect(retraced).To(Equal(validations.Retraced{FoundBy: byAutomatic, Result: validations.Result{Tag: "x3"}}))
		})

		It("holds what follows while the environment is busy", func() {
			retraced, explained := retrace(validations.Input{Tag: "x2", WhenIdle: true, Validations: automatic("x3", "x4")}, "x3")
			Expect(explained).To(BeTrue())
			Expect(retraced).To(Equal(validations.Retraced{FoundBy: byAutomatic, Result: validations.Result{Tag: "x3", Held: busy("x4")}}))
		})

		It("applies what follows once the environment is idle again", func() {
			retraced, explained := retrace(validations.Input{Tag: "x2", WhenIdle: true, Idle: true, Validations: automatic("x3", "x4")}, "x3")
			Expect(explained).To(BeTrue())
			Expect(retraced).To(Equal(validations.Retraced{FoundBy: byAutomatic, Result: validations.Result{Tag: "x4", Source: byAutomatic}}))
		})

		It("counts the environment as busy after a manual validation, as Plan does", func() {
			_, explained := retrace(validations.Input{Tag: "x2", WhenIdle: true, Idle: true, Validations: inOrder(manual("x3"), automatic("x4"))}, "x4")
			Expect(explained).To(BeFalse(), "the controller would have held x4 behind the manual move to x3")
		})
	})

	It("returns ancestry errors", func() {
		_, _, err := validations.Retrace(ctx, brokenGraph{}, validations.Input{Tag: "x2", Validations: automatic("x3")}, "x3")
		Expect(err).To(MatchError(ContainSubstring("git is unavailable")))
	})
})
