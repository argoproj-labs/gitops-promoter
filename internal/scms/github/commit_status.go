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

// Compile-time check to ensure CommitStatus implements scms.CommitStatusProvider
var _ scms.CommitStatusProvider = (*CommitStatus)(nil)

// CommitStatus implements the scms.CommitStatusProvider interface for GitHub.
// It uses the GitHub Checks API to create and update commit status checks.
type CommitStatus struct {
	client    *github.Client
	k8sClient client.Client
}

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
// It initializes the GitHub client using the provided SCM provider configuration and secret.
func NewGithubCommitStatusProvider(ctx context.Context, k8sClient client.Client, scmProvider promoterv1alpha1.GenericScmProvider, secret v1.Secret, org string) (*CommitStatus, error) {
	githubClient, _, err := GetClient(ctx, scmProvider, secret, org)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub client: %w", err)
	}

	return &CommitStatus{
		client:    githubClient,
		k8sClient: k8sClient,
	}, nil
}

// Set sets the commit status for a given commit SHA in the specified repository using GitHub Checks API.
// If the SHA hasn't changed and we have an existing check run ID, it will update the existing check run.
// Otherwise, it creates a new check run.
func (cs *CommitStatus) Set(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus) (*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	gitRepo, err := cs.getGitRepository(ctx, commitStatus)
	if err != nil {
		return nil, err
	}

	// Determine if we should update an existing check run or create a new one
	shouldUpdate := cs.shouldUpdateCheckRun(commitStatus)

	if shouldUpdate {
		logger.Info("Updating existing check run via Checks API", "checkRunId", commitStatus.Status.Id)
		return cs.updateCheckRun(ctx, commitStatus, gitRepo)
	}

	logger.Info("Creating new check run via Checks API")
	return cs.createCheckRun(ctx, commitStatus, gitRepo)
}

// getGitRepository retrieves the GitRepository resource for the given CommitStatus
func (cs *CommitStatus) getGitRepository(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus) (*promoterv1alpha1.GitRepository, error) {
	objectKey := client.ObjectKey{
		Namespace: commitStatus.Namespace,
		Name:      commitStatus.Spec.RepositoryReference.Name,
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, cs.k8sClient, objectKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	return gitRepo, nil
}

// shouldUpdateCheckRun determines if we should update an existing check run or create a new one
func (cs *CommitStatus) shouldUpdateCheckRun(commitStatus *promoterv1alpha1.CommitStatus) bool {
	return commitStatus.Status.Sha == commitStatus.Spec.Sha && commitStatus.Status.Id != ""
}

// createCheckRun creates a new GitHub check run
func (cs *CommitStatus) createCheckRun(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus, gitRepo *promoterv1alpha1.GitRepository) (*promoterv1alpha1.CommitStatus, error) {
	state := phaseToCheckRunState(commitStatus.Spec.Phase)

	// Build create-specific options
	checkRunOpts := github.CreateCheckRunOptions{
		Name:    commitStatus.Spec.Name,
		HeadSHA: commitStatus.Spec.Sha,
		Status:  github.Ptr(state.status),
	}

	// Set details URL if provided
	if commitStatus.Spec.Url != "" {
		checkRunOpts.DetailsURL = github.Ptr(commitStatus.Spec.Url)
	}

	// If the status is completed, set the conclusion and timestamp
	if state.status == checkRunStatusCompleted {
		checkRunOpts.Conclusion = github.Ptr(state.conclusion)
		now := github.Timestamp{Time: time.Now()}
		checkRunOpts.CompletedAt = &now
	}

	// Set started_at timestamp for in_progress status
	if state.status == checkRunStatusInProgress {
		now := github.Timestamp{Time: time.Now()}
		checkRunOpts.StartedAt = &now
	}

	start := time.Now()
	checkRun, response, err := cs.client.Checks.CreateCheckRun(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, checkRunOpts)
	return cs.handleCheckRunResponse(ctx, metrics.SCMOperationCreate, start, commitStatus, gitRepo, checkRun, response, err)
}

// updateCheckRun updates an existing GitHub check run
func (cs *CommitStatus) updateCheckRun(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus, gitRepo *promoterv1alpha1.GitRepository) (*promoterv1alpha1.CommitStatus, error) {
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

	// Set details URL if provided
	if commitStatus.Spec.Url != "" {
		updateOpts.DetailsURL = github.Ptr(commitStatus.Spec.Url)
	}

	// If the status is completed, set the conclusion and timestamp
	if state.status == checkRunStatusCompleted {
		updateOpts.Conclusion = github.Ptr(state.conclusion)
		now := github.Timestamp{Time: time.Now()}
		updateOpts.CompletedAt = &now
	}

	start := time.Now()
	checkRun, response, err := cs.client.Checks.UpdateCheckRun(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, checkRunID, updateOpts)
	return cs.handleCheckRunResponse(ctx, metrics.SCMOperationUpdate, start, commitStatus, gitRepo, checkRun, response, err)
}

// handleCheckRunResponse handles the response from GitHub check run API calls
// It records metrics, logs rate limit information, and updates the commit status
func (cs *CommitStatus) handleCheckRunResponse(
	ctx context.Context,
	operation metrics.SCMOperation,
	startTime time.Time,
	commitStatus *promoterv1alpha1.CommitStatus,
	gitRepo *promoterv1alpha1.GitRepository,
	checkRun *github.CheckRun,
	response *github.Response,
	err error,
) (*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	// Handle error first
	if err != nil {
		return nil, fmt.Errorf("failed to %s check run: %w", operation, err)
	}

	// Record metrics and log rate limit info if response is available
	if response != nil {
		duration := time.Since(startTime)
		metrics.RecordSCMCall(gitRepo, metrics.SCMAPICommitStatus, operation, response.StatusCode, duration, getRateLimitMetrics(response.Rate))

		logger.Info("github rate limit",
			"limit", response.Rate.Limit,
			"remaining", response.Rate.Remaining,
			"reset", response.Rate.Reset,
			"url", response.Request.URL)

		logger.V(4).Info("github response status", "status", response.Status)
	}

	// Update commit status with check run information
	if checkRun != nil && checkRun.ID != nil {
		commitStatus.Status.Id = strconv.FormatInt(*checkRun.ID, 10)
		commitStatus.Status.Phase = commitStatus.Spec.Phase
		commitStatus.Status.Sha = commitStatus.Spec.Sha
	}

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
