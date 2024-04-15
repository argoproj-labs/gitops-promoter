package git

import (
	"context"
	"github.com/argoproj/promoter/api/v1alpha1"
)

func (g *GitOperations) Clone(ctx context.Context, repoRef *v1alpha1.RepositoryRef) {
	//utils.GetScmProviderAndSecretFromRepositoryReference()
}
