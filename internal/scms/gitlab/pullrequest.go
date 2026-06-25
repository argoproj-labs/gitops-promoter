package gitlab

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// PullRequest implements the scms.PullRequestProvider interface for GitLab.
type PullRequest struct {
	client    *gitlab.Client
	k8sClient client.Client
}

var _ scms.PullRequestProvider = &PullRequest{}

// NewGitlabPullRequestProvider creates a new instance of PullRequest for GitLab.
func NewGitlabPullRequestProvider(k8sClient client.Client, secret v1.Secret, domain string) (*PullRequest, error) {
	client, err := GetClient(secret, domain)
	if err != nil {
		return nil, err
	}

	return &PullRequest{
		client:    client,
		k8sClient: k8sClient,
	}, nil
}

// Create creates a new pull request with the specified title, head, base, and description.
func (pr *PullRequest) Create(ctx context.Context, title, head, base, desc string, prObj v1alpha1.PullRequest) (string, error) {
	logger := log.FromContext(ctx)

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get GitRepository: %w", err)
	}

	options := &gitlab.CreateMergeRequestOptions{
		Title:        new(title),
		SourceBranch: new(head),
		TargetBranch: new(base),
		Description:  new(desc),
	}

	start := time.Now()
	mr, resp, err := pr.client.MergeRequests.CreateMergeRequest(
		repo.Spec.GitLab.ProjectID,
		options,
	)
	if resp != nil {
		metrics.RecordSCMCall(ctx, repo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return "", err //nolint:wrapcheck // Error wrapping handled at top level
	}

	logGitLabRateLimitsIfAvailable(
		logger,
		prObj.Spec.RepositoryReference.Name,
		resp,
	)
	logger.V(4).Info("gitlab response status",
		"status", resp.Status)

	return strconv.FormatInt(mr.IID, 10), nil
}

// Update updates an existing pull request with the specified title and description.
func (pr *PullRequest) Update(ctx context.Context, title, description string, prObj v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	mrIID, err := strconv.ParseInt(prObj.Status.ID, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to convert MR ID %q to int64: %w", prObj.Status.ID, err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get repo: %w", err)
	}

	options := &gitlab.UpdateMergeRequestOptions{
		Title:       new(title),
		Description: new(description),
	}

	start := time.Now()
	_, resp, err := pr.client.MergeRequests.UpdateMergeRequest(
		repo.Spec.GitLab.ProjectID,
		mrIID,
		options,
		gitlab.WithContext(ctx),
	)
	if resp != nil {
		metrics.RecordSCMCall(ctx, repo, metrics.SCMAPIPullRequest, metrics.SCMOperationUpdate, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return fmt.Errorf("failed to update merge request: %w", err)
	}

	logGitLabRateLimitsIfAvailable(
		logger,
		prObj.Spec.RepositoryReference.Name,
		resp,
	)
	logger.V(4).Info("gitlab response status",
		"status", resp.Status)

	return nil
}

// Close closes an existing pull request.
func (pr *PullRequest) Close(ctx context.Context, prObj v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	mrIID, err := strconv.ParseInt(prObj.Status.ID, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to convert MR ID %q to int64: %w", prObj.Status.ID, err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get repo: %w", err)
	}

	options := &gitlab.UpdateMergeRequestOptions{
		StateEvent: new("close"),
	}

	start := time.Now()
	_, resp, err := pr.client.MergeRequests.UpdateMergeRequest(
		repo.Spec.GitLab.ProjectID,
		mrIID,
		options,
		gitlab.WithContext(ctx),
	)
	if resp != nil {
		metrics.RecordSCMCall(ctx, repo, metrics.SCMAPIPullRequest, metrics.SCMOperationClose, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return fmt.Errorf("failed to close merge request: %w", err)
	}

	logGitLabRateLimitsIfAvailable(
		logger,
		prObj.Spec.RepositoryReference.Name,
		resp,
	)
	logger.V(4).Info("gitlab response status",
		"status", resp.Status)

	return nil
}

