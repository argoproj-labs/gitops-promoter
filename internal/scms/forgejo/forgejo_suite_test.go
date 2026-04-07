package forgejo_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestForgejo(t *testing.T) {
	t.Parallel()

	RegisterFailHandler(Fail)
	c, _ := GinkgoConfiguration()
	RunSpecs(t, "Forgejo Suite", c)
}
