package github

import (
	"time"

	"github.com/google/go-github/v71/github"

	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
)

// getRateLimitMetrics converts the GitHub rate limit struct to one acceptable for the metrics package.
func getRateLimitMetrics(rate github.Rate) *metrics.RateLimit {
	return &metrics.RateLimit{
		Limit:          rate.Limit,
		Remaining:      rate.Remaining,
		ResetRemaining: time.Until(rate.Reset.Time),
	}
}
