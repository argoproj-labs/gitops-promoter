package scms

import (
	"context"

	"github.com/argoproj/promoter/api/v1alpha1"
)

type GitOperationsProvider interface {
	GetGitHttpsRepoUrl(repoRef v1alpha1.RepositoryRef) string
	GetToken(ctx context.Context) (string, error)
	GetUser(ctx context.Context) (string, error)
}
