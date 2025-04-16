package github

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/go-github/v71/github"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

type PullRequest struct {
	client    *github.Client
	k8sClient client.Client
}

var _ scms.PullRequestProvider = &PullRequest{}

func NewGithubPullRequestProvider(k8sClient client.Client, scmProvider *v1alpha1.ScmProvider, secret v1.Secret) (*PullRequest, error) {
	client, err := GetClient(scmProvider, secret)
	if err != nil {
		return nil, err
	}

	return &PullRequest{
		client:    client,
		k8sClient: k8sClient,
	}, nil
}

func (pr *PullRequest) Create(ctx context.Context, title, head, base, description string, pullRequest *v1alpha1.PullRequest) (string, error) {
	logger := log.FromContext(ctx)

	newPR := &github.NewPullRequest{
		Title: github.Ptr(title),
		Head:  github.Ptr(head),
		Base:  github.Ptr(base),
		Body:  github.Ptr(description),
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return "", fmt.Errorf("failed to get GitRepository: %w", err)
	}

	start := time.Now()
	githubPullRequest, response, err := pr.client.PullRequests.Create(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, newPR)
	if response != nil {
		metrics.RecordSCMCall(gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		return "", fmt.Errorf("failed to create pull request: %w", err)
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Reset,
		"url", response.Request.URL)
	logger.Info("github response status", "status", response.Status)

	return strconv.Itoa(*githubPullRequest.Number), nil
}

func (pr *PullRequest) Update(ctx context.Context, title, description string, pullRequest *v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	newPR := &github.PullRequest{
		Title: github.Ptr(title),
		Body:  github.Ptr(description),
	}

	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert PR number to int: %w", err)
	}

	gitRepo, _ := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})

	start := time.Now()
	_, response, err := pr.client.PullRequests.Edit(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, prNumber, newPR)
	if response != nil {
		metrics.RecordSCMCall(gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationUpdate, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		return fmt.Errorf("failed to edit pull request: %w", err)
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Reset,
		"url", response.Request.URL)
	logger.V(4).Info("github response status",
		"status", response.Status)

	return nil
}

func (pr *PullRequest) Close(ctx context.Context, pullRequest *v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	newPR := &github.PullRequest{
		State: github.Ptr("closed"),
	}

	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert PR number to int: %w", err)
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	start := time.Now()
	_, response, err := pr.client.PullRequests.Edit(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, prNumber, newPR)
	if response != nil {
		metrics.RecordSCMCall(gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationClose, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		return fmt.Errorf("failed to close pull request: %w", err)
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Reset,
		"url", response.Request.URL)
	logger.V(4).Info("github response status",
		"status", response.Status)

	return nil
}

func (pr *PullRequest) Merge(ctx context.Context, commitMessage string, pullRequest *v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert PR number to int: %w", err)
	}
	gitRepo, _ := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})

	start := time.Now()
	_, response, err := pr.client.PullRequests.Merge(
		ctx,
		gitRepo.Spec.GitHub.Owner,
		gitRepo.Spec.GitHub.Name,
		prNumber,
		commitMessage,
		&github.PullRequestOptions{
			MergeMethod:        "merge",
			DontDefaultIfBlank: false,
		})
	if response != nil {
		metrics.RecordSCMCall(gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationMerge, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		return fmt.Errorf("failed to merge pull request: %w", err)
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Reset,
		"url", response.Request.URL)
	logger.V(4).Info("github response status",
		"status", response.Status)

	return nil
}

func (pr *PullRequest) FindOpen(ctx context.Context, pullRequest *v1alpha1.PullRequest) (bool, error) {
	logger := log.FromContext(ctx)
	logger.V(4).Info("Finding Open Pull Request")

	gitRepo, _ := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})

	start := time.Now()
	pullRequests, response, err := pr.client.PullRequests.List(
		ctx, gitRepo.Spec.GitHub.Owner,
		gitRepo.Spec.GitHub.Name,
		&github.PullRequestListOptions{Base: pullRequest.Spec.TargetBranch, Head: pullRequest.Spec.SourceBranch, State: "open"})
	if response != nil {
		metrics.RecordSCMCall(gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationList, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		return false, fmt.Errorf("failed to list pull requests: %w", err)
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
