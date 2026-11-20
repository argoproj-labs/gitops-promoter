package loadreport

import (
	"cmp"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/argoproj-labs/gitops-promoter/test/scmserver"
)

const (
	routeGitAdvertiseUpload = "GIT GET /info/refs?service=git-upload-pack"
	routeGitUploadPack      = "GIT POST /git-upload-pack"
	routeGitReceivePack     = "GIT POST /git-receive-pack"
)

// Totals counts the requests of a scenario.
type Totals struct {
	// Requests is every request of the promoter: REST and git, including git's authentication challenges.
	Requests int `json:"requests"`
	// REST is every request that spends the installation's budget: RESTRead + RESTWrite + WebRequest.
	REST int `json:"rest"`
	// RESTRead and RESTWrite are the promoter's own GitHub API calls. RESTCreate is the POSTs among the writes.
	RESTRead   int `json:"restRead"`
	RESTWrite  int `json:"restWrite"`
	RESTCreate int `json:"restCreate"`
	// WebRequest is the WebRequestCommitStatus calls made with the ScmProvider's credentials.
	WebRequest int `json:"webRequest"`
	// RESTApp is the calls authenticated as the GitHub App: installation lookups and token minting.
	RESTApp int `json:"restApp"`
	// Points is the REST requests weighed for GitHub's points limit (a read is 1 point, a write 5).
	Points int `json:"points"`
	// GitHTTP is every git smart HTTP request, including GitAuthChallenges, the unauthenticated first attempts.
	GitHTTP           int `json:"gitHTTP"`
	GitAuthChallenges int `json:"gitAuthChallenges"`
	// GitReadCommands is the number of git fetch and ls-remote commands, GitFetchRounds the pack negotiation
	// requests beyond the one ref listing each of them makes, and GitPushes the number of pushes.
	GitReadCommands int `json:"gitReadCommands"`
	GitFetchRounds  int `json:"gitFetchRounds"`
	GitPushes       int `json:"gitPushes"`
	// Status4xx and Status5xx count failed requests, leaving out git's authentication challenges.
	Status4xx int `json:"status4xx"`
	Status5xx int `json:"status5xx"`
	// Other counts requests of the promoter that fit no bucket, and Fixture the test fixture's own requests, which
	// are in no other total.
	Other   int `json:"other"`
	Fixture int `json:"fixture"`
}

// RouteCount is the number of requests to one route with one outcome.
type RouteCount struct {
	Bucket Bucket `json:"bucket"`
	Route  string `json:"route"`
	Status int    `json:"status"`
	Count  int    `json:"count"`
	// PerCycle is the count per requeue interval, or per polling interval for WebRequestCommitStatus calls. Steady
	// scenarios only.
	PerCycle float64 `json:"perCycle,omitempty"`
	// PerHour is the count per hour at the shipped intervals (steady scenarios) or as observed (windows).
	PerHour float64 `json:"perHour,omitempty"`
}

// Rates are a steady scenario's or a window's requests per unit of time.
type Rates struct {
	// The PerHour rates are at the shipped intervals for a steady scenario, and as observed for a window.
	RESTPerHour        float64 `json:"restPerHour"`
	RESTWritePerHour   float64 `json:"restWritePerHour"`
	WebRequestPerHour  float64 `json:"webRequestPerHour"`
	GitCommandsPerHour float64 `json:"gitCommandsPerHour"`
	GitHTTPPerHour     float64 `json:"gitHTTPPerHour"`
	// InstallationBudgetShare is RESTPerHour as a fraction of the installation's hourly budget.
	InstallationBudgetShare float64 `json:"installationBudgetShare"`
	// RESTPerCycle and GitCommandsPerCycle are per requeue interval, and WebRequestPerPoll per polling interval.
	RESTPerCycle        float64 `json:"restPerCycle,omitempty"`
	GitCommandsPerCycle float64 `json:"gitCommandsPerCycle,omitempty"`
	WebRequestPerPoll   float64 `json:"webRequestPerPoll,omitempty"`
	// RESTPerHourPerStrategy and GitCommandsPerHourPerStrategy divide by the number of PromotionStrategies.
	RESTPerHourPerStrategy        float64 `json:"restPerHourPerStrategy"`
	GitCommandsPerHourPerStrategy float64 `json:"gitCommandsPerHourPerStrategy"`
}

// Peaks are the busiest minute of an event or window, for comparison with per-minute limits.
type Peaks struct {
	RESTPerMinute       int `json:"restPerMinute"`
	RESTCreatePerMinute int `json:"restCreatePerMinute"`
	PointsPerMinute     int `json:"pointsPerMinute"`
}

