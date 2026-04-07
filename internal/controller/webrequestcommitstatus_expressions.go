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

package controller

import (
	"context"
	"errors"
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// expressionCacheKey identifies a compiled expression in the cache. Prefix distinguishes entries so the same
// source expression compiled with different options (e.g. trigger vs validation) stays separate. Used as the
// sync.Map key (struct value, not string concat) so distinct (prefix, expression) pairs cannot collide.
type expressionCacheKey struct {
	Prefix     string
	Expression string
}

// getCompiledExpression returns a compiled expr program from the reconciler's cache, or compiles the expression and caches it.
// key.Prefix distinguishes cache entries (e.g. trigger vs validation); opts are passed through to expr.Compile.
func (r *WebRequestCommitStatusReconciler) getCompiledExpression(key expressionCacheKey, opts ...expr.Option) (*vm.Program, error) {
	if cached, ok := r.expressionCache.Load(key); ok {
		program, ok := cached.(*vm.Program)
		if !ok {
			return nil, errors.New("cached value is not a *vm.Program")
		}
		return program, nil
	}

	program, err := expr.Compile(key.Expression, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to compile expression: %w", err)
	}

	r.expressionCache.Store(key, program)
	return program, nil
}

// responseExpressionEnv builds the variable environment for validation and response-output expressions.
func responseExpressionEnv(resp httpResponse) map[string]any {
	return map[string]any{
		"Response": map[string]any{
			"StatusCode": resp.StatusCode,
			"Body":       resp.Body,
			"Headers":    resp.Headers,
		},
	}
}

// getCompiledTriggerExpression returns a cached or newly compiled trigger expression program.
// Used by evaluateTriggerExpression. Compiled with expr.AsBool() so the expression must return a boolean.
func (r *WebRequestCommitStatusReconciler) getCompiledTriggerExpression(expression string) (*vm.Program, error) {
	return r.getCompiledExpression(expressionCacheKey{Prefix: "trigger", Expression: expression}, expr.AsBool())
}

// getCompiledTriggerDataExpression returns a cached or newly compiled trigger data expression program.
// Used by evaluateTriggerDataExpression. Compiled without a result type constraint; the expression is expected to return a map.
func (r *WebRequestCommitStatusReconciler) getCompiledTriggerDataExpression(expression string) (*vm.Program, error) {
	return r.getCompiledExpression(expressionCacheKey{Prefix: "triggerdata", Expression: expression})
}

// getCompiledValidationExpression returns a cached or newly compiled validation expression program.
// Used by evaluateValidationExpression. Compiled with expr.AsBool() so the expression must return a boolean.
func (r *WebRequestCommitStatusReconciler) getCompiledValidationExpression(expression string) (*vm.Program, error) {
	return r.getCompiledExpression(expressionCacheKey{Prefix: "validation", Expression: expression}, expr.AsBool())
}

// getCompiledValidationExpressionForPromotionStrategy returns a cached or newly compiled validation expression without AsBool.
// Used by evaluateValidationExpressionForPromotionStrategy when context is promotionstrategy: expression may return bool or object { defaultPhase?, environments? }.
func (r *WebRequestCommitStatusReconciler) getCompiledValidationExpressionForPromotionStrategy(expression string) (*vm.Program, error) {
	return r.getCompiledExpression(expressionCacheKey{Prefix: "validation_promotionstrategy", Expression: expression})
}

// getCompiledResponseDataExpression returns a cached or newly compiled response output expression program.
// Used by evaluateResponseDataExpression. Compiled without a result type constraint; the expression is expected to return a map for ResponseOutput.
func (r *WebRequestCommitStatusReconciler) getCompiledResponseDataExpression(expression string) (*vm.Program, error) {
	return r.getCompiledExpression(expressionCacheKey{Prefix: "responsedata", Expression: expression})
}

// triggerExprEnv builds the variable environment for trigger and trigger output expressions.
func (td templateData) triggerExprEnv() map[string]any {
	return map[string]any{
		"ReportedSha":       td.ReportedSha,
		"LastSuccessfulSha": td.LastSuccessfulSha,
		"Phase":             td.Phase,
		"PromotionStrategy": td.PromotionStrategy,
		"Environment":       td.Environment,
		"TriggerOutput":     td.TriggerOutput,
		"ResponseOutput":    td.ResponseOutput,
	}
}

// evaluateTriggerExpression runs the trigger expression to decide whether to perform the HTTP request.
// Returns true when the controller should issue the request; false keeps the phase from the last reconcile and skips.
func (r *WebRequestCommitStatusReconciler) evaluateTriggerExpression(ctx context.Context, expression string, td templateData) (triggerResult, error) {
	logger := log.FromContext(ctx)

	program, err := r.getCompiledTriggerExpression(expression)
	if err != nil {
		return triggerResult{}, fmt.Errorf("failed to compile trigger expression: %w", err)
	}

	output, err := expr.Run(program, td.triggerExprEnv())
	if err != nil {
		return triggerResult{}, fmt.Errorf("failed to evaluate trigger expression: %w", err)
	}

	boolResult, ok := output.(bool)
	if !ok {
		return triggerResult{}, fmt.Errorf("trigger expression must return bool, got %T", output)
	}

	logger.V(4).Info("Trigger expression evaluated", "trigger", boolResult)
	return triggerResult{Trigger: boolResult}, nil
}

// evaluateTriggerDataExpression runs the trigger when.output expression to produce state that is persisted
// across reconcile cycles in status.triggerOutput. Must return a map[string]any.
func (r *WebRequestCommitStatusReconciler) evaluateTriggerDataExpression(ctx context.Context, expression string, td templateData) (map[string]any, error) {
	logger := log.FromContext(ctx)

	program, err := r.getCompiledTriggerDataExpression(expression)
	if err != nil {
		return nil, fmt.Errorf("failed to compile trigger data expression: %w", err)
	}

	output, err := expr.Run(program, td.triggerExprEnv())
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate trigger data expression: %w", err)
	}

	result, ok := output.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("trigger data expression must return a map/object, got %T", output)
	}

	logger.V(4).Info("Trigger data expression evaluated", "triggerData", result)
	return result, nil
}

