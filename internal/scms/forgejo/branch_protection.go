package forgejo

import (
	"context"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
)

// BranchProtection is a stub implementation that returns ErrNotSupported.
// Forgejo branch protection will be implemented in the future.
type BranchProtection struct{}

var _ scms.BranchProtectionProvider = &BranchProtection{}

// DiscoverRequiredChecks returns ErrNotSupported for Forgejo.
func (bp *BranchProtection) DiscoverRequiredChecks(ctx context.Context, repo *promoterv1alpha1.GitRepository, branch string) ([]scms.BranchProtectionCheck, error) {
	return nil, scms.ErrNotSupported
}

// PollCheckStatus returns ErrNotSupported for Forgejo.
func (bp *BranchProtection) PollCheckStatus(ctx context.Context, repo *promoterv1alpha1.GitRepository, sha string, check scms.BranchProtectionCheck) (promoterv1alpha1.CommitStatusPhase, error) {
	return promoterv1alpha1.CommitPhasePending, scms.ErrNotSupported
}
