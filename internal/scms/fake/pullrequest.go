package fake

import (
	"context"

	"github.com/zachaller/promoter/api/v1alpha1"
)

type PullRequest struct {
}

func NewFakePullRequestProvider() *PullRequest {
	return &PullRequest{}
}

func (pr *PullRequest) Create(ctx context.Context, title, head, base, description string, pullRequest *v1alpha1.PullRequest) error {
	pullRequest.Status.ID = "1"
	return nil
}

func (pr *PullRequest) Update(ctx context.Context, title, description string, pullRequest *v1alpha1.PullRequest) error {
	pullRequest.Status.ID = "1"
	return nil
}

func (pr *PullRequest) Close(ctx context.Context, pullRequest *v1alpha1.PullRequest) error {
	pullRequest.Status.ID = "1"
	return nil
}

func (pr *PullRequest) Merge(ctx context.Context, commitMessage string, pullRequest *v1alpha1.PullRequest) error {
	pullRequest.Status.ID = "1"
	return nil
}

func (pr *PullRequest) Find(ctx context.Context, pullRequest *v1alpha1.PullRequest) (bool, error) {
	if (pullRequest.Spec.State == "closed" && pullRequest.Status.State == "closed") ||
		(pullRequest.Spec.State == "merged" && pullRequest.Status.State == "merged") {
		return false, nil
	}
	pullRequest.Status.ID = "1"
	return true, nil
}
