package scms

import (
	"context"
	"github.com/argoproj/promoter/api/v1alpha1"
)

type GitAuthenticationProvider interface {
	GetGitRepoUrl(context.Context, v1alpha1.RepositoryRef) (string, error)
}
