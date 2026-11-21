// Command loadtest measures the controller's CPU and memory as the number of
// PromotionStrategies grows.
//
// It starts a throwaway kube-apiserver/etcd (envtest), an in-process git server,
// and the real controller binary as a separate process with pprof enabled. It
// then creates PromotionStrategies in steps (for example 100, 250, 500, 1000),
// each with its own git repository, environments, and gates. At every step it
// waits for the controller to reconcile everything, lets it go idle, and records:
//
//   - RSS, Go heap, goroutines, and idle CPU from the controller process
//   - heap (inuse/alloc) and CPU pprof profiles while idle
//   - optionally, one or more "promotion waves": a new dry commit is hydrated
//     into every repository at once and the harness measures how long it takes
//     to promote through all environments, plus the CPU and peak memory of that
//     burst, with heap and CPU profiles taken during it
//
// See hack/loadtest/README.md for usage.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/sosedoff/gitkit"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

const (
	namespace = "default"
	// The fake SCM provider compiled into the controller expects the git server on 5000+GinkgoParallelProcess()
	// (1 outside Ginkgo) and sends merge webhooks to 3333+1. The controller's receiver listens on 3333.
	gitServerAddr        = "127.0.0.1:5001"
	fakeWebhookAddr      = "127.0.0.1:3334"
	controllerWebhookURL = "http://127.0.0.1:3333/"
	pprofAddr            = "127.0.0.1:6060"
	metricsAddr          = "127.0.0.1:9080"
	probeAddr            = "127.0.0.1:9081"
	repoOwner            = "loadtest"
	labelKey             = "loadtest.promoter.argoproj.io/run"
	controllerConfigName = "promoter-controller-configuration"
	hydratorNotesRef     = "refs/notes/hydrator.metadata"
)

type config struct {
	levels          []int
	envs            int
	gates           map[string]bool
	waves           int
	settle          time.Duration
	idleWindow      time.Duration
	readyTimeout    time.Duration
	waveTimeout     time.Duration
	waveProfile     time.Duration
	outDir          string
	controllerBin   string
	qps             int
	concurrency     int
	createWorkers   int
	keep            bool
	repoRoot        string
	kubebuilderPath string
	timedDuration   time.Duration
	stallTimeout    time.Duration
}

func main() {
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	cfg := parseFlags()
	if err := run(cfg); err != nil {
		log.Fatalf("loadtest failed: %v", err)
	}
}

func parseFlags() config {
	var (
		levels string
		gates  string
		c      config
	)
	flag.StringVar(&levels, "levels", "100,250,500,1000", "Comma-separated cumulative PromotionStrategy counts to measure at.")
	flag.IntVar(&c.envs, "envs", 3, "Environments per PromotionStrategy (one ChangeTransferPolicy each).")
	flag.StringVar(&gates, "gates", "timed,git", "Extra gates per PromotionStrategy, comma-separated: timed, git, or none. "+
		"A DependentsSuccessfulCommitStatus is always created because orderCommitStatusRef is required.")
	flag.IntVar(&c.waves, "waves", 1, "Promotion waves to run at each level (0 = idle only).")
	flag.DurationVar(&c.settle, "settle", 60*time.Second, "Wait after everything is reconciled before measuring idle.")
	flag.DurationVar(&c.idleWindow, "idle-window", 60*time.Second, "Window for idle CPU measurement and CPU profile.")
	flag.DurationVar(&c.readyTimeout, "ready-timeout", 30*time.Minute, "Max time for a level to finish its initial reconcile.")
	flag.DurationVar(&c.waveTimeout, "wave-timeout", 30*time.Minute, "Max time for a promotion wave to reach the last environment.")
	flag.DurationVar(&c.waveProfile, "wave-profile", 60*time.Second, "Length of the CPU profile taken at the start of each wave.")
	flag.StringVar(&c.outDir, "out", "", "Output directory (default: ./loadtest-out-<timestamp>).")
	flag.StringVar(&c.controllerBin, "controller-bin", "", "Prebuilt controller binary (default: build ./cmd).")
	flag.IntVar(&c.qps, "qps", 0, "Override every work queue bucket qps (and bucket size = 10x) in ControllerConfiguration. 0 keeps the shipped values.")
	flag.IntVar(&c.concurrency, "max-concurrent-reconciles", 0, "Override every maxConcurrentReconciles. 0 keeps the shipped values.")
	flag.IntVar(&c.createWorkers, "create-workers", 16, "Parallel workers used by the harness to create repos and objects.")
	flag.DurationVar(&c.timedDuration, "timed-duration", 5*time.Second, "Soak duration for the TimedCommitStatus gate.")
	flag.DurationVar(&c.stallTimeout, "stall-timeout", 10*time.Minute, "Give up on a level or wave if no environment is promoted for this long.")
	flag.BoolVar(&c.keep, "keep", false, "Leave the controller and apiserver running at the end until Ctrl-C.")
	flag.Parse()

	for _, s := range strings.Split(levels, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil || n <= 0 {
			log.Fatalf("bad -levels entry %q", s)
		}
		if len(c.levels) > 0 && n <= c.levels[len(c.levels)-1] {
			log.Fatalf("-levels must be increasing")
		}
		c.levels = append(c.levels, n)
	}
	c.gates = map[string]bool{}
	for _, g := range strings.Split(gates, ",") {
		g = strings.TrimSpace(g)
		switch g {
		case "", "none":
		case "timed", "git":
			c.gates[g] = true
		default:
			log.Fatalf("unknown gate %q", g)
		}
	}
	if c.envs < 1 {
		log.Fatal("-envs must be >= 1")
	}

	wd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	c.repoRoot = findRepoRoot(wd)
	if c.outDir == "" {
		c.outDir = filepath.Join(wd, "loadtest-out-"+time.Now().Format("20060102-150405"))
	}
	c.kubebuilderPath = os.Getenv("KUBEBUILDER_ASSETS")
	return c
}

