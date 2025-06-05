package fake

import (
	"context"
	"fmt"
	"strconv"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	ginkov2 "github.com/onsi/ginkgo/v2"
	v1 "k8s.io/api/core/v1"
)

type GitAuthenticationProvider struct {
	scmProvider v1alpha1.GenericScmProvider
	secret      *v1.Secret
}

func NewFakeGitAuthenticationProvider(scmProvider v1alpha1.GenericScmProvider, secret *v1.Secret) GitAuthenticationProvider {
	return GitAuthenticationProvider{
		scmProvider: scmProvider,
		secret:      secret,
	}
}

func (gh GitAuthenticationProvider) GetGitHttpsRepoUrl(gitRepo v1alpha1.GitRepository) string {
	gitServerPort := 5000 + ginkov2.GinkgoParallelProcess()
	gitServerPortStr := strconv.Itoa(gitServerPort)

	if gh.scmProvider.GetSpec().Fake != nil && gh.scmProvider.GetSpec().Fake.Domain == "" {
		return fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPortStr, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)
	}
	return fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPortStr, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)
}

func (gh GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	return "", nil
}

func (gh GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	return "git", nil
}
