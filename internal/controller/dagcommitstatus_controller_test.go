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

	Describe("upstreamsPending", func() {
		const (
			oldDry = "old-dry-sha"
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

		// prd is promoting newDry, but its upstream stg is still on oldDry (healthy from a prior
		// round) and has NOT taken newDry. prd must stay pending — otherwise the new change merges
		// ahead of its upstream, breaking DAG ordering.
		It("holds pending when an upstream is healthy on an OLD dry and has not promoted the target", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatus("stg", oldDry, newDry, true, old),
			}
			pending, _ := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
		})

		It("holds pending when the upstream's hydrator has not yet processed the target dry", func() {
			// stg's hydrator is still on oldDry (Proposed.Dry = oldDry), so it has not even
			// produced the target dry yet.
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatus("stg", oldDry, oldDry, true, old),
			}
			pending, _ := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
		})

		It("is ready when the upstream merged the target dry and is healthy", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatus("stg", newDry, newDry, true, newer),
			}
			pending, _ := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeFalse())
		})

		It("holds pending when the upstream merged the target dry but is not healthy", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"stg": dagEnvStatus("stg", newDry, newDry, false, newer),
			}
			pending, _ := upstreamsPending(linear(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
		})

		It("is ready for a root that has no upstreams", func() {
			pending, _ := upstreamsPending(linear(), "dev", newDry, metav1.NewTime(newer), map[string]promoterv1alpha1.EnvironmentStatus{})
			Expect(pending).To(BeFalse())
		})

		It("fan-in: holds pending when one upstream has not promoted the target", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"e2e":  dagEnvStatus("e2e", newDry, newDry, true, newer),
				"perf": dagEnvStatus("perf", oldDry, newDry, true, old),
			}
			pending, _ := upstreamsPending(diamond(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeTrue())
		})

		It("fan-in: is ready when both upstreams merged the target dry and are healthy", func() {
			status := map[string]promoterv1alpha1.EnvironmentStatus{
				"e2e":  dagEnvStatus("e2e", newDry, newDry, true, newer),
				"perf": dagEnvStatus("perf", newDry, newDry, true, newer),
			}
			pending, _ := upstreamsPending(diamond(), "prd", newDry, metav1.NewTime(newer), status)
			Expect(pending).To(BeFalse())
		})
	})
})
