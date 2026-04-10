package bitbucket_datacenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8sClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// PullRequest implements the scms.PullRequestProvider interface for Bitbucket DataCenter/Server.
type PullRequest struct {
	client    *Client
	k8sClient k8sClient.Client
}

var _ scms.PullRequestProvider = &PullRequest{}

// NewBitbucketDataCenterPullRequestProvider creates a new PullRequest provider for Bitbucket DataCenter/Server.
func NewBitbucketDataCenterPullRequestProvider(k8sClient k8sClient.Client, scmProvider v1alpha1.GenericScmProvider, secret corev1.Secret) (*PullRequest, error) {
	client, err := GetClient(scmProvider.GetSpec().BitbucketDataCenter.Domain, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to create Bitbucket DataCenter client: %w", err)
	}
	return &PullRequest{
		client:    client,
		k8sClient: k8sClient,
	}, nil
}

// prResponse represents the subset of fields returned by the Bitbucket DataCenter pull-request API.
type prResponse struct {
	ID          int    `json:"id"`
	Version     int    `json:"version"`
	Title       string `json:"title"`
	Description string `json:"description"`
	State       string `json:"state"`
	CreatedDate int64  `json:"createdDate"` // milliseconds since epoch
	Links       struct {
		Self []struct {
			Href string `json:"href"`
		} `json:"self"`
	} `json:"links"`
	FromRef struct {
		DisplayID string `json:"displayId"`
	} `json:"fromRef"`
	ToRef struct {
		DisplayID string `json:"displayId"`
	} `json:"toRef"`
}

// prListResponse is the paginated response for listing pull requests.
type prListResponse struct {
	Values []prResponse `json:"values"`
}

// prPath returns the base REST API path for pull requests in a given project/repo.
func prPath(projectKey, repoSlug string) string {
	return fmt.Sprintf("/rest/api/1.0/projects/%s/repos/%s/pull-requests", projectKey, repoSlug)
}

// Create creates a new pull request and returns its ID.
func (pr *PullRequest) Create(ctx context.Context, title, head, base, description string, prObj v1alpha1.PullRequest) (string, error) {
	logger := log.FromContext(ctx)

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get GitRepository: %w", err)
	}

	projectKey := repo.Spec.BitbucketDataCenter.Project
	repoSlug := repo.Spec.BitbucketDataCenter.Name

	payload := pullRequestPayload{
		Title:       title,
		Description: description,
		FromRef:     newPullRequestRef(head, projectKey, repoSlug),
		ToRef:       newPullRequestRef(base, projectKey, repoSlug),
		Reviewers:   []any{},
	}

	start := time.Now()
	resp, body, err := pr.client.do(ctx, http.MethodPost, prPath(projectKey, repoSlug), payload)
	statusCode := http.StatusCreated
	if resp != nil {
		statusCode = resp.StatusCode
	} else if err != nil {
		statusCode = http.StatusInternalServerError
	}
	metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationCreate, statusCode, time.Since(start), nil)

	if err != nil {
		return "", fmt.Errorf("failed to create pull request: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("unexpected status code %d when creating pull request: %s", resp.StatusCode, string(body))
	}

	var created prResponse
	if err := json.Unmarshal(body, &created); err != nil {
		return "", fmt.Errorf("failed to parse create pull request response: %w", err)
	}

	logger.V(4).Info("Bitbucket DataCenter pull request created", "id", created.ID)
	return strconv.Itoa(created.ID), nil
}

// Update updates the title and description of an existing pull request.
func (pr *PullRequest) Update(ctx context.Context, title, description string, prObj v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	projectKey := repo.Spec.BitbucketDataCenter.Project
	repoSlug := repo.Spec.BitbucketDataCenter.Name

	prID, err := strconv.Atoi(prObj.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert PR ID %q to int: %w", prObj.Status.ID, err)
	}

	// Fetch the current PR to get the version required by the API.
	current, err := pr.getPR(ctx, projectKey, repoSlug, prID)
	if err != nil {
		return fmt.Errorf("failed to get pull request %d for update: %w", prID, err)
	}

	payload := pullRequestPayload{
		ID:          prID,
		Version:     current.Version,
		Title:       title,
		Description: description,
		FromRef:     newPullRequestRef(current.FromRef.DisplayID, projectKey, repoSlug),
		ToRef:       newPullRequestRef(current.ToRef.DisplayID, projectKey, repoSlug),
		Reviewers:   []any{},
	}

	path := fmt.Sprintf("%s/%d", prPath(projectKey, repoSlug), prID)

	start := time.Now()
	resp, body, err := pr.client.do(ctx, http.MethodPut, path, payload)
	statusCode := http.StatusOK
	if resp != nil {
		statusCode = resp.StatusCode
	} else if err != nil {
		statusCode = http.StatusInternalServerError
	}
	metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationUpdate, statusCode, time.Since(start), nil)

	if err != nil {
		return fmt.Errorf("failed to update pull request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code %d when updating pull request: %s", resp.StatusCode, string(body))
	}

	logger.V(4).Info("Bitbucket DataCenter pull request updated", "id", prID)
	return nil
}

