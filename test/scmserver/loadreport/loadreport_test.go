package loadreport_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/argoproj-labs/gitops-promoter/test/scmserver"
	"github.com/argoproj-labs/gitops-promoter/test/scmserver/loadreport"
)

const (
	goGitHub    = "go-github/v93.0.0"
	gitClient   = "git/2.43.0"
	plainClient = "Go-http-client/2.0"

	routePulls       = "GET /api/v3/repos/{owner}/{repo}/pulls"
	routeCreatePull  = "POST /api/v3/repos/{owner}/{repo}/pulls"
	routeCheckRuns   = "GET /api/v3/repos/{owner}/{repo}/commits/{ref}/check-runs"
	routeUpdateCheck = "PATCH /api/v3/repos/{owner}/{repo}/check-runs/{id}"
	routeMintToken   = "POST /api/v3/app/installations/{id}/access_tokens"
	routeAdvertise   = "GIT GET /info/refs?service=git-upload-pack"
	routeUploadPack  = "GIT POST /git-upload-pack"
	routeReceivePack = "GIT POST /git-receive-pack"
)

var start = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// rest is a REST request of the fixture's installation.
func rest(method, route, userAgent string, status int) scmserver.RequestRecord {
	return scmserver.RequestRecord{
		Time: start, Provider: scmserver.GitHub, Method: method, Route: route, Path: "/api/v3/repos/acme/deploy/x",
		Credential: "installation:420", UserAgent: userAgent, StatusCode: status,
	}
}

// gitCommand is the requests of one ls-remote: the challenge, the ref advertisement, the ref listing, and one
// upload-pack request per fetch round.
func gitCommand(fetchRounds int) []scmserver.RequestRecord {
	git := func(method, route, credential string, status int) scmserver.RequestRecord {
		return scmserver.RequestRecord{
			Time: start, Provider: scmserver.GitHub, Method: method, Route: route, Path: "/acme/deploy.git/x",
			Credential: credential, UserAgent: gitClient, StatusCode: status, Git: true,
		}
	}
	recs := []scmserver.RequestRecord{
		git(http.MethodGet, routeAdvertise, "", http.StatusUnauthorized),
		git(http.MethodGet, routeAdvertise, "installation:420", http.StatusOK),
		git(http.MethodPost, routeUploadPack, "installation:420", http.StatusOK),
	}
	for range fetchRounds {
		recs = append(recs, git(http.MethodPost, routeUploadPack, "installation:420", http.StatusOK))
	}
	return recs
}

func repeat(n int, recs ...scmserver.RequestRecord) []scmserver.RequestRecord {
	var out []scmserver.RequestRecord
	for range n {
		out = append(out, recs...)
	}
	return out
}

// every returns burst once per period, from offset after the start until the end of a window.
func every(period, offset, window time.Duration, burst ...scmserver.RequestRecord) []scmserver.RequestRecord {
	var out []scmserver.RequestRecord
	for at := offset; at < window; at += period {
		for _, rec := range burst {
			rec.Time = start.Add(at)
			out = append(out, rec)
		}
	}
	return out
}

var _ = Describe("Classify", func() {
	DescribeTable("buckets a request by sender and budget",
		func(rec scmserver.RequestRecord, want loadreport.Bucket) {
			Expect(loadreport.Classify(rec)).To(Equal(want))
		},
		Entry("a read by the promoter's GitHub client", rest(http.MethodGet, routePulls, goGitHub, 200), loadreport.BucketRESTRead),
		Entry("a write by the promoter's GitHub client", rest(http.MethodPatch, routeUpdateCheck, goGitHub, 200), loadreport.BucketRESTWrite),
		Entry("a failed write still counts as a write", rest(http.MethodPost, routeCreatePull, goGitHub, 422), loadreport.BucketRESTWrite),
		Entry("an installation-token request by another client", rest(http.MethodGet, routeCheckRuns, plainClient, 200), loadreport.BucketWebRequest),
		Entry("a GitHub App JWT request",
			scmserver.RequestRecord{Method: http.MethodPost, Route: routeMintToken, Credential: "app:42", UserAgent: goGitHub, StatusCode: 201},
			loadreport.BucketApp),
		Entry("git's unauthenticated first attempt", gitCommand(0)[0], loadreport.BucketGitAuthChallenge),
		Entry("an authenticated ref advertisement", gitCommand(0)[1], loadreport.BucketGitRead),
		Entry("a push",
			scmserver.RequestRecord{Method: http.MethodPost, Route: routeReceivePack, Credential: "installation:420", StatusCode: 200, Git: true},
			loadreport.BucketGitWrite),
		Entry("a static token, which only fixtures use",
			scmserver.RequestRecord{Method: http.MethodGet, Route: routePulls, Credential: "token", StatusCode: 200},
			loadreport.BucketFixture),
		Entry("a request marked by the fixture",
			scmserver.RequestRecord{Method: http.MethodGet, Route: routeAdvertise, Credential: "installation:420", Git: true, NoFault: true},
			loadreport.BucketFixture),
		Entry("a REST request without valid credentials",
			scmserver.RequestRecord{Method: http.MethodGet, Route: routePulls, StatusCode: 401},
			loadreport.BucketOther),
	)
})