// Merge merges an existing pull request with the specified commit message.
func (pr *PullRequest) Merge(ctx context.Context, prObj v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	mrIID, err := strconv.ParseInt(prObj.Status.ID, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to convert MR number to int64: %w", err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get repo: %w", err)
	}

	options := &gitlab.AcceptMergeRequestOptions{
		AutoMerge:                new(false),
		ShouldRemoveSourceBranch: new(false),
		Squash:                   new(false),
		SHA:                      new(prObj.Spec.MergeSha),
	}
	// Gitlab throws a 422 if you send it an empty commit message. So leave it as nil unless we have a message.
	if prObj.Spec.Commit.Message != "" {
		options.MergeCommitMessage = new(prObj.Spec.Commit.Message)
	}

	start := time.Now()
	_, resp, err := pr.client.MergeRequests.AcceptMergeRequest(
		repo.Spec.GitLab.ProjectID,
		mrIID,
		options,
		gitlab.WithContext(ctx),
	)
	if resp != nil {
		metrics.RecordSCMCall(ctx, repo, metrics.SCMAPIPullRequest, metrics.SCMOperationMerge, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return err //nolint:wrapcheck // Error wrapping handled at top level
	}

	logGitLabRateLimitsIfAvailable(
		logger,
		prObj.Spec.RepositoryReference.Name,
		resp,
	)
	logger.V(4).Info("gitlab response status",
		"status", resp.Status)

	return nil
}

// FindOpen checks if a pull request is open and returns its status.
func (pr *PullRequest) FindOpen(ctx context.Context, pullRequest v1alpha1.PullRequest) (bool, string, time.Time, error) {
	logger := log.FromContext(ctx)
	logger.V(4).Info("Finding Open Pull Request")

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{
		Namespace: pullRequest.Namespace,
		Name:      pullRequest.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return false, "", time.Time{}, fmt.Errorf("failed to get repo: %w", err)
	}

	options := &gitlab.ListProjectMergeRequestsOptions{
		SourceBranch: new(pullRequest.Spec.SourceBranch),
		TargetBranch: new(pullRequest.Spec.TargetBranch),
		State:        new("opened"),
	}

	start := time.Now()
	mrs, resp, err := pr.client.MergeRequests.ListProjectMergeRequests(repo.Spec.GitLab.ProjectID, options)
	if resp == nil {
		statusCode := -1
		metrics.RecordSCMCall(ctx, repo, metrics.SCMAPIPullRequest, metrics.SCMOperationList, statusCode, time.Since(start), nil)
		logger.V(4).Info("gitlab response status", "status", "nil response")
		return false, "", time.Time{}, errors.New("received nil response from GitLab API")
	}
	metrics.RecordSCMCall(ctx, repo, metrics.SCMAPIPullRequest, metrics.SCMOperationList, resp.StatusCode, time.Since(start), nil)
	if err != nil {
		return false, "", time.Time{}, fmt.Errorf("failed to list pull requests: %w", err)
	}

	logGitLabRateLimitsIfAvailable(
		logger,
		pullRequest.Spec.RepositoryReference.Name,
		resp,
	)
	logger.V(4).Info("gitlab response status",
		"status", resp.Status)

	if len(mrs) > 0 {
		return true, strconv.FormatInt(mrs[0].IID, 10), *mrs[0].CreatedAt, nil
	}

	return false, "", time.Time{}, nil
}

// GetUrl retrieves the URL of the pull request.
func (pr *PullRequest) GetUrl(ctx context.Context, prObj v1alpha1.PullRequest) (string, error) {
	// Get the URL for the pull request using string formatting
	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get repo: %w", err)
	}

	return FormatMergeRequestUrl(pr.client, repo.Spec.GitLab, prObj.Status.ID), nil
}

// AddLabels adds labels to a merge request on GitLab, creating missing project labels first.
func (pr *PullRequest) AddLabels(ctx context.Context, prObj v1alpha1.PullRequest, labelNames []string) error {
	if len(labelNames) == 0 {
		return nil
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get repo: %w", err)
	}

	if err := pr.ensureProjectLabels(ctx, repo, labelNames); err != nil {
		return err
	}

	return pr.updateMergeRequestLabels(ctx, prObj, labelNames, true)
}

