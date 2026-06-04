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

package controller

import (
	"fmt"
	"os"
	"path/filepath"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// loadShippedControllerConfigurationForTests loads config/config/controllerconfiguration.yaml
// (the same manifest wired into kustomize) and applies test metadata. Tests run with the
// working directory set to this package (standard `go test`), so the path is resolved from
// internal/controller.
func loadShippedControllerConfigurationForTests(namespace, name string) (*promoterv1alpha1.ControllerConfiguration, error) {
	path := filepath.Join("..", "..", "config", "config", "controllerconfiguration.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read shipped controller configuration %s: %w", path, err)
	}
	cc := &promoterv1alpha1.ControllerConfiguration{}
	if err := unmarshalYamlStrict(string(data), cc); err != nil {
		return nil, err
	}
	cc.ObjectMeta = metav1.ObjectMeta{
		Namespace:   namespace,
		Name:        name,
		Labels:      cc.Labels,
		Annotations: cc.Annotations,
	}
	return cc, nil
}
