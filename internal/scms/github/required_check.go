package github

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v71/github"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
)

// Compile-time check to ensure RequiredCheck implements scms.RequiredCheckProvider
var _ scms.RequiredCheckProvider = &RequiredCheck{}

// RequiredCheck implements the scms.RequiredCheckProvider interface for GitHub.
// It uses GitHub's Rulesets API to discover required checks and the Checks API to poll status.
type RequiredCheck struct {
	client    *github.Client
	k8sClient client.Client
}

// NewGithubRequiredCheckProvider creates a new instance of RequiredCheck for GitHub.
// It initializes the GitHub client using the provided SCM provider configuration and secret.
func NewGithubRequiredCheckProvider(ctx context.Context, k8sClient client.Client, scmProvider promoterv1alpha1.GenericScmProvider, secret v1.Secret, org string) (*RequiredCheck, error) {
	githubClient, _, err := GetClient(ctx, scmProvider, secret, org)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub client: %w", err)
	}

	return &RequiredCheck{
		client:    githubClient,
		k8sClient: k8sClient,
	}, nil
}

// DiscoverRequiredChecks queries GitHub Rulesets API to discover required status checks
// for the given repository and branch.
//
// Returns empty slice when no rulesets are configured for the branch.
// Returns error for auth failures, rate limits, repository not found, or network errors.
func (rc *RequiredCheck) DiscoverRequiredChecks(ctx context.Context, repo *promoterv1alpha1.GitRepository, branch string) ([]scms.RequiredCheck, error) {
	logger := log.FromContext(ctx)

	if repo.Spec.GitHub == nil {
		return nil, fmt.Errorf("GitRepository does not have GitHub configuration")
	}

	owner := repo.Spec.GitHub.Owner
	name := repo.Spec.GitHub.Name

	// Make the API call
	start := time.Now()
	rules, response, err := rc.client.Repositories.GetRulesForBranch(ctx, owner, name, branch)

	// Record metrics first (even on error)
	if response != nil {
		duration := time.Since(start)
		metrics.RecordSCMCall(repo, metrics.SCMAPIRequiredCheck, metrics.SCMOperationGet,
			response.StatusCode, duration, getRateLimitMetrics(response.Rate))

		logger.Info("github rate limit",
			"limit", response.Rate.Limit,
			"remaining", response.Rate.Remaining,
			"reset", response.Rate.Reset,
			"url", response.Request.URL)

		logger.V(4).Info("github response status", "status", response.Status)
	}

	if err != nil {
		// Note: GitHub returns 200 OK with empty array when no rulesets configured (not 404)
		return nil, fmt.Errorf("failed to get branch rules: %w", err)
	}

	type checkRef struct {
		name  string
		appID *int64
	}

	// Extract required status checks from BranchRules
	var rawChecks []checkRef
	if rules != nil && rules.RequiredStatusChecks != nil {
		for _, ruleStatusCheck := range rules.RequiredStatusChecks {
			if ruleStatusCheck.Parameters.RequiredStatusChecks != nil {
				for _, check := range ruleStatusCheck.Parameters.RequiredStatusChecks {
					if check.Context != "" {
						// Note: GitHub Rulesets API uses "context" (from older Commit Status API)
						// while Check Runs API uses "name". Both refer to the same check identifier.

						// Note: GitHub Rulesets API uses "integration_id" (legacy name from when
						// GitHub Apps were called "GitHub Integrations"). The Check Runs API uses
						// "app_id". Both refer to the same GitHub App numeric identifier.
						rawChecks = append(rawChecks, checkRef{
							name:  check.Context,
							appID: check.IntegrationID,
						})
					}
				}
			}
		}
	}

	// Detect duplicates and generate computed keys
	// Group checks by name to detect duplicates
	checksByName := make(map[string][]checkRef)
	for _, check := range rawChecks {
		checksByName[check.name] = append(checksByName[check.name], check)
	}

	// Generate the Key field based on whether duplicates exist
	var requiredChecks []scms.RequiredCheck
	for _, check := range rawChecks {
		hasDuplicates := len(checksByName[check.name]) > 1
		key := fmt.Sprintf("github-%s", check.name)

		// If duplicates exist and this check has an integration ID, append it
		if hasDuplicates && check.appID != nil {
			key = fmt.Sprintf("%s-%d", key, *check.appID)
		}

		// Store complete reference in ExternalRef for API calls (used in PollCheckStatus)
		// Format: "check-name" or "check-name:integration-id"
		externalRef := check.name
		if check.appID != nil {
			externalRef = fmt.Sprintf("%s:%d", check.name, *check.appID)
		}

		requiredChecks = append(requiredChecks, scms.RequiredCheck{
			Name:        check.name,
			Key:         key,
			ExternalRef: externalRef,
		})
	}

	logger.V(4).Info("Discovered required checks",
		"branch", branch,
		"checks", len(requiredChecks))

	return requiredChecks, nil
}

