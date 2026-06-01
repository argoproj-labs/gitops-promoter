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
//
// The +k8s:openapi-gen marker is here (in doc.go) so openapi-gen picks it up as the
// package-level marker — it does not resolve package markers from other files such as
// groupversion_info.go. The dashboard aggregation apiserver embeds these types in its
// served PromotionStrategyDetails bundle and needs their OpenAPI definitions; this
// marker is inert for controller-gen.
package v1alpha1
