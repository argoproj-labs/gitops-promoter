package github

import (
	"crypto/tls"
	"net/http"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

//nolint:paralleltest // GetClient tests mutate package-level installation caches and must run serially.
func TestGithub(t *testing.T) {
	RegisterFailHandler(Fail)
	c, _ := GinkgoConfiguration()
	RunSpecs(t, "Github Suite", c)
}

var _ = BeforeSuite(func() {
	http.DefaultTransport = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // httptest TLS only
	}
})
