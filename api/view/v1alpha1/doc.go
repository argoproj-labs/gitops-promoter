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
// +k8s:openapi-gen=true
// +k8s:openapi-model-package=io.argoproj.promoter.view.v1alpha1
// +kubebuilder:object:generate=false
//
// The +k8s:openapi-model-package tag makes openapi-gen emit OpenAPIModelName()
// accessors (zz_generated.model_name.go) returning dot-separated, slash-free OpenAPI
// model names (e.g. io.argoproj.promoter.view.v1alpha1.PromotionStrategyDetails).
// Without it the model names default to the Go import path (with slashes), which
// produces $refs that JSON-pointer-escape the slashes (~1) and fail to resolve in
// strict OpenAPI v2 consumers like Argo CD's gnostic parser.
//
// These are aggregated API types served by the extension apiserver, NOT CRDs, so
// they are owned exclusively by the k8s code-generators (deepcopy-gen, openapi-gen)
// via `make generate-apiserver`. The +kubebuilder:object:generate=false opt-outs
// (here at package level, and on each type in types.go) are required because
// controller-gen's object generator treats the legacy deepcopy-gen markers as its
// own enablement switches — +k8s:deepcopy-gen=package at package level and, even
// when the package is opted out, +k8s:deepcopy-gen:interfaces on each type.
// Without the explicit opt-outs (which take precedence at each level),
// `make generate` (and everything that chains it, e.g. `make build` /
// `make test-parallel`) would rewrite zz_generated.deepcopy.go with controller-gen
// output, fighting deepcopy-gen over the file. No other kubebuilder markers
// (+groupName, +kubebuilder:object:root) are used, so controller-gen never tries
// to emit a CRD for this group.

// Package v1alpha1 contains the API types for the view aggregation layer.
// PromotionStrategyDetails is a read-only, server-computed bundle that joins a
// PromotionStrategy with all of its related resources. It is the single served
// version and is also the type the extension apiserver builds and stores in
// memory; there is no separate internal/hub version because the resource has a
// single version and no etcd backing, so no version conversion is ever needed.
package v1alpha1
