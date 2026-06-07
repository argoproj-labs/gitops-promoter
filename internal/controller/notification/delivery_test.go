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

package notification

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/notification/events"
	"github.com/argoproj-labs/gitops-promoter/internal/notification/payload"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
)

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := promoterv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add promoter scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add corev1 scheme: %v", err)
	}
	return scheme
}

func newNotification(url string, retry *promoterv1alpha1.WebhookRetry) *promoterv1alpha1.PromoterNotification {
	return &promoterv1alpha1.PromoterNotification{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "notif-a",
			Namespace:  testNamespace,
			Generation: 1,
		},
		Spec: promoterv1alpha1.PromoterNotificationSpec{
			EventTypes: []promoterv1alpha1.NotificationEventType{promoterv1alpha1.NotificationEventPromotionComplete},
			Delivery: promoterv1alpha1.NotificationDelivery{
				Webhook: &promoterv1alpha1.WebhookDelivery{
					URL:            url,
					TimeoutSeconds: 5,
					Retry:          retry,
				},
			},
		},
	}
}

func newEvent() events.Event {
	return events.Event{
		Type: promoterv1alpha1.NotificationEventPromotionComplete,
		Object: events.ObjectRef{
			APIVersion: testAPIVersion,
			Kind:       testKindCTP,
			Namespace:  testNamespace,
			Name:       "ctp-a",
			UID:        "uid-1",
		},
		Environment: "production",
		NewSha:      "deadbeef",
		OccurredAt:  time.Unix(1700000000, 0).UTC(),
	}
}

// newTestDispatcher builds a webhookDispatcher backed by a fake client preloaded with the
// notification, and a no-op timer so retry backoff does not actually sleep.
func newTestDispatcher(t *testing.T, n *promoterv1alpha1.PromoterNotification, extra ...client.Object) (*webhookDispatcher, client.Client) {
	t.Helper()
	scheme := newScheme(t)
	objs := append([]client.Object{n}, extra...)
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&promoterv1alpha1.PromoterNotification{}).
		WithObjects(objs...).
		Build()

	d := &webhookDispatcher{
		client:   cl,
		recorder: nil,
		newTimer: func(time.Duration) <-chan time.Time {
			ch := make(chan time.Time, 1)
			ch <- time.Now()
			return ch
		},
	}
	return d, cl
}

// fetchStatus reloads the notification and returns its status.
func fetchStatus(t *testing.T, cl client.Client, n *promoterv1alpha1.PromoterNotification) promoterv1alpha1.PromoterNotificationStatus {
	t.Helper()
	var got promoterv1alpha1.PromoterNotification
	if err := cl.Get(context.Background(), client.ObjectKeyFromObject(n), &got); err != nil {
		t.Fatalf("failed to get notification: %v", err)
	}
	return got.Status
}

func TestDispatch_SuccessFirstAttempt(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newNotification(srv.URL, &promoterv1alpha1.WebhookRetry{MaxAttempts: 3, Backoff: promoterv1alpha1.BackoffFixed})
	d, cl := newTestDispatcher(t, n)

	if err := d.Dispatch(context.Background(), n, newEvent()); err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 HTTP call, got %d", got)
	}

	st := fetchStatus(t, cl, n)
	if st.TotalDelivered != 1 {
		t.Errorf("TotalDelivered = %d, want 1", st.TotalDelivered)
	}
	if st.TotalFailed != 0 {
		t.Errorf("TotalFailed = %d, want 0", st.TotalFailed)
	}
	if st.LastDelivery == nil || st.LastDelivery.ResponseStatus != 200 {
		t.Errorf("LastDelivery = %+v, want ResponseStatus 200", st.LastDelivery)
	}
	ev := newEvent()
	wantEventID := ev.EventID()
	if st.LastDelivery.EventID != wantEventID {
		t.Errorf("LastDelivery.EventID = %q, want %q", st.LastDelivery.EventID, wantEventID)
	}
	cond := meta.FindStatusCondition(st.Conditions, string(promoterConditions.Ready))
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != string(promoterConditions.DeliveryHealthy) {
		t.Errorf("Ready condition = %+v, want True/DeliveryHealthy", cond)
	}
}

