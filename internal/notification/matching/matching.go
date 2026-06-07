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

// Package matching decides whether an emitted notification Event matches a
// PromoterNotification's filters. It is a pure (no I/O, no client) predicate so it
// can be unit-tested exhaustively and reused by the notification controller (Phase 2a)
// when it fans an event out to candidate PromoterNotification resources.
//
// An Event matches a PromoterNotification when ALL of the following hold:
//
//  1. Namespace: the PromoterNotification is in the same namespace as the originating
//     resource (event.Object.Namespace). Notifications are namespace-scoped; a
//     PromoterNotification never matches events from other namespaces.
//  2. Event type: event.Type is one of spec.eventTypes. spec.eventTypes is required and
//     MinItems=1 by the CRD, so an empty list never matches (fail-closed).
//  3. Label selector: spec.selector matches the originating resource's labels
//     (event.Labels). A nil selector matches everything (per the CRD field doc). An
//     empty (non-nil, zero-value) selector also matches everything, consistent with
//     metav1.LabelSelectorAsSelector semantics.
//
// The critical negative-path contract for the framework — "a PromoterNotification whose
// selector does NOT match must deliver NOTHING" — is realized here by returning false.
package matching

import (
	"errors"
	"fmt"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/notification/events"
)

// Matches reports whether event matches notification's namespace, event-type, and label
// selector filters. It returns an error only when spec.selector is structurally invalid
// (cannot be converted to a selector); in that case callers should treat the
// notification as non-matching and surface the error as a status condition rather than
// delivering.
func Matches(notification *v1alpha1.PromoterNotification, event events.Event) (bool, error) {
	if notification == nil {
		return false, errors.New("notification is nil")
	}

	// 1. Namespace scope: only events from the notification's own namespace.
	if notification.Namespace != event.Object.Namespace {
		return false, nil
	}

	// 2. Event type membership (fail-closed on empty list).
	if !slices.Contains(notification.Spec.EventTypes, event.Type) {
		return false, nil
	}

	// 3. Label selector against the originating resource's labels.
	return matchesSelector(notification.Spec.Selector, event.Labels)
}

// matchesSelector reports whether labelSet satisfies sel. A nil selector matches all.
func matchesSelector(sel *metav1.LabelSelector, labelSet map[string]string) (bool, error) {
	if sel == nil {
		return true, nil
	}
	selector, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return false, fmt.Errorf("invalid label selector: %w", err)
	}
	return selector.Matches(labels.Set(labelSet)), nil
}
