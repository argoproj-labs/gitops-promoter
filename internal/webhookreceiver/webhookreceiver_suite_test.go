package webhookreceiver_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestWebhookReceiver(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "WebhookReceiver Suite")
}
