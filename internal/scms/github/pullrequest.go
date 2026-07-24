package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/go-github/v89/github"
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
func (pr *PullRequest) FindOpen(ctx context.Context, pullRequest v1alpha1.PullRequest) (scms.FindOpenResult, error) {
	logger := log.FromContext(ctx)
	logger.V(4).Info("Finding Open Pull Request")

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil || gitRepo == nil {
		return scms.FindOpenResult{}, fmt.Errorf("failed to get GitRepository: %w", err)
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
		return scms.FindOpenResult{}, fmt.Errorf("failed to list pull requests: %w", err)
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Reset,
		"url", response.Request.URL)
	logger.V(4).Info("github response status",
		"status", response.Status)
	if len(pullRequests) > 0 {
		pr0 := pullRequests[0]
		scmLabels := make([]string, 0, len(pr0.Labels))
		for _, label := range pr0.Labels {
			if label != nil && label.Name != nil {
				scmLabels = append(scmLabels, *label.Name)
			}
		}
		return scms.FindOpenResult{
			Found:          true,
			ID:             strconv.Itoa(*pr0.Number),
			CreationTime:   pr0.CreatedAt.Time,
			SCMLabels:      scmLabels,
			LabelsReported: true,
		}, nil
	}

	return scms.FindOpenResult{}, nil
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

// AddLabels adds labels to a pull request on GitHub, creating missing repository labels first.
func (pr *PullRequest) AddLabels(ctx context.Context, pullRequest v1alpha1.PullRequest, labels []string) error {
	if len(labels) == 0 {
		return nil
	}

	logger := log.FromContext(ctx)

	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert PR number to int: %w", err)
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	if err := pr.ensureRepositoryLabels(ctx, gitRepo, labels); err != nil {
		return err
	}

	start := time.Now()
	_, response, err := pr.client.Issues.AddLabelsToIssue(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, prNumber, labels)
	if response != nil {
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationAddLabels, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		return fmt.Errorf("failed to add labels to pull request: %w", err)
	}
	logger.V(4).Info("added labels to github pull request", "labels", labels)

	return nil
}

func (pr *PullRequest) ensureRepositoryLabels(ctx context.Context, gitRepo *v1alpha1.GitRepository, labelNames []string) error {
	owner := gitRepo.Spec.GitHub.Owner
	repo := gitRepo.Spec.GitHub.Name

	existing := make(map[string]struct{}, len(labelNames))
	for label, err := range pr.client.Issues.ListLabelsIter(ctx, owner, repo, &github.ListOptions{PerPage: 100}) {
		if err != nil {
			return fmt.Errorf("failed to list repository labels: %w", err)
		}
		existing[label.GetName()] = struct{}{}
	}

	for _, name := range labelNames {
		if _, ok := existing[name]; ok {
			continue
		}

		if err := pr.createRepositoryLabel(ctx, gitRepo, owner, repo, name); err != nil {
			return err
		}
		existing[name] = struct{}{}
	}

	return nil
}

func (pr *PullRequest) createRepositoryLabel(ctx context.Context, gitRepo *v1alpha1.GitRepository, owner, repo, name string) error {
	start := time.Now()
	_, response, err := pr.client.Issues.CreateLabel(ctx, owner, repo, &github.Label{
		Name:  github.Ptr(name),
		Color: github.Ptr(scms.AutoCreatedLabelColor),
	})
	if response != nil {
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreateLabel, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err == nil {
		return nil
	}
	// GitHub returns 422 for "label already exists" (create race) and for real validation
	// failures. Re-list and only treat 422 as success when the label is actually present.
	if response != nil && response.StatusCode == http.StatusUnprocessableEntity {
		exists, err := pr.repositoryHasLabel(ctx, owner, repo, name)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
	}
	return fmt.Errorf("failed to create repository label %q: %w", name, err)
}

func (pr *PullRequest) repositoryHasLabel(ctx context.Context, owner, repo, name string) (bool, error) {
	for label, err := range pr.client.Issues.ListLabelsIter(ctx, owner, repo, &github.ListOptions{PerPage: 100}) {
		if err != nil {
			return false, fmt.Errorf("failed to list repository labels: %w", err)
		}
		if label.GetName() == name {
			return true, nil
		}
	}
	return false, nil
}

// RemoveLabels removes labels from a pull request on GitHub.
func (pr *PullRequest) RemoveLabels(ctx context.Context, pullRequest v1alpha1.PullRequest, labels []string) error {
	if len(labels) == 0 {
		return nil
	}

	logger := log.FromContext(ctx)

	prNumber, err := strconv.Atoi(pullRequest.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert PR number to int: %w", err)
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Spec.RepositoryReference.Name})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	for _, label := range labels {
		start := time.Now()
		response, err := pr.client.Issues.RemoveLabelForIssue(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, prNumber, label)
		if response != nil {
			metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationRemoveLabels, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
		}
		if err != nil {
			if response != nil && response.StatusCode == http.StatusNotFound {
				continue
			}
			return fmt.Errorf("failed to remove label %q from pull request: %w", label, err)
		}
	}
	logger.V(4).Info("removed labels from github pull request", "labels", labels)

	return nil
}
