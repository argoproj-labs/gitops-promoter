package scms

import (
	"context"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// SCMCheckStatus represents the status of a SCM check
type SCMCheckStatus string

// The following constants represent the status a check might be in
const (
	SCMCheckStatusPending    SCMCheckStatus = "pending"
	SCMCheckStatusInProgress SCMCheckStatus = "in-progress"
	SCMCheckStatusSucceeded  SCMCheckStatus = "succeeded"
	SCMCheckStatusFailed     SCMCheckStatus = "failed"
	SCMCheckStatusCancelled  SCMCheckStatus = "cancelled"
)

// SCMCheckResult encapsulates a check from an SCM by name and status
type SCMCheckResult struct {
	Name   string
	Status SCMCheckStatus
	URL    string
}

// SCMCheckParser defines the interface for retrieving SCM checks
type SCMCheckParser interface {
	// Get retrieves information about the checks happening on a PullRequest
	Get(ctx context.Context, ps v1alpha1.PromotionStrategy, env string) ([]SCMCheckResult, error)
}
