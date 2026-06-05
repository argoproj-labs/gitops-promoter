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
	"errors"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/notification/events"
)

// dispatchCall records a single Dispatch invocation for assertions.
type dispatchCall struct {
	notification string
	eventType    events.EventType
}

// captureDispatcher records the (notification, event) pairs Dispatch is called with.
type captureDispatcher struct {
	calls []dispatchCall
	mu    sync.Mutex
}

func (c *captureDispatcher) Dispatch(_ context.Context, n *promoterv1alpha1.PromoterNotification, e events.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, dispatchCall{notification: n.Namespace + "/" + n.Name, eventType: e.Type})
	return nil
}

func (c *captureDispatcher) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

// notif builds a PromoterNotification named "n1" in namespace ns. The name is fixed because
// every test uses a single notification per reconciler; only the namespace, selector, and
// subscribed event types vary across cases.
func notif(ns string, selector *metav1.LabelSelector, types ...promoterv1alpha1.NotificationEventType) *promoterv1alpha1.PromoterNotification {
	return &promoterv1alpha1.PromoterNotification{
		ObjectMeta: metav1.ObjectMeta{Name: "n1", Namespace: ns},
		Spec: promoterv1alpha1.PromoterNotificationSpec{
			EventTypes: types,
			Selector:   selector,
			Delivery: promoterv1alpha1.NotificationDelivery{
				Webhook: &promoterv1alpha1.WebhookDelivery{URL: "https://example.test/hook"},
			},
		},
	}
}

// ctpEvent builds a CTPActive event originating in testNamespace with the given labels. The
// originating namespace is fixed; namespace-scoping behavior is exercised by varying the
// notification's namespace (see TestHandleEvent_NamespaceScoped), not the event's.
func ctpEvent(labels map[string]string) events.Event {
	return events.Event{
		Type: events.TypeCTPActive,
		Object: events.ObjectRef{
			APIVersion: testAPIVersion,
			Kind:       testKindCTP,
			Namespace:  testNamespace,
			Name:       "ctp-1",
		},
		Labels: labels,
		NewSha: "deadbeef",
	}
}

// drainOnce pulls one queued work item and dispatches it, asserting a single item is present.
func drainOnce(t *testing.T, r *PromoterNotificationReconciler) {
	t.Helper()
	select {
	case item := <-r.workQueue:
		r.dispatch(context.Background(), item)
	default:
		t.Fatal("expected a queued work item but the queue was empty")
	}
}

func newReconciler(t *testing.T, d Dispatcher, objs ...*promoterv1alpha1.PromoterNotification) *PromoterNotificationReconciler {
	t.Helper()
	scheme := newScheme(t)
	builder := fake.NewClientBuilder().WithScheme(scheme)
	for _, o := range objs {
		builder = builder.WithObjects(o)
	}
	return &PromoterNotificationReconciler{
		Client:     builder.Build(),
		Scheme:     scheme,
		Subscriber: events.NewMemoryBroker(),
		Dispatcher: d,
		workQueue:  make(chan work, 16),
	}
}

func TestHandleEvent_NoOpWhenZeroNotifications(t *testing.T) {
	t.Parallel()
	d := &captureDispatcher{}
	r := newReconciler(t, d) // no PromoterNotifications exist

	if err := r.handleEvent(context.Background(), ctpEvent(nil)); err != nil {
		t.Fatalf("handleEvent returned error: %v", err)
	}
	if len(r.workQueue) != 0 {
		t.Fatalf("expected nothing enqueued with zero notifications, got %d", len(r.workQueue))
	}
	if d.count() != 0 {
		t.Fatalf("expected zero dispatches, got %d", d.count())
	}
}

func TestHandleEvent_EnqueuesMatchingNotification(t *testing.T) {
	t.Parallel()
	d := &captureDispatcher{}
	r := newReconciler(t, d, notif(testNamespace, nil, promoterv1alpha1.NotificationEventCTPActive))

	if err := r.handleEvent(context.Background(), ctpEvent(nil)); err != nil {
		t.Fatalf("handleEvent returned error: %v", err)
	}
	if got := len(r.workQueue); got != 1 {
		t.Fatalf("expected 1 enqueued item, got %d", got)
	}
	drainOnce(t, r)
	if d.count() != 1 {
		t.Fatalf("expected 1 dispatch, got %d", d.count())
	}
}

