package github

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v93/github"
	"golang.org/x/sync/singleflight"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// githubAppPrivateKeySecretKey is the key in the secret that contains the private key for the GitHub App.
	githubAppPrivateKeySecretKey = "githubAppPrivateKey"

	// defaultInstallationHitCacheTTL is how long a found installation ID is reused before it is looked up again.
	defaultInstallationHitCacheTTL = 30 * time.Minute
	// defaultInstallationMissCacheTTL is how long an account without the app installed is remembered before it is looked up again.
	defaultInstallationMissCacheTTL = 1 * time.Minute
	// defaultFindInstallationTimeout bounds one installation lookup, including the user fallback.
	// A hung request fails that attempt for every caller waiting on it. Nothing is cached, so the failed reconcile retries later.
	defaultFindInstallationTimeout = time.Minute
)

// installationHitCacheTTL, installationMissCacheTTL, and findInstallationTimeout are vars so tests can shorten them.
var (
	installationHitCacheTTL  = defaultInstallationHitCacheTTL
	installationMissCacheTTL = defaultInstallationMissCacheTTL
	findInstallationTimeout  = defaultFindInstallationTimeout
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

type clientCacheKey struct {
	domain           string
	privKeyHash      [32]byte
	appID, installID int64
}
type clientCacheClients struct {
	itr *ghinstallation.Transport
	gh  *github.Client
}

var (
	clientCacheMu sync.Mutex
	clientCache   = make(map[clientCacheKey]clientCacheClients)
)

func newTransport(domain string, appID, installationID int64, privateKey []byte) (*github.Client, *ghinstallation.Transport, error) {
	key := clientCacheKey{domain, sha256.Sum256(privateKey), appID, installationID}

	clientCacheMu.Lock()
	defer clientCacheMu.Unlock()

	if val, ok := clientCache[key]; ok {
		return val.gh, val.itr, nil
	}

	tr := http.DefaultTransport
	itr, err := ghinstallation.New(tr, appID, installationID, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create github app %d installation %d transport: %w", appID, installationID, err)
	}

	enterprise, baseURL, uploadURL := getUrls(domain)
	var client *github.Client
	if !enterprise {
		client, err = github.NewClient(github.WithHTTPClient(&http.Client{Transport: itr}))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create GitHub client: %w", err)
		}
	} else {
		itr.BaseURL = baseURL
		client, err = github.NewClient(
			github.WithHTTPClient(&http.Client{Transport: itr}),
			github.WithEnterpriseURLs(baseURL, uploadURL),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create GitHub enterprise client: %w", err)
		}
	}

	clientCache[key] = clientCacheClients{
		itr: itr,
		gh:  client,
	}
	return client, itr, nil
}

// getInstallationClient returns a possibly cached GitHub client with the specified installation ID.
// It also returns a ghinstallation.Transport, which can be used for git requests.
func getInstallationClient(scmProvider v1alpha1.GenericScmProvider, secret v1.Secret, id int64) (*github.Client, *ghinstallation.Transport, error) {
	if id <= 0 {
		return nil, nil, fmt.Errorf("installation ID is required for scmProvider %q", scmProvider.GetName())
	}

	return newTransport(scmProvider.GetSpec().GitHub.Domain, scmProvider.GetSpec().GitHub.AppID, id, secret.Data[githubAppPrivateKeySecretKey])
}

func getUrls(domain string) (enterprise bool, baseUrl, uploadUrl string) {
	if domain == "" {
		return false, "", ""
	}
	baseUrl = fmt.Sprintf("https://%s/api/v3", domain)
	uploadUrl = fmt.Sprintf("https://%s/api/uploads", domain)
	return true, baseUrl, uploadUrl
}

// installationKey identifies one account's installation of a GitHub App on one host. App IDs are only unique per
// GitHub instance, and GitHub logins are case-insensitive.
type installationKey struct {
	domain string
	owner  string
	appID  int64
}

func newInstallationKey(scmProvider v1alpha1.GenericScmProvider, owner string) installationKey {
	return installationKey{domain: scmProvider.GetSpec().GitHub.Domain, appID: scmProvider.GetSpec().GitHub.AppID, owner: strings.ToLower(owner)}
}

func (k installationKey) flightKey() string {
	return fmt.Sprintf("%s/%d/%s", k.domain, k.appID, k.owner)
}

// cachedInstallation is the result of one finished installation lookup. An id of 0 means the app is not installed
// on the account. It serves the ID until installationHitCacheTTL, and the miss until installationMissCacheTTL.
type cachedInstallation struct {
	fetched time.Time
	id      int64
}

// installationCache stores the latest installation lookup for each account and GitHub App on a host.
var installationCache = make(map[installationKey]cachedInstallation)

// installationCacheMu protects installationCache.
var installationCacheMu sync.RWMutex

// findInstallationGroup coalesces concurrent installation lookups per GitHub host, app ID, and account.
var findInstallationGroup singleflight.Group

