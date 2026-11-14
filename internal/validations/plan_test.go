package validations_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/validations"
)

// commitGraph is a dry history for the tag rules to reason about: each commit maps to its parents.
type commitGraph map[string][]string

// dryHistory is main with a hotfix branched off x2, and a later merge of that hotfix:
//
//	x1 - x2 - x3 - x4 - m5 - x6
//	       \           /
//	        h1 ------ h2
func dryHistory() commitGraph {
	return commitGraph{
		"x1": nil,
		"x2": {"x1"},
		"x3": {"x2"},
		"x4": {"x3"},
		"h1": {"x2"},
		"h2": {"h1"},
		"m5": {"x4", "h2"},
		"x6": {"m5"},
	}
}

func (g commitGraph) Contains(_ context.Context, descendant, ancestor string) (bool, error) {
	if _, known := g[descendant]; !known {
		return false, fmt.Errorf("unknown commit %s", descendant)
	}
	if _, known := g[ancestor]; !known {
		return false, fmt.Errorf("unknown commit %s", ancestor)
	}
	seen := map[string]bool{}
	queue := []string{descendant}
	for len(queue) > 0 {
		commit := queue[0]
		queue = queue[1:]
		if commit == ancestor {
			return true, nil
		}
		if seen[commit] {
			continue
		}
		seen[commit] = true
		queue = append(queue, g[commit]...)
	}
	return false, nil
}

type brokenGraph struct{}

func (brokenGraph) Contains(context.Context, string, string) (bool, error) {
	return false, errors.New("git is unavailable")
}

func automatic(shas ...string) []validations.Validation {
	out := make([]validations.Validation, 0, len(shas))
	for _, sha := range shas {
		out = append(out, validations.Validation{Sha: sha, Automatic: true})
	}
	return out
}

func pinned(sha string) *validations.Held {
	return &validations.Held{Sha: sha, Reason: promoterv1alpha1.ValidatedHeldReasonPinned}
}

