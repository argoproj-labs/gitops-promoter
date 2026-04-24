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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// ErrHTTPResponseRequiredWhenTriggerFires is returned by a simulator HTTPEXecutor when the trigger
// would fire but no mock HTTPResponse was provided.
var ErrHTTPResponseRequiredWhenTriggerFires = errors.New("HTTPResponse is required when the trigger fires (fill in a mock response, or craft inputs so the trigger does not fire)")

// TriggerDecision is the result of EvaluateTriggerDecision: whether to perform the HTTP round-trip
// and any trigger output data for the next reconcile.
type TriggerDecision struct {
	NewTriggerData map[string]any
	ShouldFire     bool
}

// ValidationResult holds the outcome of processing a fire or carry-forward path. Phase is derived
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
type ValidationResult struct {
	LastRequestTime        *metav1.Time
	LastResponseStatusCode *int
	ResponseDataJSON       *apiextensionsv1.JSON
	SuccessDataJSON        *apiextensionsv1.JSON
	PhasePerBranch         map[string]promoterv1alpha1.CommitStatusPhase
	Phase                  promoterv1alpha1.CommitStatusPhase
}

// HTTPEXecutor performs the WebRequestCommitStatus HTTP round-trip for the given template data
// (render, auth, transport). The simulator supplies an implementation that returns a mock response
// or ErrHTTPResponseRequiredWhenTriggerFires when none is set.
type HTTPEXecutor interface {
	Execute(ctx context.Context, wrcs *promoterv1alpha1.WebRequestCommitStatus, td TemplateData) (HTTPResponse, error)
}

// EvaluateTriggerDecision determines whether the HTTP request should fire this reconcile.
// For polling mode it checks whether the polling interval has elapsed since lastRequestTime.
// For trigger mode it evaluates the trigger expression and optionally the trigger output expression.
func EvaluateTriggerDecision(
	ctx context.Context,
	evaluator *Evaluator,
	mode promoterv1alpha1.ModeSpec,
	td TemplateData,
	lastRequestTime *metav1.Time,
) (TriggerDecision, error) {
	logger := log.FromContext(ctx)
	shouldFire := true
	var newTriggerData map[string]any

	if mode.Polling != nil && lastRequestTime != nil {
		if elapsed := time.Since(lastRequestTime.Time); elapsed < mode.Polling.Interval.Duration {
			logger.V(4).Info("Within polling interval, skipping HTTP request",
				"elapsed", elapsed, "interval", mode.Polling.Interval.Duration)
			shouldFire = false
		}
	}

	if mode.Trigger != nil {
		sf, ntd, err := evaluator.EvaluateTriggerWhenBranch(ctx, mode.Trigger, td)
		if err != nil {
			return TriggerDecision{}, fmt.Errorf("failed to evaluate trigger.when: %w", err)
		}
		shouldFire = sf
		newTriggerData = ntd
	}

	return TriggerDecision{ShouldFire: shouldFire, NewTriggerData: newTriggerData}, nil
}

// ValidationResultFromHTTPResponse runs response extraction and success.when evaluation after
// an HTTP response is available. It updates td.ResponseOutput when response data JSON is present.
func ValidationResultFromHTTPResponse(
	ctx context.Context,
	evaluator *Evaluator,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	td TemplateData,
	response HTTPResponse,
) (ValidationResult, error) {
	logger := log.FromContext(ctx)

	now := metav1.Now()
	lastRequestTime := &now
	lastResponseStatusCode := &response.StatusCode

	logger.V(4).Info("HTTP response received", "statusCode", response.StatusCode)

	var responseDataJSON *apiextensionsv1.JSON
	if wrcs.Spec.Mode.Trigger != nil && wrcs.Spec.Mode.Trigger.Response != nil {
		extractedData, err := evaluator.EvaluateResponseDataExpression(ctx, wrcs.Spec.Mode.Trigger.Response.Output.Expression, response)
		if err != nil {
			return ValidationResult{}, fmt.Errorf("failed to evaluate response data expression: %w", err)
		}

		responseDataBytes, err := json.Marshal(extractedData)
		if err != nil {
			return ValidationResult{}, fmt.Errorf("failed to marshal response data: %w", err)
		}
		responseDataJSON = &apiextensionsv1.JSON{Raw: responseDataBytes}
	}

	if responseDataJSON != nil {
		if data, err := UnmarshalJSONMap(responseDataJSON); err == nil && data != nil {
			td.ResponseOutput = data
		}
	}

	exprData := SuccessWhenExprData(td, &response)
	exprData, err := evaluator.EnrichWhenExprEnv(ctx, wrcs.Spec.Success.When, exprData)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("failed to evaluate success.when.variables: %w", err)
	}
	phase, phasePerBranch, err := evaluateSuccessPhase(ctx, evaluator, wrcs, exprData)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("failed to evaluate validation expression: %w", err)
	}

	successDataJSON, err := evaluateSuccessOutput(ctx, evaluator, wrcs, exprData)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("failed to evaluate success.when.output expression: %w", err)
	}

	return ValidationResult{
		Phase:                  phase,
		PhasePerBranch:         phasePerBranch,
		LastRequestTime:        lastRequestTime,
		LastResponseStatusCode: lastResponseStatusCode,
		ResponseDataJSON:       responseDataJSON,
		SuccessDataJSON:        successDataJSON,
	}, nil
}

