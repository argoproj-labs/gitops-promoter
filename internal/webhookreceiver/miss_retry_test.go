package webhookreceiver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	proposedHydratedShaField = ".status.proposed.hydrated.sha"
	activeHydratedShaField   = ".status.active.hydrated.sha"
	testNamespace            = "default"
	testProposedRef          = "refs/heads/environment/development-next"
	testShaA                 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testShaB                 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func githubPushPayload(beforeSha string) []byte {
	payload := map[string]any{
		"before": beforeSha,
		"ref":    testProposedRef,
		"pusher": map[string]any{
			"name":  "test-user",
			"email": "test@example.com",
		},
	}
	b, err := json.Marshal(payload)
	Expect(err).NotTo(HaveOccurred())
	return b
}

func newCTP(name string) *promoterv1alpha1.ChangeTransferPolicy {
	return &promoterv1alpha1.ChangeTransferPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Status: promoterv1alpha1.ChangeTransferPolicyStatus{
			Proposed: promoterv1alpha1.CommitBranchState{
				Hydrated: promoterv1alpha1.CommitShaState{Sha: testShaA},
			},
			Active: promoterv1alpha1.CommitBranchState{
				Hydrated: promoterv1alpha1.CommitShaState{Sha: testShaA},
			},
		},
	}
}

func newFakeClient(objs ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	Expect(promoterv1alpha1.AddToScheme(scheme)).To(Succeed())
	builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).
		WithIndex(&promoterv1alpha1.ChangeTransferPolicy{}, proposedHydratedShaField, func(obj client.Object) []string {
			ctp, ok := obj.(*promoterv1alpha1.ChangeTransferPolicy)
			if !ok {
				return nil
			}
			return []string{ctp.Status.Proposed.Hydrated.Sha}
		}).
		WithIndex(&promoterv1alpha1.ChangeTransferPolicy{}, activeHydratedShaField, func(obj client.Object) []string {
			ctp, ok := obj.(*promoterv1alpha1.ChangeTransferPolicy)
			if !ok {
				return nil
			}
			return []string{ctp.Status.Active.Hydrated.Sha}
		})
	return builder.Build()
}

func postGitHubPush(wr *WebhookReceiver, beforeSha string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", bytes.NewReader(githubPushPayload(beforeSha)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Github-Event", "push")
	req.Header.Set("X-Github-Delivery", "test-delivery")
	rec := httptest.NewRecorder()
	wr.postRoot(rec, req)
	return rec
}

type enqueueRecorder struct {
	calls [][2]string
	mu    sync.Mutex
}

func (e *enqueueRecorder) enqueue(namespace, name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, [2]string{namespace, name})
}

func (e *enqueueRecorder) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

func (e *enqueueRecorder) last() (namespace, name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.calls) == 0 {
		return "", ""
	}
	c := e.calls[len(e.calls)-1]
	return c[0], c[1]
}

type testLifecycle struct {
	stop     func()
	shutdown <-chan struct{}
}

func newTestLifecycle() testLifecycle {
	ctx, cancel := context.WithCancel(context.Background())
	return testLifecycle{
		stop:     cancel,
		shutdown: ctx.Done(),
	}
}

