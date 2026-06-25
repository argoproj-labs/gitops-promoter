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
	"errors"
	"fmt"

	"golang.org/x/sync/errgroup"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	clientrest "k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// dashboardRuntime holds the components started by Run.
type dashboardRuntime struct {
	readCache cache.Cache
	provider  *BundleProvider
	server    *PromoterAPIServer
}

// newDashboardRuntime wires the read cache, bundle provider, and extension apiserver.
// authz, when non-nil, overrides delegated authorization (used by tests).
func newDashboardRuntime(ctx context.Context, restConfig *clientrest.Config, opts *Options, authz authorizer.Authorizer) (*dashboardRuntime, error) {
	readCache, err := cache.New(restConfig, cache.Options{
		Scheme:           utils.GetScheme(),
		DefaultTransform: cacheTransform(),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create read cache: %w", err)
	}

	provider := NewBundleProvider(readCache)
	if err := provider.SetupInformers(ctx); err != nil {
		return nil, fmt.Errorf("failed to set up informers: %w", err)
	}

	config, err := opts.Config(provider)
	if err != nil {
		return nil, fmt.Errorf("failed to build apiserver config: %w", err)
	}
	if authz != nil {
		config.GenericConfig.Authorization.Authorizer = authz
	}

	server, err := config.Complete().New()
	if err != nil {
		return nil, fmt.Errorf("failed to create apiserver: %w", err)
	}

	return &dashboardRuntime{
		readCache: readCache,
		provider:  provider,
		server:    server,
	}, nil
}

// run starts the read cache, bundle provider workqueue, and extension apiserver until ctx is cancelled.
func (d *dashboardRuntime) run(ctx context.Context) error {
	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		if err := d.readCache.Start(ctx); err != nil {
			return fmt.Errorf("read cache stopped: %w", err)
		}
		return nil
	})

	if !d.readCache.WaitForCacheSync(ctx) {
		_ = g.Wait()
		return errors.New("failed to sync read cache")
	}

	g.Go(func() error {
		d.provider.Run(ctx)
		return nil
	})

	g.Go(func() error {
		if err := d.server.GenericAPIServer.PrepareRun().RunWithContext(ctx); err != nil {
			return fmt.Errorf("apiserver stopped: %w", err)
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		return fmt.Errorf("apiserver run group exited: %w", err)
	}
	return nil
}

// Run wires up the read-only controller-runtime cache, the BundleProvider (watch
// fan-out + debounced rebuilds), and the generic extension apiserver, then serves
// until ctx is cancelled.
func Run(ctx context.Context, restConfig *clientrest.Config, opts *Options) error {
	if err := opts.Validate(); err != nil {
		return err
	}

	runtime, err := newDashboardRuntime(ctx, restConfig, opts, nil)
	if err != nil {
		return err
	}
	return runtime.run(ctx)
}
