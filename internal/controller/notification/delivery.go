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
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sevents "k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/notification/events"
	"github.com/argoproj-labs/gitops-promoter/internal/notification/payload"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// EventIDHeader carries the stable event ID on every webhook request so receivers can
// de-duplicate redelivered events (the same logical event always produces the same ID).
// It is delivered at-least-once, so receivers SHOULD treat repeated IDs as idempotent.
//
// The value is in Go's canonical HTTP header form (X-Promoter-Event-Id); header names are
// case-insensitive on the wire, so receivers may match case-insensitively.
const EventIDHeader = "X-Promoter-Event-Id"

const (
	// defaultMaxAttempts is the total number of delivery attempts (including the first) when
	// spec.delivery.webhook.retry is unset. It mirrors the CRD default for retry.maxAttempts.
	defaultMaxAttempts = 3
	// defaultTimeoutSeconds is the per-attempt request timeout when spec.delivery.webhook.timeoutSeconds
	// is unset. It mirrors the CRD default.
	defaultTimeoutSeconds = 10
	// defaultContentType is set on every request unless overridden by a static spec header.
	defaultContentType = "application/json"
	// baseBackoff is the first inter-attempt wait. For Fixed backoff it is the constant wait;
	// for Exponential it is doubled after each attempt.
	baseBackoff = 1 * time.Second
	// maxBackoff caps the exponential backoff so a large maxAttempts cannot produce an
	// unbounded sleep.
	maxBackoff = 30 * time.Second
)

// webhookDispatcher is the concrete Dispatcher that performs at-least-once HTTP webhook
// delivery for a matched (notification, event) pair: it renders+signs the request via the
// payload seam, POSTs (or PUT/PATCH) with retry/backoff, and on permanent failure DROPS the
// event after recording the failure (TotalFailed, the DeliveryFailing Ready condition, the
// failed metric, and a log line). It also persists success status (LastDelivery,
// TotalDelivered, Ready condition) plus metrics on the PromoterNotification.
//
// IMPORTANT — delivery is best-effort, NOT durable. There is NO dead-letter queue, no on-disk
// spool, and no replay: once retries are exhausted the event is gone except for the recorded
// status/metric/log. If a receiver is unreachable for longer than the retry window
// (~maxAttempts x backoff), those notifications are permanently lost. This matches the
// best-effort contract of the event broker (publishing must never block or fail a reconcile);
// durable/replayable delivery would be a future enhancement.
//
// It is safe for concurrent use: it holds no per-call mutable state, the http.Client is
// concurrency-safe, and status writes use Server-Side Apply with a read-modify-write of the
// cumulative counters.
type webhookDispatcher struct {
	client   client.Client
	recorder k8sevents.EventRecorder
	// newTimer is the timer constructor used for backoff sleeps; overridable in tests so
	// retry/backoff can be exercised without real wall-clock delays.
	newTimer func(d time.Duration) <-chan time.Time
}

// NewWebhookDispatcher returns a Dispatcher that delivers notifications over HTTP webhooks.
// c is used to read the signing Secret and to persist delivery status; recorder may be nil.
func NewWebhookDispatcher(c client.Client, recorder k8sevents.EventRecorder) Dispatcher {
	return &webhookDispatcher{
		client:   c,
		recorder: recorder,
		newTimer: time.After,
	}
}