// evaluateValidationExpression runs the validation expression against the HTTP response to determine the commit status phase.
// The expression receives Response.StatusCode, Response.Body, and Response.Headers. Its boolean return is used directly:
// true sets the CommitStatus phase to Success, false sets it to Pending. Used when deciding whether promotion can proceed.
func (r *WebRequestCommitStatusReconciler) evaluateValidationExpression(ctx context.Context, expression string, resp httpResponse) (bool, error) {
	logger := log.FromContext(ctx)

	program, err := r.getCompiledValidationExpression(expression)
	if err != nil {
		return false, fmt.Errorf("failed to compile validation expression: %w", err)
	}

	output, err := expr.Run(program, responseExpressionEnv(resp))
	if err != nil {
		return false, fmt.Errorf("failed to evaluate validation expression: %w", err)
	}

	result, ok := output.(bool)
	if !ok {
		return false, fmt.Errorf("validation expression must return boolean, got %T", output)
	}

	logger.V(4).Info("Validation expression evaluated", "passed", result)
	return result, nil
}

// evaluateValidationExpressionForPromotionStrategy runs the success expression when mode.context is promotionstrategy.
// The expression may return: a boolean (one phase for all); or an object { defaultPhase?, environments? }.
// defaultPhase defaults to "pending" when omitted; it is used for all when environments is empty, or for branches not in environments.
// environments is an optional array of { branch, phase }. Returns (phase, phasePerBranch, err).
func (r *WebRequestCommitStatusReconciler) evaluateValidationExpressionForPromotionStrategy(ctx context.Context, expression string, resp httpResponse) (phase promoterv1alpha1.CommitStatusPhase, phasePerBranch map[string]promoterv1alpha1.CommitStatusPhase, err error) {
	logger := log.FromContext(ctx)

	program, err := r.getCompiledValidationExpressionForPromotionStrategy(expression)
	if err != nil {
		return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("failed to compile validation expression: %w", err)
	}

	output, err := expr.Run(program, responseExpressionEnv(resp))
	if err != nil {
		return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("failed to evaluate validation expression: %w", err)
	}

	// Boolean: one phase for all environments
	if b, ok := output.(bool); ok {
		if b {
			return promoterv1alpha1.CommitPhaseSuccess, nil, nil
		}
		return promoterv1alpha1.CommitPhasePending, nil, nil
	}

	// Object: { defaultPhase?, environments? } — defaultPhase defaults to "pending" when omitted
	if obj, ok := output.(map[string]any); ok {
		return parsePerBranchPhases(logger, obj)
	}

	return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression (promotionstrategy context) must return bool or object { defaultPhase?, environments? }, got %T", output)
}

// parsePerBranchPhases extracts defaultPhase and optional per-branch overrides from an expression
// result object of the form { defaultPhase?, environments?: [{ branch, phase }] }.
func parsePerBranchPhases(logger logr.Logger, obj map[string]any) (promoterv1alpha1.CommitStatusPhase, map[string]promoterv1alpha1.CommitStatusPhase, error) {
	defaultPhaseStr, err := getString(obj, "defaultPhase")
	if err != nil {
		return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression defaultPhase: %w", err)
	}
	defaultPhase, err := parsePhaseString(defaultPhaseStr, promoterv1alpha1.CommitPhasePending)
	if err != nil {
		return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression defaultPhase: %w", err)
	}
	envsVal, hasEnvs := obj["environments"]
	if !hasEnvs || envsVal == nil {
		return defaultPhase, nil, nil
	}
	sl, ok := envsVal.([]any)
	if !ok {
		return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression environments must be an array, got %T", envsVal)
	}
	if len(sl) == 0 {
		return defaultPhase, nil, nil
	}
	m := make(map[string]promoterv1alpha1.CommitStatusPhase, len(sl))
	for i, item := range sl {
		entry, ok := item.(map[string]any)
		if !ok {
			return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression environments[%d] must be object with branch and phase, got %T", i, item)
		}
		branch, err := getString(entry, "branch")
		if err != nil {
			return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression environments[%d].branch: %w", i, err)
		}
		if branch == "" {
			return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression environments[%d]: branch is required", i)
		}
		phaseStr, err := getString(entry, "phase")
		if err != nil {
			return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression environments[%d].phase: %w", i, err)
		}
		phase, err := parsePhaseString(phaseStr, defaultPhase)
		if err != nil {
			return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression environments[%d].phase: %w", i, err)
		}
		if _, exists := m[branch]; exists {
			return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression environments[%d]: duplicate branch %q", i, branch)
		}
		m[branch] = phase
	}
	logger.V(4).Info("Validation expression evaluated (per-branch object)", "defaultPhase", defaultPhase, "phasePerBranch", m)
	return defaultPhase, m, nil
}

