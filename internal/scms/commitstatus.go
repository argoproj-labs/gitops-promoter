package scms

import (
	"context"

	"github.com/argoproj/promoter/api/v1alpha1"
)

type CommitStatusProvider interface {
	Set(ctx context.Context, commitStatus *v1alpha1.CommitStatus) (*v1alpha1.CommitStatus, error)
}
