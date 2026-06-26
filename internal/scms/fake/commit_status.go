package fake

import (
	"context"
	"errors"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CommitStatus implements the scms.CommitStatusProvider interface for testing purposes.
type CommitStatus struct {
	k8sClient client.Client
}

var _ scms.CommitStatusProvider = &CommitStatus{}

// NewFakeCommitStatusProvider creates a new instance of CommitStatus for testing purposes.
func NewFakeCommitStatusProvider(k8sClient client.Client, secret v1.Secret) (*CommitStatus, error) {
	return &CommitStatus{k8sClient: k8sClient}, nil
}

// Set sets the commit status for a given commit SHA in the specified repository.
func (cs CommitStatus) Set(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus) (*promoterv1alpha1.CommitStatus, error) {
	start := time.Now()

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, cs.k8sClient, client.ObjectKey{
		Namespace: commitStatus.Namespace,
		Name:      commitStatus.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return nil, err //nolint:wrapcheck // matches other providers
	}

	if commitStatus.Spec.Sha == "" {
		err = errors.New("sha is required")
		recordFakeSCMCall(ctx, repo, metrics.SCMAPICommitStatus, metrics.SCMOperationSet, start, scmStatusFromError(err))
		return nil, err
	}
	if commitStatus.Spec.Phase == "" {
		err = errors.New("phase is required")
		recordFakeSCMCall(ctx, repo, metrics.SCMAPICommitStatus, metrics.SCMOperationSet, start, scmStatusFromError(err))
		return nil, err
	}
	commitStatus.Status.Phase = commitStatus.Spec.Phase
	commitStatus.Status.Sha = commitStatus.Spec.Sha
	recordFakeSCMCall(ctx, repo, metrics.SCMAPICommitStatus, metrics.SCMOperationSet, start, scmStatusFromError(nil))
	return commitStatus, nil
}
