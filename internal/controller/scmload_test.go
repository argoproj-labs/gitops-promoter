/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/test/scmserver"
	"github.com/argoproj-labs/gitops-promoter/test/scmserver/loadreport"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const (
	loadGroupSteady    = "Steady states"
	loadGroupLinearity = "Steady states at twice the compression"
	loadGroupScale     = "Scale"
	loadGroupEvents    = "Events"
	loadGroupThrottled = "Throttled"

	// loadEventQuiet is how long the promoter must send nothing before an event counts as over.
	loadEventQuiet = 10 * time.Second
)

// steadyScenario is a state to hold and observe: prepare brings a new fixture into it.
type steadyScenario struct {
	prepare     func(ctx context.Context, h *loadHarness) *loadFixture
	name        string
	description string
}

// eventScenario is something that happens once: prepare builds the fixture, and trigger starts the event and
// returns when its outcome is reached.
type eventScenario struct {
	prepare     func(ctx context.Context, h *loadHarness) *loadFixture
	trigger     func(ctx context.Context, h *loadHarness, f *loadFixture)
	name        string
	description string
	// mergeabilityDelay makes the server take this long to work out whether a new pull request can merge.
	mergeabilityDelay time.Duration
	// compressed runs the event with the periodic timers compressed, for an event only a requeue notices.
	compressed bool
}

// throttleScenario is a fault the SCM answers every REST request of a fixture with, while the promoter has work
// to do there.
type throttleScenario struct {
	// fault builds the response; until is when the observation ends.
	fault       func(until time.Time) scmserver.FaultRule
	name        string
	description string
	window      time.Duration
	// webRequest adds a polling WebRequestCommitStatus to the fixture.
	webRequest bool
}

// idleScenario is a steady state in which a change has been promoted everywhere and nothing is left to do.
func idleScenario(name, description string, options func(h *loadHarness) loadFixtureOptions) steadyScenario {
	return steadyScenario{
		name: name, description: description,
		prepare: func(ctx context.Context, h *loadHarness) *loadFixture {
			f := h.newLoadFixture(ctx, options(h))
			f.hydrate(ctx)
			if f.opts.manualGate {
				f.reportManualGate(ctx, promoterv1alpha1.CommitPhaseSuccess)
			}
			f.waitPromoted(ctx)
			return f
		},
	}
}

// heldScenario is a steady state in which a change is proposed and a gate holds it, leaving open pull requests in
// every strategy.
func heldScenario(name, description string, open int, options func(h *loadHarness) loadFixtureOptions) steadyScenario {
	return steadyScenario{
		name: name, description: description,
		prepare: func(ctx context.Context, h *loadHarness) *loadFixture {
			f := h.newLoadFixture(ctx, options(h))
			f.hydrate(ctx)
			if f.opts.manualGate {
				f.reportManualGate(ctx, promoterv1alpha1.CommitPhasePending)
			}
			f.waitHeld(ctx, open)
			return f
		},
	}
}

// fixed returns the options of a fixture that does not depend on the harness settings.
func fixed(opts loadFixtureOptions) func(*loadHarness) loadFixtureOptions {
	return func(*loadHarness) loadFixtureOptions { return opts }
}

// withWebRequest returns the options of a fixture with a WebRequestCommitStatus at the compressed polling interval.
func withWebRequest(w loadWebRequest) func(*loadHarness) loadFixtureOptions {
	return func(h *loadHarness) loadFixtureOptions {
		w.interval = h.compressedPoll()
		return loadFixtureOptions{webRequest: &w}
	}
}

