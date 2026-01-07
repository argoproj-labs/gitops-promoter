package bitbucket_cloud

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ktrysmt/go-bitbucket"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// PullRequest implements the scms.PullRequestProvider interface for Bitbucket Cloud.
type PullRequest struct {
	client    *bitbucket.Client
	k8sClient client.Client
}

var _ scms.PullRequestProvider = &PullRequest{}

// NewBitbucketCloudPullRequestProvider creates a new instance of PullRequest for Bitbucket Cloud.
func NewBitbucketCloudPullRequestProvider(k8sClient client.Client, secret v1.Secret) (*PullRequest, error) {
	client, err := GetClient(secret)
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

	options := &bitbucket.PullRequestsOptions{
		Owner:             repo.Spec.BitbucketCloud.Owner,
		RepoSlug:          repo.Spec.BitbucketCloud.Name,
		SourceBranch:      head,
		DestinationBranch: base,
		Title:             title,
		Description:       desc,
		CloseSourceBranch: false,
	}

	start := time.Now()
	resp, err := pr.client.Repositories.PullRequests.Create(options)
	statusCode := parseErrorStatusCode(err, http.StatusCreated)
	metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, statusCode, time.Since(start), nil)

	if err != nil {
		var unexpectedErr *bitbucket.UnexpectedResponseStatusError
		if errors.As(err, &unexpectedErr) {
			return "", fmt.Errorf("failed to create pull request: %w", unexpectedErr.ErrorWithBody())
		}
		return "", fmt.Errorf("failed to create pull request: %w", err)
	}

	// Extract pull request ID from response
	respMap, ok := resp.(map[string]any)
	if !ok {
		return "", fmt.Errorf("unexpected response type from Bitbucket API: %T", resp)
	}

	idValue, exists := respMap["id"]
	if !exists {
		return "", errors.New("pull request ID not found in Bitbucket API response")
	}

	idFloat, ok := idValue.(float64)
	if !ok {
		return "", fmt.Errorf("pull request ID has unexpected type: %T (expected float64)", idValue)
	}

	logger.V(4).Info("bitbucket response status", "status", statusCode)
	logger.V(4).Info("created pull request", "id", int(idFloat))

	return strconv.Itoa(int(idFloat)), nil
}

// Update updates an existing pull request with the specified title and description.
func (pr *PullRequest) Update(ctx context.Context, title, description string, prObj v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get repo: %w", err)
	}

	options := &bitbucket.PullRequestsOptions{
		Owner:       repo.Spec.BitbucketCloud.Owner,
		RepoSlug:    repo.Spec.BitbucketCloud.Name,
		ID:          prObj.Status.ID,
		Title:       title,
		Description: description,
	}

	start := time.Now()
	_, err = pr.client.Repositories.PullRequests.Update(options)
	statusCode := parseErrorStatusCode(err, http.StatusOK)
	metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationUpdate, statusCode, time.Since(start), nil)

	if err != nil {
		var unexpectedErr *bitbucket.UnexpectedResponseStatusError
		if errors.As(err, &unexpectedErr) {
			return fmt.Errorf("failed to update pull request: %w", unexpectedErr.ErrorWithBody())
		}
		return fmt.Errorf("failed to update pull request: %w", err)
	}

	logger.V(4).Info("bitbucket response status", "status", statusCode)
	logger.V(4).Info("updated pull request", "id", prObj.Status.ID)

	return nil
}

// Close closes an existing pull request.
func (pr *PullRequest) Close(ctx context.Context, prObj v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get repo: %w", err)
	}

	options := &bitbucket.PullRequestsOptions{
		Owner:    repo.Spec.BitbucketCloud.Owner,
		RepoSlug: repo.Spec.BitbucketCloud.Name,
		ID:       prObj.Status.ID,
	}

	start := time.Now()
	_, err = pr.client.Repositories.PullRequests.Decline(options)
	statusCode := parseErrorStatusCode(err, http.StatusOK)
	metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationClose, statusCode, time.Since(start), nil)

	if err != nil {
		var unexpectedErr *bitbucket.UnexpectedResponseStatusError
		if errors.As(err, &unexpectedErr) {
			return fmt.Errorf("failed to close pull request: %w", unexpectedErr.ErrorWithBody())
		}
		return fmt.Errorf("failed to close pull request: %w", err)
	}

	logger.V(4).Info("bitbucket response status", "status", statusCode)
	logger.V(4).Info("closed pull request", "id", prObj.Status.ID)

	return nil
}