// getString reads an optional string field from an expression object. Missing or nil key yields ("", nil).
// A present value with non-string type returns an error.
func getString(m map[string]any, key string) (string, error) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("field %q must be a string, got %T", key, v)
	}
	return s, nil
}

// resolvePhaseForBranch returns the phase for a branch from phasePerBranch, falling back to defaultPhase
// when phasePerBranch is nil or the branch is not in the map.
func resolvePhaseForBranch(branch string, defaultPhase promoterv1alpha1.CommitStatusPhase, phasePerBranch map[string]promoterv1alpha1.CommitStatusPhase) promoterv1alpha1.CommitStatusPhase {
	if phasePerBranch != nil {
		if p, ok := phasePerBranch[branch]; ok {
			return p
		}
	}
	return defaultPhase
}

// resolveAllBranchPhases builds a complete PhasePerBranch map for all applicable environments,
// merging the default phase with any per-branch overrides from the expression result.
func resolveAllBranchPhases(envs []promoterv1alpha1.Environment, defaultPhase promoterv1alpha1.CommitStatusPhase, phasePerBranch map[string]promoterv1alpha1.CommitStatusPhase) map[string]promoterv1alpha1.CommitStatusPhase {
	resolved := make(map[string]promoterv1alpha1.CommitStatusPhase, len(envs))
	for _, env := range envs {
		resolved[env.Branch] = resolvePhaseForBranch(env.Branch, defaultPhase, phasePerBranch)
	}
	return resolved
}

// aggregatePhase computes an overall phase from a PhasePerBranch map:
// success only if all branches succeeded, failure if any failed, pending otherwise.
func aggregatePhase(phasePerBranch map[string]promoterv1alpha1.CommitStatusPhase) string {
	if len(phasePerBranch) == 0 {
		return string(promoterv1alpha1.CommitPhasePending)
	}
	allSuccess := true
	for _, p := range phasePerBranch {
		if p == promoterv1alpha1.CommitPhaseFailure {
			return string(promoterv1alpha1.CommitPhaseFailure)
		}
		if p != promoterv1alpha1.CommitPhaseSuccess {
			allSuccess = false
		}
	}
	if allSuccess {
		return string(promoterv1alpha1.CommitPhaseSuccess)
	}
	return string(promoterv1alpha1.CommitPhasePending)
}

func parsePhaseString(phaseStr string, defaultPhase promoterv1alpha1.CommitStatusPhase) (promoterv1alpha1.CommitStatusPhase, error) {
	switch phaseStr {
	case "success":
		return promoterv1alpha1.CommitPhaseSuccess, nil
	case "pending":
		return promoterv1alpha1.CommitPhasePending, nil
	case "failure":
		return promoterv1alpha1.CommitPhaseFailure, nil
	case "":
		return defaultPhase, nil
	default:
		return promoterv1alpha1.CommitPhasePending, fmt.Errorf("unrecognized phase %q, must be one of: success, pending, failure", phaseStr)
	}
}

// evaluateResponseDataExpression runs the response.output expression to extract or transform data from the HTTP response.
// The expression receives Response.StatusCode, Response.Body, and Response.Headers and must return a map.
// The returned map is stored in status.responseOutput and is available to the trigger expression and to description/URL templates as ResponseOutput.
func (r *WebRequestCommitStatusReconciler) evaluateResponseDataExpression(ctx context.Context, expression string, resp httpResponse) (map[string]any, error) {
	logger := log.FromContext(ctx)

	program, err := r.getCompiledResponseDataExpression(expression)
	if err != nil {
		return nil, fmt.Errorf("failed to compile response data expression: %w", err)
	}

	output, err := expr.Run(program, responseExpressionEnv(resp))
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate response expression: %w", err)
	}

	result, ok := output.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("response data expression must return a map/object, got %T", output)
	}

	logger.V(4).Info("Response data expression evaluated", "responseData", result)
	return result, nil
}
