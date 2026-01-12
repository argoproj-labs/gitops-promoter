package webhooks_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWebhooks(t *testing.T) {
	t.Parallel()

	RegisterFailHandler(Fail)

	c, _ := GinkgoConfiguration()

	RunSpecs(t, "Webhooks Suite", c)
}