func loadSteadyScenarios() []steadyScenario {
	const (
		gitPasses = `Commit.Author != ""`
		gitFails  = `Commit.Author == ""`
	)
	proposed, active := constants.CommitRefProposed, constants.CommitRefActive
	perEnvironment, perStrategy := promoterv1alpha1.ContextEnvironments, promoterv1alpha1.ContextPromotionStrategy
	return []steadyScenario{
		idleScenario("steady/idle",
			"Every environment is in sync and no pull request is open. Only the ordering gate every "+
				"PromotionStrategy has is configured.",
			fixed(loadFixtureOptions{})),
		idleScenario("steady/idle/kubernetes-gates",
			"In sync, with the gates that only read Kubernetes and git data all passing: a commit status reported "+
				"by an external system, a TimedCommitStatus, a GitCommitStatus and a ScheduledCommitStatus.",
			fixed(loadFixtureOptions{manualGate: true, timedGate: time.Second, gitGate: gitPasses, scheduledGate: loadWindowOpen})),
		idleScenario("steady/idle/argocd",
			"In sync, with an ArgoCDCommitStatus watching one healthy Application per environment.",
			fixed(loadFixtureOptions{argoCD: true})),
		{
			name: "steady/idle/wrcs-pending",
			description: "Nothing to promote, with a WebRequestCommitStatus that polls the GitHub API about the " +
				"proposed commit and has not succeeded for it.",
			prepare: func(ctx context.Context, h *loadHarness) *loadFixture {
				opts := withWebRequest(loadWebRequest{reportOn: proposed, context: perEnvironment, expression: loadWebRequestPending})(h)
				return h.newLoadFixture(ctx, opts)
			},
		},
		idleScenario("steady/idle/wrcs-succeeded",
			"In sync, with a WebRequestCommitStatus on the proposed commit that succeeded for it.",
			withWebRequest(loadWebRequest{reportOn: proposed, context: perEnvironment, expression: loadWebRequestPassing})),
		idleScenario("steady/idle/wrcs-active",
			"In sync, with a WebRequestCommitStatus that polls the GitHub API about the active commit and succeeds.",
			withWebRequest(loadWebRequest{reportOn: active, context: perEnvironment, expression: loadWebRequestPassing})),

		heldScenario("steady/held/manual",
			"A change is proposed to every environment and held by a pending commit status that an external system "+
				"reports, so one pull request per environment stays open.",
			3, fixed(loadFixtureOptions{manualGate: true})),
		heldScenario("steady/held/timed",
			"A change is active in the first environment and held from the others by a TimedCommitStatus that has "+
				"an hour to run.",
			2, fixed(loadFixtureOptions{timedGate: time.Hour})),
		heldScenario("steady/held/git",
			"A change is proposed to every environment and held by a GitCommitStatus whose expression is false.",
			3, fixed(loadFixtureOptions{gitGate: gitFails})),
		heldScenario("steady/held/scheduled",
			"A change is proposed to every environment and held by a ScheduledCommitStatus whose window is closed.",
			3, fixed(loadFixtureOptions{scheduledGate: loadWindowClosed})),
		heldScenario("steady/held/argocd",
			"A change is active in the first environment, whose Argo CD Application has not become healthy, so an "+
				"ArgoCDCommitStatus holds it from the other environments.",
			2, fixed(loadFixtureOptions{argoCD: true, argoCDStuck: 1})),
		heldScenario("steady/held/wrcs-polling",
			"A change is proposed to every environment and held by a WebRequestCommitStatus that polls the GitHub "+
				"API once per environment and has not succeeded.",
			3, withWebRequest(loadWebRequest{reportOn: proposed, context: perEnvironment, expression: loadWebRequestPending})),
		heldScenario("steady/held/wrcs-polling-strategy",
			"As steady/held/wrcs-polling, but the WebRequestCommitStatus makes one request for the whole "+
				"PromotionStrategy instead of one per environment.",
			3, withWebRequest(loadWebRequest{reportOn: proposed, context: perStrategy, expression: loadWebRequestPending})),
		heldScenario("steady/held/wrcs-trigger",
			"A change is proposed to every environment and held by a WebRequestCommitStatus in trigger mode, which "+
				"asks the GitHub API once per proposed commit and has not succeeded.",
			3, withWebRequest(loadWebRequest{reportOn: proposed, context: perEnvironment, expression: loadWebRequestPending, trigger: true})),
		heldScenario("steady/held/no-automerge",
			"A change is proposed to every environment with autoMerge off, so the pull requests wait for a person.",
			3, fixed(loadFixtureOptions{autoMerge: new(false)})),
		heldScenario("steady/held/labels",
			"As steady/held/manual, with two labels applied to every pull request through spec.pullRequest.labels.",
			3, fixed(loadFixtureOptions{manualGate: true, labels: `['promoter', 'automated']`})),
	}
}

