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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/test/scmserver"
	"github.com/argoproj-labs/gitops-promoter/test/scmserver/loadreport"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The SCM load harness measures how many requests the controllers send to GitHub. It runs only when asked to
// (`make test-scm-load`), against the test SCM server's GitHub listener, and counts requests in the server's request
// log. See docs/contributing/scm-load.md.
const (
	scmLoadLabel = "scm-load"

	// envSCMLoad turns the harness on. Without it the specs are skipped, so the regular suites never run them.
	envSCMLoad = "PROMOTER_SCM_LOAD"
	// envSCMLoadOut is the directory the reports are written to.
	envSCMLoadOut = "PROMOTER_SCM_LOAD_OUT"
	// envSCMLoadCompression is how many times faster than shipped the periodic timers run in steady states.
	envSCMLoadCompression = "PROMOTER_SCM_LOAD_COMPRESSION"
	// envSCMLoadWindow is how long steady states are observed, as a Go duration.
	envSCMLoadWindow = "PROMOTER_SCM_LOAD_WINDOW"
	// envSCMLoadFocus is a regular expression; only scenarios whose name matches it run.
	envSCMLoadFocus = "PROMOTER_SCM_LOAD_FOCUS"

	loadNamespace          = "default"
	loadDefaultCompression = 10.0
	loadDefaultWindow      = 5 * time.Minute
	// loadShippedPoll is the polling interval WebRequestCommitStatus rates are extrapolated to: the CRD's default.
	loadShippedPoll = time.Minute
	// loadManualGate is the key of the proposed commit status the harness reports itself, as an external system
	// would.
	loadManualGate = "manual"

	loadWindowOpen   = "open"
	loadWindowClosed = "closed"

	loadSetupTimeout = 3 * time.Minute
	loadPollInterval = time.Second
)

// loadHarness holds the state of one harness run.
type loadHarness struct {
	admin  *scmserver.AdminClient
	focus  *regexp.Regexp
	outDir string
	// recs is every request the server logged since the run started, in the order they completed.
	recs        []scmserver.RequestRecord
	results     []loadreport.Result
	checks      []loadreport.Check
	budgets     loadreport.Budgets
	compression float64
	window      time.Duration
	// shippedRequeue is the requeue interval of the shipped ControllerConfiguration, which every controller shares.
	shippedRequeue time.Duration
	cursor         int64
	fixtureSeq     int
	truncated      bool
}

// newLoadHarness reads the harness settings and prepares the server for a measurement.
func newLoadHarness(ctx context.Context) *loadHarness {
	GinkgoHelper()
	h := &loadHarness{
		admin:       scmTarget.admin,
		budgets:     loadreport.DefaultBudgets(),
		compression: loadDefaultCompression,
		window:      loadDefaultWindow,
		outDir:      envOr(envSCMLoadOut, filepath.Join("..", "..", "bin", "scm-load")),
	}
	if raw := os.Getenv(envSCMLoadCompression); raw != "" {
		c, err := strconv.ParseFloat(raw, 64)
		Expect(err).NotTo(HaveOccurred(), envSCMLoadCompression)
		Expect(c).To(BeNumerically(">=", 1), envSCMLoadCompression)
		h.compression = c
	}
	if raw := os.Getenv(envSCMLoadWindow); raw != "" {
		d, err := time.ParseDuration(raw)
		Expect(err).NotTo(HaveOccurred(), envSCMLoadWindow)
		h.window = d
	}
	if raw := os.Getenv(envSCMLoadFocus); raw != "" {
		re, err := regexp.Compile(raw)
		Expect(err).NotTo(HaveOccurred(), envSCMLoadFocus)
		h.focus = re
	}
	Expect(os.MkdirAll(h.outDir, 0o750)).To(Succeed())

	h.shippedRequeue = shippedRequeueDuration()

	// The suite turns the server's rate limiting off. Turn it back on, far above anything the run sends, so that
	// responses carry the rate limit headers GitHub sends and the client parses.
	h.setMergeabilityDelay(ctx, 0)

	// Start recording from here: what an earlier run left in the log is not ours.
	h.sync(ctx)
	h.recs, h.truncated = nil, false
	return h
}

// setMergeabilityDelay sets the server's behavior for the run, with GitHub taking delay to work out whether a new
// pull request can merge. The suite runs with that at zero, as the harness does unless a scenario asks otherwise.
func (h *loadHarness) setMergeabilityDelay(ctx context.Context, delay time.Duration) {
	GinkgoHelper()
	behavior := scmserver.DefaultBehavior(scmserver.GitHub)
	behavior.MergeabilityDelay = scmserver.D(delay)
	behavior.RateLimit = 10_000_000
	Expect(h.admin.SetBehavior(ctx, scmserver.GitHub, behavior)).To(Succeed())
}

// wants reports whether the scenario called name is selected to run.
func (h *loadHarness) wants(name string) bool {
	return h.focus == nil || h.focus.MatchString(name)
}

