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

// Package notification implements the controller that watches PromoterNotification custom
// resources and bridges the in-process notification event stream (events.Broker) into
// controller-runtime.
//
// Responsibilities (Phase 2a):
//   - Subscribe to the events.Broker. For each published event, match it against every
//     PromoterNotification in the event's namespace using internal/notification/matching, and
//     enqueue the (notification, event) pairs that match onto an internal work queue.
//   - Drain that queue with a small worker pool, calling Dispatcher.Dispatch for each pair.
//     Actual send/retry/status lives behind the Dispatcher (Phase 2b).
//   - Reconcile keeps the PromoterNotification's status.observedGeneration current; delivery
//     status fields are owned by the Dispatcher implementation.
//
// When zero PromoterNotification resources exist, an incoming event matches nothing and is a
// no-op: nothing is enqueued, nothing is delivered, and no error is produced.
package notification

import (
	"context"
	stderrors "errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	runtimeevent "sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	notifevents "github.com/argoproj-labs/gitops-promoter/internal/notification/events"
	"github.com/argoproj-labs/gitops-promoter/internal/notification/matching"
)

const (
	// defaultWorkerCount is the number of concurrent dispatch workers.
	defaultWorkerCount = 4
	// defaultWorkQueueSize is the buffer of the internal (notification,event) work queue.
	// Matches the CTP controller's external-enqueue channel buffer for consistency.
	defaultWorkQueueSize = 1024
	// defaultEnqueueTimeout bounds how long handleEvent waits for space on a full work queue.
	// Publish runs handlers synchronously, so an unbounded block here would stall publisher
	// reconciles; the broker logs handler errors without failing the publisher.
	defaultEnqueueTimeout = 30 * time.Second
)

// work is a single unit of delivery: one event matched against one PromoterNotification.
type work struct {
	event        notifevents.Event
	notification promoterv1alpha1.PromoterNotification
}

// PromoterNotificationReconciler reconciles a PromoterNotification object and bridges the
// in-process event stream into delivery.
type PromoterNotificationReconciler struct {
	client.Client
	Recorder       events.EventRecorder
	Subscriber     notifevents.Subscriber
	Dispatcher     Dispatcher
	Scheme         *runtime.Scheme
	workQueue      chan work
	WorkerCount    int
	EnqueueTimeout time.Duration
}

