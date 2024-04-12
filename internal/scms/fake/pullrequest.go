package fake

import (
	"context"
	"github.com/argoproj/promoter/api/v1alpha1"
)

type FakePullRequest struct {
}

func NewFakeProvider() FakePullRequest {
	return FakePullRequest{}
}

func (pr FakePullRequest) Create(ctx context.Context, title, head, base, description string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {
	pullRequest.Status.ID = "1"
	return pullRequest, nil
}

func (pr FakePullRequest) Update(ctx context.Context, title, description string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {
	pullRequest.Status.ID = "1"
	return pullRequest, nil
}

func (pr FakePullRequest) Close(ctx context.Context, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {
	pullRequest.Status.ID = "1"
	return pullRequest, nil
}

func (pr FakePullRequest) Merge(ctx context.Context, commitMessage string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {
	pullRequest.Status.ID = "1"
	return pullRequest, nil
}
