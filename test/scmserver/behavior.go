package scmserver

import (
	"math/rand/v2"
	"sync"
	"time"
)

// Behavior controls the provider-specific timing and limits the server reproduces. The zero value of a field
// disables the behavior it controls.
type Behavior struct {
	// Latency is added before every API and git request is handled.
	Latency Duration `json:"latency"`
	// LatencyJitter adds a random extra delay in [0, LatencyJitter).
	LatencyJitter Duration `json:"latencyJitter"`
	// ListLag hides newly created pull requests from list endpoints for this long. Fetching a pull request by number
	// is not affected. This is a synthetic fault: no provider's REST list endpoint documents such lag (GitHub's and
	// GitLab's lists are not search-index backed). It exists to exercise a controller that lists right after creating.
	ListLag Duration `json:"listLag"`
	// MergeabilityDelay is how long the provider takes to compute mergeability after a pull request is created or
	// one of its branches moves. While it runs, GitHub reports mergeable=null, GitLab reports
	// detailed_merge_status=checking (its merge API checks synchronously and still merges), Gitea and Forgejo reject
	// merges with 405, and Azure DevOps reports mergeStatus=queued.
	MergeabilityDelay Duration `json:"mergeabilityDelay"`
	// CompletionDelay is how long Azure DevOps takes to complete a pull request after completion is requested. Until
	// then the pull request stays active.
	CompletionDelay Duration `json:"completionDelay"`
	// AzureAsyncCompletionFailures makes Azure DevOps accept a completion request for a conflicting or policy-blocked
	// pull request and fail it CompletionDelay later, leaving the pull request active with mergeFailureMessage. By
	// default such a request is rejected synchronously with 400, matching the documented completion semantics
	// (policies are evaluated at completion unless completionOptions.bypassPolicy is set). Azure DevOps does not
	// document the response, so the knob keeps the asynchronous model available.
	AzureAsyncCompletionFailures bool `json:"azureAsyncCompletionFailures"`
	// AsyncMergeDelay makes Bitbucket Cloud answer merges with 202 Accepted and a task-status link, completing the
	// merge after this delay. Zero merges synchronously. Bitbucket Data Center always merges synchronously.
	AsyncMergeDelay Duration `json:"asyncMergeDelay"`
	// TokenTTL is the lifetime of GitHub App installation tokens.
	TokenTTL Duration `json:"tokenTTL"`
	// RateLimitWindow is the window RateLimit applies to.
	RateLimitWindow Duration `json:"rateLimitWindow"`
	// WebhookDelay delays every webhook delivery.
	WebhookDelay Duration `json:"webhookDelay"`
	// WebhookDropRate is the probability in [0, 1] that a webhook delivery is silently dropped.
	WebhookDropRate float64 `json:"webhookDropRate"`
	// RateLimit is the number of API requests a credential may make per RateLimitWindow. Zero disables rate
	// limiting. Rate limit headers are always sent in the provider's format when it is set.
	RateLimit int `json:"rateLimit"`
}

// DefaultBehavior returns the default behavior for p. Defaults reproduce the asynchronous behavior of the real
// provider while keeping happy-path promotions fast.
func DefaultBehavior(p Provider) Behavior {
	b := Behavior{
		TokenTTL:        D(time.Hour),
		RateLimitWindow: D(time.Hour),
	}
	switch p {
	case GitHub:
		b.MergeabilityDelay = D(500 * time.Millisecond)
		b.RateLimit = 5000
	case GitLab:
		b.MergeabilityDelay = D(500 * time.Millisecond)
		b.RateLimit = 2000
		b.RateLimitWindow = D(time.Minute)
	case AzureDevOps:
		b.MergeabilityDelay = D(500 * time.Millisecond)
		b.CompletionDelay = D(2 * time.Second)
	case BitbucketCloud:
		b.RateLimit = 1000
	default:
		// Gitea and Forgejo answer merges attempted during their mergeability check with 405, which their Go SDKs
		// report as not merged without an error. Keep the check instantaneous by default so promotions are not slowed
		// by merge retries; raise MergeabilityDelay to reproduce it. Bitbucket Data Center's rate limiting is off by
		// default and its merge API checks mergeability synchronously.
	}
	return b
}

type rateWindow struct {
	start time.Time
	count int
}

// behaviorSet holds per-provider behavior and rate-limit counters.
type behaviorSet struct {
	byProvider map[Provider]Behavior
	windows    map[string]*rateWindow
	mu         sync.Mutex
}

func newBehaviorSet() *behaviorSet {
	b := &behaviorSet{}
	b.reset()
	return b
}

func (b *behaviorSet) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.byProvider = map[Provider]Behavior{}
	for _, p := range AllProviders {
		b.byProvider[p] = DefaultBehavior(p)
	}
	b.windows = map[string]*rateWindow{}
}

func (b *behaviorSet) get(p Provider) Behavior {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.byProvider[p]
}

func (b *behaviorSet) set(p Provider, behavior Behavior) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.byProvider[p] = behavior
	b.windows = map[string]*rateWindow{}
}

func (b *behaviorSet) all() map[Provider]Behavior {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[Provider]Behavior, len(b.byProvider))
	for p, behavior := range b.byProvider {
		out[p] = behavior
	}
	return out
}

// rateLimitState is the outcome of counting one request against a credential's rate limit.
type rateLimitState struct {
	reset     time.Time
	limit     int
	remaining int
	used      int
	exceeded  bool
}

// take counts a request by key against provider p's rate limit.
func (b *behaviorSet) take(p Provider, key string, now time.Time) (rateLimitState, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	behavior := b.byProvider[p]
	if behavior.RateLimit <= 0 {
		return rateLimitState{}, false
	}
	window := behavior.RateLimitWindow.Duration
	if window <= 0 {
		window = time.Hour
	}
	k := string(p) + "\x00" + key
	w := b.windows[k]
	if w == nil || now.Sub(w.start) >= window {
		w = &rateWindow{start: now}
		b.windows[k] = w
	}
	w.count++
	remaining := max(behavior.RateLimit-w.count, 0)
	return rateLimitState{
		limit:     behavior.RateLimit,
		remaining: remaining,
		used:      min(w.count, behavior.RateLimit),
		reset:     w.start.Add(window),
		exceeded:  w.count > behavior.RateLimit,
	}, true
}

// latency returns the delay to apply before handling a request.
func (b Behavior) latency() time.Duration {
	d := b.Latency.Duration
	if b.LatencyJitter.Duration > 0 {
		d += time.Duration(rand.Int64N(int64(b.LatencyJitter.Duration))) //nolint:gosec // jitter, not security
	}
	return d
}