func findRepoRoot(dir string) string {
	for d := dir; d != "/"; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			if _, err := os.Stat(filepath.Join(d, "config", "crd", "bases")); err == nil {
				return d
			}
		}
	}
	log.Fatal("run from inside the gitops-promoter repository")
	return ""
}

// ---------------------------------------------------------------------------
// harness state

type harness struct {
	cfg        config
	k8s        client.Client
	gitDir     string
	ctrlTmp    string
	ctrlCmd    *exec.Cmd
	ctrlPID    int
	ctrlDone   chan struct{}
	repos      []*repoState // index i == PromotionStrategy i
	branches   []string     // active branches
	httpClient *http.Client
	wave       int
	results    []levelResult
}

type repoState struct {
	name     string
	path     string
	drySha   string            // latest dry commit on main
	proposed map[string]string // proposed branch -> hydrated sha
}

func run(cfg config) error {
	if err := os.MkdirAll(cfg.outDir, 0o755); err != nil {
		return err
	}
	log.Printf("output directory: %s", cfg.outDir)

	h := &harness{cfg: cfg, httpClient: &http.Client{Timeout: 10 * time.Minute}}
	for i := range cfg.envs {
		h.branches = append(h.branches, envBranch(i, cfg.envs))
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 1. API server
	env, restCfg, err := h.startEnvtest()
	if err != nil {
		return err
	}
	defer func() {
		log.Print("stopping envtest")
		_ = env.Stop()
	}()

	// 2. Git server and webhook forwarder
	h.gitDir = filepath.Join(cfg.outDir, "git-server")
	h.ctrlTmp = filepath.Join(cfg.outDir, "controller-tmp")
	for _, d := range []string{h.gitDir, h.ctrlTmp} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	stopGit, err := startGitServer(h.gitDir)
	if err != nil {
		return err
	}
	defer stopGit()
	stopFwd, err := startWebhookForwarder()
	if err != nil {
		return err
	}
	defer stopFwd()

	// 3. ControllerConfiguration
	if err := h.createControllerConfiguration(ctx); err != nil {
		return err
	}

	// 4. Controller process
	kubeconfig := filepath.Join(cfg.outDir, "kubeconfig")
	if err := writeKubeconfig(restCfg, kubeconfig); err != nil {
		return err
	}
	if err := h.startController(kubeconfig); err != nil {
		return err
	}
	defer h.stopController()

	baseline, err := h.sample()
	if err != nil {
		return err
	}
	log.Printf("baseline (0 PromotionStrategies): rss=%s heap=%s goroutines=%.0f",
		mib(baseline.rssBytes), mib(baseline.heapInuse), baseline.goroutines)
	h.results = append(h.results, levelResult{Level: 0, Idle: baseline.snapshot()})
	if err := h.captureProfiles(filepath.Join(cfg.outDir, "L0000"), "idle", 10*time.Second); err != nil {
		log.Printf("WARN baseline profiles: %v", err)
	}

	// 5. Levels
	for _, level := range cfg.levels {
		if err := h.runLevel(ctx, level); err != nil {
			h.writeResults()
			return fmt.Errorf("level %d: %w", level, err)
		}
		h.writeResults()
	}

	log.Printf("done. summary: %s", filepath.Join(cfg.outDir, "results.md"))
	if cfg.keep {
		log.Printf("-keep set: controller pprof at http://%s/debug/pprof/, kubeconfig %s. Ctrl-C to exit.", pprofAddr, kubeconfig)
		waitForSignal()
	}
	return nil
}

func envBranch(i, n int) string {
	if n == 3 {
		return []string{"environment/development", "environment/staging", "environment/production"}[i]
	}
	return fmt.Sprintf("environment/env-%02d", i)
}

// ---------------------------------------------------------------------------
// infrastructure

func (h *harness) startEnvtest() (*envtest.Environment, *rest.Config, error) {
	assets := h.cfg.kubebuilderPath
	if assets == "" {
		matches, _ := filepath.Glob(filepath.Join(h.cfg.repoRoot, "bin", "k8s", "*-linux-*"))
		if len(matches) == 0 {
			return nil, nil, errors.New("no envtest binaries: run `make envtest && bin/setup-envtest-* use <ver> --bin-dir bin` or set KUBEBUILDER_ASSETS")
		}
		sort.Strings(matches)
		assets = matches[len(matches)-1]
	}
	log.Printf("starting envtest with assets %s", assets)
	env := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join(h.cfg.repoRoot, "config", "crd", "bases"),
			filepath.Join(h.cfg.repoRoot, "test", "external_crds"),
		},
		ErrorIfCRDPathMissing: true,
		BinaryAssetsDirectory: assets,
	}
	// The default in-flight limits are fine for a few thousand objects, but raise them so the
	// apiserver is not the bottleneck being measured.
	apiServer := env.ControlPlane.GetAPIServer()
	apiServer.Configure().Set("max-requests-inflight", "2000")
	apiServer.Configure().Set("max-mutating-requests-inflight", "1000")

	restCfg, err := env.Start()
	if err != nil {
		return nil, nil, fmt.Errorf("start envtest: %w", err)
	}
	harnessCfg := rest.CopyConfig(restCfg)
	harnessCfg.QPS = 500
	harnessCfg.Burst = 1000
	h.k8s, err = client.New(harnessCfg, client.Options{Scheme: utils.GetScheme()})
	if err != nil {
		return nil, nil, err
	}
	return env, restCfg, nil
}

