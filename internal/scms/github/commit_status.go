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
func (cs CommitStatus) Set(ctx context.Context, commitStatus *promoterv1alpha1.CommitStatus) (*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)
	logger.Info("Setting Commit Phase via Checks API")

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, cs.k8sClient, client.ObjectKey{Namespace: commitStatus.Namespace, Name: commitStatus.Spec.RepositoryReference.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	// Map CommitStatusPhase to check run status and conclusion
	state := phaseToCheckRunState(commitStatus.Spec.Phase)

	// Create check run options
	checkRunOpts := github.CreateCheckRunOptions{
		Name:    commitStatus.Spec.Name,
		HeadSHA: commitStatus.Spec.Sha,
		Status:  github.Ptr(state.status),
	}

	// Add details URL if provided
	if commitStatus.Spec.Url != "" {
		checkRunOpts.DetailsURL = github.Ptr(commitStatus.Spec.Url)
	}

	// Add output with summary from description
	if commitStatus.Spec.Description != "" {
		checkRunOpts.Output = &github.CheckRunOutput{
			Title:   github.Ptr(commitStatus.Spec.Name),
			Summary: github.Ptr(commitStatus.Spec.Description),
		}
	}

	// If the status is completed, set the conclusion
	if state.status == checkRunStatusCompleted {
		checkRunOpts.Conclusion = github.Ptr(state.conclusion)
		// Set completed_at timestamp
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
	if response != nil {
		metrics.RecordSCMCall(gitRepo, metrics.SCMAPICommitStatus, metrics.SCMOperationCreate, response.StatusCode, time.Since(start), getRateLimitMetrics(response.Rate))
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create check run: %w", err)
	}
	logger.Info("github rate limit",
		"limit", response.Rate.Limit,
		"remaining", response.Rate.Remaining,
		"reset", response.Rate.Reset,
		"url", response.Request.URL)
	logger.V(4).Info("github response status",
		"status", response.Status)

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
