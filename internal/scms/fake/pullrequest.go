package fake

import (
	"context"

	"github.com/argoproj/promoter/api/v1alpha1"
)

type PullRequest struct {
}

func NewFakePullRequestProvider() *PullRequest {
	return &PullRequest{}
}

func (pr *PullRequest) Create(ctx context.Context, title, head, base, description string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {
	pullRequest.Status.ID = "1"
	return pullRequest, nil
}

func (pr *PullRequest) Update(ctx context.Context, title, description string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {
	pullRequest.Status.ID = "1"
	return pullRequest, nil
}

func (pr *PullRequest) Close(ctx context.Context, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {
	pullRequest.Status.ID = "1"
	return pullRequest, nil
}

func (pr *PullRequest) Merge(ctx context.Context, commitMessage string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {
	pullRequest.Status.ID = "1"
	return pullRequest, nil
}