// compressedRequeue is the requeue interval steady states run at.
func (h *loadHarness) compressedRequeue() time.Duration {
	return time.Duration(float64(h.shippedRequeue) / h.compression).Round(time.Second)
}

// compressedPoll is the WebRequestCommitStatus polling interval steady states run at.
func (h *loadHarness) compressedPoll() time.Duration {
	return time.Duration(float64(loadShippedPoll) / h.compression).Round(time.Second)
}

// sync appends the requests the server logged since the last call.
func (h *loadHarness) sync(ctx context.Context) {
	GinkgoHelper()
	recs, err := h.admin.RequestsAfter(ctx, h.cursor)
	Expect(err).NotTo(HaveOccurred())
	if len(recs) == 0 {
		return
	}
	if h.cursor != 0 && recs[0].Seq != h.cursor+1 {
		// The server keeps a bounded log: a gap means it dropped requests before we read them.
		h.truncated = true
	}
	h.recs = append(h.recs, recs...)
	h.cursor = recs[len(recs)-1].Seq
}

// between returns the fixture's requests that started in [start, end].
func (h *loadHarness) between(f *loadFixture, start, end time.Time) []scmserver.RequestRecord {
	var in []scmserver.RequestRecord
	for _, rec := range h.recs {
		if !rec.Time.Before(start) && !rec.Time.After(end) {
			in = append(in, rec)
		}
	}
	return loadreport.Select(in, f.selector())
}

// observe records for d and returns when the observation started and ended.
func (h *loadHarness) observe(ctx context.Context, d time.Duration) (time.Time, time.Time) {
	GinkgoHelper()
	h.sync(ctx)
	start := time.Now()
	for time.Since(start) < d {
		loadSleep(ctx, min(2*time.Second, d-time.Since(start)))
		h.sync(ctx)
	}
	end := time.Now()
	// Let requests that started inside the window complete and reach the log.
	loadSleep(ctx, 500*time.Millisecond)
	h.sync(ctx)
	return start, end
}

// lastActivity returns when the fixture's controllers last did something on the SCM since the given time. With
// anyRequest it is their last request of any kind; otherwise their last change (a write, a push, or a call
// authenticated as the App) or failed request, which is what a steady state must not have.
func (h *loadHarness) lastActivity(f *loadFixture, since time.Time, anyRequest bool) time.Time {
	last := since
	for _, rec := range h.between(f, since, time.Now().Add(time.Hour)) {
		bucket := loadreport.Classify(rec)
		if bucket == loadreport.BucketFixture {
			continue
		}
		changed := bucket == loadreport.BucketRESTWrite || bucket == loadreport.BucketGitWrite || bucket == loadreport.BucketApp
		failed := rec.StatusCode >= 400 && bucket != loadreport.BucketGitAuthChallenge
		if (anyRequest || changed || failed) && rec.Time.After(last) {
			last = rec.Time
		}
	}
	return last
}

