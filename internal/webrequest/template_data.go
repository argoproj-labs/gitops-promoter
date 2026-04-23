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

package webrequest

import (
	"encoding/json"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// TriggerExprData builds the expression data map for trigger and trigger output expressions.
func (td TemplateData) TriggerExprData() map[string]any {
	return map[string]any{
		"Branch":                 td.Branch,
		"Phase":                  td.Phase,
		"PromotionStrategy":      td.PromotionStrategy,
		"WebRequestCommitStatus": td.WebRequestCommitStatus,
		"TriggerOutput":          td.TriggerOutput,
		"ResponseOutput":         td.ResponseOutput,
		"SuccessOutput":          td.SuccessOutput,
	}
}

// SuccessWhenExprData builds the expression data map for success.when expressions.
// It mirrors TriggerExprData and adds Response: the HTTP response map when a request was
// made this reconcile, or nil otherwise.
func SuccessWhenExprData(td TemplateData, resp *HTTPResponse) map[string]any {
	exprData := td.TriggerExprData()
	if resp != nil {
		exprData["Response"] = map[string]any{
			"StatusCode": resp.StatusCode,
			"Body":       resp.Body,
			"Headers":    resp.Headers,
		}
	} else {
		exprData["Response"] = nil
	}
	return exprData
}

// WithLatestOutputs returns a copy of the template data with ResponseOutput, TriggerOutput, and SuccessOutput
// updated from the latest HTTP response, trigger evaluation, and success evaluation. Used before upserting
// CommitStatuses so description/URL templates reflect current data.
func (td TemplateData) WithLatestOutputs(responseDataJSON *apiextensionsv1.JSON, newTriggerData map[string]any, successDataJSON *apiextensionsv1.JSON) TemplateData {
	result := td
	if responseDataJSON != nil {
		if data, err := unmarshalJSONMap(responseDataJSON); err == nil && data != nil {
			result.ResponseOutput = data
		}
	}
	if newTriggerData != nil {
		result.TriggerOutput = newTriggerData
	}
	if successDataJSON != nil {
		if data, err := unmarshalJSONMap(successDataJSON); err == nil && data != nil {
			result.SuccessOutput = data
		}
	}
	return result
}

// unmarshalJSONMap unmarshals an apiextensionsv1.JSON into a map. Returns (nil, nil) when raw is nil.
func unmarshalJSONMap(raw *apiextensionsv1.JSON) (map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	result := make(map[string]any)
	if err := json.Unmarshal(raw.Raw, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON map: %w", err)
	}
	return result, nil
}
