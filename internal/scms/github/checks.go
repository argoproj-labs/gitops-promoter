package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/google/go-github/v93/github"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CheckParser implements the scms.SCMCheckParser interface for GitHub.
type CheckParser struct {
	client    *github.Client
	k8sClient client.Client
}

// The following constants represent the values for the Status field for a CheckRun
// https://docs.github.com/en/pull-requests/reference/status-checks#check-statuses-and-conclusions
// https://docs.github.com/en/rest/checks/runs?apiVersion=2026-03-10#get-a-check-run
const (
	CheckRunStatusQueued     string = "queued"
	CheckRunStatusInProgress string = "in_progress"
	CheckRunStatusCompleted  string = "completed"
	CheckRunStatusWaiting    string = "waiting"
	CheckRunStatusRequested  string = "requested"
	CheckRunStatusPending    string = "pending"
)

// The following constants represent the values for the Conclusion field for a CheckRun
// https://docs.github.com/en/pull-requests/reference/status-checks#check-statuses-and-conclusions
// https://docs.github.com/en/rest/checks/runs?apiVersion=2026-03-10#get-a-check-run
const (
	CheckRunConclusionSuccess        string = "success"
	CheckRunConclusionFailure        string = "failure"
	CheckRunConclusionNeutral        string = "neutral"
	CheckRunConclusionCancelled      string = "cancelled"
	CheckRunConclusionSkipped        string = "skipped"
	CheckRunConclusionTimedOut       string = "timed_out"
	CheckRunConclusionActionRequired string = "action_required"
	CheckRunConclusionStale          string = "stale"
)

// NewGithubCheckParser creates a new CheckParser for GitHub
func NewGithubCheckParser(ctx context.Context, k8sClient client.Client, scmProvider v1alpha1.GenericScmProvider, secret v1.Secret, org string) (*CheckParser, error) {
	client, _, err := GetClient(ctx, scmProvider, secret, org)
	if err != nil {
		return nil, err
	}

	return &CheckParser{
		client:    client,
		k8sClient: k8sClient,
	}, nil
}

// Get returns a list of checks from a GitHub PR based on the most recent commit in the PR
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

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, c.k8sClient, client.ObjectKey{Namespace: ps.Namespace, Name: ps.Spec.RepositoryReference.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	start := time.Now()
	githubPR, response, err := c.client.PullRequests.Get(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, prNumber)
	if response != nil {
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMAPIPullRequest, metrics.SCMOperationGet, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		if response != nil && response.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("pull request %d was not found in repo %s/%s", prNumber, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name)
		}
		return nil, fmt.Errorf("failed to get pull request: %w", err)
	}
	if githubPR == nil {
		return nil, errors.New("retrieved PR is empty")
	}

	checks := []scms.SCMCheckResult{}

	start = time.Now()
	commitCheckList, response, err := c.client.Checks.ListCheckRunsForRef(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, githubPR.Head.GetSHA(), &github.ListCheckRunsOptions{})
	if response != nil {
		metrics.RecordSCMCall(ctx, gitRepo, metrics.SCMChecks, metrics.SCMOperationList, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		return nil, errors.New("failed to get list of github checks")
	}

	for _, check := range commitCheckList.GetCheckRuns() {
		if check == nil {
			return nil, errors.New("received a null check from GitHub")
		}

		// if conclusion is null then the check is still running, so refer to the status
		if check.Conclusion == nil {
			checks = append(checks, scms.SCMCheckResult{
				Name:   check.GetName(),
				Status: determineCheckStatusFromGHStatus(*check),
				URL:    check.GetURL(),
			})
			continue
		}
		checks = append(checks, scms.SCMCheckResult{
			Name:   check.GetName(),
			Status: determineCheckStatusFromGHConclusion(*check),
			URL:    check.GetURL(),
		})
	}

	return checks, nil
}

// determineCheckStatusFromGHConclusion is a helper function that determines the checks status based on the
// conclusion.
func determineCheckStatusFromGHConclusion(check github.CheckRun) scms.SCMCheckStatus {
	switch check.GetConclusion() {
	case CheckRunConclusionFailure, CheckRunConclusionStale:
		return scms.SCMCheckStatusFailed
	case CheckRunConclusionNeutral, CheckRunConclusionSuccess, CheckRunConclusionSkipped:
		// GitHub's API defines skipped and neutral as a success for dependent checks
		return scms.SCMCheckStatusSucceeded
	case CheckRunConclusionTimedOut, CheckRunConclusionCancelled:
		return scms.SCMCheckStatusCancelled
	default:
		return scms.SCMCheckStatusInProgress
	}
}

// determineCheckStatusFromGHStatus is a helper function that determines the checks status based on the
// status.
func determineCheckStatusFromGHStatus(check github.CheckRun) scms.SCMCheckStatus {
	switch check.GetStatus() {
	case CheckRunStatusQueued, CheckRunStatusWaiting, CheckRunStatusPending, CheckRunStatusRequested:
		return scms.SCMCheckStatusPending
	default:
		return scms.SCMCheckStatusInProgress
	}
}