func TestHandleEvent_SkipsNonMatchingEventType(t *testing.T) {
	t.Parallel()
	d := &captureDispatcher{}
	// Subscribes only to PromotionComplete; the event is CTPActive.
	r := newReconciler(t, d, notif(testNamespace, nil, promoterv1alpha1.NotificationEventPromotionComplete))

	if err := r.handleEvent(context.Background(), ctpEvent(nil)); err != nil {
		t.Fatalf("handleEvent returned error: %v", err)
	}
	if got := len(r.workQueue); got != 0 {
		t.Fatalf("expected nothing enqueued for non-matching event type, got %d", got)
	}
}

func TestHandleEvent_NamespaceScoped(t *testing.T) {
	t.Parallel()
	d := &captureDispatcher{}
	// Notification in ns-b must not match an event from testNamespace (ns-a).
	r := newReconciler(t, d, notif("ns-b", nil, promoterv1alpha1.NotificationEventCTPActive))

	if err := r.handleEvent(context.Background(), ctpEvent(nil)); err != nil {
		t.Fatalf("handleEvent returned error: %v", err)
	}
	if got := len(r.workQueue); got != 0 {
		t.Fatalf("expected nothing enqueued across namespaces, got %d", got)
	}
}

func TestHandleEvent_SelectorNegativePathDeliversNothing(t *testing.T) {
	t.Parallel()
	d := &captureDispatcher{}
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{testTeamLabelKey: testTeamLabelValue}}
	r := newReconciler(t, d, notif(testNamespace, sel, promoterv1alpha1.NotificationEventCTPActive))

	// Event labels do not satisfy the selector.
	if err := r.handleEvent(context.Background(), ctpEvent(map[string]string{testTeamLabelKey: "search"})); err != nil {
		t.Fatalf("handleEvent returned error: %v", err)
	}
	if got := len(r.workQueue); got != 0 {
		t.Fatalf("selector non-match must deliver nothing, got %d enqueued", got)
	}
}

// When the work queue stays full past EnqueueTimeout, handleEvent skips that delivery
// best-effort: it does NOT return an error (a publisher's reconcile must not fail for
// transport reasons), nothing extra is enqueued beyond the pre-existing saturating item,
// and it returns promptly rather than blocking indefinitely.
func TestHandleEvent_EnqueueTimeoutWhenQueueFull(t *testing.T) {
	t.Parallel()
	d := &captureDispatcher{}
	r := newReconciler(t, d, notif(testNamespace, nil, promoterv1alpha1.NotificationEventCTPActive))
	r.workQueue = make(chan work, 1)
	r.EnqueueTimeout = 20 * time.Millisecond
	r.workQueue <- work{} // saturate the buffer

	start := time.Now()
	err := r.handleEvent(context.Background(), ctpEvent(nil))
	if err != nil {
		t.Fatalf("handleEvent must not return an error on a full queue (best-effort skip); got: %v", err)
	}
	// The buffer still holds only the saturating item; the matched delivery was skipped.
	if got := len(r.workQueue); got != 1 {
		t.Fatalf("expected queue to still hold only the saturating item (1), got %d", got)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("enqueue timeout took too long: %v", elapsed)
	}
}

// When the PARENT context is cancelled (e.g. controller shutdown) while the queue is full,
// handleEvent stops and returns the context error rather than skipping.
func TestHandleEvent_ParentContextCancelledWhenQueueFull(t *testing.T) {
	t.Parallel()
	d := &captureDispatcher{}
	r := newReconciler(t, d, notif(testNamespace, nil, promoterv1alpha1.NotificationEventCTPActive))
	r.workQueue = make(chan work, 1)
	r.EnqueueTimeout = 5 * time.Second // long, so the parent cancellation wins the race
	r.workQueue <- work{}              // saturate the buffer

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	err := r.handleEvent(ctx, ctpEvent(nil))
	if err == nil {
		t.Fatal("expected error when parent context is cancelled")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestHandleEvent_SelectorPositiveMatch(t *testing.T) {
	t.Parallel()
	d := &captureDispatcher{}
	sel := &metav1.LabelSelector{MatchLabels: map[string]string{testTeamLabelKey: testTeamLabelValue}}
	r := newReconciler(t, d, notif(testNamespace, sel, promoterv1alpha1.NotificationEventCTPActive))

	if err := r.handleEvent(context.Background(), ctpEvent(map[string]string{testTeamLabelKey: testTeamLabelValue})); err != nil {
		t.Fatalf("handleEvent returned error: %v", err)
	}
	if got := len(r.workQueue); got != 1 {
		t.Fatalf("expected 1 enqueued for matching selector, got %d", got)
	}
}
