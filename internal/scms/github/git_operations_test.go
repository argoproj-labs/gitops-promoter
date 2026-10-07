package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

type testGitHubServer struct {
	srv             *httptest.Server
	domain          string
	pages           [][]string
	tokenInstallIDs []int64
	idBase          int64
	mu              sync.Mutex
	listCalls       atomic.Int32
	failPage        atomic.Int32
}

type testGitHubServerOpts struct {
	releasePage map[int]<-chan struct{}
	pages       [][]string
	pageDelay   time.Duration
	failPage    int
}

func newTestGitHubServer(opts testGitHubServerOpts) *testGitHubServer {
	GinkgoHelper()
	ts := &testGitHubServer{pages: opts.pages, idBase: 1000}
	ts.failPage.Store(int32(opts.failPage))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/app/installations", func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		if page == 1 {
			ts.listCalls.Add(1)
		}
		if int(ts.failPage.Load()) == page {
			http.Error(w, "list failed", http.StatusInternalServerError)
			return
		}
		if opts.pageDelay > 0 {
			time.Sleep(opts.pageDelay)
		}
		if release, ok := opts.releasePage[page]; ok && release != nil {
			<-release
		}
		orgs, idBase, nPages := ts.pageOrgs(page)
		if page > nPages {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
			return
		}
		installations := make([]map[string]any, 0, len(orgs))
		for i, org := range orgs {
			installations = append(installations, map[string]any{
				"id": idBase + int64(page*100+i),
				"account": map[string]any{
					"login": org,
					"type":  "Organization",
				},
			})
		}
		body, err := json.Marshal(installations)
		Expect(err).NotTo(HaveOccurred())
		w.Header().Set("Content-Type", "application/json")
		if page < nPages {
			next := page + 1
			w.Header().Set("Link", fmt.Sprintf(`<https://example.com/api/v3/app/installations?page=%d>; rel="next"`, next))
		}
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/api/v3/app/installations/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/access_tokens") && r.Method == http.MethodPost {
			if id, err := installationIDFromTokenPath(r.URL.Path); err == nil {
				ts.recordTokenInstallID(id)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"token":"test-token","expires_at":"2099-01-01T00:00:00Z"}`))
			return
		}
		http.NotFound(w, r)
	})
	ts.srv = httptest.NewTLSServer(mux)
	DeferCleanup(ts.srv.Close)
	ts.domain = strings.TrimPrefix(ts.srv.URL, "https://")
	return ts
}

func (ts *testGitHubServer) setFailPage(page int) {
	ts.failPage.Store(int32(page))
}

func (ts *testGitHubServer) setPages(pages [][]string, idBase int64) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.pages = pages
	if idBase != 0 {
		ts.idBase = idBase
	}
}

func (ts *testGitHubServer) pageOrgs(page int) (orgs []string, idBase int64, nPages int) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	idBase = ts.idBase
	nPages = len(ts.pages)
	if page >= 1 && page <= nPages {
		orgs = ts.pages[page-1]
	}
	return orgs, idBase, nPages
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

// releasePageFunc closes ch once. Tests that block a handler on ch call it when the test is done,
// and register it with DeferCleanup so a failed assertion cannot leave the handler blocked.
func releasePageFunc(ch chan struct{}) func() {
	return func() {
		select {
		case <-ch:
		default:
			close(ch)
		}
	}
}

func installationIDFromTokenPath(path string) (int64, error) {
	rest := strings.TrimPrefix(path, "/api/v3/app/installations/")
	idStr := strings.TrimSuffix(rest, "/access_tokens")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse installation id from %q: %w", path, err)
	}
	return id, nil
}

func testClusterScmProvider(domain string) *v1alpha1.ClusterScmProvider {
	return &v1alpha1.ClusterScmProvider{
		ObjectMeta: metav1.ObjectMeta{Name: "github-scm-provider"},
		Spec: v1alpha1.ScmProviderSpec{
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

var _ = Describe("GetClient", func() {
	BeforeEach(func() {
		resetInstallationCachesForTest()
	})

	It("lists installations once for repeated unknown-org misses within the negative-cache TTL", func() {
		privKey := testGitHubAppPrivateKey()
		server := newTestGitHubServer(testGitHubServerOpts{
			pages: [][]string{{"known-org"}},
		})
		provider := testClusterScmProvider(server.domain)
		secret := testSecret(privKey)
		ctx := context.Background()

		before := server.listCalls.Load()
		for range 3 {
			_, _, err := GetClient(ctx, provider, secret, "productlab")
			Expect(err).To(HaveOccurred())
		}
		Expect(server.listCalls.Load() - before).To(Equal(int32(1)))

		beforeHit := server.listCalls.Load()
		_, _, err := GetClient(ctx, provider, secret, "productlab")
		Expect(err).To(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(beforeHit))
	})

	It("re-lists installations after the negative-cache TTL expires", func() {
		installationMissCacheTTL = 20 * time.Millisecond
		DeferCleanup(func() { installationMissCacheTTL = defaultInstallationMissCacheTTL })

		privKey := testGitHubAppPrivateKey()
		server := newTestGitHubServer(testGitHubServerOpts{
			pages: [][]string{{"known-org"}},
		})
		provider := testClusterScmProvider(server.domain)
		secret := testSecret(privKey)
		ctx := context.Background()

		_, _, _ = GetClient(ctx, provider, secret, "productlab")
		time.Sleep(30 * time.Millisecond)
		_, _, _ = GetClient(ctx, provider, secret, "productlab")
		Expect(server.listCalls.Load()).To(Equal(int32(2)))
	})

	It("waits for every installation page before caching an org", func() {
		privKey := testGitHubAppPrivateKey()
		releasePage2 := make(chan struct{})
		server := newTestGitHubServer(testGitHubServerOpts{
			pages:       [][]string{{"known-org"}, {"page2-org"}},
			releasePage: map[int]<-chan struct{}{2: releasePage2},
		})
		release := releasePageFunc(releasePage2)
		DeferCleanup(release)
		provider := testClusterScmProvider(server.domain)
		secret := testSecret(privKey)
		ctx := context.Background()

		done := make(chan error, 1)
		go func() {
			_, _, err := GetClient(ctx, provider, secret, "known-org")
			done <- err
		}()

		Eventually(server.listCalls.Load).WithTimeout(2 * time.Second).Should(Equal(int32(1)))
		Consistently(done).WithTimeout(50 * time.Millisecond).ShouldNot(Receive())

		release()
		Expect(<-done).NotTo(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(int32(1)))
	})

	It("reflects list pagination duration on cache miss", func() {
		privKey := testGitHubAppPrivateKey()
		const (
			pageCount = 3
			pageDelay = 50 * time.Millisecond
		)
		pages := make([][]string, pageCount)
		for i := range pages {
			pages[i] = []string{fmt.Sprintf("org-%d", i)}
		}
		server := newTestGitHubServer(testGitHubServerOpts{pages: pages, pageDelay: pageDelay})
		provider := testClusterScmProvider(server.domain)
		secret := testSecret(privKey)
		ctx := context.Background()

		start := time.Now()
		_, _, err := GetClient(ctx, provider, secret, "productlab")
		elapsed := time.Since(start)
		Expect(err).To(HaveOccurred())
		Expect(elapsed).To(BeNumerically(">=", time.Duration(pageCount)*pageDelay))
	})

	It("resolves each caller org after a shared installation list", func() {
		privKey := testGitHubAppPrivateKey()
		releasePage1 := make(chan struct{})
		server := newTestGitHubServer(testGitHubServerOpts{
			pages:       [][]string{{"known-org"}},
			releasePage: map[int]<-chan struct{}{1: releasePage1},
		})
		release := releasePageFunc(releasePage1)
		DeferCleanup(release)
		provider := testClusterScmProvider(server.domain)
		secret := testSecret(privKey)
		ctx := context.Background()

		g1Done := make(chan error, 1)
		g2Done := make(chan error, 1)
		go func() {
			_, _, err := GetClient(ctx, provider, secret, "productlab")
			g1Done <- err
		}()
		go func() {
			_, _, err := GetClient(ctx, provider, secret, "known-org")
			g2Done <- err
		}()

		Eventually(server.listCalls.Load).WithTimeout(2 * time.Second).Should(Equal(int32(1)))
		release()

		Expect(<-g1Done).To(HaveOccurred())
		Expect(<-g2Done).NotTo(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(int32(1)))
	})

	It("singleflights concurrent misses for the same app", func() {
		privKey := testGitHubAppPrivateKey()
		releasePage2 := make(chan struct{})
		server := newTestGitHubServer(testGitHubServerOpts{
			pages:       [][]string{{"known-org"}, {"page2-org"}},
			releasePage: map[int]<-chan struct{}{2: releasePage2},
		})
		release := releasePageFunc(releasePage2)
		DeferCleanup(release)
		provider := testClusterScmProvider(server.domain)
		secret := testSecret(privKey)
		ctx := context.Background()

		done := make(chan struct{}, 3)
		for _, org := range []string{"productlab", "missing-a", "missing-b"} {
			go func(org string) {
				_, _, _ = GetClient(ctx, provider, secret, org)
				done <- struct{}{}
			}(org)
		}

		Eventually(server.listCalls.Load).WithTimeout(2 * time.Second).Should(BeNumerically(">=", 1))
		Expect(server.listCalls.Load()).To(Equal(int32(1)))

		release()
		for range 3 {
			<-done
		}
	})

	It("uses warm installation cache for known orgs without extra list calls or metrics", func() {
		privKey := testGitHubAppPrivateKey()
		server := newTestGitHubServer(testGitHubServerOpts{
			pages: [][]string{{"known-org"}},
		})
		provider := testClusterScmProvider(server.domain)
		secret := testSecret(privKey)
		ctx := context.Background()

		_, _, err := GetClient(ctx, provider, secret, "productlab")
		Expect(err).To(HaveOccurred())
		before := server.listCalls.Load()
		_, _, err = GetClient(ctx, provider, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(before))
	})

	It("skips installation listing when installationID is configured", func() {
		privKey := testGitHubAppPrivateKey()
		server := newTestGitHubServer(testGitHubServerOpts{
			pages: [][]string{{"known-org"}},
		})
		provider := testClusterScmProvider(server.domain)
		provider.Spec.GitHub.InstallationID = 42
		secret := testSecret(privKey)
		ctx := context.Background()

		_, _, err := GetClient(ctx, provider, secret, "any-org")
		Expect(err).NotTo(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(int32(0)))
	})

	It("replaces a stale installation ID after the positive-cache TTL", func() {
		installationHitCacheTTL = 20 * time.Millisecond
		DeferCleanup(func() { installationHitCacheTTL = defaultInstallationHitCacheTTL })

		privKey := testGitHubAppPrivateKey()
		server := newTestGitHubServer(testGitHubServerOpts{
			pages: [][]string{{"known-org"}},
		})
		provider := testClusterScmProvider(server.domain)
		secret := testSecret(privKey)
		ctx := context.Background()

		_, itr, err := GetClient(ctx, provider, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())
		_, err = itr.Token(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(server.tokenInstallIDsSnapshot()).To(Equal([]int64{1100}))
		Expect(server.listCalls.Load()).To(Equal(int32(1)))

		server.setPages([][]string{{"known-org"}}, 5000)

		_, _, err = GetClient(ctx, provider, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(int32(1)))

		time.Sleep(30 * time.Millisecond)

		_, itr, err = GetClient(ctx, provider, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())
		_, err = itr.Token(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(int32(2)))
		Expect(server.tokenInstallIDsSnapshot()).To(Equal([]int64{1100, 5100}))
	})

	It("forgets an uninstalled org when the positive-cache TTL expires", func() {
		installationHitCacheTTL = 20 * time.Millisecond
		DeferCleanup(func() { installationHitCacheTTL = defaultInstallationHitCacheTTL })

		privKey := testGitHubAppPrivateKey()
		server := newTestGitHubServer(testGitHubServerOpts{
			pages: [][]string{{"known-org"}},
		})
		provider := testClusterScmProvider(server.domain)
		secret := testSecret(privKey)
		ctx := context.Background()

		_, _, err := GetClient(ctx, provider, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(int32(1)))

		server.setPages([][]string{{"other-org"}}, 0)

		_, _, err = GetClient(ctx, provider, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(int32(1)))

		time.Sleep(30 * time.Millisecond)

		_, _, err = GetClient(ctx, provider, secret, "known-org")
		Expect(err).To(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(int32(2)))

		_, _, err = GetClient(ctx, provider, secret, "other-org")
		Expect(err).NotTo(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(int32(2)))
	})

	It("does not cache installations when a later page fails", func() {
		privKey := testGitHubAppPrivateKey()
		server := newTestGitHubServer(testGitHubServerOpts{
			pages:    [][]string{{"known-org"}, {"page2-org"}},
			failPage: 2,
		})
		provider := testClusterScmProvider(server.domain)
		secret := testSecret(privKey)
		ctx := context.Background()

		_, _, err := GetClient(ctx, provider, secret, "known-org")
		Expect(err).To(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(int32(1)))

		server.setFailPage(0)
		_, _, err = GetClient(ctx, provider, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(int32(2)))
	})

	It("keeps a finished installation snapshot when a refresh list fails", func() {
		installationHitCacheTTL = 20 * time.Millisecond
		DeferCleanup(func() { installationHitCacheTTL = defaultInstallationHitCacheTTL })

		privKey := testGitHubAppPrivateKey()
		server := newTestGitHubServer(testGitHubServerOpts{
			pages: [][]string{{"known-org"}, {"page2-org"}},
		})
		provider := testClusterScmProvider(server.domain)
		secret := testSecret(privKey)
		ctx := context.Background()

		_, _, err := GetClient(ctx, provider, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(int32(1)))

		time.Sleep(30 * time.Millisecond)
		server.setFailPage(2)
		_, _, err = GetClient(ctx, provider, secret, "known-org")
		Expect(err).To(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(int32(2)))

		installationHitCacheTTL = defaultInstallationHitCacheTTL
		server.setFailPage(0)
		_, _, err = GetClient(ctx, provider, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(int32(2)))
	})

	It("fails callers when the installation list times out", func() {
		listInstallationsTimeout = 30 * time.Millisecond
		DeferCleanup(func() { listInstallationsTimeout = defaultListInstallationsTimeout })

		privKey := testGitHubAppPrivateKey()
		releasePage1 := make(chan struct{})
		server := newTestGitHubServer(testGitHubServerOpts{
			pages:       [][]string{{"known-org"}},
			releasePage: map[int]<-chan struct{}{1: releasePage1},
		})
		DeferCleanup(releasePageFunc(releasePage1))
		provider := testClusterScmProvider(server.domain)
		secret := testSecret(privKey)
		ctx := context.Background()

		_, _, err := GetClient(ctx, provider, secret, "known-org")
		Expect(err).To(HaveOccurred())
		Expect(server.listCalls.Load()).To(Equal(int32(1)))
	})

	It("does not share installation cache entries across GitHub domains", func() {
		privKey := testGitHubAppPrivateKey()
		serverA := newTestGitHubServer(testGitHubServerOpts{
			pages: [][]string{{"known-org"}},
		})
		serverB := newTestGitHubServer(testGitHubServerOpts{
			pages: [][]string{{"other-org"}},
		})
		providerA := testClusterScmProvider(serverA.domain)
		providerB := testClusterScmProvider(serverB.domain)
		secret := testSecret(privKey)
		ctx := context.Background()

		_, _, err := GetClient(ctx, providerA, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())
		Expect(serverA.listCalls.Load()).To(Equal(int32(1)))

		_, _, err = GetClient(ctx, providerB, secret, "known-org")
		Expect(err).To(HaveOccurred())
		Expect(serverB.listCalls.Load()).To(Equal(int32(1)))
		Expect(serverA.listCalls.Load()).To(Equal(int32(1)))

		_, _, err = GetClient(ctx, providerB, secret, "known-org")
		Expect(err).To(HaveOccurred())
		Expect(serverB.listCalls.Load()).To(Equal(int32(1)))

		_, _, err = GetClient(ctx, providerA, secret, "known-org")
		Expect(err).NotTo(HaveOccurred())
		Expect(serverA.listCalls.Load()).To(Equal(int32(1)))

		_, _, err = GetClient(ctx, providerB, secret, "other-org")
		Expect(err).NotTo(HaveOccurred())
		Expect(serverB.listCalls.Load()).To(Equal(int32(1)))
	})
})