// promotionEvent is one change promoted through every environment of a fixture.
func promotionEvent(name, description string, options func(h *loadHarness) loadFixtureOptions) eventScenario {
	return eventScenario{
		name: name, description: description,
		prepare: func(ctx context.Context, h *loadHarness) *loadFixture {
			return h.newLoadFixture(ctx, options(h))
		},
		trigger: func(ctx context.Context, _ *loadHarness, f *loadFixture) {
			f.hydrate(ctx)
			if f.opts.manualGate {
				f.reportManualGate(ctx, promoterv1alpha1.CommitPhaseSuccess)
			}
			f.waitPromoted(ctx)
		},
	}
}

// heldFixture returns a fixture with a change proposed to every environment and held by the manual gate.
func heldFixture(ctx context.Context, h *loadHarness, opts loadFixtureOptions) *loadFixture {
	opts.manualGate = true
	f := h.newLoadFixture(ctx, opts)
	f.hydrate(ctx)
	f.reportManualGate(ctx, promoterv1alpha1.CommitPhasePending)
	f.waitHeld(ctx, len(f.envs))
	return f
}

func loadEventScenarios() []eventScenario {
	held := func(ctx context.Context, h *loadHarness) *loadFixture {
		return heldFixture(ctx, h, loadFixtureOptions{})
	}
	promoted := func(opts loadFixtureOptions) func(ctx context.Context, h *loadHarness) *loadFixture {
		return func(ctx context.Context, h *loadHarness) *loadFixture {
			f := h.newLoadFixture(ctx, opts)
			f.hydrate(ctx)
			f.waitPromoted(ctx)
			return f
		}
	}
	onboarding := func(name, description string, opts loadFixtureOptions) eventScenario {
		return eventScenario{
			name: name, description: description,
			prepare: func(ctx context.Context, h *loadHarness) *loadFixture { return h.buildLoadFixture(ctx, opts) },
			trigger: func(ctx context.Context, _ *loadHarness, f *loadFixture) { f.create(ctx) },
		}
	}
	// At shipped intervals a WebRequestCommitStatus polls once a minute.
	shippedWebRequest := func(*loadHarness) loadFixtureOptions {
		return loadFixtureOptions{webRequest: &loadWebRequest{
			reportOn: constants.CommitRefProposed, context: promoterv1alpha1.ContextEnvironments,
			expression: loadWebRequestPassing, interval: loadShippedPoll,
		}}
	}
	return []eventScenario{
		onboarding("event/onboarding",
			"A PromotionStrategy is created for a repository the promoter has not seen, on an App installation it "+
				"has not used: the installation is looked up, a token minted, and the branches fetched.",
			loadFixtureOptions{}),
		onboarding("event/onboarding/20-strategies",
			"As event/onboarding, for 20 repositories on one installation at once.",
			loadFixtureOptions{strategies: 20}),

		promotionEvent("event/promotion",
			"One change is hydrated and promoted through every environment, with only the ordering gate: pull "+
				"requests are opened and merged one environment after the other.",
			fixed(loadFixtureOptions{})),
		promotionEvent("event/promotion/no-publish",
			"As event/promotion, with the ordering gate's commit statuses kept in Kubernetes (publishTo: []).",
			fixed(loadFixtureOptions{noPublish: true})),
		promotionEvent("event/promotion/hydrator-notes",
			"As event/promotion, with a hydrator.metadata git note on every hydrated commit, as Argo CD's hydrator "+
				"writes them.",
			fixed(loadFixtureOptions{hydratorNotes: true})),
		promotionEvent("event/promotion/manual-gate",
			"As event/promotion, with a proposed commit status that an external system reports as passed.",
			fixed(loadFixtureOptions{manualGate: true})),
		promotionEvent("event/promotion/timed-gate",
			"As event/promotion, with a TimedCommitStatus of two seconds per environment.",
			fixed(loadFixtureOptions{timedGate: 2 * time.Second})),
		promotionEvent("event/promotion/git-gate",
			"As event/promotion, with a passing GitCommitStatus.",
			fixed(loadFixtureOptions{gitGate: `Commit.Author != ""`})),
		promotionEvent("event/promotion/scheduled-gate",
			"As event/promotion, with a ScheduledCommitStatus whose window is open.",
			fixed(loadFixtureOptions{scheduledGate: loadWindowOpen})),
		promotionEvent("event/promotion/argocd-gate",
			"As event/promotion, with an ArgoCDCommitStatus: each environment's Application becomes healthy "+
				"shortly after its merge.",
			fixed(loadFixtureOptions{argoCD: true})),
		promotionEvent("event/promotion/wrcs-gate",
			"As event/promotion, with a WebRequestCommitStatus that polls the GitHub API once a minute and passes.",
			shippedWebRequest),
		func() eventScenario {
			sc := promotionEvent("event/promotion/mergeability-delay",
				"As event/promotion, with GitHub taking half a second to compute whether each new pull request "+
					"can merge, as it does.",
				fixed(loadFixtureOptions{}))
			sc.mergeabilityDelay = 500 * time.Millisecond
			return sc
		}(),
		promotionEvent("event/promotion/20-strategies",
			"As event/promotion, for 20 PromotionStrategies on one installation at once.",
			fixed(loadFixtureOptions{strategies: 20})),

		{
			name: "event/rehydrate-open-prs",
			description: "While a pull request is open and held in every environment, a new change is hydrated: " +
				"the pull requests move to the new commit and the gate is reported pending on it.",
			prepare: held,
			trigger: func(ctx context.Context, _ *loadHarness, f *loadFixture) {
				f.hydrate(ctx)
				f.reportManualGate(ctx, promoterv1alpha1.CommitPhasePending)
				f.waitHeld(ctx, len(f.envs))
			},
		},
		{
			name: "event/gate-flap",
			description: "While a pull request is open and held in every environment, the external commit status " +
				"of each flips between failure and pending five times (10 changes per environment).",
			prepare: held,
			trigger: func(ctx context.Context, _ *loadHarness, f *loadFixture) {
				for range 5 {
					f.reportManualGate(ctx, promoterv1alpha1.CommitPhaseFailure)
					loadSleep(ctx, time.Second)
					f.reportManualGate(ctx, promoterv1alpha1.CommitPhasePending)
					loadSleep(ctx, time.Second)
				}
			},
		},
		{
			name: "event/argocd-flap",
			description: "With everything in sync, the first environment's Argo CD Application goes Progressing " +
				"and back to Healthy five times (10 changes).",
			prepare: promoted(loadFixtureOptions{argoCD: true}),
			trigger: func(ctx context.Context, _ *loadHarness, f *loadFixture) {
				for range 5 {
					f.setApplicationHealth(ctx, 0, argocd.HealthStatusProgressing)
					loadSleep(ctx, time.Second)
					f.setApplicationHealth(ctx, 0, argocd.HealthStatusHealthy)
					loadSleep(ctx, time.Second)
				}
			},
		},
		{
			name: "event/human-merge",
			description: "With autoMerge off, a person merges the open pull request of each environment in " +
				"GitHub, one after the other.",
			prepare: func(ctx context.Context, h *loadHarness) *loadFixture {
				f := h.newLoadFixture(ctx, loadFixtureOptions{autoMerge: new(false)})
				f.hydrate(ctx)
				f.waitHeld(ctx, len(f.envs))
				return f
			},
			trigger: func(ctx context.Context, _ *loadHarness, f *loadFixture) {
				for e, env := range f.envs {
					f.actByHand(ctx, "merge", 0, env)
					f.waitFor(ctx, "the merged change to be active in "+env, func(g Gomega, i int, ps *promoterv1alpha1.PromotionStrategy) {
						g.Expect(ps.Status.Environments[e].Active.Dry.Sha).To(Equal(f.drySHAs[i]))
					})
				}
				f.waitPromoted(ctx)
			},
		},
		{
			name: "event/pr-closed",
			description: "A person closes the open pull request of the first environment in GitHub without " +
				"merging it, and the promoter opens a new one. The promoter does not act on pull request webhooks, " +
				"so it notices at the next requeue; this event therefore runs with the timers compressed and " +
				"includes up to one cycle of the periodic requests of steady/held/manual.",
			compressed: true,
			prepare:    held,
			trigger: func(ctx context.Context, _ *loadHarness, f *loadFixture) {
				closed := f.actByHand(ctx, "close", 0, f.envs[0])
				Eventually(func(g Gomega) {
					prs, err := f.admin.PullRequests(ctx, f.repos[0].ID, "open")
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(prs).To(ContainElement(And(HaveField("Target", f.envs[0]), Not(HaveField("Number", closed)))))
				}).WithContext(ctx).WithTimeout(loadSetupTimeout).WithPolling(loadPollInterval).Should(Succeed())
				f.waitHeld(ctx, len(f.envs))
			},
		},
		{
			name: "event/strategy-deleted",
			description: "A PromotionStrategy is deleted while a pull request is open in every environment, and " +
				"the promoter closes them.",
			prepare: held,
			trigger: func(ctx context.Context, _ *loadHarness, f *loadFixture) { f.deleteStrategies(ctx) },
		},
		{
			name: "event/conflict",
			description: "Every environment branch was changed directly, so the next change conflicts with it: " +
				"the promoter resolves each conflict with a merge commit it pushes, then promotes.",
			prepare: promoted(loadFixtureOptions{}),
			trigger: func(ctx context.Context, _ *loadHarness, f *loadFixture) {
				f.commitToActiveBranches(ctx)
				f.hydrate(ctx)
				f.waitPromoted(ctx)
			},
		},
	}
}