// Result is the analysis of one scenario.
type Result struct {
	Rates *Rates `json:"rates,omitempty"`
	Peaks *Peaks `json:"peaks,omitempty"`
	// Instability lists what moved during a steady scenario that should have been still.
	Instability []string     `json:"instability,omitempty"`
	Routes      []RouteCount `json:"routes"`
	Scenario
	Totals Totals `json:"totals"`
	// Seconds is the length of the measurement.
	Seconds float64 `json:"seconds"`
	// Stable is false when a steady scenario saw writes or pushes, which means it was not in a steady state.
	Stable bool `json:"stable"`
}

// weights accumulates the requests of a steady scenario for its rates, each request weighed by where in the
// observation it fell (see taper).
type weights struct {
	rest, restOwn, restWrite, webRequest, gitCommands, gitHTTP float64
}

// taper weighs a request of a steady scenario by when it happened: 1 in the middle of the observation, rising from
// 0 over the first requeue interval and falling to 0 over the last. The traffic of a steady state comes in bursts
// that repeat every interval, and an observation that starts or ends inside a burst would count part of a cycle as
// a whole one. With edges one interval long, every phase of the cycle gets the same total weight, so the weighted
// count of a periodic stream is exact wherever the observation starts. It returns the weight function and the
// length of observation the weights add up to.
func taper(sc Scenario) (func(time.Time) float64, float64) {
	seconds := sc.End.Sub(sc.Start).Seconds()
	edge := sc.Requeue.Seconds()
	if sc.Kind != KindSteady || edge <= 0 || seconds < 2*edge {
		return func(time.Time) float64 { return 1 }, seconds
	}
	return func(at time.Time) float64 {
		fromEdge := min(at.Sub(sc.Start).Seconds(), sc.End.Sub(at).Seconds())
		return max(0, min(1, fromEdge/edge))
	}, seconds - edge
}

// Analyze counts the requests of a scenario. recs must hold only the scenario's own requests (see Select), from its
// start to its end.
func Analyze(sc Scenario, recs []scmserver.RequestRecord, budgets Budgets) Result {
	res := Result{Scenario: sc, Seconds: sc.End.Sub(sc.Start).Seconds(), Stable: true}
	type routeKey struct {
		bucket Bucket
		route  string
		status int
	}
	counts := map[routeKey]int{}
	weighted := map[routeKey]float64{}
	weigh, effectiveSeconds := taper(sc)
	var sum weights
	var minutes peakTracker
	uploadPackPosts := 0
	for _, rec := range recs {
		bucket := Classify(rec)
		if bucket == BucketFixture {
			res.Totals.Fixture++
			continue
		}
		w := weigh(rec.Time)
		counts[routeKey{bucket, rec.Route, rec.StatusCode}]++
		weighted[routeKey{bucket, rec.Route, rec.StatusCode}] += w
		t := &res.Totals
		t.Requests++
		if bucket != BucketGitAuthChallenge {
			switch {
			case rec.StatusCode >= http.StatusInternalServerError:
				t.Status5xx++
			case rec.StatusCode >= http.StatusBadRequest:
				t.Status4xx++
			default:
			}
		}
		points, create := 0, false
		switch bucket {
		case BucketRESTRead:
			t.RESTRead++
			sum.rest += w
			sum.restOwn += w
			points = budgets.ReadPoints
		case BucketRESTWrite:
			t.RESTWrite++
			sum.rest += w
			sum.restOwn += w
			sum.restWrite += w
			points = budgets.WritePoints
			create = rec.Method == http.MethodPost
		case BucketWebRequest:
			t.WebRequest++
			sum.rest += w
			sum.webRequest += w
			points = budgets.ReadPoints
			if rec.Method != http.MethodGet && rec.Method != http.MethodHead {
				points = budgets.WritePoints
			}
		case BucketApp:
			t.RESTApp++
		case BucketGitAuthChallenge:
			t.GitHTTP++
			t.GitAuthChallenges++
			sum.gitHTTP += w
		case BucketGitRead, BucketGitWrite:
			t.GitHTTP++
			sum.gitHTTP += w
			ok := rec.StatusCode < http.StatusBadRequest
			switch {
			case rec.Route == routeGitAdvertiseUpload && ok:
				t.GitReadCommands++
				sum.gitCommands += w
			case rec.Route == routeGitUploadPack && ok:
				uploadPackPosts++
			case rec.Route == routeGitReceivePack && ok:
				t.GitPushes++
				sum.gitCommands += w
			default:
			}
		default:
			t.Other++
		}
		if points > 0 {
			t.Points += points
			if create {
				t.RESTCreate++
			}
			minutes.add(rec.Time, points, create)
		}
	}
	res.Totals.REST = res.Totals.RESTRead + res.Totals.RESTWrite + res.Totals.WebRequest
	// With protocol v2 every fetch or ls-remote lists refs with one upload-pack request; the rest negotiate packs.
	res.Totals.GitFetchRounds = max(uploadPackPosts-res.Totals.GitReadCommands, 0)

	// perHour scales a weighted count of this scenario to an hour: at the shipped intervals for a steady scenario.
	perHour := func(float64) float64 { return 0 }
	if effectiveSeconds > 0 && sc.Kind != KindEvent {
		compression := sc.Compression
		if sc.Kind == KindWindow || compression <= 0 {
			compression = 1
		}
		perHour = func(count float64) float64 { return count / effectiveSeconds * 3600 / compression }
	}
	perInterval := func(count float64, interval time.Duration) float64 {
		if sc.Kind != KindSteady || effectiveSeconds <= 0 || interval <= 0 {
			return 0
		}
		return count / (effectiveSeconds / interval.Seconds())
	}

	for key, count := range counts {
		rc := RouteCount{Bucket: key.bucket, Route: key.route, Status: key.status, Count: count}
		// App calls follow the token lifetime and the installation cache, which are not compressed.
		if key.bucket != BucketApp {
			interval := sc.Requeue.Duration
			if key.bucket == BucketWebRequest {
				interval = sc.Poll.Duration
			}
			rc.PerCycle = perInterval(weighted[key], interval)
			rc.PerHour = perHour(weighted[key])
		}
		res.Routes = append(res.Routes, rc)
	}
	slices.SortFunc(res.Routes, func(a, b RouteCount) int {
		return cmp.Or(cmp.Compare(a.Bucket, b.Bucket), cmp.Compare(a.Route, b.Route), cmp.Compare(a.Status, b.Status))
	})

	if sc.Kind == KindEvent || sc.Kind == KindWindow {
		res.Peaks = minutes.peaks()
	}
	if sc.Kind == KindEvent {
		return res
	}
	strategies := float64(max(sc.Topology.PromotionStrategies, 1))
	res.Rates = &Rates{
		RESTPerHour:                   perHour(sum.rest),
		RESTWritePerHour:              perHour(sum.restWrite),
		WebRequestPerHour:             perHour(sum.webRequest),
		GitCommandsPerHour:            perHour(sum.gitCommands),
		GitHTTPPerHour:                perHour(sum.gitHTTP),
		RESTPerCycle:                  perInterval(sum.restOwn, sc.Requeue.Duration),
		GitCommandsPerCycle:           perInterval(sum.gitCommands, sc.Requeue.Duration),
		WebRequestPerPoll:             perInterval(sum.webRequest, sc.Poll.Duration),
		RESTPerHourPerStrategy:        perHour(sum.rest) / strategies,
		GitCommandsPerHourPerStrategy: perHour(sum.gitCommands) / strategies,
	}
	if budgets.InstallationPerHour > 0 {
		res.Rates.InstallationBudgetShare = res.Rates.RESTPerHour / float64(budgets.InstallationPerHour)
	}
	if sc.Kind == KindSteady {
		res.Instability = instability(res.Routes)
		res.Stable = len(res.Instability) == 0
	}
	return res
}