// ValidationResultCarryForward evaluates success.when with Response=nil when the trigger does not fire,
// carrying forward last HTTP metadata from lastState.
func ValidationResultCarryForward(
	ctx context.Context,
	evaluator *Evaluator,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	td TemplateData,
	lastState LastReconciledState,
) (ValidationResult, error) {
	exprData := SuccessWhenExprData(td, nil)
	var err error
	exprData, err = evaluator.EnrichWhenExprEnv(ctx, wrcs.Spec.Success.When, exprData)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("failed to evaluate success.when.variables: %w", err)
	}
	phase, phasePerBranch, err := evaluateSuccessPhase(ctx, evaluator, wrcs, exprData)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("failed to evaluate success.when expression: %w", err)
	}

	successDataJSON, err := evaluateSuccessOutput(ctx, evaluator, wrcs, exprData)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("failed to evaluate success.when.output expression: %w", err)
	}

	return ValidationResult{
		Phase:                  phase,
		PhasePerBranch:         phasePerBranch,
		LastRequestTime:        lastState.LastRequestTime,
		LastResponseStatusCode: lastState.LastResponseStatusCode,
		ResponseDataJSON:       lastState.ResponseOutput,
		SuccessDataJSON:        successDataJSON,
	}, nil
}

// FireOrCarryForward runs the HTTP executor when decision.ShouldFire, otherwise carries forward
// without a new HTTP response.
func FireOrCarryForward(
	ctx context.Context,
	evaluator *Evaluator,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	td TemplateData,
	decision TriggerDecision,
	lastState LastReconciledState,
	exec HTTPEXecutor,
) (ValidationResult, error) {
	if !decision.ShouldFire {
		return ValidationResultCarryForward(ctx, evaluator, wrcs, td, lastState)
	}
	resp, err := exec.Execute(ctx, wrcs, td)
	if err != nil {
		return ValidationResult{}, err
	}
	return ValidationResultFromHTTPResponse(ctx, evaluator, wrcs, td, resp)
}

func evaluateSuccessPhase(
	ctx context.Context,
	evaluator *Evaluator,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	exprData map[string]any,
) (promoterv1alpha1.CommitStatusPhase, map[string]promoterv1alpha1.CommitStatusPhase, error) {
	if wrcs.Spec.Mode.Context == promoterv1alpha1.ContextPromotionStrategy {
		phase, phasePerBranch, err := evaluator.EvaluateValidationExpressionForPromotionStrategy(ctx, wrcs.Spec.Success.When.Expression, exprData)
		if err != nil {
			return phase, phasePerBranch, fmt.Errorf("failed to evaluate validation expression (promotionstrategy context): %w", err)
		}
		return phase, phasePerBranch, nil
	}
	passed, err := evaluator.EvaluateValidationExpression(ctx, wrcs.Spec.Success.When.Expression, exprData)
	if err != nil {
		return "", nil, fmt.Errorf("failed to evaluate validation expression: %w", err)
	}
	if passed {
		return promoterv1alpha1.CommitPhaseSuccess, nil, nil
	}
	return promoterv1alpha1.CommitPhasePending, nil, nil
}

func evaluateSuccessOutput(
	ctx context.Context,
	evaluator *Evaluator,
	wrcs *promoterv1alpha1.WebRequestCommitStatus,
	exprData map[string]any,
) (*apiextensionsv1.JSON, error) {
	if wrcs.Spec.Success.When.Output == nil || wrcs.Spec.Success.When.Output.Expression == "" {
		return nil, nil
	}

	extractedData, err := evaluator.EvaluateSuccessDataExpression(ctx, wrcs.Spec.Success.When.Output.Expression, exprData)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate success data expression: %w", err)
	}

	return MarshalJSONMap(extractedData)
}
