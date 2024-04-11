package scms

import (
	"context"

	"github.com/argoproj/promoter/internal/scms/github"
	v1 "k8s.io/api/core/v1"

	"github.com/argoproj/promoter/api/v1alpha1"
)

type PullRequestProvider interface {
	Create(ctx context.Context, title, head, base, description string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error)
	Close(ctx context.Context, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error)
	Update(ctx context.Context, title, description string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error)
	Merge(ctx context.Context, commitMessage string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error)
}

func NewScmPullRequestProvider(providerType ScmProviderType, secret v1.Secret) PullRequestProvider {
	switch providerType {
	case GitHub:
		return github.NewGithubProvider(secret)
	default:
		return nil
	}
}