// waitQuiet waits until the fixture's controllers have been quiet (see lastActivity) for the quiet period, and
// reports whether that happened in time.
func (h *loadHarness) waitQuiet(ctx context.Context, f *loadFixture, since time.Time, quiet time.Duration, anyRequest bool) bool {
	GinkgoHelper()
	deadline := time.Now().Add(loadSetupTimeout)
	for {
		h.sync(ctx)
		if time.Since(h.lastActivity(f, since, anyRequest)) >= quiet {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		loadSleep(ctx, loadPollInterval)
	}
}

// add analyzes a scenario's requests and keeps the result for the report.
func (h *loadHarness) add(sc loadreport.Scenario, f *loadFixture) loadreport.Result {
	GinkgoHelper()
	sc.Truncated = h.truncated
	res := loadreport.Analyze(sc, h.between(f, sc.Start, sc.End), h.budgets)
	h.results = append(h.results, res)
	GinkgoWriter.Printf("scm-load %s: %+v\n", sc.Name, res.Totals)
	return res
}

// writeReport writes report.json and report.md with everything measured so far.
func (h *loadHarness) writeReport() {
	GinkgoHelper()
	report := &loadreport.Report{
		SchemaVersion:  loadreport.SchemaVersion,
		GeneratedAt:    time.Now().UTC(),
		Provider:       string(scmserver.GitHub),
		Revision:       os.Getenv("PROMOTER_SCM_LOAD_REVISION"),
		Budgets:        h.budgets,
		ShippedRequeue: h.shippedRequeue.String(),
		ShippedPoll:    loadShippedPoll.String(),
		Scenarios:      h.results,
		Checks:         h.checks,
		Limitations:    loadLimitations(),
	}
	for name, write := range map[string]func(*os.File) error{
		"report.json": func(f *os.File) error { return report.WriteJSON(f) },
		"report.md":   func(f *os.File) error { return report.WriteMarkdown(f) },
	} {
		file, err := os.Create(filepath.Join(h.outDir, name))
		Expect(err).NotTo(HaveOccurred())
		Expect(write(file)).To(Succeed())
		Expect(file.Close()).To(Succeed())
	}
	abs, err := filepath.Abs(h.outDir)
	Expect(err).NotTo(HaveOccurred())
	GinkgoWriter.Printf("scm-load report written to %s\n", abs)
}

// loadLimitations is what the report's numbers do not cover.
func loadLimitations() []string {
	return []string{
		"Measured against the test SCM server, not GitHub: it counts every request the promoter sends, but does not " +
			"reproduce GitHub's own accounting, such as secondary rate limits.",
		"A controller restart is not measured. The controllers run in the test process, whose GitHub clients and git " +
			"clones cannot be reset. On start every CommitStatus is reconciled once, which is one check run write each.",
		"Costs that follow timers the harness does not compress are counted but not extrapolated: an installation " +
			"token is minted about once an hour per installation, and the installation of an App on a repository " +
			"owner is looked up once every 30 minutes.",
		"Only event/promotion/hydrator-notes has the hydrator's git notes; the other scenarios run without them.",
	}
}

// loadSleep sleeps for d, or fails the spec when its context ends first.
func loadSleep(ctx context.Context, d time.Duration) {
	GinkgoHelper()
	if d <= 0 {
		return
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		Fail("context ended while the load harness was waiting: " + ctx.Err().Error())
	}
}

// walkJSON calls visit for every object in a decoded JSON document.
func walkJSON(v any, visit func(map[string]any)) {
	switch v := v.(type) {
	case map[string]any:
		visit(v)
		for _, child := range v {
			walkJSON(child, visit)
		}
	case []any:
		for _, child := range v {
			walkJSON(child, visit)
		}
	default:
		// Scalars have no children.
	}
}

// shippedRequeueDuration returns the requeue interval of the shipped ControllerConfiguration. The harness compresses
// every controller's interval by one factor, which only extrapolates cleanly when they all share one value.
func shippedRequeueDuration() time.Duration {
	GinkgoHelper()
	data, err := json.Marshal(controllerConfigurationForTests())
	Expect(err).NotTo(HaveOccurred())
	var doc any
	Expect(json.Unmarshal(data, &doc)).To(Succeed())
	found := map[string]bool{}
	walkJSON(doc, func(obj map[string]any) {
		if raw, ok := obj["requeueDuration"].(string); ok {
			found[raw] = true
		}
	})
	Expect(found).To(HaveLen(1), "the shipped ControllerConfiguration no longer uses one requeueDuration for every controller: %v", found)
	var shipped time.Duration
	for raw := range found {
		shipped, err = time.ParseDuration(raw)
		Expect(err).NotTo(HaveOccurred())
	}
	return shipped
}

// setLoadRequeue sets every controller's requeueDuration and waits until the reconcilers see it. An interval only
// takes effect at an object's next reconcile, so set it before creating the objects it should apply to.
func setLoadRequeue(ctx context.Context, d time.Duration) {
	GinkgoHelper()
	key := types.NamespacedName{Namespace: loadNamespace, Name: settings.ControllerConfigurationName}
	var cc promoterv1alpha1.ControllerConfiguration
	Expect(k8sClient.Get(ctx, key, &cc)).To(Succeed())
	data, err := json.Marshal(&cc)
	Expect(err).NotTo(HaveOccurred())
	var doc any
	Expect(json.Unmarshal(data, &doc)).To(Succeed())
	walkJSON(doc, func(obj map[string]any) {
		if _, ok := obj["requeueDuration"]; ok {
			obj["requeueDuration"] = d.String()
		}
	})
	data, err = json.Marshal(doc)
	Expect(err).NotTo(HaveOccurred())
	patched := &promoterv1alpha1.ControllerConfiguration{}
	Expect(json.Unmarshal(data, patched)).To(Succeed())
	Expect(k8sClient.Update(ctx, patched)).To(Succeed())

	Eventually(func(g Gomega) {
		got, err := settings.GetRequeueDuration[promoterv1alpha1.ChangeTransferPolicyConfiguration](ctx, settingsMgr)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(got).To(Equal(d))
	}).WithContext(ctx).WithTimeout(30 * time.Second).WithPolling(100 * time.Millisecond).Should(Succeed())
}

// loadFixtureOptions describes the promotion setup of a scenario.
type loadFixtureOptions struct {
	// autoMerge is the autoMerge setting of every environment; nil leaves the default (true).
	autoMerge *bool
	// strategies is the number of PromotionStrategies, each with its own repository. Defaults to 1.
	strategies int
	// environments is the number of environments per strategy. Defaults to 3.
	environments int
	// webRequest adds a WebRequestCommitStatus that calls the GitHub API with the ScmProvider's credentials.
	webRequest *loadWebRequest
	// gitGate adds a GitCommitStatus with this expression over every environment's proposed commit.
	gitGate string
	// scheduledGate adds a ScheduledCommitStatus whose promotion window is loadWindowOpen or loadWindowClosed.
	scheduledGate string
	// labels is an expression for dynamic pull request labels.
	labels string
	// timedGate adds a TimedCommitStatus: an environment must have run a commit this long before the next one may
	// take it.
	timedGate time.Duration
	// argoCDStuck is the number of leading environments whose Argo CD Application never becomes healthy.
	argoCDStuck int
	// manualGate adds a proposed commit status to every environment that the harness reports itself.
	manualGate bool
	// noPublish keeps the commit statuses of the ordering gate and the manual gate in Kubernetes (publishTo: []).
	noPublish bool
	// hydratorNotes attaches a hydrator.metadata git note to every hydrated commit, as Argo CD's hydrator does.
	hydratorNotes bool
	// argoCD adds an ArgoCDCommitStatus and an Argo CD Application per environment, which the harness syncs as
	// Argo CD would.
	argoCD bool
}

// loadFixture is one scenario's world: a repository owner with one GitHub App installation and one ScmProvider,
// shared by the fixture's repositories, with a PromotionStrategy per repository. Nothing else uses the owner or the
// App, so every request of the fixture can be told apart in the server's request log.
type loadFixture struct {
	admin *scmserver.AdminClient
	// proposed maps a repository index and environment branch to the head of its proposed ("-next") branch.
	proposed map[int]map[string]string
	name     string
	owner    string
	repos    []scmserver.RepoInfo
	envs     []string
	// drySHAs is the latest dry commit of each repository.
	drySHAs []string
	opts    loadFixtureOptions
	appID   int64
	created time.Time
}

func (f *loadFixture) selector() loadreport.Selector {
	return loadreport.Selector{Owner: f.owner, AppID: f.appID, InstallationID: f.appID * 10}
}

// strategyName is the name of the i-th PromotionStrategy, GitRepository and ordering gate.
func (f *loadFixture) strategyName(i int) string {
	return fmt.Sprintf("%s-%d", f.name, i)
}

func (f *loadFixture) topology(ctx context.Context) loadreport.Topology {
	GinkgoHelper()
	topology := loadreport.Topology{
		PromotionStrategies: f.opts.strategies,
		Environments:        f.opts.environments,
		OpenPullRequests:    f.openPullRequests(ctx),
		Gates:               f.gateNames(),
	}
	if f.opts.webRequest != nil {
		topology.WebRequest = f.opts.webRequest.describe()
	}
	return topology
}

// loadEnvironments returns n environment branches, with familiar names for the usual three.
func loadEnvironments(n int) []string {
	if n == 3 {
		return []string{testBranchDevelopment, testBranchStaging, testBranchProduction}
	}
	envs := make([]string, n)
	for i := range envs {
		envs[i] = fmt.Sprintf("environment/env-%02d", i+1)
	}
	return envs
}

// forEachRepo runs body for every repository of the fixture, concurrently.
func (f *loadFixture) forEachRepo(body func(i int, repo scmserver.RepoInfo)) {
	GinkgoHelper()
	var wg sync.WaitGroup
	for i, repo := range f.repos {
		wg.Go(func() {
			defer GinkgoRecover()
			body(i, repo)
		})
	}
	wg.Wait()
}

// newLoadFixture creates the fixture's repositories on the server and the Kubernetes objects that promote through
// them. It returns once every PromotionStrategy has read its branches.
func (h *loadHarness) newLoadFixture(ctx context.Context, opts loadFixtureOptions) *loadFixture {
	GinkgoHelper()
	f := h.buildLoadFixture(ctx, opts)
	f.create(ctx)
	return f
}

// buildLoadFixture creates the fixture's side of the SCM: the App, and the repositories laid out the way the
// hydrator leaves them. The promoter knows nothing of it until create.
func (h *loadHarness) buildLoadFixture(ctx context.Context, opts loadFixtureOptions) *loadFixture {
	GinkgoHelper()
	if opts.strategies == 0 {
		opts.strategies = 1
	}
	if opts.environments == 0 {
		opts.environments = 3
	}
	h.fixtureSeq++
	f := &loadFixture{
		admin:    h.admin,
		opts:     opts,
		name:     fmt.Sprintf("load-%s-%d", scmTarget.runID, h.fixtureSeq),
		owner:    fmt.Sprintf("load-%s-%d", scmTarget.runID, h.fixtureSeq),
		envs:     loadEnvironments(opts.environments),
		proposed: map[int]map[string]string{},
		drySHAs:  make([]string, opts.strategies),
		created:  time.Now(),
	}

	// One App installed on the owner serves every repository of the fixture, as one installation of the promoter's
	// App serves an organization. App IDs follow the suite's scheme so they never collide with another spec's.
	f.appID = githubAppIDBase + int64(GinkgoParallelProcess())*githubAppIDsPerProcess + scmTarget.githubAppSeq.Add(1)
	Expect(h.admin.RegisterGitHubApp(ctx, scmserver.RegisterGitHubAppRequest{
		AppID:         f.appID,
		PublicKeyPEM:  string(scmTarget.githubKeyPEM),
		Installations: []scmserver.GitHubInstallation{{ID: f.appID * 10, Account: f.owner}},
	})).To(Succeed())

	f.repos = make([]scmserver.RepoInfo, opts.strategies)
	for i := range f.repos {
		f.proposed[i] = map[string]string{}
	}
	var mu sync.Mutex
	f.forEachRepo(func(i int, _ scmserver.RepoInfo) {
		repo, err := h.admin.CreateRepo(ctx, scmserver.CreateRepoRequest{
			Provider: scmserver.GitHub, Owner: f.owner, Name: fmt.Sprintf("repo-%d", i),
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = h.admin.AddWebhook(ctx, scmserver.WebhookSubscription{
			Provider: scmserver.GitHub, Repo: repo.FullName, URL: fmt.Sprintf("http://127.0.0.1:%d/", webhookReceiverPort),
		})
		Expect(err).NotTo(HaveOccurred())
		heads := f.seed(ctx, repo)
		mu.Lock()
		defer mu.Unlock()
		f.repos[i] = repo
		f.proposed[i] = heads
	})
	return f
}

// publishTo is the publishTo of the commit statuses the fixture controls: nil for the default (the SCM).
func (f *loadFixture) publishTo() *[]promoterv1alpha1.CommitStatusPublishTo {
	if !f.opts.noPublish {
		return nil
	}
	return &[]promoterv1alpha1.CommitStatusPublishTo{}
}

// create creates the Kubernetes objects that make the promoter manage the fixture's repositories, and returns once
// every PromotionStrategy has read its branches.
func (f *loadFixture) create(ctx context.Context) {
	GinkgoHelper()
	f.created = time.Now()
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: f.name, Namespace: loadNamespace},
		Data:       map[string][]byte{"githubAppPrivateKey": scmTarget.githubKeyPEM},
	}
	Expect(k8sClient.Create(ctx, secret)).To(Succeed())
	// The installation ID is left for the provider to look up, as most installs of the promoter do.
	scmProvider := &promoterv1alpha1.ScmProvider{
		ObjectMeta: metav1.ObjectMeta{Name: f.name, Namespace: loadNamespace},
		Spec: promoterv1alpha1.ScmProviderSpec{
			SecretRef: &corev1.LocalObjectReference{Name: f.name},
			GitHub:    &promoterv1alpha1.GitHub{Domain: scmTarget.domain, AppID: f.appID},
		},
	}
	Expect(k8sClient.Create(ctx, scmProvider)).To(Succeed())

	for i, repo := range f.repos {
		name := f.strategyName(i)
		Expect(k8sClient.Create(ctx, &promoterv1alpha1.GitRepository{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: loadNamespace},
			Spec: promoterv1alpha1.GitRepositorySpec{
				ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{Kind: promoterv1alpha1.ScmProviderKind, Name: f.name},
				GitHub:         &promoterv1alpha1.GitHubRepo{Owner: repo.Owner, Name: repo.Name},
			},
		})).To(Succeed())
		// The ordering gate first, so the PromotionStrategy's first reconcile finds it.
		Expect(k8sClient.Create(ctx, &promoterv1alpha1.DependentsSuccessfulCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: loadNamespace},
			Spec: promoterv1alpha1.DependentsSuccessfulCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: name},
				Key:                  promoterv1alpha1.DependentsSuccessfulCommitStatusKey,
				PublishTo:            f.publishTo(),
			},
		})).To(Succeed())
		Expect(k8sClient.Create(ctx, f.promotionStrategy(i))).To(Succeed())
		for _, gate := range f.gateObjects(i) {
			Expect(k8sClient.Create(ctx, gate)).To(Succeed())
		}
	}

	f.waitFor(ctx, "every PromotionStrategy to read its branches", func(g Gomega, _ int, ps *promoterv1alpha1.PromotionStrategy) {
		g.Expect(ps.Status.Environments).To(HaveLen(len(f.envs)))
		for _, env := range ps.Status.Environments {
			g.Expect(env.Active.Hydrated.Sha).NotTo(BeEmpty())
			g.Expect(env.Proposed.Hydrated.Sha).NotTo(BeEmpty())
		}
	})
}

