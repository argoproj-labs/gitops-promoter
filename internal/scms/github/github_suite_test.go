package github_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGithub(t *testing.T) {
	t.Parallel()

	RegisterFailHandler(Fail)
	c, _ := GinkgoConfiguration()
	RunSpecs(t, "Github Suite", c)
}
