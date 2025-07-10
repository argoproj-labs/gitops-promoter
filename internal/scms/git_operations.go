package scms

import (
	"context"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// GitOperationsProvider defines the interface for performing Git operations.
type GitOperationsProvider interface {
	// GetGitHttpsRepoUrl constructs the HTTPS URL for a Git repository based on the provided GitRepository object.
	GetGitHttpsRepoUrl(gitRepo v1alpha1.GitRepository) string
	// GetToken retrieves the authentication token.
	GetToken(ctx context.Context) (string, error)
	// GetUser returns the user name for authentication.
	GetUser(ctx context.Context) (string, error)
}