// promotionStrategy builds the i-th PromotionStrategy from the fixture's options.
func (f *loadFixture) promotionStrategy(i int) *promoterv1alpha1.PromotionStrategy {
	name := f.strategyName(i)
	ps := &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: loadNamespace},
		Spec: promoterv1alpha1.PromotionStrategySpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{Name: name},
			OrderCommitStatusRef: promoterv1alpha1.OrderCommitStatusRef{
				Group: promoterv1alpha1.SchemeGroupVersion.Group,
				Kind:  "DependentsSuccessfulCommitStatus",
				Name:  name,
			},
		},
	}
	for _, branch := range f.envs {
		ps.Spec.Environments = append(ps.Spec.Environments, promoterv1alpha1.Environment{
			Branch: branch, AutoMerge: f.opts.autoMerge,
		})
	}
	f.addGateKeys(ps)
	return ps
}

// seed lays a repository out the way the hydrator does: a default branch carrying hydrator.metadata, and for each
// environment a proposed branch with the environment branch pointing at it. It commits through the server's admin
// API, so none of it shows up as traffic of the provider API. It returns the head of each proposed branch.
func (f *loadFixture) seed(ctx context.Context, repo scmserver.RepoInfo) map[string]string {
	GinkgoHelper()
	first, err := f.admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
		Branch: repo.DefaultBranch, Files: map[string]string{"hydrator.metadata": `{"drySha": "n/a"}`},
		Message: "init commit dry side n/a dry sha",
	})
	Expect(err).NotTo(HaveOccurred())
	_, err = f.admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
		Branch: repo.DefaultBranch, Files: map[string]string{"hydrator.metadata": fmt.Sprintf(`{"drySha": %q}`, first.SHA)},
		Message: "second commit with real dry sha",
	})
	Expect(err).NotTo(HaveOccurred())
	heads := map[string]string{}
	for _, env := range f.envs {
		// The message names the repository so that seeds made in the same second in different repositories get
		// different SHAs: the webhook receiver finds the ChangeTransferPolicy to wake by a push's previous SHA.
		next, err := f.admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
			Branch: env + "-next", Orphan: true,
			Message: fmt.Sprintf("initial empty commit for %s-next in %s", env, repo.FullName),
		})
		Expect(err).NotTo(HaveOccurred())
		_, err = f.admin.SetBranch(ctx, repo.ID, env, scmserver.BranchRequest{SHA: next.SHA})
		Expect(err).NotTo(HaveOccurred())
		heads[env] = next.SHA
	}
	return heads
}

