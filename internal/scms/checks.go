package scms

import (
	"context"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// SCMCheckStatus represents the status of a SCM check
type SCMCheckStatus string

// The following constants represent the status a check might be in
const (
	SCMCheckStatusPending   SCMCheckStatus = "pending"
	SCMCheckStatusSucceeded SCMCheckStatus = "succeeded"
	SCMCheckStatusFailed    SCMCheckStatus = "failed"
)

// SCMCheckResult encapsulates a check from an SCM by name and status
type SCMCheckResult struct {
	Name      string
	Status    SCMCheckStatus
	SCMStatus string
	URL       string
}

// CheckParser defines the interface for retrieving SCM checks
type CheckParser interface {
	// Get retrieves information about the checks happening on a PullRequest
	Get(ctx context.Context, ps v1alpha1.PromotionStrategy, env string) ([]SCMCheckResult, error)
}
