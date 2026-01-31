package azuredevops

import (
	"context"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
)

// RequiredCheck is a stub implementation that returns ErrNotSupported.
// Azure DevOps required check discovery will be implemented in the future.
type RequiredCheck struct{}

var _ scms.RequiredCheckProvider = &RequiredCheck{}

// DiscoverRequiredChecks returns ErrNotSupported for Azure DevOps.
func (rc *RequiredCheck) DiscoverRequiredChecks(ctx context.Context, repo *promoterv1alpha1.GitRepository, branch string) ([]scms.RequiredCheck, error) {
	return nil, scms.ErrNotSupported
}

// PollCheckStatus returns ErrNotSupported for Azure DevOps.
func (rc *RequiredCheck) PollCheckStatus(ctx context.Context, repo *promoterv1alpha1.GitRepository, sha string, check scms.RequiredCheck) (promoterv1alpha1.CommitStatusPhase, error) {
	return promoterv1alpha1.CommitPhasePending, scms.ErrNotSupported
}