// hydrate pushes a dry commit to every repository's default branch and the matching hydrated commit to every
// proposed branch, as the hydrator would after a change, through the server's admin API.
func (f *loadFixture) hydrate(ctx context.Context) {
	GinkgoHelper()
	var mu sync.Mutex
	f.forEachRepo(func(i int, repo scmserver.RepoInfo) {
		stamp := time.Now().UTC().Format(time.RFC3339Nano)
		dry, err := f.admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
			Branch: repo.DefaultBranch, Files: map[string]string{"manifests-fake.yaml": fmt.Sprintf(`{"time": %q}`, stamp)},
			Message: "change the application in " + repo.FullName, AuthorName: "developer", AuthorEmail: "developer@example.com",
		})
		Expect(err).NotTo(HaveOccurred())
		metadata, err := json.MarshalIndent(promoterv1alpha1.HydratorMetadata{
			DrySha:  dry.SHA,
			Author:  "developer <developer@example.com>",
			Date:    metav1.Now(),
			Subject: "change the application",
		}, "", "\t")
		Expect(err).NotTo(HaveOccurred())
		var notes map[string]string
		if f.opts.hydratorNotes {
			notes = map[string]string{"hydrator.metadata": fmt.Sprintf(`{"drySha": %q}`, dry.SHA)}
		}
		heads := map[string]string{}
		for _, env := range f.envs {
			next, err := f.admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
				Branch: env + "-next",
				Files: map[string]string{
					"hydrator.metadata":   string(metadata),
					"manifests-fake.yaml": fmt.Sprintf(`{"time": %q, "environment": %q}`, stamp, env),
				},
				Notes:   notes,
				Message: "hydrate " + dry.SHA + " to " + env, AuthorName: "hydrator", AuthorEmail: "hydrator@example.com",
			})
			Expect(err).NotTo(HaveOccurred())
			heads[env] = next.SHA
		}
		mu.Lock()
		defer mu.Unlock()
		f.drySHAs[i] = dry.SHA
		f.proposed[i] = heads
	})
}

