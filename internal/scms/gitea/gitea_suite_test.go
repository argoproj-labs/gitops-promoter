package gitea_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestGitea(t *testing.T) {
	t.Parallel()

	RegisterFailHandler(Fail)
	c, _ := GinkgoConfiguration()
	RunSpecs(t, "Gitea Suite", c)
}
