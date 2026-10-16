package scmserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const schedulerInterval = 20 * time.Millisecond

// Options configures a Server.
type Options struct {
	// Logger receives server logs. Defaults to a logger that discards everything.
	Logger *slog.Logger
	// Listen maps a provider to the address its HTTPS listener binds, such as ":9001". Providers without an entry
	// bind 127.0.0.1 on a random port.
	Listen map[Provider]string
	// AdminListen is the address of the plain HTTP admin API. Defaults to 127.0.0.1 on a random port.
	AdminListen string
	// DataDir holds the bare git repositories. Defaults to a temporary directory removed by Shutdown.
	DataDir string
	// PublicHost is the host name used in URLs the server generates outside of a request, such as webhook payloads.
	// Defaults to "localhost".
	PublicHost string
	// CertPEM and KeyPEM are the TLS serving certificate and key. When empty, the server generates a CA and a serving
	// certificate for PublicHost, localhost and 127.0.0.1.
	CertPEM []byte
	// KeyPEM is the private key for CertPEM.
	KeyPEM []byte
	// CAPEM is the CA certificate clients must trust. It is served at /ca.pem on the admin API. It is generated
	// together with the serving certificate when CertPEM is empty.
	CAPEM []byte
}

// Server is a test SCM server impersonating every supported provider.
type Server struct {
	log       *slog.Logger
	git       *gitCLI
	store     *store
	behavior  *behaviorSet
	faults    *faultSet
	requests  *requestLog
	webhooks  *webhookDispatcher
	tlsConfig *tls.Config
	listeners map[Provider]net.Listener
	addrs     map[Provider]string
	cancel    context.CancelFunc
	admin     net.Listener
	opts      Options
	servers   []*http.Server
	caPEM     []byte
	wg        sync.WaitGroup
	removeDir bool
}

// New creates a Server. Call Start to begin serving.
func New(ctx context.Context, opts Options) (*Server, error) {
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opts.PublicHost == "" {
		opts.PublicHost = "localhost"
	}
	s := &Server{
		log:       opts.Logger,
		opts:      opts,
		store:     newStore(),
		behavior:  newBehaviorSet(),
		faults:    &faultSet{},
		requests:  &requestLog{},
		webhooks:  newWebhookDispatcher(),
		listeners: map[Provider]net.Listener{},
		addrs:     map[Provider]string{},
	}

	if opts.DataDir == "" {
		dir, err := os.MkdirTemp("", "scmserver-*")
		if err != nil {
			return nil, fmt.Errorf("create data directory: %w", err)
		}
		s.opts.DataDir = dir
		s.removeDir = true
	}
	dataDir, err := filepath.Abs(s.opts.DataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data directory: %w", err)
	}
	s.opts.DataDir = dataDir
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	s.git, err = newGitCLI(ctx, dataDir)
	if err != nil {
		return nil, err
	}

	certPEM, keyPEM, caPEM := opts.CertPEM, opts.KeyPEM, opts.CAPEM
	if len(certPEM) == 0 {
		var caKey []byte
		caPEM, caKey, err = GenerateCA("scmserver test CA")
		if err != nil {
			return nil, err
		}
		hosts := []string{s.opts.PublicHost, "localhost", "127.0.0.1", "::1"}
		certPEM, keyPEM, err = GenerateServingCert(caPEM, caKey, hosts)
		if err != nil {
			return nil, err
		}
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("load serving certificate: %w", err)
	}
	s.caPEM = caPEM
	s.tlsConfig = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	return s, nil
}

// CAPEM returns the CA certificate clients must trust, if known.
func (s *Server) CAPEM() []byte {
	return s.caPEM
}