func writeKubeconfig(cfg *rest.Config, path string) error {
	name := "loadtest"
	apiCfg := clientcmdapi.Config{
		Clusters:       map[string]*clientcmdapi.Cluster{name: {Server: cfg.Host, CertificateAuthorityData: cfg.CAData}},
		AuthInfos:      map[string]*clientcmdapi.AuthInfo{name: {ClientCertificateData: cfg.CertData, ClientKeyData: cfg.KeyData, Token: cfg.BearerToken}},
		Contexts:       map[string]*clientcmdapi.Context{name: {Cluster: name, AuthInfo: name, Namespace: namespace}},
		CurrentContext: name,
	}
	data, err := clientcmd.Write(apiCfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func startGitServer(dir string) (func(), error) {
	service := gitkit.New(gitkit.Config{Dir: dir, AutoCreate: true})
	if err := service.Setup(); err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", gitServerAddr)
	if err != nil {
		return nil, fmt.Errorf("git server listen %s: %w", gitServerAddr, err)
	}
	srv := &http.Server{Handler: service, ReadHeaderTimeout: 30 * time.Second}
	// gitkit logs every request through the standard logger; keep ours on stderr and drop its noise.
	go func() { _ = srv.Serve(ln) }()
	log.Printf("git server on %s serving %s", gitServerAddr, dir)
	return func() { _ = srv.Close() }, nil
}

// startWebhookForwarder relays the fake SCM's merge webhooks (sent to 3334) to the controller (3333).
func startWebhookForwarder() (func(), error) {
	target, _ := url.Parse(controllerWebhookURL)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorLog = log.New(io.Discard, "", 0)
	ln, err := net.Listen("tcp", fakeWebhookAddr)
	if err != nil {
		return nil, fmt.Errorf("webhook forwarder listen: %w", err)
	}
	srv := &http.Server{Handler: proxy, ReadHeaderTimeout: 30 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return func() { _ = srv.Close() }, nil
}

func (h *harness) createControllerConfiguration(ctx context.Context) error {
	data, err := os.ReadFile(filepath.Join(h.cfg.repoRoot, "config", "config", "controllerconfiguration.yaml"))
	if err != nil {
		return err
	}
	obj := map[string]any{}
	if err := yaml.Unmarshal(data, &obj); err != nil {
		return err
	}
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if h.cfg.qps > 0 {
				if _, ok := v["qps"]; ok {
					v["qps"] = h.cfg.qps
					v["bucket"] = h.cfg.qps * 10
				}
			}
			if h.cfg.concurrency > 0 {
				if _, ok := v["maxConcurrentReconciles"]; ok {
					v["maxConcurrentReconciles"] = h.cfg.concurrency
				}
			}
			for _, c := range v {
				walk(c)
			}
		case []any:
			for _, c := range v {
				walk(c)
			}
		}
	}
	walk(obj)
	u := &unstructured.Unstructured{Object: obj}
	u.SetName(controllerConfigName)
	u.SetNamespace(namespace)
	out, _ := yaml.Marshal(obj)
	_ = os.WriteFile(filepath.Join(h.cfg.outDir, "controllerconfiguration.yaml"), out, 0o644)
	return h.k8s.Create(ctx, u)
}

func (h *harness) startController(kubeconfig string) error {
	bin := h.cfg.controllerBin
	if bin == "" {
		bin = filepath.Join(h.cfg.outDir, "promoter")
		log.Print("building controller binary")
		build := exec.Command("go", "build", "-o", bin, "./cmd")
		build.Dir = h.cfg.repoRoot
		build.Stdout, build.Stderr = os.Stdout, os.Stderr
		if err := build.Run(); err != nil {
			return fmt.Errorf("build controller: %w", err)
		}
	}
	logFile, err := os.Create(filepath.Join(h.cfg.outDir, "controller.log"))
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "controller",
		"--kubeconfig="+kubeconfig,
		"--pprof-bind-address="+pprofAddr,
		"--metrics-bind-address="+metricsAddr,
		"--health-probe-bind-address="+probeAddr,
		"--apiserver-cert-rotation=false",
	)
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Join(h.cfg.repoRoot, "hack", "git")+":"+os.Getenv("PATH"),
		"TMPDIR="+h.ctrlTmp,
		"KUBECONFIG="+kubeconfig,
	)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	h.ctrlCmd = cmd
	h.ctrlPID = cmd.Process.Pid
	h.ctrlDone = make(chan struct{})
	go func() { _ = cmd.Wait(); close(h.ctrlDone) }()
	log.Printf("controller pid %d, log %s", h.ctrlPID, logFile.Name())

	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://" + probeAddr + "/readyz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				// Give informers a moment to finish their initial list.
				time.Sleep(5 * time.Second)
				return nil
			}
		}
		if h.controllerExited() {
			return errors.New("controller exited during startup; see controller.log")
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("controller did not become ready; see controller.log")
}

func (h *harness) stopController() {
	if h.ctrlCmd == nil || h.ctrlCmd.Process == nil {
		return
	}
	_ = syscall.Kill(-h.ctrlPID, syscall.SIGTERM)
	select {
	case <-h.ctrlDone:
	case <-time.After(20 * time.Second):
		_ = syscall.Kill(-h.ctrlPID, syscall.SIGKILL)
	}
}

func (h *harness) controllerExited() bool {
	select {
	case <-h.ctrlDone:
		return true
	default:
		return false
	}
}

func waitForSignal() {
	ch := make(chan os.Signal, 1)
	signalNotify(ch)
	<-ch
}

// ---------------------------------------------------------------------------
// git repositories

var gitEnv = []string{
	"GIT_AUTHOR_NAME=loadtest", "GIT_AUTHOR_EMAIL=loadtest@example.com",
	"GIT_COMMITTER_NAME=loadtest", "GIT_COMMITTER_EMAIL=loadtest@example.com",
	"GIT_CONFIG_NOSYSTEM=1", "HOME=/nonexistent",
}

func runGit(dir string, stdin []byte, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), gitEnv...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

func proposedBranch(b string) string { return b + "-next" }

type fiWriter struct{ bytes.Buffer }

func (w *fiWriter) data(s string) { fmt.Fprintf(w, "data %d\n%s\n", len(s), s) }

func (w *fiWriter) commit(ref, mark, msg, from string, ts int64, files map[string]string) {
	fmt.Fprintf(w, "commit %s\nmark %s\ncommitter loadtest <loadtest@example.com> %d +0000\n", ref, mark, ts)
	w.data(msg)
	if from != "" {
		fmt.Fprintf(w, "from %s\n", from)
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(w, "M 100644 inline %s\n", n)
		w.data(files[n])
	}
	w.WriteString("\n")
}

