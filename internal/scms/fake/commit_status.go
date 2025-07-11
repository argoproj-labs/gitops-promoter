package fake

import (
	"context"
	"errors"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	v1 "k8s.io/api/core/v1"
)

// CommitStatus implements the scms.CommitStatusProvider interface for testing purposes.
type CommitStatus struct{}

var _ scms.CommitStatusProvider = &CommitStatus{}

// NewFakeCommitStatusProvider creates a new instance of CommitStatus for testing purposes.
func NewFakeCommitStatusProvider(secret v1.Secret) (*CommitStatus, error) {
	return &CommitStatus{}, nil
}

// Set sets the commit status for a given commit SHA in the specified repository.
func (cs CommitStatus) Set(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus) (*promoterv1alpha1.CommitStatus, error) {
	if commitStatus.Spec.Sha == "" {
		return nil, errors.New("sha is required")
	}
	commitStatus.Status.Phase = commitStatus.Spec.Phase
	commitStatus.Status.Sha = commitStatus.Spec.Sha
	return commitStatus, nil
}
