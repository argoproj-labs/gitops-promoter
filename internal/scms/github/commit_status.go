package github

import (
	"context"
	promoterv1alpha1 "github.com/argoproj/promoter/api/v1alpha1"
	"github.com/argoproj/promoter/internal/scms"
	"github.com/google/go-github/v61/github"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type CommitStatus struct {
	client *github.Client
}

var _ scms.CommitStatusProvider = &CommitStatus{}

func NewGithubCommitStatusProvider(secret v1.Secret) (*CommitStatus, error) {
	client, err := GetClient(secret)
	if err != nil {
		return nil, err
	}

	return &CommitStatus{
		client: client,
	}, nil
}

func (cs CommitStatus) Set(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus) (*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)
	logger.Info("Setting Commit Status")

	return nil, nil
}
