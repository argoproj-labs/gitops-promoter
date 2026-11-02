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
	"os"
	"time"

	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/authorization/authorizer"
	clientrest "k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/controller"
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
	cacheOpts := cache.Options{
		Scheme:           utils.GetScheme(),
		DefaultTransform: cacheTransform(),
	}
	if opts.Namespace != "" {
		cacheOpts.DefaultNamespaces = map[string]cache.Config{opts.Namespace: {}}
	}
	readCache, err := cache.New(restConfig, cacheOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create read cache: %w", err)
	}

	if err := controller.RegisterGatePromotionStrategyRefFieldIndexes(ctx, readCache); err != nil {
		return nil, fmt.Errorf("failed to register gate field indexes: %w", err)
	}

	provider := NewBundleProvider(readCache, opts.Namespace, opts.ClusterScmProviderMode != promoterv1alpha1.FeatureModeDisabled)
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

	if opts.WaitForServingCert {
		certKey := opts.RecommendedOptions.SecureServing.ServerCert.CertKey
		// The serving cert files must exist before the apiserver config is built (they are
		// loaded eagerly and then reloaded on change). With the built-in cert rotation the
		// controller-manager populates the mounted Secret after this pod starts.
		if err := waitForServingCert(ctx, certKey.CertFile, certKey.KeyFile, servingCertPollInterval); err != nil {
			if errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
	}

	runtime, err := newDashboardRuntime(ctx, restConfig, opts, nil)
	if err != nil {
		return err
	}
	return runtime.run(ctx)
}

const servingCertPollInterval = 2 * time.Second

// waitForServingCert blocks until certFile and keyFile both exist and are non-empty, or ctx
// is done.
func waitForServingCert(ctx context.Context, certFile, keyFile string, interval time.Duration) error {
	present := func() bool {
		for _, f := range []string{certFile, keyFile} {
			if st, err := os.Stat(f); err != nil || st.Size() == 0 {
				return false
			}
		}
		return true
	}
	if present() {
		return nil
	}
	log.Info("waiting for the serving certificate to be mounted", "certFile", certFile, "keyFile", keyFile)
	err := wait.PollUntilContextCancel(ctx, interval, false, func(context.Context) (bool, error) {
		return present(), nil
	})
	if err != nil {
		return fmt.Errorf("waiting for serving certificate: %w", err)
	}
	log.Info("serving certificate is mounted")
	return nil
}
