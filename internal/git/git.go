package git

import (
	"context"
	"github.com/argoproj/promoter/api/v1alpha1"
	"github.com/argoproj/promoter/internal/scms"
	"github.com/argoproj/promoter/internal/scms/github"
	"github.com/argoproj/promoter/internal/utils"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type GitOperations struct {
	gap         scms.GitAuthenticationProvider
	repoRef     *v1alpha1.RepositoryRef
	scmProvider *v1alpha1.ScmProvider
	repoUrl     string
}

func NewGitOperations(ctx context.Context, k8sClient client.Client, repoRef v1alpha1.RepositoryRef, obj v1.Object) (*GitOperations, error) {

	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, k8sClient, repoRef, obj)
	if err != nil {
		return nil, err
	}

	gitOperations := GitOperations{
		scmProvider: scmProvider,
		repoRef:     &repoRef,
	}

	switch {
	case scmProvider.Spec.GitHub != nil:
		gitOperations.repoUrl, err = github.NewGithubGitAuthenticationProvider(scmProvider, secret).GetGitRepoUrl(ctx, repoRef)
		if err != nil {
			return nil, err
		}
	}

	return &gitOperations, nil
}