var _ = Describe("WebhookReceiver miss retry", func() {
	var lc testLifecycle

	BeforeEach(func() {
		lc = newTestLifecycle()
		metrics.WebhookMissRetryPending.Set(0)
	})

	AfterEach(func() {
		lc.stop()
		// Allow in-flight retries to release the semaphore/gauge after cancel.
		Eventually(func() float64 {
			return testutil.ToFloat64(metrics.WebhookMissRetryPending)
		}, 2*time.Second, 10*time.Millisecond).Should(Equal(0.0))
	})

	It("enqueues immediately when a CTP matches on the first lookup", func() {
		ctp := newCTP("ctp-match")
		enqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			shutdown:     lc.shutdown,
			k8sClient:    newFakeClient(ctp),
			enqueueCTP:   enqueues.enqueue,
			missRetrySem: make(chan struct{}, maxPendingMissRetries),
		}

		rec := postGitHubPush(wr, testShaA)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(enqueues.count()).To(Equal(1))
		ns, name := enqueues.last()
		Expect(ns).To(Equal(testNamespace))
		Expect(name).To(Equal("ctp-match"))
		Expect(testutil.ToFloat64(metrics.WebhookMissRetryPending)).To(Equal(0.0))
	})

	It("retries asynchronously and enqueues when a CTP appears after the initial miss", func() {
		enqueues := &enqueueRecorder{}
		cl := newFakeClient()
		wr := &WebhookReceiver{
			shutdown:       lc.shutdown,
			k8sClient:      cl,
			enqueueCTP:     enqueues.enqueue,
			missRetrySem:   make(chan struct{}, maxPendingMissRetries),
			retryTimeout:   2 * time.Second,
			retryBaseDelay: 20 * time.Millisecond,
			retryMaxDelay:  50 * time.Millisecond,
			retryFactor:    2.0,
		}

		rec := postGitHubPush(wr, testShaA)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(enqueues.count()).To(Equal(0))

		Eventually(func() float64 {
			return testutil.ToFloat64(metrics.WebhookMissRetryPending)
		}, time.Second, 10*time.Millisecond).Should(Equal(1.0))

		ctp := newCTP("ctp-late")
		Expect(cl.Create(context.Background(), ctp)).To(Succeed())

		Eventually(func() int {
			return enqueues.count()
		}, 2*time.Second, 20*time.Millisecond).Should(Equal(1))
		ns, name := enqueues.last()
		Expect(ns).To(Equal(testNamespace))
		Expect(name).To(Equal("ctp-late"))

		Eventually(func() float64 {
			return testutil.ToFloat64(metrics.WebhookMissRetryPending)
		}, time.Second, 10*time.Millisecond).Should(Equal(0.0))
	})

	It("does not enqueue when no CTP appears before the retry timeout", func() {
		enqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			shutdown:       lc.shutdown,
			k8sClient:      newFakeClient(),
			enqueueCTP:     enqueues.enqueue,
			missRetrySem:   make(chan struct{}, maxPendingMissRetries),
			retryTimeout:   150 * time.Millisecond,
			retryBaseDelay: 20 * time.Millisecond,
			retryMaxDelay:  40 * time.Millisecond,
			retryFactor:    2.0,
		}

		rec := postGitHubPush(wr, testShaA)
		Expect(rec.Code).To(Equal(http.StatusNoContent))

		Consistently(func() int {
			return enqueues.count()
		}, 250*time.Millisecond, 20*time.Millisecond).Should(Equal(0))

		Eventually(func() float64 {
			return testutil.ToFloat64(metrics.WebhookMissRetryPending)
		}, time.Second, 10*time.Millisecond).Should(Equal(0.0))
	})

	It("does not enqueue or async-retry when multiple CTPs match the same sha", func() {
		ctp1 := newCTP("ctp-one")
		ctp1.Status.Active.Hydrated.Sha = testShaB
		ctp2 := newCTP("ctp-two")
		ctp2.Status.Active.Hydrated.Sha = testShaB
		enqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			shutdown:       lc.shutdown,
			k8sClient:      newFakeClient(ctp1, ctp2),
			enqueueCTP:     enqueues.enqueue,
			missRetrySem:   make(chan struct{}, maxPendingMissRetries),
			retryTimeout:   200 * time.Millisecond,
			retryBaseDelay: 20 * time.Millisecond,
			retryMaxDelay:  40 * time.Millisecond,
			retryFactor:    2.0,
		}

		rec := postGitHubPush(wr, testShaA)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(enqueues.count()).To(Equal(0))
		Expect(testutil.ToFloat64(metrics.WebhookMissRetryPending)).To(Equal(0.0))

		Consistently(func() int {
			return enqueues.count()
		}, 150*time.Millisecond, 20*time.Millisecond).Should(Equal(0))
	})

	It("returns 204 and drops additional retries when the miss-retry semaphore is full", func() {
		enqueues := &enqueueRecorder{}
		// Client that never matches so retries stay in flight until cancelled/timeout.
		wr := &WebhookReceiver{
			shutdown:       lc.shutdown,
			k8sClient:      newFakeClient(),
			enqueueCTP:     enqueues.enqueue,
			missRetrySem:   make(chan struct{}, 1),
			retryTimeout:   5 * time.Second,
			retryBaseDelay: 50 * time.Millisecond,
			retryMaxDelay:  100 * time.Millisecond,
			retryFactor:    2.0,
		}

		rec1 := postGitHubPush(wr, testShaA)
		Expect(rec1.Code).To(Equal(http.StatusNoContent))

		Eventually(func() float64 {
			return testutil.ToFloat64(metrics.WebhookMissRetryPending)
		}, time.Second, 10*time.Millisecond).Should(Equal(1.0))

		rec2 := postGitHubPush(wr, testShaB)
		Expect(rec2.Code).To(Equal(http.StatusNoContent))
		// Still only one in-flight retry; second was dropped.
		Expect(testutil.ToFloat64(metrics.WebhookMissRetryPending)).To(Equal(1.0))
		Expect(enqueues.count()).To(Equal(0))
	})
})