var _ = Describe("Plan: where an environment's tag should point", func() {
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

	It("leaves the tag alone when nothing is new", func() {
		result := plan(validations.Input{Tag: "x2"})
		Expect(result).To(Equal(validations.Result{Tag: "x2"}))
	})

	Context("automatic validations", func() {
		It("moves the tag to a validated commit that descends from it", func() {
			result := plan(validations.Input{Tag: "x2", Validations: automatic("x3")})
			Expect(result.Tag).To(Equal("x3"))
			Expect(result.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceAutomatic))
			Expect(result.Held).To(BeNil())
		})

		It("replays several and returns only the end result", func() {
			result := plan(validations.Input{Tag: "x1", Validations: automatic("x2", "x3", "x4")})
			Expect(result.Tag).To(Equal("x4"))
			Expect(result.Held).To(BeNil())
		})

		It("creates a tag that does not exist", func() {
			result := plan(validations.Input{Validations: automatic("x3")})
			Expect(result.Tag).To(Equal("x3"))
			Expect(result.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceAutomatic))
		})

		It("never moves the tag backward, and holds nothing for a commit the tag has passed", func() {
			result := plan(validations.Input{Tag: "x4", Validations: automatic("x3")})
			Expect(result).To(Equal(validations.Result{Tag: "x4"}))
		})

		It("applies validations in order, so a later one behind the tag does not undo an earlier one", func() {
			result := plan(validations.Input{Tag: "x2", Validations: automatic("x4", "x3")})
			Expect(result.Tag).To(Equal("x4"))
			Expect(result.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceAutomatic))
			Expect(result.Held).To(BeNil())
		})

		It("goes on holding a commit when a newer validation is of a commit the tag has passed", func() {
			By("The pipeline validating a commit the hotfix was branched from")
			result := plan(validations.Input{Tag: "h2", Held: pinned("x4"), Validations: automatic("x2")})
			Expect(result).To(Equal(validations.Result{Tag: "h2", Held: pinned("x4")}))
		})

		It("holds a validated commit that does not contain the commit the tag is on", func() {
			By("The tag sitting on a hotfix that is not on the dry branch")
			result := plan(validations.Input{Tag: "h2", Validations: automatic("x4")})
			Expect(result.Tag).To(Equal("h2"))
			Expect(result.Held).To(Equal(pinned("x4")))
		})

		It("releases the pin once a validated commit contains the hotfix", func() {
			result := plan(validations.Input{Tag: "h2", Held: pinned("x4"), Validations: automatic("m5")})
			Expect(result.Tag).To(Equal("m5"))
			Expect(result.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceAutomatic))
			Expect(result.Held).To(BeNil())
		})

		It("keeps what is held while nothing newer arrives", func() {
			result := plan(validations.Input{Tag: "h2", Held: pinned("x4")})
			Expect(result.Tag).To(Equal("h2"))
			Expect(result.Held).To(Equal(pinned("x4")))
		})

		It("replaces what is held with the newest validation that cannot apply either", func() {
			result := plan(validations.Input{Tag: "h2", Held: pinned("x3"), Validations: automatic("x4")})
			Expect(result.Held).To(Equal(pinned("x4")))
		})

		It("goes on holding a commit when the tag's own commit is validated", func() {
			result := plan(validations.Input{Tag: "h2", Held: pinned("x4"), Validations: automatic("h2")})
			Expect(result).To(Equal(validations.Result{Tag: "h2", Held: pinned("x4")}))
		})

		It("stops holding a commit the tag has reached some other way", func() {
			result := plan(validations.Input{Tag: "x4", Held: pinned("x4")})
			Expect(result).To(Equal(validations.Result{Tag: "x4"}))
		})
	})

	Context("the head of the dry branch, for an environment that follows it", func() {
		It("moves the tag to a new head", func() {
			result := plan(validations.Input{Tag: "x3", RootCandidate: "x4"})
			Expect(result.Tag).To(Equal("x4"))
			Expect(result.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceAutomatic))
		})

		It("holds a head that does not contain the hotfix the tag is pinned to", func() {
			result := plan(validations.Input{Tag: "h2", RootCandidate: "x4"})
			Expect(result.Tag).To(Equal("h2"))
			Expect(result.Held).To(Equal(pinned("x4")))
		})

		It("moves on once the hotfix is merged into the dry branch", func() {
			result := plan(validations.Input{Tag: "h2", Held: pinned("x4"), RootCandidate: "m5"})
			Expect(result.Tag).To(Equal("m5"))
			Expect(result.Held).To(BeNil())
		})
	})

	Context("a move that could not be pushed", func() {
		It("is tried again", func() {
			result := plan(validations.Input{
				Tag:     "x2",
				Pending: &validations.Pending{Sha: "x3", Source: promoterv1alpha1.ValidatedTagSourceAutomatic},
			})
			Expect(result.Tag).To(Equal("x3"))
			Expect(result.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceAutomatic))
		})

		It("gives way to a newer validation", func() {
			result := plan(validations.Input{
				Tag:         "x2",
				Pending:     &validations.Pending{Sha: "x3", Source: promoterv1alpha1.ValidatedTagSourceAutomatic},
				Validations: automatic("x4"),
			})
			Expect(result.Tag).To(Equal("x4"))
		})

		It("is dropped when the tag is already there", func() {
			result := plan(validations.Input{
				Tag:     "x3",
				Pending: &validations.Pending{Sha: "x3", Source: promoterv1alpha1.ValidatedTagSourceAutomatic},
			})
			Expect(result).To(Equal(validations.Result{Tag: "x3"}))
		})
	})

	It("returns ancestry errors", func() {
		_, err := validations.Plan(ctx, brokenGraph{}, validations.Input{Tag: "x2", Validations: automatic("x3")})
		Expect(err).To(MatchError(ContainSubstring("git is unavailable")))
	})
})

