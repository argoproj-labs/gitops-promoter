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

package utils

import (
	"sync"

	"k8s.io/apimachinery/pkg/util/resourceversion"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ResourceVersionTracker remembers the ResourceVersion of the last status write a
// reconciler made for each object key. It exists to detect the read-your-writes race
// where controller-runtime serializes reconciles per key but the informer cache may
// not yet reflect the status patch the previous reconcile just made. When a follow-up
// reconcile is enqueued by a side effect of the previous reconcile (e.g. enqueueing a
// related resource that fans back to this one via a watch), it can start with a
// cached object whose ResourceVersion is older than what we just wrote, causing the
// new reconcile to evaluate logic against stale state.
//
// TODO: replace this with controller-runtime's read-your-own-write client once
// kubernetes-sigs/controller-runtime#3473 lands and we upgrade to a release that
// includes it. That design provides the same guarantee (writes block subsequent
// cached reads of the same GVK+key until the cache observes them, or the request
// times out) for ALL controllers using the cached client, opted in via
// `client.Options.Cache.ReadYourOwnWrite: ptr.To(true)`. At that point this
// tracker, its Reconcile-entry stale check, and the Forget-on-NotFound cleanup
// can all be deleted in favor of constructing the manager's client with that
// option set.
//
// Usage from a reconciler is the standard pattern:
//
//	if t.IsCacheStale(key, obj.ResourceVersion) {
//	    return ctrl.Result{RequeueAfter: 100 * time.Millisecond}, nil
//	}
//	// ... do work, patch status ...
//	t.Record(key, obj.ResourceVersion) // after Status().Patch returns (obj is mutated in place by the patch)
//
// The tracker is safe for concurrent use. Callers should invoke Forget on the
// IsNotFound branch of their initial Get so deleted objects don't accumulate
// entries over the controller's lifetime.
type ResourceVersionTracker struct {
	lastWritten map[client.ObjectKey]string
	mu          sync.Mutex
}

// NewResourceVersionTracker returns an empty tracker ready for use.
func NewResourceVersionTracker() *ResourceVersionTracker {
	return &ResourceVersionTracker{lastWritten: make(map[client.ObjectKey]string)}
}

// Record stores rv as the most recent ResourceVersion we wrote for key. It is safe to
// call with an empty rv (e.g. when the status patch was skipped because the object was
// deleted concurrently): the entry is left untouched in that case so a later real
// write still has the chance to update it.
func (t *ResourceVersionTracker) Record(key client.ObjectKey, rv string) {
	if rv == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastWritten[key] = rv
}

// Forget drops any record for key. Reconcilers should call this when an object has
// been deleted (e.g. on the IsNotFound path after Get) so the tracker doesn't
// accumulate entries for objects that no longer exist. Forgetting a key that was
// never recorded is a no-op. Forgetting is safe even if a new object is later
// created with the same key: ResourceVersions are globally monotonic per resource
// type, so the new object's RV will be greater than anything the old object ever
// had, meaning a stale leftover record would not cause incorrect staleness — but
// it would needlessly hold memory, which Forget reclaims.
func (t *ResourceVersionTracker) Forget(key client.ObjectKey) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.lastWritten, key)
}

// IsCacheStale reports whether cachedRV (the ResourceVersion the reconciler just read
// from the informer cache for key) is strictly older than the last ResourceVersion we
// wrote for that key.
//
// Returns false when we have no record for key (first reconcile), when the cache is at
// or ahead of our last write (steady state), or when either ResourceVersion fails to
// parse as a well-formed positive integer (CompareResourceVersion rejects it). Failing
// open on malformed input is intentional: requeuing forever on an unparsable RV would
// be worse than processing a request we cannot prove is stale, and the apiserver only
// emits well-formed RVs in practice.
func (t *ResourceVersionTracker) IsCacheStale(key client.ObjectKey, cachedRV string) bool {
	if cachedRV == "" {
		return false
	}
	t.mu.Lock()
	last, ok := t.lastWritten[key]
	t.mu.Unlock()
	if !ok {
		return false
	}
	cmp, err := resourceversion.CompareResourceVersion(cachedRV, last)
	if err != nil {
		return false
	}
	return cmp < 0
}