func loadThrottleScenarios() []throttleScenario {
	const path = "https://docs.github.com/rest/overview/rate-limits-for-the-rest-api"
	primary := func(until time.Time) scmserver.FaultRule {
		return scmserver.FaultRule{
			Status: http.StatusForbidden,
			Headers: map[string]string{
				"X-RateLimit-Limit": "5000", "X-RateLimit-Remaining": "0", "X-RateLimit-Used": "5000",
				"X-RateLimit-Resource": "core", "X-RateLimit-Reset": strconv.FormatInt(until.Unix(), 10),
			},
			Body: `{"message":"API rate limit exceeded for installation.","documentation_url":"` + path + `"}`,
		}
	}
	return []throttleScenario{
		{
			name: "throttled/primary-rate-limit", window: 90 * time.Second, fault: primary,
			description: "GitHub answers 403 with X-RateLimit-Remaining: 0 (the installation's hourly budget is " +
				"spent) while a new change needs every pull request updated.",
		},
		{
			name: "throttled/primary-rate-limit/wrcs", window: 150 * time.Second, fault: primary, webRequest: true,
			description: "As throttled/primary-rate-limit, with a WebRequestCommitStatus polling the GitHub API " +
				"once a minute per environment.",
		},
		{
			name: "throttled/secondary-rate-limit", window: 90 * time.Second,
			description: "GitHub answers 403 with Retry-After (a secondary rate limit) while a new change needs " +
				"every pull request updated.",
			fault: func(until time.Time) scmserver.FaultRule {
				return scmserver.FaultRule{
					Status:  http.StatusForbidden,
					Headers: map[string]string{"Retry-After": strconv.Itoa(int(time.Until(until).Seconds()))},
					Body: `{"message":"You have exceeded a secondary rate limit. Please wait a few minutes before ` +
						`you try again.","documentation_url":"` + path + `#about-secondary-rate-limits"}`,
				}
			},
		},
		{
			name: "throttled/server-errors", window: 90 * time.Second,
			description: "GitHub answers every REST request with 502 while a new change needs every pull request " +
				"updated.",
			fault: func(time.Time) scmserver.FaultRule {
				return scmserver.FaultRule{Status: http.StatusBadGateway}
			},
		},
	}
}

