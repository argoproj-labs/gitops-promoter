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

package apiserver

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	viewv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/view/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/controller"
)

var log = ctrl.Log.WithName("dashboard-apiserver")

// debounceInterval coalesces bursts of child changes for the same PromotionStrategy.
const debounceInterval = 250 * time.Millisecond

// watcherBufferSize is the per-watcher headroom for delta events beyond the
// initial snapshot. A watcher whose buffer overflows is terminated (its result
// channel is closed) so the client re-lists, mirroring the apiserver cacher's
// handling of watchers that cannot keep up — deltas are never silently dropped.
const watcherBufferSize = 64

// BundleProvider builds PromotionStrategyDetails bundles from a read-only
// controller-runtime cache, and fans out watch events to registered watchers when
// any related resource changes.
type BundleProvider struct {
	cache  cache.Cache
	reader client.Reader
	queue  workqueue.TypedRateLimitingInterface[types.NamespacedName]
	// watchers holds the registered watch streams keyed by a unique id (assigned
	// from nextID), so broadcast can fan events out to matching watchers and a
	// watcher can be removed when its stream stops.
	watchers map[int]*bundleWatcher
	// known tracks the last-broadcast labels of PromotionStrategy keys that have
	// already emitted an ADDED event, so reconcileKey can distinguish ADDED from
	// MODIFIED, only emit a DELETED tombstone for keys it previously surfaced, and
	// give label-selector watchers correct ADDED/DELETED transitions when an
	// object's labels start or stop matching their selector.
	known map[types.NamespacedName]labels.Set

	mu     sync.RWMutex
	rv     atomic.Uint64
	nextID int
}

// NewBundleProvider creates a provider backed by the given cache.
func NewBundleProvider(c cache.Cache) *BundleProvider {
	p := newProviderWithReader(c)
	p.cache = c
	return p
}

// newProviderWithReader creates a provider backed by an arbitrary read-only client.
// The cache is left nil (SetupInformers/Run require a real cache); this is primarily
// used by tests and by NewBundleProvider.
func newProviderWithReader(reader client.Reader) *BundleProvider {
	p := &BundleProvider{
		reader: reader,
		queue: workqueue.NewTypedRateLimitingQueueWithConfig(
			workqueue.DefaultTypedControllerRateLimiter[types.NamespacedName](),
			workqueue.TypedRateLimitingQueueConfig[types.NamespacedName]{Name: "dashboard-bundles"},
		),
		watchers: map[int]*bundleWatcher{},
		known:    map[types.NamespacedName]labels.Set{},
	}
	p.rv.Store(1)
	return p
}

// nextResourceVersion returns the next synthetic, monotonically-increasing RV.
func (p *BundleProvider) nextResourceVersion() string {
	return strconv.FormatUint(p.rv.Add(1), 10)
}

// currentResourceVersion returns the current synthetic RV without incrementing.
func (p *BundleProvider) currentResourceVersion() string {
	return strconv.FormatUint(p.rv.Load(), 10)
}

// childKinds are all resource kinds whose changes can affect a bundle.
func (p *BundleProvider) childKinds() []client.Object {
	kinds := []client.Object{
		&promoterv1alpha1.PromotionStrategy{},
		&promoterv1alpha1.ChangeTransferPolicy{},
		&promoterv1alpha1.PullRequest{},
		&promoterv1alpha1.CommitStatus{},
	}
	kinds = append(kinds, controller.GateCommitStatusKinds()...)
	return append(kinds,
		&promoterv1alpha1.GitRepository{},
		&promoterv1alpha1.ScmProvider{},
		&promoterv1alpha1.ClusterScmProvider{},
	)
}