// Start binds all listeners and serves until Shutdown is called or ctx is canceled.
func (s *Server) Start(ctx context.Context) error {
	ctx, s.cancel = context.WithCancel(ctx)
	lc := &net.ListenConfig{}
	for _, p := range AllProviders {
		addr := s.opts.Listen[p]
		if addr == "" {
			addr = "127.0.0.1:0"
		}
		l, err := lc.Listen(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("listen for %s on %s: %w", p, addr, err)
		}
		s.listeners[p] = l
		s.addrs[p] = l.Addr().String()
		srv := &http.Server{
			Handler:           s.providerHandler(p),
			TLSConfig:         s.tlsConfig,
			ReadHeaderTimeout: 30 * time.Second,
			ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelDebug),
			BaseContext:       func(net.Listener) context.Context { return ctx },
		}
		s.servers = append(s.servers, srv)
		s.serve(srv, tls.NewListener(l, s.tlsConfig))
	}

	adminAddr := s.opts.AdminListen
	if adminAddr == "" {
		adminAddr = "127.0.0.1:0"
	}
	l, err := lc.Listen(ctx, "tcp", adminAddr)
	if err != nil {
		return fmt.Errorf("listen for admin API on %s: %w", adminAddr, err)
	}
	s.admin = l
	adminSrv := &http.Server{
		Handler:           s.adminHandler(),
		ReadHeaderTimeout: 30 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	s.servers = append(s.servers, adminSrv)
	s.serve(adminSrv, l)

	s.wg.Go(func() { s.runScheduler(ctx) })
	s.log.Info("test SCM server started", "admin", s.AdminURL(), "providers", s.addrs, "dataDir", s.opts.DataDir)
	return nil
}

func (s *Server) serve(srv *http.Server, l net.Listener) {
	s.wg.Go(func() {
		if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Error("server stopped", "addr", l.Addr().String(), "error", err)
		}
	})
}

// Shutdown stops all listeners and background work and removes a temporary data directory.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	var errs []error
	for _, srv := range s.servers {
		if err := srv.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	s.wg.Wait()
	s.webhooks.wait()
	if s.removeDir {
		if err := os.RemoveAll(s.opts.DataDir); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Addr returns the bound host:port of provider p's listener.
func (s *Server) Addr(p Provider) string {
	return s.addrs[p]
}

// Domain returns the value to put in an ScmProvider's domain field to reach provider p from this host
// ("localhost:<port>").
func (s *Server) Domain(p Provider) string {
	_, port, _ := net.SplitHostPort(s.addrs[p])
	return net.JoinHostPort("localhost", port)
}

// AdminURL returns the base URL of the admin API.
func (s *Server) AdminURL() string {
	if s.admin == nil {
		return ""
	}
	return "http://" + s.admin.Addr().String()
}

// publicBaseURL is the base URL of provider p as seen by clients, used outside of a request.
func (s *Server) publicBaseURL(p Provider) string {
	_, port, _ := net.SplitHostPort(s.addrs[p])
	return "https://" + net.JoinHostPort(s.opts.PublicHost, port)
}

// requestBaseURL is the base URL of the provider as addressed by the client of r.
func requestBaseURL(r *http.Request) string {
	return "https://" + r.Host
}

// runScheduler drives asynchronous provider behavior such as Azure DevOps completions.
func (s *Server) runScheduler(ctx context.Context) {
	ticker := time.NewTicker(schedulerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.processAzureCompletions(ctx)
			s.processBitbucketAsyncMerges(ctx)
		}
	}
}

// providerHandler builds the HTTPS handler of provider p: request logging, latency, fault injection, git smart HTTP
// and the provider's REST API.
func (s *Server) providerHandler(p Provider) http.Handler {
	mux := http.NewServeMux()
	switch p {
	case GitHub:
		s.githubRoutes(mux)
	case GitLab:
		s.gitlabRoutes(mux)
	case AzureDevOps:
		s.azureRoutes(mux)
	case BitbucketCloud:
		s.bitbucketRoutes(mux)
	case Gitea, Forgejo:
		s.giteaRoutes(p, mux)
	default:
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeProviderError(w, p, http.StatusNotFound, "Not Found")
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w}
		gitReq, isGit := parseGitRequest(r.URL.Path)
		rec.git = isGit
		defer func() {
			s.requests.add(RequestRecord{
				Time: start.UTC(), Provider: p, Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery,
				Principal: rec.principal, Duration: D(time.Since(start)), StatusCode: rec.status, Fault: rec.fault,
				Git: rec.git,
			})
		}()

		if d := s.behavior.get(p).latency(); d > 0 {
			if !sleepCtx(r.Context(), d) {
				return
			}
		}
		if rule := s.faults.match(p, r); rule != nil {
			if s.applyFault(rec, w, r, p, rule) {
				return
			}
		}
		if isGit {
			s.serveGit(rec, r, p, gitReq)
			return
		}
		mux.ServeHTTP(rec, r)
	})
}