// runSteady brings a fixture into each scenario's state, observes them all together with the periodic timers
// compressed, and returns their results by scenario name.
func (h *loadHarness) runSteady(ctx context.Context, group string, scenarios []steadyScenario) map[string]loadreport.Result {
	GinkgoHelper()
	// Compress before creating the fixtures: an interval takes effect at an object's next reconcile.
	requeue := h.compressedRequeue()
	setLoadRequeue(ctx, requeue)

	type running struct {
		fixture  *loadFixture
		scenario steadyScenario
	}
	var all []running
	for _, sc := range scenarios {
		if !h.wants(sc.name) {
			continue
		}
		By("preparing " + sc.name)
		all = append(all, running{fixture: sc.prepare(ctx, h), scenario: sc})
	}
	results := map[string]loadreport.Result{}
	if len(all) == 0 {
		return results
	}

	By("waiting for every fixture to settle")
	notes := map[string][]string{}
	for _, r := range all {
		quiet := max(2*requeue, 20*time.Second)
		if !h.waitQuiet(ctx, r.fixture, r.fixture.created, quiet, loadSetupTimeout, false) {
			notes[r.scenario.name] = append(notes[r.scenario.name], "The fixture was still changing when the observation started.")
		}
	}

	By("observing for " + h.window.String())
	start, end := h.observe(ctx, h.window)
	for _, r := range all {
		results[r.scenario.name] = h.add(loadreport.Scenario{
			Name: r.scenario.name, Group: group, Kind: loadreport.KindSteady,
			Description: r.scenario.description, Notes: notes[r.scenario.name],
			Topology: r.fixture.topology(ctx),
			Start:    start, End: end, Settled: true,
			Compression: h.compression, Requeue: scmserver.D(requeue), Poll: r.fixture.pollInterval(),
		}, r.fixture)
	}
	h.writeReport()

	By("tearing the fixtures down")
	for _, r := range all {
		r.fixture.teardown(ctx)
	}
	return results
}

