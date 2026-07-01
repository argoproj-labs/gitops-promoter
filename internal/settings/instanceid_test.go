package settings_test

import (
	"testing"

	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestSettings(t *testing.T) {
	t.Parallel()

	RegisterFailHandler(Fail)
	RunSpecs(t, "Settings Suite")
}

var _ = DescribeTable("InstanceIDsEqual",
	func(a, b *string, want bool) {
		Expect(settings.InstanceIDsEqual(a, b)).To(Equal(want))
	},
	Entry("both nil", (*string)(nil), (*string)(nil), true),
	Entry("nil and empty string", (*string)(nil), ptr(""), false),
	Entry("empty string and nil", ptr(""), (*string)(nil), false),
	Entry("both empty string", ptr(""), ptr(""), true),
	Entry("same non-empty", ptr("wave-0"), ptr("wave-0"), true),
	Entry("nil and non-empty", (*string)(nil), ptr("wave-0"), false),
	Entry("non-empty and nil", ptr("wave-0"), (*string)(nil), false),
	Entry("different non-empty", ptr("wave-0"), ptr("wave-1"), false),
)

func ptr(s string) *string {
	return &s
}
