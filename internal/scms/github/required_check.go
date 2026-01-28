package github

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/google/go-github/v71/github"
	"golang.org/x/sync/singleflight"
	v1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
)

var (
	// requiredCheckCache stores required check discovery results
	requiredCheckCache = make(map[string]*requiredCheckCacheEntry)

	// requiredCheckCacheMutex protects access to requiredCheckCache and cache configuration
	requiredCheckCacheMutex sync.RWMutex

	// requiredCheckCacheTTL is how long to cache required check discovery results
	requiredCheckCacheTTL = 15 * time.Minute // Default, updated from config

	// requiredCheckCacheMaxSize is the maximum number of entries in the cache
	requiredCheckCacheMaxSize = 1000 // Default, updated from config

	// requiredCheckFlight prevents duplicate concurrent API calls for the same cache key
	requiredCheckFlight singleflight.Group
)

type requiredCheckCacheEntry struct {
	checks    []scms.RequiredCheck
	expiresAt time.Time
}

// Compile-time check to ensure RequiredCheck implements scms.RequiredCheckProvider
var _ scms.RequiredCheckProvider = &RequiredCheck{}

// RequiredCheck implements the scms.RequiredCheckProvider interface for GitHub.
// It uses GitHub's Rulesets API to discover required checks and the Checks API to poll status.
type RequiredCheck struct {
	client    *github.Client
	k8sClient client.Client

	// GitHub instance domain (empty for github.com, custom domain for Enterprise)
	domain string
}

// NewGithubRequiredCheckProvider creates a new instance of RequiredCheck for GitHub.
// It initializes the GitHub client using the provided SCM provider configuration and secret.
// cacheTTL specifies how long to cache required check discovery results (0 disables caching).
// cacheMaxSize specifies the maximum number of cache entries (0 for unlimited).
func NewGithubRequiredCheckProvider(ctx context.Context, k8sClient client.Client, scmProvider promoterv1alpha1.GenericScmProvider, secret v1.Secret, org string, cacheTTL time.Duration, cacheMaxSize int) (*RequiredCheck, error) {
	githubClient, _, err := GetClient(ctx, scmProvider, secret, org)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub client: %w", err)
	}

	// Store domain to distinguish between different GitHub instances in cache
	domain := scmProvider.GetSpec().GitHub.Domain
	if domain == "" {
		domain = "github.com" // Normalize empty domain to explicit github.com
	}

	// Update package-level cache configuration (thread-safe)
	// This ensures all provider instances use the latest configuration
	requiredCheckCacheMutex.Lock()
	requiredCheckCacheTTL = cacheTTL
	requiredCheckCacheMaxSize = cacheMaxSize
	requiredCheckCacheMutex.Unlock()

	return &RequiredCheck{
		client:    githubClient,
		k8sClient: k8sClient,
		domain:    domain,
	}, nil
}