// SetupInformers registers event handlers for all child kinds so that any change
// is mapped back to the owning PromotionStrategy key(s) and enqueued.
func (p *BundleProvider) SetupInformers(ctx context.Context) error {
	handler := toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			p.enqueueForObject(ctx, obj)
		},
		UpdateFunc: func(_, newObj any) {
			p.enqueueForObject(ctx, newObj)
		},
		DeleteFunc: func(obj any) {
			if tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			p.enqueueForObject(ctx, obj)
		},
	}

	for _, kind := range p.childKinds() {
		informer, err := p.cache.GetInformer(ctx, kind)
		if err != nil {
			return fmt.Errorf("failed to get informer for %T: %w", kind, err)
		}
		if _, err := informer.AddEventHandler(handler); err != nil {
			return fmt.Errorf("failed to add event handler for %T: %w", kind, err)
		}
	}
	return nil
}

// Run drains the workqueue, rebuilding and broadcasting bundles until ctx is done.
func (p *BundleProvider) Run(ctx context.Context) {
	defer p.queue.ShutDown()
	go func() {
		<-ctx.Done()
		p.queue.ShutDown()
	}()
	for p.processNext(ctx) {
	}
}

func (p *BundleProvider) processNext(ctx context.Context) bool {
	key, shutdown := p.queue.Get()
	if shutdown {
		return false
	}
	defer p.queue.Done(key)
	if err := p.reconcileKey(ctx, key); err != nil {
		// Transient failure (e.g. a momentary cache hiccup): retry with backoff so
		// the delta is not silently lost — watchers would otherwise stay stale
		// until the next unrelated change to the same PromotionStrategy.
		log.Error(err, "failed to build bundle; requeuing", "namespace", key.Namespace, "name", key.Name)
		p.queue.AddRateLimited(key)
		return true
	}
	p.queue.Forget(key)
	return true
}

// reconcileKey rebuilds the bundle for a PromotionStrategy key and broadcasts the
// resulting watch event (ADDED/MODIFIED/DELETED) to matching watchers. A non-nil
// error means the rebuild failed transiently and the caller should retry.
func (p *BundleProvider) reconcileKey(ctx context.Context, key types.NamespacedName) error {
	bundle, err := buildBundle(ctx, p.reader, key.Namespace, key.Name, p.nextResourceVersion())
	if apierrors.IsNotFound(err) {
		p.mu.Lock()
		lastLabels, wasKnown := p.known[key]
		delete(p.known, key)
		p.mu.Unlock()
		if wasKnown {
			tombstone := &viewv1alpha1.PromotionStrategyDetails{
				ObjectMeta: metav1.ObjectMeta{
					Name:            key.Name,
					Namespace:       key.Namespace,
					Labels:          lastLabels,
					ResourceVersion: p.currentResourceVersion(),
				},
			}
			p.broadcast(watch.Event{Type: watch.Deleted, Object: tombstone}, key, lastLabels, nil)
		}
		return nil
	}
	if err != nil {
		return err
	}

	curLabels := labels.Set(bundle.Labels)
	p.mu.Lock()
	prevLabels, wasKnown := p.known[key]
	eventType := watch.Modified
	if !wasKnown {
		eventType = watch.Added
	}
	p.known[key] = curLabels
	p.mu.Unlock()

	p.broadcast(watch.Event{Type: eventType, Object: bundle}, key, prevLabels, curLabels)
	return nil
}

// enqueueForObject maps a changed child object to the owning PromotionStrategy
// key(s) and schedules a debounced rebuild.
func (p *BundleProvider) enqueueForObject(ctx context.Context, obj any) {
	co, ok := obj.(client.Object)
	if !ok {
		return
	}
	for _, key := range p.mapObjectToPromotionStrategies(ctx, co) {
		p.queue.AddAfter(key, debounceInterval)
	}
}

