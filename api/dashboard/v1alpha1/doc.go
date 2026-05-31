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

// +k8s:deepcopy-gen=package
// +k8s:conversion-gen=github.com/argoproj-labs/gitops-promoter/api/dashboard/dashboard
// +k8s:openapi-gen=true
//
// These are aggregated API types served by the extension apiserver, NOT CRDs, so
// they are owned exclusively by the k8s code-generators (deepcopy-gen, conversion-gen,
// openapi-gen) via `make generate-apiserver`. They intentionally carry NO kubebuilder
// markers (no +groupName, +kubebuilder:object:*), so controller-gen (`make generate`
// / `make manifests`) skips this group entirely and never tries to emit a CRD for it.

// Package v1alpha1 contains the external (served) API types for the dashboard
// aggregation layer. PromotionStrategyDetails is a read-only, server-computed
// bundle that joins a PromotionStrategy with all of its related resources.
package v1alpha1
