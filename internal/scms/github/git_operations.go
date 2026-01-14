package github

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v71/github"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

const (
	// githubAppPrivateKeySecretKey is the key in the secret that contains the private key for the GitHub App.
	githubAppPrivateKeySecretKey = "githubAppPrivateKey"
)

// GitAuthenticationProvider provides methods to authenticate with GitHub using a GitHub App.
type GitAuthenticationProvider struct {
	scmProvider v1alpha1.GenericScmProvider
	transport   *ghinstallation.Transport
}

// NewGithubGitAuthenticationProvider creates a new instance of GitAuthenticationProvider for GitHub using the provided SCM provider and secret.
func NewGithubGitAuthenticationProvider(ctx context.Context, k8sClient client.Client, scmProvider v1alpha1.GenericScmProvider, secret *v1.Secret, repoRef client.ObjectKey) (GitAuthenticationProvider, error) {
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, k8sClient, client.ObjectKey{Namespace: repoRef.Namespace, Name: repoRef.Name})
	if err != nil {
		return GitAuthenticationProvider{}, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	_, itr, err := GetClient(ctx, scmProvider, *secret, gitRepo.Spec.GitHub.Owner)
	if err != nil {
		return GitAuthenticationProvider{}, fmt.Errorf("failed to create GitHub client: %w", err)
	}

	if scmProvider.GetSpec().GitHub != nil && scmProvider.GetSpec().GitHub.Domain != "" {
		itr.BaseURL = fmt.Sprintf("https://%s/api/v3", scmProvider.GetSpec().GitHub.Domain)
	}

	return GitAuthenticationProvider{
		scmProvider: scmProvider,
		transport:   itr,
	}, nil
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
		return "", fmt.Errorf("failed to get GitHub token for provider %q: %w", gh.scmProvider.GetName(), err)
	}
	return token, nil
}

// GetUser returns a static user identifier for GitHub authentication.
func (gh GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	return "git", nil
}

// getInstallationClient creates a new GitHub client with the specified installation ID.
// It also returns a ghinstallation.Transport, which can be used for git requests.
func getInstallationClient(scmProvider v1alpha1.GenericScmProvider, secret v1.Secret, id int64) (*github.Client, *ghinstallation.Transport, error) {
	if id <= 0 {
		return nil, nil, fmt.Errorf("installation ID is required for scmProvider %q", scmProvider.GetName())
	}

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
	logger := log.FromContext(ctx)

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
		logger.V(4).Info("using provided installation ID", "org", org, "id", scmProvider.GetSpec().GitHub.InstallationID, "scmProvider", scmProvider.GetName())
		return getInstallationClient(scmProvider, secret, scmProvider.GetSpec().GitHub.InstallationID)
	}

	appInstallationIdCacheMutex.RLock()
	if id, found := installationIds[orgAppId{org: org, id: scmProvider.GetSpec().GitHub.AppID}]; found {
		appInstallationIdCacheMutex.RUnlock()
		logger.V(4).Info("found cached installation ID", "org", org, "id", id, "scmProvider", scmProvider.GetName())
		return getInstallationClient(scmProvider, secret, id)
	}
	appInstallationIdCacheMutex.RUnlock()

	var allInstallations []*github.Installation
	opts := &github.ListOptions{PerPage: 100}

	startTime := time.Now()
	// Cache the installation IDs, we take out a lock for the entire loop to avoid locking/unlocking repeatedly. We also include the single
	// read within the write lock.
	// This lock should also help with the fact that on restart we won't slam the GitHub API with multiple requests to list installations.
	appInstallationIdCacheMutex.Lock()
	for {
		installations, resp, err := client.Apps.ListInstallations(ctx, opts)
		if err != nil {
			appInstallationIdCacheMutex.Unlock()
			return nil, nil, fmt.Errorf("failed to list installations: %w", err)
		}

		allInstallations = append(allInstallations, installations...)

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	// We are logging this metric vs using prometheus because this is a provider specific metric that, and we want to try and keep prometheus metrics
	// generic.
	logger.Info("github list installations time", "duration", time.Since(startTime), "count", len(allInstallations))

	for _, installation := range allInstallations {
		if installation.Account != nil && installation.Account.Login != nil && installation.ID != nil {
			installationIds[orgAppId{org: *installation.Account.Login, id: scmProvider.GetSpec().GitHub.AppID}] = *installation.ID
			logger.V(4).Info("cached installation ID", "org", *installation.Account.Login, "id", *installation.ID, "scmProvider", scmProvider.GetName())
		}
	}

	if id, found := installationIds[orgAppId{org: org, id: scmProvider.GetSpec().GitHub.AppID}]; found {
		appInstallationIdCacheMutex.Unlock()
		logger.V(4).Info("found cached installation ID after listing installations", "org", org, "id", id, "scmProvider", scmProvider.GetName())
		return getInstallationClient(scmProvider, secret, id)
	}
	appInstallationIdCacheMutex.Unlock()
	return nil, nil, fmt.Errorf("installation of app %d not found for org: %s", scmProvider.GetSpec().GitHub.AppID, org)
}
