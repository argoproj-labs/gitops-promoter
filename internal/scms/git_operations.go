package scms

import (
	"context"

	"github.com/zachaller/promoter/api/v1alpha1"
)

type GitOperationsProvider interface {
	GetGitHttpsRepoUrl(repoRef v1alpha1.Repository) string
	GetToken(ctx context.Context) (string, error)
	GetUser(ctx context.Context) (string, error)
}
