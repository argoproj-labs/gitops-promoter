package cache

import (
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ClientOptions returns manager client options with read-your-own-write consistency
// enabled on the cache-backed client. Writes block subsequent cached reads of the
// same GVK+key until the informer observes them (or the request times out), which
// prevents reconcilers from acting on stale cache snapshots immediately after a
// status patch. See kubernetes-sigs/controller-runtime#3472.
//
// Unstructured is enabled so Get/List of unstructured objects (out-of-tree
// orderCommitStatusRef gates) use the informer cache. Those informers inherit
// OptionsForInstanceID's DefaultLabelSelector, so they are instance-id partitioned.
func ClientOptions() client.Options {
	return client.Options{
		Cache: &client.CacheOptions{
			EnableReadYourWritesConsistency: ptr.To(true),
			Unstructured:                    true,
		},
	}
}