// DiscoverRequiredChecks queries GitHub Rulesets API to discover required status checks
// for the given repository and branch.
//
// Returns empty slice when no rulesets are configured for the branch.
// Returns error for auth failures, rate limits, repository not found, or network errors.
func (rc *RequiredCheck) DiscoverRequiredChecks(ctx context.Context, repo *promoterv1alpha1.GitRepository, branch string) ([]scms.RequiredCheck, error) {
	logger := log.FromContext(ctx)

	if repo.Spec.GitHub == nil {
		return nil, fmt.Errorf("GitRepository does not have GitHub configuration")
	}

	owner := repo.Spec.GitHub.Owner
	name := repo.Spec.GitHub.Name
	cacheKey := fmt.Sprintf("%s|%s|%s|%s", rc.domain, owner, name, branch)

	// Fast path: check cache with read lock first
	requiredCheckCacheMutex.RLock()
	if entry, found := requiredCheckCache[cacheKey]; found {
		if time.Now().Before(entry.expiresAt) {
			// Cache hit with valid entry
			requiredCheckCacheMutex.RUnlock()
			logger.V(4).Info("Using cached required check discovery",
				"branch", branch,
				"checks", len(entry.checks),
				"expiresIn", time.Until(entry.expiresAt).Round(time.Second))
			return entry.checks, nil
		}
	}
	requiredCheckCacheMutex.RUnlock()

	// Cache miss or expired - use singleflight to prevent duplicate API calls
	result, err, _ := requiredCheckFlight.Do(cacheKey, func() (interface{}, error) {
		// Make the API call
		start := time.Now()
		rules, response, err := rc.client.Repositories.GetRulesForBranch(ctx, owner, name, branch)

		// Record metrics first (even on error)
		if response != nil {
			duration := time.Since(start)
			metrics.RecordSCMCall(repo, metrics.SCMAPIRequiredCheck, metrics.SCMOperationGet,
				response.StatusCode, duration, getRateLimitMetrics(response.Rate))

			logger.Info("github rate limit",
				"limit", response.Rate.Limit,
				"remaining", response.Rate.Remaining,
				"reset", response.Rate.Reset,
				"url", response.Request.URL)

			logger.V(4).Info("github response status", "status", response.Status)
		}

		if err != nil {
			// Note: GitHub returns 200 OK with empty array when no rulesets configured (not 404)
			return nil, fmt.Errorf("failed to get branch rules: %w", err)
		}

		// Extract required status checks from BranchRules
		var requiredChecks []scms.RequiredCheck
		if rules != nil && rules.RequiredStatusChecks != nil {
			for _, ruleStatusCheck := range rules.RequiredStatusChecks {
				if ruleStatusCheck.Parameters.RequiredStatusChecks != nil {
					for _, check := range ruleStatusCheck.Parameters.RequiredStatusChecks {
						if check.Context != "" {
							// Note: GitHub Rulesets API uses "context" (from older Commit Status API)
							// while Check Runs API uses "name". Both refer to the same check identifier.
							// We map it to "Name" in our generic interface.

							// Convert GitHub's int64 IntegrationID to string
							// Note: GitHub Rulesets API uses "integration_id" (legacy name from when
							// GitHub Apps were called "GitHub Integrations"). The Check Runs API uses
							// "app_id". Both refer to the same GitHub App numeric identifier.
							var integrationID *string
							if check.IntegrationID != nil {
								idStr := strconv.FormatInt(*check.IntegrationID, 10)
								integrationID = &idStr
							}

							requiredChecks = append(requiredChecks, scms.RequiredCheck{
								Name:          check.Context,
								IntegrationID: integrationID,
							})
						}
					}
				}
			}
		}

		// Store in cache before returning (write lock)
		requiredCheckCacheMutex.Lock()

		requiredCheckCache[cacheKey] = &requiredCheckCacheEntry{
			checks:    requiredChecks,
			expiresAt: time.Now().Add(requiredCheckCacheTTL),
		}

		// Check if we need to enforce cache size limit after insertion
		// to avoid race condition where cache temporarily exceeds max size
		if requiredCheckCacheMaxSize > 0 && len(requiredCheckCache) > requiredCheckCacheMaxSize {
			evictExpiredOrOldestEntries(ctx)
		}
		requiredCheckCacheMutex.Unlock()

		logger.V(4).Info("Cached required check discovery",
			"branch", branch,
			"checks", len(requiredChecks),
			"ttl", requiredCheckCacheTTL)

		return requiredChecks, nil
	})

	if err != nil {
		return nil, err
	}

	return result.([]scms.RequiredCheck), nil
}

// PollCheckStatus queries GitHub Checks API to get the status of a specific required check
// for the given commit SHA.
//
// Returns (pending, nil) when no check runs exist yet for the commit.
// Returns error for authentication failures, rate limits, network errors, or other API failures.
func (rc *RequiredCheck) PollCheckStatus(ctx context.Context, repo *promoterv1alpha1.GitRepository, sha string, check scms.RequiredCheck) (promoterv1alpha1.CommitStatusPhase, error) {
	logger := log.FromContext(ctx)

	if repo.Spec.GitHub == nil {
		return promoterv1alpha1.CommitPhasePending, fmt.Errorf("GitRepository does not have GitHub configuration")
	}

	owner := repo.Spec.GitHub.Owner
	name := repo.Spec.GitHub.Name

	// Convert string IntegrationID back to int64 for GitHub API
	// Note: The Check Runs API parameter is called "app_id" while the Rulesets API
	// returns "integration_id". Both refer to the same GitHub App numeric identifier.
	// We store it as a string in the generic interface to support other SCM providers.
	var appID *int64
	if check.IntegrationID != nil {
		id, err := strconv.ParseInt(*check.IntegrationID, 10, 64)
		if err != nil {
			logger.Error(err, "failed to parse IntegrationID as int64", "integrationID", *check.IntegrationID)
		} else {
			appID = &id
		}
	}

	// Query check runs for the specific check name, filtering by AppID if specified
	// Note: Check Runs API uses "check_name" parameter and "name" field, while
	// Rulesets API uses "context". Both refer to the same check identifier.
	opts := &github.ListCheckRunsOptions{
		CheckName: github.Ptr(check.Name),
		AppID:     appID,
		ListOptions: github.ListOptions{
			PerPage: 100, // Maximum per page
		},
	}

	start := time.Now()
	var allCheckRuns []*github.CheckRun

	// Paginate through all check runs
	for {
		checkRuns, response, err := rc.client.Checks.ListCheckRunsForRef(ctx, owner, name, sha, opts)

		// Record metrics for each page
		if response != nil {
			duration := time.Since(start)
			metrics.RecordSCMCall(repo, metrics.SCMAPIRequiredCheck, metrics.SCMOperationList,
				response.StatusCode, duration, getRateLimitMetrics(response.Rate))

			logger.Info("github rate limit",
				"limit", response.Rate.Limit,
				"remaining", response.Rate.Remaining,
				"reset", response.Rate.Reset,
				"url", response.Request.URL)

			logger.V(4).Info("github response status", "status", response.Status)
		}

		if err != nil {
			// Note: GitHub returns 200 OK with empty array when no runs exist yet (not 404)
			return promoterv1alpha1.CommitPhasePending, fmt.Errorf("failed to list check runs for %s: %w", check.Name, err)
		}

		if checkRuns != nil && len(checkRuns.CheckRuns) > 0 {
			allCheckRuns = append(allCheckRuns, checkRuns.CheckRuns...)
		}

		// Check if there are more pages
		if response == nil || response.NextPage == 0 {
			break
		}

		opts.Page = response.NextPage
		start = time.Now() // Reset timer for next page
	}

	// If no check runs found, status is pending
	if len(allCheckRuns) == 0 {
		return promoterv1alpha1.CommitPhasePending, nil
	}

	// Get the LATEST check run (most recently started)
	// This ensures we use the status of reruns/retries, not old completed runs
	var latestCheckRun *github.CheckRun
	for _, run := range allCheckRuns {
		if latestCheckRun == nil {
			latestCheckRun = run
			continue
		}

		// Use the run with the most recent StartedAt time
		// If StartedAt is equal or nil, keep the current latestCheckRun
		if run.StartedAt != nil {
			if latestCheckRun.StartedAt == nil || run.StartedAt.After(latestCheckRun.StartedAt.Time) {
				latestCheckRun = run
			}
		}
	}

	phase := mapGitHubCheckStatusToPhase(latestCheckRun)
	return phase, nil
}