// NewPromoterNotificationReconciler constructs a reconciler with the required dependencies
// injected. Tests (and main.go) can use this to wire a real *events.MemoryBroker as the
// Subscriber and any Dispatcher implementation. Callers may still set optional fields
// (WorkerCount) on the returned value before calling SetupWithManager.
func NewPromoterNotificationReconciler(c client.Client, scheme *runtime.Scheme, recorder events.EventRecorder, subscriber notifevents.Subscriber, dispatcher Dispatcher) *PromoterNotificationReconciler {
	return &PromoterNotificationReconciler{
		Client:     c,
		Scheme:     scheme,
		Recorder:   recorder,
		Subscriber: subscriber,
		Dispatcher: dispatcher,
	}
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=promoternotifications,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=promoternotifications/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=promoternotifications/finalizers,verbs=update

// Reconcile keeps the PromoterNotification's observed generation current. Delivery is driven by
// the event stream (see handleEvent), not by reconciles; delivery status fields on the CR are
// owned by the Dispatcher implementation, so this reconcile is intentionally lightweight.
func (r *PromoterNotificationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pn promoterv1alpha1.PromoterNotification
	if err := r.Get(ctx, req.NamespacedName, &pn); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PromoterNotification: %w", err)
	}

	if pn.Status.ObservedGeneration != pn.Generation {
		pn.Status.ObservedGeneration = pn.Generation
		if err := r.Status().Update(ctx, &pn); err != nil {
			if errors.IsConflict(err) {
				// Another writer (e.g. the Dispatcher updating delivery status) won the race;
				// requeue to observe the latest generation.
				return ctrl.Result{Requeue: true}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to update PromoterNotification status: %w", err)
		}
		logger.V(4).Info("Updated PromoterNotification observedGeneration", "generation", pn.Generation)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager wires the controller: it subscribes the event handler to the broker, starts
// the worker pool as a manager runnable, and registers the controller to watch
// PromoterNotification resources. The event stream is bridged into controller-runtime using the
// CTP controller's pattern (a buffered chan event.GenericEvent + WatchesRawSource(source.Channel))
// so the controller's lifecycle (and the worker pool) is owned by the manager.
func (r *PromoterNotificationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Subscriber == nil {
		return stderrors.New("PromoterNotificationReconciler requires a non-nil Subscriber")
	}
	if r.Dispatcher == nil {
		return stderrors.New("PromoterNotificationReconciler requires a non-nil Dispatcher")
	}
	if r.WorkerCount <= 0 {
		r.WorkerCount = defaultWorkerCount
	}
	r.workQueue = make(chan work, defaultWorkQueueSize)

	// Subscribe the event handler to the broker. When an event is published the handler matches
	// it against all PromoterNotifications in the event's namespace and enqueues the matches.
	r.Subscriber.Subscribe(notifevents.HandlerFunc(r.handleEvent))

	// Run the dispatch worker pool as a manager runnable so its lifecycle is tied to the manager.
	if err := mgr.Add(&workerPool{reconciler: r}); err != nil {
		return fmt.Errorf("failed to add notification worker pool runnable: %w", err)
	}

	// We watch PromoterNotification objects so Reconcile can keep observedGeneration current.
	// A buffered GenericEvent channel + source.Channel is kept available for parity with the
	// CTP pattern and to allow future external enqueues; it is harmless when unused.
	externalEnqueueChan := make(chan runtimeevent.GenericEvent, defaultWorkQueueSize)

	if err := ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.PromoterNotification{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WatchesRawSource(source.Channel(externalEnqueueChan, &handler.EnqueueRequestForObject{})).
		Complete(r); err != nil {
		return fmt.Errorf("failed to create PromoterNotification controller: %w", err)
	}
	return nil
}

// handleEvent is the events.Handler registered with the broker. It lists all
// PromoterNotifications in the event's namespace, matches each against the event, and enqueues
// the matching (notification, event) pairs for delivery. Enqueue waits are bounded by
// EnqueueTimeout (defaultEnqueueTimeout when unset) so a saturated work queue cannot block a
// publisher's reconcile indefinitely; ctx cancellation also aborts an enqueue wait.
//
// With zero PromoterNotifications in the namespace this is a no-op: the list is empty, nothing
// matches, and nothing is enqueued.
func (r *PromoterNotificationReconciler) handleEvent(ctx context.Context, event notifevents.Event) error {
	logger := log.FromContext(ctx).WithValues(
		"eventType", event.Type, "object", event.Object.String(), "eventID", event.EventID())

	var list promoterv1alpha1.PromoterNotificationList
	if err := r.List(ctx, &list, client.InNamespace(event.Object.Namespace)); err != nil {
		return fmt.Errorf("failed to list PromoterNotifications in namespace %q: %w", event.Object.Namespace, err)
	}

	if len(list.Items) == 0 {
		// No-op: nothing subscribes in this namespace.
		return nil
	}

	enqueueTimeout := r.EnqueueTimeout
	if enqueueTimeout <= 0 {
		enqueueTimeout = defaultEnqueueTimeout
	}

	matched := 0
	skipped := 0
	for i := range list.Items {
		pn := &list.Items[i]
		ok, err := matching.Matches(pn, event)
		if err != nil {
			// A structurally invalid selector is a CR-level problem, not an event problem:
			// log and skip this notification rather than failing the whole fan-out.
			logger.Error(err, "skipping PromoterNotification with invalid selector",
				"notification", pn.Namespace+"/"+pn.Name)
			continue
		}
		if !ok {
			continue
		}

		enqueueCtx, cancel := context.WithTimeout(ctx, enqueueTimeout)
		select {
		case r.workQueue <- work{notification: *pn, event: event}:
			cancel()
			matched++
		case <-enqueueCtx.Done():
			cancel()
			// If the parent context is done (controller shutting down), stop entirely.
			if ctx.Err() != nil {
				return fmt.Errorf("context cancelled while enqueueing notification deliveries: %w", ctx.Err())
			}
			// Otherwise the work queue stayed full for the whole EnqueueTimeout: skip this
			// notification (best-effort delivery) and continue with the remaining matches rather
			// than abandoning the rest of the event. The drop is logged so saturation is visible.
			skipped++
			metrics.RecordPromoterNotificationDropped(pn.Namespace, pn.Name, string(event.Type))
			logger.Error(enqueueCtx.Err(), "work queue full; skipping notification delivery (best-effort)",
				"notification", pn.Namespace+"/"+pn.Name, "eventType", event.Type, "eventID", event.EventID())
			continue
		}
	}

	if matched > 0 || skipped > 0 {
		logger.V(4).Info("Enqueued matching PromoterNotifications for delivery",
			"enqueued", matched, "skipped", skipped)
	}
	return nil
}

// workerPool drains the reconciler's work queue with a fixed number of workers, calling the
// Dispatcher for each matched (notification, event) pair. It implements manager.Runnable so the
// manager starts and stops it with the controller.
type workerPool struct {
	reconciler *PromoterNotificationReconciler
}

// NeedLeaderElection ensures the worker pool only runs on the elected leader, matching the
// rest of the controllers — we don't want every replica delivering the same notification.
func (w *workerPool) NeedLeaderElection() bool { return true }

// Start runs WorkerCount workers until ctx is cancelled, then drains in-flight work briefly.
func (w *workerPool) Start(ctx context.Context) error {
	r := w.reconciler
	logger := log.FromContext(ctx).WithName("notification-worker-pool")
	logger.Info("starting notification dispatch workers", "workers", r.WorkerCount)

	done := make(chan struct{})
	for i := 0; i < r.WorkerCount; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for {
				select {
				case <-ctx.Done():
					return
				case item := <-r.workQueue:
					r.dispatch(ctx, item)
				}
			}
		}()
	}

	<-ctx.Done()
	for i := 0; i < r.WorkerCount; i++ {
		<-done
	}
	logger.Info("notification dispatch workers stopped")
	return nil
}

// dispatch delivers a single work item via the Dispatcher. The controller does not retry; a
// non-nil error is logged here and all retry/backoff/status handling lives in the Dispatcher.
func (r *PromoterNotificationReconciler) dispatch(ctx context.Context, item work) {
	logger := log.FromContext(ctx).WithValues(
		"notification", item.notification.Namespace+"/"+item.notification.Name,
		"eventType", item.event.Type,
		"eventID", item.event.EventID(),
	)

	// Bound a single dispatch so a hung Dispatcher cannot wedge a worker forever. The
	// Dispatcher's own per-attempt timeout/retry still applies within this budget.
	dispatchCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	notification := item.notification
	if err := r.Dispatcher.Dispatch(dispatchCtx, &notification, item.event); err != nil {
		logger.Error(err, "failed to dispatch notification")
		return
	}
	logger.V(4).Info("dispatched notification")
}
