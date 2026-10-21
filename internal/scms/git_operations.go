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

// GitHTTPHeaderProvider is optionally implemented by a GitOperationsProvider whose SCM authenticates git over HTTPS
// with a request header instead of basic auth.
type GitHTTPHeaderProvider interface {
	// GetGitHTTPHeader returns the URL prefix the header is scoped to and the header itself. An empty header means none.
	GetGitHTTPHeader(ctx context.Context) (urlPrefix, header string, err error)
}
