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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/internal/validations"
)

//go:embed testdata/ValidatedCommitStatus.yaml
var testValidatedCommitStatusYAML string

// Environment branches and tags used by the decision specs.
const (
	vcsDev     = "environment/dev"
	vcsTest    = "environment/test"
	vcsProd    = "environment/prod"
	vcsTagDev  = "env/dev"
	vcsTagTest = "env/test"
	vcsTagProd = "env/prod"
)

// vcsTestGraph is an in-memory DRY commit graph (commit → parents) implementing vcsCommitGraph.
type vcsTestGraph map[string][]string

// vcsMainGraph is the history most specs run against:
//
//	a ─ b ─ c ─ d ─ e ─ f      DRY branch "main", HEAD = f
//	        └── x              a feature-branch commit that is not on main
//	    └── p ─ q              the DRY branch after a force-push that replaced c..f, HEAD = q
//	    ├── l ─┐
//	    └── r ─┴ m             two sides of a merge, HEAD = m
func vcsMainGraph() vcsTestGraph {
	return vcsTestGraph{
		"a": nil,
		"b": {"a"},
		"c": {"b"},
		"d": {"c"},
		"e": {"d"},
		"f": {"e"},
		"x": {"c"},
		"p": {"b"},
		"q": {"p"},
		"l": {"b"},
		"r": {"b"},
		"m": {"l", "r"},
	}
}

func (g vcsTestGraph) ancestors(sha string) map[string]struct{} {
	seen := map[string]struct{}{}
	stack := []string{sha}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, ok := seen[cur]; ok {
			continue
		}
		seen[cur] = struct{}{}
		stack = append(stack, g[cur]...)
	}
	return seen
}

func (g vcsTestGraph) Contains(_ context.Context, descendant, ancestor string) (bool, error) {
	if _, ok := g[descendant]; !ok {
		return false, nil
	}
	if _, ok := g[ancestor]; !ok {
		return false, nil
	}
	_, ok := g.ancestors(descendant)[ancestor]
	return ok, nil
}

func (g vcsTestGraph) MergeBase(_ context.Context, shas ...string) (string, error) {
	if len(shas) == 0 {
		return "", nil
	}
	common := g.ancestors(shas[0])
	for _, sha := range shas[1:] {
		other := g.ancestors(sha)
		for c := range common {
			if _, ok := other[c]; !ok {
				delete(common, c)
			}
		}
	}
	// The best common ancestor is the one every other common ancestor is reachable from.
	for candidate := range common {
		if len(g.ancestors(candidate)) == len(common) {
			return candidate, nil
		}
	}
	return "", nil
}

// vcsFailingGraph fails every ancestry question, for error-propagation specs.
type vcsFailingGraph struct{}

func (vcsFailingGraph) Contains(context.Context, string, string) (bool, error) {
	return false, errors.New("graph unavailable")
}

func (vcsFailingGraph) MergeBase(context.Context, ...string) (string, error) {
	return "", errors.New("graph unavailable")
}

// vcsPhases builds active commit status phases keyed check-0, check-1, ...
func vcsPhases(phases ...promoterv1alpha1.CommitStatusPhase) []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase {
	out := make([]promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase, 0, len(phases))
	for i, phase := range phases {
		out = append(out, promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
			Key:   "check-" + string(rune('0'+i)),
			Phase: string(phase),
		})
	}
	return out
}

// vcsStatus builds a PromotionStrategy environment status. Hydrated SHAs are synthetic: "<branch>@<dry>" for
// the active tip and "<branch>-next@<dry>" for the proposed tip.
func vcsStatus(branch, activeDry, proposedDry string, phases ...promoterv1alpha1.CommitStatusPhase) promoterv1alpha1.EnvironmentStatus {
	status := promoterv1alpha1.EnvironmentStatus{Branch: branch}
	status.Active.Dry.Sha = activeDry
	status.Active.Hydrated.Sha = branch + "@" + activeDry
	status.Active.CommitStatuses = vcsPhases(phases...)
	status.Proposed.Dry.Sha = proposedDry
	status.Proposed.Hydrated.Sha = branch + "-next@" + proposedDry
	return status
}

// vcsWithNote sets the hydrator note DRY SHA on the proposed tip (a no-op hydration report).
func vcsWithNote(status promoterv1alpha1.EnvironmentStatus, noteDry string) promoterv1alpha1.EnvironmentStatus {
	status.Proposed.Note = &promoterv1alpha1.HydratorMetadata{DrySha: noteDry}
	return status
}

// vcsWatermark builds a qualification watermark at the given DRY SHA.
func vcsWatermark(drySha string) *promoterv1alpha1.ValidatedCommitStatusWatermark {
	return &promoterv1alpha1.ValidatedCommitStatusWatermark{
		DrySha:      drySha,
		QualifiedAt: metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
	}
}

// vcsSettled builds an environment with nothing in flight: live is promoted, hydrated, and every active commit
// status has the given phase. tagSha "" means the tag does not exist.
func vcsSettled(branch, tag, tagSha, live string, phases ...promoterv1alpha1.CommitStatusPhase) vcsEnvironment {
	return vcsEnvironment{
		Branch:     branch,
		Tag:        tag,
		TagSha:     tagSha,
		Status:     vcsStatus(branch, live, live, phases...),
		LiveDrySha: live,
	}
}

// vcsProposing builds an environment whose hydrator has proposed proposedDry while activeDry is live.
func vcsProposing(branch, tag, tagSha, activeDry, proposedDry string, dependsOn ...string) vcsEnvironment {
	return vcsEnvironment{
		Branch:     branch,
		Tag:        tag,
		TagSha:     tagSha,
		DependsOn:  dependsOn,
		Status:     vcsStatus(branch, activeDry, proposedDry),
		LiveDrySha: activeDry,
	}
}

// vcsTagMove is one tag the controller creates or moves.
type vcsTagMove struct {
	Branch string
	Tag    string
	// From is the commit the tag points at now, "" when the tag is to be created.
	From string
	To   string
}

// vcsPlanTime is the time the decision specs plan tags at.
var vcsPlanTime = metav1.NewTime(time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC))

// vcsMoves plans the tags of envs and returns only the tags the controller creates or moves.
func vcsMoves(ctx context.Context, graph vcsCommitGraph, head string, envs []vcsEnvironment) ([]vcsTagMove, error) {
	plans, err := vcsPlanTags(ctx, graph, head, envs, vcsPlanTime)
	var moves []vcsTagMove
	for _, plan := range plans {
		if plan.moves() {
			moves = append(moves, vcsTagMove{Branch: plan.Branch, Tag: plan.Tag, From: plan.From, To: plan.To})
		}
	}
	return moves, err
}

func vcsByBranch(envs ...vcsEnvironment) map[string]vcsEnvironment {
	out := make(map[string]vcsEnvironment, len(envs))
	for _, env := range envs {
		out[env.Branch] = env
	}
	return out
}

