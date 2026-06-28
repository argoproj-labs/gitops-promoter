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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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

func satisfiedSet(branches ...string) map[string]bool {
	m := make(map[string]bool, len(branches))
	for _, b := range branches {
		m[b] = true
	}
	return m
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

	Describe("eligibleEnvironments", func() {
		It("makes a root eligible when nothing is satisfied", func() {
			g, _ := buildDAG(dagEnvs("dev", "", "stg", "dev"))
			Expect(g.eligibleEnvironments(satisfiedSet())).To(Equal([]string{"dev"}))
		})

		It("fan-out: one satisfied upstream makes all its downstreams eligible", func() {
			g, _ := buildDAG(dagEnvs("dev", "", "stg-us", "dev", "stg-eu", "dev"))
			Expect(g.eligibleEnvironments(satisfiedSet("dev"))).To(Equal([]string{"stg-us", "stg-eu"}))
		})

		It("fan-in: downstream stays ineligible until ALL upstreams are satisfied", func() {
			g, _ := buildDAG(dagEnvs("stg-us", "", "stg-eu", "", "prd", "stg-us,stg-eu"))

			// Only one upstream satisfied: prd must NOT be eligible, but the other root is.
			eligible := g.eligibleEnvironments(satisfiedSet("stg-us"))
			Expect(eligible).NotTo(ContainElement("prd"))
			Expect(eligible).To(ContainElement("stg-eu"))

			// Both upstreams satisfied: prd becomes eligible.
			Expect(g.eligibleEnvironments(satisfiedSet("stg-us", "stg-eu"))).To(Equal([]string{"prd"}))
		})

		It("excludes branches that are already satisfied", func() {
			g, _ := buildDAG(dagEnvs("dev", "", "stg", "dev"))
			// dev satisfied -> stg now eligible; dev itself is not returned.
			Expect(g.eligibleEnvironments(satisfiedSet("dev"))).To(Equal([]string{"stg"}))
		})

		It("diamond: the join is eligible only after both middle environments are satisfied", func() {
			g, _ := buildDAG(dagEnvs("dev", "", "stg-us", "dev", "stg-eu", "dev", "prd", "stg-us,stg-eu"))

			// dev satisfied -> both middles eligible, prd not.
			Expect(g.eligibleEnvironments(satisfiedSet("dev"))).To(Equal([]string{"stg-us", "stg-eu"}))

			// dev + one middle satisfied -> prd still not eligible.
			Expect(g.eligibleEnvironments(satisfiedSet("dev", "stg-us"))).NotTo(ContainElement("prd"))

			// dev + both middles satisfied -> prd eligible.
			Expect(g.eligibleEnvironments(satisfiedSet("dev", "stg-us", "stg-eu"))).To(Equal([]string{"prd"}))
		})
	})
})