// Dispatch implements Dispatcher. It delivers event to notification's webhook with retry and
// backoff, then records status and metrics. A returned error indicates the controller's view
// of a terminal failure (the failure has already been recorded on the CR and the event dropped —
// there is no requeue or replay); a nil error indicates the event was delivered.
func (d *webhookDispatcher) Dispatch(ctx context.Context, notification *v1alpha1.PromoterNotification, event events.Event) error {
	logger := log.FromContext(ctx).WithValues(
		"promoterNotification", notification.Name,
		"namespace", notification.Namespace,
		"eventType", string(event.Type),
		"eventID", event.EventID(),
	)

	webhook := notification.Spec.Delivery.Webhook
	if webhook == nil {
		// Match/filter should never enqueue a delivery for a notification without a webhook,
		// but fail closed rather than panic.
		return fmt.Errorf("notification %q has no webhook delivery configured", notification.Name)
	}

	// Render + sign once via the payload seam. We never re-render or re-sign per attempt: the
	// signature covers the exact bytes we POST, and re-rendering could change the body.
	body, signedHeaders, err := payload.BuildRequest(ctx, d.client, notification, event)
	if err != nil {
		// Rendering/signing failure is permanent for this event: record a failing status and
		// return the error. We treat it as a terminal failure (TotalFailed increment, event
		// dropped) because the event can never be delivered as configured — a missing/invalid
		// signing Secret or a bad payloadTemplate will not fix itself on retry.
		logger.Error(err, "failed to build webhook request payload")
		recErr := d.recordResult(ctx, notification, event, deliveryOutcome{
			delivered:  false,
			deadLetter: true,
			statusCode: 0,
			attempts:   0,
			// Prefix makes it unambiguous to an operator reading the Ready condition that this
			// is a configuration error (e.g. bad payloadTemplate or missing/invalid signing
			// secret) — NOT a transient receiver/network failure that retrying could fix.
			message: fmt.Sprintf("configuration error building request (no attempt made): %v", err),
		})
		if recErr != nil {
			logger.Error(recErr, "failed to record delivery status after payload build failure")
		}
		return fmt.Errorf("failed to build webhook request: %w", err)
	}

	outcome := d.deliverWithRetry(ctx, logger, notification, event, body, signedHeaders)

	if recErr := d.recordResult(ctx, notification, event, outcome); recErr != nil {
		logger.Error(recErr, "failed to record delivery status")
		// Surface the status-write failure so the controller can log it, but only if delivery
		// itself succeeded (otherwise the delivery error is the more important one to report).
		if outcome.delivered {
			return fmt.Errorf("delivery succeeded but failed to record status: %w", recErr)
		}
	}

	if !outcome.delivered {
		return fmt.Errorf("webhook delivery failed after %d attempt(s): %s", outcome.attempts, outcome.message)
	}
	return nil
}

// deliveryOutcome captures the result of a delivery so status/metrics recording is decoupled
// from the send loop.
type deliveryOutcome struct {
	message    string
	statusCode int
	attempts   int
	delivered  bool
	// deadLetter marks a terminal (permanent) failure: retries are exhausted or the request
	// could not be built. There is no dead-letter QUEUE — the event is dropped after recording
	// the failure (TotalFailed, DeliveryFailing condition, failed metric, log). The field name
	// is kept for brevity; read it as "terminally failed / dropped".
	deadLetter bool
}

// deliverWithRetry performs the at-least-once send loop: it attempts delivery up to
// maxAttempts times, sleeping per the configured backoff between attempts, and stops early on
// the first success or when the context is done. Each non-final failed attempt increments the
// retry metric.
func (d *webhookDispatcher) deliverWithRetry(
	ctx context.Context,
	logger logr.Logger,
	notification *v1alpha1.PromoterNotification,
	event events.Event,
	body []byte,
	signedHeaders map[string]string,
) deliveryOutcome {
	webhook := notification.Spec.Delivery.Webhook
	maxAttempts := defaultMaxAttempts
	backoffPolicy := v1alpha1.BackoffExponential
	if webhook.Retry != nil {
		if webhook.Retry.MaxAttempts > 0 {
			maxAttempts = int(webhook.Retry.MaxAttempts)
		}
		if webhook.Retry.Backoff != "" {
			backoffPolicy = webhook.Retry.Backoff
		}
	}

	var lastStatus int
	var lastMessage string
	wait := baseBackoff

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return deliveryOutcome{
				delivered:  false,
				deadLetter: true,
				statusCode: lastStatus,
				attempts:   attempt - 1,
				message:    fmt.Sprintf("context done before attempt %d: %v", attempt, ctx.Err()),
			}
		default:
		}

		statusCode, err := d.doRequest(ctx, notification, event.EventID(), body, signedHeaders)
		if err == nil && isSuccess(statusCode) {
			logger.V(4).Info("webhook delivered", "attempt", attempt, "statusCode", statusCode)
			return deliveryOutcome{
				delivered:  true,
				statusCode: statusCode,
				attempts:   attempt,
			}
		}

		lastStatus = statusCode
		if err != nil {
			lastMessage = err.Error()
		} else {
			lastMessage = fmt.Sprintf("received HTTP status %d", statusCode)
		}
		logger.V(4).Info("webhook delivery attempt failed", "attempt", attempt, "statusCode", statusCode, "error", lastMessage)

		// If more attempts remain, count a retry and wait per the backoff policy.
		if attempt < maxAttempts {
			metrics.RecordPromoterNotificationRetry(notification.Namespace, notification.Name, string(event.Type))
			if !d.sleep(ctx, wait) {
				return deliveryOutcome{
					delivered:  false,
					deadLetter: true,
					statusCode: lastStatus,
					attempts:   attempt,
					message:    fmt.Sprintf("context done during backoff after attempt %d: %s", attempt, lastMessage),
				}
			}
			if backoffPolicy == v1alpha1.BackoffExponential {
				wait *= 2
				if wait > maxBackoff {
					wait = maxBackoff
				}
			}
		}
	}

	// Attempts exhausted: terminal failure — the event is dropped (no requeue/replay), the
	// caller records TotalFailed + the DeliveryFailing condition + the failed metric.
	return deliveryOutcome{
		delivered:  false,
		deadLetter: true,
		statusCode: lastStatus,
		attempts:   maxAttempts,
		message:    lastMessage,
	}
}

