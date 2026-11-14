package validations_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/validations"
)

func manual(shas ...string) []validations.Validation {
	out := make([]validations.Validation, 0, len(shas))
	for _, sha := range shas {
		out = append(out, validations.Validation{Sha: sha})
	}
	return out
}

func busy(sha string) *validations.Held {
	return &validations.Held{Sha: sha, Reason: promoterv1alpha1.ValidatedHeldReasonBusy}
}

func inOrder(groups ...[]validations.Validation) []validations.Validation {
	var out []validations.Validation
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

var _ = Describe("Plan: manual validations", func() {
	var (
		ctx   context.Context
		graph commitGraph
	)

	plan := func(in validations.Input) validations.Result {
		GinkgoHelper()
		result, err := validations.Plan(ctx, graph, in)
		Expect(err).NotTo(HaveOccurred())
		return result
	}

	BeforeEach(func() {
		ctx = context.Background()
		graph = dryHistory()
	})

	DescribeTable("move the tag to the validated commit, wherever it is",
		func(tag, validated string) {
			result := plan(validations.Input{Tag: tag, Validations: manual(validated)})
			Expect(result).To(Equal(validations.Result{Tag: validated, Source: promoterv1alpha1.ValidatedTagSourceManual}))
		},
		Entry("ahead of the tag", "x2", "x4"),
		Entry("behind the tag", "x4", "x2"),
		Entry("off the dry branch", "x4", "h2"),
		Entry("back onto the dry branch from a hotfix", "h2", "x3"),
		Entry("when the tag does not exist", "", "x3"),
	)

	It("needs no ancestry at all, so a commit the graph cannot place still applies", func() {
		result, err := validations.Plan(ctx, brokenGraph{}, validations.Input{Tag: "x2", Validations: manual("h2")})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Tag).To(Equal("h2"))
	})

	It("leave the tag alone when it is already on the validated commit", func() {
		result := plan(validations.Input{Tag: "x3", Validations: manual("x3")})
		Expect(result).To(Equal(validations.Result{Tag: "x3"}))
	})

	It("apply in the order they were written, so the last one wins", func() {
		result := plan(validations.Input{Tag: "x2", Validations: manual("x4", "h2", "x3")})
		Expect(result.Tag).To(Equal("x3"))
		Expect(result.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceManual))
	})

	Context("the pin", func() {
		It("holds automatic validations that do not contain a hotfix validated by hand", func() {
			result := plan(validations.Input{Tag: "x2", Validations: inOrder(manual("h2"), automatic("x3", "x4"))})
			Expect(result.Tag).To(Equal("h2"))
			Expect(result.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceManual))
			Expect(result.Held).To(Equal(pinned("x4")))
		})

		It("is released by an automatic validation of a commit that merged the hotfix", func() {
			result := plan(validations.Input{Tag: "h2", Held: pinned("x4"), Validations: automatic("m5")})
			Expect(result).To(Equal(validations.Result{Tag: "m5", Source: promoterv1alpha1.ValidatedTagSourceAutomatic}))
		})

		It("is released by a manual validation, which also drops what was held", func() {
			result := plan(validations.Input{Tag: "h2", Held: pinned("x4"), Validations: manual("x3")})
			Expect(result).To(Equal(validations.Result{Tag: "x3", Source: promoterv1alpha1.ValidatedTagSourceManual}))
		})

		It("drops what was held when the tag's own commit is validated by hand", func() {
			result := plan(validations.Input{Tag: "h2", Held: pinned("x4"), Validations: manual("h2")})
			Expect(result).To(Equal(validations.Result{Tag: "h2"}))
		})

		It("lets a hotfix validated into an environment move on like any commit that descends from it", func() {
			By("The next environment being on x2, which the hotfix was branched from")
			result := plan(validations.Input{Tag: "x2", Validations: automatic("h2")})
			Expect(result).To(Equal(validations.Result{Tag: "h2", Source: promoterv1alpha1.ValidatedTagSourceAutomatic}))
		})
	})

	Context("mixed with automatic validations", func() {
		It("is followed by an automatic validation of a commit that descends from it", func() {
			result := plan(validations.Input{Tag: "x4", Validations: inOrder(manual("x2"), automatic("x3"))})
			Expect(result).To(Equal(validations.Result{Tag: "x3", Source: promoterv1alpha1.ValidatedTagSourceAutomatic}))
		})

		It("wins over an automatic validation written before it", func() {
			result := plan(validations.Input{Tag: "x2", Validations: inOrder(automatic("x4"), manual("x3"))})
			Expect(result).To(Equal(validations.Result{Tag: "x3", Source: promoterv1alpha1.ValidatedTagSourceManual}))
		})

		It("reports no move when the replay ends where it started", func() {
			result := plan(validations.Input{Tag: "x3", Validations: inOrder(manual("x2"), automatic("x3"))})
			Expect(result).To(Equal(validations.Result{Tag: "x3"}))
		})
	})

	Context("a manual move that could not be pushed", func() {
		It("is tried again, wherever the commit is", func() {
			result := plan(validations.Input{
				Tag:     "x4",
				Pending: &validations.Pending{Sha: "h2", Source: promoterv1alpha1.ValidatedTagSourceManual},
			})
			Expect(result).To(Equal(validations.Result{Tag: "h2", Source: promoterv1alpha1.ValidatedTagSourceManual}))
		})

		It("gives way to a newer manual validation", func() {
			result := plan(validations.Input{
				Tag:         "x4",
				Pending:     &validations.Pending{Sha: "h2", Source: promoterv1alpha1.ValidatedTagSourceManual},
				Validations: manual("x3"),
			})
			Expect(result.Tag).To(Equal("x3"))
		})

		It("comes before the automatic validation that was held behind it", func() {
			result := plan(validations.Input{
				Tag:     "x2",
				Pending: &validations.Pending{Sha: "h2", Source: promoterv1alpha1.ValidatedTagSourceManual},
				Held:    pinned("x4"),
			})
			Expect(result.Tag).To(Equal("h2"))
			Expect(result.Held).To(Equal(pinned("x4")), "it was held because of the pending move, and still is")
		})
	})
})

