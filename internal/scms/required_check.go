package scms

import (
	"context"
	"errors"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// RequiredCheckProvider discovers and monitors required checks from SCM protection rules.
// This interface allows different SCM providers (GitHub, GitLab, etc.) to implement
// required check discovery and status polling in their own way.
type RequiredCheckProvider interface {
	// DiscoverRequiredChecks queries the SCM's protection configuration
	// to find all required status checks for the given repository and branch.
	// Returns a list of check names that are required by protection rules.
	//
	// Returns ErrNotSupported if the SCM provider does not support required checks discovery.
	// Returns ErrNoProtection if the branch exists but has no protection rules configured.
	DiscoverRequiredChecks(ctx context.Context, repo *v1alpha1.GitRepository, branch string) ([]RequiredCheck, error)

	// PollCheckStatus queries the current status of a specific required check
	// for a given commit SHA in the repository.
	//
	// Parameters:
	//   - ctx: Context for the request
	//   - repo: The GitRepository to query
	//   - sha: The commit SHA to check status for
	//   - check: The check to poll (includes name and optional IntegrationID)
	//
	// Returns the current phase (Success, Failure, Pending) of the check.
	// Returns CommitPhasePending if the check has not yet been reported.
	// Returns ErrNotSupported if the SCM provider does not support status checks.
	PollCheckStatus(ctx context.Context, repo *v1alpha1.GitRepository, sha string, check RequiredCheck) (v1alpha1.CommitStatusPhase, error)
}

// RequiredCheck represents a required status check discovered from protection rules.
type RequiredCheck struct {
	// Provider is the SCM provider name (e.g., "github", "gitlab", "bitbucket").
	// This is used to generate user-friendly check labels in the format: {provider}-{name}.
	// If not set, defaults to "required-status-check".
	Provider string

	// Name is the check identifier (e.g., "ci-tests", "security-scan", "build/linux")
	Name string

	// IntegrationID is the SCM application/integration identifier that must provide this check.
	// This is used to distinguish between multiple checks with the same name
	// from different applications or integrations.
	// A nil value means any application can provide the check.
	//
	// SCM provider-specific interpretations:
	// - GitHub: GitHub App ID as string (e.g., "15368" for GitHub Actions)
	// - GitLab: Context/name identifier (e.g., "bundler:audit", "test")
	// - Bitbucket: Build key identifier (e.g., "BAMBOO-PLAN", "jenkins-build")
	IntegrationID *string
}

// Error constants for required check operations
var (
	// ErrNotSupported indicates that the SCM provider does not support required check operations
	ErrNotSupported = errors.New("required checks not supported by this SCM provider")

	// ErrNoProtection indicates that the branch exists but has no protection rules configured
	ErrNoProtection = errors.New("no protection rules configured for this branch")
)