// doRequest performs a single HTTP attempt and returns the response status code (0 on
// transport error). It builds the request from the configured method, merges the event-ID +
// signed + static headers + Content-Type, applies the per-attempt timeout, and drains/closes
// the body.
func (d *webhookDispatcher) doRequest(
	ctx context.Context,
	notification *v1alpha1.PromoterNotification,
	eventID string,
	body []byte,
	signedHeaders map[string]string,
) (int, error) {
	webhook := notification.Spec.Delivery.Webhook

	method := string(webhook.Method)
	if method == "" {
		method = string(v1alpha1.HTTPMethodPost)
	}

	timeout := time.Duration(defaultTimeoutSeconds) * time.Second
	if webhook.TimeoutSeconds > 0 {
		timeout = time.Duration(webhook.TimeoutSeconds) * time.Second
	}

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, webhook.URL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	// Header precedence (lowest to highest): Content-Type default, then the event-ID header,
	// then static spec headers, then the signed headers from the payload seam. Signed headers
	// are set LAST so a user-supplied static header can never overwrite the integrity
	// signature. The event-ID header is set before static headers so an operator could
	// deliberately override it if they really wanted to, but it is always present by default
	// for receiver-side idempotency/dedup.
	req.Header.Set("Content-Type", defaultContentType)
	req.Header.Set(EventIDHeader, eventID)
	for k, v := range webhook.Headers {
		req.Header.Set(k, v)
	}
	for k, v := range signedHeaders {
		req.Header.Set(k, v)
	}

	// A new client per attempt is cheap and keeps the dispatcher free of shared mutable HTTP
	// state; the per-request context already enforces the timeout.
	httpClient := &http.Client{Timeout: timeout}

	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		// Drain so the connection can be reused, then close.
		_, _ = io.Copy(io.Discard, resp.Body)
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.FromContext(ctx).V(4).Info("failed to close webhook response body", "error", closeErr)
		}
	}()

	return resp.StatusCode, nil
}

// sleep waits for d, returning false if ctx is cancelled first.
func (d *webhookDispatcher) sleep(ctx context.Context, dur time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-d.newTimer(dur):
		return true
	}
}

