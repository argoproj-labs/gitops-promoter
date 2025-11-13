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
	status, conclusion := phaseToCheckRunStatusAndConclusion(commitStatus.Spec.Phase)

	// Create check run options
	checkRunOpts := github.CreateCheckRunOptions{
		Name:    commitStatus.Spec.Name,
		HeadSHA: commitStatus.Spec.Sha,
		Status:  github.Ptr(status),
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
	if status == checkRunStatusCompleted {
		checkRunOpts.Conclusion = github.Ptr(conclusion)
		// Set completed_at timestamp
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

// phaseToCheckRunStatusAndConclusion maps CommitStatusPhase to GitHub check run status and conclusion
func phaseToCheckRunStatusAndConclusion(phase promoterv1alpha1.CommitStatusPhase) (status string, conclusion string) {
	switch phase {
	case promoterv1alpha1.CommitPhaseSuccess:
		return checkRunStatusCompleted, string(promoterv1alpha1.CommitPhaseSuccess)
	case promoterv1alpha1.CommitPhaseFailure:
		return checkRunStatusCompleted, string(promoterv1alpha1.CommitPhaseFailure)
	default:
		return checkRunStatusQueued, ""
	}
}
