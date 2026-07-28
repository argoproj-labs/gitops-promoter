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

package version

import (
	"fmt"
	"runtime"
)

// CommandCLI is the user-facing name of the gitops-promoter CLI binary.
const CommandCLI = "gitops-promoter"

// Version information set by link flags during build. These defaults apply for
// local builds (for example `make build` or `go run`) that do not pass ldflags.
var (
	version   = "dev"
	buildDate = "unknown"
)

// Info contains gitops-promoter version information.
type Info struct {
	Version   string
	BuildDate string
	GoVersion string
	Compiler  string
	Platform  string
}

// Get returns the version information for the current binary.
func Get() Info {
	return Info{
		Version:   version,
		BuildDate: buildDate,
		GoVersion: runtime.Version(),
		Compiler:  runtime.Compiler,
		Platform:  fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
	}
}
