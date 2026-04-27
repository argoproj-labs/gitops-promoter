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

// Package cli loads YAML bundles for local WebRequestCommitStatus template simulation.
package cli

import (
	"github.com/argoproj-labs/gitops-promoter/webrequestsimulator/simulatortypes"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SimulatorConfigKind is the Kind value for the optional third YAML document (not a CRD).
const SimulatorConfigKind = "SimulatorConfig"

// SimulatorConfig is the optional third document in a template bundle. It is not applied
// to a cluster; it only supplies namespace metadata and HTTP mocks for Simulate.
type SimulatorConfig struct {
	metav1.TypeMeta `json:",inline" yaml:",inline"`

	NamespaceMetadata *simulatortypes.NamespaceMetadata `yaml:"namespaceMetadata,omitempty"`
	HTTPResponses     []simulatortypes.HTTPResponse     `yaml:"httpResponses,omitempty"`
}
