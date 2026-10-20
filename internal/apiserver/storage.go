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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/watch"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"

	viewv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/view/v1alpha1"
)

// REST is a hand-rolled, read-only rest.Storage for PromotionStrategyDetails. The
// resource is virtual: there is no etcd backing. Objects are computed on demand by
// the BundleProvider, and watch events are fed from the provider's fan-out.
type REST struct {
	provider       *BundleProvider
	tableConvertor rest.TableConvertor
}

// Compile-time assertions that REST implements the required interfaces.
var (
	_ rest.Storage              = &REST{}
	_ rest.Getter               = &REST{}
	_ rest.Lister               = &REST{}
	_ rest.Watcher              = &REST{}
	_ rest.Scoper               = &REST{}
	_ rest.SingularNameProvider = &REST{}
	_ rest.TableConvertor       = &REST{}
)

// NewREST creates a new REST storage backed by the given provider.
func NewREST(provider *BundleProvider) *REST {
	return &REST{
		provider:       provider,
		tableConvertor: rest.NewDefaultTableConvertor(viewv1alpha1.Resource("promotionstrategydetails")),
	}
}

// New returns a new (empty) PromotionStrategyDetails.
func (r *REST) New() runtime.Object {
	return &viewv1alpha1.PromotionStrategyDetails{}
}

// Destroy releases resources; nothing to do for a virtual resource.
func (r *REST) Destroy() {}

// NewList returns a new (empty) PromotionStrategyDetailsList.
func (r *REST) NewList() runtime.Object {
	return &viewv1alpha1.PromotionStrategyDetailsList{}
}

// NamespaceScoped reports that PromotionStrategyDetails is namespaced.
func (r *REST) NamespaceScoped() bool {
	return true
}

// GetSingularName returns the singular resource name.
func (r *REST) GetSingularName() string {
	return "promotionstrategydetails"
}

// Get returns the bundle for the named PromotionStrategy in the request namespace.
func (r *REST) Get(ctx context.Context, name string, _ *metav1.GetOptions) (runtime.Object, error) {
	namespace := genericapirequest.NamespaceValue(ctx)
	return r.provider.Get(ctx, namespace, name)
}

// List returns the bundles for the PromotionStrategies in the request namespace,
// honoring the label selector and the metadata.name field selector. Unsupported
// field selectors are rejected rather than silently ignored.
func (r *REST) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	namespace := genericapirequest.NamespaceValue(ctx)
	name, err := nameFromFieldSelector(options)
	if err != nil {
		return nil, err
	}
	return r.provider.List(ctx, namespace, name, labelSelectorFromOptions(options))
}

// Watch streams bundle changes for the request namespace.
//
// It supports the client-go "watch list" protocol (sendInitialEvents): the current
// set of bundles is delivered as ADDED events, terminated by a Bookmark event marked
// with the k8s.io/initial-events-end annotation, after which deltas stream. Modern
// reflectors (client-go v0.32+) hang waiting for that bookmark, so emitting it is
// required for the dashboard's controller-runtime cache to sync. Plain watches
// (resourceVersion == "" / "0") also get the initial snapshot, without the bookmark.
func (r *REST) Watch(ctx context.Context, options *metainternalversion.ListOptions) (watch.Interface, error) {
	namespace := genericapirequest.NamespaceValue(ctx)

	sendInitialEvents := options != nil && options.SendInitialEvents != nil && *options.SendInitialEvents
	allowWatchBookmarks := options != nil && options.AllowWatchBookmarks
	sendInitial := sendInitialEvents || options == nil || options.ResourceVersion == "" || options.ResourceVersion == "0"

	name, err := nameFromFieldSelector(options)
	if err != nil {
		return nil, err
	}

	// Only emit the terminating initial-events-end bookmark for a genuine watch-list
	// request, i.e. SendInitialEvents AND AllowWatchBookmarks. This mirrors the
	// apiserver's own isListWatchRequest gate: the watch handler installs its
	// watchlist-complete hook only for that combination, and an annotated bookmark on
	// any other request makes the handler invoke a nil hook and panic.
	return r.provider.Watch(ctx, namespace, name, labelSelectorFromOptions(options), sendInitial, sendInitialEvents && allowWatchBookmarks)
}

// labelSelectorFromOptions extracts the label selector, normalizing "everything"
// to nil so the provider can skip matching entirely.
func labelSelectorFromOptions(options *metainternalversion.ListOptions) labels.Selector {
	if options == nil || options.LabelSelector == nil || options.LabelSelector.Empty() {
		return nil
	}
	return options.LabelSelector
}

// nameFromFieldSelector extracts an exact metadata.name match from the field
// selector. Any other field selector requirement is rejected with a 400 — this
// virtual resource has no other selectable fields, and silently ignoring a
// selector would return results the client explicitly filtered out.
func nameFromFieldSelector(options *metainternalversion.ListOptions) (string, error) {
	if options == nil || options.FieldSelector == nil || options.FieldSelector.Empty() {
		return "", nil
	}
	var name string
	for _, req := range options.FieldSelector.Requirements() {
		if req.Field == "metadata.name" && (req.Operator == selection.Equals || req.Operator == selection.DoubleEquals) {
			name = req.Value
			continue
		}
		return "", apierrors.NewBadRequest(fmt.Sprintf(
			"field selector %q is not supported on promotionstrategydetails; only an exact metadata.name match is allowed",
			req.Field))
	}
	return name, nil
}

// ConvertToTable converts objects to a table for kubectl output.
func (r *REST) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	table, err := r.tableConvertor.ConvertToTable(ctx, object, tableOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to table: %w", err)
	}
	return table, nil
}
