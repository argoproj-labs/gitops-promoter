package github

import (
	"context"
	"strconv"

	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
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

func (pr *PullRequest) Create(ctx context.Context, title, head, base, description string, pullRequest *v1alpha1.PullRequest) (string, error) {
	logger := log.FromContext(ctx)

	newPR := &github.NewPullRequest{
		Title: github.String(title),
		Head:  github.String(head),
		Base:  github.String(base),
		Body:  github.String(description),
	}

	githubPullRequest, response, err := pr.client.PullRequests.Create(context.Background(), pullRequest.Spec.RepositoryReference.Owner, pullRequest.Spec.RepositoryReference.Name, newPR)
	if err != nil {
		return "", err
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Reset,
		"url", response.Request.URL)
	logger.V(4).Info("github response status",
		"status", response.Status)

	return strconv.Itoa(*githubPullRequest.Number), nil
}

func (pr *PullRequest) Update(ctx context.Context, title, description string, pullRequest *v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	newPR := &github.PullRequest{
		Title: github.String(title),
		Body:  github.String(description),
	}

	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return err
	}

	_, response, err := pr.client.PullRequests.Edit(context.Background(), pullRequest.Spec.RepositoryReference.Owner, pullRequest.Spec.RepositoryReference.Name, prNumber, newPR)
	if err != nil {
		return err
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Reset,
		"url", response.Request.URL)
	logger.V(4).Info("github response status",
		"status", response.Status)

	//pullRequest.Status.State = v1alpha1.PullRequestClosed
	//pullRequest.Status.ID = strconv.Itoa(*githubPullRequest.Number)

	return nil

}

func (pr *PullRequest) Close(ctx context.Context, pullRequest *v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	newPR := &github.PullRequest{
		State: github.String("closed"),
	}

	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return err
	}

	_, response, err := pr.client.PullRequests.Edit(context.Background(), pullRequest.Spec.RepositoryReference.Owner, pullRequest.Spec.RepositoryReference.Name, prNumber, newPR)
	if err != nil {
		return err
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Reset,
		"url", response.Request.URL)
	logger.V(4).Info("github response status",
		"status", response.Status)

	//pullRequest.Status.State = v1alpha1.PullRequestClosed
	//pullRequest.Status.ID = strconv.Itoa(*githubPullRequest.Number)
	return nil
}

func (pr *PullRequest) Merge(ctx context.Context, commitMessage string, pullRequest *v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return err
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
		return err
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Reset,
		"url", response.Request.URL)
	logger.V(4).Info("github response status",
		"status", response.Status)

	pullRequest.Status.State = v1alpha1.PullRequestMerged
	return nil
}

func (pr *PullRequest) FindOpen(ctx context.Context, pullRequest *v1alpha1.PullRequest) (bool, error) {
	logger := log.FromContext(ctx)
	logger.V(4).Info("Finding Pull Request")

	pullRequests, response, err := pr.client.PullRequests.List(ctx, pullRequest.Spec.RepositoryReference.Owner,
		pullRequest.Spec.RepositoryReference.Name,
		&github.PullRequestListOptions{Base: pullRequest.Spec.TargetBranch, Head: pullRequest.Spec.SourceBranch, State: "open"})
	if err != nil {
		return false, err
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Reset,
		"url", response.Request.URL)
	logger.V(4).Info("github response status",
		"status", response.Status)
	if len(pullRequests) > 0 {
		pullRequest.Status.ID = strconv.Itoa(*pullRequests[0].Number)
		pullRequest.Status.State = v1alpha1.PullRequestState(*pullRequests[0].State)
		return true, nil
	}

	return false, nil
}
