package github

import (
	"context"
	"crypto/sha256"
	"fmt"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/bradleyfalzon/ghinstallation/v2"
	"github.com/google/go-github/v92/github"
	"golang.org/x/sync/singleflight"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// githubAppPrivateKeySecretKey is the key in the secret that contains the private key for the GitHub App.
	githubAppPrivateKeySecretKey = "githubAppPrivateKey"

	// defaultInstallationHitCacheTTL is how long a listed installation ID is reused before ListInstallations runs again.
	defaultInstallationHitCacheTTL = 30 * time.Minute
	// defaultInstallationMissCacheTTL is how long an org missing from that list is remembered before ListInstallations runs again.
	defaultInstallationMissCacheTTL = 1 * time.Minute
	// defaultListInstallationsTimeout bounds one paginated ListInstallations call.
	// A hung page fails that attempt for every caller. Nothing is cached, so the failed reconcile retries later.
	defaultListInstallationsTimeout = time.Minute
)

// installationHitCacheTTL, installationMissCacheTTL, and listInstallationsTimeout are vars so tests can shorten them.
var (
	installationHitCacheTTL  = defaultInstallationHitCacheTTL
	installationMissCacheTTL = defaultInstallationMissCacheTTL
	listInstallationsTimeout = defaultListInstallationsTimeout
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

// appInstallationKey identifies a GitHub App on one host. App IDs are only unique per GitHub instance.
type appInstallationKey struct {
	domain string
	appID  int64
}

func installationCacheKey(scmProvider v1alpha1.GenericScmProvider) appInstallationKey {
	return appInstallationKey{domain: scmProvider.GetSpec().GitHub.Domain, appID: scmProvider.GetSpec().GitHub.AppID}
}

func (k appInstallationKey) flightKey() string {
	return fmt.Sprintf("%s/%d", k.domain, k.appID)
}

// appInstallations is the set of orgs returned by one finished ListInstallations call for a GitHub App.
// The snapshot is stored only after every page succeeds. It serves installation IDs until
// installationHitCacheTTL, and treats orgs absent from byOrg as misses until installationMissCacheTTL.
type appInstallations struct {
	byOrg   map[string]int64
	fetched time.Time
}

// appInstallationCache stores the latest installation snapshot for each GitHub App on a host.
var appInstallationCache = make(map[appInstallationKey]appInstallations)

// appInstallationCacheMu protects appInstallationCache.
var appInstallationCacheMu sync.RWMutex

// listInstallationsGroup coalesces concurrent ListInstallations calls per GitHub host and app ID.
var listInstallationsGroup singleflight.Group

func lookupCachedInstallationID(org string, key appInstallationKey, now time.Time) (int64, bool, error) {
	appInstallationCacheMu.RLock()
	defer appInstallationCacheMu.RUnlock()

	snap, ok := appInstallationCache[key]
	if !ok {
		return 0, false, nil
	}
	age := now.Sub(snap.fetched)
	if id, found := snap.byOrg[org]; found {
		if age < installationHitCacheTTL {
			return id, true, nil
		}
		return 0, false, nil
	}
	if age < installationMissCacheTTL {
		return 0, false, fmt.Errorf("installation of app %d not found for org: %s", key.appID, org)
	}
	return 0, false, nil
}

func installationsByOrg(installations []*github.Installation) map[string]int64 {
	byOrg := make(map[string]int64, len(installations))
	for _, installation := range installations {
		if installation == nil || installation.Account == nil || installation.Account.Login == nil || installation.ID == nil {
			continue
		}
		byOrg[*installation.Account.Login] = *installation.ID
	}
	return byOrg
}

// storeInstallationSnapshot replaces the app's cached installations with a finished list.
func storeInstallationSnapshot(ctx context.Context, byOrg map[string]int64, scmProvider v1alpha1.GenericScmProvider) {
	logger := log.FromContext(ctx)
	for org, id := range byOrg {
		logger.V(4).Info("cached installation ID", "org", org, "id", id, "scmProvider", scmProvider.GetName())
	}

	appInstallationCacheMu.Lock()
	defer appInstallationCacheMu.Unlock()
	appInstallationCache[installationCacheKey(scmProvider)] = appInstallations{byOrg: maps.Clone(byOrg), fetched: time.Now()}
}

func listAndCacheGitHubAppInstallations(ctx context.Context, client *github.Client, scmProvider v1alpha1.GenericScmProvider) error {
	ctx, cancel := context.WithTimeout(ctx, listInstallationsTimeout)
	defer cancel()

	startTime := time.Now()
	opts := &github.ListOptions{PerPage: 100}
	building := make(map[string]int64)
	var lastResp *github.Response

	for {
		installations, resp, err := client.Apps.ListInstallations(ctx, opts)
		if err != nil {
			statusCode := 500
			if resp != nil {
				statusCode = resp.StatusCode
			}
			// Empty api: this call is not a pull request. Nil rate limit: the app JWT budget must not overwrite the installation-token gauges.
			metrics.RecordSCMCall(ctx, scmProvider, "", metrics.SCMOperationListInstallations, statusCode, time.Since(startTime), nil)
			return fmt.Errorf("failed to list installations: %w", err)
		}
		lastResp = resp
		maps.Copy(building, installationsByOrg(installations))
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	storeInstallationSnapshot(ctx, building, scmProvider)

	statusCode := 200
	if lastResp != nil {
		statusCode = lastResp.StatusCode
	}
	// Empty api: this call is not a pull request. Nil rate limit: the app JWT budget must not overwrite the installation-token gauges.
	metrics.RecordSCMCall(ctx, scmProvider, "", metrics.SCMOperationListInstallations, statusCode, time.Since(startTime), nil)
	return nil
}

func resolveInstallationID(ctx context.Context, client *github.Client, scmProvider v1alpha1.GenericScmProvider, org string) (int64, error) {
	key := installationCacheKey(scmProvider)
	if id, found, err := lookupCachedInstallationID(org, key, time.Now()); found || err != nil {
		return id, err
	}

	_, err, _ := listInstallationsGroup.Do(key.flightKey(), func() (any, error) {
		return nil, listAndCacheGitHubAppInstallations(ctx, client, scmProvider)
	})
	if err != nil {
		return 0, err //nolint:wrapcheck // singleflight.Do returns the fn error unchanged
	}

	if id, found, err := lookupCachedInstallationID(org, key, time.Now()); found || err != nil {
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

	if id, found, err := lookupCachedInstallationID(org, installationCacheKey(scmProvider), time.Now()); found {
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
	appInstallationCacheMu.Lock()
	clear(appInstallationCache)
	appInstallationCacheMu.Unlock()

	clientCacheMu.Lock()
	clear(clientCache)
	clientCacheMu.Unlock()

	listInstallationsGroup = singleflight.Group{}
	installationHitCacheTTL = defaultInstallationHitCacheTTL
	installationMissCacheTTL = defaultInstallationMissCacheTTL
	listInstallationsTimeout = defaultListInstallationsTimeout
}
