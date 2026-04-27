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

package cli

import (
	"encoding/json"
	"fmt"

	"github.com/argoproj-labs/gitops-promoter/webrequestsimulator/simulatortypes"
	sigyaml "sigs.k8s.io/yaml"
)

// EncodeResultJSON marshals a simulator result as indented JSON.
func EncodeResultJSON(r *simulatortypes.Result) ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// EncodeResultYAML marshals a simulator result as YAML (via JSON for consistent handling of api types).
func EncodeResultYAML(r *simulatortypes.Result) ([]byte, error) {
	j, err := json.Marshal(r)
	if err != nil {
		return nil, fmt.Errorf("marshal result to json: %w", err)
	}
	y, err := sigyaml.JSONToYAML(j)
	if err != nil {
		return nil, fmt.Errorf("json to yaml: %w", err)
	}
	return y, nil
}