// recordResult persists the delivery outcome on the PromoterNotification status and emits the
// matching metric. It re-fetches the latest object and does a read-modify-write of the
// cumulative TotalDelivered/TotalFailed counters before a full-status Server-Side Apply, so
// concurrent dispatches for the same notification don't lose increments to a stale snapshot.
func (d *webhookDispatcher) recordResult(
	ctx context.Context,
	notification *v1alpha1.PromoterNotification,
	event events.Event,
	outcome deliveryOutcome,
) error {
	// Record metrics first; metrics must reflect the delivery even if the status write races.
	if outcome.delivered {
		metrics.RecordPromoterNotificationDelivered(notification.Namespace, notification.Name, string(event.Type))
	} else if outcome.deadLetter {
		metrics.RecordPromoterNotificationFailed(notification.Namespace, notification.Name, string(event.Type))
	}

	// Re-fetch the latest object so counter increments are applied to current state.
	var current v1alpha1.PromoterNotification
	if err := d.client.Get(ctx, client.ObjectKeyFromObject(notification), &current); err != nil {
		return fmt.Errorf("failed to get PromoterNotification for status update: %w", err)
	}

	now := metav1.Now()
	current.Status.LastDelivery = &v1alpha1.NotificationDeliveryStatus{
		Time:           &now,
		EventID:        event.EventID(),
		ResponseStatus: int32(outcome.statusCode),
	}

	if outcome.delivered {
		current.Status.TotalDelivered++
	} else if outcome.deadLetter {
		current.Status.TotalFailed++
	}

	current.Status.ObservedGeneration = current.Generation

	// Set the Ready condition. Success => DeliveryHealthy/True; terminal failure => DeliveryFailing/False.
	cond := metav1.Condition{
		Type:               string(promoterConditions.Ready),
		ObservedGeneration: current.Generation,
		LastTransitionTime: now,
	}
	if outcome.delivered {
		cond.Status = metav1.ConditionTrue
		cond.Reason = string(promoterConditions.DeliveryHealthy)
		cond.Message = fmt.Sprintf("delivered event %s (HTTP %d)", event.EventID(), outcome.statusCode)
	} else {
		cond.Status = metav1.ConditionFalse
		cond.Reason = string(promoterConditions.DeliveryFailing)
		if outcome.attempts == 0 {
			// No HTTP attempt was made (e.g. payload build/sign config error). The message
			// already explains the cause; don't tack on a confusing "after 0 attempt(s)".
			cond.Message = fmt.Sprintf("failed to deliver event %s: %s", event.EventID(), outcome.message)
		} else {
			cond.Message = fmt.Sprintf("failed to deliver event %s after %d attempt(s): %s", event.EventID(), outcome.attempts, outcome.message)
		}
	}
	meta.SetStatusCondition(&current.Status.Conditions, cond)

	if d.recorder != nil {
		eventType := "Normal"
		if !outcome.delivered {
			eventType = "Warning"
		}
		d.recorder.Eventf(&current, nil, eventType, cond.Reason, "Delivering", cond.Message)
	}

	// Persist via Server-Side Apply of the full status under the notification controller's
	// field owner, consistent with the other controllers' status writes.
	if err := d.applyStatus(ctx, &current); err != nil {
		return fmt.Errorf("failed to apply PromoterNotification status: %w", err)
	}
	return nil
}

// applyStatus writes the full status subresource of n via Server-Side Apply under the
// PromoterNotification controller's field owner, mirroring how the reconcilers persist status.
func (d *webhookDispatcher) applyStatus(ctx context.Context, n *v1alpha1.PromoterNotification) error {
	statusAC := acv1alpha1.PromoterNotificationStatus().
		WithObservedGeneration(n.Status.ObservedGeneration).
		WithTotalDelivered(n.Status.TotalDelivered).
		WithTotalFailed(n.Status.TotalFailed).
		WithConditions(utils.ConditionsToApply(n.Status.Conditions)...)

	if ld := n.Status.LastDelivery; ld != nil {
		ldAC := acv1alpha1.NotificationDeliveryStatus().
			WithEventID(ld.EventID).
			WithResponseStatus(ld.ResponseStatus)
		if ld.Time != nil {
			ldAC = ldAC.WithTime(*ld.Time)
		}
		statusAC = statusAC.WithLastDelivery(ldAC)
	}

	applyCfg := acv1alpha1.PromoterNotification(n.Name, n.Namespace).WithStatus(statusAC)

	patched := &v1alpha1.PromoterNotification{}
	patched.Name = n.Name
	patched.Namespace = n.Namespace
	if err := d.client.Status().Patch(ctx, patched, utils.ApplyPatch{ApplyConfig: applyCfg},
		client.FieldOwner(constants.PromoterNotificationControllerFieldOwner), client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to patch PromoterNotification status: %w", err)
	}
	return nil
}

// isSuccess reports whether an HTTP status code is a 2xx success.
func isSuccess(code int) bool {
	return code >= 200 && code < 300
}
