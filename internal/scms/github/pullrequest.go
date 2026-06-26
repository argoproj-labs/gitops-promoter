package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/go-github/v88/github"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// PullRequest implements the scms.PullRequestProvider interface for GitHub.
type PullRequest struct {
	client    *github.Client
	k8sClient client.Client
}

var _ scms.PullRequestProvider = &PullRequest{}

// NewGithubPullRequestProvider creates a new instance of PullRequest for GitHub.
func NewGithubPullRequestProvider(ctx context.Context, k8sClient client.Client, scmProvider v1alpha1.GenericScmProvider, secret v1.Secret, org string) (*PullRequest, error) {
	client, _, err := GetClient(ctx, scmProvider, secret, org)
	if err != nil {
		return nil, err
	}

	return &PullRequest{
		client:    client,
		k8sClient: k8sClient,
	}, nil
}

// Create creates a new pull request with the specified title, head, base, and description.
func (pr *PullRequest) Create(ctx context.Context, title, head, base, description string, pullRequest v1alpha1.PullRequest) (string, error) {
	logger := log.FromContext(ctx)

	newPR := &github.NewPullRequest{
		Title: new(title),
		Head:  new(head),
		Base:  new(base),
		Body:  new(description),
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return "", fmt.Errorf("failed to get GitRepository: %w", err)
	}

	start := time.Now()
	githubPullRequest, response, err := pr.client.PullRequests.Create(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, newPR)
	if response != nil {
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		return "", err //nolint:wrapcheck // Error wrapping handled at top level
	}
	if githubPullRequest == nil || githubPullRequest.Number == nil {
		return "", errors.New("GitHub returned empty pull request response")
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Reset,
		"url", response.Request.URL)
	logger.Info("github response status", "status", response.Status)

	return strconv.Itoa(*githubPullRequest.Number), nil
}

