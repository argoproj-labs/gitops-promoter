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

package metrics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitOperationHitsNetwork(t *testing.T) {
	t.Parallel()

	network := []GitOperation{
		GitOperationClone,
		GitOperationFetch,
		GitOperationFetchNotes,
		GitOperationLsRemote,
		GitOperationPush,
		GitOperationPull,
	}
	for _, op := range network {
		require.True(t, GitOperationHitsNetwork(op), op)
	}

	local := []GitOperation{
		"config",
		"show",
		"rev-parse",
		"merge-tree",
		"interpret-trailers",
		"notes",
	}
	for _, op := range local {
		require.False(t, GitOperationHitsNetwork(op), op)
	}
}
