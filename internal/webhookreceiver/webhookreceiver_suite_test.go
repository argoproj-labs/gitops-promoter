package webhookreceiver

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWebhookReceiver(t *testing.T) {
	t.Parallel()

	RegisterFailHandler(Fail)

	c, _ := GinkgoConfiguration()

	RunSpecs(t, "WebhookReceiver Suite", c)
}
