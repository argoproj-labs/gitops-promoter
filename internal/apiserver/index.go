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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	toolscache "k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	viewv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/view/v1alpha1"
)

var log = ctrl.Log.WithName("dashboard-apiserver")

// debounceInterval coalesces bursts of child changes for the same PromotionStrategy.
const debounceInterval = 250 * time.Millisecond

// BundleProvider builds PromotionStrategyDetails bundles from a read-only
// controller-runtime cache, and fans out watch events to registered watchers when
// any related resource changes.
type BundleProvider struct {
	cache    cache.Cache
	reader   client.Reader
	queue    workqueue.TypedRateLimitingInterface[types.NamespacedName]
	watchers map[int]*bundleWatcher
	known    map[types.NamespacedName]bool

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
		known:    map[types.NamespacedName]bool{},
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
	return []client.Object{
		&promoterv1alpha1.PromotionStrategy{},
		&promoterv1alpha1.ChangeTransferPolicy{},
		&promoterv1alpha1.PullRequest{},
		&promoterv1alpha1.CommitStatus{},
		&promoterv1alpha1.ArgoCDCommitStatus{},
		&promoterv1alpha1.GitCommitStatus{},
		&promoterv1alpha1.TimedCommitStatus{},
		&promoterv1alpha1.WebRequestCommitStatus{},
		&promoterv1alpha1.GitRepository{},
		&promoterv1alpha1.ScmProvider{},
		&promoterv1alpha1.ClusterScmProvider{},
	}
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
	p.reconcileKey(ctx, key)
	p.queue.Forget(key)
	return true
}

