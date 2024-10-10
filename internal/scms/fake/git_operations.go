package fake

import (
	"context"
	"fmt"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
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

func (gh GitAuthenticationProvider) GetGitHttpsRepoUrl(repoRef v1alpha1.Repository) string {
	gitServerPort := 5000 + GinkgoParallelProcess()
	gitServerPortStr := fmt.Sprintf("%d", gitServerPort)
	if gh.scmProvider.Spec.Fake != nil && gh.scmProvider.Spec.Fake.Domain == "" {
		return fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPortStr, repoRef.Owner, repoRef.Name)
	}
	return fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPortStr, repoRef.Owner, repoRef.Name)
}

func (gh GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	return "", nil
}

func (gh GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	return "git", nil
}
