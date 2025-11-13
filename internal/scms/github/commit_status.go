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
// It uses GitHub's CheckRun API.
type CommitStatus struct {
	client    *github.Client
	k8sClient client.Client
}

const (
	// GitHub Check Run status values (not to be confused with conclusions)
	// Status represents the state of the check run execution
	checkRunStatusQueued     = "queued"      // Check is queued
	checkRunStatusInProgress = "in_progress" // Check is running
	checkRunStatusCompleted  = "completed"   // Check is finished (use conclusion for success/failure)
)

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

	// Determine if we should update an existing check run or create a new one
	shouldUpdate := cs.shouldUpdateCheckRun(commitStatus)

	if shouldUpdate {
		logger.Info("Updating existing check run via Checks API", "checkRunId", commitStatus.Status.Id)
		return cs.updateCheckRun(ctx, commitStatus)
	}

	logger.Info("Creating new check run via Checks API")
	return cs.createCheckRun(ctx, commitStatus)
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
func (cs *CommitStatus) createCheckRun(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus) (*promoterv1alpha1.CommitStatus, error) {
	gitRepo, err := cs.getGitRepository(ctx, commitStatus)
	if err != nil {
		return nil, err
	}

	status := phaseToCheckRunStatus(commitStatus.Spec.Phase)

	// Build create-specific options
	checkRunOpts := github.CreateCheckRunOptions{
		Name:    commitStatus.Spec.Name,
		HeadSHA: commitStatus.Spec.Sha,
		Status:  github.Ptr(status),
	}

	// Set details URL if provided
	if commitStatus.Spec.Url != "" {
		checkRunOpts.DetailsURL = github.Ptr(commitStatus.Spec.Url)
	}

	// If the status is completed, set the conclusion and timestamp
	// The conclusion is the final result: "success" or "failure"
	// This maps directly from the CommitStatus phase (success/failure)
	if status == checkRunStatusCompleted {
		checkRunOpts.Conclusion = github.Ptr(string(commitStatus.Spec.Phase)) // "success" or "failure"
		now := github.Timestamp{Time: time.Now()}
		checkRunOpts.CompletedAt = &now
	}

	// Set started_at timestamp for in_progress status
	if status == checkRunStatusInProgress {
		now := github.Timestamp{Time: time.Now()}
		checkRunOpts.StartedAt = &now
	}

	start := time.Now()
	checkRun, response, err := cs.client.Checks.CreateCheckRun(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, checkRunOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create check run: %w", err)
	}
	return cs.handleCheckRunResponse(ctx, metrics.SCMOperationCreate, start, commitStatus, gitRepo, checkRun, response)
}

// updateCheckRun updates an existing GitHub check run
func (cs *CommitStatus) updateCheckRun(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus) (*promoterv1alpha1.CommitStatus, error) {
	gitRepo, err := cs.getGitRepository(ctx, commitStatus)
	if err != nil {
		return nil, err
	}

	// Parse the check run ID
	checkRunID, err := strconv.ParseInt(commitStatus.Status.Id, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse check run ID %q: %w", commitStatus.Status.Id, err)
	}

	status := phaseToCheckRunStatus(commitStatus.Spec.Phase)

	// Build update-specific options
	updateOpts := github.UpdateCheckRunOptions{
		Name:   commitStatus.Spec.Name,
		Status: github.Ptr(status),
	}

	// Set details URL if provided
	if commitStatus.Spec.Url != "" {
		updateOpts.DetailsURL = github.Ptr(commitStatus.Spec.Url)
	}

	// If the status is completed, set the conclusion and timestamp
	// The conclusion is the final result: "success" or "failure"
	// This maps directly from the CommitStatus phase (success/failure)
	if status == checkRunStatusCompleted {
		updateOpts.Conclusion = github.Ptr(string(commitStatus.Spec.Phase)) // "success" or "failure"
		now := github.Timestamp{Time: time.Now()}
		updateOpts.CompletedAt = &now
	}

	start := time.Now()
	checkRun, response, err := cs.client.Checks.UpdateCheckRun(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, checkRunID, updateOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to update check run: %w", err)
	}
	return cs.handleCheckRunResponse(ctx, metrics.SCMOperationUpdate, start, commitStatus, gitRepo, checkRun, response)
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
) (*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

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

// phaseToCheckRunStatus maps CommitStatusPhase to GitHub check run status.
// Note: This returns the status (queued/in_progress/completed), not the conclusion.
// The conclusion (success/failure) is set separately when status is completed.
//
// Mapping:
//   - success/failure -> "completed" (conclusion will be set to "success" or "failure")
//   - pending -> "in_progress"
//   - default -> "queued"
func phaseToCheckRunStatus(phase promoterv1alpha1.CommitStatusPhase) string {
	switch phase {
	case promoterv1alpha1.CommitPhaseSuccess, promoterv1alpha1.CommitPhaseFailure:
		return checkRunStatusCompleted
	case promoterv1alpha1.CommitPhasePending:
		return checkRunStatusInProgress
	default:
		return checkRunStatusQueued
	}
}
