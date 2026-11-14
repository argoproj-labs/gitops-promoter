package webhookreceiver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("DetectProvider", func() {
	var wr *WebhookReceiver

	BeforeEach(func() {
		wr = &WebhookReceiver{}
	})

	tests := map[string]struct {
		headers        map[string]string
		expectedResult string
	}{
		"GitHub webhook with X-GitHub-Event": {
			headers: map[string]string{
				"X-GitHub-Event": "push",
			},
			expectedResult: ProviderGitHub,
		},
		"GitHub webhook with X-GitHub-Delivery": {
			headers: map[string]string{
				"X-GitHub-Delivery": "12345",
			},
			expectedResult: ProviderGitHub,
		},
		"GitLab webhook with X-Gitlab-Event": {
			headers: map[string]string{
				"X-Gitlab-Event": "Push Hook",
			},
			expectedResult: ProviderGitLab,
		},
		"GitLab webhook with X-Gitlab-Token": {
			headers: map[string]string{
				"X-Gitlab-Token": "secret",
			},
			expectedResult: ProviderGitLab,
		},
		"Forgejo webhook with X-Forgejo-Event": {
			headers: map[string]string{
				"X-Forgejo-Event": "push",
			},
			expectedResult: ProviderForgejo,
		},
		"Gitea webhook with X-Gitea-Event": {
			headers: map[string]string{
				"X-Gitea-Event": "push",
			},
			expectedResult: ProviderGitea,
		},
		"Bitbucket Cloud webhook with X-Hook-UUID": {
			headers: map[string]string{
				"X-Hook-UUID": "12345-abcde",
			},
			expectedResult: ProviderBitbucketCloud,
		},
		"Bitbucket DataCenter webhook with X-Event-Key": {
			headers: map[string]string{
				"X-Event-Key":  "repo:refs_changed",
				"X-Request-Id": "abcde-12345",
			},
			expectedResult: ProviderBitbucketDataCenter,
		},
		"Unknown provider - no headers": {
			headers:        map[string]string{},
			expectedResult: ProviderUnknown,
		},
		"Unknown provider - wrong headers": {
			headers: map[string]string{
				"X-Custom-Header": "value",
			},
			expectedResult: ProviderUnknown,
		},
	}

	for name, test := range tests {
		It(name, func() {
			req, err := http.NewRequest(http.MethodPost, "/", nil)
			Expect(err).NotTo(HaveOccurred())

			for key, value := range test.headers {
				req.Header.Set(key, value)
			}

			result := wr.DetectProvider(req)
			Expect(result).To(Equal(test.expectedResult))
		})
	}

	It("should detect GitHub first when multiple provider headers are present", func() {
		req, err := http.NewRequest(http.MethodPost, "/", nil)
		Expect(err).NotTo(HaveOccurred())
		req.Header.Set("X-Github-Event", "push")
		req.Header.Set("X-Gitlab-Event", "Push Hook")

		result := wr.DetectProvider(req)
		Expect(result).To(Equal(ProviderGitHub))
	})
})

const (
	proposedHydratedShaField = ".status.proposed.hydrated.sha"
	activeHydratedShaField   = ".status.active.hydrated.sha"
	testNamespace            = "default"
	testProposedRef          = "refs/heads/environment/development-next"
	testShaA                 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testShaB                 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func githubPushPayloadForRef(beforeSha, ref string) []byte {
	payload := map[string]any{
		"before": beforeSha,
		"ref":    ref,
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
		Name:      name,
		Namespace: testNamespace,
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
		}).
		WithIndex(&promoterv1alpha1.ValidatedCommitStatus{}, constants.ValidatedCommitStatusRefShaField, func(obj client.Object) []string {
			gate, ok := obj.(*promoterv1alpha1.ValidatedCommitStatus)
			if !ok {
				return nil
			}
			var shas []string
			if gate.Status.DryBranch != nil {
				shas = append(shas, gate.Status.DryBranch.Sha)
			}
			for _, environment := range gate.Status.Environments {
				if environment.Tag != nil {
					shas = append(shas, environment.Tag.Sha)
				}
			}
			return shas
		})
	return builder.Build()
}

// newGate returns a ValidatedCommitStatus that last saw drySha at the head of its dry branch and
// tagSha on its one environment tag.
func newGate(name, drySha, tagSha string) *promoterv1alpha1.ValidatedCommitStatus {
	return &promoterv1alpha1.ValidatedCommitStatus{
		Name:      name,
		Namespace: testNamespace,
		Status: promoterv1alpha1.ValidatedCommitStatusStatus{
			DryBranch: &promoterv1alpha1.ValidatedCommitStatusDryBranchStatus{Sha: drySha},
			Environments: []promoterv1alpha1.ValidatedCommitStatusEnvironmentStatus{{
				Branch: "environment/development",
				Tag:    &promoterv1alpha1.ValidatedCommitStatusTagStatus{Name: "environment/development-tag", Sha: tagSha},
			}},
		},
	}
}

