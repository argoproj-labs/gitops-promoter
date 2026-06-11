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

// Package v1alpha1 contains API Schema definitions for the promoter v1alpha1 API group
// +kubebuilder:object:generate=true
// +kubebuilder:ac:generate=true
// +kubebuilder:ac:output:package=../../applyconfiguration
// +groupName=promoter.argoproj.io
// +k8s:openapi-gen=true
// +k8s:openapi-model-package=io.argoproj.promoter.v1alpha1
//
// The +k8s:openapi-* markers let openapi-gen emit definitions and slash-free
// OpenAPIModelName() accessors (zz_generated.model_name.go) for these types, which
// the dashboard aggregation apiserver embeds in its PromotionStrategyDetails bundle.
// The model package (io.argoproj.promoter.v1alpha1) intentionally matches the names
// these CRDs already publish in the cluster OpenAPI, since both schemas derive from
// the same Go types. These markers are inert for controller-gen.
package v1alpha1
