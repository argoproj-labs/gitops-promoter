package bitbucket_cloud

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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

// The below constants represent the values of Status states
const (
	StatusStateSuccessful = "SUCCESSFUL"
	StatusStateFailed     = "FAILED"
	StatusStateInProgress = "INPROGRESS"
	StatusStateStopped    = "STOPPED"
)

// CheckParser implements the scms.SCMCheckParser interface for BitBucket Cloud.
type CheckParser struct {
	client    *bitbucket.Client
	k8sClient client.Client
}

// NewBitbucketCloudCheckParser creates a new CheckParser for BitBucket Cloud
func NewBitbucketCloudCheckParser(ctx context.Context, k8sClient client.Client, secret v1.Secret, domain string) (*CheckParser, error) {
	client, err := GetClient(secret, domain)
	if err != nil {
		return nil, fmt.Errorf("failed to create BitBucket Cloud client: %w", err)
	}

	return &CheckParser{
		client:    client,
		k8sClient: k8sClient,
	}, nil
}

// Get returns a list of checks from a BitBucket Cloud PR based on the most recent commit in the PR. BitBucket pipelines do not have
// a way to name them so all pipeline results are aggregated into a single result.
func (c *CheckParser) Get(ctx context.Context, ps v1alpha1.PromotionStrategy, branch string) ([]scms.SCMCheckResult, error) {
	logger := log.FromContext(ctx)
	logger.V(4).Info("Retrieving checks from SCM")

	branchID, err := utils.GetPullRequestIDForBranch(ps, branch)
	if err != nil {
		return nil, fmt.Errorf("failed to get pull request id: %w", err)
	}

	repo, err := utils.GetGitRepositoryFromObjectKey(ctx, c.k8sClient, client.ObjectKey{
		Namespace: ps.Namespace,
		Name:      ps.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get repo: %w", err)
	}

	prOptions := &bitbucket.PullRequestsOptions{
		Owner:    repo.Spec.BitbucketCloud.Owner,
		RepoSlug: repo.Spec.BitbucketCloud.Name,
		ID:       branchID,
	}

	start := time.Now()
	result, err := c.client.Repositories.PullRequests.Get(prOptions)
	statusCode := parseErrorStatusCode(err, http.StatusOK)
	metrics.RecordSCMCall(ctx, repo, metrics.SCMAPIPullRequest, metrics.SCMOperationGet, statusCode, time.Since(start), nil)

	if err != nil {
		if statusCode == http.StatusNotFound {
			return nil, errors.New("pull request was not found")
		}
		if unexpectedErr, ok := errors.AsType[*bitbucket.UnexpectedResponseStatusError](err); ok {
			return nil, fmt.Errorf("failed to get pull request: %w", unexpectedErr.ErrorWithBody())
		}
		return nil, fmt.Errorf("failed to get pull request: %w", err)
	}

	prMap, ok := result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected response type from BitBucket API: %T", result)
	}

	prSource, ok := prMap["source"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("pr source on Bitbucket PR was unexpected type: %T", prSource)
	}

	prCommit, ok := prSource["commit"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("commit on Bitbucket PR was unexpected type: %T", prCommit)
	}

	latestCommitHash, ok := prCommit["hash"].(string)
	if !ok {
		return nil, fmt.Errorf("hash on Bitbucket PR was unexpected type: %T", latestCommitHash)
	}

	if latestCommitHash == "" {
		return nil, errors.New("latest commit hash from PR is empty")
	}

	statuses, err := c.client.Repositories.Commits.GetCommitStatuses(&bitbucket.CommitsOptions{
		Owner:    repo.Spec.BitbucketCloud.Owner,
		RepoSlug: repo.Spec.BitbucketCloud.Name,
		Revision: latestCommitHash,
	})
	statusCode = parseErrorStatusCode(err, http.StatusOK)
	metrics.RecordSCMCall(ctx, repo, metrics.SCMChecks, metrics.SCMOperationGet, statusCode, time.Since(start), nil)

	statusMap, ok := statuses.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected status response type from BitBucket API: %T", statusMap)
	}

	statusList, ok := statusMap["values"].([]any)
	if !ok {
		return nil, fmt.Errorf("values in status response was unexpected type: %T", statusMap["values"])
	}

	checks := []scms.SCMCheckResult{}
	for _, rawStatus := range statusList {
		status, ok := rawStatus.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("status was unexpected type: %T", status)
		}

		check, err := parseStatus(status)
		if err != nil {
			return nil, fmt.Errorf("failed to parse status: %w", err)
		}

		checks = append(checks, check)
	}

	return checks, nil
}

// parseStatus parses a status for the result and returns an SCM check for that status.
func parseStatus(status map[string]any) (scms.SCMCheckResult, error) {
	key, ok := status["key"].(string)
	if !ok {
		return scms.SCMCheckResult{}, fmt.Errorf("key of status had unexpected type: %T", key)
	}

	state, ok := status["state"].(string)
	if !ok {
		return scms.SCMCheckResult{}, fmt.Errorf("state of status had unexpected type: %T", state)
	}

	url, ok := status["url"].(string)
	if !ok {
		return scms.SCMCheckResult{}, fmt.Errorf("url of status had unexpected type: %T", url)
	}

	return scms.SCMCheckResult{
		Name:   key,
		Status: determineCheckStatusFromBitbucketCloudState(state),
		URL:    url,
	}, nil
}

// determineCheckStatusFromBitbucketCloudState determines what the check result is based on the values that
// state can be.
func determineCheckStatusFromBitbucketCloudState(state string) scms.SCMCheckStatus {
	switch state {
	case StatusStateSuccessful:
		return scms.SCMCheckStatusSucceeded
	case StatusStateFailed, StatusStateStopped:
		return scms.SCMCheckStatusFailed
	default:
		return scms.SCMCheckStatusInProgress
	}
}