// RemoveLabels removes labels from a merge request on GitLab.
func (pr *PullRequest) RemoveLabels(ctx context.Context, prObj v1alpha1.PullRequest, labelNames []string) error {
	return pr.updateMergeRequestLabels(ctx, prObj, labelNames, false)
}

func (pr *PullRequest) updateMergeRequestLabels(ctx context.Context, prObj v1alpha1.PullRequest, labelNames []string, add bool) error {
	if len(labelNames) == 0 {
		return nil
	}

	mrIID, err := strconv.ParseInt(prObj.Status.ID, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to convert MR ID %q to int64: %w", prObj.Status.ID, err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get repo: %w", err)
	}

	labelOpts := gitlab.LabelOptions(labelNames)
	options := &gitlab.UpdateMergeRequestOptions{}
	operation := metrics.SCMOperationRemoveLabels
	failVerb := "remove labels from"
	if add {
		options.AddLabels = &labelOpts
		operation = metrics.SCMOperationAddLabels
		failVerb = "add labels to"
	} else {
		options.RemoveLabels = &labelOpts
	}

	start := time.Now()
	_, resp, err := pr.client.MergeRequests.UpdateMergeRequest(
		repo.Spec.GitLab.ProjectID,
		mrIID,
		options,
		gitlab.WithContext(ctx),
	)
	if resp != nil {
		metrics.RecordSCMCall(ctx, repo, metrics.SCMAPIPullRequest, operation, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return fmt.Errorf("failed to %s merge request: %w", failVerb, err)
	}

	return nil
}

const defaultGitLabLabelColor = "#428BCA"

func (pr *PullRequest) ensureProjectLabels(ctx context.Context, repo *v1alpha1.GitRepository, labelNames []string) error {
	existing := make(map[string]struct{}, len(labelNames))
	page := int64(1)
	for {
		labels, resp, err := pr.client.Labels.ListLabels(repo.Spec.GitLab.ProjectID, &gitlab.ListLabelsOptions{
			ListOptions: gitlab.ListOptions{Page: page, PerPage: 100},
		}, gitlab.WithContext(ctx))
		if err != nil {
			return fmt.Errorf("failed to list project labels: %w", err)
		}
		for _, label := range labels {
			existing[label.Name] = struct{}{}
		}
		if resp == nil || resp.CurrentPage >= resp.TotalPages {
			break
		}
		page = resp.NextPage
	}

	for _, name := range labelNames {
		if _, ok := existing[name]; ok {
			continue
		}

		start := time.Now()
		_, resp, err := pr.client.Labels.CreateLabel(repo.Spec.GitLab.ProjectID, &gitlab.CreateLabelOptions{
			Name:  gitlab.Ptr(name),
			Color: gitlab.Ptr(defaultGitLabLabelColor),
		}, gitlab.WithContext(ctx))
		if resp != nil {
			metrics.RecordSCMCall(ctx, repo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreateLabel, resp.StatusCode, time.Since(start), nil)
		}
		if err != nil {
			if resp != nil && (resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusConflict) {
				continue
			}
			return fmt.Errorf("failed to create project label %q: %w", name, err)
		}
		existing[name] = struct{}{}
	}

	return nil
}

// FormatMergeRequestUrl constructs a GitLab merge request URL from the client's base URL and repository details.
// The client's BaseURL() returns the full API URL including protocol (e.g., "https://gitlab.example.com/api/v4").
// This function extracts the scheme and host to construct the web UI URL for the merge request.
func FormatMergeRequestUrl(client *gitlab.Client, gitlabRepo *v1alpha1.GitLabRepo, prID string) string {
	baseURL := client.BaseURL()
	return fmt.Sprintf("%s://%s/%s/%s/-/merge_requests/%s", baseURL.Scheme, baseURL.Host, gitlabRepo.Namespace, gitlabRepo.Name, prID)
}