// mapObjectToPromotionStrategies returns the PromotionStrategy keys affected by a
// change to the given object.
func (p *BundleProvider) mapObjectToPromotionStrategies(ctx context.Context, obj client.Object) []types.NamespacedName {
	switch o := obj.(type) {
	case *promoterv1alpha1.PromotionStrategy:
		return []types.NamespacedName{{Namespace: o.Namespace, Name: o.Name}}
	case *promoterv1alpha1.ChangeTransferPolicy:
		return keyFromLabel(o.Namespace, o.Labels)
	case *promoterv1alpha1.PullRequest:
		return keyFromLabel(o.Namespace, o.Labels)
	case *promoterv1alpha1.CommitStatus:
		return keyFromLabel(o.Namespace, o.Labels)
	case *promoterv1alpha1.GitRepository:
		return p.keysReferencingGitRepository(ctx, o.Namespace, o.Name)
	case *promoterv1alpha1.ScmProvider:
		return p.keysReferencingScmProvider(ctx, "ScmProvider", o.Namespace, o.Name)
	case *promoterv1alpha1.ClusterScmProvider:
		return p.keysReferencingScmProvider(ctx, "ClusterScmProvider", "", o.Name)
	default:
		// PromotionStrategyRef gate managers (ArgoCD/Git/Timed/WebRequest/Scheduled
		// CommitStatus, and any future gate with Spec.PromotionStrategyRef).
		if name := controller.PromotionStrategyRefName(obj); name != "" {
			return keyFromRef(obj.GetNamespace(), name)
		}
		return nil
	}
}

func keyFromLabel(namespace string, labels map[string]string) []types.NamespacedName {
	if labels == nil {
		return nil
	}
	if name := labels[promoterv1alpha1.PromotionStrategyLabel]; name != "" {
		return []types.NamespacedName{{Namespace: namespace, Name: name}}
	}
	return nil
}

func keyFromRef(namespace, name string) []types.NamespacedName {
	if name == "" {
		return nil
	}
	return []types.NamespacedName{{Namespace: namespace, Name: name}}
}

// keysReferencingGitRepository returns PromotionStrategy keys in a namespace whose
// repositoryReference points at the given GitRepository.
func (p *BundleProvider) keysReferencingGitRepository(ctx context.Context, namespace, repoName string) []types.NamespacedName {
	psList := &promoterv1alpha1.PromotionStrategyList{}
	if err := p.reader.List(ctx, psList, client.InNamespace(namespace)); err != nil {
		log.Error(err, "failed to list PromotionStrategies for GitRepository mapping", "namespace", namespace)
		return nil
	}
	var keys []types.NamespacedName
	for i := range psList.Items {
		if psList.Items[i].Spec.RepositoryReference.Name == repoName {
			keys = append(keys, types.NamespacedName{Namespace: namespace, Name: psList.Items[i].Name})
		}
	}
	return keys
}

// keysReferencingScmProvider returns PromotionStrategy keys for GitRepositories
// that use the given ScmProvider or ClusterScmProvider (cluster-scoped providers
// fan out across all namespaces).
func (p *BundleProvider) keysReferencingScmProvider(ctx context.Context, providerKind, providerNamespace, providerName string) []types.NamespacedName {
	repoList := &promoterv1alpha1.GitRepositoryList{}
	var listOpts []client.ListOption
	if providerKind == "ScmProvider" {
		listOpts = append(listOpts, client.InNamespace(providerNamespace))
	}
	if err := p.reader.List(ctx, repoList, listOpts...); err != nil {
		log.Error(err, "failed to list GitRepositories for ScmProvider mapping", "kind", providerKind, "name", providerName)
		return nil
	}

	var keys []types.NamespacedName
	for i := range repoList.Items {
		repo := &repoList.Items[i]
		if repo.Spec.ScmProviderRef.Kind == providerKind && repo.Spec.ScmProviderRef.Name == providerName {
			keys = append(keys, p.keysReferencingGitRepository(ctx, repo.Namespace, repo.Name)...)
		}
	}
	return keys
}

// Get builds the bundle for a single PromotionStrategy.
func (p *BundleProvider) Get(ctx context.Context, namespace, name string) (*viewv1alpha1.PromotionStrategyDetails, error) {
	return buildBundle(ctx, p.reader, namespace, name, p.currentResourceVersion())
}