func postGitHubPush(wr *WebhookReceiver, beforeSha string) *httptest.ResponseRecorder {
	return postGitHubPushForRef(wr, beforeSha, testProposedRef)
}

func postGitHubPushForRef(wr *WebhookReceiver, beforeSha, ref string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", bytes.NewReader(githubPushPayloadForRef(beforeSha, ref)))
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

func (e *enqueueRecorder) names() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	names := make([]string, 0, len(e.calls))
	for _, call := range e.calls {
		names = append(names, call[1])
	}
	return names
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

// flakyListClient fails List a fixed number of times, then delegates to the wrapped client.
type flakyListClient struct {
	client.Client
	mu           sync.Mutex
	failuresLeft int
}

func (f *flakyListClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failuresLeft > 0 {
		f.failuresLeft--
		return errors.New("simulated list failure")
	}
	if err := f.Client.List(ctx, list, opts...); err != nil {
		return fmt.Errorf("list changetransferpolicies: %w", err)
	}
	return nil
}

// listRecordingClient records the type of every list it is asked for.
type listRecordingClient struct {
	client.Client
	listed []string
}

func (l *listRecordingClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	l.listed = append(l.listed, fmt.Sprintf("%T", list))
	if err := l.Client.List(ctx, list, opts...); err != nil {
		return fmt.Errorf("list: %w", err)
	}
	return nil
}

type testLifecycle struct {
	ctx  context.Context //nolint:containedctx // stands in for the server-lifetime context passed to Start
	stop func()
}

func newTestLifecycle() testLifecycle {
	ctx, cancel := context.WithCancel(context.Background())
	return testLifecycle{
		ctx:  ctx,
		stop: cancel,
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
		// Allow in-flight retries to release the pending counter/gauge after cancel.
		Eventually(func() float64 {
			return testutil.ToFloat64(metrics.WebhookMissRetryPending)
		}, 2*time.Second, 10*time.Millisecond).Should(Equal(0.0))
	})

	It("enqueues immediately when a CTP matches on the first lookup", func() {
		ctp := newCTP("ctp-match")
		enqueues := &enqueueRecorder{}
		wr := &WebhookReceiver{
			baseCtx:    lc.ctx,
			k8sClient:  newFakeClient(ctp),
			enqueueCTP: enqueues.enqueue,
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
			baseCtx:        lc.ctx,
			k8sClient:      cl,
			enqueueCTP:     enqueues.enqueue,
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
			baseCtx:        lc.ctx,
			k8sClient:      newFakeClient(),
			enqueueCTP:     enqueues.enqueue,
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
			baseCtx:        lc.ctx,
			k8sClient:      newFakeClient(ctp1, ctp2),
			enqueueCTP:     enqueues.enqueue,
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

	It("retries after transient list failures and enqueues when lookup succeeds", func() {
		ctp := newCTP("ctp-flaky")
		enqueues := &enqueueRecorder{}
		// Fail the sync lookup and the first deferred attempt, then succeed.
		flaky := &flakyListClient{Client: newFakeClient(ctp), failuresLeft: 2}
		wr := &WebhookReceiver{
			baseCtx:        lc.ctx,
			k8sClient:      flaky,
			enqueueCTP:     enqueues.enqueue,
			retryTimeout:   2 * time.Second,
			retryBaseDelay: 20 * time.Millisecond,
			retryMaxDelay:  50 * time.Millisecond,
			retryFactor:    2.0,
		}

		rec := postGitHubPush(wr, testShaA)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(enqueues.count()).To(Equal(0))

		Eventually(func() int {
			return enqueues.count()
		}, 2*time.Second, 20*time.Millisecond).Should(Equal(1))
		ns, name := enqueues.last()
		Expect(ns).To(Equal(testNamespace))
		Expect(name).To(Equal("ctp-flaky"))

		Eventually(func() float64 {
			return testutil.ToFloat64(metrics.WebhookMissRetryPending)
		}, time.Second, 10*time.Millisecond).Should(Equal(0.0))
	})

	It("returns 204 and drops additional retries when pending miss retries are at capacity", func() {
		enqueues := &enqueueRecorder{}
		// Client that never matches so retries stay in flight until cancelled/timeout.
		wr := &WebhookReceiver{
			baseCtx:           lc.ctx,
			k8sClient:         newFakeClient(),
			enqueueCTP:        enqueues.enqueue,
			maxPendingRetries: 1,
			retryTimeout:      5 * time.Second,
			retryBaseDelay:    50 * time.Millisecond,
			retryMaxDelay:     100 * time.Millisecond,
			retryFactor:       2.0,
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

var _ = Describe("WebhookReceiver pushes to a dry branch or an environment tag", func() {
	const (
		dryRef = "refs/heads/main"
		tagRef = "refs/tags/environment/development-tag"
	)

	var lc testLifecycle

	BeforeEach(func() {
		lc = newTestLifecycle()
		metrics.WebhookMissRetryPending.Set(0)
	})

	AfterEach(func() {
		lc.stop()
		Eventually(func() float64 {
			return testutil.ToFloat64(metrics.WebhookMissRetryPending)
		}, 2*time.Second, 10*time.Millisecond).Should(Equal(0.0))
	})

	newReceiver := func(cl client.Client, ctps, gates *enqueueRecorder) *WebhookReceiver {
		wr := &WebhookReceiver{
			baseCtx:        lc.ctx,
			k8sClient:      cl,
			enqueueCTP:     ctps.enqueue,
			retryTimeout:   150 * time.Millisecond,
			retryBaseDelay: 20 * time.Millisecond,
			retryMaxDelay:  40 * time.Millisecond,
			retryFactor:    2.0,
		}
		if gates != nil {
			WithValidatedCommitStatusEnqueue(gates.enqueue)(wr)
		}
		return wr
	}

	It("enqueues the gate that last saw the pushed dry branch at the pre-push commit", func() {
		ctps, gates := &enqueueRecorder{}, &enqueueRecorder{}
		wr := newReceiver(newFakeClient(newGate("gate", testShaA, testShaB)), ctps, gates)

		rec := postGitHubPushForRef(wr, testShaA, dryRef)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(gates.names()).To(Equal([]string{"gate"}))
		Expect(ctps.count()).To(Equal(0))
		Expect(testutil.ToFloat64(metrics.WebhookMissRetryPending)).To(Equal(0.0), "a match needs no retry")
	})

	It("enqueues the gate whose environment tag was moved by hand", func() {
		ctps, gates := &enqueueRecorder{}, &enqueueRecorder{}
		wr := newReceiver(newFakeClient(newGate("gate", testShaA, testShaB)), ctps, gates)

		rec := postGitHubPushForRef(wr, testShaB, tagRef)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(gates.names()).To(Equal([]string{"gate"}))
	})

	It("enqueues every gate that follows the pushed dry branch", func() {
		ctps, gates := &enqueueRecorder{}, &enqueueRecorder{}
		cl := newFakeClient(
			newGate("gate-one", testShaA, testShaB),
			newGate("gate-two", testShaA, testShaA),
			newGate("gate-elsewhere", testShaB, testShaB),
		)
		wr := newReceiver(cl, ctps, gates)

		rec := postGitHubPushForRef(wr, testShaA, dryRef)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(gates.names()).To(ConsistOf("gate-one", "gate-two"))
	})

	It("still enqueues the ChangeTransferPolicy for a push to a hydrated branch", func() {
		ctps, gates := &enqueueRecorder{}, &enqueueRecorder{}
		wr := newReceiver(newFakeClient(newCTP("ctp-match"), newGate("gate", testShaB, testShaB)), ctps, gates)

		rec := postGitHubPush(wr, testShaA)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(ctps.names()).To(Equal([]string{"ctp-match"}))
		Expect(gates.count()).To(Equal(0))
	})

	It("does not retry a tag push that nothing reads", func() {
		ctps, gates := &enqueueRecorder{}, &enqueueRecorder{}
		wr := newReceiver(newFakeClient(newGate("gate", testShaB, testShaB)), ctps, gates)

		rec := postGitHubPushForRef(wr, testShaA, "refs/tags/v1.2.3")
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(testutil.ToFloat64(metrics.WebhookMissRetryPending)).To(Equal(0.0))
		Expect(gates.count()).To(Equal(0))
		Expect(ctps.count()).To(Equal(0))
	})

	It("takes a tag push that nothing reads as none of its business, and not as a delivery it gave up on", func() {
		ctps, gates := &enqueueRecorder{}, &enqueueRecorder{}
		wr := newReceiver(newFakeClient(newGate("gate", testShaB, testShaB)), ctps, gates)

		result, err := wr.tryLookupAndEnqueue(context.Background(), testShaA, "refs/tags/v1.2.3", "test")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.outcome(err)).To(Equal(deliveryIgnored))

		By("Still giving up, and saying so, when a lookup cannot tell which ChangeTransferPolicy was pushed")
		ambiguous := newCTP("ctp-one")
		twin := newCTP("ctp-two")
		wr = newReceiver(newFakeClient(ambiguous, twin), ctps, gates)
		result, err = wr.tryLookupAndEnqueue(context.Background(), testShaA, dryRef, "test")
		Expect(err).To(HaveOccurred())
		Expect(result.outcome(err)).To(Equal(deliveryFailed))
	})

	It("stops retrying a tag push once a lookup that works finds nothing that reads it", func() {
		ctps, gates := &enqueueRecorder{}, &enqueueRecorder{}
		// The lookup of gates fails once, which is worth a retry. The retry finds that nothing reads the tag.
		flaky := &flakyListClient{Client: newFakeClient(newGate("gate", testShaB, testShaB)), failuresLeft: 1}
		wr := newReceiver(flaky, ctps, gates)
		wr.retryTimeout = 30 * time.Second

		rec := postGitHubPushForRef(wr, testShaA, "refs/tags/v1.2.3")
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Eventually(func() float64 {
			return testutil.ToFloat64(metrics.WebhookMissRetryPending)
		}, 2*time.Second, 10*time.Millisecond).Should(Equal(0.0), "the retry must not wait out its timeout")
		Expect(gates.count()).To(Equal(0))
		Expect(ctps.count()).To(Equal(0))
	})

	It("retries a branch push that nothing reads yet, and enqueues the gate once its status shows the commit", func() {
		ctps, gates := &enqueueRecorder{}, &enqueueRecorder{}
		cl := newFakeClient()
		wr := newReceiver(cl, ctps, gates)
		wr.retryTimeout = 2 * time.Second

		rec := postGitHubPushForRef(wr, testShaA, dryRef)
		Expect(rec.Code).To(Equal(http.StatusNoContent))
		Expect(gates.count()).To(Equal(0))

		Expect(cl.Create(context.Background(), newGate("gate-late", testShaA, testShaB))).To(Succeed())
		Eventually(gates.names, 2*time.Second, 20*time.Millisecond).Should(Equal([]string{"gate-late"}))
	})

	It("does not look for gates unless it was given a way to enqueue them", func() {
		ctps := &enqueueRecorder{}
		recording := &listRecordingClient{Client: newFakeClient(newCTP("ctp-match"), newGate("gate", testShaA, testShaB))}
		wr := newReceiver(recording, ctps, nil)

		result, err := wr.tryLookupAndEnqueue(context.Background(), testShaA, dryRef, "test")
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(lookupResult{ctp: true}))
		Expect(ctps.names()).To(Equal([]string{"ctp-match"}))
		Expect(recording.listed).To(Equal([]string{"*v1alpha1.ChangeTransferPolicyList"}))
	})
})

var _ = Describe("parseWebhookPush for Bitbucket Cloud", func() {
	payload := func(change map[string]any) []byte {
		b, err := json.Marshal(map[string]any{
			"actor": map[string]any{"display_name": "test-user"},
			"push":  map[string]any{"changes": []map[string]any{change}},
		})
		Expect(err).NotTo(HaveOccurred())
		return b
	}
	ref := func(refType, name, hash string) map[string]any {
		return map[string]any{"type": refType, "name": name, "target": map[string]any{"hash": hash}}
	}

	It("reads a branch push", func() {
		sha, pushed := parseWebhookPush(ProviderBitbucketCloud, payload(map[string]any{
			"old": ref("branch", "environment/development-next", testShaA),
			"new": ref("branch", "environment/development-next", testShaB),
		}))
		Expect(sha).To(Equal(testShaA))
		Expect(pushed).To(Equal("refs/heads/environment/development-next"))
	})

	It("reads a tag push as a tag", func() {
		sha, pushed := parseWebhookPush(ProviderBitbucketCloud, payload(map[string]any{
			"old": ref("tag", "environment/development-tag", testShaA),
			"new": ref("tag", "environment/development-tag", testShaB),
		}))
		Expect(sha).To(Equal(testShaA))
		Expect(pushed).To(Equal("refs/tags/environment/development-tag"))
	})

	It("names a deleted tag from what it was", func() {
		sha, pushed := parseWebhookPush(ProviderBitbucketCloud, payload(map[string]any{
			"old": ref("tag", "environment/development-tag", testShaA),
			"new": nil,
		}))
		Expect(sha).To(Equal(testShaA))
		Expect(pushed).To(Equal("refs/tags/environment/development-tag"))
	})

	It("names a created branch from what it is", func() {
		sha, pushed := parseWebhookPush(ProviderBitbucketCloud, payload(map[string]any{
			"old": nil,
			"new": ref("branch", "feature", testShaB),
		}))
		Expect(sha).To(BeEmpty())
		Expect(pushed).To(Equal("refs/heads/feature"))
	})
})
