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
	"fmt"
	"net"

	"github.com/spf13/pflag"
	"k8s.io/apiserver/pkg/admission"
	genericapiserver "k8s.io/apiserver/pkg/server"
	genericoptions "k8s.io/apiserver/pkg/server/options"

	dashboardv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/dashboard/v1alpha1"
)

// Options holds the configuration for the dashboard extension apiserver. It wraps
// the generic RecommendedOptions but disables etcd because the served resource is
// virtual (computed on the fly from a controller-runtime cache).
type Options struct {
	RecommendedOptions *genericoptions.RecommendedOptions
}

// NewOptions returns Options with sane defaults for the dashboard apiserver.
func NewOptions() *Options {
	o := &Options{
		RecommendedOptions: genericoptions.NewRecommendedOptions(
			"",
			Codecs.LegacyCodec(dashboardv1alpha1.SchemeGroupVersion),
		),
	}
	// Virtual resource: nothing is persisted, so there is no etcd backend.
	o.RecommendedOptions.Etcd = nil
	// We perform no admission on a read-only virtual resource.
	o.RecommendedOptions.Admission = nil
	// Disable the CoreAPI loopback-informer wiring. It is unused by this read-only,
	// no-admission server and its --kubeconfig flag would collide with the root
	// command's persistent --kubeconfig. Delegated authn/authz use their own
	// --authentication-kubeconfig/--authorization-kubeconfig flags (or in-cluster).
	o.RecommendedOptions.CoreAPI = nil
	// Required: ApplyTo always invokes ExtraAdmissionInitializers even when
	// Admission is nil, so provide a no-op.
	o.RecommendedOptions.ExtraAdmissionInitializers = func(*genericapiserver.RecommendedConfig) ([]admission.PluginInitializer, error) {
		return nil, nil
	}
	return o
}

// AddFlags registers the apiserver flags (secure serving, delegated auth, etc.).
func (o *Options) AddFlags(fs *pflag.FlagSet) {
	o.RecommendedOptions.AddFlags(fs)
}

// Validate validates the options.
func (o *Options) Validate() error {
	errs := o.RecommendedOptions.Validate()
	if len(errs) > 0 {
		return fmt.Errorf("invalid apiserver options: %v", errs)
	}
	return nil
}

// Config builds the apiserver Config from the options. The provided BundleProvider
// backs the REST storage and watch fan-out.
func (o *Options) Config(provider *BundleProvider) (*Config, error) {
	// Allow running without externally-provided certs (dev/local); production
	// deployments mount a serving cert and pass --tls-cert-file/--tls-private-key-file.
	if err := o.RecommendedOptions.SecureServing.MaybeDefaultWithSelfSignedCerts(
		"localhost", nil, []net.IP{net.ParseIP("127.0.0.1")},
	); err != nil {
		return nil, fmt.Errorf("error creating self-signed certificates: %w", err)
	}

	serverConfig := genericapiserver.NewRecommendedConfig(Codecs)
	if err := o.RecommendedOptions.ApplyTo(serverConfig); err != nil {
		return nil, fmt.Errorf("error applying recommended options: %w", err)
	}

	config := &Config{
		GenericConfig: serverConfig,
		ExtraConfig: ExtraConfig{
			Provider: provider,
		},
	}
	return config, nil
}
