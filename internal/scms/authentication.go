package scms

import (
	"context"
)

type GitAuthenticationProvider interface {
	GetGitAuthentication(ctx context.Context) (string, string, error)
}
