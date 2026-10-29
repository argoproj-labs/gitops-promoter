package bitbucket_datacenter_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestBitbucketDataCenter(t *testing.T) {
	t.Parallel()

	RegisterFailHandler(Fail)
	c, _ := GinkgoConfiguration()
	RunSpecs(t, "Bitbucket DataCenter Suite", c)
}
