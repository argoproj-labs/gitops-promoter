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

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/notification/events"
)

// Dispatcher delivers a single matched notification for a single event. It is the seam
// between the notification controller (Phase 2a — match/filter/enqueue/worker-pool) and the
// delivery implementation (Phase 2b — HTTP send, signing, retry/backoff, dead-letter, and
// status/metrics updates).
//
// Contract:
//   - Dispatch is called once by a controller worker for each (matching notification, event)
//     pair. notification is the live PromoterNotification CR the controller fetched; event is
//     the value-typed event that matched it.
//   - The controller does NOT retry: a non-nil error from Dispatch is treated as a terminal
//     delivery failure for the controller's own logging/metrics. All retry, backoff,
//     de-duplication, dead-lettering, and status/condition updates on the CR are the
//     responsibility of the Dispatcher implementation.
//   - Implementations must be safe for concurrent use: multiple workers may call Dispatch
//     concurrently, including for the same notification with different events.
//   - Dispatch should honor ctx cancellation/deadline.
type Dispatcher interface {
	Dispatch(ctx context.Context, notification *v1alpha1.PromoterNotification, event events.Event) error
}

// DispatcherFunc adapts a plain function to the Dispatcher interface.
type DispatcherFunc func(ctx context.Context, notification *v1alpha1.PromoterNotification, event events.Event) error

// Dispatch calls f.
func (f DispatcherFunc) Dispatch(ctx context.Context, notification *v1alpha1.PromoterNotification, event events.Event) error {
	return f(ctx, notification, event)
}
