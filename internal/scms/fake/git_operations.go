package fake

import (
	"context"
	"fmt"
	"strconv"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	ginkov2 "github.com/onsi/ginkgo/v2"
	v1 "k8s.io/api/core/v1"
)

// GitAuthenticationProvider implements the scms.GitOperationsProvider interface for testing purposes.
type GitAuthenticationProvider struct {
	scmProvider v1alpha1.GenericScmProvider
	secret      *v1.Secret
}

var _ scms.GitOperationsProvider = &GitAuthenticationProvider{}

// NewFakeGitAuthenticationProvider creates a new instance of GitAuthenticationProvider for testing purposes.
func NewFakeGitAuthenticationProvider(scmProvider v1alpha1.GenericScmProvider, secret *v1.Secret) GitAuthenticationProvider {
	return GitAuthenticationProvider{
		scmProvider: scmProvider,
		secret:      secret,
	}
}

// GetGitHttpsRepoUrl constructs the HTTPS URL for a Git repository in the fake SCM provider.
func (gh GitAuthenticationProvider) GetGitHttpsRepoUrl(gitRepo v1alpha1.GitRepository) string {
	gitServerPort := 5000 + ginkov2.GinkgoParallelProcess()
	gitServerPortStr := strconv.Itoa(gitServerPort)

	if gh.scmProvider.GetSpec().Fake != nil && gh.scmProvider.GetSpec().Fake.Domain == "" {
		return fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPortStr, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)
	}
	return fmt.Sprintf("http://localhost:%s/%s/%s", gitServerPortStr, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name)
}

// GetToken returns an empty string as a placeholder for the Git authentication token.
func (gh GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	return "", nil
}

// GetUser returns a fixed user name for Git authentication.
func (gh GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	return "git", nil
}
