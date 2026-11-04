package scms

import (
	"context"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

type SCMCheckPhase string

// The following constants represent the status a check might be in
const (
	SCMCheckStatusPending    SCMCheckPhase = "pending"
	SCMCheckStatusInProgress SCMCheckPhase = "in-progress"
	SCMCheckStatusSucceeded  SCMCheckPhase = "succeeded"
	SCMCheckStatusFailed     SCMCheckPhase = "failed"
	SCMCheckStatusCancelled  SCMCheckPhase = "cancelled"
)

// SCMCheck encapsulates a check from an SCM by name and status
type SCMCheckResult struct {
	Name   string
	Status SCMCheckPhase
	URL    string
}

// SCMCheckParser defines the interface for retrieving SCM checks
type SCMCheckParser interface {
	// Get retreives information about the checks happening on a PullRequest
	Get(ctx context.Context, ps v1alpha1.PromotionStrategy, env string) ([]SCMCheckResult, error)
}
