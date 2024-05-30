package fake

import (
	"context"
	promoterv1alpha1 "github.com/zachaller/promoter/api/v1alpha1"
	"github.com/zachaller/promoter/internal/scms"
	v1 "k8s.io/api/core/v1"
)

type CommitStatus struct {
}

var _ scms.CommitStatusProvider = &CommitStatus{}

func NewFakeCommitStatusProvider(secret v1.Secret) (*CommitStatus, error) {
	return &CommitStatus{}, nil
}

func (cs CommitStatus) Set(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus) (*promoterv1alpha1.CommitStatus, error) {
	return commitStatus, nil
}
