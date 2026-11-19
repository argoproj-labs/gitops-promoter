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
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

//go:embed testdata/DependentsQualifiedCommitStatus.yaml
var testDependentsQualifiedCommitStatusYAML string

// Environment branches and tags used by the decision specs.
const (
	dqDev     = "environment/dev"
	dqTest    = "environment/test"
	dqProd    = "environment/prod"
	dqTagDev  = "env/dev"
	dqTagTest = "env/test"
	dqTagProd = "env/prod"
)

// dqTestGraph is an in-memory DRY commit graph (commit → parents) implementing dqCommitGraph.
type dqTestGraph map[string][]string

// dqMainGraph is the history most specs run against:
//
//	a ─ b ─ c ─ d ─ e ─ f      DRY branch "main", HEAD = f
//	        └── x              a feature-branch commit that is not on main
//	    └── p ─ q              the DRY branch after a force-push that replaced c..f, HEAD = q
//	    ├── l ─┐
//	    └── r ─┴ m             two sides of a merge, HEAD = m
func dqMainGraph() dqTestGraph {
	return dqTestGraph{
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

func (g dqTestGraph) ancestors(sha string) map[string]struct{} {
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

func (g dqTestGraph) Contains(_ context.Context, descendant, ancestor string) (bool, error) {
	if _, ok := g[descendant]; !ok {
		return false, nil
	}
	if _, ok := g[ancestor]; !ok {
		return false, nil
	}
	_, ok := g.ancestors(descendant)[ancestor]
	return ok, nil
}

func (g dqTestGraph) MergeBase(_ context.Context, shas ...string) (string, error) {
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

// dqFailingGraph fails every ancestry question, for error-propagation specs.
type dqFailingGraph struct{}

func (dqFailingGraph) Contains(context.Context, string, string) (bool, error) {
	return false, errors.New("graph unavailable")
}

func (dqFailingGraph) MergeBase(context.Context, ...string) (string, error) {
	return "", errors.New("graph unavailable")
}

// dqDryRefs is one moment of a DRY repository: its branch HEAD and where each environment tag points.
type dqDryRefs struct {
	head string
	tags map[string]string
}

// dqMovingDryRepo is a DRY repository that receives a push while the gate is reading it: the first read sees
// before, every later read sees after. It implements dqDryRefReader.
type dqMovingDryRepo struct {
	err    error
	before dqDryRefs
	after  dqDryRefs
	reads  int
}

func (r *dqMovingDryRepo) now() dqDryRefs {
	r.reads++
	if r.reads == 1 {
		return r.before
	}
	return r.after
}

func (r *dqMovingDryRepo) GetBranchSha(context.Context, string, string) (string, error) {
	return r.now().head, r.err
}

func (r *dqMovingDryRepo) FetchTags(context.Context, ...string) (map[string]string, error) {
	return r.now().tags, r.err
}

// dqPhases builds active commit status phases keyed check-0, check-1, ...
func dqPhases(phases ...promoterv1alpha1.CommitStatusPhase) []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase {
	out := make([]promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase, 0, len(phases))
	for i, phase := range phases {
		out = append(out, promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
			Key:   "check-" + string(rune('0'+i)),
			Phase: string(phase),
		})
	}
	return out
}

// dqStatus builds a PromotionStrategy environment status. Hydrated SHAs are synthetic: "<branch>@<dry>" for
// the active tip and "<branch>-next@<dry>" for the proposed tip.
func dqStatus(branch, activeDry, proposedDry string, phases ...promoterv1alpha1.CommitStatusPhase) promoterv1alpha1.EnvironmentStatus {
	status := promoterv1alpha1.EnvironmentStatus{Branch: branch}
	status.Active.Dry.Sha = activeDry
	status.Active.Hydrated.Sha = branch + "@" + activeDry
	status.Active.CommitStatuses = dqPhases(phases...)
	status.Proposed.Dry.Sha = proposedDry
	status.Proposed.Hydrated.Sha = branch + "-next@" + proposedDry
	return status
}

// dqWithNote sets the hydrator note DRY SHA on the proposed tip (a no-op hydration report).
func dqWithNote(status promoterv1alpha1.EnvironmentStatus, noteDry string) promoterv1alpha1.EnvironmentStatus {
	status.Proposed.Note = &promoterv1alpha1.HydratorMetadata{DrySha: noteDry}
	return status
}

// dqWatermark builds a qualification watermark at the given DRY SHA.
func dqWatermark(drySha string) *promoterv1alpha1.DependentsQualifiedWatermark {
	return &promoterv1alpha1.DependentsQualifiedWatermark{
		DrySha:      drySha,
		QualifiedAt: metav1.NewTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)),
	}
}

// dqSettled builds an environment with nothing in flight: live is promoted, hydrated, and every active commit
// status has the given phase. tagSha "" means the tag does not exist.
func dqSettled(branch, tag, tagSha, live string, phases ...promoterv1alpha1.CommitStatusPhase) dqEnvironment {
	return dqEnvironment{
		Branch:     branch,
		Tag:        tag,
		TagSha:     tagSha,
		Status:     dqStatus(branch, live, live, phases...),
		LiveDrySha: live,
	}
}

// dqProposing builds an environment whose hydrator has proposed proposedDry while activeDry is live.
func dqProposing(branch, tag, tagSha, activeDry, proposedDry string, dependsOn ...string) dqEnvironment {
	return dqEnvironment{
		Branch:     branch,
		Tag:        tag,
		TagSha:     tagSha,
		DependsOn:  dependsOn,
		Status:     dqStatus(branch, activeDry, proposedDry),
		LiveDrySha: activeDry,
	}
}

func dqByBranch(envs ...dqEnvironment) map[string]dqEnvironment {
	out := make(map[string]dqEnvironment, len(envs))
	for _, env := range envs {
		out[env.Branch] = env
	}
	return out
}

var _ = Describe("DependentsQualifiedCommitStatus decisions", func() {
	var (
		ctx   context.Context
		graph dqTestGraph
		now   metav1.Time
	)

	const (
		success = promoterv1alpha1.CommitPhaseSuccess
		pending = promoterv1alpha1.CommitPhasePending
		failure = promoterv1alpha1.CommitPhaseFailure
	)

	BeforeEach(func() {
		ctx = context.Background()
		graph = dqMainGraph()
		now = metav1.NewTime(time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC))
	})

	Describe("dqLiveDrySha: which DRY commit an environment is running", func() {
		neverCalled := func() (bool, error) {
			Fail("manifests must not be compared when the answer is already known")
			return false, nil
		}

		It("is the active DRY SHA while a promotion is pending", func() {
			live, err := dqLiveDrySha(dqStatus(dqDev, "c", "d"), neverCalled)
			Expect(err).NotTo(HaveOccurred())
			Expect(live).To(Equal("c"))
		})

		It("is the active DRY SHA when the hydrator has reported nothing newer", func() {
			live, err := dqLiveDrySha(dqStatus(dqDev, "c", "c"), neverCalled)
			Expect(err).NotTo(HaveOccurred())
			Expect(live).To(Equal("c"))

			live, err = dqLiveDrySha(dqWithNote(dqStatus(dqDev, "c", "c"), "c"), neverCalled)
			Expect(err).NotTo(HaveOccurred())
			Expect(live).To(Equal("c"))
		})

		It("is empty when nothing has been promoted yet", func() {
			live, err := dqLiveDrySha(dqStatus(dqDev, "", ""), neverCalled)
			Expect(err).NotTo(HaveOccurred())
			Expect(live).To(BeEmpty())
		})

		It("is the hydrator note's DRY SHA after a no-op hydration of identical manifests", func() {
			status := dqWithNote(dqStatus(dqDev, "c", "c"), "e")
			live, err := dqLiveDrySha(status, func() (bool, error) { return true, nil })
			Expect(err).NotTo(HaveOccurred())
			Expect(live).To(Equal("e"))
		})

		It("stays on the active DRY SHA when the proposed manifests differ from the active manifests", func() {
			// The same DRY SHA can be hydrated again with different output. No pull request opens for it
			// (proposed and active name the same DRY SHA), so a later note on that tip must not be credited
			// to manifests the environment is not running.
			status := dqWithNote(dqStatus(dqDev, "c", "c"), "e")
			live, err := dqLiveDrySha(status, func() (bool, error) { return false, nil })
			Expect(err).NotTo(HaveOccurred())
			Expect(live).To(Equal("c"))
		})

		It("ignores a hydrator note while a promotion is pending", func() {
			status := dqWithNote(dqStatus(dqDev, "c", "d"), "e")
			live, err := dqLiveDrySha(status, neverCalled)
			Expect(err).NotTo(HaveOccurred())
			Expect(live).To(Equal("c"))
		})

		It("returns the manifest comparison error", func() {
			status := dqWithNote(dqStatus(dqDev, "c", "c"), "e")
			_, err := dqLiveDrySha(status, func() (bool, error) { return false, errors.New("boom") })
			Expect(err).To(MatchError(ContainSubstring("boom")))
		})
	})

	Describe("dqQualify: recording what an environment has run successfully", func() {
		It("qualifies the live commit when every active commit status is success", func() {
			env := dqSettled(dqDev, dqTagDev, "d", "d", success, success)
			qualified, err := dqQualify(ctx, graph, "f", env, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).NotTo(BeNil())
			Expect(qualified.DrySha).To(Equal("d"))
			Expect(qualified.HydratedSha).To(Equal(env.Status.Active.Hydrated.Sha))
			Expect(qualified.QualifiedAt).To(Equal(now))
		})

		It("qualifies immediately when the environment has no active commit statuses", func() {
			qualified, err := dqQualify(ctx, graph, "f", dqSettled(dqDev, dqTagDev, "d", "d"), now)
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).NotTo(BeNil())
			Expect(qualified.DrySha).To(Equal("d"))
		})

		DescribeTable("does not qualify while an active commit status is not success",
			func(phases ...promoterv1alpha1.CommitStatusPhase) {
				qualified, err := dqQualify(ctx, graph, "f", dqSettled(dqDev, dqTagDev, "d", "d", phases...), now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified).To(BeNil())
			},
			Entry("pending", pending),
			Entry("failure", failure),
			Entry("one of several pending", success, pending),
		)

		It("does not qualify when nothing is live", func() {
			qualified, err := dqQualify(ctx, graph, "f", dqSettled(dqDev, dqTagDev, "f", ""), now)
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(BeNil())
		})

		It("advances the watermark when a newer commit is live and successful", func() {
			env := dqSettled(dqDev, dqTagDev, "f", "f", success)
			env.Qualified = dqWatermark("d")
			qualified, err := dqQualify(ctx, graph, "f", env, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified.DrySha).To(Equal("f"))
			Expect(qualified.QualifiedAt).To(Equal(now))
		})

		It("leaves the watermark untouched when the live commit is already the watermark", func() {
			env := dqSettled(dqDev, dqTagDev, "d", "d", success)
			env.Qualified = dqWatermark("d")
			qualified, err := dqQualify(ctx, graph, "f", env, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(Equal(dqWatermark("d")), "qualifiedAt records when the commit first qualified")
		})

		It("is latched: a qualified commit stays qualified when the environment turns unhealthy", func() {
			env := dqSettled(dqDev, dqTagDev, "d", "d", failure)
			env.Qualified = dqWatermark("d")
			qualified, err := dqQualify(ctx, graph, "f", env, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(Equal(dqWatermark("d")))
		})

		It("is latched: the watermark survives a newer commit that is live but not yet successful", func() {
			env := dqSettled(dqDev, dqTagDev, "f", "f", pending)
			env.Qualified = dqWatermark("d")
			qualified, err := dqQualify(ctx, graph, "f", env, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(Equal(dqWatermark("d")))
		})

		It("is forward-only: promoting an older commit does not move the watermark back", func() {
			// A person moved the environment's tag backward and the older commit was promoted the normal
			// way. Nothing said the newer commit was bad, so it stays qualified.
			env := dqSettled(dqProd, dqTagProd, "c", "c", success)
			env.Qualified = dqWatermark("e")
			qualified, err := dqQualify(ctx, graph, "f", env, now)
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(Equal(dqWatermark("e")))
		})

		Context("commits that are not on the DRY branch", func() {
			It("does not qualify a live commit that is not reachable from the DRY branch HEAD", func() {
				// A person pushed a root environment's tag to a feature-branch commit and it was promoted.
				qualified, err := dqQualify(ctx, graph, "f", dqSettled(dqDev, dqTagDev, "x", "x", success), now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified).To(BeNil())
			})

			It("keeps an on-branch watermark while an off-branch commit is live", func() {
				env := dqSettled(dqDev, dqTagDev, "x", "x", success)
				env.Qualified = dqWatermark("c")
				qualified, err := dqQualify(ctx, graph, "f", env, now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified).To(Equal(dqWatermark("c")))
			})
		})

		Context("after the DRY branch is force-pushed", func() {
			It("drops a watermark that is no longer reachable from HEAD", func() {
				env := dqSettled(dqDev, dqTagDev, "e", "e", success)
				env.Qualified = dqWatermark("e")
				qualified, err := dqQualify(ctx, graph, "q", env, now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified).To(BeNil(), "e was rewritten away and e is still what is live")
			})

			It("keeps a watermark that survived the rewrite", func() {
				env := dqSettled(dqDev, dqTagDev, "e", "e", success)
				env.Qualified = dqWatermark("b")
				qualified, err := dqQualify(ctx, graph, "q", env, now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified).To(Equal(dqWatermark("b")))
			})

			It("qualifies again once a commit on the new history is live and successful", func() {
				env := dqSettled(dqDev, dqTagDev, "q", "q", success)
				env.Qualified = dqWatermark("e")
				qualified, err := dqQualify(ctx, graph, "q", env, now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified.DrySha).To(Equal("q"))
			})
		})

		Context("after a RestoreActiveCommit", func() {
			It("pulls the watermark back to the restored commit", func() {
				env := dqSettled(dqTest, dqTagTest, "e", "c", pending)
				env.Qualified = dqWatermark("e")
				env.Restored = true
				qualified, err := dqQualify(ctx, graph, "f", env, now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified).NotTo(BeNil())
				Expect(qualified.DrySha).To(Equal("c"), "the restore says d and e should not have been trusted")
				Expect(qualified.HydratedSha).To(Equal(env.Status.Active.Hydrated.Sha))
				Expect(qualified.QualifiedAt).To(Equal(now))
			})

			It("does nothing more once the watermark is at the restored commit", func() {
				env := dqSettled(dqTest, dqTagTest, "e", "c", success)
				env.Qualified = dqWatermark("c")
				env.Restored = true
				qualified, err := dqQualify(ctx, graph, "f", env, now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified).To(Equal(dqWatermark("c")))
			})

			It("does not pull back a watermark the restored commit is ahead of", func() {
				env := dqSettled(dqTest, dqTagTest, "e", "e", pending)
				env.Qualified = dqWatermark("c")
				env.Restored = true
				qualified, err := dqQualify(ctx, graph, "f", env, now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified).To(Equal(dqWatermark("c")))
			})

			It("qualifies forward again when the environment is promoted past the restore", func() {
				env := dqSettled(dqTest, dqTagTest, "f", "f", success)
				env.Qualified = dqWatermark("c")
				qualified, err := dqQualify(ctx, graph, "f", env, now)
				Expect(err).NotTo(HaveOccurred())
				Expect(qualified.DrySha).To(Equal("f"))
			})
		})

		It("returns ancestry errors", func() {
			env := dqSettled(dqDev, dqTagDev, "f", "f", success)
			env.Qualified = dqWatermark("d")
			_, err := dqQualify(ctx, dqFailingGraph{}, "f", env, now)
			Expect(err).To(MatchError(ContainSubstring("graph unavailable")))
		})
	})

	Describe("dqUnrecordedQualification: what stands when a qualification could not be written to its note", func() {
		It("keeps the previous watermark when an advance could not be recorded", func() {
			env := dqSettled(dqDev, dqTagDev, "f", "f", success)
			env.Qualified = dqWatermark("d")
			qualified, err := dqUnrecordedQualification(ctx, graph, "f", env, dqWatermark("f"))
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(Equal(dqWatermark("d")))
		})

		It("qualifies nothing when a first qualification could not be recorded", func() {
			env := dqSettled(dqDev, dqTagDev, "f", "f", success)
			qualified, err := dqUnrecordedQualification(ctx, graph, "f", env, dqWatermark("f"))
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(BeNil())
		})

		It("takes a restore's pull-back anyway, so that the gate does not stay open on a revoked commit", func() {
			env := dqSettled(dqTest, dqTagTest, "e", "c", pending)
			env.Qualified = dqWatermark("e")
			env.Restored = true
			qualified, err := dqUnrecordedQualification(ctx, graph, "f", env, dqWatermark("c"))
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(Equal(dqWatermark("c")), "d and e were taken back by the restore, recorded or not")
		})

		It("drops a previous watermark that is no longer on the DRY branch", func() {
			env := dqSettled(dqDev, dqTagDev, "q", "q", success)
			env.Qualified = dqWatermark("e")
			qualified, err := dqUnrecordedQualification(ctx, graph, "q", env, dqWatermark("q"))
			Expect(err).NotTo(HaveOccurred())
			Expect(qualified).To(BeNil())
		})

		It("returns ancestry errors", func() {
			env := dqSettled(dqDev, dqTagDev, "f", "f", success)
			env.Qualified = dqWatermark("d")
			_, err := dqUnrecordedQualification(ctx, dqFailingGraph{}, "f", env, dqWatermark("f"))
			Expect(err).To(MatchError(ContainSubstring("graph unavailable")))
		})
	})

	Describe("dqReadDryRefs: reading the DRY branch HEAD and the environment tags", func() {
		It("returns the HEAD and the commit each tag points at", func() {
			refs := dqDryRefs{head: "f", tags: map[string]string{dqTagDev: "f", dqTagTest: "d", dqTagProd: ""}}
			head, tags, err := dqReadDryRefs(ctx, &dqMovingDryRepo{before: refs, after: refs}, "main", "", []string{dqTagDev, dqTagTest, dqTagProd})
			Expect(err).NotTo(HaveOccurred())
			Expect(head).To(Equal("f"))
			Expect(tags).To(Equal(refs.tags))
		})

		It("never reads a tag that is ahead of the HEAD it read, when both move during the reads", func() {
			// Someone pushes f and moves the tag to it between the gate's two reads. A tag that looks like
			// it is not on the branch gets reset, so the gate must not pair the new tag with the old HEAD.
			repo := &dqMovingDryRepo{
				before: dqDryRefs{head: "e", tags: map[string]string{dqTagDev: "e"}},
				after:  dqDryRefs{head: "f", tags: map[string]string{dqTagDev: "f"}},
			}
			head, tags, err := dqReadDryRefs(ctx, repo, "main", "e", []string{dqTagDev})
			Expect(err).NotTo(HaveOccurred())
			onBranch, err := graph.Contains(ctx, head, tags[dqTagDev])
			Expect(err).NotTo(HaveOccurred())
			Expect(onBranch).To(BeTrue(), "read HEAD %s and tag at %s", head, tags[dqTagDev])
		})

		It("returns read errors", func() {
			_, _, err := dqReadDryRefs(ctx, &dqMovingDryRepo{err: errors.New("remote unavailable")}, "main", "", []string{dqTagDev})
			Expect(err).To(MatchError(ContainSubstring("remote unavailable")))
		})
	})

	Describe("dqPlanTags: where each environment tag should point", func() {
		// chain returns dev → test → prod with the given tag SHAs; dev is live on devLive.
		chain := func(devTag, testTag, prodTag string) (dqEnvironment, dqEnvironment, dqEnvironment) {
			dev := dqSettled(dqDev, dqTagDev, devTag, devTag)
			test := dqSettled(dqTest, dqTagTest, testTag, testTag)
			test.DependsOn = []string{dqDev}
			prod := dqSettled(dqProd, dqTagProd, prodTag, prodTag)
			prod.DependsOn = []string{dqTest}
			return dev, test, prod
		}

		It("moves a root environment's tag to the DRY branch HEAD", func() {
			dev, test, prod := chain("d", "d", "d")
			moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev, test, prod})
			Expect(err).NotTo(HaveOccurred())
			Expect(moves).To(Equal([]dqTagMove{{Branch: dqDev, Tag: dqTagDev, From: "d", To: "f"}}))
		})

		It("moves nothing when every tag is where it should be", func() {
			dev, test, prod := chain("f", "d", "c")
			dev.Qualified = dqWatermark("d")
			test.Qualified = dqWatermark("c")
			moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev, test, prod})
			Expect(err).NotTo(HaveOccurred())
			Expect(moves).To(BeEmpty())
		})

		It("moves a dependent's tag to the newest commit its upstream has qualified", func() {
			dev, test, prod := chain("f", "c", "c")
			dev.Qualified = dqWatermark("e")
			moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev, test, prod})
			Expect(err).NotTo(HaveOccurred())
			Expect(moves).To(Equal([]dqTagMove{{Branch: dqTest, Tag: dqTagTest, From: "c", To: "e"}}))
		})

		It("does not move a dependent's tag while its upstream has qualified nothing", func() {
			dev, test, prod := chain("f", "c", "c")
			moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev, test, prod})
			Expect(err).NotTo(HaveOccurred())
			Expect(moves).To(BeEmpty())
		})

		It("looks only at direct upstreams", func() {
			dev, test, prod := chain("f", "f", "b")
			dev.Qualified = dqWatermark("f")
			test.Qualified = dqWatermark("c")
			moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev, test, prod})
			Expect(err).NotTo(HaveOccurred())
			Expect(moves).To(Equal([]dqTagMove{{Branch: dqProd, Tag: dqTagProd, From: "b", To: "c"}}),
				"prod follows test's watermark, not dev's")
		})

		It("ignores the environment's own state: an open promotion and running checks do not delay its tag", func() {
			dev, _, _ := chain("f", "", "")
			dev.Qualified = dqWatermark("e")
			test := dqProposing(dqTest, dqTagTest, "d", "c", "d", dqDev)
			test.Status.Active.CommitStatuses = dqPhases(pending)
			moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev, test})
			Expect(err).NotTo(HaveOccurred())
			Expect(moves).To(Equal([]dqTagMove{{Branch: dqTest, Tag: dqTagTest, From: "d", To: "e"}}))
		})

		It("never moves a tag backward", func() {
			// test's tag is at e; dev was restored and its watermark pulled back to c.
			dev, test, prod := chain("f", "e", "b")
			dev.Qualified = dqWatermark("c")
			moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev, test, prod})
			Expect(err).NotTo(HaveOccurred())
			Expect(moves).To(BeEmpty())
		})

		It("plans one move per tag, in environment order", func() {
			dev, test, prod := chain("c", "b", "a")
			dev.Qualified = dqWatermark("c")
			test.Qualified = dqWatermark("b")
			moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev, test, prod})
			Expect(err).NotTo(HaveOccurred())
			Expect(moves).To(Equal([]dqTagMove{
				{Branch: dqDev, Tag: dqTagDev, From: "c", To: "f"},
				{Branch: dqTest, Tag: dqTagTest, From: "b", To: "c"},
				{Branch: dqProd, Tag: dqTagProd, From: "a", To: "b"},
			}))
		})

		Context("fan-in", func() {
			fanIn := func(leftQualified, rightQualified *promoterv1alpha1.DependentsQualifiedWatermark, prodTag string) []dqEnvironment {
				left := dqSettled("environment/left", "env/left", "f", "f")
				left.Qualified = leftQualified
				right := dqSettled("environment/right", "env/right", "f", "f")
				right.Qualified = rightQualified
				prod := dqSettled(dqProd, dqTagProd, prodTag, prodTag)
				prod.DependsOn = []string{left.Branch, right.Branch}
				return []dqEnvironment{left, right, prod}
			}

			It("moves the tag to the newest commit every upstream has qualified", func() {
				moves, err := dqPlanTags(ctx, graph, "f", fanIn(dqWatermark("f"), dqWatermark("d"), "b"))
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]dqTagMove{{Branch: dqProd, Tag: dqTagProd, From: "b", To: "d"}}))
			})

			It("waits while any upstream has qualified nothing", func() {
				moves, err := dqPlanTags(ctx, graph, "f", fanIn(dqWatermark("f"), nil, "b"))
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(BeEmpty())
			})

			It("uses the merge base when the upstream watermarks are on different sides of a merge", func() {
				envs := fanIn(dqWatermark("l"), dqWatermark("r"), "a")
				envs[0].TagSha, envs[1].TagSha = "m", "m"
				moves, err := dqPlanTags(ctx, graph, "m", envs)
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]dqTagMove{{Branch: dqProd, Tag: dqTagProd, From: "a", To: "b"}}),
					"b is the newest commit both l and r contain")
			})
		})

		Context("advance: Manual", func() {
			It("never moves the tag", func() {
				dev, test, prod := chain("f", "f", "b")
				dev.Qualified = dqWatermark("f")
				test.Qualified = dqWatermark("f")
				prod.Manual = true
				moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev, test, prod})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(BeEmpty())
			})

			It("never moves a root's tag", func() {
				dev, _, _ := chain("c", "", "")
				dev.Manual = true
				moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(BeEmpty())
			})

			It("creates a missing tag at the live commit and then leaves it alone", func() {
				dev, test, prod := chain("f", "f", "")
				dev.Qualified = dqWatermark("f")
				test.Qualified = dqWatermark("f")
				prod.Manual = true
				prod.LiveDrySha = "c"
				moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev, test, prod})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]dqTagMove{{Branch: dqProd, Tag: dqTagProd, From: "", To: "c"}}),
					"the first hydration from the tag is a no-op; a person decides what comes next")
			})
		})

		Context("bootstrapping missing tags", func() {
			It("creates a dependent's tag at the commit that is live in it", func() {
				dev, test, prod := chain("f", "", "")
				test.LiveDrySha = "d"
				prod.LiveDrySha = "c"
				moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev, test, prod})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]dqTagMove{
					{Branch: dqTest, Tag: dqTagTest, From: "", To: "d"},
					{Branch: dqProd, Tag: dqTagProd, From: "", To: "c"},
				}))
			})

			It("creates the tag at the DRY branch HEAD when nothing is live", func() {
				dev, test, prod := chain("", "", "")
				moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev, test, prod})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]dqTagMove{
					{Branch: dqDev, Tag: dqTagDev, From: "", To: "f"},
					{Branch: dqTest, Tag: dqTagTest, From: "", To: "f"},
					{Branch: dqProd, Tag: dqTagProd, From: "", To: "f"},
				}), "the gate still holds each promotion until the upstreams qualify HEAD")
			})

			It("creates the tag at the DRY branch HEAD when the live commit is not on the DRY branch", func() {
				dev, test, _ := chain("f", "", "")
				test.LiveDrySha = "x"
				moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev, test})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]dqTagMove{{Branch: dqTest, Tag: dqTagTest, From: "", To: "f"}}))
			})

			It("creates a root's tag directly at the DRY branch HEAD", func() {
				dev, _, _ := chain("", "", "")
				dev.LiveDrySha = "c"
				moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]dqTagMove{{Branch: dqDev, Tag: dqTagDev, From: "", To: "f"}}))
			})

			It("creates a dependent's tag directly at its upstream's watermark when that is ahead of what is live", func() {
				dev, test, _ := chain("f", "", "")
				dev.Qualified = dqWatermark("e")
				test.LiveDrySha = "c"
				moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev, test})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]dqTagMove{{Branch: dqTest, Tag: dqTagTest, From: "", To: "e"}}))
			})

			It("does not create a dependent's tag behind what is live", func() {
				dev, test, _ := chain("f", "", "")
				dev.Qualified = dqWatermark("c")
				test.LiveDrySha = "e"
				moves, err := dqPlanTags(ctx, graph, "f", []dqEnvironment{dev, test})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]dqTagMove{{Branch: dqTest, Tag: dqTagTest, From: "", To: "e"}}))
			})
		})

		Context("after the DRY branch is force-pushed", func() {
			It("resets a root's tag that is no longer reachable from HEAD", func() {
				dev, _, _ := chain("e", "", "")
				moves, err := dqPlanTags(ctx, graph, "q", []dqEnvironment{dev})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]dqTagMove{{Branch: dqDev, Tag: dqTagDev, From: "e", To: "q"}}))
			})

			It("leaves a dependent's unreachable tag until an upstream qualifies a commit on the new history", func() {
				dev, test, _ := chain("q", "d", "")
				moves, err := dqPlanTags(ctx, graph, "q", []dqEnvironment{dev, test})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(BeEmpty())

				dev.Qualified = dqWatermark("p")
				moves, err = dqPlanTags(ctx, graph, "q", []dqEnvironment{dev, test})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]dqTagMove{{Branch: dqTest, Tag: dqTagTest, From: "d", To: "p"}}))
			})

			It("resets a dependent's unreachable tag even when the target is not ahead of it", func() {
				dev, test, _ := chain("q", "e", "")
				dev.Qualified = dqWatermark("b")
				moves, err := dqPlanTags(ctx, graph, "q", []dqEnvironment{dev, test})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(Equal([]dqTagMove{{Branch: dqTest, Tag: dqTagTest, From: "e", To: "b"}}))
			})

			It("leaves an advance: Manual tag alone", func() {
				dev, _, _ := chain("e", "", "")
				dev.Manual = true
				moves, err := dqPlanTags(ctx, graph, "q", []dqEnvironment{dev})
				Expect(err).NotTo(HaveOccurred())
				Expect(moves).To(BeEmpty())
			})
		})

		It("returns ancestry errors", func() {
			dev, test, prod := chain("d", "d", "d")
			_, err := dqPlanTags(ctx, dqFailingGraph{}, "f", []dqEnvironment{dev, test, prod})
			Expect(err).To(MatchError(ContainSubstring("graph unavailable")))
		})
	})

	Describe("dqEvaluateGate: whether an environment's proposed commit may be promoted", func() {
		It("is pending until the environment's tag exists", func() {
			dev := dqProposing(dqDev, dqTagDev, "", "c", "d")
			gate, err := dqEvaluateGate(ctx, graph, "f", "main", dev, dqByBranch(dev))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(pending))
			Expect(gate.Description).To(Equal(`Waiting for tag "env/dev" to be created`))
		})

		It("is pending until the hydrator has processed the tagged commit", func() {
			dev := dqProposing(dqDev, dqTagDev, "f", "c", "e")
			gate, err := dqEvaluateGate(ctx, graph, "f", "main", dev, dqByBranch(dev))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(pending))
			Expect(gate.Description).To(Equal(`Waiting for hydrator to process tag "env/dev" at f (currently e)`))
		})

		It("is pending when the hydrator has reported nothing", func() {
			dev := dqProposing(dqDev, dqTagDev, "f", "c", "")
			gate, err := dqEvaluateGate(ctx, graph, "f", "main", dev, dqByBranch(dev))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(pending))
			Expect(gate.Description).To(Equal(`Waiting for hydrator to process tag "env/dev" at f (currently none)`))
		})

		It("shortens SHAs to seven characters in descriptions", func() {
			const tagSha = "0123456789abcdef0123456789abcdef01234567"
			const proposedSha = "fedcba9876543210fedcba9876543210fedcba98"
			dev := dqProposing(dqDev, dqTagDev, tagSha, "c", proposedSha)
			gate, err := dqEvaluateGate(ctx, graph, "f", "main", dev, dqByBranch(dev))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Description).To(Equal(`Waiting for hydrator to process tag "env/dev" at 0123456 (currently fedcba9)`))
		})

		It("prefers the hydrator note over hydrator.metadata for the hydrated DRY SHA", func() {
			dev := dqProposing(dqDev, dqTagDev, "f", "c", "e")
			dev.Status = dqWithNote(dev.Status, "f")
			gate, err := dqEvaluateGate(ctx, graph, "f", "main", dev, dqByBranch(dev))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(success))
		})

		It("succeeds for a root once the proposed commit was hydrated from its tag", func() {
			dev := dqProposing(dqDev, dqTagDev, "f", "c", "f")
			gate, err := dqEvaluateGate(ctx, graph, "f", "main", dev, dqByBranch(dev))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(success))
			Expect(gate.Description).To(Equal(`Tag "env/dev" at f is hydrated`))
			Expect(gate.Upstreams).To(BeEmpty())
		})

		It("succeeds for a dependent when its upstream qualified exactly the tagged commit", func() {
			dev := dqSettled(dqDev, dqTagDev, "f", "f", success)
			dev.Qualified = dqWatermark("e")
			test := dqProposing(dqTest, dqTagTest, "e", "c", "e", dqDev)
			gate, err := dqEvaluateGate(ctx, graph, "f", "main", test, dqByBranch(dev, test))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(success))
			Expect(gate.Description).To(Equal(`Tag "env/test" at e is hydrated and qualified upstream`))
			Expect(gate.Upstreams).To(Equal([]promoterv1alpha1.DependentsQualifiedCommitStatusUpstreamStatus{
				{Branch: dqDev, Satisfied: true},
			}))
		})

		It("succeeds when the upstream qualified a newer commit that contains the tagged commit", func() {
			// The upstream never ran d itself. It ran f, which contains d.
			dev := dqSettled(dqDev, dqTagDev, "f", "f", success)
			dev.Qualified = dqWatermark("f")
			test := dqProposing(dqTest, dqTagTest, "d", "c", "d", dqDev)
			gate, err := dqEvaluateGate(ctx, graph, "f", "main", test, dqByBranch(dev, test))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(success))
		})

		It("stays successful when the upstream turns unhealthy after qualifying", func() {
			dev := dqSettled(dqDev, dqTagDev, "f", "f", failure)
			dev.Qualified = dqWatermark("f")
			test := dqProposing(dqTest, dqTagTest, "f", "c", "f", dqDev)
			gate, err := dqEvaluateGate(ctx, graph, "f", "main", test, dqByBranch(dev, test))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(success))
		})

		It("is pending when a person pushed the tag ahead of what the upstream has qualified", func() {
			dev := dqSettled(dqDev, dqTagDev, "f", "f", pending)
			dev.Qualified = dqWatermark("d")
			test := dqProposing(dqTest, dqTagTest, "f", "c", "f", dqDev)
			test.Manual = true
			gate, err := dqEvaluateGate(ctx, graph, "f", "main", test, dqByBranch(dev, test))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(pending))
			Expect(gate.Description).To(Equal(`Waiting for "environment/dev" to qualify dry f (newest qualified: d)`))
			Expect(gate.Upstreams).To(Equal([]promoterv1alpha1.DependentsQualifiedCommitStatusUpstreamStatus{
				{Branch: dqDev, Satisfied: false, Reason: `Waiting for "environment/dev" to qualify dry f (newest qualified: d)`},
			}))
		})

		It("is pending when the upstream has qualified nothing", func() {
			dev := dqSettled(dqDev, dqTagDev, "f", "f", pending)
			test := dqProposing(dqTest, dqTagTest, "f", "c", "f", dqDev)
			gate, err := dqEvaluateGate(ctx, graph, "f", "main", test, dqByBranch(dev, test))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(pending))
			Expect(gate.Description).To(Equal(`Waiting for "environment/dev" to qualify dry f (newest qualified: none)`))
		})

		It("succeeds for a tag a person moved backward", func() {
			dev := dqSettled(dqDev, dqTagDev, "f", "f", success)
			dev.Qualified = dqWatermark("f")
			test := dqProposing(dqTest, dqTagTest, "b", "e", "b", dqDev)
			test.Manual = true
			gate, err := dqEvaluateGate(ctx, graph, "f", "main", test, dqByBranch(dev, test))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(success))
		})

		It("goes pending when a RestoreActiveCommit pulled the upstream's watermark back", func() {
			// test's tag already advanced to e. dev was then restored to c, revoking d and e.
			dev := dqSettled(dqDev, dqTagDev, "f", "c", success)
			dev.Qualified = dqWatermark("c")
			test := dqProposing(dqTest, dqTagTest, "e", "c", "e", dqDev)
			gate, err := dqEvaluateGate(ctx, graph, "f", "main", test, dqByBranch(dev, test))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(pending))
			Expect(gate.Description).To(Equal(`Waiting for "environment/dev" to qualify dry e (newest qualified: c)`))
		})

		It("is pending for a tagged commit that is not on the DRY branch", func() {
			dev := dqProposing(dqDev, dqTagDev, "x", "c", "x")
			dev.Manual = true
			gate, err := dqEvaluateGate(ctx, graph, "f", "main", dev, dqByBranch(dev))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(pending))
			Expect(gate.Description).To(Equal(`Tag "env/dev" at x is not on dry branch "main"`))
		})

		It("is pending for a tagged commit that was rewritten away by a force-push", func() {
			dev := dqSettled(dqDev, dqTagDev, "q", "q", success)
			dev.Qualified = dqWatermark("q")
			test := dqProposing(dqTest, dqTagTest, "e", "c", "e", dqDev)
			gate, err := dqEvaluateGate(ctx, graph, "q", "main", test, dqByBranch(dev, test))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(pending))
			Expect(gate.Description).To(Equal(`Tag "env/test" at e is not on dry branch "main"`))
		})

		It("looks only at direct upstreams", func() {
			dev := dqSettled(dqDev, dqTagDev, "f", "f", pending)
			dev.Qualified = dqWatermark("b")
			test := dqSettled(dqTest, dqTagTest, "e", "e", success)
			test.DependsOn = []string{dqDev}
			test.Qualified = dqWatermark("e")
			prod := dqProposing(dqProd, dqTagProd, "e", "c", "e", dqTest)
			gate, err := dqEvaluateGate(ctx, graph, "f", "main", prod, dqByBranch(dev, test, prod))
			Expect(err).NotTo(HaveOccurred())
			Expect(gate.Phase).To(Equal(success), "test qualified e; how test got there is test's own gate's business")
			Expect(gate.Upstreams).To(HaveLen(1))
		})

		Context("fan-in", func() {
			var left, right, prod dqEnvironment

			BeforeEach(func() {
				left = dqSettled("environment/left", "env/left", "f", "f", success)
				right = dqSettled("environment/right", "env/right", "f", "f", success)
				prod = dqProposing(dqProd, dqTagProd, "e", "c", "e", left.Branch, right.Branch)
			})

			It("succeeds when every upstream qualified the tagged commit", func() {
				left.Qualified = dqWatermark("f")
				right.Qualified = dqWatermark("e")
				gate, err := dqEvaluateGate(ctx, graph, "f", "main", prod, dqByBranch(left, right, prod))
				Expect(err).NotTo(HaveOccurred())
				Expect(gate.Phase).To(Equal(success))
				Expect(gate.Upstreams).To(Equal([]promoterv1alpha1.DependentsQualifiedCommitStatusUpstreamStatus{
					{Branch: left.Branch, Satisfied: true},
					{Branch: right.Branch, Satisfied: true},
				}))
			})

			It("reports every upstream and describes the first one it is waiting for", func() {
				left.Qualified = dqWatermark("f")
				right.Qualified = dqWatermark("d")
				gate, err := dqEvaluateGate(ctx, graph, "f", "main", prod, dqByBranch(left, right, prod))
				Expect(err).NotTo(HaveOccurred())
				Expect(gate.Phase).To(Equal(pending))
				Expect(gate.Description).To(Equal(`Waiting for "environment/right" to qualify dry e (newest qualified: d)`))
				Expect(gate.Upstreams).To(Equal([]promoterv1alpha1.DependentsQualifiedCommitStatusUpstreamStatus{
					{Branch: left.Branch, Satisfied: true},
					{Branch: right.Branch, Satisfied: false, Reason: `Waiting for "environment/right" to qualify dry e (newest qualified: d)`},
				}))
			})
		})

		It("returns ancestry errors", func() {
			dev := dqProposing(dqDev, dqTagDev, "f", "c", "f")
			_, err := dqEvaluateGate(ctx, dqFailingGraph{}, "f", "main", dev, dqByBranch(dev))
			Expect(err).To(MatchError(ContainSubstring("graph unavailable")))
		})
	})

	Describe("a release moving through dev → test → prod", func() {
		// step runs one reconcile's worth of decisions and applies the planned tag moves, as the reconciler does.
		step := func(head string, envs []dqEnvironment) ([]dqEnvironment, []dqTagMove) {
			GinkgoHelper()
			for i := range envs {
				qualified, err := dqQualify(ctx, graph, head, envs[i], now)
				Expect(err).NotTo(HaveOccurred())
				envs[i].Qualified = qualified
			}
			moves, err := dqPlanTags(ctx, graph, head, envs)
			Expect(err).NotTo(HaveOccurred())
			for _, move := range moves {
				idx := slices.IndexFunc(envs, func(e dqEnvironment) bool { return e.Branch == move.Branch })
				Expect(envs[idx].TagSha).To(Equal(move.From), "the lease value must be the tag the plan was made from")
				envs[idx].TagSha = move.To
			}
			return envs, moves
		}
		// promote makes dry live in the environment at index i with the given active commit status phases.
		promote := func(envs []dqEnvironment, i int, dry string, phases ...promoterv1alpha1.CommitStatusPhase) {
			envs[i].Status = dqStatus(envs[i].Branch, dry, dry, phases...)
			envs[i].LiveDrySha = dry
		}
		gatePhase := func(head string, envs []dqEnvironment, i int, proposedDry string) promoterv1alpha1.CommitStatusPhase {
			GinkgoHelper()
			env := envs[i]
			env.Status = dqStatus(env.Branch, env.LiveDrySha, proposedDry)
			gate, err := dqEvaluateGate(ctx, graph, head, "main", env, dqByBranch(envs...))
			Expect(err).NotTo(HaveOccurred())
			return gate.Phase
		}

		It("holds higher environments on qualified commits while newer commits land", func() {
			envs := []dqEnvironment{
				dqSettled(dqDev, dqTagDev, "", "c", success),
				dqSettled(dqTest, dqTagTest, "", "c", success),
				dqSettled(dqProd, dqTagProd, "", "c", success),
			}
			envs[1].DependsOn = []string{dqDev}
			envs[2].DependsOn = []string{dqTest}

			By("bootstrapping: every environment qualifies what it runs; dev's tag goes to HEAD (d)")
			envs, moves := step("d", envs)
			Expect(moves).To(Equal([]dqTagMove{
				{Branch: dqDev, Tag: dqTagDev, From: "", To: "d"},
				{Branch: dqTest, Tag: dqTagTest, From: "", To: "c"},
				{Branch: dqProd, Tag: dqTagProd, From: "", To: "c"},
			}))

			By("dev promotes d and its checks start running: nothing moves downstream")
			Expect(gatePhase("d", envs, 0, "d")).To(Equal(success))
			promote(envs, 0, "d", pending)
			envs, moves = step("d", envs)
			Expect(moves).To(BeEmpty())

			By("dev's checks pass on d: test's tag advances to d")
			promote(envs, 0, "d", success)
			envs, moves = step("d", envs)
			Expect(moves).To(Equal([]dqTagMove{{Branch: dqTest, Tag: dqTagTest, From: "c", To: "d"}}))

			By("e and f land before test finishes d: dev's tag follows HEAD, test keeps its candidate")
			envs, moves = step("f", envs)
			Expect(moves).To(Equal([]dqTagMove{{Branch: dqDev, Tag: dqTagDev, From: "d", To: "f"}}))
			Expect(gatePhase("f", envs, 1, "d")).To(Equal(success), "d is still promotable to test")

			By("test promotes d and passes: prod's tag advances to d even though HEAD is f")
			promote(envs, 1, "d", success)
			envs, moves = step("f", envs)
			Expect(moves).To(Equal([]dqTagMove{{Branch: dqProd, Tag: dqTagProd, From: "c", To: "d"}}))
			Expect(gatePhase("f", envs, 2, "d")).To(Equal(success))

			By("dev promotes f but its checks fail: prod's candidate is unaffected")
			promote(envs, 0, "f", failure)
			envs, moves = step("f", envs)
			Expect(moves).To(BeEmpty())
			Expect(gatePhase("f", envs, 2, "d")).To(Equal(success))

			By("dev recovers on f: test's tag skips e and goes straight to f")
			promote(envs, 0, "f", success)
			envs, moves = step("f", envs)
			Expect(moves).To(Equal([]dqTagMove{{Branch: dqTest, Tag: dqTagTest, From: "d", To: "f"}}))
			Expect(envs[2].TagSha).To(Equal("d"))
		})

		It("lets a person hold prod back and release a commit the upstream skipped", func() {
			envs := []dqEnvironment{
				dqSettled(dqDev, dqTagDev, "f", "f", success),
				dqSettled(dqTest, dqTagTest, "f", "f", success),
				dqSettled(dqProd, dqTagProd, "b", "b", success),
			}
			envs[1].DependsOn = []string{dqDev}
			envs[2].DependsOn = []string{dqTest}
			envs[2].Manual = true

			envs, moves := step("f", envs)
			Expect(moves).To(BeEmpty(), "prod's tag is not the controller's to move")

			By("tagging e, which test never ran itself but f contains")
			envs[2].TagSha = "e"
			Expect(gatePhase("f", envs, 2, "b")).To(Equal(pending), "the old proposed commit cannot merge after the tag moved")
			Expect(gatePhase("f", envs, 2, "e")).To(Equal(success))
		})

		It("stops a rollback in an upstream from being promoted over downstream", func() {
			envs := []dqEnvironment{
				dqSettled(dqDev, dqTagDev, "e", "e", success),
				dqSettled(dqTest, dqTagTest, "c", "c", success),
			}
			envs[1].DependsOn = []string{dqDev}
			envs, moves := step("e", envs)
			Expect(moves).To(Equal([]dqTagMove{{Branch: dqTest, Tag: dqTagTest, From: "c", To: "e"}}))
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

	Describe("dqNeedsNudge: when to wake an environment's ChangeTransferPolicy", func() {
		// SCMs send no webhook for a pushed git note, so a no-op hydration of a moved tag is invisible until
		// the ChangeTransferPolicy reconciles.
		It("is true while the hydrator has not reported the tagged commit", func() {
			Expect(dqNeedsNudge(dqProposing(dqDev, dqTagDev, "f", "c", "e"))).To(BeTrue())
			Expect(dqNeedsNudge(dqSettled(dqDev, dqTagDev, "f", "e"))).To(BeTrue())
		})

		It("is false once the hydrator has reported the tagged commit", func() {
			Expect(dqNeedsNudge(dqProposing(dqDev, dqTagDev, "f", "c", "f"))).To(BeFalse())
			env := dqSettled(dqDev, dqTagDev, "f", "e")
			env.Status = dqWithNote(env.Status, "f")
			Expect(dqNeedsNudge(env)).To(BeFalse())
		})

		It("is false while the tag does not exist", func() {
			Expect(dqNeedsNudge(dqSettled(dqDev, dqTagDev, "", "e"))).To(BeFalse())
		})
	})

	Describe("dqBackoff: pacing ChangeTransferPolicy nudges", func() {
		var (
			backoff *dqBackoff
			start   time.Time
		)

		BeforeEach(func() {
			backoff = newDQBackoff(15*time.Second, 2*time.Minute)
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

	Describe("dqValidateEnvironments: spec.environments must cover the PromotionStrategy", func() {
		specEnvs := func(branches ...string) []promoterv1alpha1.DependentsQualifiedEnvironment {
			out := make([]promoterv1alpha1.DependentsQualifiedEnvironment, 0, len(branches))
			for _, branch := range branches {
				out = append(out, promoterv1alpha1.DependentsQualifiedEnvironment{Branch: branch, Tag: "env/" + branch})
			}
			return out
		}

		It("accepts one entry per PromotionStrategy environment in any order", func() {
			Expect(dqValidateEnvironments(specEnvs(dqProd, dqDev, dqTest), []string{dqDev, dqTest, dqProd})).To(Succeed())
		})

		It("rejects a PromotionStrategy environment with no tag", func() {
			err := dqValidateEnvironments(specEnvs(dqDev, dqTest), []string{dqDev, dqTest, dqProd})
			Expect(err).To(MatchError(ContainSubstring(`spec.environments has no entry for PromotionStrategy environment "environment/prod"`)))
		})

		It("rejects an entry for a branch the PromotionStrategy does not have", func() {
			err := dqValidateEnvironments(specEnvs(dqDev, dqTest, dqProd, "environment/extra"), []string{dqDev, dqTest, dqProd})
			Expect(err).To(MatchError(ContainSubstring(`spec.environments lists "environment/extra", which is not an environment of the PromotionStrategy`)))
		})
	})

	Describe("dqNoteKey: the qualification note entry an environment owns", func() {
		It("is the branch for an environment that owns its active branch", func() {
			Expect(dqNoteKey(dqProd, "")).To(Equal("environment/prod"))
		})

		It("includes the activePath so environments sharing an active branch do not overwrite each other", func() {
			Expect(dqNoteKey("environments", "apps/one")).To(Equal("environments:apps/one"))
			Expect(dqNoteKey("environments", "apps/one")).NotTo(Equal(dqNoteKey("environments", "apps/two")))
		})
	})

	Describe("dqRestored: whether the live commit was put there by a RestoreActiveCommit", func() {
		restore := func(branch, activeSha string) promoterv1alpha1.RestoreActiveCommit {
			return promoterv1alpha1.RestoreActiveCommit{
				Spec:   promoterv1alpha1.RestoreActiveCommitSpec{Branch: branch},
				Status: promoterv1alpha1.RestoreActiveCommitStatus{ActiveSha: activeSha},
			}
		}

		It("is true when a restore for the branch produced the active commit", func() {
			racs := []promoterv1alpha1.RestoreActiveCommit{restore(dqDev, "aaa"), restore(dqTest, "bbb")}
			Expect(dqRestored(racs, dqTest, "bbb")).To(BeTrue())
		})

		It("is false when the environment has been promoted past the restore commit", func() {
			Expect(dqRestored([]promoterv1alpha1.RestoreActiveCommit{restore(dqTest, "bbb")}, dqTest, "ccc")).To(BeFalse())
		})

		It("is false for another environment's restore", func() {
			Expect(dqRestored([]promoterv1alpha1.RestoreActiveCommit{restore(dqDev, "bbb")}, dqTest, "bbb")).To(BeFalse())
		})

		It("is false for a restore that has not run yet", func() {
			Expect(dqRestored([]promoterv1alpha1.RestoreActiveCommit{restore(dqTest, "")}, dqTest, "")).To(BeFalse())
		})
	})
})

// dqRemoteTagSha returns the commit a tag points at on the test git server, or "" when the tag does not exist.
func dqRemoteTagSha(ctx context.Context, gitPath, tag string) (string, error) {
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

// dqPushTag points a tag at sha on the test git server, as a person with push access would.
func dqPushTag(ctx context.Context, gitPath, tag, sha string) {
	GinkgoHelper()
	_, err := runGitCmd(ctx, gitPath, "fetch", "origin")
	Expect(err).NotTo(HaveOccurred())
	_, err = runGitCmd(ctx, gitPath, "push", "--force", "origin", sha+":refs/tags/"+tag)
	Expect(err).NotTo(HaveOccurred())
}

// dqDefaultBranch returns the test repository's DRY branch name.
func dqDefaultBranch(ctx context.Context, gitPath string) string {
	GinkgoHelper()
	out, err := runGitCmd(ctx, gitPath, "rev-parse", "--abbrev-ref", "origin/HEAD")
	Expect(err).NotTo(HaveOccurred())
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "origin/"))
}

var _ = Describe("DependentsQualifiedCommitStatus Controller", func() {
	const (
		tagDevelopment = "env/development"
		tagStaging     = "env/staging"
		tagProduction  = "env/production"
	)

	// newGate builds a gate for the three test environments of a PromotionStrategy.
	newGate := func(name, dryBranch string) *promoterv1alpha1.DependentsQualifiedCommitStatus {
		return &promoterv1alpha1.DependentsQualifiedCommitStatus{
			Name:      name,
			Namespace: "default",
			Spec: promoterv1alpha1.DependentsQualifiedCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
				Key:                  promoterv1alpha1.DependentsQualifiedCommitStatusKey,
				DrySource:            promoterv1alpha1.DependentsQualifiedDrySource{Branch: dryBranch},
				Environments: []promoterv1alpha1.DependentsQualifiedEnvironment{
					{Branch: testBranchDevelopment, Tag: tagDevelopment},
					{Branch: testBranchStaging, Tag: tagStaging},
					{Branch: testBranchProduction, Tag: tagProduction},
				},
			},
		}
	}

	readyCondition := func(ctx context.Context, g Gomega, gate *promoterv1alpha1.DependentsQualifiedCommitStatus) *metav1.Condition {
		updated := &promoterv1alpha1.DependentsQualifiedCommitStatus{}
		g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gate), updated)).To(Succeed())
		condition := meta.FindStatusCondition(updated.Status.Conditions, string(promoterConditions.Ready))
		g.Expect(condition).NotTo(BeNil())
		g.Expect(condition.ObservedGeneration).To(Equal(updated.Generation))
		return condition
	}

	Context("When unmarshalling the test data", func() {
		It("should unmarshal the DependentsQualifiedCommitStatus resource", func() {
			err := unmarshalYamlStrict(testDependentsQualifiedCommitStatusYAML, &promoterv1alpha1.DependentsQualifiedCommitStatus{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("When validating the spec", func() {
		var ctx context.Context

		BeforeEach(func() {
			ctx = context.Background()
		})

		It("should reject two environments that share a tag", func() {
			gate := newGate("dq-duplicate-tag", "main")
			gate.Spec.Environments[2].Tag = tagStaging
			err := k8sClient.Create(ctx, gate)
			Expect(err).To(MatchError(ContainSubstring("each environment must have its own tag")))
		})

		It("should reject an unknown advance mode", func() {
			gate := newGate("dq-bad-advance", "main")
			gate.Spec.Environments[2].Advance = "Sometimes"
			err := k8sClient.Create(ctx, gate)
			Expect(err).To(MatchError(ContainSubstring("Unsupported value")))
		})

		It("should reject a tag that is not a valid ref name", func() {
			gate := newGate("dq-bad-tag", "main")
			gate.Spec.Environments[0].Tag = "-env..dev"
			err := k8sClient.Create(ctx, gate)
			Expect(err).To(HaveOccurred())
		})

		It("should default advance to Auto", func() {
			gate := newGate("dq-default-advance", "main")
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, gate) })
			Expect(gate.Spec.Environments[0].Advance).To(Equal(promoterv1alpha1.TagAdvanceAuto))
		})
	})

	Context("When the PromotionStrategy is missing", func() {
		It("should set Ready=False when the PromotionStrategy is not found", func() {
			ctx := context.Background()
			gate := newGate("dq-missing-ps", "main")
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
			gate              *promoterv1alpha1.DependentsQualifiedCommitStatus
			gitPath           string
			dryBranch         string
		)

		// environmentStatus returns the gate's status entry for a branch, failing the enclosing Eventually
		// while the entry does not exist.
		environmentStatus := func(g Gomega, branch string) promoterv1alpha1.DependentsQualifiedCommitStatusEnvironmentStatus {
			updated := &promoterv1alpha1.DependentsQualifiedCommitStatus{}
			g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gate), updated)).To(Succeed())
			idx := slices.IndexFunc(updated.Status.Environments, func(e promoterv1alpha1.DependentsQualifiedCommitStatusEnvironmentStatus) bool {
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
			sha, err := dqRemoteTagSha(ctx, gitPath, tag)
			g.Expect(err).NotTo(HaveOccurred())
			return sha
		}
		// hydrate plays the hydrator: it hydrates an environment's proposed branch from the commit its tag
		// points at.
		hydrate := func(proposedBranch, tag string) {
			GinkgoHelper()
			sha, err := dqRemoteTagSha(ctx, gitPath, tag)
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

		BeforeEach(func() {
			ctx = context.Background()
			gate = nil

			By("Setting up a test git repository and a PromotionStrategy ordered by a DependentsQualifiedCommitStatus")
			name, scmSecret, scmProvider, gitRepo, _, _, promotionStrategy = promotionStrategyResource(ctx, "dependents-qualified-commit-status-controller-test", "default")
			promotionStrategy.Spec.ProposedCommitStatuses = nil
			promotionStrategy.Spec.OrderCommitStatusRef = promoterv1alpha1.OrderCommitStatusRef{
				Group: promoterv1alpha1.SchemeGroupVersion.Group,
				Kind:  promoterv1alpha1.DependentsQualifiedOrderCommitStatusKind,
				Name:  name,
			}
			setupInitialTestGitRepoOnServer(ctx, gitRepo)

			var err error
			gitPath, err = cloneTestRepo(ctx, gitRepo)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { _ = os.RemoveAll(gitPath) })
			dryBranch = dqDefaultBranch(ctx, gitPath)

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

		It("should set Ready=False when spec.environments does not cover the PromotionStrategy", func() {
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			gate.Spec.Environments = gate.Spec.Environments[:2]
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())

			Eventually(func(g Gomega) {
				condition := readyCondition(ctx, g, gate)
				g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(condition.Message).To(ContainSubstring(`spec.environments has no entry for PromotionStrategy environment "environment/production"`))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Not creating any tag for a gate that is misconfigured")
			Consistently(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(BeEmpty())
			}, 2*time.Second, 500*time.Millisecond).Should(Succeed())
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
			Expect(dqRemoteTagSha(ctx, gitPath, tagDevelopment)).To(BeEmpty())
		})

		It("should set Ready=False with reason TagPushFailed when a tag cannot be pushed, and still manage the others", func() {
			neverHydrated()

			By("Occupying the ref namespace the development tag needs")
			// refs/tags/blocker exists, so refs/tags/blocker/development cannot be created.
			dqPushTag(ctx, gitPath, "blocker", headSha())

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
			head := headSha()
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagStaging)).To(Equal(head))
				g.Expect(tagSha(g, tagProduction)).To(Equal(head))
			}, constants.EventuallyTimeout).Should(Succeed())
		})

		It("should advance tags and gate promotions as environments qualify commits", func() {
			const healthKey = "dq-health"
			neverHydrated()

			By("Requiring an active commit status on development")
			promotionStrategy.Spec.Environments[0].ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{{Key: healthKey}}
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())

			By("Creating every missing tag at the DRY branch HEAD, since nothing is live yet")
			first := headSha()
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(first))
				g.Expect(tagSha(g, tagStaging)).To(Equal(first))
				g.Expect(tagSha(g, tagProduction)).To(Equal(first))
				g.Expect(readyCondition(ctx, g, gate).Status).To(Equal(metav1.ConditionTrue))

				updated := &promoterv1alpha1.DependentsQualifiedCommitStatus{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gate), updated)).To(Succeed())
				g.Expect(updated.Status.Dry).NotTo(BeNil())
				g.Expect(updated.Status.Dry.Sha).To(Equal(first))
				development := environmentStatus(g, testBranchDevelopment)
				g.Expect(development.Tag).NotTo(BeNil())
				g.Expect(development.Tag.Name).To(Equal(tagDevelopment))
				g.Expect(development.Tag.Sha).To(Equal(first))
				g.Expect(development.Qualified).To(BeNil())
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Hydrating every environment from its tag")
			hydrate(testBranchDevelopmentNext, tagDevelopment)
			hydrate(testBranchStagingNext, tagStaging)
			hydrate(testBranchProductionNext, tagProduction)

			By("Promoting development, a root, as soon as it is hydrated from its tag")
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchDevelopment)).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Holding staging until development qualifies the commit")
			Eventually(func(g Gomega) {
				staging := environmentStatus(g, testBranchStaging)
				g.Expect(staging.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				g.Expect(staging.Description).To(Equal(
					`Waiting for "environment/development" to qualify dry ` + first[:7] + ` (newest qualified: none)`))
				g.Expect(staging.Upstreams).To(HaveLen(1))
				g.Expect(staging.Upstreams[0].Satisfied).To(BeFalse())

				cs := &promoterv1alpha1.CommitStatus{}
				csName := utils.CommitStatusResourceName(ctx, gate, testBranchStaging)
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Namespace: "default", Name: csName}, cs)).To(Succeed())
				g.Expect(cs.Spec.Name).To(Equal(promoterv1alpha1.DependentsQualifiedCommitStatusKey))
				g.Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
				g.Expect(cs.Spec.Sha).To(Equal(staging.ReportedSha))
			}, constants.EventuallyTimeout).Should(Succeed())
			Consistently(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchStaging)).To(BeEmpty())
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

			By("Recording the qualification and promoting staging, then production")
			Eventually(func(g Gomega) {
				development := environmentStatus(g, testBranchDevelopment)
				g.Expect(development.Qualified).NotTo(BeNil())
				g.Expect(development.Qualified.DrySha).To(Equal(first))
				g.Expect(development.Qualified.HydratedSha).To(Equal(developmentActive))

				g.Expect(activeDrySha(g, testBranchStaging)).To(Equal(first))
				g.Expect(activeDrySha(g, testBranchProduction)).To(Equal(first))
				g.Expect(environmentStatus(g, testBranchStaging).Qualified.DrySha).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Pushing a new DRY commit")
			second, err := makeDryCommit(ctx, gitPath, "second release")
			Expect(err).NotTo(HaveOccurred())

			By("Moving only the root environment's tag")
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(second))
			}, constants.EventuallyTimeout).Should(Succeed())
			Expect(dqRemoteTagSha(ctx, gitPath, tagStaging)).To(Equal(first))
			Expect(dqRemoteTagSha(ctx, gitPath, tagProduction)).To(Equal(first))

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
			Expect(dqRemoteTagSha(ctx, gitPath, tagProduction)).To(Equal(first))

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
			promotionStrategy.Spec.Environments[0].ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{{Key: "dq-health"}}

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

			By("Creating every tag at the DRY branch HEAD, the hydrator needs them to write the branches")
			head := headSha()
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(head))
				g.Expect(tagSha(g, tagStaging)).To(Equal(head))
				g.Expect(tagSha(g, tagProduction)).To(Equal(head))
				g.Expect(environmentStatus(g, testBranchProduction).Qualified).To(BeNil())
			}, constants.EventuallyTimeout).Should(Succeed())
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
			gate.Spec.Environments[2].Advance = promoterv1alpha1.TagAdvanceManual
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

		It("should leave an advance: Manual tag to a person and verify what they tag", func() {
			neverHydrated()
			Expect(k8sClient.Create(ctx, promotionStrategy)).To(Succeed())
			gate = newGate(name, dryBranch)
			gate.Spec.Environments[2].Advance = promoterv1alpha1.TagAdvanceManual
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())

			By("Creating the missing tags, including the manual one")
			first := headSha()
			Eventually(func(g Gomega) {
				g.Expect(tagSha(g, tagDevelopment)).To(Equal(first))
				g.Expect(tagSha(g, tagStaging)).To(Equal(first))
				g.Expect(tagSha(g, tagProduction)).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Promoting the first commit everywhere")
			hydrate(testBranchDevelopmentNext, tagDevelopment)
			hydrate(testBranchStagingNext, tagStaging)
			hydrate(testBranchProductionNext, tagProduction)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchDevelopment)).To(Equal(first))
				g.Expect(activeDrySha(g, testBranchStaging)).To(Equal(first))
				g.Expect(activeDrySha(g, testBranchProduction)).To(Equal(first))
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
			dqPushTag(ctx, gitPath, tagProduction, second)
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
			dqPushTag(ctx, gitPath, tagProduction, first)
			hydrate(testBranchProductionNext, tagProduction)
			Eventually(func(g Gomega) {
				g.Expect(activeDrySha(g, testBranchProduction)).To(Equal(first))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})