// applyFault applies a matched fault rule. It returns true when the request was answered by the fault.
func (s *Server) applyFault(rec *recorder, w http.ResponseWriter, r *http.Request, p Provider, rule *FaultRule) bool {
	if rule.Delay.Duration > 0 && !sleepCtx(r.Context(), rule.Delay.Duration) {
		return true
	}
	if rule.Drop {
		rec.fault = true
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return true
			}
		}
		panic(http.ErrAbortHandler)
	}
	if rule.Status == 0 {
		return false
	}
	rec.fault = true
	for k, v := range rule.Headers {
		rec.Header().Set(k, v)
	}
	if rule.Body != "" {
		if rec.Header().Get("Content-Type") == "" {
			rec.Header().Set("Content-Type", "application/json; charset=utf-8")
		}
		rec.Header().Set("Content-Length", strconv.Itoa(len(rule.Body)))
		rec.WriteHeader(rule.Status)
		_, _ = io.WriteString(rec, rule.Body)
		return true
	}
	writeProviderError(rec, p, rule.Status, rule.Message)
	return true
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// gitRepoFromPath maps a git URL path to a hosted repository.
func (s *Server) gitRepoFromPath(p Provider, repoPath string) (*repo, error) {
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	if p == AzureDevOps {
		parts := strings.Split(repoPath, "/")
		if len(parts) != 4 || parts[2] != "_git" {
			return nil, errNotFound
		}
		return s.store.findAzureRepoLocked(parts[0], parts[1], parts[3])
	}
	idx := strings.LastIndex(repoPath, "/")
	if idx <= 0 {
		return nil, errNotFound
	}
	return s.store.findRepoLocked(p, repoPath[:idx], "", repoPath[idx+1:])
}

// serveGit serves git smart HTTP for a hosted repository, emitting push events for receive-pack requests.
func (s *Server) serveGit(rec *recorder, r *http.Request, p Provider, gitReq gitRequest) {
	who, err := s.authenticateGit(p, r)
	if err != nil {
		rec.Header().Set("WWW-Authenticate", `Basic realm="`+string(p)+`"`)
		http.Error(rec, "authentication required", http.StatusUnauthorized)
		return
	}
	rec.principal = who.name
	target, err := s.gitRepoFromPath(p, gitReq.repoPath)
	if err != nil {
		http.Error(rec, "repository not found", http.StatusNotFound)
		return
	}
	rel, err := filepath.Rel(s.opts.DataDir, target.dir)
	if err != nil {
		http.Error(rec, err.Error(), http.StatusInternalServerError)
		return
	}

	if gitReq.rest != "/git-receive-pack" {
		s.git.serveSmartHTTP(rec, r, rel, gitReq.rest, who.name)
		return
	}

	ctx := r.Context()
	target.gitMu.Lock()
	before, err := s.git.refs(ctx, target.dir)
	if err != nil {
		target.gitMu.Unlock()
		http.Error(rec, err.Error(), http.StatusInternalServerError)
		return
	}
	s.git.serveSmartHTTP(rec, r, rel, gitReq.rest, who.name)
	after, err := s.git.refs(context.WithoutCancel(ctx), target.dir)
	target.gitMu.Unlock()
	if err != nil {
		s.log.Error("list refs after push", "repo", target.dir, "error", err)
		return
	}
	s.afterRefChanges(context.WithoutCancel(ctx), target, diffRefs(before, after), who.name)
}