func lookupCachedInstallationID(key installationKey, now time.Time) (int64, bool, error) {
	installationCacheMu.RLock()
	defer installationCacheMu.RUnlock()

	cached, ok := installationCache[key]
	if !ok {
		return 0, false, nil
	}
	age := now.Sub(cached.fetched)
	if cached.id != 0 {
		if age < installationHitCacheTTL {
			return cached.id, true, nil
		}
		return 0, false, nil
	}
	if age < installationMissCacheTTL {
		return 0, false, fmt.Errorf("installation of app %d not found for org: %s", key.appID, key.owner)
	}
	return 0, false, nil
}

// storeInstallation caches a finished lookup. id is 0 when the app is not installed on the account.
func storeInstallation(key installationKey, id int64) {
	installationCacheMu.Lock()
	defer installationCacheMu.Unlock()
	installationCache[key] = cachedInstallation{id: id, fetched: time.Now()}
}

// findInstallation fetches the app's installation on owner, trying the organization endpoint first and the user
// endpoint when the owner is not an organization. It returns 0 without an error when the app is not installed.
func findInstallation(ctx context.Context, client *github.Client, scmProvider v1alpha1.GenericScmProvider, owner string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, findInstallationTimeout)
	defer cancel()

	lookups := []struct {
		find      func(context.Context, string) (*github.Installation, *github.Response, error)
		operation metrics.GitHubSCMProviderOperation
	}{
		{client.Apps.GetOrganizationInstallation, metrics.GitHubSCMProviderOperationGetOrgInstallation},
		{client.Apps.GetUserInstallation, metrics.GitHubSCMProviderOperationGetUserInstallation},
	}
	for _, lookup := range lookups {
		startTime := time.Now()
		installation, resp, err := lookup.find(ctx, owner)
		statusCode := 500
		if resp != nil {
			statusCode = resp.StatusCode
		}
		// Provider api: Nil rate limit: the app JWT budget must not overwrite the installation-token gauges.
		metrics.RecordSCMCall(ctx, scmProvider, metrics.SCMAPIProvider, lookup.operation, statusCode, time.Since(startTime), nil)
		if statusCode == http.StatusNotFound {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("failed to get installation of app %d for %s: %w", scmProvider.GetSpec().GitHub.AppID, owner, err)
		}
		return installation.GetID(), nil
	}
	return 0, nil
}

func resolveInstallationID(ctx context.Context, client *github.Client, scmProvider v1alpha1.GenericScmProvider, org string) (int64, error) {
	key := newInstallationKey(scmProvider, org)
	if id, found, err := lookupCachedInstallationID(key, time.Now()); found || err != nil {
		return id, err
	}

	_, err, _ := findInstallationGroup.Do(key.flightKey(), func() (any, error) {
		id, err := findInstallation(ctx, client, scmProvider, org)
		if err != nil {
			return nil, err
		}
		log.FromContext(ctx).V(4).Info("cached installation ID", "org", org, "id", id, "scmProvider", scmProvider.GetName())
		storeInstallation(key, id)
		return nil, nil
	})
	if err != nil {
		return 0, err //nolint:wrapcheck // singleflight.Do returns the fn error unchanged
	}

	if id, found, err := lookupCachedInstallationID(key, time.Now()); found || err != nil {
		return id, err
	}
	return 0, fmt.Errorf("installation of app %d not found for org: %s", key.appID, org)
}

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
		client, err = github.NewClient(github.WithHTTPClient(&http.Client{Transport: itr}))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create GitHub client: %w", err)
		}
	} else {
		itr.BaseURL = baseUrl
		client, err = github.NewClient(
			github.WithHTTPClient(&http.Client{Transport: itr}),
			github.WithEnterpriseURLs(baseUrl, uploadUrl),
		)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create GitHub enterprise client: %w", err)
		}
	}

	// If an installation ID is already provided, use it directly.
	if scmProvider.GetSpec().GitHub.InstallationID != 0 {
		logger.V(4).Info("using provided installation ID", "org", org, "id", scmProvider.GetSpec().GitHub.InstallationID, "scmProvider", scmProvider.GetName())
		return getInstallationClient(scmProvider, secret, scmProvider.GetSpec().GitHub.InstallationID)
	}

	if id, found, err := lookupCachedInstallationID(newInstallationKey(scmProvider, org), time.Now()); found {
		logger.V(4).Info("found cached installation ID", "org", org, "id", id, "scmProvider", scmProvider.GetName())
		return getInstallationClient(scmProvider, secret, id)
	} else if err != nil {
		return nil, nil, err
	}

	id, err := resolveInstallationID(ctx, client, scmProvider, org)
	if err != nil {
		return nil, nil, err
	}
	logger.V(4).Info("found installation ID after looking it up", "org", org, "id", id, "scmProvider", scmProvider.GetName())
	return getInstallationClient(scmProvider, secret, id)
}

// resetInstallationCachesForTest clears installation lookup caches between tests.
func resetInstallationCachesForTest() {
	installationCacheMu.Lock()
	clear(installationCache)
	installationCacheMu.Unlock()

	clientCacheMu.Lock()
	clear(clientCache)
	clientCacheMu.Unlock()

	findInstallationGroup = singleflight.Group{}
	installationHitCacheTTL = defaultInstallationHitCacheTTL
	installationMissCacheTTL = defaultInstallationMissCacheTTL
	findInstallationTimeout = defaultFindInstallationTimeout
}