func TestDispatch_RetryThenSuccess(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First call 500, second call 200.
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newNotification(srv.URL, &promoterv1alpha1.WebhookRetry{MaxAttempts: 3, Backoff: promoterv1alpha1.BackoffExponential})
	d, cl := newTestDispatcher(t, n)

	if err := d.Dispatch(context.Background(), n, newEvent()); err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected exactly 2 HTTP calls (500 then 200), got %d", got)
	}

	st := fetchStatus(t, cl, n)
	if st.TotalDelivered != 1 {
		t.Errorf("TotalDelivered = %d, want 1", st.TotalDelivered)
	}
	if st.TotalFailed != 0 {
		t.Errorf("TotalFailed = %d, want 0", st.TotalFailed)
	}
	cond := meta.FindStatusCondition(st.Conditions, string(promoterConditions.Ready))
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Errorf("Ready condition = %+v, want True", cond)
	}
}

func TestDispatch_DeadLetterAfterMaxAttempts(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := newNotification(srv.URL, &promoterv1alpha1.WebhookRetry{MaxAttempts: 3, Backoff: promoterv1alpha1.BackoffExponential})
	d, cl := newTestDispatcher(t, n)

	err := d.Dispatch(context.Background(), n, newEvent())
	if err == nil {
		t.Fatal("expected dead-letter error after exhausting attempts, got nil")
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("expected exactly 3 HTTP calls (maxAttempts), got %d", got)
	}

	st := fetchStatus(t, cl, n)
	if st.TotalDelivered != 0 {
		t.Errorf("TotalDelivered = %d, want 0", st.TotalDelivered)
	}
	if st.TotalFailed != 1 {
		t.Errorf("TotalFailed = %d, want 1", st.TotalFailed)
	}
	if st.LastDelivery == nil || st.LastDelivery.ResponseStatus != 500 {
		t.Errorf("LastDelivery = %+v, want ResponseStatus 500", st.LastDelivery)
	}
	cond := meta.FindStatusCondition(st.Conditions, string(promoterConditions.Ready))
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != string(promoterConditions.DeliveryFailing) {
		t.Errorf("Ready condition = %+v, want False/DeliveryFailing", cond)
	}
}

func TestDispatch_DefaultMaxAttemptsWhenRetryNil(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	// retry == nil → default of 3 attempts.
	n := newNotification(srv.URL, nil)
	d, _ := newTestDispatcher(t, n)

	if err := d.Dispatch(context.Background(), n, newEvent()); err == nil {
		t.Fatal("expected dead-letter error, got nil")
	}
	if got := calls.Load(); got != defaultMaxAttempts {
		t.Fatalf("expected %d HTTP calls (default maxAttempts), got %d", defaultMaxAttempts, got)
	}
}

func TestDispatch_SingleAttemptNoRetry(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := newNotification(srv.URL, &promoterv1alpha1.WebhookRetry{MaxAttempts: 1, Backoff: promoterv1alpha1.BackoffFixed})
	d, _ := newTestDispatcher(t, n)

	if err := d.Dispatch(context.Background(), n, newEvent()); err == nil {
		t.Fatal("expected failure, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected exactly 1 HTTP call when maxAttempts=1, got %d", got)
	}
}

