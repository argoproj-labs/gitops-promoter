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

// Package events defines the in-process event-publisher contract for the notification
// framework. Reconcilers (PromotionStrategy, ChangeTransferPolicy, CommitStatus, ...)
// construct an Event and Publish it; the notification controller subscribes and matches
// the event against PromoterNotification resources for delivery.
//
// The types here are intentionally minimal and stable: they are the serialization point
// the whole team builds on. They carry plain Go values (no live Kubernetes objects) so an
// event can be safely buffered, copied, rendered into a Go template, and logged.
package events

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"time"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// EventType is the type of a notification event. It mirrors
// v1alpha1.NotificationEventType so that PromoterNotification.spec.eventTypes can be matched
// directly against an emitted Event without conversion.
type EventType = v1alpha1.NotificationEventType

// Re-export the event type constants for ergonomic use by publishers.
const (
	// TypePromotionComplete fires when a promotion to an environment completes.
	TypePromotionComplete = v1alpha1.NotificationEventPromotionComplete
	// TypeGateFailed fires when a commit-status gate transitions to failure.
	TypeGateFailed = v1alpha1.NotificationEventGateFailed
	// TypeGateStale fires when a commit-status gate is stale.
	TypeGateStale = v1alpha1.NotificationEventGateStale
	// TypeCTPProposed fires when a ChangeTransferPolicy proposes a new dry sha.
	TypeCTPProposed = v1alpha1.NotificationEventCTPProposed
	// TypeCTPActive fires when a ChangeTransferPolicy's active branch advances.
	TypeCTPActive = v1alpha1.NotificationEventCTPActive
)

// ObjectRef identifies the originating Kubernetes resource that produced an event.
// It is a value type so events remain decoupled from live client objects.
type ObjectRef struct {
	// APIVersion of the originating resource, e.g. "promoter.argoproj.io/v1alpha1".
	APIVersion string `json:"apiVersion"`
	// Kind of the originating resource, e.g. "ChangeTransferPolicy".
	Kind string `json:"kind"`
	// Namespace of the originating resource.
	Namespace string `json:"namespace"`
	// Name of the originating resource.
	Name string `json:"name"`
	// UID of the originating resource, when known.
	UID string `json:"uid,omitempty"`
}

// String returns a stable "namespace/kind/name" identity for the referenced object.
func (r ObjectRef) String() string {
	return r.Namespace + "/" + r.Kind + "/" + r.Name
}

// Event is the immutable, self-contained description of something that happened in the
// promoter that a PromoterNotification may want to be notified about.
//
// Field-population guidance by Type:
//   - PromotionComplete: Environment, PreviousSha, NewSha, ActiveURL set.
//   - CTPProposed:       Environment, NewSha (proposed dry sha), ProposedURL set.
//   - CTPActive:         Environment, PreviousSha, NewSha (active sha), ActiveURL set.
//   - GateFailed/GateStale: GateName, Environment, NewSha set; PreviousState/NewState
//     describe the gate phase transition.
//
// Unset fields are simply empty; consumers and templates must tolerate missing values.
type Event struct {
	// Type is the kind of event. Matched against PromoterNotification.spec.eventTypes.
	Type EventType `json:"type"`

	// Object references the originating custom resource.
	Object ObjectRef `json:"object"`

	// Labels are the labels of the originating resource at the time of the event.
	// PromoterNotification.spec.selector is matched against these.
	Labels map[string]string `json:"labels,omitempty"`

	// Environment is the promotion environment (target branch) the event pertains to, if any.
	Environment string `json:"environment,omitempty"`

	// GateName is the commit-status / gate key for gate events, if any.
	GateName string `json:"gateName,omitempty"`

	// PreviousSha is the prior git SHA, where a transition is involved.
	PreviousSha string `json:"previousSha,omitempty"`
	// NewSha is the new/current git SHA the event pertains to.
	NewSha string `json:"newSha,omitempty"`

	// PreviousState is the prior state/phase, where a transition is involved
	// (e.g. a gate phase "pending"). Free-form to stay event-type agnostic.
	PreviousState string `json:"previousState,omitempty"`
	// NewState is the new/current state/phase the event pertains to.
	NewState string `json:"newState,omitempty"`

	// ActiveURL is a link to the active environment/resource view, if available.
	ActiveURL string `json:"activeURL,omitempty"`
	// ProposedURL is a link to the proposed environment/resource view, if available.
	ProposedURL string `json:"proposedURL,omitempty"`

	// OccurredAt is when the underlying change happened (set by the publisher).
	OccurredAt time.Time `json:"occurredAt"`

	// id is the cached stable event ID. It is computed lazily by EventID and never serialized
	// directly; consumers should call EventID().
	id string
}

// EventID returns a stable, deterministic identifier for the event. The same logical event
// (same type, object identity, environment, gate, and the shas/states that define it) always
// produces the same ID, which lets delivery be idempotent and de-duplicated across retries
// and controller restarts. OccurredAt is intentionally excluded so a re-emitted identical
// event collapses to one ID.
func (e *Event) EventID() string {
	if e.id != "" {
		return e.id
	}
	parts := []string{
		string(e.Type),
		e.Object.Namespace,
		e.Object.Kind,
		e.Object.Name,
		e.Object.UID,
		e.Environment,
		e.GateName,
		e.PreviousSha,
		e.NewSha,
		e.PreviousState,
		e.NewState,
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	e.id = hex.EncodeToString(sum[:])
	return e.id
}

// SortedLabelKeys returns the label keys in sorted order, a convenience for deterministic
// rendering in payload templates.
func (e *Event) SortedLabelKeys() []string {
	keys := make([]string, 0, len(e.Labels))
	for k := range e.Labels {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Publisher is the in-process emit side of the contract. Reconcilers hold a Publisher and
// call Publish when a notable change occurs. Implementations must be safe for concurrent use
// and should be non-blocking / best-effort: publishing a notification must never block or fail
// a reconcile. Publish returns an error only for programmer errors (e.g. a nil/invalid event);
// transport and delivery failures are handled asynchronously by the subscriber side.
type Publisher interface {
	Publish(ctx context.Context, event Event) error
}

// Handler consumes a single event. The notification controller registers a Handler that
// matches the event against PromoterNotification resources and enqueues deliveries.
// Implementations must be safe for concurrent use and should return quickly.
type Handler interface {
	Handle(ctx context.Context, event Event) error
}

// HandlerFunc adapts a function to the Handler interface.
type HandlerFunc func(ctx context.Context, event Event) error

// Handle calls f(ctx, event).
func (f HandlerFunc) Handle(ctx context.Context, event Event) error { return f(ctx, event) }

// Subscriber is the consume side of the contract. The notification controller registers one
// or more Handlers via Subscribe; the underlying broker fans published events out to them.
type Subscriber interface {
	Subscribe(handler Handler)
}

// Broker is a component that is both a Publisher and a Subscriber: the wiring point that
// connects reconcilers (publishers) to the notification controller (subscriber). A concrete
// in-memory implementation is provided by Phase 2.
type Broker interface {
	Publisher
	Subscriber
}
