package fake

import (
	"context"
	"fmt"
	"github.com/argoproj/promoter/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
)

type GitAuthenticationProvider struct {
	scmProvider *v1alpha1.ScmProvider
	secret      *v1.Secret
}

func NewFakeGitAuthenticationProvider(scmProvider *v1alpha1.ScmProvider, secret *v1.Secret) GitAuthenticationProvider {
	return GitAuthenticationProvider{
		scmProvider: scmProvider,
		secret:      secret,
	}
}

func (gh GitAuthenticationProvider) GetGitHttpsRepoUrl(repoRef v1alpha1.RepositoryRef) string {
	if gh.scmProvider.Spec.Fake != nil && gh.scmProvider.Spec.Fake.Domain == "" {
		return fmt.Sprintf("https://fake.com/%s/%s.git", repoRef.Owner, repoRef.Name)
	}
	return fmt.Sprintf("https://%s/%s/%s.git", gh.scmProvider.Spec.Fake.Domain, repoRef.Owner, repoRef.Name)
}

func (gh GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	return "", nil
}

func (gh GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	return "git", nil
}
