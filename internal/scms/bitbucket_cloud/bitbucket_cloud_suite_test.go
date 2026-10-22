package bitbucket_cloud

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

//nolint:paralleltest // specs swap http.DefaultTransport to trust httptest TLS servers and must run serially.
func TestBitbucketCloud(t *testing.T) {
	RegisterFailHandler(Fail)
	c, _ := GinkgoConfiguration()
	RunSpecs(t, "Bitbucket Cloud Suite", c)
}
