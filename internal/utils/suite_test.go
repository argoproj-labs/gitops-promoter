package utils

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestInternalUtils(t *testing.T) {
	t.Parallel()

	RegisterFailHandler(Fail)

	c, _ := GinkgoConfiguration()

	RunSpecs(t, "Template Suite", c)
}

var _ = BeforeSuite(func() {
})

var _ = AfterSuite(func() {
})
