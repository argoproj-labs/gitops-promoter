package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const modulePath = "github.com/argoproj-labs/gitops-promoter"

var (
	testSkips    = []string{"/test/", "/tmp/", "/node_modules/"}
	testSuffixes = []string{"ForTest", "ForTests"}
)

var _ = Describe("deadcode", func() {
	Describe("parseAllowlist", func() {
		It("records the entry and its line, skipping comments", func() {
			list, err := parseAllowlist(`
# comment

github.com/argoproj-labs/gitops-promoter/internal/settings.SetControllerInstanceIDForTest
`)
			Expect(err).NotTo(HaveOccurred())
			Expect(list.order).To(HaveLen(1))
			Expect(list.order[0].line).To(Equal(4))
		})

		It("rejects a duplicate entry", func() {
			_, err := parseAllowlist("pkg.A\npkg.A\n")
			Expect(err).To(MatchError(ContainSubstring("duplicate")))
		})
	})

	DescribeTable("ignoredSuffix",
		func(name string, want bool) {
			Expect(ignoredSuffix(name, testSuffixes)).To(Equal(want))
		},
		Entry("ForTest suffix", modulePath+"/internal/settings.SetControllerInstanceIDForTest", true),
		Entry("ForTests suffix", modulePath+"/internal/controller.loadShippedControllerConfigurationForTests", true),
		Entry("ordinary name", modulePath+"/api/v1alpha1.Environment.GetAutoMerge", false),
	)

	DescribeTable("kindOf",
		func(reachable, called bool, want string) {
			Expect(kindOf(reachable, called)).To(Equal(want))
		},
		Entry("unreachable", false, false, "unreachable"),
		Entry("reflection-only", true, false, "reflection-only"),
		Entry("live", true, true, ""),
	)

	DescribeTable("skippedPath",
		func(path string, skip bool) {
			Expect(skippedPath(path, testSkips)).To(Equal(skip))
		},
		Entry("test helpers", modulePath+"/test/scmserver", true),
		Entry("npm Go files", modulePath+"/ui/dashboard/node_modules/flatted/golang/pkg/flatted", true),
		Entry("scratch mains", modulePath+"/tmp/wrcscheck", true),
		Entry("internal packages", modulePath+"/internal/utils/httpauth", false),
		Entry("cmd", modulePath+"/cmd/demo", false),
		Entry("simulator library", modulePath+"/webrequestsimulator", false),
	)

	Describe("finding format", func() {
		It("marks an unused function with func:", func() {
			unused := formatDeadFunc(jsonPosition{
				File: "api/v1alpha1/promotionstrategy_types.go",
				Line: 144,
				Col:  1,
			}, "reflection-only", modulePath+"/api/v1alpha1.Environment.GetAutoMerge")
			Expect(unused).To(ContainSubstring("reflection-only func: "))
		})

		It("omits func: from an allowlist problem", func() {
			stale := formatAllowProblem("hack/deadcode/allowlist.txt", 3, "stale allowlist entry", "pkg.Old")
			Expect(stale).NotTo(ContainSubstring(" func:"))
		})
	})
})