// instability lists the routes that show a steady scenario was not steady: the promoter's own writes, pushes, and
// failed requests.
func instability(routes []RouteCount) []string {
	var out []string
	for _, rc := range routes {
		moved := rc.Bucket == BucketRESTWrite || rc.Bucket == BucketGitWrite
		failed := rc.Status >= http.StatusBadRequest && rc.Bucket != BucketGitAuthChallenge
		if moved || failed {
			out = append(out, fmt.Sprintf("%d x %s -> %d", rc.Count, rc.Route, rc.Status))
		}
	}
	return out
}

// peakTracker finds the busiest minute of a scenario, in minutes counted from its first request.
type peakTracker struct {
	first   time.Time
	rest    map[int]int
	creates map[int]int
	points  map[int]int
}

func (p *peakTracker) add(at time.Time, points int, create bool) {
	if p.rest == nil {
		p.first = at
		p.rest, p.creates, p.points = map[int]int{}, map[int]int{}, map[int]int{}
	}
	minute := int(at.Sub(p.first) / time.Minute)
	p.rest[minute]++
	p.points[minute] += points
	if create {
		p.creates[minute]++
	}
}

func (p *peakTracker) peaks() *Peaks {
	out := &Peaks{}
	for _, n := range p.rest {
		out.RESTPerMinute = max(out.RESTPerMinute, n)
	}
	for _, n := range p.creates {
		out.RESTCreatePerMinute = max(out.RESTCreatePerMinute, n)
	}
	for _, n := range p.points {
		out.PointsPerMinute = max(out.PointsPerMinute, n)
	}
	return out
}

// ShortRoute drops the API prefix of a route for display: "GET /repos/{owner}/{repo}/pulls".
func ShortRoute(route string) string {
	return strings.Replace(route, " /api/v3/", " /", 1)
}
