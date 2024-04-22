package github

import (
	"context"
	"strconv"

	"github.com/argoproj/promoter/internal/scms"

	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj/promoter/api/v1alpha1"
	"github.com/google/go-github/v61/github"
)

type PullRequest struct {
	client *github.Client
}

var _ scms.PullRequestProvider = &PullRequest{}

func NewGithubPullRequestProvider(secret v1.Secret) (*PullRequest, error) {
	client, err := GetClient(secret)
	if err != nil {
		return nil, err
	}

	return &PullRequest{
		client: client,
	}, nil
}

func (pr *PullRequest) Create(ctx context.Context, title, head, base, description string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {
	logger := log.FromContext(ctx)

	newPR := &github.NewPullRequest{
		Title: github.String(title),
		Head:  github.String(head),
		Base:  github.String(base),
		Body:  github.String(description),
	}

	githubPullRequest, response, err := pr.client.PullRequests.Create(context.Background(), pullRequest.Spec.RepositoryReference.Owner, pullRequest.Spec.RepositoryReference.Name, newPR)
	if err != nil {
		return nil, err
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Remaining)
	logger.Info("github response status",
		"status", response.Status)

	return &v1alpha1.PullRequest{
		TypeMeta:   pullRequest.TypeMeta,
		ObjectMeta: pullRequest.ObjectMeta,
		Spec:       pullRequest.Spec,
		Status: v1alpha1.PullRequestStatus{
			ID:    strconv.Itoa(*githubPullRequest.Number),
			State: v1alpha1.Open,
		},
	}, nil
}

func (pr *PullRequest) Update(ctx context.Context, title, description string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {
	logger := log.FromContext(ctx)

	newPR := &github.PullRequest{
		Title: github.String(title),
		Body:  github.String(description),
	}

	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return pullRequest, err
	}

	githubPullRequest, response, err := pr.client.PullRequests.Edit(context.Background(), pullRequest.Spec.RepositoryReference.Owner, pullRequest.Spec.RepositoryReference.Name, prNumber, newPR)
	if err != nil {
		return nil, err
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Remaining)
	logger.Info("github response status",
		"status", response.Status)

	return &v1alpha1.PullRequest{
		TypeMeta:   pullRequest.TypeMeta,
		ObjectMeta: pullRequest.ObjectMeta,
		Spec:       pullRequest.Spec,
		Status: v1alpha1.PullRequestStatus{
			ID:    strconv.Itoa(*githubPullRequest.Number),
			State: pullRequest.Status.State,
		},
	}, nil

}

func (pr *PullRequest) Close(ctx context.Context, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {
	logger := log.FromContext(ctx)

	newPR := &github.PullRequest{
		State: github.String("closed"),
	}

	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return pullRequest, err
	}

	githubPullRequest, response, err := pr.client.PullRequests.Edit(context.Background(), pullRequest.Spec.RepositoryReference.Owner, pullRequest.Spec.RepositoryReference.Name, prNumber, newPR)
	if err != nil {
		return nil, err
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Remaining)
	logger.Info("github response status",
		"status", response.Status)

	return &v1alpha1.PullRequest{
		TypeMeta:   pullRequest.TypeMeta,
		ObjectMeta: pullRequest.ObjectMeta,
		Spec:       pullRequest.Spec,
		Status: v1alpha1.PullRequestStatus{
			ID:    strconv.Itoa(*githubPullRequest.Number),
			State: v1alpha1.Closed,
		},
	}, nil
}

func (pr *PullRequest) Merge(ctx context.Context, commitMessage string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {
	logger := log.FromContext(ctx)

	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return pullRequest, err
	}

	_, response, err := pr.client.PullRequests.Merge(
		context.Background(),
		pullRequest.Spec.RepositoryReference.Owner,
		pullRequest.Spec.RepositoryReference.Name,
		prNumber,
		commitMessage,
		&github.PullRequestOptions{
			MergeMethod:        "merge",
			DontDefaultIfBlank: false,
		})
	if err != nil {
		return nil, err
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Remaining)
	logger.Info("github response status",
		"status", response.Status)

	pullRequest.Status.State = v1alpha1.Merged
	return pullRequest, nil
}
