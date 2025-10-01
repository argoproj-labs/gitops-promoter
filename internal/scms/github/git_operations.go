package github

import (
	"context"
	"fmt"
	"net/http"
	"sync"

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
func getInstallationClient(scmProvider v1alpha1.GenericScmProvider, secret v1.Secret, id int64) (*github.Client, *ghinstallation.Transport, error) {
	itr, err := ghinstallation.New(http.DefaultTransport, scmProvider.GetSpec().GitHub.AppID, id, secret.Data[githubAppPrivateKeySecretKey])
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create GitHub installation transport: %w", err)
	}

	enterprise, baseUrl, uploadUrl := getUrls(scmProvider.GetSpec().GitHub.Domain)

	var client *github.Client
	if !enterprise {
		client = github.NewClient(&http.Client{Transport: itr})
		return client, itr, nil
	}

	itr.BaseURL = baseUrl
	client, err = github.NewClient(&http.Client{Transport: itr}).WithEnterpriseURLs(baseUrl, uploadUrl)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create GitHub enterprise client: %w", err)
	}
	return client, itr, nil
}

func getUrls(domain string) (enterprise bool, baseUrl, uploadUrl string) {
	if domain == "" {
		return false, "", ""
	}
	baseUrl = fmt.Sprintf("https://%s/api/v3", domain)
	uploadUrl = fmt.Sprintf("https://%s/api/uploads", domain)
	return true, baseUrl, uploadUrl
}

// installationIds caches installation IDs for organizations to avoid redundant API calls.
var installationIds = make(map[orgAppId]int64)

// orgAppId is a composite key of organization and app ID for caching installation IDs.
type orgAppId struct {
	org string
	id  int64
}

// appInstallationIdCacheMutex protects access to the installationIds map.
var appInstallationIdCacheMutex sync.RWMutex

// GetClient retrieves a GitHub client for the specified organization using the provided SCM provider and secret.
// We return a client for API calls and a transport that gets used for git operations via GitAuthenticationProvider.
func GetClient(ctx context.Context, scmProvider v1alpha1.GenericScmProvider, secret v1.Secret, org string) (*github.Client, *ghinstallation.Transport, error) {
	itr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, scmProvider.GetSpec().GitHub.AppID, secret.Data[githubAppPrivateKeySecretKey])
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create GitHub installation transport: %w", err)
	}

	enterprise, baseUrl, uploadUrl := getUrls(scmProvider.GetSpec().GitHub.Domain)

	var client *github.Client
	if !enterprise {
		client = github.NewClient(&http.Client{Transport: itr})
	} else {
		itr.BaseURL = baseUrl
		client, err = github.NewClient(&http.Client{Transport: itr}).WithEnterpriseURLs(baseUrl, uploadUrl)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create GitHub enterprise client: %w", err)
		}
	}

	// If an installation ID is already provided, use it directly.
	if scmProvider.GetSpec().GitHub.InstallationID != 0 {
		return getInstallationClient(scmProvider, secret, scmProvider.GetSpec().GitHub.InstallationID)
	}

	appInstallationIdCacheMutex.RLock()
	if id, found := installationIds[orgAppId{org: org, id: scmProvider.GetSpec().GitHub.AppID}]; found {
		appInstallationIdCacheMutex.RUnlock()
		return getInstallationClient(scmProvider, secret, id)
	}
	appInstallationIdCacheMutex.RUnlock()

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

	// Cache the installation IDs, we take out a lock for the entire loop to avoid locking/unlocking repeatedly. We also include the single
	// read within the write lock.
	appInstallationIdCacheMutex.Lock()
	for _, installation := range allInstallations {
		if installation.Account != nil && installation.Account.Login != nil && installation.ID != nil {
			installationIds[orgAppId{org: *installation.Account.Login, id: scmProvider.GetSpec().GitHub.AppID}] = *installation.ID
		}
	}

	if id, found := installationIds[orgAppId{org: org, id: scmProvider.GetSpec().GitHub.AppID}]; found {
		appInstallationIdCacheMutex.Unlock()
		return getInstallationClient(scmProvider, secret, id)
	}
	appInstallationIdCacheMutex.Unlock()
	return nil, nil, fmt.Errorf("installation not found for org: %s", org)
}