func TestDispatch_HonorsHTTPMethod(t *testing.T) {
	t.Parallel()
	var gotMethod string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		gotMethod = r.Method
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	n := newNotification(srv.URL, &promoterv1alpha1.WebhookRetry{MaxAttempts: 1})
	n.Spec.Delivery.Webhook.Method = promoterv1alpha1.HTTPMethodPut
	d, _ := newTestDispatcher(t, n)

	if err := d.Dispatch(context.Background(), n, newEvent()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotMethod != http.MethodPut {
		t.Errorf("server saw method %q, want PUT", gotMethod)
	}
}

func TestDispatch_MergesHeadersAndContentType(t *testing.T) {
	t.Parallel()
	var (
		mu          sync.Mutex
		contentType string
		staticHdr   string
		sigHdr      string
		eventIDHdr  string
		body        []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		contentType = r.Header.Get("Content-Type")
		staticHdr = r.Header.Get(testCustomHeaderName)
		sigHdr = r.Header.Get(payload.SignatureHeader)
		eventIDHdr = r.Header.Get(EventIDHeader)
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testSigningSecret, Namespace: testNamespace},
		Data:       map[string][]byte{testSigningKey: []byte("topsecret")},
	}
	n := newNotification(srv.URL, &promoterv1alpha1.WebhookRetry{MaxAttempts: 1})
	n.Spec.Delivery.Webhook.Headers = map[string]string{testCustomHeaderName: testCustomHeaderVal}
	n.Spec.Delivery.Webhook.Signing = &promoterv1alpha1.WebhookSigning{
		SecretRef: promoterv1alpha1.SecretKeyReference{Name: testSigningSecret, Key: testSigningKey},
	}
	d, _ := newTestDispatcher(t, n, secret)

	ev := newEvent()
	if err := d.Dispatch(context.Background(), n, ev); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", contentType)
	}
	if staticHdr != testCustomHeaderVal {
		t.Errorf("X-Custom = %q, want abc", staticHdr)
	}
	// Signature must match the signature over the exact body the server received.
	wantSig := payload.ComputeSignature([]byte("topsecret"), body)
	if sigHdr != wantSig {
		t.Errorf("signature header = %q, want %q", sigHdr, wantSig)
	}
	// The event-ID header must be present on the wire for receiver-side dedup.
	if eventIDHdr != ev.EventID() {
		t.Errorf("%s header = %q, want %q", EventIDHeader, eventIDHdr, ev.EventID())
	}
}

func TestDispatch_PayloadBuildFailureDeadLetters(t *testing.T) {
	t.Parallel()
	// Signing is configured but the referenced secret does not exist, so BuildRequest fails.
	n := newNotification("http://127.0.0.1:0/never", &promoterv1alpha1.WebhookRetry{MaxAttempts: 3})
	n.Spec.Delivery.Webhook.Signing = &promoterv1alpha1.WebhookSigning{
		SecretRef: promoterv1alpha1.SecretKeyReference{Name: "absent", Key: testSigningKey},
	}
	d, cl := newTestDispatcher(t, n)

	if err := d.Dispatch(context.Background(), n, newEvent()); err == nil {
		t.Fatal("expected error when payload build fails, got nil")
	}

	st := fetchStatus(t, cl, n)
	if st.TotalFailed != 1 {
		t.Errorf("TotalFailed = %d, want 1", st.TotalFailed)
	}
	cond := meta.FindStatusCondition(st.Conditions, string(promoterConditions.Ready))
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Errorf("Ready condition = %+v, want False", cond)
	}
	// The condition message must signal a configuration error (not a transient failure) and
	// must not claim a misleading "after 0 attempt(s)".
	if cond != nil {
		if !strings.Contains(cond.Message, "configuration error") {
			t.Errorf("condition message = %q, want it to mention 'configuration error'", cond.Message)
		}
		if strings.Contains(cond.Message, "attempt(s)") {
			t.Errorf("condition message = %q, should not mention attempts for a build failure", cond.Message)
		}
	}
}

func TestDispatch_ContextCancellationDeadLetters(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	n := newNotification(srv.URL, &promoterv1alpha1.WebhookRetry{MaxAttempts: 5})
	d, _ := newTestDispatcher(t, n)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before dispatch

	if err := d.Dispatch(ctx, n, newEvent()); err == nil {
		t.Fatal("expected error with cancelled context, got nil")
	}
}
