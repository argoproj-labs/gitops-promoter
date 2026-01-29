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
	//   - check: The check to poll (includes name and computed key)
	//
	// Returns the current phase (Success, Failure, Pending) of the check.
	// Returns CommitPhasePending if the check has not yet been reported.
	// Returns ErrNotSupported if the SCM provider does not support status checks.
	PollCheckStatus(ctx context.Context, repo *v1alpha1.GitRepository, sha string, check RequiredCheck) (v1alpha1.CommitStatusPhase, error)
}

// RequiredCheck represents a required status check discovered from protection rules.
type RequiredCheck struct {
	// Name is the raw check identifier from the SCM (e.g., "smoke", "lint", "e2e-test").
	// This is the check name as it appears in the SCM protection rules.
	Name string

	// Key is the computed label key in the format "{provider}-{name}" or "{provider}-{name}-{appID}".
	// This is the value that will be used as the CommitStatus label and selector key.
	// The provider (e.g., "github") detects duplicates and adds the integration ID suffix only when needed.
	//
	// Examples:
	//   - Single check: Name="smoke" → Key="github-smoke"
	//   - Duplicate with integration ID: Name="smoke" → Key="github-smoke-15368"
	//   - Duplicate without integration ID: Name="smoke" → Key="github-smoke"
	Key string

	// ExternalRef is an opaque, complete reference used by the SCM provider for API calls.
	// This is an internal field that contains everything the provider needs to uniquely
	// identify and query the check status. The format is provider-specific.
	// This should never be exposed in Kubernetes API types.
	//
	// Examples:
	//   - GitHub: "check-name" or "check-name:app-id" (e.g., "smoke", "smoke:15368")
	//   - GitLab: "pipeline-id:check-name" or similar
	//   - Bitbucket: "build-key:check-name" or similar
	ExternalRef string
}

// Error constants for required check operations
var (
	// ErrNotSupported indicates that the SCM provider does not support required check operations
	ErrNotSupported = errors.New("required checks not supported by this SCM provider")

	// ErrNoProtection indicates that the branch exists but has no protection rules configured
	ErrNoProtection = errors.New("no protection rules configured for this branch")
)
