/*
Copyright 2026.

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

package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"

	argocd "github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

const (
	// ClusterNameSeparator joins a provider name and a cluster name in the multi provider.
	ClusterNameSeparator = "#"
	// KubeConfigProviderName is the multi provider prefix for remote clusters discovered from kubeconfig Secrets.
	KubeConfigProviderName = "kubeconfig"
	// LocalApplicationsProviderName is the multi provider prefix for the controller's own cluster, engaged only
	// while spec.argocdCommitStatus.watchLocalApplications is true.
	LocalApplicationsProviderName = "local"
)

// LocalApplicationsClusterName is the multicluster name under which the local cluster's Argo CD Applications are
// engaged.
var LocalApplicationsClusterName = multicluster.ClusterName(LocalApplicationsProviderName + ClusterNameSeparator + mcmanager.LocalCluster.String())

// ErrApplicationCRDNotInstalled is returned by LocalApplicationsProvider.Engage when the local cluster has no Argo CD
// Application CRD.
var ErrApplicationCRDNotInstalled = errors.New("the Argo CD Application CRD is not installed on the local cluster")

var (
	_ multicluster.Provider         = &LocalApplicationsProvider{}
	_ multicluster.ProviderRunnable = &LocalApplicationsProvider{}
)

type localApplicationsIndexer struct {
	obj          client.Object
	extractValue client.IndexerFunc
	field        string
}

// LocalApplicationsProvider is a multicluster.Provider that offers the controller's own cluster as a source of Argo CD
// Applications, but only while engaged. The ControllerConfiguration controller engages and disengages it as
// spec.argocdCommitStatus.watchLocalApplications changes, so toggling the setting needs no restart and the Application
// CRD is only required on the local cluster while the setting is on.
//
// The engaged cluster is a dedicated cluster.Cluster with an Application-only cache, separate from the host manager.
// That keeps the host cache free of any Application informer: the host manager starts without the CRD, and
// disengaging stops the Application watch instead of leaving an informer behind.
//
//nolint:containedctx // runCtx is the provider's lifetime (from Start), not a request context.
type LocalApplicationsProvider struct {
	restConfig *rest.Config
	// started is closed once Start has recorded runCtx and aware.
	started chan struct{}

	// Guarded by lock.
	runCtx   context.Context
	aware    multicluster.Aware
	cl       cluster.Cluster
	cancel   context.CancelFunc
	indexers []localApplicationsIndexer

	clusterOptions []cluster.Option
	lock           sync.RWMutex
}

// NewLocalApplicationsProvider returns a provider that, once engaged, watches Argo CD Applications on the cluster
// described by restConfig using a cluster built from clusterOptions.
func NewLocalApplicationsProvider(restConfig *rest.Config, clusterOptions ...cluster.Option) *LocalApplicationsProvider {
	return &LocalApplicationsProvider{
		restConfig:     restConfig,
		clusterOptions: clusterOptions,
		started:        make(chan struct{}),
	}
}

// Start implements multicluster.ProviderRunnable. It records the Aware used to engage the local cluster and blocks
// until ctx is done, then disengages.
func (p *LocalApplicationsProvider) Start(ctx context.Context, aware multicluster.Aware) error {
	p.lock.Lock()
	p.runCtx = ctx
	p.aware = aware
	p.lock.Unlock()
	close(p.started)

	<-ctx.Done()
	p.Disengage()
	return nil
}

// Engaged reports whether the local cluster is currently engaged.
func (p *LocalApplicationsProvider) Engaged() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.cl != nil
}

// Engage starts watching Argo CD Applications on the local cluster. It is a no-op if already engaged, and returns
// ErrApplicationCRDNotInstalled when the CRD is absent. The multicluster manager starts providers after its other
// runnables, so Engage waits for Start (or for ctx to end) before engaging.
func (p *LocalApplicationsProvider) Engage(ctx context.Context) error {
	select {
	case <-p.started:
	case <-ctx.Done():
		return fmt.Errorf("waiting for the local Applications provider to start: %w", ctx.Err())
	}

	return p.engage() //nolint:contextcheck // the engagement must outlive the caller, so it inherits the provider-lifetime context instead
}

// engage does the work of Engage once the provider has started. The engagement lives as long as the provider (runCtx)
// rather than the caller's request, so it takes no context of its own.
func (p *LocalApplicationsProvider) engage() error {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.cl != nil {
		return nil
	}

	// Check before cluster.New: its cache RESTMaps the Application type and would fail with a less specific error.
	if err := p.checkApplicationCRDInstalled(); err != nil {
		return err
	}

	cl, err := cluster.New(p.restConfig, p.clusterOptions...)
	if err != nil {
		return fmt.Errorf("failed to create local Applications cluster: %w", err)
	}

	clusterCtx, cancel := context.WithCancel(p.runCtx)
	for _, idx := range p.indexers {
		if err := cl.GetFieldIndexer().IndexField(clusterCtx, idx.obj, idx.field, idx.extractValue); err != nil {
			cancel()
			return fmt.Errorf("failed to index field %q on the local Applications cluster: %w", idx.field, err)
		}
	}

	go func() {
		if err := cl.Start(clusterCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.FromContext(clusterCtx).Error(err, "local Applications cluster stopped")
		}
	}()
	if !cl.GetCache().WaitForCacheSync(clusterCtx) {
		cancel()
		return errors.New("failed to sync the local Applications cluster cache")
	}

	// The Aware is wrapped by the multi provider, so this engages as LocalApplicationsClusterName.
	if err := p.aware.Engage(clusterCtx, mcmanager.LocalCluster, cl); err != nil {
		cancel()
		return fmt.Errorf("failed to engage the local Applications cluster: %w", err)
	}

	p.cl = cl
	p.cancel = cancel
	return nil
}

// checkApplicationCRDInstalled returns ErrApplicationCRDNotInstalled if the local cluster has no Argo CD Application
// CRD. It uses a fresh RESTMapper each time so a CRD installed after startup is found.
func (p *LocalApplicationsProvider) checkApplicationCRDInstalled() error {
	httpClient, err := rest.HTTPClientFor(p.restConfig)
	if err != nil {
		return fmt.Errorf("failed to create HTTP client to discover the Argo CD Application CRD: %w", err)
	}
	mapper, err := apiutil.NewDynamicRESTMapper(p.restConfig, httpClient)
	if err != nil {
		return fmt.Errorf("failed to create RESTMapper to discover the Argo CD Application CRD: %w", err)
	}
	_, err = mapper.RESTMapping(schema.GroupKind{
		Group: argocd.SchemeGroupVersion.Group,
		Kind:  "Application",
	}, argocd.SchemeGroupVersion.Version)
	if meta.IsNoMatchError(err) {
		return ErrApplicationCRDNotInstalled
	}
	if err != nil {
		return fmt.Errorf("failed to look up the Argo CD Application CRD: %w", err)
	}
	return nil
}

// Disengage stops watching Argo CD Applications on the local cluster. It is a no-op if not engaged.
func (p *LocalApplicationsProvider) Disengage() {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.cl == nil {
		return
	}
	// Cancelling the cluster context stops its cache and tells multicluster-runtime to drop the engagement.
	p.cancel()
	p.cl = nil
	p.cancel = nil
}

// Get implements multicluster.Provider. It returns the local cluster only while engaged.
func (p *LocalApplicationsProvider) Get(_ context.Context, clusterName multicluster.ClusterName) (cluster.Cluster, error) {
	if clusterName != mcmanager.LocalCluster {
		return nil, multicluster.ErrClusterNotFound
	}

	p.lock.RLock()
	defer p.lock.RUnlock()

	if p.cl == nil {
		return nil, multicluster.ErrClusterNotFound
	}
	return p.cl, nil
}

// IndexField implements multicluster.Provider. The index is applied now if engaged, and on every later engagement.
func (p *LocalApplicationsProvider) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.indexers = append(p.indexers, localApplicationsIndexer{obj: obj, field: field, extractValue: extractValue})

	if p.cl == nil {
		return nil
	}
	if err := p.cl.GetFieldIndexer().IndexField(ctx, obj, field, extractValue); err != nil {
		return fmt.Errorf("failed to index field %q on the local Applications cluster: %w", field, err)
	}
	return nil
}