// apiHandler authenticates and rate-limits a REST API request before calling h.
func (s *Server) apiHandler(p Provider, h func(http.ResponseWriter, *http.Request, principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		who, err := s.authenticateAPI(p, r)
		if err != nil {
			writeUnauthorized(w, p)
			return
		}
		if rec, ok := w.(*recorder); ok {
			rec.principal = who.name
		}
		if !s.rateLimit(w, p, who) {
			return
		}
		h(w, r, who)
	}
}

// rateLimit counts the request against the caller's rate limit, writes the provider's rate limit headers, and
// writes the provider's throttling response when the limit is exceeded.
func (s *Server) rateLimit(w http.ResponseWriter, p Provider, who principal) bool {
	state, enabled := s.behavior.take(p, who.key, s.now())
	if !enabled {
		return true
	}
	h := w.Header()
	resetUnix := strconv.FormatInt(state.reset.Unix(), 10)
	retryAfter := strconv.Itoa(max(int(time.Until(state.reset).Seconds()), 1))
	switch p {
	case GitHub:
		h.Set("X-Ratelimit-Limit", strconv.Itoa(state.limit))
		h.Set("X-Ratelimit-Remaining", strconv.Itoa(state.remaining))
		h.Set("X-Ratelimit-Reset", resetUnix)
		h.Set("X-Ratelimit-Used", strconv.Itoa(state.used))
		h.Set("X-Ratelimit-Resource", "core")
		if state.exceeded {
			msg := "API rate limit exceeded for " + who.key + "."
			if who.installationID != 0 {
				msg = fmt.Sprintf("API rate limit exceeded for installation ID %d.", who.installationID)
			}
			writeJSON(w, http.StatusForbidden, map[string]any{
				"message":           msg,
				"documentation_url": "https://docs.github.com/rest/overview/rate-limits-for-the-rest-api",
			})
			return false
		}
	case GitLab:
		h.Set("Ratelimit-Limit", strconv.Itoa(state.limit))
		h.Set("Ratelimit-Observed", strconv.Itoa(state.used))
		h.Set("Ratelimit-Remaining", strconv.Itoa(state.remaining))
		h.Set("Ratelimit-Reset", resetUnix)
		h.Set("Ratelimit-Resettime", state.reset.UTC().Format(http.TimeFormat))
		if state.exceeded {
			h.Set("Retry-After", retryAfter)
			h.Set("Content-Type", "text/plain")
			http.Error(w, "Retry later", http.StatusTooManyRequests)
			return false
		}
	case BitbucketCloud:
		h.Set("X-Ratelimit-Limit", strconv.Itoa(state.limit))
		h.Set("X-Ratelimit-Resource", "api")
		h.Set("X-Ratelimit-Nearlimit", strconv.FormatBool(state.remaining < state.limit/20))
		if state.exceeded {
			writeProviderError(w, p, http.StatusTooManyRequests, "Rate limit for this resource has been exceeded")
			return false
		}
	case AzureDevOps:
		if state.exceeded {
			h.Set("Retry-After", retryAfter)
			h.Set("X-Ratelimit-Resource", "ATCPU")
			h.Set("X-Ratelimit-Limit", strconv.Itoa(state.limit))
			h.Set("X-Ratelimit-Remaining", "0")
			h.Set("X-Ratelimit-Reset", resetUnix)
			writeJSON(w, http.StatusTooManyRequests, azureError("Request was blocked due to exceeding usage of resource 'ATCPU'.", "", 0))
			return false
		}
	default:
		if state.exceeded {
			writeProviderError(w, p, http.StatusTooManyRequests, "rate limit exceeded")
			return false
		}
	}
	return true
}
