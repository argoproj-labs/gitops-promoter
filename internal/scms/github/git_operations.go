package github

import (
	"context"
	"fmt"
	"net/http"

	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v71/github"
	v1 "k8s.io/api/core/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

const (
	// githubAppPrivateKeySecretKey is the key in the secret that contains the private key for the GitHub App.
	githubAppPrivateKeySecretKey = "githubAppPrivateKey"
)

type GitAuthenticationProvider struct {
	scmProvider v1alpha1.GenericScmProvider
	secret      *v1.Secret
	transport   *ghinstallation.Transport
}

func NewGithubGitAuthenticationProvider(scmProvider v1alpha1.GenericScmProvider, secret *v1.Secret) GitAuthenticationProvider {
	itr, err := ghinstallation.New(http.DefaultTransport, scmProvider.GetSpec().GitHub.AppID, scmProvider.GetSpec().GitHub.InstallationID, secret.Data[githubAppPrivateKeySecretKey])
	if err != nil {
		panic(err)
	}

	if scmProvider.GetSpec().GitHub != nil && scmProvider.GetSpec().GitHub.Domain != "" {
		itr.BaseURL = fmt.Sprintf("https://%s/api/v3", scmProvider.GetSpec().GitHub.Domain)
	}

	return GitAuthenticationProvider{
		scmProvider: scmProvider,
		secret:      secret,
		transport:   itr,
	}
}

func (gh GitAuthenticationProvider) GetGitHttpsRepoUrl(gitRepository v1alpha1.GitRepository) string {
	if gh.scmProvider.GetSpec().GitHub != nil && gh.scmProvider.GetSpec().GitHub.Domain != "" {
		return fmt.Sprintf("https://git@%s/%s/%s.git", gh.scmProvider.GetSpec().GitHub.Domain, gitRepository.Spec.GitHub.Owner, gitRepository.Spec.GitHub.Name)
	}
	return fmt.Sprintf("https://git@github.com/%s/%s.git", gitRepository.Spec.GitHub.Owner, gitRepository.Spec.GitHub.Name)
}

func (gh GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	token, err := gh.transport.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get token: %w", err)
	}
	return token, nil
}

func (gh GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	return "git", nil
}

func GetClient(scmProvider v1alpha1.GenericScmProvider, secret v1.Secret) (*github.Client, error) {
	itr, err := ghinstallation.New(http.DefaultTransport, scmProvider.GetSpec().GitHub.AppID, scmProvider.GetSpec().GitHub.InstallationID, secret.Data[githubAppPrivateKeySecretKey])
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub installation transport: %w", err)
	}

	var client *github.Client
	if scmProvider.GetSpec().GitHub.Domain == "" {
		client = github.NewClient(&http.Client{Transport: itr})
	} else {
		baseURL := fmt.Sprintf("https://%s/api/v3", scmProvider.GetSpec().GitHub.Domain)
		itr.BaseURL = baseURL
		uploadsURL := fmt.Sprintf("https://%s/api/uploads", scmProvider.GetSpec().GitHub.Domain)
		client, err = github.NewClient(&http.Client{Transport: itr}).WithEnterpriseURLs(baseURL, uploadsURL)
		if err != nil {
			return nil, fmt.Errorf("failed to create GitHub enterprise client: %w", err)
		}
	}

	return client, nil
}
