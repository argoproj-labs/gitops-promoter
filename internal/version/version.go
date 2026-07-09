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

// Package version holds the version of the gitops-promoter binary.
package version

import "runtime/debug"

// shortRevisionLength is the number of characters to use from a VCS revision hash.
const shortRevisionLength = 7

// buildVersion may be set at build time via -ldflags (e.g. by goreleaser).
var buildVersion string //nolint:gochecknoglobals

// Version is the gitops-promoter version.
// If buildVersion was set via -ldflags it is used directly.
// Otherwise the version is derived from the embedded build info provided by
// the Go toolchain (VCS revision / dirty flag), and falls back to
// "v0.0.0+unknown" when no information is available.
var Version = GetVersion() //nolint:gochecknoglobals

// GetVersion returns the version string for the gitops-promoter binary.
func GetVersion() string {
	if buildVersion != "" {
		return buildVersion
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "v0.0.0+unknown"
	}
	// Fall back to VCS settings embedded by the Go toolchain (Go 1.18+).
	var revision string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) > shortRevisionLength {
				revision = s.Value[:shortRevisionLength]
			} else {
				revision = s.Value
			}
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision != "" {
		if modified {
			return revision + "-dirty"
		}
		return revision
	}
	return "v0.0.0+unknown"
}
