package git

import (
	"context"
	"github.com/argoproj/promoter/api/v1alpha1"
	"github.com/argoproj/promoter/internal/scms"
	"github.com/argoproj/promoter/internal/utils"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type GitOperations struct {
	gap         scms.GitOperationsProvider
	repoRef     *v1alpha1.RepositoryRef
	scmProvider *v1alpha1.ScmProvider
	pathLookup  utils.PathLookup
}

func NewGitOperations(ctx context.Context, k8sClient client.Client, gap scms.GitOperationsProvider, pathLookup utils.PathLookup, repoRef v1alpha1.RepositoryRef, obj v1.Object) (*GitOperations, error) {

	scmProvider, err := utils.GetScmProviderFromRepositoryReference(ctx, k8sClient, repoRef, obj)
	if err != nil {
		return nil, err
	}

	gitOperations := GitOperations{
		gap:         gap,
		scmProvider: scmProvider,
		repoRef:     &repoRef,
		pathLookup:  pathLookup,
	}

	return &gitOperations, nil
}
