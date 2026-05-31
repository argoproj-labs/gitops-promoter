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

	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	genericapirequest "k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"

	dashboardapi "github.com/argoproj-labs/gitops-promoter/api/dashboard/dashboard"
	dashboardv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/dashboard/v1alpha1"
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
		tableConvertor: rest.NewDefaultTableConvertor(dashboardv1alpha1.Resource("promotionstrategydetails")),
	}
}

// New returns a new (empty) PromotionStrategyDetails (internal type).
func (r *REST) New() runtime.Object {
	return &dashboardapi.PromotionStrategyDetails{}
}

// Destroy releases resources; nothing to do for a virtual resource.
func (r *REST) Destroy() {}

// NewList returns a new (empty) PromotionStrategyDetailsList (internal type).
func (r *REST) NewList() runtime.Object {
	return &dashboardapi.PromotionStrategyDetailsList{}
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

// List returns the bundles for all PromotionStrategies in the request namespace.
func (r *REST) List(ctx context.Context, _ *metainternalversion.ListOptions) (runtime.Object, error) {
	namespace := genericapirequest.NamespaceValue(ctx)
	return r.provider.List(ctx, namespace)
}

// Watch streams bundle changes for the request namespace. When the client does not
// supply a resourceVersion (i.e. a fresh watch), the current set of bundles is sent
// as ADDED events first, followed by deltas.
func (r *REST) Watch(ctx context.Context, options *metainternalversion.ListOptions) (watch.Interface, error) {
	namespace := genericapirequest.NamespaceValue(ctx)
	sendInitial := options == nil || options.ResourceVersion == "" || options.ResourceVersion == "0"

	var name string
	if options != nil && options.FieldSelector != nil {
		if v, ok := options.FieldSelector.RequiresExactMatch("metadata.name"); ok {
			name = v
		}
	}

	return r.provider.Watch(ctx, namespace, name, sendInitial)
}

// ConvertToTable converts objects to a table for kubectl output.
func (r *REST) ConvertToTable(ctx context.Context, object runtime.Object, tableOptions runtime.Object) (*metav1.Table, error) {
	table, err := r.tableConvertor.ConvertToTable(ctx, object, tableOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to convert to table: %w", err)
	}
	return table, nil
}
