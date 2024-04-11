package github

import (
	"context"
	"fmt"
	v1 "k8s.io/api/core/v1"
	"strconv"

	"github.com/argoproj/promoter/api/v1alpha1"
	"github.com/google/go-github/v61/github"
)

type GithubPullRequest struct {
	client *github.Client
}

func NewGithubProvider(secret v1.Secret) GithubPullRequest {
	return GithubPullRequest{
		client: GetClient(secret),
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
			ID:    strconv.Itoa(*githubPullRequest.Number),
			State: *githubPullRequest.State,
		},
	}, nil
}

func (pr GithubPullRequest) Update(title, description string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {

	newPR := &github.PullRequest{
		Title: github.String(title),
		Body:  github.String(description),
	}

	githubPullRequest, response, err := pr.client.PullRequests.Edit(context.Background(), pullRequest.Spec.RepositoryReference.Owner, pullRequest.Spec.RepositoryReference.Name, *newPR.Number, newPR)
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

func (pr GithubPullRequest) Close(pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {
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

func (pr GithubPullRequest) Merge(commitMessage string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error) {
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
	fmt.Println(pullRequest)
	fmt.Println(response)
	fmt.Println(err)

	pullRequest.Status.State = "merged"
	return pullRequest, nil
}
