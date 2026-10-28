package main

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

	DescribeTable("ignoredByName",
		func(name string, want bool) {
			Expect(ignoredByName(name)).To(Equal(want))
		},
		Entry("ForTest suffix", "github.com/argoproj-labs/gitops-promoter/internal/settings.SetControllerInstanceIDForTest", true),
		Entry("ForTests suffix", "github.com/argoproj-labs/gitops-promoter/internal/controller.loadShippedControllerConfigurationForTests", true),
		Entry("ordinary name", "github.com/argoproj-labs/gitops-promoter/api/v1alpha1.Environment.GetAutoMerge", false),
	)

	DescribeTable("kindOf",
		func(reachable, called bool, want string) {
			Expect(kindOf(reachable, called)).To(Equal(want))
		},
		Entry("unreachable", false, false, "unreachable"),
		Entry("reflection-only", true, false, "reflection-only"),
		Entry("live", true, true, ""),
	)

	DescribeTable("skippedPackage",
		func(path string, skip bool) {
			Expect(skippedPackage(path)).To(Equal(skip))
		},
		Entry("test helpers", modulePath+"/test/scmserver", true),
		Entry("npm Go files", modulePath+"/ui/dashboard/node_modules/flatted/golang/pkg/flatted", true),
		Entry("scratch mains", modulePath+"/tmp/wrcscheck", true),
		Entry("internal packages", modulePath+"/internal/utils/httpauth", false),
		Entry("cmd", modulePath+"/cmd/demo", false),
	)

	DescribeTable("scratchPackage",
		func(path string, scratch bool) {
			Expect(scratchPackage(path)).To(Equal(scratch))
		},
		Entry("tmp main", modulePath+"/tmp/wrcscheck", true),
		Entry("simulator library", modulePath+"/webrequestsimulator", false),
	)

	DescribeTable("isLibraryEntry",
		func(name string, entry bool) {
			Expect(isLibraryEntry(name)).To(Equal(entry))
		},
		Entry("public Simulate", modulePath+"/webrequestsimulator.Simulate", true),
		Entry("internal Simulate", modulePath+"/webrequestsimulator/internal/simulate.Simulate", false),
	)

	Describe("packagePath", func() {
		It("splits the package path from the selector", func() {
			got := packagePath("github.com/argoproj-labs/gitops-promoter/internal/controller.ChangeTransferPolicyReconciler.Reconcile")
			Expect(got).To(Equal("github.com/argoproj-labs/gitops-promoter/internal/controller"))
			Expect(inModule(got)).To(BeTrue())
			Expect(inModule("golang.org/x/tools/go/ssa")).To(BeFalse())
		})
	})

	Describe("finding format", func() {
		It("marks an unused function with func:", func() {
			unused := finding{
				file: "api/v1alpha1/promotionstrategy_types.go",
				line: 144,
				col:  1,
				kind: "reflection-only",
				name: "github.com/argoproj-labs/gitops-promoter/api/v1alpha1.Environment.GetAutoMerge",
			}.String()
			Expect(unused).To(ContainSubstring("reflection-only func: "))
		})

		It("omits func: from an allowlist problem", func() {
			stale := finding{
				file: "hack/deadcode/allowlist.txt",
				line: 3,
				col:  1,
				kind: "stale allowlist entry",
				name: "pkg.Old",
			}.String()
			Expect(stale).NotTo(ContainSubstring(" func:"))
		})
	})
})
