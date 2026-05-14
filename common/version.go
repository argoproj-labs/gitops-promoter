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

// Package common provides shared constants and variables for the GitOps Promoter.
package common

// version is set at build time by goreleaser via:
//
//	-X github.com/argoproj-labs/gitops-promoter/common.version={{.Version}}
var version = "unknown"

// buildDate is set at build time by goreleaser via:
//
//	-X github.com/argoproj-labs/gitops-promoter/common.buildDate={{.Date}}
var buildDate = "unknown"

// Version returns the current application version.
// Returns "unknown" when the binary was not built with release tooling.
func Version() string { return version }

// BuildDate returns the build date of the binary.
// Returns "unknown" when the binary was not built with release tooling.
func BuildDate() string { return buildDate }