var _ = Describe("ValidatedCommitStatus decisions", func() {
	var (
		ctx   context.Context
		graph vcsTestGraph
		now   metav1.Time
	)

	const (
		success = promoterv1alpha1.CommitPhaseSuccess
		pending = promoterv1alpha1.CommitPhasePending
		failure = promoterv1alpha1.CommitPhaseFailure
	)

	BeforeEach(func() {
		ctx = context.Background()
		graph = vcsMainGraph()
		now = metav1.NewTime(time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC))
	})

	Describe("vcsLiveDrySha: which DRY commit an environment is running", func() {
		neverCalled := func() (bool, error) {
			Fail("manifests must not be compared when the answer is already known")
			return false, nil
		}

		It("is the active DRY SHA while a promotion is pending", func() {
			live, err := vcsLiveDrySha(vcsStatus(vcsDev, "c", "d"), neverCalled)
			Expect(err).NotTo(HaveOccurred())
			Expect(live).To(Equal("c"))
		})

		It("is the active DRY SHA when the hydrator has reported nothing newer", func() {
			live, err := vcsLiveDrySha(vcsStatus(vcsDev, "c", "c"), neverCalled)
			Expect(err).NotTo(HaveOccurred())
			Expect(live).To(Equal("c"))

			live, err = vcsLiveDrySha(vcsWithNote(vcsStatus(vcsDev, "c", "c"), "c"), neverCalled)
			Expect(err).NotTo(HaveOccurred())
			Expect(live).To(Equal("c"))
		})

		It("is empty when nothing has been promoted yet", func() {
			live, err := vcsLiveDrySha(vcsStatus(vcsDev, "", ""), neverCalled)
			Expect(err).NotTo(HaveOccurred())
			Expect(live).To(BeEmpty())
		})

		It("is the hydrator note's DRY SHA after a no-op hydration of identical manifests", func() {
			status := vcsWithNote(vcsStatus(vcsDev, "c", "c"), "e")
			live, err := vcsLiveDrySha(status, func() (bool, error) { return true, nil })
			Expect(err).NotTo(HaveOccurred())
			Expect(live).To(Equal("e"))
		})

		It("stays on the active DRY SHA when the proposed manifests differ from the active manifests", func() {
			// The same DRY SHA can be hydrated again with different output. No pull request opens for it
			// (proposed and active name the same DRY SHA), so a later note on that tip must not be credited
			// to manifests the environment is not running.
			status := vcsWithNote(vcsStatus(vcsDev, "c", "c"), "e")
			live, err := vcsLiveDrySha(status, func() (bool, error) { return false, nil })
			Expect(err).NotTo(HaveOccurred())
			Expect(live).To(Equal("c"))
		})

		It("ignores a hydrator note while a promotion is pending", func() {
			status := vcsWithNote(vcsStatus(vcsDev, "c", "d"), "e")
			live, err := vcsLiveDrySha(status, neverCalled)
			Expect(err).NotTo(HaveOccurred())
			Expect(live).To(Equal("c"))
		})

		It("returns the manifest comparison error", func() {
			status := vcsWithNote(vcsStatus(vcsDev, "c", "c"), "e")
			_, err := vcsLiveDrySha(status, func() (bool, error) { return false, errors.New("boom") })
			Expect(err).To(MatchError(ContainSubstring("boom")))
		})
	})

	Describe("vcsQualify: recording what an environment has run successfully", func() {
		It("qualifies the live commit when every active commit status is success", func() {
			env := vcsSettled(vcsDev, vcsTagDev, "d", "d", success, success)
			qualified, err := vcsQualify(ctx, graph, "f", env, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).NotTo(BeNil())
			Expect(qualified.DrySha).To(Equal("d"))
			Expect(qualified.HydratedSha).To(Equal(env.Status.Active.Hydrated.Sha))
			Expect(qualified.QualifiedAt).To(Equal(now))
		})

		It("qualifies immediately when the environment has no active commit statuses", func() {
			qualified, err := vcsQualify(ctx, graph, "f", vcsSettled(vcsDev, vcsTagDev, "d", "d"), now)
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).NotTo(BeNil())
			Expect(qualified.DrySha).To(Equal("d"))
		})

		DescribeTable("does not qualify while an active commit status is not success",
			func(phases ...promoterv1alpha1.CommitStatusPhase) {
				qualified, err := vcsQualify(ctx, graph, "f", vcsSettled(vcsDev, vcsTagDev, "d", "d", phases...), now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified).To(BeNil())
			},
			Entry("pending", pending),
			Entry("failure", failure),
			Entry("one of several pending", success, pending),
		)

		It("does not qualify when nothing is live", func() {
			qualified, err := vcsQualify(ctx, graph, "f", vcsSettled(vcsDev, vcsTagDev, "f", ""), now)
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(BeNil())
		})

		It("advances the watermark when a newer commit is live and successful", func() {
			env := vcsSettled(vcsDev, vcsTagDev, "f", "f", success)
			env.Qualified = vcsWatermark("d")
			qualified, err := vcsQualify(ctx, graph, "f", env, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified.DrySha).To(Equal("f"))
			Expect(qualified.QualifiedAt).To(Equal(now))
		})

		It("leaves the watermark untouched when the live commit is already the watermark", func() {
			env := vcsSettled(vcsDev, vcsTagDev, "d", "d", success)
			env.Qualified = vcsWatermark("d")
			qualified, err := vcsQualify(ctx, graph, "f", env, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(Equal(vcsWatermark("d")), "qualifiedAt records when the commit first qualified")
		})

		It("is latched: a qualified commit stays qualified when the environment turns unhealthy", func() {
			env := vcsSettled(vcsDev, vcsTagDev, "d", "d", failure)
			env.Qualified = vcsWatermark("d")
			qualified, err := vcsQualify(ctx, graph, "f", env, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(Equal(vcsWatermark("d")))
		})

		It("is latched: the watermark survives a newer commit that is live but not yet successful", func() {
			env := vcsSettled(vcsDev, vcsTagDev, "f", "f", pending)
			env.Qualified = vcsWatermark("d")
			qualified, err := vcsQualify(ctx, graph, "f", env, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(Equal(vcsWatermark("d")))
		})

		It("is forward-only: promoting an older commit does not move the watermark back", func() {
			// A person moved the environment's tag backward and the older commit was promoted the normal
			// way. Nothing said the newer commit was bad, so it stays qualified.
			env := vcsSettled(vcsProd, vcsTagProd, "c", "c", success)
			env.Qualified = vcsWatermark("e")
			qualified, err := vcsQualify(ctx, graph, "f", env, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(Equal(vcsWatermark("e")))
		})

		Context("commits that are not on the DRY branch", func() {
			It("does not qualify a live commit that is not reachable from the DRY branch HEAD", func() {
				// A person pushed a root environment's tag to a feature-branch commit and it was promoted.
				qualified, err := vcsQualify(ctx, graph, "f", vcsSettled(vcsDev, vcsTagDev, "x", "x", success), now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified).To(BeNil())
			})

			It("keeps an on-branch watermark while an off-branch commit is live", func() {
				env := vcsSettled(vcsDev, vcsTagDev, "x", "x", success)
				env.Qualified = vcsWatermark("c")
				qualified, err := vcsQualify(ctx, graph, "f", env, now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified).To(Equal(vcsWatermark("c")))
			})
		})

		Context("after the DRY branch is force-pushed", func() {
			It("drops a watermark that is no longer reachable from HEAD", func() {
				env := vcsSettled(vcsDev, vcsTagDev, "e", "e", success)
				env.Qualified = vcsWatermark("e")
				qualified, err := vcsQualify(ctx, graph, "q", env, now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified).To(BeNil(), "e was rewritten away and e is still what is live")
			})

			It("keeps a watermark that survived the rewrite", func() {
				env := vcsSettled(vcsDev, vcsTagDev, "e", "e", success)
				env.Qualified = vcsWatermark("b")
				qualified, err := vcsQualify(ctx, graph, "q", env, now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified).To(Equal(vcsWatermark("b")))
			})

			It("qualifies again once a commit on the new history is live and successful", func() {
				env := vcsSettled(vcsDev, vcsTagDev, "q", "q", success)
				env.Qualified = vcsWatermark("e")
				qualified, err := vcsQualify(ctx, graph, "q", env, now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified.DrySha).To(Equal("q"))
			})
		})

		Context("after a RestoreActiveCommit", func() {
			It("pulls the watermark back to the restored commit", func() {
				env := vcsSettled(vcsTest, vcsTagTest, "e", "c", pending)
				env.Qualified = vcsWatermark("e")
				env.Restored = true
				qualified, err := vcsQualify(ctx, graph, "f", env, now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified).NotTo(BeNil())
				Expect(qualified.DrySha).To(Equal("c"), "the restore says d and e should not have been trusted")
				Expect(qualified.HydratedSha).To(Equal(env.Status.Active.Hydrated.Sha))
				Expect(qualified.QualifiedAt).To(Equal(now))
			})

			It("does nothing more once the watermark is at the restored commit", func() {
				env := vcsSettled(vcsTest, vcsTagTest, "e", "c", success)
				env.Qualified = vcsWatermark("c")
				env.Restored = true
				qualified, err := vcsQualify(ctx, graph, "f", env, now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified).To(Equal(vcsWatermark("c")))
			})

			It("does not pull back a watermark the restored commit is ahead of", func() {
				env := vcsSettled(vcsTest, vcsTagTest, "e", "e", pending)
				env.Qualified = vcsWatermark("c")
				env.Restored = true
				qualified, err := vcsQualify(ctx, graph, "f", env, now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified).To(Equal(vcsWatermark("c")))
			})

			It("qualifies forward again when the environment is promoted past the restore", func() {
				env := vcsSettled(vcsTest, vcsTagTest, "f", "f", success)
				env.Qualified = vcsWatermark("c")
				qualified, err := vcsQualify(ctx, graph, "f", env, now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified.DrySha).To(Equal("f"))
			})
		})

		It("returns ancestry errors", func() {
			env := vcsSettled(vcsDev, vcsTagDev, "f", "f", success)
			env.Qualified = vcsWatermark("d")
			_, err := vcsQualify(ctx, vcsFailingGraph{}, "f", env, now)
			Expect(err).To(MatchError(ContainSubstring("graph unavailable")))
		})
	})

	Describe("vcsUnrecordedQualification: what stands when a qualification could not be written to its note", func() {
		It("keeps the previous watermark when an advance could not be recorded", func() {
			env := vcsSettled(vcsDev, vcsTagDev, "f", "f", success)
			env.Qualified = vcsWatermark("d")
			qualified, err := vcsUnrecordedQualification(ctx, graph, "f", env, vcsWatermark("f"))
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(Equal(vcsWatermark("d")))
		})

		It("qualifies nothing when a first qualification could not be recorded", func() {
			env := vcsSettled(vcsDev, vcsTagDev, "f", "f", success)
			qualified, err := vcsUnrecordedQualification(ctx, graph, "f", env, vcsWatermark("f"))
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(BeNil())
		})

		It("takes a restore's pull-back anyway, so that the gate does not stay open on a revoked commit", func() {
			env := vcsSettled(vcsTest, vcsTagTest, "e", "c", pending)
			env.Qualified = vcsWatermark("e")
			env.Restored = true
			qualified, err := vcsUnrecordedQualification(ctx, graph, "f", env, vcsWatermark("c"))
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(Equal(vcsWatermark("c")), "d and e were taken back by the restore, recorded or not")
		})

		It("drops a previous watermark that is no longer on the DRY branch", func() {
			env := vcsSettled(vcsDev, vcsTagDev, "q", "q", success)
			env.Qualified = vcsWatermark("e")
			qualified, err := vcsUnrecordedQualification(ctx, graph, "q", env, vcsWatermark("q"))
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(BeNil())
		})

		It("returns ancestry errors", func() {
			env := vcsSettled(vcsDev, vcsTagDev, "f", "f", success)
			env.Qualified = vcsWatermark("d")
			_, err := vcsUnrecordedQualification(ctx, vcsFailingGraph{}, "f", env, vcsWatermark("f"))
			Expect(err).To(MatchError(ContainSubstring("graph unavailable")))
		})
	})

	Describe("vcsPlanTags: where each environment tag should point", func() {
		// chain returns dev → test → prod with the given tag SHAs; dev is live on devLive.
		chain := func(devTag, testTag, prodTag string) (vcsEnvironment, vcsEnvironment, vcsEnvironment) {
			dev := vcsSettled(vcsDev, vcsTagDev, devTag, devTag)
			test := vcsSettled(vcsTest, vcsTagTest, testTag, testTag)
			test.DependsOn = []string{vcsDev}
			prod := vcsSettled(vcsProd, vcsTagProd, prodTag, prodTag)
			prod.DependsOn = []string{vcsTest}
			return dev, test, prod
		}

		It("moves a root environment's tag to the DRY branch HEAD", func() {
			dev, test, prod := chain("d", "d", "d")
			moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test, prod})
			Expect(err).NotTo(HaveOccurred())
			Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsDev, Tag: vcsTagDev, From: "d", To: "f"}}))
		})

		It("moves nothing when every tag is where it should be", func() {
			dev, test, prod := chain("f", "d", "c")
			dev.Qualified = vcsWatermark("d")
			test.Qualified = vcsWatermark("c")
			moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test, prod})
			Expect(err).NotTo(HaveOccurred())
			Expect(moves).To(BeEmpty())
		})

		It("moves a dependent's tag to the newest commit its upstream has qualified", func() {
			dev, test, prod := chain("f", "c", "c")
			dev.Qualified = vcsWatermark("e")
			moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test, prod})
			Expect(err).NotTo(HaveOccurred())
			Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsTest, Tag: vcsTagTest, From: "c", To: "e"}}))
		})

		It("does not move a dependent's tag while its upstream has qualified nothing", func() {
			dev, test, prod := chain("f", "c", "c")
			moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test, prod})
			Expect(err).NotTo(HaveOccurred())
			Expect(moves).To(BeEmpty())
		})

		It("looks only at direct upstreams", func() {
			dev, test, prod := chain("f", "f", "b")
			dev.Qualified = vcsWatermark("f")
			test.Qualified = vcsWatermark("c")
			moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test, prod})
			Expect(err).NotTo(HaveOccurred())
			Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsProd, Tag: vcsTagProd, From: "b", To: "c"}}),
				"prod follows test's watermark, not dev's")
		})

		It("ignores the environment's own state: an open promotion and running checks do not delay its tag", func() {
			dev, _, _ := chain("f", "", "")
			dev.Qualified = vcsWatermark("e")
			test := vcsProposing(vcsTest, vcsTagTest, "d", "c", "d", vcsDev)
			test.Status.Active.CommitStatuses = vcsPhases(pending)
			moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test})
			Expect(err).NotTo(HaveOccurred())
			Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsTest, Tag: vcsTagTest, From: "d", To: "e"}}))
		})

		It("never moves a tag backward", func() {
			// test's tag is at e; dev was restored and its watermark pulled back to c.
			dev, test, prod := chain("f", "e", "b")
			dev.Qualified = vcsWatermark("c")
			moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test, prod})
			Expect(err).NotTo(HaveOccurred())
			Expect(moves).To(BeEmpty())
		})

		It("plans one move per tag, in environment order", func() {
			dev, test, prod := chain("c", "b", "a")
			dev.Qualified = vcsWatermark("c")
			test.Qualified = vcsWatermark("b")
			moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test, prod})
			Expect(err).NotTo(HaveOccurred())
			Expect(moves).To(Equal([]vcsTagMove{
				{Branch: vcsDev, Tag: vcsTagDev, From: "c", To: "f"},
				{Branch: vcsTest, Tag: vcsTagTest, From: "b", To: "c"},
				{Branch: vcsProd, Tag: vcsTagProd, From: "a", To: "b"},
			}))
		})

		Context("fan-in", func() {
			fanIn := func(leftQualified, rightQualified *promoterv1alpha1.ValidatedCommitStatusWatermark, prodTag string) []vcsEnvironment {
				left := vcsSettled("environment/left", "env/left", "f", "f")
				left.Qualified = leftQualified
				right := vcsSettled("environment/right", "env/right", "f", "f")
				right.Qualified = rightQualified
				prod := vcsSettled(vcsProd, vcsTagProd, prodTag, prodTag)
				prod.DependsOn = []string{left.Branch, right.Branch}
				return []vcsEnvironment{left, right, prod}
			}

			It("moves the tag to the newest commit every upstream has qualified", func() {
				moves, err := vcsMoves(ctx, graph, "f", fanIn(vcsWatermark("f"), vcsWatermark("d"), "b"))
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsProd, Tag: vcsTagProd, From: "b", To: "d"}}))
			})

			It("waits while any upstream has qualified nothing", func() {
				moves, err := vcsMoves(ctx, graph, "f", fanIn(vcsWatermark("f"), nil, "b"))
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(BeEmpty())
			})

			It("uses the merge base when the upstream watermarks are on different sides of a merge", func() {
				envs := fanIn(vcsWatermark("l"), vcsWatermark("r"), "a")
				envs[0].TagSha, envs[1].TagSha = "m", "m"
				moves, err := vcsMoves(ctx, graph, "m", envs)
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsProd, Tag: vcsTagProd, From: "a", To: "b"}}),
					"b is the newest commit both l and r contain")
			})
		})

		Context("validation: Manual", func() {
			It("never moves the tag", func() {
				dev, test, prod := chain("f", "f", "b")
				dev.Qualified = vcsWatermark("f")
				test.Qualified = vcsWatermark("f")
				prod.Manual = true
				moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test, prod})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(BeEmpty())
			})

			It("never moves a root's tag", func() {
				dev, _, _ := chain("c", "", "")
				dev.Manual = true
				moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(BeEmpty())
			})

			It("creates a missing tag at the live commit and then leaves it alone", func() {
				dev, test, prod := chain("f", "f", "")
				dev.Qualified = vcsWatermark("f")
				test.Qualified = vcsWatermark("f")
				prod.Manual = true
				prod.LiveDrySha = "c"
				moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test, prod})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsProd, Tag: vcsTagProd, From: "", To: "c"}}),
					"the first hydration from the tag is a no-op; a person decides what comes next")
			})
		})

		Context("bootstrapping missing tags", func() {
			It("creates a dependent's tag at the commit that is live in it", func() {
				dev, test, prod := chain("f", "", "")
				test.LiveDrySha = "d"
				prod.LiveDrySha = "c"
				moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test, prod})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]vcsTagMove{
					{Branch: vcsTest, Tag: vcsTagTest, From: "", To: "d"},
					{Branch: vcsProd, Tag: vcsTagProd, From: "", To: "c"},
				}))
			})

			It("creates only the root's tag when nothing is live and nothing is qualified", func() {
				dev, test, prod := chain("", "", "")
				moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test, prod})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsDev, Tag: vcsTagDev, From: "", To: "f"}}),
					"a tag at HEAD would propose a commit to test and prod that nothing upstream has run")
			})

			It("creates a dependent's tag at its upstream's watermark when nothing is live in it", func() {
				dev, test, prod := chain("f", "", "")
				dev.Qualified = vcsWatermark("e")
				moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test, prod})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsTest, Tag: vcsTagTest, From: "", To: "e"}}))
			})

			It("creates the tag at its target when the live commit is not on the DRY branch", func() {
				dev, test, _ := chain("f", "", "")
				dev.Qualified = vcsWatermark("e")
				test.LiveDrySha = "x"
				moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsTest, Tag: vcsTagTest, From: "", To: "e"}}))
			})

			It("does not create a validation: Manual tag while nothing on the DRY branch is live", func() {
				dev, test, prod := chain("f", "f", "")
				dev.Qualified = vcsWatermark("f")
				test.Qualified = vcsWatermark("f")
				prod.Manual = true
				moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test, prod})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(BeEmpty(), "nobody has chosen a commit for prod")

				prod.LiveDrySha = "x"
				moves, err = vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test, prod})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(BeEmpty())
			})

			It("creates a root's tag directly at the DRY branch HEAD", func() {
				dev, _, _ := chain("", "", "")
				dev.LiveDrySha = "c"
				moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsDev, Tag: vcsTagDev, From: "", To: "f"}}))
			})

			It("creates a dependent's tag directly at its upstream's watermark when that is ahead of what is live", func() {
				dev, test, _ := chain("f", "", "")
				dev.Qualified = vcsWatermark("e")
				test.LiveDrySha = "c"
				moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsTest, Tag: vcsTagTest, From: "", To: "e"}}))
			})

			It("does not create a dependent's tag behind what is live", func() {
				dev, test, _ := chain("f", "", "")
				dev.Qualified = vcsWatermark("c")
				test.LiveDrySha = "e"
				moves, err := vcsMoves(ctx, graph, "f", []vcsEnvironment{dev, test})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsTest, Tag: vcsTagTest, From: "", To: "e"}}))
			})
		})

		Context("after the DRY branch is force-pushed", func() {
			It("resets a root's tag that is no longer reachable from HEAD", func() {
				dev, _, _ := chain("e", "", "")
				moves, err := vcsMoves(ctx, graph, "q", []vcsEnvironment{dev})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsDev, Tag: vcsTagDev, From: "e", To: "q"}}))
			})

			It("leaves a dependent's unreachable tag until an upstream qualifies a commit on the new history", func() {
				dev, test, _ := chain("q", "d", "")
				moves, err := vcsMoves(ctx, graph, "q", []vcsEnvironment{dev, test})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(BeEmpty())

				dev.Qualified = vcsWatermark("p")
				moves, err = vcsMoves(ctx, graph, "q", []vcsEnvironment{dev, test})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsTest, Tag: vcsTagTest, From: "d", To: "p"}}))
			})

			It("resets a dependent's unreachable tag even when the target is not ahead of it", func() {
				dev, test, _ := chain("q", "e", "")
				dev.Qualified = vcsWatermark("b")
				moves, err := vcsMoves(ctx, graph, "q", []vcsEnvironment{dev, test})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsTest, Tag: vcsTagTest, From: "e", To: "b"}}))
			})

			It("leaves a validation: Manual tag alone", func() {
				dev, _, _ := chain("e", "", "")
				dev.Manual = true
				moves, err := vcsMoves(ctx, graph, "q", []vcsEnvironment{dev})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(BeEmpty())
			})
		})

		Context("what put a tag where it is", func() {
			// recorded is a tag as status has it.
			recorded := func(sha string, source promoterv1alpha1.ValidatedTagSource) *promoterv1alpha1.ValidatedCommitStatusTagStatus {
				return &promoterv1alpha1.ValidatedCommitStatusTagStatus{Name: vcsTagTest, Sha: sha, Source: source}
			}
			// planTest plans test's tag in dev → test, where dev has qualified devQualified ("" for nothing).
			planTest := func(head, devQualified string, test vcsEnvironment) vcsTagPlan {
				GinkgoHelper()
				dev := vcsSettled(vcsDev, vcsTagDev, head, head)
				if devQualified != "" {
					dev.Qualified = vcsWatermark(devQualified)
				}
				test.DependsOn = []string{vcsDev}
				plans, err := vcsPlanTags(ctx, graph, head, []vcsEnvironment{dev, test}, vcsPlanTime)
				Expect(err).NotTo(HaveOccurred())
				return plans[1]
			}
			testAt := func(tagSha string, record *promoterv1alpha1.ValidatedCommitStatusTagStatus) vcsEnvironment {
				env := vcsSettled(vcsTest, vcsTagTest, tagSha, tagSha)
				env.Recorded = record
				return env
			}

			It("takes a tag it has no record of as its own for an environment it validates for", func() {
				plan := planTest("f", "e", testAt("c", nil))
				Expect(plan.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceAutomatic))
				Expect(plan.To).To(Equal("e"))
				Expect(plan.MovedByHand).To(BeFalse())
			})

			It("takes a tag it has no record of as a person's for an environment people validate for", func() {
				test := testAt("c", nil)
				test.Manual = true
				plan := planTest("f", "e", test)
				Expect(plan.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceExternal))
				Expect(plan.To).To(Equal("c"))
				Expect(plan.MovedByHand).To(BeFalse(), "nothing says it was moved")
			})

			It("keeps what status says about a tag that is where status has it", func() {
				at := metav1.NewTime(vcsPlanTime.Add(-time.Hour))
				record := recorded("c", promoterv1alpha1.ValidatedTagSourceSeeded)
				record.MovedAt = &at
				plan := planTest("f", "c", testAt("c", record))
				Expect(plan.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceSeeded))
				Expect(plan.MovedAt).To(Equal(&at))
				Expect(plan.moves()).To(BeFalse())
			})

			It("takes a tag that moved forward to within its target as its own move, whose record was lost", func() {
				plan := planTest("f", "e", testAt("d", recorded("c", promoterv1alpha1.ValidatedTagSourceAutomatic)))
				Expect(plan.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceAutomatic))
				Expect(plan.MovedByHand).To(BeFalse())
				Expect(plan.To).To(Equal("e"), "and goes on from there")
			})

			It("takes a tag that was created where it would create it as its own, whose record was lost", func() {
				test := testAt("c", recorded("", ""))
				plan := planTest("f", "c", test)
				Expect(plan.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceSeeded))
				Expect(plan.MovedByHand).To(BeFalse())
			})

			It("takes a tag that appeared anywhere else as created by hand", func() {
				test := testAt("b", recorded("", ""))
				test.LiveDrySha = "c"
				test.Status = vcsStatus(vcsTest, "c", "c")
				plan := planTest("f", "c", test)
				Expect(plan.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceExternal))
				Expect(plan.MovedByHand).To(BeTrue())
			})

			It("takes a tag that moved backward as moved by hand, and leaves it there", func() {
				plan := planTest("f", "e", testAt("b", recorded("e", promoterv1alpha1.ValidatedTagSourceAutomatic)))
				Expect(plan.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceExternal))
				Expect(plan.MovedByHand).To(BeTrue())
				Expect(plan.MovedAt).To(Equal(&vcsPlanTime))
				Expect(plan.Acknowledged).To(Equal("e"), "what the controller had for the tag when it was found")
				Expect(plan.moves()).To(BeFalse(), "e is nothing new")
				Expect(plan.Held).To(BeNil())
			})

			It("takes a tag that moved past its target as moved by hand, and never moves it back", func() {
				plan := planTest("f", "d", testAt("e", recorded("d", promoterv1alpha1.ValidatedTagSourceAutomatic)))
				Expect(plan.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceExternal))
				Expect(plan.MovedByHand).To(BeTrue())
				Expect(plan.moves()).To(BeFalse())
			})

			It("takes any move of a tag people validate for as made by hand, and acknowledges nothing for it", func() {
				test := testAt("d", recorded("c", promoterv1alpha1.ValidatedTagSourceExternal))
				test.Manual = true
				plan := planTest("f", "e", test)
				Expect(plan.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceExternal))
				Expect(plan.MovedByHand).To(BeTrue())
				Expect(plan.Acknowledged).To(BeEmpty())
				Expect(plan.moves()).To(BeFalse())
			})

			It("reports where an automatically validated tag is headed, also when it does not move", func() {
				Expect(planTest("f", "e", testAt("e", nil)).Target).To(Equal("e"))
				Expect(planTest("f", "", testAt("c", nil)).Target).To(BeEmpty())
			})
		})

		Context("a tag a person placed", func() {
			external := func(sha, acknowledged string) *promoterv1alpha1.ValidatedCommitStatusTagStatus {
				return &promoterv1alpha1.ValidatedCommitStatusTagStatus{
					Name: vcsTagTest, Sha: sha, Source: promoterv1alpha1.ValidatedTagSourceExternal, AcknowledgedTarget: acknowledged,
				}
			}
			planTest := func(head, devQualified string, test vcsEnvironment) vcsTagPlan {
				GinkgoHelper()
				dev := vcsSettled(vcsDev, vcsTagDev, head, head)
				if devQualified != "" {
					dev.Qualified = vcsWatermark(devQualified)
				}
				test.DependsOn = []string{vcsDev}
				plans, err := vcsPlanTags(ctx, graph, head, []vcsEnvironment{dev, test}, vcsPlanTime)
				Expect(err).NotTo(HaveOccurred())
				return plans[1]
			}
			testAt := func(tagSha string, record *promoterv1alpha1.ValidatedCommitStatusTagStatus) vcsEnvironment {
				env := vcsSettled(vcsTest, vcsTagTest, tagSha, tagSha)
				env.Recorded = record
				return env
			}

			It("stays while the controller has nothing newer than it had when the tag was placed", func() {
				plan := planTest("f", "d", testAt("b", external("b", "d")))
				Expect(plan.moves()).To(BeFalse())
				Expect(plan.Acknowledged).To(Equal("d"))

				plan = planTest("f", "c", testAt("b", external("b", "d")))
				Expect(plan.moves()).To(BeFalse(), "an older target, after a restore upstream, is nothing new either")
			})

			It("is taken over once the upstreams qualify something newer that contains it", func() {
				plan := planTest("f", "e", testAt("b", external("b", "d")))
				Expect(plan.To).To(Equal("e"))
				Expect(plan.MoveSource).To(Equal(promoterv1alpha1.ValidatedTagSourceAutomatic))
				Expect(plan.tagStatus(true, vcsPlanTime).AcknowledgedTarget).To(BeEmpty(), "the controller placed it now")
				Expect(plan.tagStatus(false, vcsPlanTime).AcknowledgedTarget).To(Equal("d"), "a push that failed changes nothing")
			})

			It("is taken over as soon as something is cleared, when nothing was at the time", func() {
				plan := planTest("f", "", testAt("b", external("b", "")))
				Expect(plan.moves()).To(BeFalse())
				plan = planTest("f", "c", testAt("b", external("b", "")))
				Expect(plan.To).To(Equal("c"))
			})

			It("is not moved back when it is ahead of something newly qualified", func() {
				plan := planTest("f", "d", testAt("e", external("e", "c")))
				Expect(plan.moves()).To(BeFalse())
				Expect(plan.Held).To(BeNil())
			})

			It("is held as Pinned when something newer is qualified that does not contain it", func() {
				// m merges l and r. The tag was put on r while dev had qualified b; dev then qualifies l.
				plan := planTest("m", "l", testAt("r", external("r", "b")))
				Expect(plan.moves()).To(BeFalse())
				Expect(plan.Held).To(Equal(&promoterv1alpha1.ValidatedCommitStatusHeld{
					Sha:     "l",
					Reason:  promoterv1alpha1.ValidatedHeldReasonPinned,
					Message: "Waiting for the dry branch to contain r",
				}))

				By("moving on once the upstream qualifies a commit that contains it")
				plan = planTest("m", "m", testAt("r", external("r", "b")))
				Expect(plan.To).To(Equal("m"))
				Expect(plan.Held).To(BeNil())
			})

			It("is put back on the DRY branch when it is not on it: nothing off the branch passes the gate", func() {
				plan := planTest("f", "d", testAt("x", external("x", "d")))
				Expect(plan.To).To(Equal("d"))
				Expect(plan.MoveSource).To(Equal(promoterv1alpha1.ValidatedTagSourceAutomatic))
			})

			It("is taken over when the target it acknowledged was rewritten away", func() {
				// The DRY branch was force-pushed from f to q after the tag was put on b with e acknowledged.
				plan := planTest("q", "p", testAt("b", external("b", "e")))
				Expect(plan.To).To(Equal("p"))
			})

			It("is left alone, wherever it is, in an environment people validate for", func() {
				for _, tagSha := range []string{"b", "f", "x"} {
					test := testAt(tagSha, external(tagSha, ""))
					test.Manual = true
					plan := planTest("f", "d", test)
					Expect(plan.moves()).To(BeFalse(), "tag at %s", tagSha)
					Expect(plan.Held).To(BeNil())
				}
			})
		})

		Context("a tag that was deleted", func() {
			It("is put back where it was, with what was known about it", func() {
				test := vcsSettled(vcsTest, vcsTagTest, "", "d")
				test.DependsOn = []string{vcsDev}
				test.Recorded = &promoterv1alpha1.ValidatedCommitStatusTagStatus{
					Name: vcsTagTest, Sha: "b", Source: promoterv1alpha1.ValidatedTagSourceExternal, AcknowledgedTarget: "d",
				}
				dev := vcsSettled(vcsDev, vcsTagDev, "f", "f")
				dev.Qualified = vcsWatermark("d")
				plans, err := vcsPlanTags(ctx, graph, "f", []vcsEnvironment{dev, test}, vcsPlanTime)
				Expect(err).NotTo(HaveOccurred())
				Expect(plans[1].To).To(Equal("b"))
				Expect(plans[1].MovedByHand).To(BeTrue())

				status := plans[1].tagStatus(true, vcsPlanTime)
				Expect(status.Sha).To(Equal("b"))
				Expect(status.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceExternal))
				Expect(status.AcknowledgedTarget).To(Equal("d"))
			})

			It("is created afresh when the commit it was on is gone", func() {
				dev := vcsSettled(vcsDev, vcsTagDev, "", "")
				dev.Recorded = &promoterv1alpha1.ValidatedCommitStatusTagStatus{
					Name: vcsTagDev, Sha: "gone", Source: promoterv1alpha1.ValidatedTagSourceAutomatic,
				}
				plans, err := vcsPlanTags(ctx, graph, "f", []vcsEnvironment{dev}, vcsPlanTime)
				Expect(err).NotTo(HaveOccurred())
				Expect(plans[0].To).To(Equal("f"))
				Expect(plans[0].MoveSource).To(Equal(promoterv1alpha1.ValidatedTagSourceSeeded))
			})
		})

		Context("a validation", func() {
			const jane = "jane@example.com"
			// planTest plans test's tag in dev → test, where dev has qualified devQualified ("" for nothing).
			planTest := func(head, devQualified string, test vcsEnvironment) vcsTagPlan {
				GinkgoHelper()
				dev := vcsSettled(vcsDev, vcsTagDev, head, head)
				if devQualified != "" {
					dev.Qualified = vcsWatermark(devQualified)
				}
				test.DependsOn = []string{vcsDev}
				plans, err := vcsPlanTags(ctx, graph, head, []vcsEnvironment{dev, test}, vcsPlanTime)
				Expect(err).NotTo(HaveOccurred())
				return plans[1]
			}
			// validated returns test with its tag on tagSha, where status has it, and a validation of sha.
			validated := func(tagSha, sha string) vcsEnvironment {
				env := vcsSettled(vcsTest, vcsTagTest, tagSha, tagSha)
				env.Recorded = &promoterv1alpha1.ValidatedCommitStatusTagStatus{
					Name: vcsTagTest, Sha: tagSha, Source: promoterv1alpha1.ValidatedTagSourceAutomatic,
				}
				env.Validation = &vcsValidation{Sha: sha, By: jane}
				return env
			}

			DescribeTable("moves the tag to its commit, wherever that is",
				func(sha string) {
					plan := planTest("f", "d", validated("d", sha))
					Expect(plan.To).To(Equal(sha))
					Expect(plan.MoveSource).To(Equal(promoterv1alpha1.ValidatedTagSourceManual))
					Expect(plan.Held).To(BeNil())

					status := plan.tagStatus(true, vcsPlanTime)
					Expect(status.Sha).To(Equal(sha))
					Expect(status.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceManual))
					Expect(status.ValidatedBy).To(Equal(jane))
					Expect(status.MovedAt).To(Equal(&vcsPlanTime))
					Expect(status.AcknowledgedTarget).To(Equal("d"), "what the controller had for the tag when it was validated")
				},
				Entry("ahead of what the upstreams have qualified", "f"),
				Entry("behind the tag", "b"),
				Entry("off the DRY branch", "x"),
			)

			It("comes before the controller's own move, in the same reconcile", func() {
				plan := planTest("f", "e", validated("c", "b"))
				Expect(plan.To).To(Equal("b"), "not e, which the controller would have moved the tag to")
			})

			It("makes a validated tag of the tag that is on its commit already, without a push", func() {
				plan := planTest("f", "d", validated("d", "d"))
				Expect(plan.moves()).To(BeFalse())
				Expect(plan.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceManual))
				Expect(plan.ValidatedBy).To(Equal(jane))
				Expect(plan.Acknowledged).To(Equal("d"))
				Expect(plan.MovedAt).To(Equal(&vcsPlanTime))
			})

			It("explains a tag found on its commit: the controller moved it there and did not get to say so", func() {
				test := validated("c", "x")
				test.TagSha = "x"
				plan := planTest("f", "d", test)
				Expect(plan.moves()).To(BeFalse())
				Expect(plan.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceManual))
				Expect(plan.MovedByHand).To(BeFalse())
			})

			It("creates a tag that does not exist on its commit", func() {
				test := vcsSettled(vcsTest, vcsTagTest, "", "")
				test.Manual = true
				test.Validation = &vcsValidation{Sha: "e", By: jane}
				plan := planTest("f", "c", test)
				Expect(plan.To).To(Equal("e"))
				status := plan.tagStatus(true, vcsPlanTime)
				Expect(status.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceManual))
				Expect(status.AcknowledgedTarget).To(BeEmpty(), "the controller moves nothing for an environment people validate for")
			})

			It("is tried again when its move could not be pushed", func() {
				test := validated("d", "b")
				test.Validation.Pending = true
				plan := planTest("f", "e", test)
				Expect(plan.To).To(Equal("b"), "and the controller moves the tag nowhere else meanwhile")
			})

			It("gives way to a push of the tag by hand that came after it", func() {
				test := validated("d", "b")
				test.Validation.Pending = true
				test.TagSha = "c"
				plan := planTest("f", "e", test)
				Expect(plan.MovedByHand).To(BeTrue())
				Expect(plan.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceExternal))
				Expect(plan.moves()).To(BeFalse())
			})

			It("does not give way to a push by hand when it was written after the notes were last applied", func() {
				test := validated("d", "b")
				test.TagSha = "c"
				plan := planTest("f", "e", test)
				Expect(plan.To).To(Equal("b"))
			})
		})

		Context("a tag a validation placed", func() {
			planTest := func(head, devQualified string, test vcsEnvironment) vcsTagPlan {
				GinkgoHelper()
				dev := vcsSettled(vcsDev, vcsTagDev, head, head)
				if devQualified != "" {
					dev.Qualified = vcsWatermark(devQualified)
				}
				test.DependsOn = []string{vcsDev}
				plans, err := vcsPlanTags(ctx, graph, head, []vcsEnvironment{dev, test}, vcsPlanTime)
				Expect(err).NotTo(HaveOccurred())
				return plans[1]
			}
			manual := func(tagSha, acknowledged string) vcsEnvironment {
				env := vcsSettled(vcsTest, vcsTagTest, tagSha, tagSha)
				env.Recorded = &promoterv1alpha1.ValidatedCommitStatusTagStatus{
					Name: vcsTagTest, Sha: tagSha, Source: promoterv1alpha1.ValidatedTagSourceManual,
					ValidatedBy: "jane@example.com", AcknowledgedTarget: acknowledged,
				}
				return env
			}

			It("stays, validated, while the upstreams qualify nothing newer", func() {
				plan := planTest("f", "d", manual("b", "d"))
				Expect(plan.moves()).To(BeFalse())
				status := plan.tagStatus(false, vcsPlanTime)
				Expect(status.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceManual))
				Expect(status.ValidatedBy).To(Equal("jane@example.com"))
			})

			It("is taken over by the controller once the upstreams qualify something newer that contains it", func() {
				plan := planTest("f", "e", manual("b", "d"))
				Expect(plan.To).To(Equal("e"))
				status := plan.tagStatus(true, vcsPlanTime)
				Expect(status.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceAutomatic))
				Expect(status.ValidatedBy).To(BeEmpty())
				Expect(status.AcknowledgedTarget).To(BeEmpty())
			})

			It("is left off the DRY branch, and holds what the upstreams qualify next as Pinned", func() {
				// x is a hotfix on c. The tag was put there when dev had qualified c.
				plan := planTest("f", "c", manual("x", "c"))
				Expect(plan.moves()).To(BeFalse(), "a validated tag is not put back on the branch")
				Expect(plan.Held).To(BeNil())

				plan = planTest("f", "e", manual("x", "c"))
				Expect(plan.moves()).To(BeFalse())
				Expect(plan.Held).NotTo(BeNil())
				Expect(plan.Held.Reason).To(Equal(promoterv1alpha1.ValidatedHeldReasonPinned))
				Expect(plan.Held.Sha).To(Equal("e"))
			})

			It("is released once the DRY branch contains it", func() {
				// r was validated for test when dev had qualified b. m merges it into the DRY branch.
				plan := planTest("m", "m", manual("r", "b"))
				Expect(plan.To).To(Equal("m"))
			})
		})

		Context("a tag the gate has no record of, that the validation notes know", func() {
			at := metav1.NewTime(time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC))
			noted := func(source promoterv1alpha1.ValidatedTagSource, by string) vcsEnvironment {
				env := vcsSettled(vcsTest, vcsTagTest, "b", "b")
				env.DependsOn = []string{vcsDev}
				env.Noted = &vcsNotedTag{At: at, By: by, Source: source}
				return env
			}
			plan := func(test vcsEnvironment) vcsTagPlan {
				GinkgoHelper()
				dev := vcsSettled(vcsDev, vcsTagDev, "f", "f")
				dev.Qualified = vcsWatermark("e")
				plans, err := vcsPlanTags(ctx, graph, "f", []vcsEnvironment{dev, test}, vcsPlanTime)
				Expect(err).NotTo(HaveOccurred())
				return plans[1]
			}

			It("is a validated tag again, and stays although the upstreams have qualified more", func() {
				got := plan(noted(promoterv1alpha1.ValidatedTagSourceManual, "jane@example.com"))
				Expect(got.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceManual))
				Expect(got.ValidatedBy).To(Equal("jane@example.com"))
				Expect(got.MovedAt).To(Equal(&at))
				Expect(got.Acknowledged).To(Equal("e"))
				Expect(got.moves()).To(BeFalse(), "a rollback is not undone by a gate that lost its status")
			})

			It("is a tag moved by hand again, with the time the move was first seen", func() {
				got := plan(noted(promoterv1alpha1.ValidatedTagSourceExternal, ""))
				Expect(got.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceExternal))
				Expect(got.MovedAt).To(Equal(&at))
				Expect(got.MovedByHand).To(BeFalse(), "it is not a new move")
				Expect(got.moves()).To(BeFalse())
			})

			It("goes by status when status has a record", func() {
				test := noted(promoterv1alpha1.ValidatedTagSourceManual, "jane@example.com")
				test.Recorded = &promoterv1alpha1.ValidatedCommitStatusTagStatus{
					Name: vcsTagTest, Sha: "b", Source: promoterv1alpha1.ValidatedTagSourceAutomatic,
				}
				Expect(plan(test).To).To(Equal("e"))
			})
		})

		It("takes over the time of a hand move that is in the notes already, so it is not recorded twice", func() {
			seen := metav1.NewTime(vcsPlanTime.Add(-time.Minute))
			test := vcsSettled(vcsTest, vcsTagTest, "b", "b")
			test.DependsOn = []string{vcsDev}
			test.Recorded = &promoterv1alpha1.ValidatedCommitStatusTagStatus{
				Name: vcsTagTest, Sha: "e", Source: promoterv1alpha1.ValidatedTagSourceAutomatic,
			}
			test.HandMoves = map[string]metav1.Time{"b": seen, "c": metav1.NewTime(vcsPlanTime.Add(-time.Hour))}
			dev := vcsSettled(vcsDev, vcsTagDev, "f", "f")
			dev.Qualified = vcsWatermark("e")
			plans, err := vcsPlanTags(ctx, graph, "f", []vcsEnvironment{dev, test}, vcsPlanTime)
			Expect(err).NotTo(HaveOccurred())
			Expect(plans[1].MovedByHand).To(BeTrue())
			Expect(plans[1].MovedAt).To(Equal(&seen))
		})

		It("records nothing about a tag that does not exist", func() {
			plan := vcsTagPlan{Tag: vcsTagTest}
			Expect(plan.tagStatus(false, vcsPlanTime)).To(Equal(&promoterv1alpha1.ValidatedCommitStatusTagStatus{Name: vcsTagTest}))
		})

		It("returns ancestry errors", func() {
			dev, test, prod := chain("d", "d", "d")
			_, err := vcsMoves(ctx, vcsFailingGraph{}, "f", []vcsEnvironment{dev, test, prod})
			Expect(err).To(MatchError(ContainSubstring("graph unavailable")))
		})
	})

	Describe("vcsEvaluateGate: whether an environment's proposed commit may be promoted", func() {
		It("is pending until the environment's tag exists", func() {
			dev := vcsProposing(vcsDev, vcsTagDev, "", "c", "d")
			gate, err := vcsEvaluateGate(ctx, graph, "f", "main", dev, vcsByBranch(dev))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(pending))
			Expect(gate.Description).To(Equal(`Waiting for tag "env/dev" to be created`))
		})

		Context("for a tag a validation placed", func() {
			// validatedTest returns test, which depends on dev, proposing the commit its validated tag is on.
			validatedTest := func(tagSha, proposed string) (vcsEnvironment, map[string]vcsEnvironment) {
				dev := vcsSettled(vcsDev, vcsTagDev, "f", "f")
				dev.Qualified = vcsWatermark("c")
				test := vcsProposing(vcsTest, vcsTagTest, tagSha, "b", proposed, vcsDev)
				test.TagSource = promoterv1alpha1.ValidatedTagSourceManual
				test.TagValidatedBy = "CommitValidation/demo-test"
				return test, vcsByBranch(dev, test)
			}

			DescribeTable("succeeds without asking the upstreams, wherever the commit is",
				func(tagSha string) {
					test, byBranch := validatedTest(tagSha, tagSha)
					gate, err := vcsEvaluateGate(ctx, graph, "f", "main", test, byBranch)
					Expect(err).NotTo(HaveOccurred())
					Expect(gate.Phase).To(Equal(success))
					Expect(gate.Description).To(Equal(`Tag "env/test" at ` + tagSha + ` is hydrated and was validated by CommitValidation/demo-test`))
					Expect(gate.Upstreams).To(BeEmpty())
				},
				Entry("ahead of what dev has qualified", "e"),
				Entry("off the DRY branch", "x"),
				Entry("on history the DRY branch no longer has", "q"),
			)

			It("is still pending until the hydrator has processed the validated commit", func() {
				test, byBranch := validatedTest("e", "d")
				gate, err := vcsEvaluateGate(ctx, graph, "f", "main", test, byBranch)
				Expect(err).NotTo(HaveOccurred())
				Expect(gate.Phase).To(Equal(pending))
				Expect(gate.Description).To(Equal(`Waiting for hydrator to process tag "env/test" at e (currently d)`))
			})

			It("does not need the graph", func() {
				test, byBranch := validatedTest("e", "e")
				gate, err := vcsEvaluateGate(ctx, vcsFailingGraph{}, "f", "main", test, byBranch)
				Expect(err).NotTo(HaveOccurred())
				Expect(gate.Phase).To(Equal(success))
			})

			It("says a person validated when it is not known who, and shortens a long name", func() {
				test, byBranch := validatedTest("e", "e")
				test.TagValidatedBy = ""
				gate, err := vcsEvaluateGate(ctx, graph, "f", "main", test, byBranch)
				Expect(err).NotTo(HaveOccurred())
				Expect(gate.Description).To(Equal(`Tag "env/test" at e is hydrated and was validated by a person`))

				test.TagValidatedBy = strings.Repeat("a", 100)
				gate, err = vcsEvaluateGate(ctx, graph, "f", "main", test, byBranch)
				Expect(err).NotTo(HaveOccurred())
				Expect(gate.Description).To(HaveSuffix("validated by " + strings.Repeat("a", 60)))
			})

			DescribeTable("is not what the gate does for any other tag on the same commit",
				func(source promoterv1alpha1.ValidatedTagSource) {
					test, byBranch := validatedTest("e", "e")
					test.TagSource = source
					gate, err := vcsEvaluateGate(ctx, graph, "f", "main", test, byBranch)
					Expect(err).NotTo(HaveOccurred())
					Expect(gate.Phase).To(Equal(pending))
					Expect(gate.Description).To(Equal(`Waiting for "environment/dev" to qualify dry e (newest qualified: c)`))
				},
				Entry("pushed by hand", promoterv1alpha1.ValidatedTagSourceExternal),
				Entry("moved by the controller", promoterv1alpha1.ValidatedTagSourceAutomatic),
				Entry("created by the controller", promoterv1alpha1.ValidatedTagSourceSeeded),
				Entry("of unknown origin", promoterv1alpha1.ValidatedTagSource("")),
			)
		})

		It("is pending until the hydrator has processed the tagged commit", func() {
			dev := vcsProposing(vcsDev, vcsTagDev, "f", "c", "e")
			gate, err := vcsEvaluateGate(ctx, graph, "f", "main", dev, vcsByBranch(dev))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(pending))
			Expect(gate.Description).To(Equal(`Waiting for hydrator to process tag "env/dev" at f (currently e)`))
		})

		It("is pending when the hydrator has reported nothing", func() {
			dev := vcsProposing(vcsDev, vcsTagDev, "f", "c", "")
			gate, err := vcsEvaluateGate(ctx, graph, "f", "main", dev, vcsByBranch(dev))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(pending))
			Expect(gate.Description).To(Equal(`Waiting for hydrator to process tag "env/dev" at f (currently none)`))
		})

		It("shortens SHAs to seven characters in descriptions", func() {
			const tagSha = "0123456789abcdef0123456789abcdef01234567"
			const proposedSha = "fedcba9876543210fedcba9876543210fedcba98"
			dev := vcsProposing(vcsDev, vcsTagDev, tagSha, "c", proposedSha)
			gate, err := vcsEvaluateGate(ctx, graph, "f", "main", dev, vcsByBranch(dev))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Description).To(Equal(`Waiting for hydrator to process tag "env/dev" at 0123456 (currently fedcba9)`))
		})

		It("prefers the hydrator note over hydrator.metadata for the hydrated DRY SHA", func() {
			dev := vcsProposing(vcsDev, vcsTagDev, "f", "c", "e")
			dev.Status = vcsWithNote(dev.Status, "f")
			gate, err := vcsEvaluateGate(ctx, graph, "f", "main", dev, vcsByBranch(dev))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(success))
		})

		It("succeeds for a root once the proposed commit was hydrated from its tag", func() {
			dev := vcsProposing(vcsDev, vcsTagDev, "f", "c", "f")
			gate, err := vcsEvaluateGate(ctx, graph, "f", "main", dev, vcsByBranch(dev))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(success))
			Expect(gate.Description).To(Equal(`Tag "env/dev" at f is hydrated`))
			Expect(gate.Upstreams).To(BeEmpty())
		})

		It("succeeds for a dependent when its upstream qualified exactly the tagged commit", func() {
			dev := vcsSettled(vcsDev, vcsTagDev, "f", "f", success)
			dev.Qualified = vcsWatermark("e")
			test := vcsProposing(vcsTest, vcsTagTest, "e", "c", "e", vcsDev)
			gate, err := vcsEvaluateGate(ctx, graph, "f", "main", test, vcsByBranch(dev, test))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(success))
			Expect(gate.Description).To(Equal(`Tag "env/test" at e is hydrated and qualified upstream`))
			Expect(gate.Upstreams).To(Equal([]promoterv1alpha1.ValidatedCommitStatusUpstreamStatus{
				{Branch: vcsDev, Satisfied: true},
			}))
		})

		It("succeeds when the upstream qualified a newer commit that contains the tagged commit", func() {
			// The upstream never ran d itself. It ran f, which contains d.
			dev := vcsSettled(vcsDev, vcsTagDev, "f", "f", success)
			dev.Qualified = vcsWatermark("f")
			test := vcsProposing(vcsTest, vcsTagTest, "d", "c", "d", vcsDev)
			gate, err := vcsEvaluateGate(ctx, graph, "f", "main", test, vcsByBranch(dev, test))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(success))
		})

		It("stays successful when the upstream turns unhealthy after qualifying", func() {
			dev := vcsSettled(vcsDev, vcsTagDev, "f", "f", failure)
			dev.Qualified = vcsWatermark("f")
			test := vcsProposing(vcsTest, vcsTagTest, "f", "c", "f", vcsDev)
			gate, err := vcsEvaluateGate(ctx, graph, "f", "main", test, vcsByBranch(dev, test))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(success))
		})

		It("is pending when a person pushed the tag ahead of what the upstream has qualified", func() {
			dev := vcsSettled(vcsDev, vcsTagDev, "f", "f", pending)
			dev.Qualified = vcsWatermark("d")
			test := vcsProposing(vcsTest, vcsTagTest, "f", "c", "f", vcsDev)
			test.Manual = true
			gate, err := vcsEvaluateGate(ctx, graph, "f", "main", test, vcsByBranch(dev, test))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(pending))
			Expect(gate.Description).To(Equal(`Waiting for "environment/dev" to qualify dry f (newest qualified: d)`))
			Expect(gate.Upstreams).To(Equal([]promoterv1alpha1.ValidatedCommitStatusUpstreamStatus{
				{Branch: vcsDev, Satisfied: false, Reason: `Waiting for "environment/dev" to qualify dry f (newest qualified: d)`},
			}))
		})

		It("is pending when the upstream has qualified nothing", func() {
			dev := vcsSettled(vcsDev, vcsTagDev, "f", "f", pending)
			test := vcsProposing(vcsTest, vcsTagTest, "f", "c", "f", vcsDev)
			gate, err := vcsEvaluateGate(ctx, graph, "f", "main", test, vcsByBranch(dev, test))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(pending))
			Expect(gate.Description).To(Equal(`Waiting for "environment/dev" to qualify dry f (newest qualified: none)`))
		})

		It("succeeds for a tag a person moved backward", func() {
			dev := vcsSettled(vcsDev, vcsTagDev, "f", "f", success)
			dev.Qualified = vcsWatermark("f")
			test := vcsProposing(vcsTest, vcsTagTest, "b", "e", "b", vcsDev)
			test.Manual = true
			gate, err := vcsEvaluateGate(ctx, graph, "f", "main", test, vcsByBranch(dev, test))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(success))
		})

		It("goes pending when a RestoreActiveCommit pulled the upstream's watermark back", func() {
			// test's tag already advanced to e. dev was then restored to c, revoking d and e.
			dev := vcsSettled(vcsDev, vcsTagDev, "f", "c", success)
			dev.Qualified = vcsWatermark("c")
			test := vcsProposing(vcsTest, vcsTagTest, "e", "c", "e", vcsDev)
			gate, err := vcsEvaluateGate(ctx, graph, "f", "main", test, vcsByBranch(dev, test))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(pending))
			Expect(gate.Description).To(Equal(`Waiting for "environment/dev" to qualify dry e (newest qualified: c)`))
		})

		It("is pending for a tagged commit that is not on the DRY branch", func() {
			dev := vcsProposing(vcsDev, vcsTagDev, "x", "c", "x")
			dev.Manual = true
			gate, err := vcsEvaluateGate(ctx, graph, "f", "main", dev, vcsByBranch(dev))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(pending))
			Expect(gate.Description).To(Equal(`Tag "env/dev" at x is not on dry branch "main"`))
		})

		It("is pending for a tagged commit that was rewritten away by a force-push", func() {
			dev := vcsSettled(vcsDev, vcsTagDev, "q", "q", success)
			dev.Qualified = vcsWatermark("q")
			test := vcsProposing(vcsTest, vcsTagTest, "e", "c", "e", vcsDev)
			gate, err := vcsEvaluateGate(ctx, graph, "q", "main", test, vcsByBranch(dev, test))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(pending))
			Expect(gate.Description).To(Equal(`Tag "env/test" at e is not on dry branch "main"`))
		})

		It("looks only at direct upstreams", func() {
			dev := vcsSettled(vcsDev, vcsTagDev, "f", "f", pending)
			dev.Qualified = vcsWatermark("b")
			test := vcsSettled(vcsTest, vcsTagTest, "e", "e", success)
			test.DependsOn = []string{vcsDev}
			test.Qualified = vcsWatermark("e")
			prod := vcsProposing(vcsProd, vcsTagProd, "e", "c", "e", vcsTest)
			gate, err := vcsEvaluateGate(ctx, graph, "f", "main", prod, vcsByBranch(dev, test, prod))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(success), "test qualified e; how test got there is test's own gate's business")
			Expect(gate.Upstreams).To(HaveLen(1))
		})

		Context("fan-in", func() {
			var left, right, prod vcsEnvironment

			BeforeEach(func() {
				left = vcsSettled("environment/left", "env/left", "f", "f", success)
				right = vcsSettled("environment/right", "env/right", "f", "f", success)
				prod = vcsProposing(vcsProd, vcsTagProd, "e", "c", "e", left.Branch, right.Branch)
			})

			It("succeeds when every upstream qualified the tagged commit", func() {
				left.Qualified = vcsWatermark("f")
				right.Qualified = vcsWatermark("e")
				gate, err := vcsEvaluateGate(ctx, graph, "f", "main", prod, vcsByBranch(left, right, prod))
				Expect(err).NotTo(HaveOccurred())
				Expect(gate.Phase).To(Equal(success))
				Expect(gate.Upstreams).To(Equal([]promoterv1alpha1.ValidatedCommitStatusUpstreamStatus{
					{Branch: left.Branch, Satisfied: true},
					{Branch: right.Branch, Satisfied: true},
				}))
			})

			It("reports every upstream and describes the first one it is waiting for", func() {
				left.Qualified = vcsWatermark("f")
				right.Qualified = vcsWatermark("d")
				gate, err := vcsEvaluateGate(ctx, graph, "f", "main", prod, vcsByBranch(left, right, prod))
				Expect(err).NotTo(HaveOccurred())
				Expect(gate.Phase).To(Equal(pending))
				Expect(gate.Description).To(Equal(`Waiting for "environment/right" to qualify dry e (newest qualified: d)`))
				Expect(gate.Upstreams).To(Equal([]promoterv1alpha1.ValidatedCommitStatusUpstreamStatus{
					{Branch: left.Branch, Satisfied: true},
					{Branch: right.Branch, Satisfied: false, Reason: `Waiting for "environment/right" to qualify dry e (newest qualified: d)`},
				}))
			})
		})

		It("returns ancestry errors", func() {
			dev := vcsProposing(vcsDev, vcsTagDev, "f", "c", "f")
			_, err := vcsEvaluateGate(ctx, vcsFailingGraph{}, "f", "main", dev, vcsByBranch(dev))
			Expect(err).To(MatchError(ContainSubstring("graph unavailable")))
		})
	})

	Describe("a release moving through dev → test → prod", func() {
		// step runs one reconcile's worth of decisions and applies the planned tag moves, as the reconciler does.
		step := func(head string, envs []vcsEnvironment) ([]vcsEnvironment, []vcsTagMove) {
			GinkgoHelper()
			for i := range envs {
				qualified, err := vcsQualify(ctx, graph, head, envs[i], now)
				Expect(err).NotTo(HaveOccurred())
				envs[i].Qualified = qualified
			}
			moves, err := vcsMoves(ctx, graph, head, envs)
			Expect(err).NotTo(HaveOccurred())
			for _, move := range moves {
				idx := slices.IndexFunc(envs, func(e vcsEnvironment) bool { return e.Branch == move.Branch })
				Expect(envs[idx].TagSha).To(Equal(move.From), "the lease value must be the tag the plan was made from")
				envs[idx].TagSha = move.To
			}
			return envs, moves
		}
		// promote makes dry live in the environment at index i with the given active commit status phases.
		promote := func(envs []vcsEnvironment, i int, dry string, phases ...promoterv1alpha1.CommitStatusPhase) {
			envs[i].Status = vcsStatus(envs[i].Branch, dry, dry, phases...)
			envs[i].LiveDrySha = dry
		}
		gatePhase := func(head string, envs []vcsEnvironment, i int, proposedDry string) promoterv1alpha1.CommitStatusPhase {
			GinkgoHelper()
			env := envs[i]
			env.Status = vcsStatus(env.Branch, env.LiveDrySha, proposedDry)
			gate, err := vcsEvaluateGate(ctx, graph, head, "main", env, vcsByBranch(envs...))
			Expect(err).NotTo(HaveOccurred())
			return gate.Phase
		}

		It("holds higher environments on qualified commits while newer commits land", func() {
			envs := []vcsEnvironment{
				vcsSettled(vcsDev, vcsTagDev, "", "c", success),
				vcsSettled(vcsTest, vcsTagTest, "", "c", success),
				vcsSettled(vcsProd, vcsTagProd, "", "c", success),
			}
			envs[1].DependsOn = []string{vcsDev}
			envs[2].DependsOn = []string{vcsTest}

			By("bootstrapping: every environment qualifies what it runs; dev's tag goes to HEAD (d)")
			envs, moves := step("d", envs)
			Expect(moves).To(Equal([]vcsTagMove{
				{Branch: vcsDev, Tag: vcsTagDev, From: "", To: "d"},
				{Branch: vcsTest, Tag: vcsTagTest, From: "", To: "c"},
				{Branch: vcsProd, Tag: vcsTagProd, From: "", To: "c"},
			}))

			By("dev promotes d and its checks start running: nothing moves downstream")
			Expect(gatePhase("d", envs, 0, "d")).To(Equal(success))
			promote(envs, 0, "d", pending)
			envs, moves = step("d", envs)
			Expect(moves).To(BeEmpty())

			By("dev's checks pass on d: test's tag advances to d")
			promote(envs, 0, "d", success)
			envs, moves = step("d", envs)
			Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsTest, Tag: vcsTagTest, From: "c", To: "d"}}))

			By("e and f land before test finishes d: dev's tag follows HEAD, test keeps its candidate")
			envs, moves = step("f", envs)
			Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsDev, Tag: vcsTagDev, From: "d", To: "f"}}))
			Expect(gatePhase("f", envs, 1, "d")).To(Equal(success), "d is still promotable to test")

			By("test promotes d and passes: prod's tag advances to d even though HEAD is f")
			promote(envs, 1, "d", success)
			envs, moves = step("f", envs)
			Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsProd, Tag: vcsTagProd, From: "c", To: "d"}}))
			Expect(gatePhase("f", envs, 2, "d")).To(Equal(success))

			By("dev promotes f but its checks fail: prod's candidate is unaffected")
			promote(envs, 0, "f", failure)
			envs, moves = step("f", envs)
			Expect(moves).To(BeEmpty())
			Expect(gatePhase("f", envs, 2, "d")).To(Equal(success))

			By("dev recovers on f: test's tag skips e and goes straight to f")
			promote(envs, 0, "f", success)
			envs, moves = step("f", envs)
			Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsTest, Tag: vcsTagTest, From: "d", To: "f"}}))
			Expect(envs[2].TagSha).To(Equal("d"))
		})

		It("lets a person hold prod back and release a commit the upstream skipped", func() {
			envs := []vcsEnvironment{
				vcsSettled(vcsDev, vcsTagDev, "f", "f", success),
				vcsSettled(vcsTest, vcsTagTest, "f", "f", success),
				vcsSettled(vcsProd, vcsTagProd, "b", "b", success),
			}
			envs[1].DependsOn = []string{vcsDev}
			envs[2].DependsOn = []string{vcsTest}
			envs[2].Manual = true

			envs, moves := step("f", envs)
			Expect(moves).To(BeEmpty(), "prod's tag is not the controller's to move")

			By("tagging e, which test never ran itself but f contains")
			envs[2].TagSha = "e"
			Expect(gatePhase("f", envs, 2, "b")).To(Equal(pending), "the old proposed commit cannot merge after the tag moved")
			Expect(gatePhase("f", envs, 2, "e")).To(Equal(success))
		})

		It("stops a rollback in an upstream from being promoted over downstream", func() {
			envs := []vcsEnvironment{
				vcsSettled(vcsDev, vcsTagDev, "e", "e", success),
				vcsSettled(vcsTest, vcsTagTest, "c", "c", success),
			}
			envs[1].DependsOn = []string{vcsDev}
			envs, moves := step("e", envs)
			Expect(moves).To(Equal([]vcsTagMove{{Branch: vcsTest, Tag: vcsTagTest, From: "c", To: "e"}}))
			Expect(gatePhase("e", envs, 1, "e")).To(Equal(success))

			By("restoring dev to c before test merged e")
			promote(envs, 0, "c", success)
			envs[0].Restored = true
			envs, moves = step("e", envs)
			Expect(moves).To(BeEmpty(), "test's tag stays at e")
			Expect(envs[0].Qualified.DrySha).To(Equal("c"))
			Expect(gatePhase("e", envs, 1, "e")).To(Equal(pending))

			By("dev is promoted to e again and passes")
			envs[0].Restored = false
			promote(envs, 0, "e", success)
			envs, _ = step("e", envs)
			Expect(gatePhase("e", envs, 1, "e")).To(Equal(success))
		})
	})

	Describe("vcsNeedsNudge: when to wake an environment's ChangeTransferPolicy", func() {
		// SCMs send no webhook for a pushed git note, so a no-op hydration of a moved tag is invisible until
		// the ChangeTransferPolicy reconciles.
		It("is true while the hydrator has not reported the tagged commit", func() {
			Expect(vcsNeedsNudge(vcsProposing(vcsDev, vcsTagDev, "f", "c", "e"))).To(BeTrue())
			Expect(vcsNeedsNudge(vcsSettled(vcsDev, vcsTagDev, "f", "e"))).To(BeTrue())
		})

		It("is false once the hydrator has reported the tagged commit", func() {
			Expect(vcsNeedsNudge(vcsProposing(vcsDev, vcsTagDev, "f", "c", "f"))).To(BeFalse())
			env := vcsSettled(vcsDev, vcsTagDev, "f", "e")
			env.Status = vcsWithNote(env.Status, "f")
			Expect(vcsNeedsNudge(env)).To(BeFalse())
		})

		It("is false while the tag does not exist", func() {
			Expect(vcsNeedsNudge(vcsSettled(vcsDev, vcsTagDev, "", "e"))).To(BeFalse())
		})
	})

	Describe("vcsBackoff: pacing ChangeTransferPolicy nudges", func() {
		var (
			backoff *vcsBackoff
			start   time.Time
		)

		BeforeEach(func() {
			backoff = newVCSBackoff(15*time.Second, 2*time.Minute)
			start = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
		})

		It("fires immediately the first time and asks to be called back after the base delay", func() {
			fire, wait := backoff.next("ns/ctp", "f", start)
			Expect(fire).To(BeTrue())
			Expect(wait).To(Equal(15 * time.Second))
		})

		It("does not fire again before the delay has elapsed", func() {
			backoff.next("ns/ctp", "f", start)
			fire, wait := backoff.next("ns/ctp", "f", start.Add(5*time.Second))
			Expect(fire).To(BeFalse())
			Expect(wait).To(Equal(10 * time.Second))
		})

		It("doubles the delay on each firing up to the maximum", func() {
			at := start
			waits := make([]time.Duration, 0, 6)
			for range 6 {
				fire, wait := backoff.next("ns/ctp", "f", at)
				Expect(fire).To(BeTrue())
				waits = append(waits, wait)
				at = at.Add(wait)
			}
			Expect(waits).To(Equal([]time.Duration{
				15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 2 * time.Minute, 2 * time.Minute,
			}))
		})

		It("starts over when the tag moves to a different commit", func() {
			backoff.next("ns/ctp", "e", start)
			backoff.next("ns/ctp", "e", start.Add(15*time.Second))
			fire, wait := backoff.next("ns/ctp", "f", start.Add(16*time.Second))
			Expect(fire).To(BeTrue())
			Expect(wait).To(Equal(15 * time.Second))
		})

		It("tracks each ChangeTransferPolicy separately", func() {
			backoff.next("ns/dev", "f", start)
			fire, _ := backoff.next("ns/test", "f", start.Add(time.Second))
			Expect(fire).To(BeTrue())
		})

		It("starts over after forget", func() {
			backoff.next("ns/ctp", "f", start)
			backoff.forget("ns/ctp")
			fire, wait := backoff.next("ns/ctp", "f", start.Add(time.Second))
			Expect(fire).To(BeTrue())
			Expect(wait).To(Equal(15 * time.Second))
		})
	})

	Describe("vcsEnvironmentSettings: how each environment is set up", func() {
		strategy := func(activePath string, branches ...string) *promoterv1alpha1.PromotionStrategy {
			ps := &promoterv1alpha1.PromotionStrategy{}
			ps.Spec.ActivePath = activePath
			for _, branch := range branches {
				ps.Spec.Environments = append(ps.Spec.Environments, promoterv1alpha1.Environment{Branch: branch})
			}
			return ps
		}

		It("names every tag after its environment branch and has the controller move it, by default", func() {
			settings := vcsEnvironmentSettings(&promoterv1alpha1.ValidatedCommitStatus{}, strategy("", vcsDev, vcsProd))
			Expect(settings).To(Equal(map[string]vcsEnvironmentSetting{
				vcsDev:  {Tag: "environment/dev-tag"},
				vcsProd: {Tag: "environment/prod-tag"},
			}))
		})

		It("puts the activePath in the tag name, the environment's own before the strategy's", func() {
			ps := strategy("apps/one", vcsDev, vcsProd)
			ps.Spec.Environments[1].ActivePath = "apps/prod"
			settings := vcsEnvironmentSettings(&promoterv1alpha1.ValidatedCommitStatus{}, ps)
			Expect(settings[vcsDev].Tag).To(Equal("environment/dev-tag/apps/one"))
			Expect(settings[vcsProd].Tag).To(Equal("environment/prod-tag/apps/prod"))
		})

		It("takes the tag name and who validates from spec.environments", func() {
			gate := &promoterv1alpha1.ValidatedCommitStatus{}
			gate.Spec.Environments = []promoterv1alpha1.ValidatedCommitStatusEnvironment{
				{Branch: vcsTest, Tag: vcsTagTest},
				{Branch: vcsProd, Validation: promoterv1alpha1.ValidationManual},
			}
			settings := vcsEnvironmentSettings(gate, strategy("", vcsDev, vcsTest, vcsProd))
			Expect(settings).To(Equal(map[string]vcsEnvironmentSetting{
				vcsDev:  {Tag: "environment/dev-tag"},
				vcsTest: {Tag: vcsTagTest},
				vcsProd: {Tag: "environment/prod-tag", Manual: true},
			}))
		})

		It("has no setting for a branch that is not an environment of the strategy", func() {
			gate := &promoterv1alpha1.ValidatedCommitStatus{}
			gate.Spec.Environments = []promoterv1alpha1.ValidatedCommitStatusEnvironment{{Branch: "environment/extra"}}
			Expect(vcsEnvironmentSettings(gate, strategy("", vcsDev))).NotTo(HaveKey("environment/extra"))
		})
	})

	Describe("vcsValidateEnvironments: spec.environments must fit the PromotionStrategy", func() {
		branches := []string{vcsDev, vcsTest, vcsProd}
		defaults := map[string]vcsEnvironmentSetting{
			vcsDev:  {Tag: vcsTagDev},
			vcsTest: {Tag: vcsTagTest},
			vcsProd: {Tag: vcsTagProd},
		}

		It("accepts a gate that lists no environment, or only some", func() {
			Expect(vcsValidateEnvironments(nil, branches, defaults)).To(Succeed())
			some := []promoterv1alpha1.ValidatedCommitStatusEnvironment{{Branch: vcsProd}}
			Expect(vcsValidateEnvironments(some, branches, defaults)).To(Succeed())
		})

		It("rejects an entry for a branch the PromotionStrategy does not have", func() {
			extra := []promoterv1alpha1.ValidatedCommitStatusEnvironment{{Branch: "environment/extra"}}
			err := vcsValidateEnvironments(extra, branches, defaults)
			Expect(err).To(MatchError(ContainSubstring(`spec.environments lists "environment/extra", which is not an environment of the PromotionStrategy`)))
		})

		It("rejects two environments that read the same tag", func() {
			shared := map[string]vcsEnvironmentSetting{
				vcsDev:  {Tag: vcsTagDev},
				vcsTest: {Tag: vcsTagProd},
				vcsProd: {Tag: vcsTagProd},
			}
			err := vcsValidateEnvironments(nil, branches, shared)
			Expect(err).To(MatchError(ContainSubstring(`environments "environment/test" and "environment/prod" both read tag "env/prod"`)))
		})
	})

	Describe("validatedCommitStatusFor: which ValidatedCommitStatus moves the tags of a PromotionStrategy", func() {
		const namespace = "test-ns"
		created := time.Date(2026, 11, 13, 15, 0, 0, 0, time.UTC)

		gate := func(name, strategy string, age time.Duration) *promoterv1alpha1.ValidatedCommitStatus {
			return &promoterv1alpha1.ValidatedCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace, CreationTimestamp: metav1.NewTime(created.Add(-age))},
				Spec: promoterv1alpha1.ValidatedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: strategy},
					Key:                  promoterv1alpha1.ValidatedCommitStatusKey,
					DrySource:            promoterv1alpha1.ValidatedCommitStatusDrySource{Branch: "main"},
				},
			}
		}
		strategy := func(ref promoterv1alpha1.OrderCommitStatusRef) *promoterv1alpha1.PromotionStrategy {
			ps := &promoterv1alpha1.PromotionStrategy{}
			ps.Name, ps.Namespace = "demo", namespace
			ps.Spec.OrderCommitStatusRef = ref
			return ps
		}
		owner := func(ps *promoterv1alpha1.PromotionStrategy, gates ...client.Object) string {
			GinkgoHelper()
			builder := fake.NewClientBuilder().WithScheme(utils.GetScheme())
			for _, kind := range GateCommitStatusKinds() {
				builder = builder.WithIndex(kind, PromotionStrategyRefField, PromotionStrategyRefIndexValues)
			}
			found, err := validatedCommitStatusFor(ctx, builder.WithObjects(gates...).Build(), ps)
			Expect(err).NotTo(HaveOccurred())
			if found == nil {
				return ""
			}
			return found.Name
		}
		validatedRef := func(name string) promoterv1alpha1.OrderCommitStatusRef {
			return promoterv1alpha1.OrderCommitStatusRef{Kind: promoterv1alpha1.ValidatedOrderCommitStatusKind, Name: name}
		}

		It("is none when no gate references the strategy", func() {
			Expect(owner(strategy(validatedRef("demo")))).To(BeEmpty())
			Expect(owner(strategy(validatedRef("demo")), gate("elsewhere", "another-strategy", time.Hour))).To(BeEmpty())
		})

		It("is the one the strategy references, however young", func() {
			Expect(owner(strategy(validatedRef("young")),
				gate("old", "demo", 48*time.Hour),
				gate("young", "demo", time.Minute),
			)).To(Equal("young"))
		})

		It("is the oldest while the strategy references another kind of gate, or a gate that is not there", func() {
			gates := []client.Object{
				gate("young", "demo", time.Minute),
				gate("old", "demo", 48*time.Hour),
				gate("older-but-elsewhere", "another-strategy", 96*time.Hour),
			}
			Expect(owner(strategy(promoterv1alpha1.OrderCommitStatusRef{Name: "young"}), gates...)).To(Equal("old"),
				"the default kind is DependentsSuccessfulCommitStatus, so the name is not a ValidatedCommitStatus's")
			Expect(owner(strategy(validatedRef("missing")), gates...)).To(Equal("old"))
		})

		It("goes by name between gates of the same age", func() {
			Expect(owner(strategy(promoterv1alpha1.OrderCommitStatusRef{Name: "demo"}),
				gate("b", "demo", time.Hour),
				gate("a", "demo", time.Hour),
			)).To(Equal("a"))
		})
	})

	Describe("the validation notes", func() {
		const tag = vcsTagProd
		at := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)
		// line returns the nth line of the notes: a line for the tag on sha, with the given source.
		line := func(n int, sha, source string) git.ValidationEvent {
			return git.ValidationEvent{
				Sha:    sha,
				Author: "jane@example.com",
				Time:   at.Add(time.Duration(n) * time.Minute),
				Line:   validations.Line{Target: tag, Source: source},
			}
		}

		DescribeTable("vcsManualValidation: which lines are a person's validation",
			func(source string, expected bool) {
				Expect(vcsManualValidation(validations.Line{Target: tag, Source: source})).To(Equal(expected))
			},
			Entry("a line pushed as a note", validations.SourceGit, true),
			Entry("a line a CommitValidation wrote", validations.SourceResource, true),
			Entry("the record of a tag moved by hand", validations.SourceExternal, false),
			Entry("an automatic validation, which this controller never writes", validations.SourceAutomatic, false),
		)

		Describe("vcsResumeAfter: where a gate that lost its place takes the notes up again", func() {
			shas := func(lines []git.ValidationEvent) []string {
				out := make([]string, 0, len(lines))
				for _, l := range lines {
					out = append(out, l.Sha)
				}
				return out
			}

			It("resumes after the newest line of the commit the tag is on", func() {
				lines := []git.ValidationEvent{
					line(1, "b", validations.SourceGit),
					line(2, "c", validations.SourceGit),
					line(3, "b", validations.SourceResource),
					line(4, "d", validations.SourceGit),
					line(5, "e", validations.SourceGit),
				}
				matched, later := vcsResumeAfter(lines, tag, "b")
				Expect(matched).NotTo(BeNil())
				Expect(matched.Time).To(Equal(at.Add(3*time.Minute)), "the newer of the two lines of b")
				Expect(shas(later)).To(Equal([]string{"d", "e"}), "c was written before the tag was put on b, and is not applied again")
			})

			It("takes nothing when the notes have no line of the tag's commit: the controller put the tag there", func() {
				lines := []git.ValidationEvent{line(1, "b", validations.SourceGit), line(2, "c", validations.SourceGit)}
				matched, later := vcsResumeAfter(lines, tag, "e")
				Expect(matched).To(BeNil())
				Expect(later).To(BeEmpty())
			})

			It("resumes after the record of a move by hand, and never applies such a record", func() {
				lines := []git.ValidationEvent{
					line(1, "b", validations.SourceGit),
					line(2, "c", validations.SourceExternal),
					line(3, "d", validations.SourceExternal),
					line(4, "e", validations.SourceGit),
				}
				matched, later := vcsResumeAfter(lines, tag, "c")
				Expect(matched).NotTo(BeNil())
				Expect(matched.Line.Source).To(Equal(validations.SourceExternal))
				Expect(shas(later)).To(Equal([]string{"e"}), "the record of the move to d moves nothing")
			})

			It("ignores other tags and automatic validations", func() {
				other := line(2, "b", validations.SourceGit)
				other.Line.Target = vcsTagTest
				lines := []git.ValidationEvent{
					line(1, "b", validations.SourceAutomatic),
					other,
					line(3, "c", validations.SourceGit),
				}
				matched, later := vcsResumeAfter(lines, tag, "b")
				Expect(matched).To(BeNil())
				Expect(later).To(BeEmpty())

				matched, later = vcsResumeAfter(append(lines, line(4, "b", validations.SourceGit), line(5, "d", validations.SourceAutomatic)), tag, "b")
				Expect(matched).NotTo(BeNil())
				Expect(later).To(BeEmpty())
			})

			It("takes nothing for a tag that does not exist", func() {
				matched, later := vcsResumeAfter([]git.ValidationEvent{line(1, "b", validations.SourceGit)}, tag, "")
				Expect(matched).To(BeNil())
				Expect(later).To(BeEmpty())
			})
		})

		Describe("vcsNoted: what a line says put the tag on its commit", func() {
			It("is the validation, with who validated and when", func() {
				validation := line(1, "b", validations.SourceGit)
				Expect(vcsNoted(&validation)).To(Equal(&vcsNotedTag{
					At: metav1.NewTime(at.Add(time.Minute)), By: "jane@example.com", Source: promoterv1alpha1.ValidatedTagSourceManual,
				}))
			})

			It("is a push by hand, at the time the line gives for it", func() {
				record := line(1, "b", validations.SourceExternal)
				record.Line.At = at.Add(-time.Hour)
				Expect(vcsNoted(&record)).To(Equal(&vcsNotedTag{
					At: metav1.NewTime(at.Add(-time.Hour)), Source: promoterv1alpha1.ValidatedTagSourceExternal,
				}))
			})
		})

		DescribeTable("vcsResumesFromHead: which tags take no validation after the gate lost its place",
			func(recorded *promoterv1alpha1.ValidatedCommitStatusTagStatus, expected bool) {
				env := vcsSettled(vcsProd, tag, "c", "c")
				env.Recorded = recorded
				Expect(vcsResumesFromHead(env)).To(Equal(expected))
			},
			Entry("a tag status has no record of", nil, false),
			Entry("a tag pushed by hand, still there",
				&promoterv1alpha1.ValidatedCommitStatusTagStatus{Sha: "c", Source: promoterv1alpha1.ValidatedTagSourceExternal}, true),
			Entry("a tag the controller moved, still there",
				&promoterv1alpha1.ValidatedCommitStatusTagStatus{Sha: "c", Source: promoterv1alpha1.ValidatedTagSourceAutomatic}, true),
			Entry("a tag a validation placed, still there: it resumes after that validation",
				&promoterv1alpha1.ValidatedCommitStatusTagStatus{Sha: "c", Source: promoterv1alpha1.ValidatedTagSourceManual}, false),
			Entry("a tag that is no longer where status has it",
				&promoterv1alpha1.ValidatedCommitStatusTagStatus{Sha: "b", Source: promoterv1alpha1.ValidatedTagSourceExternal}, false),
			Entry("a tag of unknown origin",
				&promoterv1alpha1.ValidatedCommitStatusTagStatus{Sha: "c"}, false),
		)

		Describe("vcsHandMoveRecord: the record of a tag moved by hand", func() {
			movedAt := metav1.NewTime(at)
			moved := func(source promoterv1alpha1.ValidatedTagSource) *promoterv1alpha1.ValidatedCommitStatusTagStatus {
				return &promoterv1alpha1.ValidatedCommitStatusTagStatus{Name: tag, Sha: "c", Source: source, MovedAt: &movedAt}
			}

			It("is a line that validates nothing, on the commit the tag was moved to", func() {
				entry, ok := vcsHandMoveRecord("demo", moved(promoterv1alpha1.ValidatedTagSourceExternal))
				Expect(ok).To(BeTrue())
				Expect(entry.Sha).To(Equal("c"))
				Expect(entry.Line.Validates()).To(BeFalse())
				Expect(entry.Line).To(Equal(validations.Line{
					Target: tag,
					Source: validations.SourceExternal,
					By:     "ValidatedCommitStatus/demo",
					Reason: "Tag moved by hand",
					At:     at,
					ID:     tag + "@2026-11-13T09:00:00Z",
				}))
			})

			It("is the same record for the same move, and another for a later move to the same commit", func() {
				first, _ := vcsHandMoveRecord("demo", moved(promoterv1alpha1.ValidatedTagSourceExternal))
				again, _ := vcsHandMoveRecord("demo", moved(promoterv1alpha1.ValidatedTagSourceExternal))
				Expect(again.Line.ID).To(Equal(first.Line.ID))

				later := moved(promoterv1alpha1.ValidatedTagSourceExternal)
				laterAt := metav1.NewTime(at.Add(time.Hour))
				later.MovedAt = &laterAt
				second, _ := vcsHandMoveRecord("demo", later)
				Expect(second.Line.ID).NotTo(Equal(first.Line.ID))
			})

			It("is written for no other tag", func() {
				for _, source := range []promoterv1alpha1.ValidatedTagSource{
					promoterv1alpha1.ValidatedTagSourceManual, promoterv1alpha1.ValidatedTagSourceAutomatic, promoterv1alpha1.ValidatedTagSourceSeeded, "",
				} {
					_, ok := vcsHandMoveRecord("demo", moved(source))
					Expect(ok).To(BeFalse(), "source %q", source)
				}
				_, ok := vcsHandMoveRecord("demo", nil)
				Expect(ok).To(BeFalse())
				_, ok = vcsHandMoveRecord("demo", &promoterv1alpha1.ValidatedCommitStatusTagStatus{Name: tag, Source: promoterv1alpha1.ValidatedTagSourceExternal})
				Expect(ok).To(BeFalse(), "a tag that does not exist")
			})
		})

		DescribeTable("vcsFailedPushOutcome: what became of a push that reported an error",
			func(after git.TagState, readErr error, expected vcsPushOutcome) {
				before := git.TagState{Raw: "c", Commit: "c"}
				Expect(vcsFailedPushOutcome(before, after, readErr, "e")).To(Equal(expected))
			},
			Entry("the tag is where it was: the push failed", git.TagState{Raw: "c", Commit: "c"}, nil, vcsPushFailed),
			Entry("the tag is where the push put it: only the answer was lost", git.TagState{Raw: "e", Commit: "e"}, nil, vcsPushLanded),
			Entry("the tag is somewhere else: someone moved it first", git.TagState{Raw: "d", Commit: "d"}, nil, vcsPushOvertaken),
			Entry("the tag was deleted meanwhile", git.TagState{}, nil, vcsPushOvertaken),
			Entry("the tag was replaced by an annotated tag of the same commit", git.TagState{Raw: "tagobject", Commit: "c"}, nil, vcsPushOvertaken),
			Entry("the tag could not be read again", git.TagState{}, errors.New("remote unavailable"), vcsPushFailed),
		)
	})

	Describe("vcsNoteKey: the qualification note entry an environment owns", func() {
		It("is the branch for an environment that owns its active branch", func() {
			Expect(vcsNoteKey(vcsProd, "")).To(Equal("environment/prod"))
		})

		It("includes the activePath so environments sharing an active branch do not overwrite each other", func() {
			Expect(vcsNoteKey("environments", "apps/one")).To(Equal("environments:apps/one"))
			Expect(vcsNoteKey("environments", "apps/one")).NotTo(Equal(vcsNoteKey("environments", "apps/two")))
		})
	})

	Describe("vcsRestored: whether the live commit was put there by a RestoreActiveCommit", func() {
		restore := func(branch, activeSha string) promoterv1alpha1.RestoreActiveCommit {
			return promoterv1alpha1.RestoreActiveCommit{
				Spec:   promoterv1alpha1.RestoreActiveCommitSpec{Branch: branch},
				Status: promoterv1alpha1.RestoreActiveCommitStatus{ActiveSha: activeSha},
			}
		}

		It("is true when a restore for the branch produced the active commit", func() {
			racs := []promoterv1alpha1.RestoreActiveCommit{restore(vcsDev, "aaa"), restore(vcsTest, "bbb")}
			Expect(vcsRestored(racs, vcsTest, "bbb")).To(BeTrue())
		})

		It("is false when the environment has been promoted past the restore commit", func() {
			Expect(vcsRestored([]promoterv1alpha1.RestoreActiveCommit{restore(vcsTest, "bbb")}, vcsTest, "ccc")).To(BeFalse())
		})

		It("is false for another environment's restore", func() {
			Expect(vcsRestored([]promoterv1alpha1.RestoreActiveCommit{restore(vcsDev, "bbb")}, vcsTest, "bbb")).To(BeFalse())
		})

		It("is false for a restore that has not run yet", func() {
			Expect(vcsRestored([]promoterv1alpha1.RestoreActiveCommit{restore(vcsTest, "")}, vcsTest, "")).To(BeFalse())
		})
	})
})