// reconcileKey rebuilds the bundle for a PromotionStrategy key and broadcasts the
// resulting watch event (ADDED/MODIFIED/DELETED) to matching watchers.
func (p *BundleProvider) reconcileKey(ctx context.Context, key types.NamespacedName) {
	bundle, err := buildBundle(ctx, p.reader, key.Namespace, key.Name, p.nextResourceVersion())
	if apierrors.IsNotFound(err) {
		p.mu.Lock()
		wasKnown := p.known[key]
		delete(p.known, key)
		p.mu.Unlock()
		if wasKnown {
			tombstone := &viewv1alpha1.PromotionStrategyDetails{
				ObjectMeta: metav1.ObjectMeta{
					Name:            key.Name,
					Namespace:       key.Namespace,
					ResourceVersion: p.currentResourceVersion(),
				},
			}
			p.broadcast(watch.Event{Type: watch.Deleted, Object: tombstone}, key)
		}
		return
	}
	if err != nil {
		log.Error(err, "failed to build bundle", "namespace", key.Namespace, "name", key.Name)
		return
	}

	p.mu.Lock()
	eventType := watch.Modified
	if !p.known[key] {
		eventType = watch.Added
		p.known[key] = true
	}
	p.mu.Unlock()

	p.broadcast(watch.Event{Type: eventType, Object: bundle}, key)
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
	case *promoterv1alpha1.ArgoCDCommitStatus:
		return keyFromRef(o.Namespace, o.Spec.PromotionStrategyRef.Name)
	case *promoterv1alpha1.GitCommitStatus:
		return keyFromRef(o.Namespace, o.Spec.PromotionStrategyRef.Name)
	case *promoterv1alpha1.TimedCommitStatus:
		return keyFromRef(o.Namespace, o.Spec.PromotionStrategyRef.Name)
	case *promoterv1alpha1.WebRequestCommitStatus:
		return keyFromRef(o.Namespace, o.Spec.PromotionStrategyRef.Name)
	case *promoterv1alpha1.GitRepository:
		return p.keysReferencingGitRepository(ctx, o.Namespace, o.Name)
	case *promoterv1alpha1.ScmProvider:
		return p.keysReferencingScmProvider(ctx, "ScmProvider", o.Namespace, o.Name)
	case *promoterv1alpha1.ClusterScmProvider:
		return p.keysReferencingScmProvider(ctx, "ClusterScmProvider", "", o.Name)
	default:
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

// List builds bundles for all PromotionStrategies in the given namespace (all
// namespaces when namespace is empty).
func (p *BundleProvider) List(ctx context.Context, namespace string) (*viewv1alpha1.PromotionStrategyDetailsList, error) {
	psList := &promoterv1alpha1.PromotionStrategyList{}
	var listOpts []client.ListOption
	if namespace != "" {
		listOpts = append(listOpts, client.InNamespace(namespace))
	}
	if err := p.reader.List(ctx, psList, listOpts...); err != nil {
		return nil, fmt.Errorf("failed to list PromotionStrategies: %w", err)
	}

	rv := p.currentResourceVersion()
	out := &viewv1alpha1.PromotionStrategyDetailsList{
		ListMeta: metav1.ListMeta{ResourceVersion: rv},
	}
	for i := range psList.Items {
		ps := &psList.Items[i]
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

// Watch registers a watcher for the given namespace (and optional name filter).
// When sendInitial is true, the current set of bundles is delivered as ADDED events
// before subsequent deltas stream in. When sendInitialEventsBookmark is also true
// (client-go "watch list" protocol), a terminating Bookmark event annotated with
// k8s.io/initial-events-end is sent after the snapshot so reflectors know the initial
// list is complete.
func (p *BundleProvider) Watch(ctx context.Context, namespace, name string, sendInitial, sendInitialEventsBookmark bool) (watch.Interface, error) {
	w := &bundleWatcher{
		namespace: namespace,
		name:      name,
		result:    make(chan watch.Event, 64),
		provider:  p,
	}

	p.mu.Lock()
	p.nextID++
	w.id = p.nextID
	p.watchers[w.id] = w
	p.mu.Unlock()

	if sendInitial {
		var items []viewv1alpha1.PromotionStrategyDetails
		var resourceVersion string
		if name != "" {
			// A name filter resolves to exactly one bundle, so Get it directly
			// instead of listing (and building) every PromotionStrategy in the
			// namespace just to discard all but one.
			bundle, err := p.Get(ctx, namespace, name)
			if err != nil {
				if !apierrors.IsNotFound(err) {
					w.Stop()
					return nil, err
				}
				// Not found yet: send no initial ADDED event, but still surface
				// a resource version so the reflector can begin watching.
				resourceVersion = p.currentResourceVersion()
			} else {
				items = []viewv1alpha1.PromotionStrategyDetails{*bundle}
				resourceVersion = bundle.ResourceVersion
			}
		} else {
			list, err := p.List(ctx, namespace)
			if err != nil {
				w.Stop()
				return nil, err
			}
			items = list.Items
			resourceVersion = list.ResourceVersion
		}

		for i := range items {
 com			item := items[i]
			select {
			case w.result <- watch.Event{Type: watch.Added, Object: &item}:
			default:
			}
		}

		if sendInitialEventsBookmark {
			bookmark := &viewv1alpha1.PromotionStrategyDetails{}
			bookmark.SetResourceVersion(resourceVersion)
			bookmark.SetAnnotations(map[string]string{metav1.InitialEventsAnnotationKey: "true"})
			select {
			case w.result <- watch.Event{Type: watch.Bookmark, Object: bookmark}:
			default:
			}
		}
	}

	return w, nil
}

// broadcast sends an event to all watchers matching the affected key.
func (p *BundleProvider) broadcast(ev watch.Event, key types.NamespacedName) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, w := range p.watchers {
		if !w.matches(key.Namespace, key.Name) {
			continue
		}
		evCopy := watch.Event{Type: ev.Type, Object: ev.Object.DeepCopyObject()}
		select {
		case w.result <- evCopy:
		default:
			log.Info("dropping watch event; watcher buffer full", "namespace", key.Namespace, "name", key.Name)
		}
	}
}

func (p *BundleProvider) removeWatcher(id int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if w, ok := p.watchers[id]; ok {
		delete(p.watchers, id)
		close(w.result)
	}
}

// bundleWatcher implements watch.Interface for a single registered watcher.
type bundleWatcher struct {
	provider  *BundleProvider
	result    chan watch.Event
	namespace string
	name      string
	stopOnce  sync.Once
	id        int
}

func (w *bundleWatcher) matches(namespace, name string) bool {
	if w.namespace != "" && w.namespace != namespace {
		return false
	}
	if w.name != "" && w.name != name {
		return false
	}
	return true
}

// Stop implements watch.Interface.
func (w *bundleWatcher) Stop() {
	w.stopOnce.Do(func() {
		w.provider.removeWatcher(w.id)
	})
}

// ResultChan implements watch.Interface.
func (w *bundleWatcher) ResultChan() <-chan watch.Event {
	return w.result
}
