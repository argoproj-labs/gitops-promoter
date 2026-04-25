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
	"golang.org/x/sync/singleflight"
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
		return fmt.Sprintf("https://%s/%s/%s.git", gh.scmProvider.GetSpec().GitHub.Domain, gitRepository.Spec.GitHub.Owner, gitRepository.Spec.GitHub.Name)
	}
	return fmt.Sprintf("https://github.com/%s/%s.git", gitRepository.Spec.GitHub.Owner, gitRepository.Spec.GitHub.Name)
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

// installationIds caches installation IDs for organizations to avoid redundant
// API calls.  It is a grow-only cache (entries are written once and read many
// times), which is exactly the use-case for sync.Map.  The map key is
// orgAppId and the value type is int64.
var installationIds sync.Map

// orgAppId is a composite key of organization and app ID for caching installation IDs.
type orgAppId struct {
	org string
	id  int64
}

// appInstallationIdGroup deduplicates concurrent ListInstallations calls that
// share the same GitHub App and domain so only one network round-trip is made
// when many goroutines discover a cold cache at the same time.
var appInstallationIdGroup singleflight.Group

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

	if val, found := installationIds.Load(orgAppId{org: org, id: scmProvider.GetSpec().GitHub.AppID}); found {
		id, ok := val.(int64)
		if !ok {
			return nil, nil, fmt.Errorf("unexpected type in installationIds cache for org %s", org)
		}
		logger.V(4).Info("found cached installation ID", "org", org, "id", id, "scmProvider", scmProvider.GetName())
		return getInstallationClient(scmProvider, secret, id)
	}

	// Slow path: the cache was cold. Use singleflight so that concurrent
	// goroutines that all discovered the empty cache only make one network
	// round-trip between them; the rest join the in-flight call and receive
	// the same result once it completes.
	sfKey := fmt.Sprintf("%d/%s", scmProvider.GetSpec().GitHub.AppID, scmProvider.GetSpec().GitHub.Domain)
	_, sfErr, shared := appInstallationIdGroup.Do(sfKey, func() (any, error) {
		var allInstallations []*github.Installation
		opts := &github.ListOptions{PerPage: 100}

		startTime := time.Now()
		for {
			installations, resp, err := client.Apps.ListInstallations(ctx, opts)
			if err != nil {
				return nil, fmt.Errorf("failed to list installations: %w", err)
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
				installationIds.Store(orgAppId{org: *installation.Account.Login, id: scmProvider.GetSpec().GitHub.AppID}, *installation.ID)
				logger.V(4).Info("cached installation ID", "org", *installation.Account.Login, "id", *installation.ID, "scmProvider", scmProvider.GetName())
			}
		}

		// The installation IDs are stored in the shared cache above; the
		// singleflight return value is not used directly.
		return nil, nil
	})
	if sfErr != nil {
		return nil, nil, fmt.Errorf("failed to list GitHub app installations: %w", sfErr)
	}
	if shared {
		logger.V(4).Info("singleflight deduplicated concurrent installations lookup", "org", org, "scmProvider", scmProvider.GetName())
	}

	val, found := installationIds.Load(orgAppId{org: org, id: scmProvider.GetSpec().GitHub.AppID})
	if !found {
		return nil, nil, fmt.Errorf("installation of app %d not found for org: %s", scmProvider.GetSpec().GitHub.AppID, org)
	}
	id, ok := val.(int64)
	if !ok {
		return nil, nil, fmt.Errorf("unexpected type in installationIds cache for org %s", org)
	}
	logger.V(4).Info("found installation ID after listing installations", "org", org, "id", id, "scmProvider", scmProvider.GetName())
	return getInstallationClient(scmProvider, secret, id)
}
