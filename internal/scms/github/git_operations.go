package github

import (
	"context"
	"fmt"
	"net/http"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v71/github"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

const (
	// githubAppPrivateKeySecretKey is the key in the secret that contains the private key for the GitHub App.
	githubAppPrivateKeySecretKey = "githubAppPrivateKey"
)

// GitAuthenticationProvider provides methods to authenticate with GitHub using a GitHub App.
type GitAuthenticationProvider struct {
	scmProvider v1alpha1.GenericScmProvider
	secret      *v1.Secret
	transport   *ghinstallation.Transport
}

// NewGithubGitAuthenticationProvider creates a new instance of GitAuthenticationProvider for GitHub using the provided SCM provider and secret.
func NewGithubGitAuthenticationProvider(ctx context.Context, k8sClient client.Client, scmProvider v1alpha1.GenericScmProvider, secret *v1.Secret, repoRef client.ObjectKey) GitAuthenticationProvider {
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, k8sClient, client.ObjectKey{Namespace: repoRef.Namespace, Name: repoRef.Name})
	if err != nil {
		panic(fmt.Errorf("failed to get GitRepository: %w", err))
	}

	_, itr, err := GetClient(ctx, scmProvider, *secret, gitRepo.Spec.GitHub.Owner)
	if err != nil {
		panic(fmt.Errorf("failed to create GitHub client: %w", err))
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

// GetGitHttpsRepoUrl constructs the HTTPS URL for a GitHub repository based on the provided GitRepository object.
func (gh GitAuthenticationProvider) GetGitHttpsRepoUrl(gitRepository v1alpha1.GitRepository) string {
	if gh.scmProvider.GetSpec().GitHub != nil && gh.scmProvider.GetSpec().GitHub.Domain != "" {
		return fmt.Sprintf("https://git@%s/%s/%s.git", gh.scmProvider.GetSpec().GitHub.Domain, gitRepository.Spec.GitHub.Owner, gitRepository.Spec.GitHub.Name)
	}
	return fmt.Sprintf("https://git@github.com/%s/%s.git", gitRepository.Spec.GitHub.Owner, gitRepository.Spec.GitHub.Name)
}

// GetToken retrieves the authentication token for GitHub.
func (gh GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	token, err := gh.transport.Token(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get token: %w", err)
	}
	return token, nil
}

// GetUser returns a static user identifier for GitHub authentication.
func (gh GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	return "git", nil
}

// GetClientFromInstallationId creates a new GitHub client with the specified installation ID.
func getClientFromInstallationId(scmProvider v1alpha1.GenericScmProvider, secret v1.Secret, id int64) (*github.Client, *ghinstallation.Transport, error) {
	itr, err := ghinstallation.New(http.DefaultTransport, scmProvider.GetSpec().GitHub.AppID, id, secret.Data[githubAppPrivateKeySecretKey])
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create GitHub installation transport: %w", err)
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
			return nil, nil, fmt.Errorf("failed to create GitHub enterprise client: %w", err)
		}
	}

	return client, itr, nil
}

// installationIds caches installation IDs for organizations to avoid redundant API calls.
var installationIds map[string]int64

// GetClient retrieves a GitHub client for the specified organization using the provided SCM provider and secret.
func GetClient(ctx context.Context, scmProvider v1alpha1.GenericScmProvider, secret v1.Secret, org string) (*github.Client, *ghinstallation.Transport, error) {
	itr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, scmProvider.GetSpec().GitHub.AppID, secret.Data[githubAppPrivateKeySecretKey])
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create GitHub installation transport: %w", err)
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
			return nil, nil, fmt.Errorf("failed to create GitHub enterprise client: %w", err)
		}
	}

	if installationIds == nil {
		installationIds = make(map[string]int64)
	}

	if id, found := installationIds[org]; found {
		return getClientFromInstallationId(scmProvider, secret, id)
	}

	var allInstallations []*github.Installation
	opts := &github.ListOptions{PerPage: 100}

	for {
		installations, resp, err := client.Apps.ListInstallations(ctx, opts)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to list installations: %w", err)
		}

		allInstallations = append(allInstallations, installations...)

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	for _, installation := range allInstallations {
		if installation.Account != nil && installation.Account.Login != nil && installation.ID != nil {
			installationIds[*installation.Account.Login] = *installation.ID
		}
	}

	if id, found := installationIds[org]; found {
		return getClientFromInstallationId(scmProvider, secret, id)
	}

	return nil, nil, fmt.Errorf("installation not found for org: %s", org)
}
