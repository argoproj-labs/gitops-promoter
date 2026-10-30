package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	v1 "k8s.io/api/core/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const testAppID int64 = 219

func testGitHubAppPrivateKey() []byte {
	GinkgoHelper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred())
	der, err := x509.MarshalPKCS8PrivateKey(key)
	Expect(err).NotTo(HaveOccurred())
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// testGitHubServer serves the per-account installation endpoints. GitHub logins are case-insensitive, so orgs and
// users are keyed by lowercase login.
type testGitHubServer struct {
	srv             *httptest.Server
	release         <-chan struct{}
	orgs            map[string]int64
	users           map[string]int64
	orgCalls        map[string]int
	userCalls       map[string]int
	domain          string
	tokenInstallIDs []int64
	mu              sync.Mutex
	listCalls       atomic.Int32
	fail            atomic.Bool
}

type testGitHubServerOpts struct {
	// release, when set, blocks every installation lookup until it is closed.
	release <-chan struct{}
	orgs    map[string]int64
	users   map[string]int64
}

func newTestGitHubServer(opts testGitHubServerOpts) *testGitHubServer {
	GinkgoHelper()
	ts := &testGitHubServer{
		release:   opts.release,
		orgs:      map[string]int64{},
		users:     map[string]int64{},
		orgCalls:  map[string]int{},
		userCalls: map[string]int{},
	}
	ts.setInstallations(opts.orgs, opts.users)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v3/orgs/{org}/installation", func(w http.ResponseWriter, r *http.Request) {
		ts.serveInstallation(w, r.PathValue("org"), "Organization")
	})
	mux.HandleFunc("GET /api/v3/users/{user}/installation", func(w http.ResponseWriter, r *http.Request) {
		ts.serveInstallation(w, r.PathValue("user"), "User")
	})
	mux.HandleFunc("GET /api/v3/app/installations", func(w http.ResponseWriter, r *http.Request) {
		ts.listCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	})
	mux.HandleFunc("POST /api/v3/app/installations/{id}/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		if id, err := strconv.ParseInt(r.PathValue("id"), 10, 64); err == nil {
			ts.recordTokenInstallID(id)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"test-token","expires_at":"2099-01-01T00:00:00Z"}`))
	})
	ts.srv = httptest.NewTLSServer(mux)
	DeferCleanup(ts.srv.Close)
	ts.domain = strings.TrimPrefix(ts.srv.URL, "https://")
	return ts
}

func (ts *testGitHubServer) serveInstallation(w http.ResponseWriter, login, accountType string) {
	login = strings.ToLower(login)
	ts.mu.Lock()
	installations, calls := ts.orgs, ts.orgCalls
	if accountType == "User" {
		installations, calls = ts.users, ts.userCalls
	}
	calls[login]++
	id, ok := installations[login]
	ts.mu.Unlock()

	if ts.release != nil {
		<-ts.release
	}
	if ts.fail.Load() {
		http.Error(w, "lookup failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
		return
	}
	body, err := json.Marshal(map[string]any{
		"id":      id,
		"account": map[string]any{"login": login, "type": accountType},
	})
	Expect(err).NotTo(HaveOccurred())
	_, _ = w.Write(body)
}

func (ts *testGitHubServer) setInstallations(orgs, users map[string]int64) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	clear(ts.orgs)
	clear(ts.users)
	for login, id := range orgs {
		ts.orgs[strings.ToLower(login)] = id
	}
	for login, id := range users {
		ts.users[strings.ToLower(login)] = id
	}
}

func (ts *testGitHubServer) orgLookups(login string) int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.orgCalls[strings.ToLower(login)]
}

func (ts *testGitHubServer) userLookups(login string) int {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.userCalls[strings.ToLower(login)]
}

func (ts *testGitHubServer) recordTokenInstallID(id int64) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.tokenInstallIDs = append(ts.tokenInstallIDs, id)
}

func (ts *testGitHubServer) tokenInstallIDsSnapshot() []int64 {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	out := make([]int64, len(ts.tokenInstallIDs))
	copy(out, ts.tokenInstallIDs)
	return out
}

// releaseFunc closes ch once. Tests that block a handler on ch call it when the test is done,
// and register it with DeferCleanup so a failed assertion cannot leave the handler blocked.
func releaseFunc(ch chan struct{}) func() {
	return func() {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

func testClusterScmProvider(domain string) *v1alpha1.ClusterScmProvider {
	return &v1alpha1.ClusterScmProvider{
		Name: "github-scm-provider",
		Spec: v1alpha1.ClusterScmProviderSpec{
			GitHub: &v1alpha1.GitHub{
				Domain: domain,
				AppID:  testAppID,
			},
		},
	}
}

func testSecret(privateKey []byte) v1.Secret {
	return v1.Secret{Data: map[string][]byte{githubAppPrivateKeySecretKey: privateKey}}
}

// installationIDFor returns the installation ID the client returned by GetClient authenticates as.
func installationIDFor(ctx context.Context, server *testGitHubServer, provider *v1alpha1.ClusterScmProvider, secret v1.Secret, org string) int64 {
	GinkgoHelper()
	_, itr, err := GetClient(ctx, provider, secret, org)
	Expect(err).NotTo(HaveOccurred())
	_, err = itr.Token(ctx)
	Expect(err).NotTo(HaveOccurred())
	ids := server.tokenInstallIDsSnapshot()
	Expect(ids).NotTo(BeEmpty())
	return ids[len(ids)-1]
}

var _ = Describe("GetClient", func() {
	var (
		ctx    context.Context
		secret v1.Secret
	)

	BeforeEach(func() {
		resetInstallationCachesForTest()
		ctx = context.Background()
		secret = testSecret(testGitHubAppPrivateKey())
	})

	It("looks up only the requested org's installation", func() {
		server := newTestGitHubServer(testGitHubServerOpts{
			orgs: map[string]int64{"known-org": 1100, "other-org": 2200},
		})
		provider := testClusterScmProvider(server.domain)

		Expect(installationIDFor(ctx, server, provider, secret, "known-org")).To(Equal(int64(1100)))
		Expect(server.orgLookups("known-org")).To(Equal(1))
		Expect(server.orgLookups("other-org")).To(Equal(0))
		Expect(server.userLookups("known-org")).To(Equal(0))
		Expect(server.listCalls.Load()).To(Equal(int32(0)))
	})

	It("falls back to the user installation when the owner is not an org", func() {
		server := newTestGitHubServer(testGitHubServerOpts{
			users: map[string]int64{"some-user": 3300},
		})
		provider := testClusterScmProvider(server.domain)

		Expect(installationIDFor(ctx, server, provider, secret, "some-user")).To(Equal(int64(3300)))
		Expect(server.orgLookups("some-user")).To(Equal(1))
		Expect(server.userLookups("some-user")).To(Equal(1))

		_, _, err := GetClient(ctx, provider, secret, "some-user")
		Expect(err).NotTo(HaveOccurred())
		Expect(server.orgLookups("some-user")).To(Equal(1))
		Expect(server.userLookups("some-user")).To(Equal(1))
	})

	It("treats owner logins case-insensitively", func() {
		server := newTestGitHubServer(testGitHubServerOpts{
			orgs: map[string]int64{"known-org": 1100},
		})
		provider := testClusterScmProvider(server.domain)

		Expect(installationIDFor(ctx, server, provider, secret, "Known-Org")).To(Equal(int64(1100)))
		Expect(installationIDFor(ctx, server, provider, secret, "known-org")).To(Equal(int64(1100)))
		Expect(server.orgLookups("known-org")).To(Equal(1))
	})

	It("looks up an uninstalled owner once within the negative-cache TTL", func() {
		server := newTestGitHubServer(testGitHubServerOpts{
			orgs: map[string]int64{"known-org": 1100},
		})
		provider := testClusterScmProvider(server.domain)

		for range 3 {
			_, _, err := GetClient(ctx, provider, secret, "productlab")
			Expect(err).To(MatchError(ContainSubstring("not found for org: productlab")))
		}
		Expect(server.orgLookups("productlab")).To(Equal(1))
		Expect(server.userLookups("productlab")).To(Equal(1))
	})

	It("looks up an uninstalled owner again after the negative-cache TTL expires", func() {
		installationMissCacheTTL = 20 * time.Millisecond
		DeferCleanup(func() { installationMissCacheTTL = defaultInstallationMissCacheTTL })

		server := newTestGitHubServer(testGitHubServerOpts{})
		provider := testClusterScmProvider(server.domain)

		_, _, err := GetClient(ctx, provider, secret, "productlab")
		Expect(err).To(HaveOccurred())

		server.setInstallations(map[string]int64{"productlab": 4400}, nil)
		time.Sleep(30 * time.Millisecond)

		Expect(installationIDFor(ctx, server, provider, secret, "productlab")).To(Equal(int64(4400)))
		Expect(server.orgLookups("productlab")).To(Equal(2))
	})

	It("caches each owner separately", func() {
		server := newTestGitHubServer(testGitHubServerOpts{
			orgs: map[string]int64{"known-org": 1100},
		})
		provider := testClusterScmProvider(server.domain)

		_, _, err := GetClient(ctx, provider, secret, "productlab")
		Expect(err).To(HaveOccurred())
		_, _, err = GetClient(ctx, provider, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())
		_, _, err = GetClient(ctx, provider, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())

		Expect(server.orgLookups("productlab")).To(Equal(1))
		Expect(server.orgLookups("known-org")).To(Equal(1))
	})

	It("singleflights concurrent lookups for the same owner", func() {
		release := make(chan struct{})
		server := newTestGitHubServer(testGitHubServerOpts{
			orgs:    map[string]int64{"known-org": 1100},
			release: release,
		})
		// Registered after the server so it runs before srv.Close, which waits for blocked handlers.
		DeferCleanup(releaseFunc(release))
		provider := testClusterScmProvider(server.domain)

		done := make(chan error, 3)
		for range 3 {
			go func() {
				_, _, err := GetClient(ctx, provider, secret, "known-org")
				done <- err
			}()
		}

		Eventually(func() int { return server.orgLookups("known-org") }).WithTimeout(2 * time.Second).Should(Equal(1))
		Consistently(func() int { return server.orgLookups("known-org") }).WithTimeout(50 * time.Millisecond).Should(Equal(1))

		releaseFunc(release)()
		for range 3 {
			Expect(<-done).NotTo(HaveOccurred())
		}
		Expect(server.orgLookups("known-org")).To(Equal(1))
	})

	It("skips the installation lookup when installationID is configured", func() {
		server := newTestGitHubServer(testGitHubServerOpts{
			orgs: map[string]int64{"known-org": 1100},
		})
		provider := testClusterScmProvider(server.domain)
		provider.Spec.GitHub.InstallationID = 42

		_, _, err := GetClient(ctx, provider, secret, "any-org")
		Expect(err).NotTo(HaveOccurred())
		Expect(server.orgLookups("any-org")).To(Equal(0))
		Expect(server.userLookups("any-org")).To(Equal(0))
	})

	It("replaces a stale installation ID after the positive-cache TTL", func() {
		installationHitCacheTTL = 20 * time.Millisecond
		DeferCleanup(func() { installationHitCacheTTL = defaultInstallationHitCacheTTL })

		server := newTestGitHubServer(testGitHubServerOpts{
			orgs: map[string]int64{"known-org": 1100},
		})
		provider := testClusterScmProvider(server.domain)

		Expect(installationIDFor(ctx, server, provider, secret, "known-org")).To(Equal(int64(1100)))

		server.setInstallations(map[string]int64{"known-org": 5100}, nil)
		Expect(installationIDFor(ctx, server, provider, secret, "known-org")).To(Equal(int64(1100)))
		Expect(server.orgLookups("known-org")).To(Equal(1))

		time.Sleep(30 * time.Millisecond)

		Expect(installationIDFor(ctx, server, provider, secret, "known-org")).To(Equal(int64(5100)))
		Expect(server.orgLookups("known-org")).To(Equal(2))
	})

	It("forgets an uninstalled owner when the positive-cache TTL expires", func() {
		installationHitCacheTTL = 20 * time.Millisecond
		DeferCleanup(func() { installationHitCacheTTL = defaultInstallationHitCacheTTL })

		server := newTestGitHubServer(testGitHubServerOpts{
			orgs: map[string]int64{"known-org": 1100},
		})
		provider := testClusterScmProvider(server.domain)

		_, _, err := GetClient(ctx, provider, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())

		server.setInstallations(nil, nil)
		_, _, err = GetClient(ctx, provider, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())

		time.Sleep(30 * time.Millisecond)

		_, _, err = GetClient(ctx, provider, secret, "known-org")
		Expect(err).To(HaveOccurred())
		Expect(server.orgLookups("known-org")).To(Equal(2))
	})

	It("does not cache a failed lookup", func() {
		server := newTestGitHubServer(testGitHubServerOpts{
			orgs: map[string]int64{"known-org": 1100},
		})
		provider := testClusterScmProvider(server.domain)

		server.fail.Store(true)
		_, _, err := GetClient(ctx, provider, secret, "known-org")
		Expect(err).To(MatchError(ContainSubstring("failed to get installation")))
		Expect(server.userLookups("known-org")).To(Equal(0))

		server.fail.Store(false)
		_, _, err = GetClient(ctx, provider, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())
		Expect(server.orgLookups("known-org")).To(Equal(2))
	})

	It("keeps a found installation ID when a refresh lookup fails", func() {
		installationHitCacheTTL = 20 * time.Millisecond
		DeferCleanup(func() { installationHitCacheTTL = defaultInstallationHitCacheTTL })

		server := newTestGitHubServer(testGitHubServerOpts{
			orgs: map[string]int64{"known-org": 1100},
		})
		provider := testClusterScmProvider(server.domain)

		_, _, err := GetClient(ctx, provider, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())

		time.Sleep(30 * time.Millisecond)
		server.fail.Store(true)
		_, _, err = GetClient(ctx, provider, secret, "known-org")
		Expect(err).To(HaveOccurred())
		Expect(server.orgLookups("known-org")).To(Equal(2))

		installationHitCacheTTL = defaultInstallationHitCacheTTL
		server.fail.Store(false)
		_, _, err = GetClient(ctx, provider, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())
		Expect(server.orgLookups("known-org")).To(Equal(2))
	})

	It("fails callers when the installation lookup times out", func() {
		findInstallationTimeout = 30 * time.Millisecond
		DeferCleanup(func() { findInstallationTimeout = defaultFindInstallationTimeout })

		release := make(chan struct{})
		server := newTestGitHubServer(testGitHubServerOpts{
			orgs:    map[string]int64{"known-org": 1100},
			release: release,
		})
		// Registered after the server so it runs before srv.Close, which waits for blocked handlers.
		DeferCleanup(releaseFunc(release))
		provider := testClusterScmProvider(server.domain)

		_, _, err := GetClient(ctx, provider, secret, "known-org")
		Expect(err).To(HaveOccurred())
		Expect(server.orgLookups("known-org")).To(Equal(1))
	})

	It("does not share installation cache entries across GitHub domains", func() {
		serverA := newTestGitHubServer(testGitHubServerOpts{
			orgs: map[string]int64{"known-org": 1100},
		})
		serverB := newTestGitHubServer(testGitHubServerOpts{})
		providerA := testClusterScmProvider(serverA.domain)
		providerB := testClusterScmProvider(serverB.domain)

		_, _, err := GetClient(ctx, providerA, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())

		_, _, err = GetClient(ctx, providerB, secret, "known-org")
		Expect(err).To(HaveOccurred())
		Expect(serverB.orgLookups("known-org")).To(Equal(1))

		_, _, err = GetClient(ctx, providerA, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())
		Expect(serverA.orgLookups("known-org")).To(Equal(1))
	})
})