func readMarks(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		parts := strings.Fields(line)
		if len(parts) == 2 {
			out[parts[0]] = parts[1]
		}
	}
	return out, nil
}

// fastImport runs one git fast-import stream against the bare repo and returns mark -> sha.
func fastImport(repoPath string, stream []byte) (map[string]string, error) {
	marks := repoPath + ".marks"
	defer os.Remove(marks)
	if _, err := runGit(repoPath, stream, "fast-import", "--quiet", "--force", "--export-marks="+marks); err != nil {
		return nil, err
	}
	return readMarks(marks)
}

// hydrate writes a new dry commit to main and a hydrated commit (plus hydrator note) to every proposed
// branch, in two fast-import passes so the hydrated metadata can carry the dry SHA. When initial is
// true it also points every active branch at its proposed commit, so the repo starts fully promoted.
func (h *harness) hydrate(r *repoState, wave int, initial bool) error {
	ts := time.Now().Unix()
	var dry fiWriter
	from := ""
	if !initial {
		from = "refs/heads/main^0"
	}
	dry.commit("refs/heads/main", ":1", fmt.Sprintf("dry commit wave %d for %s", wave, r.name), from, ts,
		map[string]string{"manifests-fake.yaml": fmt.Sprintf("{\"repo\": %q, \"wave\": %d}\n", r.name, wave)})
	marks, err := fastImport(r.path, dry.Bytes())
	if err != nil {
		return err
	}
	drySha := marks[":1"]

	var hyd fiWriter
	for i, active := range h.branches {
		prop := proposedBranch(active)
		from := ""
		if !initial {
			from = "refs/heads/" + prop + "^0"
		}
		meta, _ := json.MarshalIndent(map[string]any{
			"drySha":  drySha,
			"author":  "loadtest <loadtest@example.com>",
			"date":    time.Unix(ts, 0).UTC().Format(time.RFC3339),
			"subject": fmt.Sprintf("dry commit wave %d for %s", wave, r.name),
		}, "", "\t")
		hyd.commit("refs/heads/"+prop, fmt.Sprintf(":%d", i+1),
			fmt.Sprintf("hydrate %s wave %d for %s", active, wave, r.name), from, ts,
			map[string]string{
				"hydrator.metadata":   string(meta),
				"manifests-fake.yaml": fmt.Sprintf("{\"repo\": %q, \"env\": %q, \"wave\": %d}\n", r.name, active, wave),
			})
	}
	// Notes for every hydrated commit, in one notes commit.
	fmt.Fprintf(&hyd, "commit %s\ncommitter loadtest <loadtest@example.com> %d +0000\n", hydratorNotesRef, ts)
	hyd.data("hydrator notes")
	if !initial {
		fmt.Fprintf(&hyd, "from %s^0\n", hydratorNotesRef)
	}
	for i := range h.branches {
		fmt.Fprintf(&hyd, "N inline :%d\n", i+1)
		hyd.data(fmt.Sprintf("{\"drySha\": %q}", drySha))
	}
	hyd.WriteString("\n")
	if initial {
		for i, active := range h.branches {
			fmt.Fprintf(&hyd, "reset refs/heads/%s\nfrom :%d\n\n", active, i+1)
		}
	}
	marks, err = fastImport(r.path, hyd.Bytes())
	if err != nil {
		return err
	}
	r.drySha = drySha
	if r.proposed == nil {
		r.proposed = map[string]string{}
	}
	for i, active := range h.branches {
		r.proposed[proposedBranch(active)] = marks[fmt.Sprintf(":%d", i+1)]
	}
	return nil
}

func (h *harness) createRepo(idx int) (*repoState, error) {
	name := fmt.Sprintf("ps-%05d", idx)
	p := filepath.Join(h.gitDir, repoOwner, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		return nil, err
	}
	if _, err := runGit(p, nil, "init", "--bare", "--quiet", "--initial-branch=main"); err != nil {
		return nil, err
	}
	// gitkit serves receive-pack; allow the controller to push merges and notes.
	if _, err := runGit(p, nil, "config", "http.receivepack", "true"); err != nil {
		return nil, err
	}
	r := &repoState{name: name, path: p}
	if err := h.hydrate(r, h.wave, true); err != nil {
		return nil, err
	}
	return r, nil
}

// ---------------------------------------------------------------------------
// Kubernetes objects

