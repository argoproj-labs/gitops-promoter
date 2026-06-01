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

// Package dashboard contains the internal ("hub") API types for the dashboard
// aggregation layer, converted to/from the versioned types in the v1alpha1
// sub-package. These types are owned by the k8s code-generators (no kubebuilder
// markers).
//
// The +k8s:deepcopy-gen marker lives here (in doc.go) so deepcopy-gen picks it up as
// the package-level marker; gengo resolves package markers from doc.go, not from
// other files such as register.go.
//
// +k8s:deepcopy-gen=package
package dashboard