// mapGitHubCheckStatusToPhase maps GitHub check run status to CommitStatusPhase.
func mapGitHubCheckStatusToPhase(checkRun *github.CheckRun) promoterv1alpha1.CommitStatusPhase {
	if checkRun.Status == nil {
		return promoterv1alpha1.CommitPhasePending
	}

	status := *checkRun.Status

	if status == "completed" {
		if checkRun.Conclusion == nil {
			return promoterv1alpha1.CommitPhasePending
		}

		conclusion := *checkRun.Conclusion
		switch conclusion {
		case "success", "neutral", "skipped":
			return promoterv1alpha1.CommitPhaseSuccess
		case "failure", "cancelled", "timed_out":
			return promoterv1alpha1.CommitPhaseFailure
		case "action_required":
			// Note: "action_required" means the check requires manual user action
			// (e.g., approval, acknowledgment) before proceeding. While it blocks merging,
			// it's semantically a waiting state (similar to autoMerge: false) rather than
			// a failure state. We treat it as CommitPhasePending for consistency.
			return promoterv1alpha1.CommitPhasePending
		default:
			return promoterv1alpha1.CommitPhasePending
		}
	}

	// Status is queued or in_progress
	return promoterv1alpha1.CommitPhasePending
}

// evictExpiredOrOldestEntries removes expired entries and, if still over limit, evicts oldest entries.
// Must be called with requiredCheckCacheMutex write lock held.
func evictExpiredOrOldestEntries(ctx context.Context) {
	logger := log.FromContext(ctx)
	now := time.Now()
	initialSize := len(requiredCheckCache)

	// First pass: remove expired entries
	expiredCount := 0
	for key, entry := range requiredCheckCache {
		if now.After(entry.expiresAt) {
			delete(requiredCheckCache, key)
			expiredCount++
		}
	}

	if expiredCount > 0 {
		logger.V(4).Info("Evicted expired cache entries",
			"expiredCount", expiredCount,
			"remainingEntries", len(requiredCheckCache),
			"cacheMaxSize", requiredCheckCacheMaxSize)
	}

	// If still over limit, remove oldest entries
	if len(requiredCheckCache) >= requiredCheckCacheMaxSize {
		beforeLRU := len(requiredCheckCache)

		// Create slice of keys sorted by expiration time (oldest first)
		type cacheItem struct {
			key       string
			expiresAt time.Time
		}
		items := make([]cacheItem, 0, len(requiredCheckCache))
		for key, entry := range requiredCheckCache {
			items = append(items, cacheItem{key: key, expiresAt: entry.expiresAt})
		}

		// Sort by expiration time (oldest first)
		sort.Slice(items, func(i, j int) bool {
			return items[i].expiresAt.Before(items[j].expiresAt)
		})

		// Remove oldest entries until we're under the limit
		// Keep 10% headroom to avoid frequent evictions
		targetSize := int(float64(requiredCheckCacheMaxSize) * 0.9)
		lruEvictedCount := 0
		for i := 0; i < len(items) && len(requiredCheckCache) > targetSize; i++ {
			delete(requiredCheckCache, items[i].key)
			lruEvictedCount++
		}

		if lruEvictedCount > 0 {
			logger.V(4).Info("Evicted oldest cache entries due to size limit",
				"evictedCount", lruEvictedCount,
				"beforeEviction", beforeLRU,
				"afterEviction", len(requiredCheckCache),
				"cacheMaxSize", requiredCheckCacheMaxSize,
				"targetSize", targetSize)
		}
	}

	if expiredCount > 0 || len(requiredCheckCache) < initialSize {
		logger.V(4).Info("Cache eviction summary",
			"initialSize", initialSize,
			"finalSize", len(requiredCheckCache),
			"totalEvicted", initialSize-len(requiredCheckCache),
			"expiredEvicted", expiredCount,
			"lruEvicted", initialSize-len(requiredCheckCache)-expiredCount)
	}
}
