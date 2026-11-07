package bitbucket_cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
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

// The below constants represent the values of Pipeline states
const (
	PipelineStatePending     string = "PENDING"
	PipelineStateInProgress  string = "IN_PROGRESS"
	PipelineStateCompleted   string = "COMPLETED"
	PipelineResultSuccessful string = "SUCCESSFUL"
	PipelineResultFailed     string = "FAILED"
	PipelineResultError      string = "ERROR"
	PipelineResultStopped    string = "STOPPED"
	PipelineResultExpired    string = "EXPIRED"
)

// pipelineListResponse represents the response of a pipeline list
type pipelineListResponse struct {
	Next   string              `json:"next"`
	Values []bitbucketPipeline `json:"values"`
}

// bitbucketPipeline represents a BitBucket Pipeline
type bitbucketPipeline struct {
	State pipelineState `json:"state"`
}

// pipelineState represents the state field of a BitBucket pipeline
type pipelineState struct {
	Name   string         `json:"name"`
	Result pipelineResult `json:"result"`
}

// pipelineResult represents the state.result field of a BitBucket pipeline
type pipelineResult struct {
	Name string `json:"name"`
}

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

	// Loop through all pages of pipelines listed
	page := 1
	successFound := false
	failureFound := false
	inProgressFound := false
	for {
		pipelineOptions := &bitbucket.PipelinesOptions{
			Page:     page,
			Owner:    repo.Spec.BitbucketCloud.Owner,
			RepoSlug: repo.Spec.BitbucketCloud.Name,
			Query:    "target.commit.hash=" + latestCommitHash,
		}

		start = time.Now()
		result, err = c.client.Repositories.Pipelines.List(pipelineOptions)
		statusCode = parseErrorStatusCode(err, http.StatusOK)
		metrics.RecordSCMCall(ctx, repo, metrics.SCMChecks, metrics.SCMOperationList, statusCode, time.Since(start), nil)

		if err != nil {
			return nil, fmt.Errorf("failed to list SCM checks: %w", err)
		}

		jsonBytes, err := json.Marshal(result)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal pipeline list result: %w", err)
		}

		var pipelineList pipelineListResponse
		err = json.Unmarshal(jsonBytes, &pipelineList)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal pipeline list result json data: %w", err)
		}

		phase := parsePipelines(pipelineList.Values)

		switch phase {
		case scms.SCMCheckStatusSucceeded:
			successFound = true
		case scms.SCMCheckStatusFailed:
			failureFound = true
		default:
			inProgressFound = true
		}

		if pipelineList.Next == "" {
			break
		}
		page++
	}

	var check scms.SCMCheckResult
	check.Name = "Bitbucket Cloud Builds Aggregated Status"

	// Only set success if no other status was found
	if successFound && !failureFound && !inProgressFound {
		check.Status = scms.SCMCheckStatusSucceeded
	} else if failureFound {
		check.Status = scms.SCMCheckStatusFailed
	} else {
		check.Status = scms.SCMCheckStatusInProgress
	}
	return []scms.SCMCheckResult{check}, nil
}

// parsePipelines iterates over a list of pipelines and returns a status found.
// If a failure is found it will automatically return
func parsePipelines(pipelines []bitbucketPipeline) scms.SCMCheckStatus {
	var result scms.SCMCheckStatus
	inProgress := false
	for _, p := range pipelines {
		if p.State.Name != PipelineStateCompleted {
			inProgress = true
			continue
		}

		if isNegativeResult(p.State.Result.Name) {
			return scms.SCMCheckStatusFailed
		}

		if p.State.Result.Name == PipelineResultSuccessful {
			result = scms.SCMCheckStatusSucceeded
		}
	}

	if inProgress {
		result = scms.SCMCheckStatusInProgress
	}
	return result
}

// isNegativeResult is a helper function that returns if the result is negative
func isNegativeResult(result string) bool {
	negativeResults := []string{PipelineResultExpired, PipelineResultError, PipelineResultStopped, PipelineResultFailed}
	return slices.Contains(negativeResults, result)
}