var _ = Describe("Plan: advancing when idle", func() {
	var (
		ctx   context.Context
		graph commitGraph
	)

	plan := func(in validations.Input) validations.Result {
		GinkgoHelper()
		in.WhenIdle = true
		result, err := validations.Plan(ctx, graph, in)
		Expect(err).NotTo(HaveOccurred())
		return result
	}

	BeforeEach(func() {
		ctx = context.Background()
		graph = dryHistory()
	})

	It("moves the tag for an automatic validation while the environment is idle", func() {
		result := plan(validations.Input{Tag: "x2", Idle: true, Validations: automatic("x3")})
		Expect(result).To(Equal(validations.Result{Tag: "x3", Source: promoterv1alpha1.ValidatedTagSourceAutomatic}))
	})

	It("moves straight to the newest of several automatic validations", func() {
		result := plan(validations.Input{Tag: "x2", Idle: true, Validations: automatic("x3", "x4")})
		Expect(result).To(Equal(validations.Result{Tag: "x4", Source: promoterv1alpha1.ValidatedTagSourceAutomatic}))
	})

	It("holds an automatic validation while the environment is busy", func() {
		result := plan(validations.Input{Tag: "x2", Validations: automatic("x3")})
		Expect(result).To(Equal(validations.Result{Tag: "x2", Held: busy("x3")}))
	})

	It("holds only the newest while the environment is busy", func() {
		result := plan(validations.Input{Tag: "x2", Held: busy("x3"), Validations: automatic("x4")})
		Expect(result).To(Equal(validations.Result{Tag: "x2", Held: busy("x4")}))
	})

	It("keeps holding while the environment stays busy", func() {
		result := plan(validations.Input{Tag: "x2", Held: busy("x3")})
		Expect(result).To(Equal(validations.Result{Tag: "x2", Held: busy("x3")}))
	})

	It("applies what was held once the environment is idle", func() {
		result := plan(validations.Input{Tag: "x2", Idle: true, Held: busy("x3")})
		Expect(result).To(Equal(validations.Result{Tag: "x3", Source: promoterv1alpha1.ValidatedTagSourceAutomatic}))
	})

	It("follows the head of the dry branch only when idle", func() {
		Expect(plan(validations.Input{Tag: "x3", RootCandidate: "x4"})).To(Equal(validations.Result{Tag: "x3", Held: busy("x4")}))
		Expect(plan(validations.Input{Tag: "x3", Idle: true, RootCandidate: "x4"}).Tag).To(Equal("x4"))
	})

	It("tries a pending automatic move again only when idle", func() {
		pending := &validations.Pending{Sha: "x3", Source: promoterv1alpha1.ValidatedTagSourceAutomatic}
		Expect(plan(validations.Input{Tag: "x2", Pending: pending})).To(Equal(validations.Result{Tag: "x2", Held: busy("x3")}))
		Expect(plan(validations.Input{Tag: "x2", Idle: true, Pending: pending}).Tag).To(Equal("x3"))
	})

	It("reports a commit on another line of history as pinned rather than busy, because waiting will not help", func() {
		result := plan(validations.Input{Tag: "h2", Validations: automatic("x4")})
		Expect(result).To(Equal(validations.Result{Tag: "h2", Held: pinned("x4")}))
	})

	It("holds nothing for a commit the tag has passed, busy or not", func() {
		result := plan(validations.Input{Tag: "x4", Validations: automatic("x3")})
		Expect(result).To(Equal(validations.Result{Tag: "x4"}))
	})

	It("applies a manual validation whether or not the environment is idle", func() {
		result := plan(validations.Input{Tag: "x2", Validations: manual("x4")})
		Expect(result).To(Equal(validations.Result{Tag: "x4", Source: promoterv1alpha1.ValidatedTagSourceManual}))
	})

	It("drops what was held when a manual validation arrives", func() {
		result := plan(validations.Input{Tag: "x2", Held: busy("x3"), Validations: manual("x2")})
		Expect(result).To(Equal(validations.Result{Tag: "x2"}))
	})

	It("counts the environment as busy after a manual validation moved its tag", func() {
		result := plan(validations.Input{Tag: "x2", Idle: true, Validations: inOrder(manual("x3"), automatic("x4"))})
		Expect(result).To(Equal(validations.Result{Tag: "x3", Source: promoterv1alpha1.ValidatedTagSourceManual, Held: busy("x4")}))
	})

	It("stays idle after a manual validation of the commit the tag is already on", func() {
		result := plan(validations.Input{Tag: "x2", Idle: true, Validations: inOrder(manual("x2"), automatic("x3"))})
		Expect(result).To(Equal(validations.Result{Tag: "x3", Source: promoterv1alpha1.ValidatedTagSourceAutomatic}))
	})

	It("gives what was held its turn before a newer manual validation", func() {
		By("The environment having become idle, the held commit applies, and then the manual validation moves the tag on")
		result := plan(validations.Input{Tag: "x2", Idle: true, Held: busy("x3"), Validations: manual("h2")})
		Expect(result).To(Equal(validations.Result{Tag: "h2", Source: promoterv1alpha1.ValidatedTagSourceManual}))
	})

	It("creates a tag that does not exist", func() {
		result := plan(validations.Input{Idle: true, Validations: automatic("x3")})
		Expect(result).To(Equal(validations.Result{Tag: "x3", Source: promoterv1alpha1.ValidatedTagSourceAutomatic}))
	})
})

var _ = Describe("Plan: without WhenIdle", func() {
	It("does not read Idle", func() {
		result, err := validations.Plan(context.Background(), dryHistory(), validations.Input{Tag: "x2", Validations: automatic("x3")})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(validations.Result{Tag: "x3", Source: promoterv1alpha1.ValidatedTagSourceAutomatic}))
	})

	It("still applies a commit that was held as busy before the environment stopped waiting for idle", func() {
		result, err := validations.Plan(context.Background(), dryHistory(), validations.Input{Tag: "x2", Held: busy("x3")})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(validations.Result{Tag: "x3", Source: promoterv1alpha1.ValidatedTagSourceAutomatic}))
	})
})