func (h *harness) createObjects(ctx context.Context, r *repoState) error {
	labels := map[string]string{labelKey: "true"}
	om := func(name string) metav1.ObjectMeta {
		return metav1.ObjectMeta{Name: name, Namespace: namespace, Labels: labels}
	}
	n := r.name
	objs := []client.Object{
		&corev1.Secret{ObjectMeta: om(n)},
		&promoterv1alpha1.ScmProvider{ObjectMeta: om(n), Spec: promoterv1alpha1.ScmProviderSpec{
			SecretRef: &corev1.LocalObjectReference{Name: n},
			Fake:      &promoterv1alpha1.Fake{},
		}},
		&promoterv1alpha1.GitRepository{ObjectMeta: om(n), Spec: promoterv1alpha1.GitRepositorySpec{
			ScmProviderRef: promoterv1alpha1.ScmProviderObjectReference{Kind: promoterv1alpha1.ScmProviderKind, Name: n},
			Fake:           &promoterv1alpha1.FakeRepo{Owner: repoOwner, Name: n},
		}},
	}

	ps := &promoterv1alpha1.PromotionStrategy{ObjectMeta: om(n), Spec: promoterv1alpha1.PromotionStrategySpec{
		RepositoryReference: promoterv1alpha1.ObjectReference{Name: n},
		OrderCommitStatusRef: promoterv1alpha1.OrderCommitStatusRef{
			Group: promoterv1alpha1.SchemeGroupVersion.Group,
			Kind:  "DependentsSuccessfulCommitStatus",
			Name:  n,
		},
	}}
	for _, b := range h.branches {
		ps.Spec.Environments = append(ps.Spec.Environments, promoterv1alpha1.Environment{Branch: b})
	}
	if h.cfg.gates["timed"] {
		ps.Spec.ActiveCommitStatuses = append(ps.Spec.ActiveCommitStatuses, promoterv1alpha1.CommitStatusSelector{Key: "timer"})
		tcs := &promoterv1alpha1.TimedCommitStatus{ObjectMeta: om(n), Spec: promoterv1alpha1.TimedCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: n},
			Key:                  "timer",
		}}
		for _, b := range h.branches {
			tcs.Spec.Environments = append(tcs.Spec.Environments, promoterv1alpha1.TimedCommitStatusEnvironments{
				Branch: b, Duration: metav1.Duration{Duration: h.cfg.timedDuration},
			})
		}
		objs = append(objs, tcs)
	}
	if h.cfg.gates["git"] {
		ps.Spec.ProposedCommitStatuses = append(ps.Spec.ProposedCommitStatuses, promoterv1alpha1.CommitStatusSelector{Key: "git-check"})
		objs = append(objs, &promoterv1alpha1.GitCommitStatus{ObjectMeta: om(n), Spec: promoterv1alpha1.GitCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: n},
			Key:                  "git-check",
			Target:               "proposed",
			Expression:           `!(Commit.Subject startsWith "Revert")`,
		}})
	}
	// Create the PromotionStrategy before its gates so gate controllers do not log "not found" errors.
	gates := objs[3:]
	objs = append(objs[:3:3], ps, &promoterv1alpha1.DependentsSuccessfulCommitStatus{ObjectMeta: om(n), Spec: promoterv1alpha1.DependentsSuccessfulCommitStatusSpec{
		PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: n},
		Key:                  promoterv1alpha1.DependentsSuccessfulCommitStatusKey,
	}})
	objs = append(objs, gates...)
	for _, o := range objs {
		if err := h.k8s.Create(ctx, o); err != nil {
			return fmt.Errorf("create %T %s: %w", o, o.GetName(), err)
		}
	}
	return nil
}

// countPromoted returns how many of the first n PromotionStrategies have every environment's active
// dry SHA equal to the repo's latest dry SHA, and how many individual environments have.
func (h *harness) countPromoted(ctx context.Context, n int) (int, int, error) {
	list := &promoterv1alpha1.PromotionStrategyList{}
	if err := h.k8s.List(ctx, list, client.InNamespace(namespace), client.MatchingLabels{labelKey: "true"}); err != nil {
		return 0, 0, err
	}
	want := map[string]string{}
	for _, r := range h.repos[:n] {
		want[r.name] = r.drySha
	}
	done, envs := 0, 0
	for i := range list.Items {
		ps := &list.Items[i]
		dry, ok := want[ps.Name]
		if !ok {
			continue
		}
		n := 0
		for _, e := range ps.Status.Environments {
			if e.Active.Dry.Sha == dry && e.Proposed.Dry.Sha == dry {
				n++
			}
		}
		envs += n
		if n == len(h.branches) {
			done++
		}
	}
	return done, envs, nil
}

func (h *harness) objectCounts(ctx context.Context) map[string]int {
	out := map[string]int{}
	lists := map[string]client.ObjectList{
		"PromotionStrategy":                &promoterv1alpha1.PromotionStrategyList{},
		"ChangeTransferPolicy":             &promoterv1alpha1.ChangeTransferPolicyList{},
		"PullRequest":                      &promoterv1alpha1.PullRequestList{},
		"CommitStatus":                     &promoterv1alpha1.CommitStatusList{},
		"GitRepository":                    &promoterv1alpha1.GitRepositoryList{},
		"TimedCommitStatus":                &promoterv1alpha1.TimedCommitStatusList{},
		"GitCommitStatus":                  &promoterv1alpha1.GitCommitStatusList{},
		"DependentsSuccessfulCommitStatus": &promoterv1alpha1.DependentsSuccessfulCommitStatusList{},
		"ChangeTransferPolicyHistory":      &promoterv1alpha1.ChangeTransferPolicyHistoryList{},
	}
	for k, l := range lists {
		if err := h.k8s.List(ctx, l, client.InNamespace(namespace)); err == nil {
			out[k] = metaLen(l)
		}
	}
	return out
}

func metaLen(l client.ObjectList) int {
	data, _ := json.Marshal(l)
	var v struct {
		Items []json.RawMessage `json:"items"`
	}
	_ = json.Unmarshal(data, &v)
	return len(v.Items)
}

