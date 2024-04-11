package scms

import (
	"context"

	"github.com/argoproj/promoter/api/v1alpha1"
)

type CommitStatusProvider interface {
	Create(ctx context.Context) (*v1alpha1.CommitStatus, error)
}
