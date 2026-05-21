package predicate_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestInternalPredicate(t *testing.T) {
	t.Parallel()

	RegisterFailHandler(Fail)

	c, _ := GinkgoConfiguration()

	RunSpecs(t, "Internal Predicate Suite", c)
}

var _ = BeforeSuite(func() {
})

var _ = AfterSuite(func() {
})
