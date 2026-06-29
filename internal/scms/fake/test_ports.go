package fake

import (
	"os"
	"strconv"

	ginkov2 "github.com/onsi/ginkgo/v2"

	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
)

// gitServerPort returns the in-process gitkit HTTP port for fake SCM git URLs.
// apicallmetrics parallel runs set PROMOTER_API_METRICS_GIT_PORT per subprocess;
// controller envtest suites use 5000 + GinkgoParallelProcess() when unset.
func gitServerPort() int {
	return portFromEnv("PROMOTER_API_METRICS_GIT_PORT", 5000+ginkov2.GinkgoParallelProcess())
}

// webhookReceiverPort returns the promoter webhook receiver port for fake SCM merge webhooks.
func webhookReceiverPort() int {
	return portFromEnv("PROMOTER_API_METRICS_WEBHOOK_PORT", constants.WebhookReceiverPort+ginkov2.GinkgoParallelProcess())
}

func portFromEnv(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}
