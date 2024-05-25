package scms

import (
	"context"

	"github.com/zachaller/promoter/api/v1alpha1"
)

type PullRequestProvider interface {
	Create(ctx context.Context, title, head, base, description string, pullRequest *v1alpha1.PullRequest) error
	Close(ctx context.Context, pullRequest *v1alpha1.PullRequest) error
	Update(ctx context.Context, title, description string, pullRequest *v1alpha1.PullRequest) error
	Merge(ctx context.Context, commitMessage string, pullRequest *v1alpha1.PullRequest) error
	Find(ctx context.Context, pullRequest *v1alpha1.PullRequest) (bool, error)
}
