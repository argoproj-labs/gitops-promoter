package gitlab

import (
	"context"
	"fmt"
	"strconv"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
		Title:        gitlab.Ptr(title),
		SourceBranch: gitlab.Ptr(head),
		TargetBranch: gitlab.Ptr(base),
		Description:  gitlab.Ptr(desc),
	}

	start := time.Now()
	mr, resp, err := pr.client.MergeRequests.CreateMergeRequest(
		repo.Spec.GitLab.ProjectID,
		options,
	)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return "", fmt.Errorf("failed to create pull request: %w", err)
	}

	logGitLabRateLimitsIfAvailable(
		logger,
		prObj.Spec.RepositoryReference.Name,
		resp,
	)
	logger.V(4).Info("gitlab response status",
		"status", resp.Status)

	return strconv.Itoa(mr.IID), nil
}

// Update updates an existing pull request with the specified title and description.
func (pr *PullRequest) Update(ctx context.Context, title, description string, prObj v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	mrIID, err := strconv.Atoi(prObj.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert MR ID %q to int: %w", prObj.Status.ID, err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get repo: %w", err)
	}

	options := &gitlab.UpdateMergeRequestOptions{
		Title:       gitlab.Ptr(title),
		Description: gitlab.Ptr(description),
	}

	start := time.Now()
	_, resp, err := pr.client.MergeRequests.UpdateMergeRequest(
		repo.Spec.GitLab.ProjectID,
		mrIID,
		options,
		gitlab.WithContext(ctx),
	)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationUpdate, resp.StatusCode, time.Since(start), nil)
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

	mrIID, err := strconv.Atoi(prObj.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert MR ID %q to int: %w", prObj.Status.ID, err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get repo: %w", err)
	}

	options := &gitlab.UpdateMergeRequestOptions{
		StateEvent: gitlab.Ptr("close"),
	}

	start := time.Now()
	_, resp, err := pr.client.MergeRequests.UpdateMergeRequest(
		repo.Spec.GitLab.ProjectID,
		mrIID,
		options,
		gitlab.WithContext(ctx),
	)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationClose, resp.StatusCode, time.Since(start), nil)
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
func (pr *PullRequest) Merge(ctx context.Context, commitMessage string, prObj v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	mrIID, err := strconv.Atoi(prObj.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert MR number to int: %w", err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get repo: %w", err)
	}

	options := &gitlab.AcceptMergeRequestOptions{
		AutoMerge:                gitlab.Ptr(false),
		ShouldRemoveSourceBranch: gitlab.Ptr(false),
		Squash:                   gitlab.Ptr(false),
	}
	// Gitlab throws a 422 if you send it an empty commit message. So leave it as nil unless we have a message.
	if commitMessage != "" {
		options.MergeCommitMessage = gitlab.Ptr(commitMessage)
	}

	start := time.Now()
	_, resp, err := pr.client.MergeRequests.AcceptMergeRequest(
		repo.Spec.GitLab.ProjectID,
		mrIID,
		options,
		gitlab.WithContext(ctx),
	)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationMerge, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return fmt.Errorf("failed to merge request: %w", err)
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
func (pr *PullRequest) FindOpen(ctx context.Context, prObj v1alpha1.PullRequest) (bool, v1alpha1.PullRequestCommonStatus, error) {
	logger := log.FromContext(ctx)
	logger.V(4).Info("Finding Open Pull Request")

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return false, v1alpha1.PullRequestCommonStatus{}, fmt.Errorf("failed to get repo: %w", err)
	}

	options := &gitlab.ListMergeRequestsOptions{
		SourceBranch: gitlab.Ptr(prObj.Spec.SourceBranch),
		TargetBranch: gitlab.Ptr(prObj.Spec.TargetBranch),
		State:        gitlab.Ptr("opened"),
	}

	start := time.Now()
	mrs, resp, err := pr.client.MergeRequests.ListMergeRequests(options)
	if resp != nil {
		metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationList, resp.StatusCode, time.Since(start), nil)
	}
	if err != nil {
		return false, v1alpha1.PullRequestCommonStatus{}, fmt.Errorf("failed to list pull requests: %w", err)
	}

	logGitLabRateLimitsIfAvailable(
		logger,
		prObj.Spec.RepositoryReference.Name,
		resp,
	)
	logger.V(4).Info("gitlab response status",
		"status", resp.Status)

	if len(mrs) > 0 {
		url, err := pr.GetUrl(ctx, prObj)
		if err != nil {
			return false, v1alpha1.PullRequestCommonStatus{}, fmt.Errorf("failed to get pull request URL: %w", err)
		}

		pullRequestStatus := v1alpha1.PullRequestCommonStatus{
			ID:             strconv.Itoa(mrs[0].IID),
			State:          mapMergeRequestState(mrs[0].State),
			Url:            url,
			PRCreationTime: metav1.Time{Time: *mrs[0].CreatedAt},
		}

		return true, pullRequestStatus, nil
	}

	return false, v1alpha1.PullRequestCommonStatus{}, nil
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

	return fmt.Sprintf("https://%s/%s/%s/-/merge_requests/%s", pr.client.BaseURL(), repo.Spec.GitLab.Namespace, repo.Spec.GitLab.Name, prObj.Status.ID), nil
}
