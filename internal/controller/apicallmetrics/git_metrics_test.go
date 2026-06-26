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

package apicallmetrics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGitOperationNetworkTotal(t *testing.T) {
	t.Parallel()

	breakdown := map[string]float64{
		"clone":              3,
		"config":             9,
		"fetch":              54,
		"fetch-notes":        27,
		"interpret-trailers": 93,
		"ls-remote":          3,
		"show":               486,
	}

	require.Equal(t, float64(87), gitOperationNetworkTotal(breakdown))
	require.Less(t, gitOperationNetworkTotal(breakdown), gitOperationTotal(breakdown))
}