// waitFor waits until check passes for every PromotionStrategy of the fixture.
func (f *loadFixture) waitFor(ctx context.Context, what string, check func(g Gomega, i int, ps *promoterv1alpha1.PromotionStrategy)) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		f.syncApplications(ctx)
		for i := range f.repos {
			var ps promoterv1alpha1.PromotionStrategy
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: loadNamespace, Name: f.strategyName(i)}, &ps)).To(Succeed())
			check(g, i, &ps)
		}
	}).WithContext(ctx).WithTimeout(loadSetupTimeout).WithPolling(loadPollInterval).Should(Succeed(), "waiting for "+what)
}

// waitPromoted waits until the latest dry commit is active in every environment and its pull requests are gone.
func (f *loadFixture) waitPromoted(ctx context.Context) {
	GinkgoHelper()
	f.waitFor(ctx, "the dry commit to be active everywhere", func(g Gomega, i int, ps *promoterv1alpha1.PromotionStrategy) {
		for _, env := range ps.Status.Environments {
			g.Expect(env.Active.Dry.Sha).To(Equal(f.drySHAs[i]), env.Branch)
		}
	})
	Eventually(func(g Gomega) {
		g.Expect(f.pullRequests(ctx)).To(BeEmpty())
	}).WithContext(ctx).WithTimeout(loadSetupTimeout).WithPolling(loadPollInterval).Should(Succeed(), "waiting for merged pull requests to be cleaned up")
}