// agree adds a consistency check over the results that were measured; a scenario left out by the focus is skipped.
func (h *loadHarness) agree(name string, tolerance float64, results map[string]loadreport.Result, names []string, value func(loadreport.Result) float64) {
	var labels []string
	var values []float64
	for _, scenario := range names {
		if res, ok := results[scenario]; ok {
			labels = append(labels, scenario)
			values = append(values, value(res))
		}
	}
	if len(values) > 1 {
		h.checks = append(h.checks, loadreport.Agreement(name, tolerance, labels, values))
	}
}

var _ = Describe("SCM load", Label(scmLoadLabel), Serial, Ordered, func() {
	var h *loadHarness
	// steady holds the results of the main steady-state run, which later runs are compared with.
	steady := map[string]loadreport.Result{}

	BeforeAll(func(ctx SpecContext) {
		if os.Getenv(envSCMLoad) == "" {
			Skip("the SCM load harness runs only with " + envSCMLoad + " set; use `make test-scm-load`")
		}
		Expect(scmTarget.provider).To(Equal(scmserver.GitHub),
			"the SCM load harness measures GitHub: run it with "+envSCMProvider+"=github")
		h = newLoadHarness(ctx)
		DeferCleanup(func(ctx SpecContext) {
			setLoadRequeue(ctx, h.shippedRequeue)
			h.writeReport()
		})
	})

	It("measures steady states with the periodic timers compressed", func(ctx SpecContext) {
		steady = h.runSteady(ctx, loadGroupSteady, loadSteadyScenarios())
		if len(steady) == 0 {
			Skip("no steady scenario selected")
		}
	}, SpecTimeout(45*time.Minute))

	It("measures the same rates at twice the compression", func(ctx SpecContext) {
		// The extrapolation holds only if the load follows the compressed timers: a state measured with the timers
		// twice as fast must come out at the same rate per hour.
		compression, window := h.compression, h.window
		h.compression, h.window = 2*compression, window/2
		defer func() { h.compression, h.window = compression, window }()

		pairs := map[string]string{
			"linearity/idle":              "steady/idle",
			"linearity/held/manual":       "steady/held/manual",
			"linearity/held/wrcs-polling": "steady/held/wrcs-polling",
		}
		var scenarios []steadyScenario
		for _, sc := range loadSteadyScenarios() {
			for twice, once := range pairs {
				if sc.name == once {
					sc.name = twice
					sc.description = "As " + once + ", with the timers compressed twice as much."
					scenarios = append(scenarios, sc)
				}
			}
		}
		results := h.runSteady(ctx, loadGroupLinearity, scenarios)
		if len(results) == 0 {
			Skip("no linearity scenario selected")
		}
		for twice, once := range pairs {
			a, okA := steady[once]
			b, okB := results[twice]
			if !okA || !okB {
				continue
			}
			labels := []string{fmt.Sprintf("%gx", compression), fmt.Sprintf("%gx", 2*compression)}
			h.checks = append(h.checks,
				loadreport.Agreement(once+": REST per hour at both compressions", 0.05, labels,
					[]float64{a.Rates.RESTPerHour, b.Rates.RESTPerHour}),
				loadreport.Agreement(once+": git commands per hour at both compressions", 0.05, labels,
					[]float64{a.Rates.GitCommandsPerHour, b.Rates.GitCommandsPerHour}))
		}
		h.writeReport()
	}, SpecTimeout(45*time.Minute))

	It("measures how steady-state load scales", func(ctx SpecContext) {
		var scenarios []steadyScenario
		var idle, held, byEnvironments []string
		for _, n := range []int{1, 5, 20} {
			idleName, heldName := fmt.Sprintf("scale/idle/strategies=%d", n), fmt.Sprintf("scale/held/strategies=%d", n)
			idle, held = append(idle, idleName), append(held, heldName)
			scenarios = append(scenarios,
				idleScenario(idleName,
					fmt.Sprintf("As steady/idle, with %d PromotionStrategies on one App installation.", n),
					fixed(loadFixtureOptions{strategies: n})),
				heldScenario(heldName,
					fmt.Sprintf("As steady/held/manual, with %d PromotionStrategies on one App installation.", n),
					3, fixed(loadFixtureOptions{strategies: n, manualGate: true})))
		}
		for _, e := range []int{1, 6} {
			name := fmt.Sprintf("scale/held/environments=%d", e)
			byEnvironments = append(byEnvironments, name)
			scenarios = append(scenarios, heldScenario(name,
				fmt.Sprintf("As steady/held/manual, with %d environments.", e),
				e, fixed(loadFixtureOptions{environments: e, manualGate: true})))
		}
		results := h.runSteady(ctx, loadGroupScale, scenarios)
		if len(results) == 0 {
			Skip("no scale scenario selected")
		}
		perStrategy := func(value func(*loadreport.Rates) float64) func(loadreport.Result) float64 {
			return func(res loadreport.Result) float64 {
				return value(res.Rates) / float64(res.Topology.PromotionStrategies)
			}
		}
		perEnvironment := func(value func(*loadreport.Rates) float64) func(loadreport.Result) float64 {
			return func(res loadreport.Result) float64 {
				return value(res.Rates) / float64(res.Topology.PromotionStrategies*res.Topology.Environments)
			}
		}
		rest := func(r *loadreport.Rates) float64 { return r.RESTPerHour }
		git := func(r *loadreport.Rates) float64 { return r.GitCommandsPerHour }
		h.agree("idle: git commands per hour per strategy, by number of strategies", 0.05, results, idle, perStrategy(git))
		h.agree("held: git commands per hour per strategy, by number of strategies", 0.05, results, held, perStrategy(git))
		h.agree("held: REST per hour per strategy, by number of strategies", 0.05, results, held, perStrategy(rest))
		byEnvironments = append(byEnvironments, "scale/held/strategies=1")
		h.agree("held: REST per hour per environment, by number of environments", 0.05, results, byEnvironments, perEnvironment(rest))
		h.agree("held: git commands per hour per environment, by number of environments", 0.05, results, byEnvironments, perEnvironment(git))
		h.writeReport()
	}, SpecTimeout(45*time.Minute))

	It("measures events at the shipped intervals", func(ctx SpecContext) {
		ran := false
		for _, sc := range loadEventScenarios() {
			if !h.wants(sc.name) {
				continue
			}
			ran = true
			requeue := h.shippedRequeue
			if sc.compressed {
				requeue = h.compressedRequeue()
			}
			setLoadRequeue(ctx, requeue)
			h.setMergeabilityDelay(ctx, sc.mergeabilityDelay)

			By("preparing " + sc.name)
			f := sc.prepare(ctx, h)
			// The event starts from rest: whatever creating the fixture set off has to be over.
			var notes []string
			if !h.waitQuiet(ctx, f, f.created, loadEventQuiet, loadSetupTimeout, true) {
				notes = append(notes, "The fixture was still busy when the event started.")
			}

			By("triggering " + sc.name)
			start := time.Now()
			sc.trigger(ctx, h, f)
			settled := h.waitQuiet(ctx, f, start, loadEventQuiet, loadSetupTimeout, true)
			// The event ends with the promoter's last request, not with the quiet period that proves it.
			end := h.lastActivity(f, start, true)
			h.add(loadreport.Scenario{
				Name: sc.name, Group: loadGroupEvents, Kind: loadreport.KindEvent,
				Description: sc.description, Notes: notes,
				Topology: f.topology(ctx),
				Start:    start, End: end, Settled: settled,
				Compression: 1, Requeue: scmserver.D(requeue),
			}, f)
			h.writeReport()
			h.setMergeabilityDelay(ctx, 0)
			f.teardown(ctx)
		}
		if !ran {
			Skip("no event scenario selected")
		}
	}, SpecTimeout(90*time.Minute))

	It("measures what the promoter sends while the API refuses it", func(ctx SpecContext) {
		setLoadRequeue(ctx, h.shippedRequeue)
		ran := false
		for _, sc := range loadThrottleScenarios() {
			if !h.wants(sc.name) {
				continue
			}
			ran = true
			By("preparing " + sc.name)
			opts := loadFixtureOptions{}
			if sc.webRequest {
				opts.webRequest = &loadWebRequest{
					reportOn: constants.CommitRefProposed, context: promoterv1alpha1.ContextEnvironments,
					expression: loadWebRequestPending, interval: loadShippedPoll,
				}
			}
			f := heldFixture(ctx, h, opts)
			h.waitQuiet(ctx, f, f.created, loadEventQuiet, loadSetupTimeout, !sc.webRequest)

			By("refusing the REST requests of " + sc.name)
			until := time.Now().Add(sc.window)
			rule := sc.fault(until)
			rule.Provider = scmserver.GitHub
			rule.Path = "^/api/v3/repos/" + f.owner + "/"
			_, err := h.admin.AddFault(ctx, rule)
			Expect(err).NotTo(HaveOccurred())

			// Give the promoter work: a new change moves every pull request and commit status.
			start := time.Now()
			f.hydrate(ctx)
			f.reportManualGate(ctx, promoterv1alpha1.CommitPhasePending)
			_, end := h.observe(ctx, time.Until(until))
			h.add(loadreport.Scenario{
				Name: sc.name, Group: loadGroupThrottled, Kind: loadreport.KindWindow,
				Description: sc.description,
				Topology:    f.topology(ctx),
				Start:       start, End: end, Settled: true,
				Compression: 1, Requeue: scmserver.D(h.shippedRequeue), Poll: f.pollInterval(),
			}, f)
			h.writeReport()

			Expect(h.admin.ClearFaults(ctx)).To(Succeed())
			// The client honors the reset time the fault announced; let it pass before cleaning up through it.
			loadSleep(ctx, time.Until(until)+2*time.Second)
			f.teardown(ctx)
		}
		if !ran {
			Skip("no throttle scenario selected")
		}
	}, SpecTimeout(45*time.Minute))
})
