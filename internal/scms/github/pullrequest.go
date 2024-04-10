package github

import (
	"context"
	"fmt"
	"github.com/argoproj/promoter/api/v1alpha1"
	"github.com/google/go-github/v61/github"
	"strconv"
)

type GithubPullRequest struct {
	client *github.Client
}

func NewGithubProvider() GithubPullRequest {
	return GithubPullRequest{
		client: GetClient(),
	}
}

func (pr GithubPullRequest) Create(title, head, base, description string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {

	newPR := &github.NewPullRequest{
		Title: github.String(title),
		Head:  github.String(head),
		Base:  github.String(base),
		Body:  github.String(description),
	}

	githubPullRequest, response, err := pr.client.PullRequests.Create(context.Background(), pullRequest.Spec.RepositoryReference.Owner, pullRequest.Spec.RepositoryReference.Name, newPR)
	fmt.Println(pullRequest)
	fmt.Println(response)
	fmt.Println(err)

	return &v1alpha1.PullRequest{
		TypeMeta:   pullRequest.TypeMeta,
		ObjectMeta: pullRequest.ObjectMeta,
		Spec:       pullRequest.Spec,
		Status: v1alpha1.PullRequestStatus{
			ID:    strconv.FormatInt(*githubPullRequest.ID, 10),
			State: *githubPullRequest.State,
		},
	}, nil
}

func (pr GithubPullRequest) Update(title, head, base, body string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {

	return &v1alpha1.PullRequest{}, nil
}

func (pr GithubPullRequest) Close(pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {
	return &v1alpha1.PullRequest{}, nil
}

func (pr GithubPullRequest) Merge(pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {
	return &v1alpha1.PullRequest{}, nil
}