// Merge merges an existing pull request with the specified commit message.
func (pr *PullRequest) Merge(ctx context.Context, prObj v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get repo: %w", err)
	}

	options := &bitbucket.PullRequestsOptions{
		Owner:             repo.Spec.BitbucketCloud.Owner,
		RepoSlug:          repo.Spec.BitbucketCloud.Name,
		ID:                prObj.Status.ID,
		CloseSourceBranch: false,
	}

	start := time.Now()
	_, err = pr.client.Repositories.PullRequests.Merge(options)
	statusCode := parseErrorStatusCode(err, http.StatusOK)
	metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationMerge, statusCode, time.Since(start), nil)

	if err != nil {
		var unexpectedErr *bitbucket.UnexpectedResponseStatusError
		if errors.As(err, &unexpectedErr) {
			return fmt.Errorf("failed to merge request: %w", unexpectedErr.ErrorWithBody())
		}
		return fmt.Errorf("failed to merge request: %w", err)
	}

	logger.V(4).Info("bitbucket response status", "status", statusCode)
	logger.V(4).Info("merged pull request", "id", prObj.Status.ID)

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

	// Build query to find PRs matching source and target branches and state
	// Bitbucket query syntax: https://developer.atlassian.com/cloud/bitbucket/rest/intro/#querying
	query := fmt.Sprintf(`source.branch.name=%s AND destination.branch.name=%s AND state="OPEN"`,
		strconv.Quote(pullRequest.Spec.SourceBranch),
		strconv.Quote(pullRequest.Spec.TargetBranch),
	)

	options := &bitbucket.PullRequestsOptions{
		Owner:    repo.Spec.BitbucketCloud.Owner,
		RepoSlug: repo.Spec.BitbucketCloud.Name,
		Query:    query,
	}

	start := time.Now()
	result, err := pr.client.Repositories.PullRequests.Gets(options)
	statusCode := parseErrorStatusCode(err, http.StatusOK)
	metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationList, statusCode, time.Since(start), nil)

	if err != nil {
		var unexpectedErr *bitbucket.UnexpectedResponseStatusError
		if errors.As(err, &unexpectedErr) {
			return false, "", time.Time{}, fmt.Errorf("failed to list pull requests: %w", unexpectedErr.ErrorWithBody())
		}
		return false, "", time.Time{}, fmt.Errorf("failed to list pull requests: %w", err)
	}

	logger.V(4).Info("bitbucket response status", "status", statusCode)

	// Parse the paginated response
	resultMap, ok := result.(map[string]any)
	if !ok {
		return false, "", time.Time{}, fmt.Errorf("unexpected response type from Bitbucket API: %T", result)
	}

	values, exists := resultMap["values"]
	if !exists {
		return false, "", time.Time{}, nil
	}
	prs, ok := values.([]any)
	if !ok || len(prs) == 0 {
		return false, "", time.Time{}, nil
	}

	// Get the first matching PR
	firstPR, ok := prs[0].(map[string]any)
	if !ok {
		return false, "", time.Time{}, errors.New("unexpected PR format in response")
	}

	// Extract and validate PR ID
	idValue, exists := firstPR["id"]
	if !exists {
		return false, "", time.Time{}, errors.New("PR ID not found in response")
	}
	idFloat, ok := idValue.(float64)
	if !ok {
		return false, "", time.Time{}, fmt.Errorf("PR ID has unexpected type: %T", idValue)
	}

	// Extract and validate created_on timestamp
	createdOn, exists := firstPR["created_on"]
	if !exists {
		return false, "", time.Time{}, errors.New("created_on not found in response")
	}
	createdStr, ok := createdOn.(string)
	if !ok {
		return false, "", time.Time{}, fmt.Errorf("created_on has unexpected type: %T", createdOn)
	}

	createdAt, err := time.Parse(time.RFC3339, createdStr)
	if err != nil {
		return false, "", time.Time{}, fmt.Errorf("failed to parse created_on timestamp: %w", err)
	}

	return true, strconv.Itoa(int(idFloat)), createdAt, nil
}

// GetUrl retrieves the URL of the pull request.
func (pr *PullRequest) GetUrl(ctx context.Context, prObj v1alpha1.PullRequest) (string, error) {
	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, client.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get repo: %w", err)
	}

	return fmt.Sprintf("https://bitbucket.org/%s/%s/pull-requests/%s",
		repo.Spec.BitbucketCloud.Owner,
		repo.Spec.BitbucketCloud.Name,
		prObj.Status.ID), nil
}
