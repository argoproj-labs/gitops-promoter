package fake

import (
	"context"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	v1 "k8s.io/api/core/v1"
)

// BranchProtection implements the scms.BranchProtectionProvider interface for testing purposes.
type BranchProtection struct {
	// RequiredChecks is a configurable list of required checks to return for testing
	RequiredChecks []string
	// CheckStatuses is a configurable map of check names to their statuses for testing
	CheckStatuses map[string]promoterv1alpha1.CommitStatusPhase
}

var _ scms.BranchProtectionProvider = &BranchProtection{}

// NewFakeBranchProtectionProvider creates a new instance of BranchProtection for testing purposes.
func NewFakeBranchProtectionProvider(secret v1.Secret) (*BranchProtection, error) {
	return &BranchProtection{
		RequiredChecks: []string{},
		CheckStatuses:  make(map[string]promoterv1alpha1.CommitStatusPhase),
	}, nil
}

// DiscoverRequiredChecks returns the configured list of required checks for testing.
func (bp *BranchProtection) DiscoverRequiredChecks(ctx context.Context, repo *promoterv1alpha1.GitRepository, branch string) ([]scms.BranchProtectionCheck, error) {
	checks := make([]scms.BranchProtectionCheck, 0, len(bp.RequiredChecks))
	for _, checkName := range bp.RequiredChecks {
		checks = append(checks, scms.BranchProtectionCheck{
			Name: checkName,
		})
	}
	return checks, nil
}

// PollCheckStatus returns the configured status for the given check name, or Success if not configured.
func (bp *BranchProtection) PollCheckStatus(ctx context.Context, repo *promoterv1alpha1.GitRepository, sha string, check scms.BranchProtectionCheck) (promoterv1alpha1.CommitStatusPhase, error) {
	if status, ok := bp.CheckStatuses[check.Name]; ok {
		return status, nil
	}
	// Default to success for testing
	return promoterv1alpha1.CommitPhaseSuccess, nil
}