// List builds bundles for the PromotionStrategies in the given namespace (all
// namespaces when namespace is empty), filtered by the optional exact name and
// label selector (nil selects everything). The bundle's labels are the source
// PromotionStrategy's labels, so the selector is applied to the PS before the
// (more expensive) bundle is built.
func (p *BundleProvider) List(ctx context.Context, namespace, name string, labelSelector labels.Selector) (*viewv1alpha1.PromotionStrategyDetailsList, error) {
	rv := p.currentResourceVersion()
	out := &viewv1alpha1.PromotionStrategyDetailsList{
		ListMeta: metav1.ListMeta{ResourceVersion: rv},
	}

	if name != "" {
		// A name filter resolves to at most one bundle, so Get it directly instead
		// of listing (and building) every PromotionStrategy in the namespace.
		bundle, err := p.Get(ctx, namespace, name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return out, nil
			}
			return nil, err
		}
		if matchesLabels(labelSelector, bundle.Labels) {
			out.Items = append(out.Items, *bundle)
		}
		return out, nil
	}

	psList := &promoterv1alpha1.PromotionStrategyList{}
	var listOpts []client.ListOption
	if namespace != "" {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}
	if err := p.reader.List(ctx, psList, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list PromotionStrategies: %w", err)
	}

	for i := range psList.Items {
		ps := &psList.Items[i]
		if !matchesLabels(labelSelector, ps.Labels) {
			continue
		}
		bundle, err := buildBundle(ctx, p.reader, ps.Namespace, ps.Name, rv)
		if err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		out.Items = append(out.Items, *bundle)
	}
	return out, nil
}

// Watch registers a watcher for the given namespace (and optional exact-name and
// label-selector filters). When sendInitial is true, the current set of bundles is
// delivered as ADDED events before subsequent deltas stream in. When
// sendInitialEventsBookmark is also true (client-go "watch list" protocol), a
// terminating Bookmark event annotated with k8s.io/initial-events-end is sent after
// the snapshot so reflectors know the initial list is complete.
//
// The snapshot and the watcher registration happen atomically with respect to
// broadcast (under the provider lock), so a delta can never be delivered ahead of
// the snapshot it is newer than, and the snapshot itself can never be interleaved
// with concurrent events. Initial events are buffered in full on the watcher's
// input queue — they are never dropped, no matter how many bundles exist — and a
// per-watcher goroutine forwards them to the result channel as the watch handler
// drains it.
func (p *BundleProvider) Watch(ctx context.Context, namespace, name string, labelSelector labels.Selector, sendInitial, sendInitialEventsBookmark bool) (watch.Interface, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var initial []watch.Event
	if sendInitial {
		list, err := p.List(ctx, namespace, name, labelSelector)
		if err != nil {
			return nil, err
		}
		for i := range list.Items {
			initial = append(initial, watch.Event{Type: watch.Added, Object: &list.Items[i]})
		}
		if sendInitialEventsBookmark {
			bookmark := &viewv1alpha1.PromotionStrategyDetails{}
			bookmark.SetResourceVersion(list.ResourceVersion)
			bookmark.SetAnnotations(map[string]string{metav1.InitialEventsAnnotationKey: "true"})
			initial = append(initial, watch.Event{Type: watch.Bookmark, Object: bookmark})
		}
	}

	w := &bundleWatcher{
		provider:      p,
		namespace:     namespace,
		name:          name,
		labelSelector: labelSelector,
		// Size the input queue to hold the entire initial snapshot plus headroom
		// for deltas, so the queueing below can never block or drop.
		input:  make(chan watch.Event, len(initial)+watcherBufferSize),
		result: make(chan watch.Event),
		done:   make(chan struct{}),
	}
	for _, ev := range initial {
		w.input <- ev
	}

	p.nextID++
	w.id = p.nextID
	p.watchers[w.id] = w
	go w.process()

	return w, nil
}