// waitHeld waits until every strategy has perStrategy open pull requests on the provider for the latest dry commit.
func (f *loadFixture) waitHeld(ctx context.Context, perStrategy int) {
	GinkgoHelper()
	f.waitFor(ctx, "the pull requests to be open", func(g Gomega, i int, ps *promoterv1alpha1.PromotionStrategy) {
		open := 0
		for _, env := range ps.Status.Environments {
			g.Expect(env.Proposed.Dry.Sha).To(Equal(f.drySHAs[i]), env.Branch)
			if env.PullRequest != nil && env.PullRequest.ID != "" && env.PullRequest.State == promoterv1alpha1.PullRequestOpen {
				open++
			}
		}
		g.Expect(open).To(Equal(perStrategy))
	})
}

// pullRequests lists the PullRequest objects of the fixture's strategies.
func (f *loadFixture) pullRequests(ctx context.Context) []promoterv1alpha1.PullRequest {
	GinkgoHelper()
	out := make([]promoterv1alpha1.PullRequest, 0, len(f.repos)*len(f.envs))
	for i := range f.repos {
		var list promoterv1alpha1.PullRequestList
		Expect(k8sClient.List(ctx, &list, client.InNamespace(loadNamespace),
			client.MatchingLabels{promoterv1alpha1.PromotionStrategyLabel: f.strategyName(i)})).To(Succeed())
		out = append(out, list.Items...)
	}
	return out
}

// openPullRequests counts the fixture's pull requests that are open on the provider.
func (f *loadFixture) openPullRequests(ctx context.Context) int {
	GinkgoHelper()
	open := 0
	for _, pr := range f.pullRequests(ctx) {
		if pr.Status.ID != "" && pr.Status.State == promoterv1alpha1.PullRequestOpen {
			open++
		}
	}
	return open
}

// reportManualGate reports the manual gate's phase for the head of every proposed branch, as an external system
// reporting on the proposed commit would.
func (f *loadFixture) reportManualGate(ctx context.Context, phase promoterv1alpha1.CommitStatusPhase) {
	GinkgoHelper()
	for i := range f.repos {
		for e, env := range f.envs {
			cs := &promoterv1alpha1.CommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("%s-%s-%d", f.strategyName(i), loadManualGate, e), Namespace: loadNamespace},
			}
			_, err := controllerutil.CreateOrUpdate(ctx, k8sClient, cs, func() error {
				cs.Labels = map[string]string{promoterv1alpha1.CommitStatusLabel: loadManualGate}
				cs.Spec.RepositoryReference = promoterv1alpha1.ObjectReference{Name: f.strategyName(i)}
				cs.Spec.Sha = f.proposed[i][env]
				cs.Spec.Name = loadManualGate
				cs.Spec.Description = "Reported by the load harness"
				cs.Spec.Phase = phase
				cs.Spec.PublishTo = f.publishTo()
				return nil
			})
			Expect(err).NotTo(HaveOccurred())
		}
	}
}

// openPullRequestOnServer returns the open pull request into an environment branch of the i-th repository.
func (f *loadFixture) openPullRequestOnServer(ctx context.Context, i int, env string) scmserver.PullRequestInfo {
	GinkgoHelper()
	prs, err := f.admin.PullRequests(ctx, f.repos[i].ID, "open")
	Expect(err).NotTo(HaveOccurred())
	for _, pr := range prs {
		if pr.Target == env {
			return pr
		}
	}
	Fail(fmt.Sprintf("no open pull request into %s in %s", env, f.repos[i].FullName))
	return scmserver.PullRequestInfo{}
}

// actByHand does to the open pull request of an environment what a person would do in GitHub's UI: "merge" or
// "close". It returns the pull request's number.
func (f *loadFixture) actByHand(ctx context.Context, action string, i int, env string) int {
	GinkgoHelper()
	pr := f.openPullRequestOnServer(ctx, i, env)
	_, err := f.admin.PullRequestAction(ctx, f.repos[i].ID, pr.Number, scmserver.PullRequestAction{Action: action})
	Expect(err).NotTo(HaveOccurred())
	return pr.Number
}

