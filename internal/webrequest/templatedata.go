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

// Package webrequest contains shared types and pure helpers used by the WebRequestCommitStatus
// reconciler for evaluating expr expressions and rendering templates. It intentionally has no
// dependency on client.Client, the settings manager, or the HTTP transport so it can be reused
// by a future simulator/CLI package without pulling in controller-runtime machinery.
package webrequest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// ErrHTTPResponseRequiredWhenTriggerFires is returned by a simulator HTTPEXecutor when the trigger
// would fire but no mock HTTPResponse was provided. Callers may wrap it with fmt.Errorf("... %w", err)
// (for example to attach the environment branch).
var ErrHTTPResponseRequiredWhenTriggerFires = errors.New("HTTPResponse is required when the trigger fires (fill in a mock response, or craft inputs so the trigger does not fire)")

// TemplateData is the data passed to Go templates when rendering URL, headers, body, and description,
// and (via triggerExprData / successWhenExprData) to expr expressions.
//
// Branch is the environment branch currently being processed — set per-iteration in both
// environments and promotionstrategy contexts (empty for the shared HTTP request in promotionstrategy).
//
// Phase is the previous reconcile's phase for carry-forward logic in expressions.
// For description/URL templates it is updated to the current reconcile's phase before upsert.
type TemplateData struct {
	NamespaceMetadata      NamespaceMetadata
	PromotionStrategy      *promoterv1alpha1.PromotionStrategy
	WebRequestCommitStatus *promoterv1alpha1.WebRequestCommitStatus
	TriggerOutput          map[string]any
	ResponseOutput         map[string]any
	SuccessOutput          map[string]any
	// TriggerVariables is the result of trigger.when.variables.expression evaluated this reconcile.
	// Nil when trigger mode is not configured, when.variables is not set, or in the skipped-SHA fast-path.
	TriggerVariables map[string]any
	// SuccessVariables is the result of success.when.variables.expression evaluated this reconcile.
	// Nil when success.when.variables is not set or in the skipped-SHA fast-path.
	SuccessVariables map[string]any
	Branch           string
	Phase            string
}

// NamespaceMetadata holds the labels and annotations of the WebRequestCommitStatus's namespace.
// It is included in TemplateData so URL, header, body, and description templates can reference them.
type NamespaceMetadata struct {
	Labels      map[string]string
	Annotations map[string]string
}

// HTTPResponse holds the raw HTTP response (status, body, headers) after the controller makes the
// HTTP request. It is passed to the validation and response expressions as the Response variable.
type HTTPResponse struct {
	Body       any
	Headers    map[string][]string
	StatusCode int
}

// triggerResult holds the result of evaluateTriggerExpression. Trigger is true when the controller
// should perform the HTTP request. When when.output.expression is configured its map result is
// stored in WebRequestCommitStatusEnvironmentStatus.TriggerOutput and on the next reconcile is
// passed back into TemplateData.TriggerOutput for both trigger expressions and into the CommitStatus
// description/URL templates.
type triggerResult struct {
	Trigger bool
}

// httpExecutionDecision is the result of resolveHTTPExecutionDecision: whether to perform the HTTP round-trip
// and any trigger output data for the next reconcile.
type httpExecutionDecision struct {
	NewTriggerData   map[string]any
	TriggerVariables map[string]any
	ShouldFire       bool
}

// reconcileOutcome holds the outcome of processing a fire or carry-forward path. Phase is derived
// from the success.when expression and is written to CommitStatus / simulated status.
//
// When context is promotionstrategy and the success expression returns an object { defaultPhase?, environments? },
// PhasePerBranch is set and used to set each environment's CommitStatus phase; Phase is the default for branches not in the per-branch map.
//
// ResponseDataJSON is set only in trigger mode when response.output.expression is configured: it is the
// JSON-serialized map returned by the data expression (extract/transform from the HTTP response).
// It is stored in WebRequestCommitStatusEnvironmentStatus.ResponseOutput so it persists across
// reconciles. On the next run it is unmarshalled into templateData.ResponseOutput, so the trigger
// expression can read it (e.g. ResponseOutput.buildUrl). When upserting the CommitStatus it is also
// passed into the description and URL templates as ResponseOutput, so the SCM status can show links
// or text derived from the response (e.g. a link to the CI run).
type reconcileOutcome struct {
	LastRequestTime        *metav1.Time
	LastResponseStatusCode *int
	ResponseDataJSON       *apiextensionsv1.JSON
	SuccessDataJSON        *apiextensionsv1.JSON
	PhasePerBranch         map[string]promoterv1alpha1.CommitStatusPhase
	SuccessVariables       map[string]any
	Phase                  promoterv1alpha1.CommitStatusPhase
}

// HTTPEXecutor performs the WebRequestCommitStatus HTTP round-trip for the given template data
// (render, auth, transport). The simulator supplies an implementation that returns a mock response
// or ErrHTTPResponseRequiredWhenTriggerFires when none is set.
type HTTPEXecutor interface {
	Execute(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, td TemplateData) (HTTPResponse, error)
}

// triggerExprData builds the expression data map for trigger and trigger output expressions.
func (td TemplateData) triggerExprData() map[string]any {
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

// successWhenExprData builds the expression data map for success.when expressions.
// It mirrors triggerExprData and adds Response: the HTTP response map when a request was
// made this reconcile, or nil otherwise.
func successWhenExprData(td TemplateData, resp *HTTPResponse) map[string]any {
	exprData := td.triggerExprData()
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

// withLatestOutputs returns a copy of the template data with ResponseOutput, TriggerOutput, and SuccessOutput
// updated from the latest HTTP response, trigger evaluation, and success evaluation. Used before upserting
// CommitStatuses so description/URL templates reflect current data.
func (td TemplateData) withLatestOutputs(responseDataJSON *apiextensionsv1.JSON, newTriggerData map[string]any, successDataJSON *apiextensionsv1.JSON) TemplateData {
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

// marshalJSONMap marshals a map into an apiextensionsv1.JSON. Returns (nil, nil) when data is nil.
func marshalJSONMap(data map[string]any) (*apiextensionsv1.JSON, error) {
	if data == nil {
		return nil, nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON map: %w", err)
	}
	return &apiextensionsv1.JSON{Raw: raw}, nil
}
