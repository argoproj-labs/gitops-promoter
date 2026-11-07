package bitbucket_datacenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// CheckParser implements the scms.SCMCheckParser interface for Bitbucket DataCenter.
type CheckParser struct {
	client    *Client
	k8sClient client.Client
}

// buildStatusStatsResponse represents the response from the build status statistics API.
type buildStatusStatsResponse struct {
	Cancelled  int `json:"cancelled"`
	Failed     int `json:"failed"`
	InProgress int `json:"inProgress"`
	Successful int `json:"successful"`
	Unknown    int `json:"unknown"`
}

// NewBitbucketDataCenterCheckParser creates a new CheckParser for Bitbucket DataCenter
func NewBitbucketDataCenterCheckParser(ctx context.Context, k8sClient client.Client, scmProvider v1alpha1.GenericScmProvider, secret v1.Secret) (*CheckParser, error) {
	client, err := GetClient(scmProvider.GetSpec().BitbucketDataCenter.Domain, secret)
	if err != nil {
		return nil, fmt.Errorf("failed to create Bitbucket DataCenter client: %w", err)
	}

	return &CheckParser{
		client:    client,
		k8sClient: k8sClient,
	}, nil
}

// Get returns a list of checks from a Bitbucket DataCenter PR based on the most recent commit in the PR.
// BitBucket Data Center does not have a nice way to get all of the builds on a commit. The list option is deprecated.
// So we use the build stats endpoint which returns the number of builds in each state. Because of this we set the singular result to be
// in progress if nothing is reported success or fail and any failures will report a failure for the result.
func (c *CheckParser) Get(ctx context.Context, ps v1alpha1.PromotionStrategy, branch string) ([]scms.SCMCheckResult, error) {
	logger := log.FromContext(ctx)
	logger.V(4).Info("Retrieving checks from SCM")

	branchID, err := utils.GetPullRequestIDForBranch(ps, branch)
	if err != nil {
		return nil, fmt.Errorf("failed to get pull request id: %w", err)
	}
	prNumber, err := strconv.Atoi(branchID)
	if err != nil {
		return nil, fmt.Errorf("failed to convert PR ID to int: %w", err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, c.k8sClient, client.ObjectKey{
		Namespace: ps.Namespace,
		Name:      ps.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get repo: %w", err)
	}

	prClient := PullRequest{
		client:    c.client,
		k8sClient: c.k8sClient,
	}

	start := time.Now()
	statusCode, pr, err := prClient.getPR(ctx, repo.Spec.BitbucketDataCenter.Project, repo.Spec.BitbucketDataCenter.Name, prNumber)
	if err != nil && statusCode == 0 {
		statusCode = http.StatusInternalServerError
	}
	metrics.RecordSCMCall(ctx, repo, metrics.SCMAPIPullRequest, metrics.SCMOperationGet, statusCode, time.Since(start), nil)
	if statusCode == http.StatusNotFound {
		return nil, errors.New("bitbucket datacenter pull request was not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get pull request: %w", err)
	}
	if pr == nil {
		return nil, errors.New("retrieved pull request is empty")
	}

	start = time.Now()
	statusCode, buildStatusStats, err := c.getBuilds(ctx, pr.FromRef.LatestCommit)
	if buildStatusStats != nil {
		metrics.RecordSCMCall(ctx, repo, metrics.SCMChecks, metrics.SCMOperationList, statusCode, time.Since(start), nil)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to list SCM checks: %w", err)
	}

	var check scms.SCMCheckResult
	check.Name = "Bitbucket DataCenter Builds Aggregated Status"
	check.Status = scms.SCMCheckStatusInProgress

	if buildStatusStats.Successful > 0 {
		check.Status = scms.SCMCheckStatusSucceeded
	}

	if buildStatusStats.Failed > 0 || buildStatusStats.Cancelled > 0 {
		check.Status = scms.SCMCheckStatusFailed
	}

	return []scms.SCMCheckResult{check}, nil
}

// buildStatsPath returns the base REST API path for build status statistics.
func buildStatsPath(commitId string) string {
	return "/rest/build-status/1.0/commits/stats/" + commitId
}

// getBuilds gets the statistics for builds on a commit
func (c *CheckParser) getBuilds(ctx context.Context, commitId string) (int, *buildStatusStatsResponse, error) {
	path := buildStatsPath(commitId)
	statusCode, body, err := c.client.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return statusCode, nil, fmt.Errorf("failed to get build stats: %w", err)
	}
	if statusCode != http.StatusOK {
		return statusCode, nil, fmt.Errorf("unexpected status code %d when getting build stats: %s", statusCode, string(body))
	}
	var result buildStatusStatsResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return statusCode, nil, fmt.Errorf("failed to parse build stats response: %w", err)
	}
	return statusCode, &result, nil
}