var _ = Describe("Select", func() {
	It("attributes requests by credential and by the owner they address", func() {
		mint := scmserver.RequestRecord{Path: "/api/v3/app/installations/420/access_tokens", Credential: "app:42"}
		lookup := scmserver.RequestRecord{Path: "/api/v3/orgs/acme/installation", Credential: "app:42"}
		pulls := scmserver.RequestRecord{Path: "/api/v3/repos/acme/deploy/pulls", Credential: "installation:420"}
		challenge := scmserver.RequestRecord{Path: "/acme/deploy.git/info/refs", Git: true}
		otherOwner := scmserver.RequestRecord{Path: "/api/v3/repos/acme-2/deploy/pulls", Credential: "installation:430"}
		otherApp := scmserver.RequestRecord{Path: "/api/v3/app/installations/430/access_tokens", Credential: "app:43"}

		got := loadreport.Select(
			[]scmserver.RequestRecord{mint, lookup, pulls, challenge, otherOwner, otherApp},
			loadreport.Selector{Owner: "acme", AppID: 42, InstallationID: 420})
		Expect(got).To(Equal([]scmserver.RequestRecord{mint, lookup, pulls, challenge}))
	})
})

var _ = Describe("Analyze", func() {
	steady := func() loadreport.Scenario {
		return loadreport.Scenario{
			Name: "steady/held", Group: "Steady state", Kind: loadreport.KindSteady,
			Start: start, End: start.Add(300 * time.Second),
			Compression: 10, Requeue: scmserver.D(30 * time.Second), Poll: scmserver.D(6 * time.Second),
			Topology: loadreport.Topology{PromotionStrategies: 2, Environments: 3, OpenPullRequests: 3},
		}
	}

	It("extrapolates a steady scenario to the shipped intervals", func() {
		// Ten compressed requeue cycles: three pull request lists and six ls-remotes per cycle, fifty polls of
		// three WebRequestCommitStatus calls, and one token minted along the way.
		const window = 300 * time.Second
		var recs []scmserver.RequestRecord
		recs = append(recs, every(30*time.Second, 7*time.Second, window, repeat(3, rest(http.MethodGet, routePulls, goGitHub, 200))...)...)
		recs = append(recs, every(30*time.Second, 11*time.Second, window, repeat(6, gitCommand(0)...)...)...)
		recs = append(recs, every(6*time.Second, 2*time.Second, window, repeat(3, rest(http.MethodGet, routeCheckRuns, plainClient, 200))...)...)
		recs = append(recs, scmserver.RequestRecord{
			Time: start.Add(100 * time.Second), Method: http.MethodPost, Route: routeMintToken, Credential: "app:42", StatusCode: 201,
		})
		recs = append(recs, scmserver.RequestRecord{
			Time: start.Add(100 * time.Second), Method: http.MethodGet, Route: routeAdvertise, Credential: "token", Git: true,
		})

		res := loadreport.Analyze(steady(), recs, loadreport.DefaultBudgets())

		Expect(res.Stable).To(BeTrue())
		Expect(res.Totals).To(Equal(loadreport.Totals{
			Requests: 30 + 180 + 150 + 1, REST: 180, RESTRead: 30, WebRequest: 150, RESTApp: 1, Points: 180,
			GitHTTP: 180, GitAuthChallenges: 60, GitReadCommands: 60, Fixture: 1,
		}))
		// Three pull request lists per 30s cycle is 360 an hour compressed, 36 an hour at shipped intervals.
		Expect(res.Rates.RESTPerHour).To(BeNumerically("~", 36+180, 1e-9))
		Expect(res.Rates.WebRequestPerHour).To(BeNumerically("~", 180, 1e-9))
		Expect(res.Rates.RESTWritePerHour).To(BeZero())
		Expect(res.Rates.InstallationBudgetShare).To(BeNumerically("~", 216.0/5000, 1e-9))
		Expect(res.Rates.RESTPerHourPerStrategy).To(BeNumerically("~", 108, 1e-9))
		Expect(res.Rates.GitCommandsPerHour).To(BeNumerically("~", 72, 1e-9))
		Expect(res.Rates.GitHTTPPerHour).To(BeNumerically("~", 216, 1e-9))
		Expect(res.Rates.RESTPerCycle).To(BeNumerically("~", 3, 1e-9))
		Expect(res.Rates.WebRequestPerPoll).To(BeNumerically("~", 3, 1e-9))
		Expect(res.Rates.GitCommandsPerCycle).To(BeNumerically("~", 6, 1e-9))

		route := func(name string) loadreport.RouteCount {
			for _, rc := range res.Routes {
				if rc.Route == name && rc.Status < 400 {
					return rc
				}
			}
			Fail("no route " + name)
			return loadreport.RouteCount{}
		}
		Expect(route(routePulls)).To(And(
			HaveField("Bucket", loadreport.BucketRESTRead), HaveField("Count", 30),
			HaveField("PerCycle", BeNumerically("~", 3, 1e-9)), HaveField("PerHour", BeNumerically("~", 36, 1e-9))))
		Expect(route(routeCheckRuns)).To(And(
			HaveField("Bucket", loadreport.BucketWebRequest), HaveField("Count", 150),
			HaveField("PerCycle", BeNumerically("~", 3, 1e-9)), HaveField("PerHour", BeNumerically("~", 180, 1e-9))))
		Expect(route(routeMintToken)).To(Equal(loadreport.RouteCount{
			Bucket: loadreport.BucketApp, Route: routeMintToken, Status: 201, Count: 1,
		}), "App calls follow timers that are not compressed, so they are not extrapolated")
	})

	DescribeTable("measures the same rate wherever in the cycle the observation starts",
		func(offset time.Duration) {
			recs := every(30*time.Second, offset, 300*time.Second, repeat(3, rest(http.MethodGet, routePulls, goGitHub, 200))...)
			res := loadreport.Analyze(steady(), recs, loadreport.DefaultBudgets())
			Expect(res.Rates.RESTPerCycle).To(BeNumerically("~", 3, 1e-9))
			Expect(res.Rates.RESTPerHour).To(BeNumerically("~", 36, 1e-9))
		},
		Entry("a burst right at the start", time.Duration(0)),
		Entry("a burst just inside", 100*time.Millisecond),
		Entry("a burst mid-cycle", 15*time.Second),
		Entry("a burst just before the next cycle", 29900*time.Millisecond),
	)

	It("marks a steady scenario unstable when the promoter wrote or a request failed", func() {
		recs := every(time.Hour, 100*time.Second, 300*time.Second,
			rest(http.MethodGet, routePulls, goGitHub, 200),
			rest(http.MethodPatch, routeUpdateCheck, goGitHub, 200),
			rest(http.MethodGet, routePulls, goGitHub, 502),
		)
		res := loadreport.Analyze(steady(), recs, loadreport.DefaultBudgets())
		Expect(res.Stable).To(BeFalse())
		Expect(res.Instability).To(ConsistOf(
			"1 x "+routeUpdateCheck+" -> 200",
			"1 x "+routePulls+" -> 502",
		))
		Expect(res.Totals.Status5xx).To(Equal(1))
	})

	It("totals an event and finds its busiest minute", func() {
		sc := loadreport.Scenario{
			Name: "event/promotion", Group: "Events", Kind: loadreport.KindEvent, Settled: true,
			Start: start, End: start.Add(90 * time.Second), Compression: 1,
			Topology: loadreport.Topology{PromotionStrategies: 1, Environments: 3},
		}
		late := rest(http.MethodPost, routeCreatePull, goGitHub, 201)
		late.Time = start.Add(70 * time.Second)
		push := scmserver.RequestRecord{
			Time: start, Method: http.MethodPost, Route: routeReceivePack, Credential: "installation:420", StatusCode: 200, Git: true,
		}
		recs := append(gitCommand(2),
			rest(http.MethodPost, routeCreatePull, goGitHub, 201),
			rest(http.MethodPost, routeCreatePull, goGitHub, 201),
			rest(http.MethodPatch, routeUpdateCheck, goGitHub, 200),
			rest(http.MethodGet, routePulls, goGitHub, 200),
			late, push)

		res := loadreport.Analyze(sc, recs, loadreport.DefaultBudgets())

		Expect(res.Rates).To(BeNil())
		Expect(res.Totals.REST).To(Equal(5))
		Expect(res.Totals.RESTWrite).To(Equal(4))
		Expect(res.Totals.RESTCreate).To(Equal(3))
		Expect(res.Totals.Points).To(Equal(4*5 + 1))
		Expect(res.Totals.GitReadCommands).To(Equal(1))
		Expect(res.Totals.GitFetchRounds).To(Equal(2))
		Expect(res.Totals.GitPushes).To(Equal(1))
		Expect(*res.Peaks).To(Equal(loadreport.Peaks{RESTPerMinute: 4, RESTCreatePerMinute: 2, PointsPerMinute: 16}))
	})

	It("reports a window at the speed it was observed", func() {
		sc := steady()
		sc.Kind = loadreport.KindWindow
		sc.End = start.Add(90 * time.Second)
		recs := repeat(3, rest(http.MethodGet, routePulls, goGitHub, http.StatusForbidden))
		res := loadreport.Analyze(sc, recs, loadreport.DefaultBudgets())
		Expect(res.Rates.RESTPerHour).To(BeNumerically("~", 120, 1e-9))
		Expect(res.Totals.Status4xx).To(Equal(3))
		Expect(res.Stable).To(BeTrue(), "only steady scenarios are judged for stability")
	})
})