// broadcast fans an event out to all watchers matching the affected key. For
// label-selector watchers it applies the upstream cacher's transition semantics
// using the previous and current label sets: an object that starts matching is
// delivered as ADDED, one that stops matching is delivered as DELETED, and one
// that matches throughout is delivered with the original event type. Watchers
// whose input buffer is full are terminated (closing their result channel) so the
// client re-lists instead of silently missing deltas.
func (p *BundleProvider) broadcast(ev watch.Event, key types.NamespacedName, prevLabels, curLabels labels.Set) {
	var overflowed []*bundleWatcher

	p.mu.RLock()
	for _, w := range p.watchers {
		if !w.matchesKey(key.Namespace, key.Name) {
			continue
		}
		// prev only exists for MODIFIED/DELETED events; cur only for ADDED/MODIFIED.
		matchesPrev := ev.Type != watch.Added && w.matchesLabels(prevLabels)
		matchesCur := ev.Type != watch.Deleted && w.matchesLabels(curLabels)

		var sendType watch.EventType
		switch {
		case matchesPrev && matchesCur:
			sendType = ev.Type
		case matchesCur:
			sendType = watch.Added
		case matchesPrev:
			sendType = watch.Deleted
		default:
			continue
		}

		// Each watcher is served by its own goroutine in the apiserver watch
		// handler, whose versioning codec mutates the object's TypeMeta (GVK) in
		// place during serialization (SetGroupVersionKind, restored via defer).
		// For the default JSON/YAML watch the serving path does NOT copy the
		// embedded object before encoding; the only layer that copies for this
		// reason is the storage cacher's cachingObject, which deep-copies the
		// wrapped object before encoding (see cachingObject.GetObject:
		// https://github.com/kubernetes/kubernetes/blob/v1.36.1/staging/src/k8s.io/apiserver/pkg/storage/cacher/caching_object.go#L159-L165)
		// but only wraps etcd-backed resources. This virtual REST bypasses the
		// cacher, so nothing copies on our behalf. Give every watcher its own
		// copy so concurrent encodes of a shared object don't race on TypeMeta.
		evCopy := watch.Event{Type: sendType, Object: ev.Object.DeepCopyObject()}
		select {
		case w.input <- evCopy:
		default:
			overflowed = append(overflowed, w)
		}
	}
	p.mu.RUnlock()

	// Stop takes the provider lock, so terminate overflowed watchers outside it.
	for _, w := range overflowed {
		log.Info("terminating watcher that cannot keep up; client will re-list",
			"namespace", key.Namespace, "name", key.Name)
		w.Stop()
	}
}

func (p *BundleProvider) removeWatcher(id int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.watchers, id)
}

// matchesLabels reports whether the given label set matches the selector (a nil
// selector matches everything).
func matchesLabels(selector labels.Selector, lbls map[string]string) bool {
	return selector == nil || selector.Matches(labels.Set(lbls))
}

// bundleWatcher implements watch.Interface for a single registered watcher.
// Events are queued on input (initial snapshot first, then deltas) and forwarded
// to result by the process goroutine; result is closed when the watcher stops,
// which the apiserver watch handler surfaces to the client as the end of the
// watch (triggering a re-list).
type bundleWatcher struct {
	provider      *BundleProvider
	input         chan watch.Event
	result        chan watch.Event
	done          chan struct{}
	labelSelector labels.Selector
	namespace     string
	name          string
	stopOnce      sync.Once
	id            int
}

// process forwards queued events to the result channel until the watcher stops.
// It is the only sender on (and closer of) result.
func (w *bundleWatcher) process() {
	defer close(w.result)
	for {
		select {
		case ev := <-w.input:
			select {
			case w.result <- ev:
			case <-w.done:
				return
			}
		case <-w.done:
			return
		}
	}
}

func (w *bundleWatcher) matchesKey(namespace, name string) bool {
	if w.namespace != "" && w.namespace != namespace {
		return false
	}
	if w.name != "" && w.name != name {
		return false
	}
	return true
}

func (w *bundleWatcher) matchesLabels(lbls labels.Set) bool {
	return w.labelSelector == nil || w.labelSelector.Matches(lbls)
}

// Stop implements watch.Interface. It is idempotent and safe to call from the
// watch handler, the broadcaster (on overflow), or both concurrently.
func (w *bundleWatcher) Stop() {
	w.stopOnce.Do(func() {
		w.provider.removeWatcher(w.id)
		close(w.done)
	})
}

// ResultChan implements watch.Interface.
func (w *bundleWatcher) ResultChan() <-chan watch.Event {
	return w.result
}
