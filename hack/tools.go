//go:build tools

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

// Package tools pins build-time code generators used by `make generate-apiserver`
// for the dashboard aggregation API (api/dashboard/...). It is never compiled into
// the binary (guarded by the "tools" build tag); it only keeps these modules in
// go.mod so the generators can be `go run`/`go install`ed at pinned versions.
package tools

import (
	_ "k8s.io/code-generator/cmd/conversion-gen"
	_ "k8s.io/code-generator/cmd/deepcopy-gen"
	_ "k8s.io/code-generator/cmd/defaulter-gen"
	_ "k8s.io/kube-openapi/cmd/openapi-gen"
)
