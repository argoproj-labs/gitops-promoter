package github

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("GetClient", func() {
	Describe("concurrent cache population", func() {
		It("should call the GitHub installations API only once when multiple goroutines race for the same org", func() {
			const numGoroutines = 5
			var apiCallCount int64

			// Generate a throwaway RSA key; the test server never validates JWTs.
			key, err := rsa.GenerateKey(rand.Reader, 2048)
			Expect(err).NotTo(HaveOccurred())
			var buf bytes.Buffer
			Expect(pem.Encode(&buf, &pem.Block{
				Type:  "RSA PRIVATE KEY",
				Bytes: x509.MarshalPKCS1PrivateKey(key),
			})).To(Succeed())
			privateKeyPEM := buf.Bytes()

			installID := int64(42)
			installLogin := "testorg"

			// The server sleeps on the first request so that, by the time it
			// responds, all other goroutines have had ample opportunity to discover
			// the empty cache and attempt their own API calls.  This reliably
			// triggers the race: with the current code every goroutine acquires the
			// write lock in turn and calls ListInstallations again.
			var firstCall int32 = 1
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.URL.Path, "installations") {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				atomic.AddInt64(&apiCallCount, 1)
				if atomic.CompareAndSwapInt32(&firstCall, 1, 0) {
					// Hold the first response long enough for the other goroutines
					// to pass the empty-cache read-lock check and queue up.
					time.Sleep(50 * time.Millisecond)
				}
				type fakeAccount struct {
					Login string `json:"login"`
				}
				type fakeInstallation struct {
					Account fakeAccount `json:"account"`
					ID      int64       `json:"id"`
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode([]fakeInstallation{
					{ID: installID, Account: fakeAccount{Login: installLogin}},
				})
			}))
			defer server.Close()

			// Redirect all HTTPS traffic to the TLS test server (self-signed cert).
			origTransport := http.DefaultTransport
			http.DefaultTransport = server.Client().Transport
			defer func() { http.DefaultTransport = origTransport }()

			// Reset the global cache so this test starts with a cold cache.
			// NOTE: Ginkgo runs specs within a suite sequentially by default, so
			// mutating package-level state here is safe as long as all specs in
			// this suite restore it before returning (the defer below does that for
			// http.DefaultTransport; installationIds is always re-initialised here).
			appInstallationIdCacheMutex.Lock()
			installationIds = make(map[orgAppId]int64)
			appInstallationIdCacheMutex.Unlock()

			domain := strings.TrimPrefix(server.URL, "https://")
			scmProvider := &v1alpha1.ScmProvider{
				ObjectMeta: metav1.ObjectMeta{Name: "test-provider-" + domain},
				Spec: v1alpha1.ScmProviderSpec{
					GitHub: &v1alpha1.GitHub{
						AppID:  1,
						Domain: domain,
					},
				},
			}
			secret := corev1.Secret{
				Data: map[string][]byte{
					githubAppPrivateKeySecretKey: privateKeyPEM,
				},
			}

			var wg sync.WaitGroup
			for i := 0; i < numGoroutines; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, _, _ = GetClient(context.Background(), scmProvider, secret, installLogin)
				}()
			}
			wg.Wait()

			// Without the fix, every goroutine that discovered the empty cache goes on
			// to acquire the write lock and redundantly calls ListInstallations, so
			// apiCallCount equals numGoroutines.
			// With singleflight, only one goroutine ever makes the network call.
			Expect(atomic.LoadInt64(&apiCallCount)).To(Equal(int64(1)))
		})
	})
})
