package scmserver_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

//nolint:paralleltest // the suite configures process-wide TLS trust (SSL_CERT_FILE, http.DefaultTransport).
func TestSCMServer(t *testing.T) {
	RegisterFailHandler(Fail)
	c, _ := GinkgoConfiguration()
	RunSpecs(t, "SCM Server Suite", c)
}