// commitToActiveBranches changes the hydrated manifests on every environment branch directly, the way a hotfix
// pushed past the promoter would, so that the next proposed change conflicts with it.
func (f *loadFixture) commitToActiveBranches(ctx context.Context) {
	GinkgoHelper()
	f.forEachRepo(func(_ int, repo scmserver.RepoInfo) {
		for _, env := range f.envs {
			_, err := f.admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
				Branch: env, Files: map[string]string{"manifests-fake.yaml": fmt.Sprintf(`{"hotfix": %q}`, env)},
				Message: "hotfix " + env + " in " + repo.FullName, AuthorName: "operator", AuthorEmail: "operator@example.com",
			})
			Expect(err).NotTo(HaveOccurred())
		}
	})
}

// deleteStrategies deletes the fixture's PromotionStrategies and what they own, and waits until the controllers have
// closed the pull requests. envtest runs no garbage collector, so the owned objects are deleted here, owners first,
// as the garbage collector would.
func (f *loadFixture) deleteStrategies(ctx context.Context) {
	GinkgoHelper()
	inNamespace := client.InNamespace(loadNamespace)
	for i := range f.repos {
		name := f.strategyName(i)
		ps := &promoterv1alpha1.PromotionStrategy{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: loadNamespace}}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, ps))).To(Succeed())
	}
	for i := range f.repos {
		owned := client.MatchingLabels{promoterv1alpha1.PromotionStrategyLabel: f.strategyName(i)}
		Expect(k8sClient.DeleteAllOf(ctx, &promoterv1alpha1.ChangeTransferPolicy{}, inNamespace, owned)).To(Succeed())
		Expect(k8sClient.DeleteAllOf(ctx, &promoterv1alpha1.PullRequest{}, inNamespace, owned)).To(Succeed())
	}
	Eventually(func(g Gomega) {
		for i := range f.repos {
			name := f.strategyName(i)
			var ps promoterv1alpha1.PromotionStrategy
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: loadNamespace, Name: name}, &ps)
			g.Expect(k8serrors.IsNotFound(err)).To(BeTrue(), "PromotionStrategy %s still exists", name)
			var ctps promoterv1alpha1.ChangeTransferPolicyList
			g.Expect(k8sClient.List(ctx, &ctps, inNamespace,
				client.MatchingLabels{promoterv1alpha1.PromotionStrategyLabel: name})).To(Succeed())
			g.Expect(ctps.Items).To(BeEmpty(), "ChangeTransferPolicies of %s still exist", name)
		}
		g.Expect(f.pullRequests(ctx)).To(BeEmpty(), "pull requests still exist")
	}).WithContext(ctx).WithTimeout(loadSetupTimeout).WithPolling(loadPollInterval).Should(Succeed())
}

// teardown deletes the fixture: the PromotionStrategies first, so the controllers close their pull requests while
// the repositories still exist, then everything else. Only the first part is checked: what is left of a fixture
// whose strategies are gone sends nothing to the SCM.
func (f *loadFixture) teardown(ctx context.Context) {
	GinkgoHelper()
	f.deleteGates(ctx)
	f.deleteStrategies(ctx)

	inNamespace := client.InNamespace(loadNamespace)
	strategies := map[string]bool{}
	for i := range f.repos {
		name := f.strategyName(i)
		strategies[name] = true
		meta := metav1.ObjectMeta{Name: name, Namespace: loadNamespace}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &promoterv1alpha1.DependentsSuccessfulCommitStatus{ObjectMeta: meta}))).To(Succeed())
		Expect(k8sClient.DeleteAllOf(ctx, &promoterv1alpha1.ChangeTransferPolicyHistory{}, inNamespace,
			client.MatchingLabels{promoterv1alpha1.PromotionStrategyLabel: name})).To(Succeed())
	}
	// Commit statuses name the repository they report on, whoever created them.
	var statuses promoterv1alpha1.CommitStatusList
	Expect(k8sClient.List(ctx, &statuses, inNamespace)).To(Succeed())
	for i := range statuses.Items {
		if strategies[statuses.Items[i].Spec.RepositoryReference.Name] {
			Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &statuses.Items[i]))).To(Succeed())
		}
	}
	for name := range strategies {
		meta := metav1.ObjectMeta{Name: name, Namespace: loadNamespace}
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &promoterv1alpha1.GitRepository{ObjectMeta: meta}))).To(Succeed())
	}
	meta := metav1.ObjectMeta{Name: f.name, Namespace: loadNamespace}
	Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &promoterv1alpha1.ScmProvider{ObjectMeta: meta}))).To(Succeed())
	Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, &corev1.Secret{ObjectMeta: meta}))).To(Succeed())
	// Commit statuses are published on deletion paths too; let that finish before the repositories go.
	loadSleep(ctx, 2*time.Second)
	for _, repo := range f.repos {
		_ = f.admin.DeleteRepo(ctx, repo.ID)
	}
}
