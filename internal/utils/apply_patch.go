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

package utils

import (
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ApplyPatch wraps an apply configuration to implement client.Patch.
// This allows using Patch() with server-side apply while getting the
// result object back, avoiding the need for a separate Get() call.
type ApplyPatch struct {
	// ApplyConfig is the apply configuration object (e.g., *acv1alpha1.ChangeTransferPolicyApplyConfiguration)
	ApplyConfig any
}

// Type returns the patch type for server-side apply.
func (p ApplyPatch) Type() types.PatchType {
	return types.ApplyPatchType
}

// Data returns the JSON-encoded apply configuration.
func (p ApplyPatch) Data(_ client.Object) ([]byte, error) {
	data, err := json.Marshal(p.ApplyConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal apply configuration: %w", err)
	}
	return data, nil
}
