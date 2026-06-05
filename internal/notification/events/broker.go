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

package events

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// MemoryBroker is a concrete, in-memory Broker: the wiring point that connects reconcilers
// (publishers) to the notification controller (subscriber). It is the Phase 2 implementation
// of the events.Broker interface.
//
// Design contract (per the Publisher docs in events.go):
//   - Publish must never block a reconcile and must never fail a reconcile for transport
//     reasons. MemoryBroker fans an event out to all registered handlers synchronously but
//     handlers are expected to be cheap (the notification controller's handler just enqueues
//     onto a buffered channel). A handler that returns an error is logged, not propagated —
//     Publish only returns an error for programmer errors (a nil/zero event).
//   - Subscribe registers a handler. Handlers should be registered during setup (before the
//     manager starts publishing); registration is safe for concurrent use regardless.
//
// MemoryBroker is safe for concurrent use.
type MemoryBroker struct {
	handlers []Handler
	mu       sync.RWMutex
}

// NewMemoryBroker returns a ready-to-use in-memory Broker.
func NewMemoryBroker() *MemoryBroker {
	return &MemoryBroker{}
}

// Subscribe registers handler to receive every published event. It is safe to call
// concurrently, though handlers are normally registered once during controller setup.
func (b *MemoryBroker) Subscribe(handler Handler) {
	if handler == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, handler)
}

// Publish fans event out to all registered handlers. It returns an error only for a
// programmer error (a zero/invalid event). Handler errors are logged and swallowed so that
// publishing can never fail or block the caller's reconcile.
func (b *MemoryBroker) Publish(ctx context.Context, event Event) error {
	if event.Type == "" {
		return errors.New("cannot publish event with empty Type")
	}
	if event.Object.Namespace == "" || event.Object.Name == "" {
		return fmt.Errorf("cannot publish event with empty Object namespace/name (type %q)", event.Type)
	}

	b.mu.RLock()
	handlers := make([]Handler, len(b.handlers))
	copy(handlers, b.handlers)
	b.mu.RUnlock()

	logger := log.FromContext(ctx)
	for _, h := range handlers {
		if err := h.Handle(ctx, event); err != nil {
			// Best-effort: a handler failure must not fail the publisher's reconcile.
			logger.Error(err, "notification event handler returned an error",
				"eventType", event.Type, "object", event.Object.String(), "eventID", event.EventID())
		}
	}
	return nil
}
