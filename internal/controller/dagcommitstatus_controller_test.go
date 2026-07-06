/*
Copyright 2024.

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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// dagEnvs builds a DAGEnvironment slice from alternating (branch, dependsOn) pairs so tests can
// declare a graph compactly. dependsOn is a comma-joined string, empty for a graph root.
func dagEnvs(pairs ...string) []promoterv1alpha1.DAGEnvironment {
	out := make([]promoterv1alpha1.DAGEnvironment, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		var dependsOn []string
		if pairs[i+1] != "" {
			dependsOn = strings.Split(pairs[i+1], ",")
		}
		out = append(out, promoterv1alpha1.DAGEnvironment{Branch: pairs[i], DependsOn: dependsOn})
	}
	return out
}

// dagEnvStatus builds an EnvironmentStatus for the upstreamsPending tests. activeDry is the dry SHA
// the branch has merged and deployed; hydratedDry is the dry SHA its hydrator has processed
// (Proposed.Dry.Sha); healthy toggles the active argocd-health commit status.
func dagEnvStatus(branch, activeDry, hydratedDry string, healthy bool, commitTime time.Time) promoterv1alpha1.EnvironmentStatus {
	phase := "success"
	if !healthy {
		phase = "pending"
	}
	return promoterv1alpha1.EnvironmentStatus{
		Branch: branch,
		Active: promoterv1alpha1.CommitBranchState{
			Dry: promoterv1alpha1.CommitShaState{Sha: activeDry, CommitTime: metav1.NewTime(commitTime)},
			CommitStatuses: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
				{Key: "argocd-health", Phase: phase},
			},
		},
		Proposed: promoterv1alpha1.CommitBranchState{
			Dry: promoterv1alpha1.CommitShaState{Sha: hydratedDry},
		},
	}
}

// dagEnvStatusWithNote is like dagEnvStatus but also sets the hydrator git note. The note dry SHA
// is what getEffectiveHydratedDrySha treats as the branch's effective hydrated dry, so when it
// differs from Proposed.Dry.Sha the branch is a no-op for that SHA (the note advanced without a new
// hydrated commit). This is required to exercise upstreamPending's no-op recursion, which the
// note-less dagEnvStatus cannot reach.
//
//nolint:unparam // branch is always "stg" in current tests but kept for consistency with dagEnvStatus
func dagEnvStatusWithNote(branch, activeDry, proposedDry, noteDry string, healthy bool, commitTime time.Time) promoterv1alpha1.EnvironmentStatus {
	envStatus := dagEnvStatus(branch, activeDry, proposedDry, healthy, commitTime)
	if noteDry != "" {
		envStatus.Proposed.Note = &promoterv1alpha1.HydratorMetadata{DrySha: noteDry}
	}
	return envStatus
}

var _ = Describe("DAG graph logic", func() {
	Describe("buildDAG", func() {
		It("builds a graph preserving spec order", func() {
			g, err := buildDAG(dagEnvs("dev", "", "stg", "dev", "prd", "stg"))
			Expect(err).NotTo(HaveOccurred())
			Expect(g.branches).To(Equal([]string{"dev", "stg", "prd"}))
			Expect(g.dependsOn["stg"]).To(Equal([]string{"dev"}))
			Expect(g.dependsOn["dev"]).To(BeEmpty())
		})

		It("rejects a duplicate branch", func() {
			_, err := buildDAG(dagEnvs("dev", "", "dev", ""))
			Expect(err).To(MatchError(ContainSubstring("duplicate branch")))
		})
	})

	Describe("validateDAG", func() {
		It("accepts dependsOn that reference declared branches", func() {
			g, err := buildDAG(dagEnvs("dev", "", "prd", "dev"))
			Expect(err).NotTo(HaveOccurred())
			Expect(g.validateDAG()).To(Succeed())
		})

		It("rejects dependsOn referencing an unknown branch", func() {
			g, err := buildDAG(dagEnvs("dev", "", "prd", "stg"))
			Expect(err).NotTo(HaveOccurred())
			Expect(g.validateDAG()).To(MatchError(ContainSubstring("unknown branch")))
		})
	})

	Describe("topologicalSort", func() {
		It("orders a linear chain", func() {
			g, _ := buildDAG(dagEnvs("dev", "", "stg", "dev", "prd", "stg"))
			order, err := g.topologicalSort()
			Expect(err).NotTo(HaveOccurred())
			Expect(order).To(Equal([]string{"dev", "stg", "prd"}))
		})

		It("orders a diamond so every upstream precedes its downstream", func() {
			g, _ := buildDAG(dagEnvs("dev", "", "stg-us", "dev", "stg-eu", "dev", "prd", "stg-us,stg-eu"))
			order, err := g.topologicalSort()
			Expect(err).NotTo(HaveOccurred())
			Expect(order).To(Equal([]string{"dev", "stg-us", "stg-eu", "prd"}))
		})

		It("detects a cycle between two branches", func() {
			g, _ := buildDAG(dagEnvs("a", "b", "b", "a"))
			_, err := g.topologicalSort()
			Expect(err).To(MatchError(ContainSubstring("cycle")))
		})

		It("detects a self-dependency cycle", func() {
			g, _ := buildDAG(dagEnvs("a", "a"))
			_, err := g.topologicalSort()
			Expect(err).To(MatchError(ContainSubstring("cycle")))
		})
	})

	// upstreamsPending runs the following truth table against every dependsOn upstream (a fan-in
	// passes only when all upstreams are satisfied; a linear chain is the single-upstream case). The
	// logic is a direct port of the PreviousEnvironmentCommitStatus controller's linear
	// isPreviousEnvironmentPending, generalized to a DAG.
	//
	// Truth table for upstreamPending (per upstream):
	// | Hydrated | NoOp | Pending | Merged | Healthy | Result |
	// |----------|------|---------|--------|---------|--------|
	// | N        | -    | -       | -      | -       | BLOCK (hydrator) |
	// | Y        | N    | -       | N      | -       | BLOCK (waiting for promotion) |
	// | Y        | N    | -       | Y      | N       | BLOCK (commit status) |
	// | Y        | N    | -       | Y      | Y       | ALLOW |
	// | Y        | Y    | Y       | -      | -       | BLOCK (pending changes from previous commit) |
	// | Y        | Y    | N       | -      | N       | BLOCK (commit status on no-op env) |
	// | Y        | Y    | N       | -      | Y       | RECURSE (or ALLOW if base case) |
	//
	// Each It below is annotated with the Case it covers.
	Describe("upstreamsPending", func() {
		const (
			oldDry = "old-dry-sha"
			midDry = "mid-dry-sha"
			newDry = "new-dry-sha"
		)
		old := time.Date(2026, 6, 30, 10, 0, 0, 0, time.UTC)
		newer := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)

		// linear chain: dev -> stg -> prd
		linear := func() *dag { g, _ := buildDAG(dagEnvs("dev", "", "stg", "dev", "prd", "stg")); return g }
		// diamond: dev -> {e2e, perf} -> prd
		diamond := func() *dag {
			g, _ := buildDAG(dagEnvs("dev", "", "e2e", "dev", "perf", "dev", "prd", "e2e,perf"))
			return g
		}

		// Case 2 (hydrated, not no-op, not merged): prd is promoting newDry, but its upstream stg is
		// still on oldDry (healthy from a prior round) and has NOT taken newDry. prd must stay
		// pending — otherwise the new change merges ahead of its upstream, breaking DAG ordering.
		It("holds pending when an upstream is healthy on an OLD dry and has not promoted the target", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatus("stg", oldDry, newDry, true, old),
			}
			pending, _ := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
		})

		// Case 1 (not hydrated): stg's hydrator is still on oldDry (Proposed.Dry = oldDry), so it
		// has not even produced the target dry yet.
		It("holds pending when the upstream's hydrator has not yet processed the target dry", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatus("stg", oldDry, oldDry, true, old),
			}
			pending, _ := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
		})

		// Case 4 (hydrated, not no-op, merged, healthy): the ALLOW row.
		It("is ready when the upstream merged the target dry and is healthy", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatus("stg", newDry, newDry, true, newer),
			}
			pending, _ := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeFalse())
		})

		// Case 3 (hydrated, not no-op, merged, not healthy): BLOCK on commit status.
		It("holds pending when the upstream merged the target dry but is not healthy", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatus("stg", newDry, newDry, false, newer),
			}
			pending, _ := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
		})

		// Case 7 base case (RECURSE bottoms out at a graph root): a node with no upstreams is
		// always ready.
		It("is ready for a root that has no upstreams", func() {
			pending, _ := upstreamsPending(linear(), "dev", newDry, metav1.NewTime(newer), map[string]promoterv1alpha1.EnvironmentStatus{})
			Expect(pending).To(BeFalse())
		})

		// Fan-in (DAG generalization): with multiple upstreams, any one
		// unsatisfied upstream blocks — here perf has not promoted the target (Case 2 per-upstream).
		It("fan-in: holds pending when one upstream has not promoted the target", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"e2e":  dagEnvStatus("e2e", newDry, newDry, true, newer),
				"perf": dagEnvStatus("perf", oldDry, newDry, true, old),
			}
			pending, _ := upstreamsPending(diamond(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
		})

		// Fan-in (DAG generalization): all upstreams satisfied (Case 4 each) → the whole fan-in
		// passes.
		It("fan-in: is ready when both upstreams merged the target dry and are healthy", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"e2e":  dagEnvStatus("e2e", newDry, newDry, true, newer),
				"perf": dagEnvStatus("perf", newDry, newDry, true, newer),
			}
			pending, _ := upstreamsPending(diamond(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeFalse())
		})

		// Fan-in (DAG generalization): the fan-in cases above only assert the boolean. Verify the
		// pending reason names the upstream that is actually blocking, so users can see which one to
		// look at.
		It("fan-in: pending reason names the upstream that has not promoted the target", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"e2e":  dagEnvStatus("e2e", newDry, newDry, true, newer),
				"perf": dagEnvStatus("perf", oldDry, newDry, true, old),
			}
			pending, reason := upstreamsPending(diamond(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
			Expect(reason).To(Equal("Waiting for previous environment to be promoted"))
		})

		// Case 7 (hydrated, no-op, no pending changes, healthy → RECURSE): a clean, healthy no-op
		// upstream (its git note advanced to the target dry without a new hydrated commit) must be
		// transparently skipped by recursing into its own upstreams. Here stg is a healthy no-op for
		// newDry and its upstream dev has merged newDry and is healthy, so prd is ready.
		It("no-op recursion: ready when a healthy no-op upstream's own upstream is ready", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				// stg: note advanced to newDry, but active == proposed == oldDry (no new commit).
				"stg": dagEnvStatusWithNote("stg", oldDry, oldDry, newDry, true, old),
				"dev": dagEnvStatus("dev", newDry, newDry, true, newer),
			}
			pending, _ := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeFalse())
		})

		// Case 7 (RECURSE), negative side: recursion through a healthy no-op still blocks when the
		// deeper upstream is not satisfied — stg is a healthy no-op, but dev has not promoted newDry
		// yet. This proves the recursion actually happens (see the short-circuit reverse-test).
		It("no-op recursion: pending when a healthy no-op upstream's own upstream is not ready", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatusWithNote("stg", oldDry, oldDry, newDry, true, old),
				"dev": dagEnvStatus("dev", oldDry, newDry, true, old),
			}
			pending, _ := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
		})

		// Case 6 (hydrated, no-op, no pending changes, not healthy → BLOCK): a no-op upstream is
		// only skippable if it is itself healthy. An unhealthy no-op blocks (no recursion) even
		// though it carries no real change of its own — this is the scenario that previously caused
		// premature promotions.
		It("no-op recursion: pending when the no-op upstream itself is unhealthy", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatusWithNote("stg", oldDry, oldDry, newDry, false, old),
				"dev": dagEnvStatus("dev", newDry, newDry, true, newer),
			}
			pending, reason := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
			Expect(reason).To(ContainSubstring("argocd-health"))
		})

		// Case 5 (hydrated, no-op for the target, but has its own pending change → BLOCK): a previous
		// commit (midDry) changed stg and its PR is not yet merged (active still oldDry), while the
		// target (newDry) is a no-op for stg (its note advanced to newDry without a new commit). stg
		// is a no-op for the target yet still has an in-flight change of its own, so it must block
		// rather than be recursed past. Mirrors the old isPreviousEnvironmentPending Case 5 test
		// (active=OLD, proposed=COMMIT1, note=COMMIT2).
		It("no-op recursion: pending when a no-op upstream has its own pending change", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				// stg: active = oldDry (PR from the previous change not merged), proposed = midDry
				// (that previous change), note = newDry = target (a no-op for stg). So it IS a no-op
				// (note != proposed) AND has a pending change (active != proposed).
				"stg": dagEnvStatusWithNote("stg", oldDry, midDry, newDry, true, old),
				"dev": dagEnvStatus("dev", newDry, newDry, true, newer),
			}
			pending, reason := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
			Expect(reason).To(ContainSubstring("Waiting for previous environment to be promoted"))
		})

		// Case 4, commit-time sub-check: an upstream that has merged the target dry but whose commit
		// is older than the current environment's active commit must block — otherwise the current
		// environment would promote ahead of an upstream that has not caught up in time ordering.
		It("commit-time ordering: pending when the upstream's merged commit is older than current", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatus("stg", newDry, newDry, true, old),
			}
			pending, reason := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
			Expect(reason).To(ContainSubstring("older"))
		})

		// Fan-in over unequal-length paths (DAG generalization, no linear-table equivalent):
		// dev -> fast -> prd (short path) and dev -> canary -> soak -> prd (long path). prd must
		// wait for the SLOW path: even when the short path (fast) is fully satisfied, an unsatisfied
		// node on the long path (soak) keeps prd pending.
		unevenDiamond := func() *dag {
			g, _ := buildDAG(dagEnvs(
				"dev", "",
				"fast", "dev",
				"canary", "dev",
				"soak", "canary",
				"prd", "fast,soak",
			))
			return g
		}
		It("uneven diamond: pending when the long path is not ready even though the short path is", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"fast": dagEnvStatus("fast", newDry, newDry, true, newer),
				"soak": dagEnvStatus("soak", oldDry, newDry, true, old),
			}
			pending, _ := upstreamsPending(unevenDiamond(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
		})
		It("uneven diamond: ready when both paths have promoted the target and are healthy", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"fast": dagEnvStatus("fast", newDry, newDry, true, newer),
				"soak": dagEnvStatus("soak", newDry, newDry, true, newer),
			}
			pending, _ := upstreamsPending(unevenDiamond(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeFalse())
		})
	})
})