// Close declines (closes) an existing pull request.
func (pr *PullRequest) Close(ctx context.Context, prObj v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	projectKey := repo.Spec.BitbucketDataCenter.Project
	repoSlug := repo.Spec.BitbucketDataCenter.Name

	prID, err := strconv.Atoi(prObj.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert PR ID %q to int: %w", prObj.Status.ID, err)
	}

	current, err := pr.getPR(ctx, projectKey, repoSlug, prID)
	if err != nil {
		return fmt.Errorf("failed to get pull request %d before declining: %w", prID, err)
	}

	path := fmt.Sprintf("%s/%d/decline?version=%d", prPath(projectKey, repoSlug), prID, current.Version)

	start := time.Now()
	resp, body, err := pr.client.do(ctx, http.MethodPost, path, nil)
	statusCode := http.StatusOK
	if resp != nil {
		statusCode = resp.StatusCode
	} else if err != nil {
		statusCode = http.StatusInternalServerError
	}
	metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationClose, statusCode, time.Since(start), nil)

	if err != nil {
		return fmt.Errorf("failed to decline pull request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code %d when declining pull request: %s", resp.StatusCode, string(body))
	}

	logger.V(4).Info("Bitbucket DataCenter pull request declined", "id", prID)
	return nil
}

// Merge merges an existing pull request.
func (pr *PullRequest) Merge(ctx context.Context, prObj v1alpha1.PullRequest) error {
	logger := log.FromContext(ctx)

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}

	projectKey := repo.Spec.BitbucketDataCenter.Project
	repoSlug := repo.Spec.BitbucketDataCenter.Name

	prID, err := strconv.Atoi(prObj.Status.ID)
	if err != nil {
		return fmt.Errorf("failed to convert PR ID %q to int: %w", prObj.Status.ID, err)
	}

	current, err := pr.getPR(ctx, projectKey, repoSlug, prID)
	if err != nil {
		return fmt.Errorf("failed to get pull request %d before merging: %w", prID, err)
	}

	path := fmt.Sprintf("%s/%d/merge?version=%d", prPath(projectKey, repoSlug), prID, current.Version)

	start := time.Now()
	resp, body, err := pr.client.do(ctx, http.MethodPost, path, nil)
	statusCode := http.StatusOK
	if resp != nil {
		statusCode = resp.StatusCode
	} else if err != nil {
		statusCode = http.StatusInternalServerError
	}
	metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationMerge, statusCode, time.Since(start), nil)

	if err != nil {
		return fmt.Errorf("failed to merge pull request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code %d when merging pull request: %s", resp.StatusCode, string(body))
	}

	logger.V(4).Info("Bitbucket DataCenter pull request merged", "id", prID)
	return nil
}

// FindOpen returns true if an open pull request exists for the given source and target branches,
// along with its ID and creation time.
func (pr *PullRequest) FindOpen(ctx context.Context, pullRequest v1alpha1.PullRequest) (bool, string, time.Time, error) {
	logger := log.FromContext(ctx)

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: pullRequest.Namespace,
		Name:      pullRequest.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return false, "", time.Time{}, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	projectKey := repo.Spec.BitbucketDataCenter.Project
	repoSlug := repo.Spec.BitbucketDataCenter.Name

	// List open PRs filtering by the source branch using the "at" query parameter.
	path := fmt.Sprintf("%s?state=OPEN&direction=OUTGOING&at=refs/heads/%s",
		prPath(projectKey, repoSlug),
		pullRequest.Spec.SourceBranch,
	)

	start := time.Now()
	resp, body, err := pr.client.do(ctx, http.MethodGet, path, nil)
	statusCode := http.StatusOK
	if resp != nil {
		statusCode = resp.StatusCode
	} else if err != nil {
		statusCode = http.StatusInternalServerError
	}
	metrics.RecordSCMCall(repo, metrics.SCMAPIPullRequest, metrics.SCMOperationList, statusCode, time.Since(start), nil)

	if err != nil {
		return false, "", time.Time{}, fmt.Errorf("failed to list pull requests: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return false, "", time.Time{}, fmt.Errorf("unexpected status code %d when listing pull requests: %s", resp.StatusCode, string(body))
	}

	var list prListResponse
	if err := json.Unmarshal(body, &list); err != nil {
		return false, "", time.Time{}, fmt.Errorf("failed to parse pull request list response: %w", err)
	}

	logger.V(4).Info("Bitbucket DataCenter pull request list", "count", len(list.Values))

	for _, p := range list.Values {
		if p.FromRef.DisplayID != pullRequest.Spec.SourceBranch ||
			p.ToRef.DisplayID != pullRequest.Spec.TargetBranch {
			continue
		}
		createdAt := time.UnixMilli(p.CreatedDate)
		return true, strconv.Itoa(p.ID), createdAt, nil
	}

	return false, "", time.Time{}, nil
}

// GetUrl returns the web UI URL for the given pull request.
func (pr *PullRequest) GetUrl(ctx context.Context, prObj v1alpha1.PullRequest) (string, error) {
	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, pr.k8sClient, k8sClient.ObjectKey{
		Namespace: prObj.Namespace,
		Name:      prObj.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get GitRepository: %w", err)
	}

	return fmt.Sprintf("%s/projects/%s/repos/%s/pull-requests/%s",
		pr.client.baseURL,
		repo.Spec.BitbucketDataCenter.Project,
		repo.Spec.BitbucketDataCenter.Name,
		prObj.Status.ID,
	), nil
}

// getPR fetches the current state of a pull request including its version.
func (pr *PullRequest) getPR(ctx context.Context, projectKey, repoSlug string, prID int) (*prResponse, error) {
	path := fmt.Sprintf("%s/%d", prPath(projectKey, repoSlug), prID)
	_, body, err := pr.client.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get pull request: %w", err)
	}
	var result prResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse pull request response: %w", err)
	}
	return &result, nil
}