// Update updates an existing pull request with the specified title and description.
func (pr *PullRequest) Update(ctx context.Context, title, description string, pullRequest v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	newPR := &github.PullRequest{
		Title: new(title),
		Body:  new(description),
	}

	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert PR number to int: %w", err)
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil || gitRepo == nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	start := time.Now()
	_, response, err := pr.client.PullRequests.Edit(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, prNumber, newPR)
	if response != nil {
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationUpdate, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
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

// Close closes an existing pull request.
func (pr *PullRequest) Close(ctx context.Context, pullRequest v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	newPR := &github.PullRequest{
		State: new("closed"),
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
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationClose, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		return err //nolint:wrapcheck // Error wrapping handled at top level
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

// Merge merges an existing pull request with the specified commit message.
func (pr *PullRequest) Merge(ctx context.Context, pullRequest v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert PR number to int: %w", err)
	}
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil || gitRepo == nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	start := time.Now()
	_, response, err := pr.client.PullRequests.Merge(
		ctx,
		gitRepo.Spec.GitHub.Owner,
		gitRepo.Spec.GitHub.Name,
		prNumber,
		pullRequest.Spec.Commit.Message,
		&github.PullRequestOptions{
			MergeMethod:        "merge",
			DontDefaultIfBlank: false,
			SHA:                pullRequest.Spec.MergeSha,
		})
	if response != nil {
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationMerge, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		return err //nolint:wrapcheck // Error wrapping handled at top level
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

// FindOpen checks if a pull request is open and returns its status.
func (pr *PullRequest) FindOpen(ctx context.Context, pullRequest v1alpha1.PullRequest) (bool, string, time.Time, error) {
	logger := log.FromContext(ctx)
	logger.V(4).Info("Finding Open Pull Request")

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil || gitRepo == nil {
		return false, "", time.Time{}, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	start := time.Now()
	pullRequests, response, err := pr.client.PullRequests.List(
		ctx, gitRepo.Spec.GitHub.Owner,
		gitRepo.Spec.GitHub.Name,
		&github.PullRequestListOptions{Base: pullRequest.Spec.TargetBranch, Head: fmt.Sprintf("%s:%s", gitRepo.Spec.GitHub.Owner, pullRequest.Spec.SourceBranch), State: "open"})
	if response != nil {
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationList, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		return false, "", time.Time{}, fmt.Errorf("failed to list pull requests: %w", err)
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Reset,
		"url", response.Request.URL)
	logger.V(4).Info("github response status",
		"status", response.Status)
	if len(pullRequests) > 0 {
		return true, strconv.Itoa(*pullRequests[0].Number), pullRequests[0].CreatedAt.Time, nil
	}

	return false, "", time.Time{}, nil
}

// AddLabels adds labels to an existing pull request.
// PRs are issues in GitHub's model, so the Issues API is used here.
func (pr *PullRequest) AddLabels(ctx context.Context, pullRequest v1alpha1.PullRequest, labels []string) error {
	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert PR number to int: %w", err)
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil || gitRepo == nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	start := time.Now()
	_, response, err := pr.client.Issues.AddLabelsToIssue(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, prNumber, labels)
	if response != nil {
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationUpdate, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		return fmt.Errorf("failed to add labels to pull request: %w", err)
	}

	return nil
}

// RemoveLabel removes a single label from an existing pull request.
// PRs are issues in GitHub's model, so the Issues API is used here.
func (pr *PullRequest) RemoveLabel(ctx context.Context, pullRequest v1alpha1.PullRequest, label string) error {
	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert PR number to int: %w", err)
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil || gitRepo == nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	start := time.Now()
	response, err := pr.client.Issues.RemoveLabelForIssue(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, prNumber, label)
	if response != nil {
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationUpdate, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		if response != nil && response.StatusCode == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("failed to remove label from pull request: %w", err)
	}

	return nil
}

// CreateComment posts a comment on an existing pull request and returns its SCM-assigned ID.
// PRs are issues in GitHub's model, so the Issues API is used here.
func (pr *PullRequest) CreateComment(ctx context.Context, pullRequest v1alpha1.PullRequest, comment string) (string, error) {
	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return "", fmt.Errorf("failed to convert PR number to int: %w", err)
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil || gitRepo == nil {
		return "", fmt.Errorf("failed to get GitRepository: %w", err)
	}

	start := time.Now()
	issueComment, response, err := pr.client.Issues.CreateComment(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, prNumber, &github.IssueComment{Body: &comment})
	if response != nil {
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		return "", fmt.Errorf("failed to create comment on pull request: %w", err)
	}

	return strconv.FormatInt(issueComment.GetID(), 10), nil
}

// DeleteComment removes a previously posted comment from an existing pull request.
// PRs are issues in GitHub's model, so the Issues API is used here.
func (pr *PullRequest) DeleteComment(ctx context.Context, pullRequest v1alpha1.PullRequest, commentID string) error {
	commentIDInt, err := strconv.ParseInt(commentID, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse comment ID: %w", err)
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil || gitRepo == nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	start := time.Now()
	response, err := pr.client.Issues.DeleteComment(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, commentIDInt)
	if response != nil {
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationClose, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		if response != nil && response.StatusCode == http.StatusNotFound {
			return nil
		}
		return fmt.Errorf("failed to delete comment from pull request: %w", err)
	}

	return nil
}

// GetUrl returns the URL of the pull request.
func (pr *PullRequest) GetUrl(ctx context.Context, pullRequest v1alpha1.PullRequest) (string, error) {
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil || gitRepo == nil {
		return "", fmt.Errorf("failed to get GitRepository: %w", err)
	}

	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return "", fmt.Errorf("failed to convert PR number to int when generating pull request url: %w", err)
	}

	baseURL, err := url.Parse(pr.client.BaseURL())
	if err != nil {
		return "", fmt.Errorf("failed to parse GitHub base URL: %w", err)
	}

	if baseURL.Host == "api.github.com" {
		return fmt.Sprintf("%s/%s/%s/pull/%d", "https://github.com", gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, prNumber), nil
	}

	return fmt.Sprintf("https://%s/%s/%s/pull/%d", baseURL.Host, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, prNumber), nil
}
