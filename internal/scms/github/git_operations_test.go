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
	srv       *httptest.Server
	domain    string
	listCalls atomic.Int32
}

type testGitHubServerOpts struct {
	releasePage map[int]<-chan struct{}
	pages       [][]string
	pageDelay   time.Duration
}

func newTestGitHubServer(opts testGitHubServerOpts) *testGitHubServer {
	GinkgoHelper()
	ts := &testGitHubServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v3/app/installations", func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 0 {
			page = 1
		}
		if page == 1 {
			ts.listCalls.Add(1)
		}
		if opts.pageDelay > 0 {
			time.Sleep(opts.pageDelay)
		}
		if release, ok := opts.releasePage[page]; ok && release != nil {
			<-release
		}
		if page > len(opts.pages) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("[]"))
			return
		}
		orgs := opts.pages[page-1]
		installations := make([]map[string]any, 0, len(orgs))
		for i, org := range orgs {
			installations = append(installations, map[string]any{
				"id": 1000 + page*100 + i,
				"account": map[string]any{
					"login": org,
					"type":  "Organization",
				},
			})
		}
		body, err := json.Marshal(installations)
		Expect(err).NotTo(HaveOccurred())
		w.Header().Set("Content-Type", "application/json")
		if page < len(opts.pages) {
			next := page + 1
			w.Header().Set("Link", fmt.Sprintf(`<https://example.com/api/v3/app/installations?page=%d>; rel="next"`, next))
		}
		_, _ = w.Write(body)
	})
	mux.HandleFunc("/api/v3/app/installations/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/access_tokens") && r.Method == http.MethodPost {
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

	It("does not block other orgs while pagination is stalled on a later page", func() {
		privKey := testGitHubAppPrivateKey()
		releasePage2 := make(chan struct{})
		server := newTestGitHubServer(testGitHubServerOpts{
			pages:       [][]string{{"known-org"}, {"page2-org"}},
			releasePage: map[int]<-chan struct{}{2: releasePage2},
		})
		provider := testClusterScmProvider(server.domain)
		secret := testSecret(privKey)
		ctx := context.Background()

		g1Done := make(chan error, 1)
		go func() {
			_, _, err := GetClient(ctx, provider, secret, "productlab")
			g1Done <- err
		}()

		Eventually(server.listCalls.Load).WithTimeout(2 * time.Second).Should(BeNumerically(">=", 1))
		time.Sleep(20 * time.Millisecond)

		g2Start := time.Now()
		client, _, err := GetClient(ctx, provider, secret, "known-org")
		g2Duration := time.Since(g2Start)
		Expect(err).NotTo(HaveOccurred())
		Expect(client).NotTo(BeNil())
		Expect(g2Duration).To(BeNumerically("<", 100*time.Millisecond))

		close(releasePage2)
		Expect(<-g1Done).To(HaveOccurred())
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
		close(releasePage1)

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

		close(releasePage2)
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
})
