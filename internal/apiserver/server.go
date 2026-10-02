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

	"k8s.io/apiserver/pkg/registry/rest"
	genericapiserver "k8s.io/apiserver/pkg/server"

	viewv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/view/v1alpha1"
)

// ExtraConfig holds dashboard-specific apiserver configuration.
type ExtraConfig struct {
	// Provider backs the REST storage and the watch fan-out.
	Provider *BundleProvider
}

// Config is the configuration for the dashboard extension apiserver.
type Config struct {
	GenericConfig *genericapiserver.RecommendedConfig
	ExtraConfig   ExtraConfig
}

type completedConfig struct {
	GenericConfig genericapiserver.CompletedConfig
	ExtraConfig   *ExtraConfig
}

// CompletedConfig is a completed Config, ready to build a server.
type CompletedConfig struct {
	// embed a private pointer so this cannot be instantiated outside this package
	*completedConfig
}

// PromoterAPIServer is the dashboard extension apiserver.
type PromoterAPIServer struct {
	GenericAPIServer *genericapiserver.GenericAPIServer
	Provider         *BundleProvider
}

// Complete fills in defaults and returns a CompletedConfig.
func (c *Config) Complete() CompletedConfig {
	completed := completedConfig{
		c.GenericConfig.Complete(),
		&c.ExtraConfig,
	}
	return CompletedConfig{&completed}
}

// New builds a PromoterAPIServer from the completed config, installing the
// dashboard API group with the PromotionStrategyDetails virtual resource.
func (c CompletedConfig) New() (*PromoterAPIServer, error) {
	genericServer, err := c.GenericConfig.New("promoter-dashboard-apiserver", genericapiserver.NewEmptyDelegate())
	if err != nil {
		return nil, fmt.Errorf("failed to create generic apiserver: %w", err)
	}

	s := &PromoterAPIServer{
		GenericAPIServer: genericServer,
		Provider:         c.ExtraConfig.Provider,
	}

	apiGroupInfo := genericapiserver.NewDefaultAPIGroupInfo(viewv1alpha1.GroupName, Scheme, ParameterCodec, Codecs)

	store := NewREST(c.ExtraConfig.Provider)
	v1alpha1storage := map[string]rest.Storage{
		"promotionstrategydetails": store,
	}
	apiGroupInfo.VersionedResourcesStorageMap["v1alpha1"] = v1alpha1storage

	if err := genericServer.InstallAPIGroup(&apiGroupInfo); err != nil {
		return nil, fmt.Errorf("failed to install dashboard API group: %w", err)
	}

	return s, nil
}
