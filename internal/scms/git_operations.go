package scms

import (
	"context"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

type GitOperationsProvider interface {
	GetGitHttpsRepoUrl(gitRepo v1alpha1.GitRepository) string
	GetToken(ctx context.Context) (string, error)
	GetUser(ctx context.Context) (string, error)
}
