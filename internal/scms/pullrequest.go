package scms

import (
	"context"
	"time"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// PullRequestProvider defines the interface for managing pull requests in a source control management system.
type PullRequestProvider interface {
	// Create creates a new pull request with the specified title, head, base, and description.
	Create(ctx context.Context, title, head, base, description string, pullRequest v1alpha1.PullRequest) (string, error)
	// Close closes an existing pull request.
	// pullRequest.Status.ID is guaranteed to be set when this is called.
	Close(ctx context.Context, pullRequest v1alpha1.PullRequest) error
	// Update updates an existing pull request with the specified title, description, and pull request details.
	// pullRequest.Status.ID is guaranteed to be set when this is called.
	Update(ctx context.Context, title, description string, pullRequest v1alpha1.PullRequest) error
	// Merge merges an existing pull request with the specified commit message.
	// pullRequest.Status.ID is guaranteed to be set when this is called.
	Merge(ctx context.Context, pullRequest v1alpha1.PullRequest) error
	// FindOpen checks if a pull request is open and returns its status. The returned PullRequestCommonStatus should
	// contain a populated ID and PRCreationTime. All other fields are ignored.
	FindOpen(ctx context.Context, pullRequest v1alpha1.PullRequest) (found bool, id string, creationTime time.Time, err error)
	// GetUrl retrieves the URL of the pull request.
	GetUrl(ctx context.Context, pullRequest v1alpha1.PullRequest) (string, error)
	// AddLabels adds SCM labels to an open pull request, creating missing repository or project labels when needed.
	// pullRequest.Status.ID is guaranteed to be set when this is called.
	AddLabels(ctx context.Context, pullRequest v1alpha1.PullRequest, labels []string) error
	// RemoveLabels removes SCM labels from a pull request.
	// pullRequest.Status.ID is guaranteed to be set when this is called.
	RemoveLabels(ctx context.Context, pullRequest v1alpha1.PullRequest, labels []string) error
}