var _ = Describe("Report", func() {
	build := func() *loadreport.Report {
		budgets := loadreport.DefaultBudgets()
		steady := loadreport.Analyze(loadreport.Scenario{
			Name: "steady/held", Group: "Steady state", Kind: loadreport.KindSteady, Description: "Three pull requests held.",
			Start: start, End: start.Add(300 * time.Second), Compression: 10, Requeue: scmserver.D(30 * time.Second),
			Topology: loadreport.Topology{PromotionStrategies: 1, Environments: 3, OpenPullRequests: 3, Gates: []string{"manual"}},
		}, every(30*time.Second, 7*time.Second, 300*time.Second, repeat(3, rest(http.MethodGet, routePulls, goGitHub, 200))...), budgets)
		event := loadreport.Analyze(loadreport.Scenario{
			Name: "event/promotion", Group: "Events", Kind: loadreport.KindEvent, Description: "One promotion.", Settled: true,
			Start: start, End: start.Add(20 * time.Second), Compression: 1,
			Topology: loadreport.Topology{PromotionStrategies: 1, Environments: 3},
		}, []scmserver.RequestRecord{rest(http.MethodPost, routeCreatePull, goGitHub, 201)}, budgets)
		return &loadreport.Report{
			SchemaVersion: loadreport.SchemaVersion, GeneratedAt: start, Provider: "github", Budgets: budgets,
			ShippedRequeue: "5m0s", ShippedPoll: "1m0s",
			Scenarios:   []loadreport.Result{steady, event},
			Checks:      []loadreport.Check{loadreport.Agreement("linearity", 0.1, []string{"10x", "20x"}, []float64{36, 35})},
			Limitations: []string{"Controller restarts are not measured."},
		}
	}

	It("renders tables per group, checks, limitations and routes as Markdown", func() {
		var buf bytes.Buffer
		Expect(build().WriteMarkdown(&buf)).To(Succeed())
		md := buf.String()
		Expect(md).To(ContainSubstring("## Steady state\n"))
		Expect(md).To(ContainSubstring("| steady/held | 1 x 3 | 3 | 36.0 | 0 | 0 | 0.72% | 36.0 | 0 | 0 | 3.00 | 0 | 0 | yes |"))
		Expect(md).To(ContainSubstring("## Events\n"))
		Expect(md).To(ContainSubstring("| event/promotion | 1 x 3 | 1 | 0 | 1 | 1 | 0 | 0 | 0 | 0 | 0 | 0 | 1 | 1 | 20 | yes |"))
		Expect(md).To(ContainSubstring("| linearity | pass | 10x=36.0, 20x=35.0 (spread 2.8%, tolerance 10%) |"))
		Expect(md).To(ContainSubstring("- Controller restarts are not measured."))
		Expect(md).To(ContainSubstring("| rest.installation.read | `GET /repos/{owner}/{repo}/pulls` | 200 | 30 | 3.00 | 36.0 |"))
		Expect(md).To(ContainSubstring("Gates: manual."))
	})

	It("round-trips through JSON", func() {
		report := build()
		var buf bytes.Buffer
		Expect(report.WriteJSON(&buf)).To(Succeed())
		var decoded loadreport.Report
		Expect(json.Unmarshal(buf.Bytes(), &decoded)).To(Succeed())
		Expect(decoded.SchemaVersion).To(Equal(loadreport.SchemaVersion))
		Expect(decoded.Scenarios).To(HaveLen(2))
		Expect(decoded.Scenarios[0].Rates.RESTPerHour).To(BeNumerically("~", 36, 1e-9))
		Expect(decoded.Scenarios[0].Requeue.Duration).To(Equal(30 * time.Second))
		Expect(decoded.Scenarios[1].Name).To(Equal("event/promotion"))
	})

	It("fails an agreement check when the values spread beyond the tolerance", func() {
		Expect(loadreport.Agreement("scale", 0.1, []string{"N=1", "N=20"}, []float64{36, 20}).Pass).To(BeFalse())
	})
})