// sendPushWebhooks notifies the controller about pushes to every proposed branch, the way an SCM would.
func (h *harness) sendPushWebhook(before, branch string) {
	payload, _ := json.Marshal(map[string]any{
		"before": before, "ref": "refs/heads/" + branch,
		"pusher": map[string]any{"name": "loadtest", "email": "loadtest@example.com"},
	})
	req, _ := http.NewRequest(http.MethodPost, controllerWebhookURL, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Github-Event", "push")
	req.Header.Set("X-Github-Delivery", fmt.Sprintf("lt-%d", time.Now().UnixNano()))
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err == nil {
		_ = resp.Body.Close()
	}
}

// ---------------------------------------------------------------------------
// measurement

type sample struct {
	at          time.Time
	cpuSelf     float64 // seconds, controller process (utime+stime)
	cpuChildren float64 // seconds, reaped children (git subprocesses)
	rssBytes    float64
	hwmBytes    float64
	heapInuse   float64
	heapAlloc   float64
	sys         float64
	goroutines  float64
	metrics     map[string]float64
}

type snapshot struct {
	RSSMiB        float64            `json:"rss_mib"`
	PeakRSSMiB    float64            `json:"peak_rss_mib"`
	HeapInuseMiB  float64            `json:"heap_inuse_mib"`
	HeapAllocMiB  float64            `json:"heap_alloc_mib"`
	GoSysMiB      float64            `json:"go_sys_mib"`
	Goroutines    float64            `json:"goroutines"`
	CPUCores      float64            `json:"cpu_cores,omitempty"`
	GitCPUCores   float64            `json:"git_cpu_cores,omitempty"`
	WindowSeconds float64            `json:"window_seconds,omitempty"`
	ReconcilesPS  map[string]float64 `json:"reconciles_per_sec,omitempty"`
	ReconcileCPU  map[string]float64 `json:"reconcile_seconds_per_sec,omitempty"`
	CloneDirMiB   float64            `json:"clone_dir_mib,omitempty"`
}

func (s sample) snapshot() snapshot {
	return snapshot{
		RSSMiB: s.rssBytes / (1 << 20), PeakRSSMiB: s.hwmBytes / (1 << 20),
		HeapInuseMiB: s.heapInuse / (1 << 20), HeapAllocMiB: s.heapAlloc / (1 << 20),
		GoSysMiB: s.sys / (1 << 20), Goroutines: s.goroutines,
	}
}

func window(a, b sample) snapshot {
	s := b.snapshot()
	dt := b.at.Sub(a.at).Seconds()
	s.WindowSeconds = dt
	s.CPUCores = (b.cpuSelf - a.cpuSelf) / dt
	s.GitCPUCores = (b.cpuChildren - a.cpuChildren) / dt
	s.ReconcilesPS = map[string]float64{}
	s.ReconcileCPU = map[string]float64{}
	for k, v := range b.metrics {
		if c, ok := strings.CutPrefix(k, "reconcile_total/"); ok {
			if d := v - a.metrics[k]; d > 0 {
				s.ReconcilesPS[c] = round2(d / dt)
			}
		}
		if c, ok := strings.CutPrefix(k, "reconcile_time/"); ok {
			if d := v - a.metrics[k]; d > 0 {
				s.ReconcileCPU[c] = round2(d / dt)
			}
		}
	}
	return s
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }

func (h *harness) sample() (sample, error) {
	s := sample{at: time.Now()}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", h.ctrlPID))
	if err != nil {
		return s, err
	}
	// Fields after the ")" that closes comm: index 0 is state (field 3).
	rest := string(stat[bytes.LastIndexByte(stat, ')')+2:])
	f := strings.Fields(rest)
	tick := 100.0 // CLK_TCK on Linux
	ut, _ := strconv.ParseFloat(f[11], 64)
	st, _ := strconv.ParseFloat(f[12], 64)
	cut, _ := strconv.ParseFloat(f[13], 64)
	cst, _ := strconv.ParseFloat(f[14], 64)
	s.cpuSelf = (ut + st) / tick
	s.cpuChildren = (cut + cst) / tick

	status, _ := os.ReadFile(fmt.Sprintf("/proc/%d/status", h.ctrlPID))
	for _, line := range strings.Split(string(status), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		kb, _ := strconv.ParseFloat(fields[1], 64)
		switch fields[0] {
		case "VmRSS:":
			s.rssBytes = kb * 1024
		case "VmHWM:":
			s.hwmBytes = kb * 1024
		}
	}

	s.metrics, err = h.scrapeMetrics()
	if err != nil {
		return s, err
	}
	s.heapInuse = s.metrics["go_memstats_heap_inuse_bytes"]
	s.heapAlloc = s.metrics["go_memstats_heap_alloc_bytes"]
	s.sys = s.metrics["go_memstats_sys_bytes"]
	s.goroutines = s.metrics["go_goroutines"]
	return s, nil
}

func (h *harness) scrapeMetrics() (map[string]float64, error) {
	resp, err := h.httpClient.Get("http://" + metricsAddr + "/metrics")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	out := map[string]float64{}
	for _, line := range strings.Split(string(body), "\n") {
		if line == "" || line[0] == '#' {
			continue
		}
		sp := strings.LastIndexByte(line, ' ')
		if sp < 0 {
			continue
		}
		v, err := strconv.ParseFloat(line[sp+1:], 64)
		if err != nil {
			continue
		}
		key := line[:sp]
		name, labels := key, ""
		if i := strings.IndexByte(key, '{'); i >= 0 {
			name, labels = key[:i], key[i:]
		}
		switch name {
		case "go_memstats_heap_inuse_bytes", "go_memstats_heap_alloc_bytes", "go_memstats_sys_bytes", "go_goroutines":
			out[name] = v
		case "controller_runtime_reconcile_total":
			out["reconcile_total/"+labelValue(labels, "controller")] += v
		case "controller_runtime_reconcile_time_seconds_sum":
			out["reconcile_time/"+labelValue(labels, "controller")] += v
		case "workqueue_depth":
			out["queue_depth/"+labelValue(labels, "controller")] += v
		}
	}
	return out, nil
}

func labelValue(labels, key string) string {
	i := strings.Index(labels, key+"=\"")
	if i < 0 {
		return "unknown"
	}
	rest := labels[i+len(key)+2:]
	return rest[:strings.IndexByte(rest, '"')]
}

func (h *harness) fetchProfile(path, dest string) error {
	resp, err := h.httpClient.Get("http://" + pprofAddr + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", path, resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// captureProfiles takes a CPU profile of length cpu (concurrently with whatever is running), then
// heap and goroutine profiles.
func (h *harness) captureProfiles(dir, tag string, cpu time.Duration) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if cpu > 0 {
		if err := h.fetchProfile(fmt.Sprintf("/debug/pprof/profile?seconds=%d", int(cpu.Seconds())), filepath.Join(dir, "cpu-"+tag+".pb.gz")); err != nil {
			return err
		}
	}
	if err := h.fetchProfile("/debug/pprof/heap?gc=1", filepath.Join(dir, "heap-"+tag+".pb.gz")); err != nil {
		return err
	}
	return h.fetchProfile("/debug/pprof/goroutine?debug=1", filepath.Join(dir, "goroutine-"+tag+".txt"))
}

func dirSizeMiB(dir string) float64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return float64(total) / (1 << 20)
}

func mib(b float64) string { return fmt.Sprintf("%.0fMiB", b/(1<<20)) }

// ---------------------------------------------------------------------------
// levels

type levelResult struct {
	Level        int            `json:"level"`
	CreateSecs   float64        `json:"create_seconds,omitempty"`
	ReadySecs    float64        `json:"ready_seconds,omitempty"`
	Ramp         *snapshot      `json:"ramp,omitempty"`
	Idle         snapshot       `json:"idle"`
	Waves        []waveResult   `json:"waves,omitempty"`
	ObjectCounts map[string]int `json:"object_counts,omitempty"`
}

type waveResult struct {
	Wave        int      `json:"wave"`
	Seconds     float64  `json:"seconds"`
	Completed   int      `json:"completed"`
	Burst       snapshot `json:"burst"`
	PeakRSSMiB  float64  `json:"peak_rss_mib"`
	PeakHeapMiB float64  `json:"peak_heap_inuse_mib"`
	// PeakQueueDepth is the largest work queue depth seen per controller during the wave.
	PeakQueueDepth map[string]float64 `json:"peak_queue_depth"`
}

func (h *harness) runLevel(ctx context.Context, level int) error {
	dir := filepath.Join(h.cfg.outDir, fmt.Sprintf("L%04d", level))
	start := len(h.repos)
	log.Printf("=== level %d: creating PromotionStrategies %d..%d", level, start, level-1)

	before, err := h.sample()
	if err != nil {
		return err
	}
	t0 := time.Now()
	newRepos := make([]*repoState, level-start)
	var created atomic.Int64
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(h.cfg.createWorkers)
	for i := start; i < level; i++ {
		g.Go(func() error {
			r, err := h.createRepo(i)
			if err != nil {
				return err
			}
			newRepos[i-start] = r
			if err := h.createObjects(gctx, r); err != nil {
				return err
			}
			if c := created.Add(1); c%250 == 0 {
				log.Printf("  created %d/%d", c, level-start)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	h.repos = append(h.repos, newRepos...)
	res := levelResult{Level: level, CreateSecs: time.Since(t0).Seconds()}
	log.Printf("  created %d in %.1fs; waiting for initial reconcile", level-start, res.CreateSecs)

	done, err := h.waitPromoted(ctx, level, h.cfg.readyTimeout, nil)
	if err != nil {
		return err
	}
	after, _ := h.sample()
	ramp := window(before, after)
	res.Ramp = &ramp
	res.ReadySecs = time.Since(t0).Seconds()
	log.Printf("  %d/%d ready after %.1fs (ramp cpu %.2f cores, rss %s)", done, level, res.ReadySecs, ramp.CPUCores, mib(after.rssBytes))
	if done < level {
		log.Printf("  WARN only %d/%d PromotionStrategies converged within %s", done, level, h.cfg.readyTimeout)
	}

	log.Printf("  settling for %s", h.cfg.settle)
	time.Sleep(h.cfg.settle)

	// Idle window: CPU profile for the whole window, metrics at both ends.
	a, _ := h.sample()
	if err := h.captureProfiles(dir, "idle", h.cfg.idleWindow); err != nil {
		log.Printf("  WARN idle profiles: %v", err)
	}
	b, _ := h.sample()
	res.Idle = window(a, b)
	res.Idle.CloneDirMiB = dirSizeMiB(h.ctrlTmp)
	res.ObjectCounts = h.objectCounts(ctx)
	log.Printf("  idle: rss=%s heap=%s goroutines=%.0f cpu=%.3f cores git=%.3f cores clones=%.0fMiB",
		mib(b.rssBytes), mib(b.heapInuse), b.goroutines, res.Idle.CPUCores, res.Idle.GitCPUCores, res.Idle.CloneDirMiB)

	for w := 1; w <= h.cfg.waves; w++ {
		wr, err := h.runWave(ctx, level, w, dir)
		if err != nil {
			return err
		}
		res.Waves = append(res.Waves, wr)
	}
	h.results = append(h.results, res)
	return nil
}

func (h *harness) runWave(ctx context.Context, level, w int, dir string) (waveResult, error) {
	h.wave++
	log.Printf("  wave %d: hydrating %d repos", w, level)
	before := make([]map[string]string, level)
	g, _ := errgroup.WithContext(ctx)
	g.SetLimit(h.cfg.createWorkers)
	for i, r := range h.repos[:level] {
		before[i] = map[string]string{}
		for k, v := range r.proposed {
			before[i][k] = v
		}
		g.Go(func() error { return h.hydrate(r, h.wave, false) })
	}
	if err := g.Wait(); err != nil {
		return waveResult{}, err
	}

	a, _ := h.sample()
	t0 := time.Now()
	// Webhooks, like the SCM would send after the hydrator pushes.
	g2, _ := errgroup.WithContext(ctx)
	g2.SetLimit(32)
	for i := range h.repos[:level] {
		for branch, sha := range before[i] {
			g2.Go(func() error { h.sendPushWebhook(sha, branch); return nil })
		}
	}
	_ = g2.Wait()

	var profErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		profErr = h.fetchProfile(fmt.Sprintf("/debug/pprof/profile?seconds=%d", int(h.cfg.waveProfile.Seconds())),
			filepath.Join(dir, fmt.Sprintf("cpu-wave%d.pb.gz", w)))
	}()

	peakRSS, peakHeap := 0.0, 0.0
	peakQueue := map[string]float64{}
	var heapTaken bool
	track := func(s sample) {
		for k, v := range s.metrics {
			if c, ok := strings.CutPrefix(k, "queue_depth/"); ok && v > peakQueue[c] {
				peakQueue[c] = v
			}
		}
		peakRSS = max(peakRSS, s.rssBytes)
		if s.heapInuse > peakHeap {
			peakHeap = s.heapInuse
		}
		// Heap profile once, a third of the way through the CPU profile, while the burst is in flight.
		if !heapTaken && time.Since(t0) > h.cfg.waveProfile/3 {
			heapTaken = true
			_ = h.fetchProfile("/debug/pprof/heap", filepath.Join(dir, fmt.Sprintf("heap-wave%d.pb.gz", w)))
		}
	}
	done, err := h.waitPromoted(ctx, level, h.cfg.waveTimeout, track)
	if err != nil {
		return waveResult{}, err
	}
	b, _ := h.sample()
	wg.Wait()
	if profErr != nil {
		log.Printf("  WARN wave cpu profile: %v", profErr)
	}
	wr := waveResult{Wave: w, Seconds: time.Since(t0).Seconds(), Completed: done, Burst: window(a, b),
		PeakRSSMiB: peakRSS / (1 << 20), PeakHeapMiB: peakHeap / (1 << 20), PeakQueueDepth: peakQueue}
	log.Printf("  wave %d: %d/%d promoted through %d envs in %.1fs; cpu %.2f cores (+git %.2f), peak rss %.0fMiB, peak heap %.0fMiB, peak queue depth %s",
		w, done, level, len(h.branches), wr.Seconds, wr.Burst.CPUCores, wr.Burst.GitCPUCores, wr.PeakRSSMiB, wr.PeakHeapMiB, fmtMap(peakQueue))
	return wr, nil
}

// waitPromoted polls until all n PromotionStrategies are fully promoted or timeout. It stops early if no
// environment makes progress for -stall-timeout. onTick, if set, gets a process sample every poll.
func (h *harness) waitPromoted(ctx context.Context, n int, timeout time.Duration, onTick func(sample)) (int, error) {
	deadline := time.Now().Add(timeout)
	last, lastChange := -1, time.Now()
	lastLog := time.Now()
	for {
		done, envs, err := h.countPromoted(ctx, n)
		if err != nil {
			return 0, err
		}
		if onTick != nil {
			if s, err := h.sample(); err == nil {
				onTick(s)
			}
		}
		if done >= n {
			return done, nil
		}
		if envs != last {
			last, lastChange = envs, time.Now()
		}
		if time.Since(lastLog) > 30*time.Second {
			log.Printf("    progress %d/%d PS, %d/%d envs", done, n, envs, n*len(h.branches))
			lastLog = time.Now()
		}
		if time.Now().After(deadline) || time.Since(lastChange) > h.cfg.stallTimeout {
			return done, nil
		}
		if h.controllerExited() {
			return done, errors.New("controller exited; see controller.log")
		}
		// Listing every PromotionStrategy is not free for the apiserver, which shares the machine.
		time.Sleep(5 * time.Second)
	}
}

// ---------------------------------------------------------------------------
// output

func (h *harness) writeResults() {
	data, _ := json.MarshalIndent(map[string]any{
		"config": map[string]any{
			"levels": h.cfg.levels, "envs": h.cfg.envs, "gates": h.cfg.gates, "waves": h.cfg.waves,
			"qps": h.cfg.qps, "max_concurrent_reconciles": h.cfg.concurrency,
		},
		"results": h.results,
	}, "", "  ")
	_ = os.WriteFile(filepath.Join(h.cfg.outDir, "results.json"), data, 0o644)

	var b strings.Builder
	gates := []string{"dscs"}
	for g := range h.cfg.gates {
		gates = append(gates, g)
	}
	sort.Strings(gates)
	fmt.Fprintf(&b, "# Load test results\n\nenvs/PS=%d gates=%s waves=%d qps=%s concurrency=%s\n\n",
		h.cfg.envs, strings.Join(gates, ","), h.cfg.waves, orShipped(h.cfg.qps), orShipped(h.cfg.concurrency))
	b.WriteString("## Idle (steady state)\n\n")
	b.WriteString("| PS | CTPs | RSS MiB | Heap inuse MiB | Go sys MiB | Goroutines | CPU cores | git CPU cores | Clone dir MiB | Initial reconcile s |\n")
	b.WriteString("|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, r := range h.results {
		fmt.Fprintf(&b, "| %d | %d | %.0f | %.0f | %.0f | %.0f | %.3f | %.3f | %.0f | %.0f |\n",
			r.Level, r.ObjectCounts["ChangeTransferPolicy"], r.Idle.RSSMiB, r.Idle.HeapInuseMiB, r.Idle.GoSysMiB,
			r.Idle.Goroutines, r.Idle.CPUCores, r.Idle.GitCPUCores, r.Idle.CloneDirMiB, r.ReadySecs)
	}
	if h.cfg.waves > 0 {
		b.WriteString("\n## Promotion waves (every PS gets a new dry commit at once)\n\n")
		b.WriteString("| PS | Wave | Seconds to promote all envs | Completed | CPU cores | git CPU cores | Peak RSS MiB | Peak heap MiB | Peak CTP queue |\n")
		b.WriteString("|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
		for _, r := range h.results {
			for _, w := range r.Waves {
				fmt.Fprintf(&b, "| %d | %d | %.0f | %d | %.2f | %.2f | %.0f | %.0f | %.0f |\n",
					r.Level, w.Wave, w.Seconds, w.Completed, w.Burst.CPUCores, w.Burst.GitCPUCores, w.PeakRSSMiB, w.PeakHeapMiB,
					w.PeakQueueDepth["changetransferpolicy"])
			}
		}
	}
	b.WriteString("\n## Idle reconciles/sec by controller\n\n")
	for _, r := range h.results {
		if len(r.Idle.ReconcilesPS) == 0 {
			continue
		}
		fmt.Fprintf(&b, "- **%d PS**: %s\n", r.Level, fmtMap(r.Idle.ReconcilesPS))
	}
	_ = os.WriteFile(filepath.Join(h.cfg.outDir, "results.md"), []byte(b.String()), 0o644)
}

func orShipped(v int) string {
	if v == 0 {
		return "shipped"
	}
	return strconv.Itoa(v)
}

func fmtMap(m map[string]float64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] })
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%.2f", k, m[k]))
	}
	return strings.Join(parts, ", ")
}
