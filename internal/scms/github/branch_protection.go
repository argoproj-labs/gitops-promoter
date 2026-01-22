package github

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/go-github/v71/github"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
)

// Compile-time check to ensure BranchProtection implements scms.BranchProtectionProvider
var _ scms.BranchProtectionProvider = &BranchProtection{}

// BranchProtection implements the scms.BranchProtectionProvider interface for GitHub.
// It uses GitHub's Rulesets API to discover required checks and the Checks API to poll status.
type BranchProtection struct {
	client    *github.Client
	k8sClient client.Client
}

// NewGithubBranchProtectionProvider creates a new instance of BranchProtection for GitHub.
// It initializes the GitHub client using the provided SCM provider configuration and secret.
func NewGithubBranchProtectionProvider(ctx context.Context, k8sClient client.Client, scmProvider promoterv1alpha1.GenericScmProvider, secret v1.Secret, org string) (*BranchProtection, error) {
	githubClient, _, err := GetClient(ctx, scmProvider, secret, org)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub client: %w", err)
	}

	return &BranchProtection{
		client:    githubClient,
		k8sClient: k8sClient,
	}, nil
}

// DiscoverRequiredChecks queries GitHub Rulesets API to discover required status checks
// for the given repository and branch.
func (bp *BranchProtection) DiscoverRequiredChecks(ctx context.Context, repo *promoterv1alpha1.GitRepository, branch string) ([]scms.BranchProtectionCheck, error) {
	logger := log.FromContext(ctx)

	if repo.Spec.GitHub == nil {
		return nil, fmt.Errorf("GitRepository does not have GitHub configuration")
	}

	owner := repo.Spec.GitHub.Owner
	name := repo.Spec.GitHub.Name

	// Query GetRulesForBranch API
	rules, _, err := bp.client.Repositories.GetRulesForBranch(ctx, owner, name, branch)
	if err != nil {
		// If the branch doesn't have rulesets, return empty list
		logger.V(4).Info("No rulesets found for branch", "branch", branch, "error", err)
		return []scms.BranchProtectionCheck{}, nil
	}

	// Extract required status checks from BranchRules
	var requiredChecks []scms.BranchProtectionCheck
	if rules != nil && rules.RequiredStatusChecks != nil {
		for _, ruleStatusCheck := range rules.RequiredStatusChecks {
			if ruleStatusCheck.Parameters.RequiredStatusChecks != nil {
				for _, check := range ruleStatusCheck.Parameters.RequiredStatusChecks {
					if check.Context != "" {
						// Note: GitHub Rulesets API uses "context" (from older Commit Status API)
						// while Check Runs API uses "name". Both refer to the same check identifier.
						// We map it to "Name" in our generic interface.

						// Convert GitHub's int64 IntegrationID to string
						// Note: GitHub Rulesets API uses "integration_id" (legacy name from when
						// GitHub Apps were called "GitHub Integrations"). The Check Runs API uses
						// "app_id". Both refer to the same GitHub App numeric identifier.
						var integrationID *string
						if check.IntegrationID != nil {
							idStr := strconv.FormatInt(*check.IntegrationID, 10)
							integrationID = &idStr
						}

						requiredChecks = append(requiredChecks, scms.BranchProtectionCheck{
							Name:          check.Context,
							IntegrationID: integrationID,
						})
					}
				}
			}
		}
	}

	return requiredChecks, nil
}

// PollCheckStatus queries GitHub Checks API to get the status of a specific required check
// for the given commit SHA.
func (bp *BranchProtection) PollCheckStatus(ctx context.Context, repo *promoterv1alpha1.GitRepository, sha string, check scms.BranchProtectionCheck) (promoterv1alpha1.CommitStatusPhase, error) {
	logger := log.FromContext(ctx)

	if repo.Spec.GitHub == nil {
		return promoterv1alpha1.CommitPhasePending, fmt.Errorf("GitRepository does not have GitHub configuration")
	}

	owner := repo.Spec.GitHub.Owner
	name := repo.Spec.GitHub.Name

	// Convert string IntegrationID back to int64 for GitHub API
	// Note: The Check Runs API parameter is called "app_id" while the Rulesets API
	// returns "integration_id". Both refer to the same GitHub App numeric identifier.
	// We store it as a string in the generic interface to support other SCM providers.
	var appID *int64
	if check.IntegrationID != nil {
		id, err := strconv.ParseInt(*check.IntegrationID, 10, 64)
		if err != nil {
			logger.Error(err, "failed to parse IntegrationID as int64", "integrationID", *check.IntegrationID)
		} else {
			appID = &id
		}
	}

	// Query check runs for the specific check name, filtering by AppID if specified
	// Note: Check Runs API uses "check_name" parameter and "name" field, while
	// Rulesets API uses "context". Both refer to the same check identifier.
	opts := &github.ListCheckRunsOptions{
		CheckName: github.Ptr(check.Name),
		AppID:     appID,
	}

	checkRuns, _, err := bp.client.Checks.ListCheckRunsForRef(ctx, owner, name, sha, opts)
	if err != nil {
		logger.Error(err, "failed to list check runs", "check", check.Name, "sha", sha, "appID", appID)
		// Default to pending if we can't get the status
		return promoterv1alpha1.CommitPhasePending, nil
	}

	// If no check runs found, status is pending
	if checkRuns == nil || len(checkRuns.CheckRuns) == 0 {
		return promoterv1alpha1.CommitPhasePending, nil
	}

	// Get the latest check run
	checkRun := checkRuns.CheckRuns[0]

	// Map GitHub check status to CommitStatusPhase
	phase := mapGitHubCheckStatusToPhase(checkRun)
	return phase, nil
}

// mapGitHubCheckStatusToPhase maps GitHub check run status to CommitStatusPhase.
func mapGitHubCheckStatusToPhase(checkRun *github.CheckRun) promoterv1alpha1.CommitStatusPhase {
	if checkRun.Status == nil {
		return promoterv1alpha1.CommitPhasePending
	}

	status := *checkRun.Status

	if status == "completed" {
		if checkRun.Conclusion == nil {
			return promoterv1alpha1.CommitPhasePending
		}

		conclusion := *checkRun.Conclusion
		switch conclusion {
		case "success", "neutral", "skipped":
			return promoterv1alpha1.CommitPhaseSuccess
		case "failure", "cancelled", "timed_out", "action_required":
			// Note: "action_required" means the check requires manual user action
			// (e.g., approval, acknowledgment) before proceeding. It blocks merging
			// similar to a failure, so we treat it as CommitPhaseFailure.
			return promoterv1alpha1.CommitPhaseFailure
		default:
			return promoterv1alpha1.CommitPhasePending
		}
	}

	// Status is queued or in_progress
	return promoterv1alpha1.CommitPhasePending
}
