package bitbucket_datacenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8sClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	v1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// CommitStatus implements the scms.CommitStatusProvider interface for Bitbucket DataCenter/Server.
type CommitStatus struct {
	client    *Client
	k8sClient k8sClient.Client
}

var _ scms.CommitStatusProvider = &CommitStatus{}

// NewBitbucketDataCenterCommitStatusProvider creates a new CommitStatus provider for Bitbucket DataCenter/Server.
func NewBitbucketDataCenterCommitStatusProvider(k8sClient k8sClient.Client, scmProvider v1alpha1.GenericScmProvider, secret corev1.Secret) (*CommitStatus, error) {
	client, err := GetClient(scmProvider.GetSpec().BitbucketDataCenter.Domain, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to create Bitbucket DataCenter client: %w", err)
	}
	return &CommitStatus{
		client:    client,
		k8sClient: k8sClient,
	}, nil
}

// buildStatusRequest is the request body for POST /rest/build-status/1.0/commits/{commitId}.
type buildStatusRequest struct {
	State       string `json:"state"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// Set sets the commit (build) status for a given commit SHA in the specified repository.
func (cs *CommitStatus) Set(ctx context.Context, commitStatus *v1alpha1.CommitStatus) (*v1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)
	logger.Info("Setting Commit Status for Bitbucket DataCenter")

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, cs.k8sClient, k8sClient.ObjectKey{
		Namespace: commitStatus.Namespace,
		Name:      commitStatus.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	commitURL := commitStatus.Spec.Url
	if commitURL == "" {
		commitURL = createCommitURL(cs.client.baseURL, repo, commitStatus.Spec.Sha)
	}

	payload := buildStatusRequest{
		State:       phaseToBuildState(commitStatus.Spec.Phase),
		Key:         commitStatus.Spec.Name,
		Name:        commitStatus.Spec.Name,
		URL:         commitURL,
		Description: commitStatus.Spec.Description,
	}

	path := fmt.Sprintf("/rest/build-status/1.0/commits/%s", commitStatus.Spec.Sha)

	start := time.Now()
	resp, _, err := cs.client.do(ctx, http.MethodPost, path, payload)
	statusCode := http.StatusNoContent
	if resp != nil {
		statusCode = resp.StatusCode
	} else if err != nil {
		statusCode = http.StatusInternalServerError
	}
	metrics.RecordSCMCall(repo, metrics.SCMAPICommitStatus, metrics.SCMOperationCreate, statusCode, time.Since(start), nil)

	if err != nil {
		return nil, fmt.Errorf("failed to set build status: %w", err)
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code %d when setting build status", resp.StatusCode)
	}

	logger.V(4).Info("Bitbucket DataCenter build status set", "status", resp.StatusCode)

	commitStatus.Status.Phase = commitStatus.Spec.Phase
	commitStatus.Status.Sha = commitStatus.Spec.Sha

	return commitStatus, nil
}

// createCommitURL generates a URL to the commit in the Bitbucket DataCenter web UI.
func createCommitURL(baseURL string, repo *v1alpha1.GitRepository, sha string) string {
	return fmt.Sprintf("%s/projects/%s/repos/%s/commits/%s",
		baseURL,
		repo.Spec.BitbucketDataCenter.Project,
		repo.Spec.BitbucketDataCenter.Name,
		sha,
	)
}

// buildStatusResponse is the response body returned by the build-status GET endpoint.
type buildStatusResponse struct {
	Values []struct {
		State string `json:"state"`
		Key   string `json:"key"`
	} `json:"values"`
}

// getBuildStatus retrieves the build status for a given commit SHA.
// This is unused at runtime but is useful for debugging.
func (cs *CommitStatus) getBuildStatus(ctx context.Context, sha string) (*buildStatusResponse, error) {
	path := fmt.Sprintf("/rest/build-status/1.0/commits/%s", sha)
	_, body, err := cs.client.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get build status: %w", err)
	}
	var result buildStatusResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse build status response: %w", err)
	}
	return &result, nil
}