// PollCheckStatus queries GitHub Checks API to get the status of a specific required check
// for the given commit SHA.
//
// Returns (pending, nil) when no check runs exist yet for the commit.
// Returns error for authentication failures, rate limits, network errors, or other API failures.
func (rc *RequiredCheck) PollCheckStatus(ctx context.Context, repo *promoterv1alpha1.GitRepository, sha string, check scms.RequiredCheck) (promoterv1alpha1.CommitStatusPhase, error) {
	logger := log.FromContext(ctx)

	if repo.Spec.GitHub == nil {
		return promoterv1alpha1.CommitPhasePending, fmt.Errorf("GitRepository does not have GitHub configuration")
	}

	owner := repo.Spec.GitHub.Owner
	name := repo.Spec.GitHub.Name

	// Parse ExternalRef to extract check name and optional app ID
	// Format: "check-name" or "check-name:app-id"
	checkName := check.ExternalRef
	var appID *int64

	if parts := strings.Split(check.ExternalRef, ":"); len(parts) == 2 {
		checkName = parts[0]
		if id, err := strconv.ParseInt(parts[1], 10, 64); err != nil {
			logger.Error(err, "failed to parse app ID from ExternalRef",
				"externalRef", check.ExternalRef,
				"appIDPart", parts[1])
		} else {
			appID = &id
		}
	}

	// Query check runs for the specific check name, filtering by AppID if specified
	// Note: Check Runs API uses "check_name" parameter and "name" field, while
	// Rulesets API uses "context". Both refer to the same check identifier.
	opts := &github.ListCheckRunsOptions{
		CheckName: github.Ptr(checkName),
		AppID:     appID,
		ListOptions: github.ListOptions{
			PerPage: 100, // Maximum per page
		},
	}

	start := time.Now()
	var allCheckRuns []*github.CheckRun

	// Paginate through all check runs
	for {
		checkRuns, response, err := rc.client.Checks.ListCheckRunsForRef(ctx, owner, name, sha, opts)

		// Record metrics for each page
		if response != nil {
			duration := time.Since(start)
			metrics.RecordSCMCall(repo, metrics.SCMAPIRequiredCheck, metrics.SCMOperationList,
				response.StatusCode, duration, getRateLimitMetrics(response.Rate))

			logger.Info("github rate limit",
				"limit", response.Rate.Limit,
				"remaining", response.Rate.Remaining,
				"reset", response.Rate.Reset,
				"url", response.Request.URL)

			logger.V(4).Info("github response status", "status", response.Status)
		}

		if err != nil {
			// Note: GitHub returns 200 OK with empty array when no runs exist yet (not 404)
			return promoterv1alpha1.CommitPhasePending, fmt.Errorf("failed to list check runs for %s: %w", checkName, err)
		}

		if checkRuns != nil && len(checkRuns.CheckRuns) > 0 {
			allCheckRuns = append(allCheckRuns, checkRuns.CheckRuns...)
		}

		// Check if there are more pages
		if response == nil || response.NextPage == 0 {
			break
		}

		opts.Page = response.NextPage
		start = time.Now() // Reset timer for next page
	}

	// If no check runs found, status is pending
	if len(allCheckRuns) == 0 {
		return promoterv1alpha1.CommitPhasePending, nil
	}

	// Get the LATEST check run (most recently started)
	// This ensures we use the status of reruns/retries, not old completed runs
	var latestCheckRun *github.CheckRun
	for _, run := range allCheckRuns {
		if latestCheckRun == nil {
			latestCheckRun = run
			continue
		}

		// Use the run with the most recent StartedAt time
		// If StartedAt is equal or nil, keep the current latestCheckRun
		if run.StartedAt != nil {
			if latestCheckRun.StartedAt == nil || run.StartedAt.After(latestCheckRun.StartedAt.Time) {
				latestCheckRun = run
			}
		}
	}

	phase := mapGitHubCheckStatusToPhase(latestCheckRun)
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
		case "failure", "cancelled", "timed_out":
			return promoterv1alpha1.CommitPhaseFailure
		case "action_required":
			// Note: "action_required" means the check requires manual user action
			// (e.g., approval, acknowledgment) before proceeding. While it blocks merging,
			// it's semantically a waiting state (similar to autoMerge: false) rather than
			// a failure state. We treat it as CommitPhasePending for consistency.
			return promoterv1alpha1.CommitPhasePending
		default:
			return promoterv1alpha1.CommitPhasePending
		}
	}

	// Status is queued or in_progress
	return promoterv1alpha1.CommitPhasePending
}
