package github

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v90/github"
	"golang.org/x/sync/singleflight"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// githubAppPrivateKeySecretKey is the key in the secret that contains the private key for the GitHub App.
	githubAppPrivateKeySecretKey = "githubAppPrivateKey"

	defaultInstallationMissCacheTTL = 1 * time.Minute
)

// installationMissCacheTTL is how long a missing org+app installation lookup is remembered without re-listing.
var installationMissCacheTTL = defaultInstallationMissCacheTTL

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

// installationIds caches installation IDs for organizations to avoid redundant API calls.
var installationIds = make(map[orgAppId]int64)

// installationMissUntil caches negative lookup results (org not installed for app).
var installationMissUntil = make(map[orgAppId]time.Time)

// orgAppId is a composite key of organization and app ID for caching installation IDs.
type orgAppId struct {
	org string
	id  int64
}

// appInstallationIdCacheMutex protects installationIds and installationMissUntil.
var appInstallationIdCacheMutex sync.RWMutex

// listInstallationsGroup coalesces concurrent ListInstallations calls per app ID.
var listInstallationsGroup singleflight.Group

func lookupCachedInstallationID(org string, appID int64) (int64, bool, error) {
	key := orgAppId{org: org, id: appID}
	appInstallationIdCacheMutex.RLock()
	defer appInstallationIdCacheMutex.RUnlock()
	if id, found := installationIds[key]; found {
		return id, true, nil
	}
	if until, ok := installationMissUntil[key]; ok && time.Now().Before(until) {
		return 0, false, fmt.Errorf("installation of app %d not found for org: %s", appID, org)
	}
	return 0, false, nil
}

func cacheInstallationPage(ctx context.Context, appID int64, installations []*github.Installation, scmProvider v1alpha1.GenericScmProvider) {
	logger := log.FromContext(ctx)
	appInstallationIdCacheMutex.Lock()
	defer appInstallationIdCacheMutex.Unlock()
	for _, installation := range installations {
		if installation.Account != nil && installation.Account.Login != nil && installation.ID != nil {
			installationIds[orgAppId{org: *installation.Account.Login, id: appID}] = *installation.ID
			logger.V(4).Info("cached installation ID", "org", *installation.Account.Login, "id", *installation.ID, "scmProvider", scmProvider.GetName())
		}
	}
}

func listAndCacheGitHubAppInstallations(ctx context.Context, client *github.Client, scmProvider v1alpha1.GenericScmProvider) error {
	appID := scmProvider.GetSpec().GitHub.AppID
	startTime := time.Now()
	opts := &github.ListOptions{PerPage: 100}
	var lastResp *github.Response

	for {
		installations, resp, err := client.Apps.ListInstallations(ctx, opts)
		if err != nil {
			statusCode := 500
			var rateLimit *metrics.RateLimit
			if resp != nil {
				statusCode = resp.StatusCode
				rateLimit = getRateLimitMetrics(resp.Rate)
			}
			metrics.RecordSCMCall(ctx, scmProvider, metrics.SCMAPIPullRequest, metrics.SCMOperationListInstallations, statusCode, time.Since(startTime), rateLimit)
			return fmt.Errorf("failed to list installations: %w", err)
		}
		lastResp = resp
		cacheInstallationPage(ctx, appID, installations, scmProvider)

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	statusCode := 200
	var rateLimit *metrics.RateLimit
	if lastResp != nil {
		statusCode = lastResp.StatusCode
		rateLimit = getRateLimitMetrics(lastResp.Rate)
	}
	metrics.RecordSCMCall(ctx, scmProvider, metrics.SCMAPIPullRequest, metrics.SCMOperationListInstallations, statusCode, time.Since(startTime), rateLimit)
	return nil
}

func resolveInstallationID(ctx context.Context, client *github.Client, scmProvider v1alpha1.GenericScmProvider, org string) (int64, error) {
	appID := scmProvider.GetSpec().GitHub.AppID
	if id, found, err := lookupCachedInstallationID(org, appID); found || err != nil {
		return id, err
	}

	result, err, _ := listInstallationsGroup.Do(fmt.Sprintf("app:%d", appID), func() (any, error) {
		if id, found, err := lookupCachedInstallationID(org, appID); found || err != nil {
			return id, err
		}
		if err := listAndCacheGitHubAppInstallations(ctx, client, scmProvider); err != nil {
			return int64(0), err
		}
		if id, found, err := lookupCachedInstallationID(org, appID); found || err != nil {
			return id, err
		}
		key := orgAppId{org: org, id: appID}
		appInstallationIdCacheMutex.Lock()
		installationMissUntil[key] = time.Now().Add(installationMissCacheTTL)
		appInstallationIdCacheMutex.Unlock()
		return int64(0), fmt.Errorf("installation of app %d not found for org: %s", appID, org)
	})
	if err != nil {
		return 0, err //nolint:wrapcheck // singleflight.Do returns the fn error unchanged
	}
	id, ok := result.(int64)
	if !ok {
		return 0, fmt.Errorf("unexpected installation ID type %T", result)
	}
	return id, nil
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

	if id, found, err := lookupCachedInstallationID(org, scmProvider.GetSpec().GitHub.AppID); found {
		logger.V(4).Info("found cached installation ID", "org", org, "id", id, "scmProvider", scmProvider.GetName())
		return getInstallationClient(scmProvider, secret, id)
	} else if err != nil {
		return nil, nil, err
	}

	id, err := resolveInstallationID(ctx, client, scmProvider, org)
	if err != nil {
		return nil, nil, err
	}
	logger.V(4).Info("found cached installation ID after listing installations", "org", org, "id", id, "scmProvider", scmProvider.GetName())
	return getInstallationClient(scmProvider, secret, id)
}

// resetInstallationCachesForTest clears installation lookup caches between tests.
func resetInstallationCachesForTest() {
	appInstallationIdCacheMutex.Lock()
	clear(installationIds)
	clear(installationMissUntil)
	appInstallationIdCacheMutex.Unlock()

	clientCacheMu.Lock()
	clear(clientCache)
	clientCacheMu.Unlock()

	listInstallationsGroup = singleflight.Group{}
	installationMissCacheTTL = defaultInstallationMissCacheTTL
}
