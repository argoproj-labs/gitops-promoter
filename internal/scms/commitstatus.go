package scms

import (
	"context"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// CommitStatusProvider defines the interface for managing commit statuses in a source control management system.
type CommitStatusProvider interface {
	// Set sets the commit status for a given commit SHA in the specified repository.
	Set(ctx context.Context, commitStatus *v1alpha1.CommitStatus) (*v1alpha1.CommitStatus, error)
}