var _ = Describe("Success memory", func() {
	base := time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)
	at := func(minutes int) metav1.Time {
		return metav1.NewTime(base.Add(time.Duration(minutes) * time.Minute))
	}
	success := func(sha string, minutes int) promoterv1alpha1.ValidatedCommitStatusSuccessfulSha {
		return promoterv1alpha1.ValidatedCommitStatusSuccessfulSha{Sha: sha, Time: at(minutes)}
	}
	list := func(entries ...promoterv1alpha1.ValidatedCommitStatusSuccessfulSha) []promoterv1alpha1.ValidatedCommitStatusSuccessfulSha {
		return entries
	}

	Describe("RecordSuccess", func() {
		It("puts a new commit first", func() {
			Expect(validations.RecordSuccess(list(success("x1", 1)), "x2", at(2))).To(Equal(list(success("x2", 2), success("x1", 1))))
		})

		It("keeps the time a commit was first seen while it stays the newest", func() {
			memory := list(success("x2", 2), success("x1", 1))
			Expect(validations.RecordSuccess(memory, "x2", at(9))).To(Equal(memory))
		})

		It("leaves a commit where it is when the environment succeeds on it again", func() {
			By("The environment going back to x1, or reporting x1 again once x2, which changed nothing for it, has a successor")
			memory := list(success("x2", 2), success("x1", 1))
			Expect(validations.RecordSuccess(memory, "x1", at(5))).To(Equal(memory))
			Expect(validations.UpstreamCleared(validations.RecordSuccess(memory, "x1", at(5)))).To(Equal("x2"),
				"what was cleared for the next environment does not step back")
		})

		It("records nothing for an empty SHA", func() {
			memory := list(success("x1", 1))
			Expect(validations.RecordSuccess(memory, "", at(2))).To(Equal(memory))
		})

		It("forgets the oldest commit beyond the cap", func() {
			var memory []promoterv1alpha1.ValidatedCommitStatusSuccessfulSha
			for i := range validations.MaxSuccessfulShas + 3 {
				memory = validations.RecordSuccess(memory, fmt.Sprintf("x%d", i), at(i))
			}
			Expect(memory).To(HaveLen(validations.MaxSuccessfulShas))
			Expect(memory[0].Sha).To(Equal(fmt.Sprintf("x%d", validations.MaxSuccessfulShas+2)))
			Expect(memory[validations.MaxSuccessfulShas-1].Sha).To(Equal("x3"))
		})
	})

	Describe("UpstreamCleared", func() {
		It("is nothing for an environment with no upstreams", func() {
			Expect(validations.UpstreamCleared()).To(BeEmpty())
		})

		It("is the single upstream's newest success", func() {
			Expect(validations.UpstreamCleared(list(success("x3", 3), success("x2", 2)))).To(Equal("x3"))
		})

		It("is nothing while an upstream has succeeded on nothing", func() {
			Expect(validations.UpstreamCleared(list(success("x3", 3)), nil)).To(BeEmpty())
		})

		It("is the newest commit every upstream has succeeded on", func() {
			e2e := list(success("x4", 8), success("x3", 5), success("x2", 2))
			perf := list(success("x3", 6), success("x2", 3))
			Expect(validations.UpstreamCleared(e2e, perf)).To(Equal("x3"))
		})

		It("does not need the upstreams to succeed at the same moment", func() {
			e2e := list(success("x5", 9), success("x4", 4))
			perf := list(success("x4", 12), success("x3", 3))
			Expect(validations.UpstreamCleared(e2e, perf)).To(Equal("x4"))
		})

		It("is nothing when the upstreams have no success in common", func() {
			e2e := list(success("x5", 9), success("x3", 4))
			perf := list(success("x4", 12), success("x2", 3))
			Expect(validations.UpstreamCleared(e2e, perf)).To(BeEmpty())
		})

		It("goes by when the last upstream succeeded, not by which commit is listed first", func() {
			By("x2 being cleared by both upstreams later than x3 was, after a rollback")
			e2e := list(success("x2", 20), success("x3", 5))
			perf := list(success("x3", 6), success("x2", 3))
			Expect(validations.UpstreamCleared(e2e, perf)).To(Equal("x2"))
		})

		It("takes the commit listed first of two that were cleared at the same moment", func() {
			By("Status keeping times to the second, so that two successes can carry the same one")
			Expect(validations.UpstreamCleared(list(success("x3", 5), success("x2", 5)))).To(Equal("x3"))
			Expect(validations.UpstreamCleared(list(success("x2", 5), success("x3", 5)))).To(Equal("x2"))

			e2e := list(success("x3", 5), success("x2", 5))
			perf := list(success("x2", 5), success("x3", 5))
			Expect(validations.UpstreamCleared(e2e, perf)).To(Equal("x3"), "the first upstream's order decides")
		})
	})
})