// vcsRemoteTagSha returns the commit a tag points at on the test git server, or "" when the tag does not exist.
func vcsRemoteTagSha(ctx context.Context, gitPath, tag string) (string, error) {
	out, err := runGitCmd(ctx, gitPath, "ls-remote", "origin", "refs/tags/"+tag)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return "", nil
	}
	return fields[0], nil
}

// vcsPushTag points a tag at sha on the test git server, as a person with push access would.
func vcsPushTag(ctx context.Context, gitPath, tag, sha string) {
	GinkgoHelper()
	_, err := runGitCmd(ctx, gitPath, "fetch", "origin")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, gitPath, "push", "--force", "origin", sha+":refs/tags/"+tag)
	Expect(err).NotTo(HaveOccurred())
}

// vcsDefaultBranch returns the test repository's DRY branch name.
func vcsDefaultBranch(ctx context.Context, gitPath string) string {
	GinkgoHelper()
	out, err := runGitCmd(ctx, gitPath, "rev-parse", "--abbrev-ref", "origin/HEAD")
	Expect(err).NotTo(HaveOccurred())
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "origin/"))
}

var _ = Describe("ValidatedCommitStatus Controller", func() {
	const (
		tagDevelopment = "env/development"
		tagStaging     = "env/staging"
		tagProduction  = "env/production"
	)

	// newGate builds a gate for the three test environments of a PromotionStrategy.
	newGate := func(name, dryBranch string) *promoterv1alpha1.ValidatedCommitStatus {
		return &promoterv1alpha1.ValidatedCommitStatus{
			Name:      name,
			Namespace: "default",
			Spec: promoterv1alpha1.ValidatedCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
				Key:                  promoterv1alpha1.ValidatedCommitStatusKey,
				DrySource:            promoterv1alpha1.ValidatedCommitStatusDrySource{Branch: dryBranch},
				Environments: []promoterv1alpha1.ValidatedCommitStatusEnvironment{
					{Branch: testBranchDevelopment, Tag: tagDevelopment},
					{Branch: testBranchStaging, Tag: tagStaging},
					{Branch: testBranchProduction, Tag: tagProduction},
				},
			},
		}
	}

	readyCondition := func(ctx context.Context, g Gomega, gate *promoterv1alpha1.ValidatedCommitStatus) *metav1.Condition {
		updated := &promoterv1alpha1.ValidatedCommitStatus{}
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gate), updated)).To(Succeed())
		condition := meta.FindStatusCondition(updated.Status.Conditions, string(promoterConditions.Ready))
		g.Expect(condition).NotTo(BeNil())
		g.Expect(condition.ObservedGeneration).To(Equal(updated.Generation))
		return condition
	}

	Context("When unmarshalling the test data", func() {
		It("should unmarshal the ValidatedCommitStatus resource", func() {
			err := unmarshalYamlStrict(testValidatedCommitStatusYAML, &promoterv1alpha1.ValidatedCommitStatus{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("When validating the spec", func() {
		var ctx context.Context

		BeforeEach(func() {
			ctx = context.Background()
		})

		It("should reject an unknown validation policy", func() {
			gate := newGate("vcs-bad-validation", "main")
			gate.Spec.Environments[2].Validation = "Sometimes"
			err := k8sClient.Create(ctx, gate)
			Expect(err).To(MatchError(ContainSubstring("Unsupported value")))
		})

		It("should reject a tag that is not a valid ref name", func() {
			gate := newGate("vcs-bad-tag", "main")
			gate.Spec.Environments[0].Tag = "-env..dev"
			err := k8sClient.Create(ctx, gate)
			Expect(err).To(HaveOccurred())
		})

		It("should default validation to Automatic, and accept a gate that lists no environment", func() {
			gate := newGate("vcs-default-validation", "main")
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gate) })
			Expect(gate.Spec.Environments[0].Validation).To(Equal(promoterv1alpha1.ValidationAutomatic))

			bare := newGate("vcs-no-environments", "main")
			bare.Spec.Environments = nil
			Expect(k8sClient.Create(ctx, bare)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, bare) })
		})
	})

	Context("When the PromotionStrategy is missing", func() {
		It("should set Ready=False when the PromotionStrategy is not found", func() {
			ctx := context.Background()
			gate := newGate("vcs-missing-ps", "main")
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gate) })

			Eventually(func(g Gomega) {
				g.Expect(readyCondition(ctx, g, gate).Status).To(Equal(metav1.ConditionFalse))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})

	Context("When reconciling against a PromotionStrategy", func() {
		var (
			ctx               context.Context
			name              string
			scmSecret         *v1.Secret
			scmProvider       *promoterv1alpha1.ScmProvider
			gitRepo           *promoterv1alpha1.GitRepository
			promotionStrategy *promoterv1alpha1.PromotionStrategy
			gate              *promoterv1alpha1.ValidatedCommitStatus
			gitPath           string
			dryBranch         string
		)

		// environmentStatus returns the gate's status entry for a branch, failing the enclosing Eventually
		// while the entry does not exist.
		environmentStatus := func(g Gomega, branch string) promoterv1alpha1.ValidatedCommitStatusEnvironmentStatus {
			updated := &promoterv1alpha1.ValidatedCommitStatus{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gate), updated)).To(Succeed())
			idx := slices.IndexFunc(updated.Status.Environments, func(e promoterv1alpha1.ValidatedCommitStatusEnvironmentStatus) bool {
				return e.Branch == branch
			})
			g.Expect(idx).To(BeNumerically(">=", 0), "no status entry for %s", branch)
			return updated.Status.Environments[idx]
		}
		// activeDrySha returns the DRY SHA the PromotionStrategy reports as live in an environment.
		activeDrySha := func(g Gomega, branch string) string {
			ps := &promoterv1alpha1.PromotionStrategy{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(promotionStrategy), ps)).To(Succeed())
			idx := slices.IndexFunc(ps.Status.Environments, func(e promoterv1alpha1.EnvironmentStatus) bool { return e.Branch == branch })
			g.Expect(idx).To(BeNumerically(">=", 0), "no PromotionStrategy status for %s", branch)
			return ps.Status.Environments[idx].Active.Dry.Sha
		}
		activeHydratedSha := func(g Gomega, branch string) string {
			ps := &promoterv1alpha1.PromotionStrategy{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(promotionStrategy), ps)).To(Succeed())
			idx := slices.IndexFunc(ps.Status.Environments, func(e promoterv1alpha1.EnvironmentStatus) bool { return e.Branch == branch })
			g.Expect(idx).To(BeNumerically(">=", 0), "no PromotionStrategy status for %s", branch)
			return ps.Status.Environments[idx].Active.Hydrated.Sha
		}
		tagSha := func(g Gomega, tag string) string {
			sha, err := vcsRemoteTagSha(ctx, gitPath, tag)
			g.Expect(err).NotTo(HaveOccurred())
			return sha
		}
		// hydrate plays the hydrator: it hydrates an environment's proposed branch from the commit its tag
		// points at.
		hydrate := func(proposedBranch, tag string) {
			GinkgoHelper()
			sha, err := vcsRemoteTagSha(ctx, gitPath, tag)
			Expect(err).NotTo(HaveOccurred())
			Expect(sha).NotTo(BeEmpty(), "tag %s does not exist", tag)
			Expect(hydrateEnvironment(ctx, gitPath, proposedBranch, sha, "hydrate "+tag+" at "+sha)).To(Succeed())
		}
		headSha := func() string {
			GinkgoHelper()
			_, err := runGitCmd(ctx, gitPath, "fetch", "origin")
			Expect(err).NotTo(HaveOccurred())
			out, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+dryBranch)
			Expect(err).NotTo(HaveOccurred())
			return strings.TrimSpace(out)
		}

		// neverHydrated removes hydrator.metadata from every environment branch, so that nothing is live
		// anywhere, as in environments that were never hydrated. The shared test repository otherwise starts
		// with its first DRY commit live in every environment.
		neverHydrated := func() {
			GinkgoHelper()
			_, err := runGitCmd(ctx, gitPath, "fetch", "origin")
			Expect(err).NotTo(HaveOccurred())
			for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
				_, err = runGitCmd(ctx, gitPath, "checkout", "-B", branch, "origin/"+branch)
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "rm", "hydrator.metadata")
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "commit", "-m", "never hydrated: "+branch)
				Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "push", "origin", "HEAD:refs/heads/"+branch, "HEAD:refs/heads/"+branch+"-next")
				Expect(err).NotTo(HaveOccurred())
			}
		}

		// validate validates a DRY commit for the environment that reads tag, the way a person does: with a
		// line in the validation note of the commit. No webhook announces a note, so the gate is enqueued,
		// standing in for its requeue.
		validate := func(tag, drySha, reason string) {
			GinkgoHelper()
			line := `{"target":"` + tag + `","reason":"` + reason + `"}`
			// The controller writes to the same ref, so a push can lose the race and has to start over.
			Eventually(func() error {
				refspec := "+" + git.PromoterValidationsNotesRef + ":" + git.PromoterValidationsNotesRef
				if _, err := runGitCmd(ctx, gitPath, "fetch", "origin", refspec); err != nil && !strings.Contains(err.Error(), "couldn't find remote ref") {
					return err
				}
				if _, err := runGitCmd(ctx, gitPath, "notes", "--ref="+git.PromoterValidationsNotesRef, "append", "-m", line, drySha); err != nil {
					return err
				}
				_, err := runGitCmd(ctx, gitPath, "push", "origin", git.PromoterValidationsNotesRef)
				return err
			}, constants.EventuallyTimeout, time.Second).Should(Succeed())
			enqueueValidatedCommitStatus(gate.Namespace, gate.Name)
		}
		// validationLines returns the lines of the validation note of a DRY commit, as the repository has them.
		validationLines := func(drySha string) []validations.Line {
			refspec := "+" + git.PromoterValidationsNotesRef + ":" + git.PromoterValidationsNotesRef
			if _, err := runGitCmd(ctx, gitPath, "fetch", "origin", refspec); err != nil {
				return nil
			}
			note, err := runGitCmd(ctx, gitPath, "notes", "--ref="+git.PromoterValidationsNotesRef, "show", drySha)
			if err != nil {
				return nil
			}
			return validations.ParseNote(note)
		}
		// commitHotfix pushes a commit to a branch of that name that starts at the given DRY commit, and
		// returns the commit. The DRY branch is checked out again afterwards.
		commitHotfix := func(branch, from string) string {
			GinkgoHelper()
			for _, args := range [][]string{{"fetch", "origin"}, {"checkout", "-B", branch, from}} {
				_, err := runGitCmd(ctx, gitPath, args...)
				Expect(err).NotTo(HaveOccurred())
			}
			Expect(os.WriteFile(gitPath+"/hotfix.yaml", []byte("fixed: "+time.Now().Format(time.RFC3339Nano)), 0o644)).To(Succeed())
			for _, args := range [][]string{{"add", "hotfix.yaml"}, {"commit", "-m", "hotfix"}, {"push", "origin", branch}} {
				_, err := runGitCmd(ctx, gitPath, args...)
				Expect(err).NotTo(HaveOccurred())
			}
			out, err := runGitCmd(ctx, gitPath, "rev-parse", "HEAD")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(ctx, gitPath, "checkout", dryBranch)
			Expect(err).NotTo(HaveOccurred())
			return strings.TrimSpace(out)
		}
		// mergeIntoDry merges a branch into the DRY branch with a merge commit, pushes it, and returns the merge.
		mergeIntoDry := func(branch string) string {
			GinkgoHelper()
			before := headSha()
			for _, args := range [][]string{
				{"checkout", dryBranch},
				{"reset", "--hard", "origin/" + dryBranch},
				{"merge", "--no-ff", "-m", "merge " + branch, branch},
				{"push", "origin", dryBranch},
			} {
				_, err := runGitCmd(ctx, gitPath, args...)
				Expect(err).NotTo(HaveOccurred())
			}
			sendWebhookForPush(ctx, before, dryBranch)
			return headSha()
		}
		// liveSha returns the DRY commit the test repository starts with in every environment: the parent
		// of the DRY branch HEAD, which is hydrated nowhere.
		liveSha := func() string {
			GinkgoHelper()
			out, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+dryBranch+"~1")
			Expect(err).NotTo(HaveOccurred())
			return strings.TrimSpace(out)
		}

		BeforeEach(func() {
			ctx = context.Background()
			gate = nil

			By("Setting up a test git repository and a PromotionStrategy ordered by a ValidatedCommitStatus")
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "validated-commit-status-controller-test", "default")
			promotionStrategy.Spec.ProposedCommitStatuses = nil
			promotionStrategy.Spec.OrderCommitStatusRef = promoterv1alpha1.OrderCommitStatusRef{
				Group: promoterv1alpha1.SchemeGroupVersion.Group,
				Kind:  promoterv1alpha1.ValidatedOrderCommitStatusKind,
				Name:  name,
			}
			setupInitialTestGitRepoOnServer(ctx, gitRepo)

			var err error
			gitPath, err = cloneTestRepo(ctx, gitRepo)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = os.RemoveAll(gitPath) })
			dryBranch = vcsDefaultBranch(ctx, gitPath)

			Expect(k8sClient.Create(ctx, scmSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())
			Expect(k8sClient.Create(ctx, gitRepo)).To(Succeed())
		})

		AfterEach(func() {
			By("Cleaning up test resources")
			if gate != nil {
				_ = k8sClient.Delete(ctx, gate)
			}
			_ = k8sClient.Delete(ctx, promotionStrategy)
			_ = k8sClient.Delete(ctx, gitRepo)
			_ = k8sClient.Delete(ctx, scmProvider)
			_ = k8sClient.Delete(ctx, scmSecret)
		})

		It("should set Ready=False when two environments read the same tag", func() {
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			gate.Spec.Environments[2].Tag = tagStaging
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())

			Eventually(func(g Gomega) {
				condition := readyCondition(ctx, g, gate)
				g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(condition.Message).To(ContainSubstring(`environments "environment/staging" and "environment/production" both read tag "env/staging"`))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Not creating any tag for a gate that is misconfigured")
			Consistently(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(BeEmpty())
			}, 2*time.Second, 500*time.Millisecond).Should(Succeed())
		})

		It("should name the tags after the environment branches when spec.environments does not say", func() {
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			gate.Spec.Environments = nil
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())

			Eventually(func(g Gomega) {
				for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
					g.Expect(tagSha(g, branch+"-tag")).NotTo(BeEmpty(), "tag of %s", branch)
					g.Expect(environmentStatus(g, branch).Tag.Name).To(Equal(branch + "-tag"))
				}
				g.Expect(readyCondition(ctx, g, gate).Status).To(Equal(metav1.ConditionTrue))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should leave the tags to the gate the PromotionStrategy uses when a second gate references it", func() {
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).NotTo(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			second := newGate(name+"-second", dryBranch)
			second.Spec.PromotionStrategyRef.Name = name
			second.Spec.Key = "validated-second"
			Expect(k8sClient.Create(ctx, second)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, second) })

			Eventually(func(g Gomega) {
				condition := readyCondition(ctx, g, second)
				g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(condition.Reason).To(Equal(string(promoterConditions.EnvironmentTagsOwnedByAnotherGate)))
				g.Expect(condition.Message).To(ContainSubstring(name))

				updated := &promoterv1alpha1.ValidatedCommitStatus{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(second), updated)).To(Succeed())
				g.Expect(updated.Status.Environments).To(BeEmpty())
				g.Expect(updated.Status.Dry).To(BeNil())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(readyCondition(ctx, g, gate).Status).To(Equal(metav1.ConditionTrue))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should set Ready=False with reason DryRepositoryUnavailable when the DRY branch cannot be read", func() {
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, "no-such-branch")
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())

			Eventually(func(g Gomega) {
				condition := readyCondition(ctx, g, gate)
				g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(condition.Reason).To(Equal(string(promoterConditions.DryRepositoryUnavailable)))
				g.Expect(condition.Message).To(ContainSubstring("no-such-branch"))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Not creating any tag while the DRY branch is unknown")
			Expect(vcsRemoteTagSha(ctx, gitPath, tagDevelopment)).To(BeEmpty())
		})

		It("should set Ready=False with reason TagPushFailed when a tag cannot be pushed, and still manage the others", func() {
			By("Occupying the ref namespace the development tag needs")
			// refs/tags/blocker exists, so refs/tags/blocker/development cannot be created.
			vcsPushTag(ctx, gitPath, "blocker", headSha())

			// The test repository starts with its first DRY commit hydrated to every environment.
			out, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+dryBranch+"~1")
			Expect(err).NotTo(HaveOccurred())
			live := strings.TrimSpace(out)

			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			gate.Spec.Environments[0].Tag = "blocker/development"
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())

			Eventually(func(g Gomega) {
				condition := readyCondition(ctx, g, gate)
				g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(condition.Reason).To(Equal(string(promoterConditions.TagPushFailed)))
				g.Expect(condition.Message).To(ContainSubstring("blocker/development"))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Creating the tags that can be pushed")
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagStaging)).To(Equal(live))
				g.Expect(tagSha(g, tagProduction)).To(Equal(live))
				g.Expect(environmentStatus(g, testBranchDevelopment).Tag.Sha).To(BeEmpty())
				g.Expect(environmentStatus(g, testBranchDevelopment).Tag.Source).To(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should advance tags and gate promotions as environments qualify commits", func() {
			const healthKey = "vcs-health"
			neverHydrated()

			By("Requiring an active commit status on development")
			promotionStrategy.Spec.Environments[0].ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{{Key: healthKey}}
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())

			By("Creating the root's tag at the DRY branch HEAD, and no other: nothing is live and nothing is qualified")
			first := headSha()
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(first))
				g.Expect(readyCondition(ctx, g, gate).Status).To(Equal(metav1.ConditionTrue))

				updated := &promoterv1alpha1.ValidatedCommitStatus{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gate), updated)).To(Succeed())
				g.Expect(updated.Status.Dry).NotTo(BeNil())
				g.Expect(updated.Status.Dry.Sha).To(Equal(first))
				development := environmentStatus(g, testBranchDevelopment)
				g.Expect(development.Tag).NotTo(BeNil())
				g.Expect(development.Tag.Name).To(Equal(tagDevelopment))
				g.Expect(development.Tag.Sha).To(Equal(first))
				g.Expect(development.Tag.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceSeeded))
				g.Expect(development.Tag.MovedAt).NotTo(BeNil())
				g.Expect(development.UpstreamCleared).NotTo(BeNil())
				g.Expect(development.UpstreamCleared.Sha).To(Equal(first))
				g.Expect(development.Qualified).To(BeNil())

				staging := environmentStatus(g, testBranchStaging)
				g.Expect(staging.Tag.Name).To(Equal(tagStaging))
				g.Expect(staging.Tag.Sha).To(BeEmpty())
				g.Expect(staging.UpstreamCleared).To(BeNil(), "development has qualified nothing")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Promoting development, a root, as soon as it is hydrated from its tag")
			hydrate(testBranchDevelopmentNext, tagDevelopment)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchDevelopment)).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Not creating staging's tag until development qualifies a commit")
			Consistently(func(g Gomega) {
				g.Expect(tagSha(g, tagStaging)).To(BeEmpty())
				g.Expect(tagSha(g, tagProduction)).To(BeEmpty())
			}, 3*time.Second, 500*time.Millisecond).Should(Succeed())

			By("Reporting development healthy")
			var developmentActive string
			Eventually(func(g Gomega) {
				developmentActive = activeHydratedSha(g, testBranchDevelopment)
				g.Expect(developmentActive).NotTo(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())
			health := &promoterv1alpha1.CommitStatus{
				Name:      name + "-health",
				Namespace: "default",
				Labels:    map[string]string{promoterv1alpha1.CommitStatusLabel: healthKey},
				Spec: promoterv1alpha1.CommitStatusSpec{
					RepositoryReference: promoterv1alpha1.ObjectReference{Name: gitRepo.Name},
					Sha:                 developmentActive,
					Name:                healthKey,
					Description:         "development is healthy",
					Phase:               promoterv1alpha1.CommitPhaseSuccess,
				},
			}
			Expect(k8sClient.Create(ctx, health)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, health) })

			By("Recording the qualification and creating staging's tag at the commit development qualified")
			Eventually(func(g Gomega) {
				development := environmentStatus(g, testBranchDevelopment)
				g.Expect(development.Qualified).NotTo(BeNil())
				g.Expect(development.Qualified.DrySha).To(Equal(first))
				g.Expect(development.Qualified.HydratedSha).To(Equal(developmentActive))

				g.Expect(tagSha(g, tagStaging)).To(Equal(first))
				staging := environmentStatus(g, testBranchStaging)
				g.Expect(staging.Tag.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceSeeded))
				g.Expect(staging.UpstreamCleared).NotTo(BeNil())
				g.Expect(staging.UpstreamCleared.Sha).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Promoting staging, then production, each once the environment before it has qualified the commit")
			hydrate(testBranchStagingNext, tagStaging)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchStaging)).To(Equal(first))
				qualified := environmentStatus(g, testBranchStaging).Qualified
				g.Expect(qualified).NotTo(BeNil())
				g.Expect(qualified.DrySha).To(Equal(first))
				g.Expect(tagSha(g, tagProduction)).To(Equal(first))

				cs := &promoterv1alpha1.CommitStatus{}
				csName := utils.CommitStatusResourceName(ctx, gate, testBranchStaging)
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: csName}, cs)).To(Succeed())
				g.Expect(cs.Spec.Name).To(Equal(promoterv1alpha1.ValidatedCommitStatusKey))
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())
			hydrate(testBranchProductionNext, tagProduction)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchProduction)).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Pushing a new DRY commit")
			second, err := makeDryCommit(ctx, gitPath, "second release")
			Expect(err).NotTo(HaveOccurred())

			By("Moving only the root environment's tag")
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(second))
				g.Expect(environmentStatus(g, testBranchDevelopment).Tag.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceAutomatic))
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(vcsRemoteTagSha(ctx, gitPath, tagStaging)).To(Equal(first))
			Expect(vcsRemoteTagSha(ctx, gitPath, tagProduction)).To(Equal(first))

			By("Promoting the new commit to development, where it is not yet healthy")
			hydrate(testBranchDevelopmentNext, tagDevelopment)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchDevelopment)).To(Equal(second))
			}, constants.EventuallyTimeout).Should(Succeed())
			Consistently(func(g Gomega) {
				g.Expect(tagSha(g, tagStaging)).To(Equal(first), "staging's tag waits for development to qualify the new commit")
				g.Expect(environmentStatus(g, testBranchDevelopment).Qualified.DrySha).To(Equal(first))
			}, 3*time.Second, 500*time.Millisecond).Should(Succeed())

			By("Reporting development healthy on the new commit")
			Eventually(func(g Gomega) {
				developmentActive = activeHydratedSha(g, testBranchDevelopment)
				current := &promoterv1alpha1.CommitStatus{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(health), current)).To(Succeed())
				current.Spec.Sha = developmentActive
				g.Expect(k8sClient.Update(ctx, current)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Advancing staging's tag, but not production's")
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagStaging)).To(Equal(second))
				g.Expect(environmentStatus(g, testBranchStaging).Tag.Sha).To(Equal(second))
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(vcsRemoteTagSha(ctx, gitPath, tagProduction)).To(Equal(first))

			By("Promoting staging, which advances production's tag")
			hydrate(testBranchStagingNext, tagStaging)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchStaging)).To(Equal(second))
				g.Expect(tagSha(g, tagProduction)).To(Equal(second))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Promoting production once it is hydrated from its tag")
			hydrate(testBranchProductionNext, tagProduction)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchProduction)).To(Equal(second))
				production := environmentStatus(g, testBranchProduction)
				g.Expect(production.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should take a qualification back after a restore whose RestoreActiveCommit no longer exists", func() {
			neverHydrated()
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())

			By("Promoting the first commit to development")
			first := headSha()
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())
			hydrate(testBranchDevelopmentNext, tagDevelopment)
			var firstHydrated string
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchDevelopment)).To(Equal(first))
				firstHydrated = activeHydratedSha(g, testBranchDevelopment)
				qualified := environmentStatus(g, testBranchDevelopment).Qualified
				g.Expect(qualified).NotTo(BeNil())
				g.Expect(qualified.DrySha).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Promoting a second commit to development, which qualifies it for staging")
			second, err := makeDryCommit(ctx, gitPath, "second release")
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(second))
			}, constants.EventuallyTimeout).Should(Succeed())
			hydrate(testBranchDevelopmentNext, tagDevelopment)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchDevelopment)).To(Equal(second))
				g.Expect(environmentStatus(g, testBranchDevelopment).Qualified.DrySha).To(Equal(second))
				g.Expect(tagSha(g, tagStaging)).To(Equal(second))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Restoring development to the first commit, as a RestoreActiveCommit that is already deleted left it")
			// What a restore that no longer blocks leaves in git: a commit on the active branch with the
			// restored commit's tree and a Promoter-restored-from trailer, and a history note saying the
			// environment was unblocked. No RestoreActiveCommit exists for the gate to see, and the
			// ChangeTransferPolicy does not create one for an unblocked restore.
			_, err = runGitCmd(ctx, gitPath, "fetch", "origin")
			Expect(err).NotTo(HaveOccurred())
			out, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+testBranchDevelopment)
			Expect(err).NotTo(HaveOccurred())
			secondHydrated := strings.TrimSpace(out)
			out, err = runGitCmd(ctx, gitPath, "commit-tree", firstHydrated+"^{tree}", "-p", secondHydrated,
				"-m", "Revert "+testBranchDevelopment+" to "+firstHydrated[:7], "-m", constants.TrailerRestoredFrom+": "+firstHydrated)
			Expect(err).NotTo(HaveOccurred())
			restoreSha := strings.TrimSpace(out)
			historyNote, err := json.Marshal(map[string][]string{
				constants.TrailerRestoredFrom:       {firstHydrated},
				constants.TrailerRestoreUnblockedAt: {"2026-10-01T12:00:00Z"},
			})
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				// The ChangeTransferPolicy writes to the same notes ref, so start from what is there.
				_, err := runGitCmd(ctx, gitPath, "fetch", "origin", "+refs/notes/*:refs/notes/*")
				g.Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "notes", "--ref="+git.PromoterHistoryNotesRef, "add", "-f", "-m", string(historyNote), restoreSha)
				g.Expect(err).NotTo(HaveOccurred())
				_, err = runGitCmd(ctx, gitPath, "push", "origin", git.PromoterHistoryNotesRef)
				g.Expect(err).NotTo(HaveOccurred())
			}, constants.EventuallyTimeout).Should(Succeed())
			_, err = runGitCmd(ctx, gitPath, "push", "origin", restoreSha+":refs/heads/"+testBranchDevelopment)
			Expect(err).NotTo(HaveOccurred())
			sendWebhookForPush(ctx, secondHydrated, testBranchDevelopment)

			By("Taking back development's qualification of the second commit")
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchDevelopment)).To(Equal(first))
				g.Expect(environmentStatus(g, testBranchDevelopment).Qualified.DrySha).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())
			var racs promoterv1alpha1.RestoreActiveCommitList
			Expect(k8sClient.List(ctx, &racs, client.InNamespace(promotionStrategy.Namespace))).To(Succeed())
			for _, rac := range racs.Items {
				Expect(rac.Spec.PromotionStrategyRef.Name).NotTo(Equal(promotionStrategy.Name), "the restore was recognized from git, not from a RestoreActiveCommit")
			}

			By("Holding staging, whose tag already points at the second commit")
			hydrate(testBranchStagingNext, tagStaging)
			Eventually(func(g Gomega) {
				staging := environmentStatus(g, testBranchStaging)
				g.Expect(staging.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				g.Expect(staging.Description).To(Equal(
					`Waiting for "environment/development" to qualify dry ` + second[:7] + ` (newest qualified: ` + first[:7] + `)`))
			}, constants.EventuallyTimeout).Should(Succeed())
			Consistently(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchStaging)).To(BeEmpty())
			}, 3*time.Second, 500*time.Millisecond).Should(Succeed())
		})

		It("should recover what an environment qualified from its notes when the PromotionStrategy has reported nothing yet", func() {
			By("Finding the commit that is live in development")
			// The test repository starts with its first DRY commit hydrated to every environment.
			_, err := runGitCmd(ctx, gitPath, "fetch", "origin")
			Expect(err).NotTo(HaveOccurred())
			out, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+dryBranch+"~1")
			Expect(err).NotTo(HaveOccurred())
			live := strings.TrimSpace(out)
			out, err = runGitCmd(ctx, gitPath, "rev-parse", "origin/"+testBranchDevelopment)
			Expect(err).NotTo(HaveOccurred())
			developmentTip := strings.TrimSpace(out)

			By("Recording, as an earlier gate would have, that development qualified it")
			qualifiedAt := metav1.NewTime(time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC))
			note, err := json.Marshal(git.QualifiedNote{Environments: map[string]git.QualifiedNoteEntry{
				testBranchDevelopment: {QualifiedAt: qualifiedAt, DrySha: live},
			}})
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(ctx, gitPath, "notes", "--ref="+git.PromoterQualifiedNotesRef, "add", "-f", "-m", string(note), developmentTip)
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(ctx, gitPath, "push", "origin", git.PromoterQualifiedNotesRef)
			Expect(err).NotTo(HaveOccurred())

			By("Requiring a commit status on development that nothing reports, so that it cannot qualify the commit again")
			promotionStrategy.Spec.Environments[0].ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{{Key: "vcs-health"}}

			By("Creating the gate first, so that it reconciles the new PromotionStrategy before anything is reported")
			gate = newGate(name, dryBranch)
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
			Eventually(func(g Gomega) {
				g.Expect(readyCondition(ctx, g, gate).Status).To(Equal(metav1.ConditionFalse))
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())

			By("Crediting development with what the note says")
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchDevelopment)).To(Equal(live))
				qualified := environmentStatus(g, testBranchDevelopment).Qualified
				g.Expect(qualified).NotTo(BeNil(), "development has qualified nothing")
				g.Expect(qualified.DrySha).To(Equal(live))
				g.Expect(qualified.HydratedSha).To(Equal(developmentTip))
				g.Expect(qualified.QualifiedAt.Time).To(BeTemporally("==", qualifiedAt.Time))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should manage the tags of a PromotionStrategy with an environment whose active branch does not exist yet", func() {
			neverHydrated()

			By("Deleting production's branches, as before the hydrator has written anything for it")
			_, err := runGitCmd(ctx, gitPath, "push", "origin", "--delete", testBranchProduction, testBranchProductionNext)
			Expect(err).NotTo(HaveOccurred())

			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())

			By("Creating the root's tag, which its hydrator needs to write the branches, and reporting the others")
			head := headSha()
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(head))
				g.Expect(readyCondition(ctx, g, gate).Status).To(Equal(metav1.ConditionTrue))
				production := environmentStatus(g, testBranchProduction)
				g.Expect(production.Qualified).To(BeNil())
				g.Expect(production.Tag.Sha).To(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(vcsRemoteTagSha(ctx, gitPath, tagProduction)).To(BeEmpty())
		})

		It("should create missing tags at what is already live, so that adopting the gate promotes nothing", func() {
			By("Finding the DRY commit that is live in every environment")
			// The test repository starts with its first DRY commit hydrated to every environment and a second
			// DRY commit, the HEAD, hydrated nowhere.
			head := headSha()
			out, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+dryBranch+"~1")
			Expect(err).NotTo(HaveOccurred())
			live := strings.TrimSpace(out)

			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			gate.Spec.Environments[2].Validation = promoterv1alpha1.ValidationManual
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())

			By("Creating the root's tag at the DRY branch HEAD and the other tags at what their environment runs")
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchStaging)).To(Equal(live))
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(head))
				g.Expect(tagSha(g, tagStaging)).To(Equal(live))
				g.Expect(tagSha(g, tagProduction)).To(Equal(live))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Crediting each environment with the commit it is already running")
			Eventually(func(g Gomega) {
				for _, branch := range []string{testBranchDevelopment, testBranchStaging, testBranchProduction} {
					qualified := environmentStatus(g, branch).Qualified
					g.Expect(qualified).NotTo(BeNil(), "%s has qualified nothing", branch)
					g.Expect(qualified.DrySha).To(Equal(live))
				}
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Leaving the dependents where they are until the root runs something newer")
			Consistently(func(g Gomega) {
				g.Expect(tagSha(g, tagStaging)).To(Equal(live))
				g.Expect(tagSha(g, tagProduction)).To(Equal(live))
				g.Expect(activeDrySha(g, testBranchStaging)).To(Equal(live))
				g.Expect(activeDrySha(g, testBranchProduction)).To(Equal(live))
			}, 3*time.Second, 500*time.Millisecond).Should(Succeed())
		})

		It("should leave a validation: Manual tag to a person and verify what they tag", func() {
			neverHydrated()
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			gate.Spec.Environments[2].Validation = promoterv1alpha1.ValidationManual
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())

			By("Creating the root's tag, and staging's once development runs the commit")
			first := headSha()
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())
			hydrate(testBranchDevelopmentNext, tagDevelopment)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchDevelopment)).To(Equal(first))
				g.Expect(tagSha(g, tagStaging)).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())
			hydrate(testBranchStagingNext, tagStaging)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchStaging)).To(Equal(first))
				cleared := environmentStatus(g, testBranchProduction).UpstreamCleared
				g.Expect(cleared).NotTo(BeNil())
				g.Expect(cleared.Sha).To(Equal(first), "what a person can take for production")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Not creating production's tag: nothing is live there and nobody has chosen a commit for it")
			Consistently(func(g Gomega) {
				g.Expect(tagSha(g, tagProduction)).To(BeEmpty())
			}, 3*time.Second, 500*time.Millisecond).Should(Succeed())

			By("Tagging the first commit for production by hand")
			vcsPushTag(ctx, gitPath, tagProduction, first)
			hydrate(testBranchProductionNext, tagProduction)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchProduction)).To(Equal(first))
				production := environmentStatus(g, testBranchProduction)
				g.Expect(production.Tag.Sha).To(Equal(first))
				g.Expect(production.Tag.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceExternal))
				g.Expect(production.Tag.AcknowledgedTarget).To(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Releasing a second commit to development and staging")
			second, err := makeDryCommit(ctx, gitPath, "second release")
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(second))
			}, constants.EventuallyTimeout).Should(Succeed())
			hydrate(testBranchDevelopmentNext, tagDevelopment)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchDevelopment)).To(Equal(second))
				g.Expect(tagSha(g, tagStaging)).To(Equal(second))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Tagging the second commit for production before staging has run it")
			vcsPushTag(ctx, gitPath, tagProduction, second)
			hydrate(testBranchProductionNext, tagProduction)
			Eventually(func(g Gomega) {
				production := environmentStatus(g, testBranchProduction)
				g.Expect(production.Tag.Sha).To(Equal(second))
				g.Expect(production.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				g.Expect(production.Description).To(Equal(
					`Waiting for "environment/staging" to qualify dry ` + second[:7] + ` (newest qualified: ` + first[:7] + `)`))
			}, constants.EventuallyTimeout).Should(Succeed())
			Consistently(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchProduction)).To(Equal(first))
			}, 3*time.Second, 500*time.Millisecond).Should(Succeed())

			By("Promoting production once staging qualifies the tagged commit")
			hydrate(testBranchStagingNext, tagStaging)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchStaging)).To(Equal(second))
				g.Expect(activeDrySha(g, testBranchProduction)).To(Equal(second))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Releasing a third commit through staging: production's tag does not move by itself")
			third, err := makeDryCommit(ctx, gitPath, "third release")
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(third))
			}, constants.EventuallyTimeout).Should(Succeed())
			hydrate(testBranchDevelopmentNext, tagDevelopment)
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagStaging)).To(Equal(third))
			}, constants.EventuallyTimeout).Should(Succeed())
			hydrate(testBranchStagingNext, tagStaging)
			Eventually(func(g Gomega) {
				g.Expect(environmentStatus(g, testBranchStaging).Qualified.DrySha).To(Equal(third))
			}, constants.EventuallyTimeout).Should(Succeed())
			Consistently(func(g Gomega) {
				g.Expect(tagSha(g, tagProduction)).To(Equal(second))
				g.Expect(activeDrySha(g, testBranchProduction)).To(Equal(second))
			}, 3*time.Second, 500*time.Millisecond).Should(Succeed())

			By("Moving production's tag backward to the first commit")
			vcsPushTag(ctx, gitPath, tagProduction, first)
			hydrate(testBranchProductionNext, tagProduction)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchProduction)).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should leave a tag a person pushed where it is until the upstream qualifies something newer", func() {
			// The test repository starts with its first DRY commit hydrated to every environment and a second
			// DRY commit, the HEAD, hydrated nowhere.
			second := headSha()
			out, err := runGitCmd(ctx, gitPath, "rev-parse", "origin/"+dryBranch+"~1")
			Expect(err).NotTo(HaveOccurred())
			first := strings.TrimSpace(out)

			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())

			By("Promoting the second commit through every environment")
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(second))
			}, constants.EventuallyTimeout).Should(Succeed())
			hydrate(testBranchDevelopmentNext, tagDevelopment)
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagStaging)).To(Equal(second))
			}, constants.EventuallyTimeout).Should(Succeed())
			hydrate(testBranchStagingNext, tagStaging)
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagProduction)).To(Equal(second))
			}, constants.EventuallyTimeout).Should(Succeed())
			hydrate(testBranchProductionNext, tagProduction)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchProduction)).To(Equal(second))
				production := environmentStatus(g, testBranchProduction)
				g.Expect(production.Tag.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceAutomatic))
				g.Expect(production.Tag.AcknowledgedTarget).To(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Pushing production's tag back to the first commit by hand, and hydrating production from it")
			// A tag pushed by hand sends no webhook here. The hydrator's push is what wakes the gate.
			vcsPushTag(ctx, gitPath, tagProduction, first)
			hydrate(testBranchProductionNext, tagProduction)
			Eventually(func(g Gomega) {
				production := environmentStatus(g, testBranchProduction)
				g.Expect(production.Tag.Sha).To(Equal(first))
				g.Expect(production.Tag.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceExternal))
				g.Expect(production.Tag.AcknowledgedTarget).To(Equal(second), "what the controller had for production at the time")
				g.Expect(production.UpstreamCleared).NotTo(BeNil())
				g.Expect(production.UpstreamCleared.Sha).To(Equal(second))
				g.Expect(production.Held).To(BeNil())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Promoting what the person tagged: staging has qualified it")
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchProduction)).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Leaving the tag there, although staging has qualified the second commit")
			Consistently(func(g Gomega) {
				g.Expect(tagSha(g, tagProduction)).To(Equal(first))
			}, 3*time.Second, 500*time.Millisecond).Should(Succeed())

			By("Taking the tag over once staging qualifies a third commit")
			third, err := makeDryCommit(ctx, gitPath, "third release")
			Expect(err).NotTo(HaveOccurred())
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(third))
			}, constants.EventuallyTimeout).Should(Succeed())
			hydrate(testBranchDevelopmentNext, tagDevelopment)
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagStaging)).To(Equal(third))
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(vcsRemoteTagSha(ctx, gitPath, tagProduction)).To(Equal(first), "development qualifying the commit is nothing new for production")
			hydrate(testBranchStagingNext, tagStaging)
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagProduction)).To(Equal(third))
				production := environmentStatus(g, testBranchProduction)
				g.Expect(production.Tag.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceAutomatic))
				g.Expect(production.Tag.AcknowledgedTarget).To(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should move a tag to a commit a person validated, and promote it without asking the upstreams", func() {
			second := headSha()
			first := liveSha()

			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			gate.Spec.Environments[2].Validation = promoterv1alpha1.ValidationManual
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())

			By("Creating production's tag at what is live there, and leaving it")
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(second))
				g.Expect(tagSha(g, tagProduction)).To(Equal(first))
				production := environmentStatus(g, testBranchProduction)
				g.Expect(production.UpstreamCleared).NotTo(BeNil())
				g.Expect(production.UpstreamCleared.Sha).To(Equal(first), "staging runs the first commit")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Validating the second commit for production, which no other environment has run")
			validate(tagProduction, second, "CHG-1234 approved")
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagProduction)).To(Equal(second))
				production := environmentStatus(g, testBranchProduction)
				g.Expect(production.Tag.Sha).To(Equal(second))
				g.Expect(production.Tag.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceManual))
				g.Expect(production.Tag.ValidatedBy).To(Equal("testmail@test.com"), "the author of the note")
				g.Expect(production.Pending).To(BeNil())

				updated := &promoterv1alpha1.ValidatedCommitStatus{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gate), updated)).To(Succeed())
				g.Expect(updated.Status.ValidationsCursor).NotTo(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Promoting it to production, although staging still runs the first commit")
			hydrate(testBranchProductionNext, tagProduction)
			Eventually(func(g Gomega) {
				production := environmentStatus(g, testBranchProduction)
				g.Expect(production.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				g.Expect(production.Description).To(Equal(
					`Tag "env/production" at ` + second[:7] + ` is hydrated and was validated by testmail@test.com`))
				g.Expect(activeDrySha(g, testBranchProduction)).To(Equal(second))
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(vcsRemoteTagSha(ctx, gitPath, tagStaging)).To(Equal(first))

			By("Deleting the gate and creating it again: what it knew is in git")
			Expect(k8sClient.Delete(ctx, gate)).To(Succeed())
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, client.ObjectKeyFromObject(gate), &promoterv1alpha1.ValidatedCommitStatus{})
				g.Expect(k8serrors.IsNotFound(err)).To(BeTrue())
			}, constants.EventuallyTimeout).Should(Succeed())
			gate = newGate(name, dryBranch)
			gate.Spec.Environments[2].Validation = promoterv1alpha1.ValidationManual
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
			Eventually(func(g Gomega) {
				production := environmentStatus(g, testBranchProduction)
				g.Expect(production.Tag.Sha).To(Equal(second))
				g.Expect(production.Tag.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceManual), "the validation is in the notes")
				g.Expect(production.Tag.ValidatedBy).To(Equal("testmail@test.com"))
			}, constants.EventuallyTimeout).Should(Succeed())
			Consistently(func(g Gomega) {
				g.Expect(tagSha(g, tagProduction)).To(Equal(second))
			}, 3*time.Second, 500*time.Millisecond).Should(Succeed())
		})

		It("should send a hotfix that is not on the DRY branch to one environment, and pin it there until the branch contains the hotfix", func() {
			second := headSha()
			first := liveSha()

			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(second))
				g.Expect(tagSha(g, tagStaging)).To(Equal(first))
				cleared := environmentStatus(g, testBranchStaging).UpstreamCleared
				g.Expect(cleared).NotTo(BeNil(), "development has qualified what it runs")
				g.Expect(cleared.Sha).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Validating a hotfix for staging that starts at the first commit")
			hotfix := commitHotfix("hotfix", first)
			validate(tagStaging, hotfix, "hotfix")
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagStaging)).To(Equal(hotfix))
				staging := environmentStatus(g, testBranchStaging)
				g.Expect(staging.Tag.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceManual))
				g.Expect(staging.Tag.AcknowledgedTarget).To(Equal(first), "what development had qualified")
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Promoting the hotfix to staging, where it qualifies nothing: it is not on the DRY branch")
			hydrate(testBranchStagingNext, tagStaging)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchStaging)).To(Equal(hotfix))
			}, constants.EventuallyTimeout).Should(Succeed())
			Consistently(func(g Gomega) {
				staging := environmentStatus(g, testBranchStaging)
				g.Expect(staging.Qualified).NotTo(BeNil())
				g.Expect(staging.Qualified.DrySha).To(Equal(first))
				g.Expect(tagSha(g, tagProduction)).To(Equal(first), "production does not take the hotfix on its own")
			}, 3*time.Second, 500*time.Millisecond).Should(Succeed())

			By("Development qualifying the second commit, which does not contain the hotfix")
			hydrate(testBranchDevelopmentNext, tagDevelopment)
			Eventually(func(g Gomega) {
				staging := environmentStatus(g, testBranchStaging)
				g.Expect(staging.Held).NotTo(BeNil())
				g.Expect(staging.Held.Reason).To(Equal(promoterv1alpha1.ValidatedHeldReasonPinned))
				g.Expect(staging.Held.Sha).To(Equal(second))
				g.Expect(staging.Held.Message).To(Equal("Waiting for the dry branch to contain " + hotfix[:7]))
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(vcsRemoteTagSha(ctx, gitPath, tagStaging)).To(Equal(hotfix))

			By("Merging the hotfix into the DRY branch, and development qualifying the merge")
			merge := mergeIntoDry("hotfix")
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(merge))
			}, constants.EventuallyTimeout).Should(Succeed())
			hydrate(testBranchDevelopmentNext, tagDevelopment)

			By("Releasing the pin: the controller moves staging's tag again")
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagStaging)).To(Equal(merge))
				staging := environmentStatus(g, testBranchStaging)
				g.Expect(staging.Tag.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceAutomatic))
				g.Expect(staging.Held).To(BeNil())
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should ignore a validation of a commit that is not in the repository, or that is not a DRY commit", func() {
			first := liveSha()

			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			gate.Spec.Environments[2].Validation = promoterv1alpha1.ValidationManual
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
			var hydrated string
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagProduction)).To(Equal(first))
				hydrated = activeHydratedSha(g, testBranchProduction)
				g.Expect(hydrated).NotTo(BeEmpty())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Validating a hydrated commit, which the repository has and the DRY branch shares no history with")
			validate(tagProduction, hydrated, "not a dry commit")
			Eventually(func(g Gomega) {
				updated := &promoterv1alpha1.ValidatedCommitStatus{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gate), updated)).To(Succeed())
				g.Expect(updated.Status.ValidationsCursor).NotTo(BeEmpty(), "the validation was read, and passed over")
			}, constants.EventuallyTimeout).Should(Succeed())
			Consistently(func(g Gomega) {
				g.Expect(tagSha(g, tagProduction)).To(Equal(first))
				g.Expect(environmentStatus(g, testBranchProduction).Pending).To(BeNil())
			}, 3*time.Second, 500*time.Millisecond).Should(Succeed())
		})

		It("should record a tag that was pushed by hand in the validation notes", func() {
			second := headSha()
			first := liveSha()

			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			gate.Spec.Environments[2].Validation = promoterv1alpha1.ValidationManual
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagProduction)).To(Equal(first))
				g.Expect(environmentStatus(g, testBranchProduction).Tag.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceSeeded))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Pushing production's tag to the second commit by hand")
			vcsPushTag(ctx, gitPath, tagProduction, second)
			sendWebhookForRef(ctx, first, "refs/tags/"+tagProduction)
			Eventually(func(g Gomega) {
				production := environmentStatus(g, testBranchProduction)
				g.Expect(production.Tag.Sha).To(Equal(second))
				g.Expect(production.Tag.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceExternal))

				var records []validations.Line
				for _, line := range validationLines(second) {
					if line.Target == tagProduction && line.Source == validations.SourceExternal {
						records = append(records, line)
					}
				}
				g.Expect(records).To(HaveLen(1))
				g.Expect(records[0].By).To(Equal("ValidatedCommitStatus/" + gate.Name))
				g.Expect(records[0].Reason).To(Equal("Tag moved by hand"))
				g.Expect(records[0].Validates()).To(BeFalse())
				g.Expect(readyCondition(ctx, g, gate).Status).To(Equal(metav1.ConditionTrue))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Verifying the tag like any other: staging has not qualified the second commit")
			hydrate(testBranchProductionNext, tagProduction)
			Eventually(func(g Gomega) {
				production := environmentStatus(g, testBranchProduction)
				g.Expect(production.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				g.Expect(production.Description).To(Equal(
					`Waiting for "environment/staging" to qualify dry ` + second[:7] + ` (newest qualified: ` + first[:7] + `)`))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Validating the commit the tag is on: the same tag now passes")
			validate(tagProduction, second, "approved after all")
			Eventually(func(g Gomega) {
				production := environmentStatus(g, testBranchProduction)
				g.Expect(production.Tag.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceManual))
				g.Expect(production.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				g.Expect(activeDrySha(g, testBranchProduction)).To(Equal(second))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should keep a validation whose move of the tag is refused, and make the move once it is allowed", func() {
			if !scmTarget.isFake() {
				Skip("refusing a push takes a hook in the git server, which only the in-process server lets a spec install")
			}
			second := headSha()
			first := liveSha()

			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			gate.Spec.Environments[2].Validation = promoterv1alpha1.ValidationManual
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagProduction)).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Protecting production's tag on the git server")
			owner, repo := scmTarget.repoOwnerName(gitRepo)
			hooks := filepath.Join(gitStoragePath, owner, repo, "hooks")
			Expect(hooks).To(BeADirectory())
			hook := filepath.Join(hooks, "update")
			script := "#!/bin/sh\nif [ \"$1\" = \"refs/tags/" + tagProduction + "\" ]; then echo 'this tag is protected' >&2; exit 1; fi\n"
			Expect(os.WriteFile(hook, []byte(script), 0o755)).To(Succeed())
			DeferCleanup(func() { _ = os.Remove(hook) })

			By("Validating the second commit for production")
			validate(tagProduction, second, "approved")
			Eventually(func(g Gomega) {
				production := environmentStatus(g, testBranchProduction)
				g.Expect(production.Pending).NotTo(BeNil())
				g.Expect(production.Pending.Sha).To(Equal(second))
				g.Expect(production.Pending.ValidatedBy).To(Equal("testmail@test.com"))
				g.Expect(production.Pending.Message).NotTo(BeEmpty())
				g.Expect(production.Tag.Sha).To(Equal(first))
				g.Expect(production.Tag.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceSeeded))

				condition := readyCondition(ctx, g, gate)
				g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(condition.Reason).To(Equal(string(promoterConditions.TagPushFailed)))
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(vcsRemoteTagSha(ctx, gitPath, tagProduction)).To(Equal(first))

			By("Lifting the protection: the validation is still there to apply")
			Expect(os.Remove(hook)).To(Succeed())
			enqueueValidatedCommitStatus(gate.Namespace, gate.Name)
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagProduction)).To(Equal(second))
				production := environmentStatus(g, testBranchProduction)
				g.Expect(production.Pending).To(BeNil())
				g.Expect(production.Tag.Source).To(Equal(promoterv1alpha1.ValidatedTagSourceManual))
				g.Expect(production.Tag.ValidatedBy).To(Equal("testmail@test.com"))
				g.Expect(readyCondition(ctx, g, gate).Status).To(Equal(metav1.ConditionTrue))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})
