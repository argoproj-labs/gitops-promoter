package github

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/go-github/v71/github"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// CommitStatus implements the scms.CommitStatusProvider interface for GitHub.
type CommitStatus struct {
	client    *github.Client
	k8sClient client.Client
}

var _ scms.CommitStatusProvider = &CommitStatus{}

const (
	checkRunStatusQueued     = "queued"
	checkRunStatusInProgress = "in_progress"
	checkRunStatusCompleted  = "completed"
)

// checkRunState represents the status and optional conclusion of a GitHub check run
type checkRunState struct {
	status     string
	conclusion string // only set when status is "completed"
}

// NewGithubCommitStatusProvider creates a new instance of CommitStatus for GitHub.
func NewGithubCommitStatusProvider(ctx context.Context, k8sClient client.Client, scmProvider promoterv1alpha1.GenericScmProvider, secret v1.Secret, org string) (*CommitStatus, error) {
	client, _, err := GetClient(ctx, scmProvider, secret, org)
	if err != nil {
		return nil, err
	}

	return &CommitStatus{
		client:    client,
		k8sClient: k8sClient,
	}, nil
}

// Set sets the commit status for a given commit SHA in the specified repository using GitHub Checks API.
// If the SHA hasn't changed and we have an existing check run ID, it will update the existing check run.
// Otherwise, it creates a new check run.
func (cs CommitStatus) Set(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus) (*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, cs.k8sClient, client.ObjectKey{Namespace: commitStatus.Namespace, Name: commitStatus.Spec.RepositoryReference.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	// Determine if we should update an existing check run or create a new one
	shouldUpdate := commitStatus.Status.Sha == commitStatus.Spec.Sha && commitStatus.Status.Id != ""

	if shouldUpdate {
		logger.Info("Updating existing check run via Checks API", "checkRunId", commitStatus.Status.Id)
		return cs.updateCheckRun(ctx, commitStatus, gitRepo)
	}

	logger.Info("Creating new check run via Checks API")
	return cs.createCheckRun(ctx, commitStatus, gitRepo)
}

// createCheckRun creates a new GitHub check run
func (cs CommitStatus) createCheckRun(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus, gitRepo *promoterv1alpha1.GitRepository) (*promoterv1alpha1.CommitStatus, error) {
	state := phaseToCheckRunState(commitStatus.Spec.Phase)

	// Build create-specific options
	checkRunOpts := github.CreateCheckRunOptions{
		Name:    commitStatus.Spec.Name,
		HeadSHA: commitStatus.Spec.Sha,
		Status:  github.Ptr(state.status),
	}

	// Apply common options
	cs.applyCheckRunOptions(&checkRunOpts.DetailsURL, &checkRunOpts.Output,
		&checkRunOpts.Conclusion, &checkRunOpts.CompletedAt, commitStatus, state)

	// Set started_at timestamp for in_progress status
	if state.status == checkRunStatusInProgress {
		now := github.Timestamp{Time: time.Now()}
		checkRunOpts.StartedAt = &now
	}

	start := time.Now()
	checkRun, response, err := cs.client.Checks.CreateCheckRun(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, checkRunOpts)
	return cs.handleCheckRunResponse(ctx, checkRun, response, err, commitStatus, gitRepo, metrics.SCMOperationCreate, start)
}

// updateCheckRun updates an existing GitHub check run
func (cs CommitStatus) updateCheckRun(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus, gitRepo *promoterv1alpha1.GitRepository) (*promoterv1alpha1.CommitStatus, error) {
	// Parse the check run ID
	checkRunID, err := strconv.ParseInt(commitStatus.Status.Id, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse check run ID %q: %w", commitStatus.Status.Id, err)
	}

	state := phaseToCheckRunState(commitStatus.Spec.Phase)

	// Build update-specific options
	updateOpts := github.UpdateCheckRunOptions{
		Name:   commitStatus.Spec.Name,
		Status: github.Ptr(state.status),
	}

	// Apply common options
	cs.applyCheckRunOptions(&updateOpts.DetailsURL, &updateOpts.Output,
		&updateOpts.Conclusion, &updateOpts.CompletedAt, commitStatus, state)

	start := time.Now()
	checkRun, response, err := cs.client.Checks.UpdateCheckRun(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, checkRunID, updateOpts)
	return cs.handleCheckRunResponse(ctx, checkRun, response, err, commitStatus, gitRepo, metrics.SCMOperationUpdate, start)
}

// applyCheckRunOptions applies common options to both create and update check run requests
func (cs CommitStatus) applyCheckRunOptions(
	detailsURL **string, output **github.CheckRunOutput,
	conclusion **string, completedAt **github.Timestamp,
	commitStatus *promoterv1alpha1.CommitStatus, state checkRunState,
) {
	// Add details URL if provided
	if commitStatus.Spec.Url != "" {
		*detailsURL = github.Ptr(commitStatus.Spec.Url)
	}

	// Add output with summary from description
	if commitStatus.Spec.Description != "" {
		*output = &github.CheckRunOutput{
			Title:   github.Ptr(commitStatus.Spec.Name),
			Summary: github.Ptr(commitStatus.Spec.Description),
		}
	}

	// If the status is completed, set the conclusion and timestamp
	if state.status == checkRunStatusCompleted {
		*conclusion = github.Ptr(state.conclusion)
		now := github.Timestamp{Time: time.Now()}
		*completedAt = &now
	}
}

// handleCheckRunResponse handles the response from GitHub check run API calls
func (cs CommitStatus) handleCheckRunResponse(
	ctx context.Context,
	checkRun *github.CheckRun,
	response *github.Response,
	err error,
	commitStatus *promoterv1alpha1.CommitStatus,
	gitRepo *promoterv1alpha1.GitRepository,
	operation metrics.SCMOperation,
	startTime time.Time,
) (*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	if response != nil {
		metrics.RecordSCMCall(gitRepo, metrics.SCMAPICommitStatus, operation, response.StatusCode, time.Since(startTime), getRateLimitMetrics(response.Rate))

		logger.Info("github rate limit",
			"limit", response.Rate.Limit,
			"remaining", response.Rate.Remaining,
			"reset", response.Rate.Reset,
			"url", response.Request.URL)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to %s check run: %w", operation, err)
	}

	logger.V(4).Info("github response status", "status", response.Status)

	commitStatus.Status.Id = strconv.FormatInt(*checkRun.ID, 10)
	commitStatus.Status.Phase = commitStatus.Spec.Phase
	commitStatus.Status.Sha = commitStatus.Spec.Sha
	return commitStatus, nil
}

// phaseToCheckRunState maps CommitStatusPhase to GitHub check run state
func phaseToCheckRunState(phase promoterv1alpha1.CommitStatusPhase) checkRunState {
	switch phase {
	case promoterv1alpha1.CommitPhaseSuccess:
		return checkRunState{
			status:     checkRunStatusCompleted,
			conclusion: string(promoterv1alpha1.CommitPhaseSuccess),
		}
	case promoterv1alpha1.CommitPhaseFailure:
		return checkRunState{
			status:     checkRunStatusCompleted,
			conclusion: string(promoterv1alpha1.CommitPhaseFailure),
		}
	case promoterv1alpha1.CommitPhasePending:
		return checkRunState{
			status:     checkRunStatusInProgress,
			conclusion: "", // no conclusion for non-completed states
		}
	default:
		return checkRunState{
			status:     checkRunStatusQueued,
			conclusion: "", // no conclusion for non-completed states
		}
	}
}
