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
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// expressionCacheKey identifies a compiled expression in the cache. Prefix (e.g. "trigger:", "validation:")
// ensures the same expression compiled with different options gets distinct cache entries.
type expressionCacheKey struct {
	Prefix     string
	Expression string
}

func (k expressionCacheKey) String() string { return k.Prefix + k.Expression }

// getCompiledExpression returns a compiled expr program from the reconciler's cache, or compiles the expression and caches it.
// key.Prefix distinguishes cache entries (e.g. trigger vs validation); opts are passed through to expr.Compile.
func (r *WebRequestCommitStatusReconciler) getCompiledExpression(key expressionCacheKey, opts ...expr.Option) (*vm.Program, error) {
	cacheKey := key.String()

	if cached, ok := r.expressionCache.Load(cacheKey); ok {
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

	r.expressionCache.Store(cacheKey, program)
	return program, nil
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

// getCompiledValidationExpressionFlexible returns a cached or newly compiled validation expression without AsBool.
// Used by evaluateValidationExpressionForPromotionStrategy when context is promotionstrategy: expression may return bool or object { defaultPhase?, environments? }.
func (r *WebRequestCommitStatusReconciler) getCompiledValidationExpressionFlexible(expression string) (*vm.Program, error) {
	return r.getCompiledExpression(expressionCacheKey{Prefix: "validation_flex", Expression: expression})
}

// getCompiledResponseDataExpression returns a cached or newly compiled response output expression program.
// Used by evaluateResponseDataExpression. Compiled without a result type constraint; the expression is expected to return a map for ResponseOutput.
func (r *WebRequestCommitStatusReconciler) getCompiledResponseDataExpression(expression string) (*vm.Program, error) {
	return r.getCompiledExpression(expressionCacheKey{Prefix: "responsedata", Expression: expression})
}

// evaluateTriggerExpression runs the trigger expression to decide whether to perform the HTTP request for this environment.
// It is called before each potential request; the expression has access to ReportedSha, LastSuccessfulSha, Phase, PromotionStrategy, Environment, TriggerOutput, and ResponseOutput.
// Returns a bool: when true, the controller issues the request; when false, it keeps the previous phase (e.g. Pending) and skips the request.
func (r *WebRequestCommitStatusReconciler) evaluateTriggerExpression(ctx context.Context, expression string, td templateData) (triggerResult, error) {
	logger := log.FromContext(ctx)

	// Get compiled expression from cache or compile it
	program, err := r.getCompiledTriggerExpression(expression)
	if err != nil {
		return triggerResult{}, fmt.Errorf("failed to compile trigger expression: %w", err)
	}

	// Build environment for expression evaluation
	env := map[string]any{
		"ReportedSha":       td.ReportedSha,
		"LastSuccessfulSha": td.LastSuccessfulSha,
		"Phase":             td.Phase,
		"PromotionStrategy": td.PromotionStrategy,
		"Environment":       td.Environment,
		"TriggerOutput":     td.TriggerOutput,
		"ResponseOutput":    td.ResponseOutput,
	}

	// Run the expression
	output, err := expr.Run(program, env)
	if err != nil {
		return triggerResult{}, fmt.Errorf("failed to evaluate trigger expression: %w", err)
	}

	// Must return a boolean
	boolResult, ok := output.(bool)
	if !ok {
		return triggerResult{}, fmt.Errorf("trigger expression must return bool, got %T", output)
	}

	logger.V(4).Info("Trigger expression evaluated", "trigger", boolResult)
	return triggerResult{Trigger: boolResult}, nil
}

// evaluateTriggerDataExpression runs the trigger when.output expression to produce state that is persisted
// across reconcile cycles in status.triggerOutput. The expression has the same variable set as the trigger
// expression. It must return a map[string]any; every key in that map is stored as TriggerOutput.
func (r *WebRequestCommitStatusReconciler) evaluateTriggerDataExpression(ctx context.Context, expression string, td templateData) (map[string]any, error) {
	logger := log.FromContext(ctx)

	program, err := r.getCompiledTriggerDataExpression(expression)
	if err != nil {
		return nil, fmt.Errorf("failed to compile trigger data expression: %w", err)
	}

	env := map[string]any{
		"ReportedSha":       td.ReportedSha,
		"LastSuccessfulSha": td.LastSuccessfulSha,
		"Phase":             td.Phase,
		"PromotionStrategy": td.PromotionStrategy,
		"Environment":       td.Environment,
		"TriggerOutput":     td.TriggerOutput,
		"ResponseOutput":    td.ResponseOutput,
	}

	output, err := expr.Run(program, env)
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

	// Get compiled expression from cache or compile it
	program, err := r.getCompiledValidationExpression(expression)
	if err != nil {
		return false, fmt.Errorf("failed to compile validation expression: %w", err)
	}

	// Build environment for expression evaluation
	env := map[string]any{
		"Response": map[string]any{
			"StatusCode": resp.StatusCode,
			"Body":       resp.Body,
			"Headers":    resp.Headers,
		},
	}

	// Run the expression
	output, err := expr.Run(program, env)
	if err != nil {
		return false, fmt.Errorf("failed to evaluate validation expression: %w", err)
	}

	// Check the result
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

	program, err := r.getCompiledValidationExpressionFlexible(expression)
	if err != nil {
		return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("failed to compile validation expression: %w", err)
	}

	env := map[string]any{
		"Response": map[string]any{
			"StatusCode": resp.StatusCode,
			"Body":       resp.Body,
			"Headers":    resp.Headers,
		},
	}

	output, err := expr.Run(program, env)
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
		defaultPhase := parsePhaseString(getString(obj, "defaultPhase"), promoterv1alpha1.CommitPhasePending)
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
			branch := getString(entry, "branch")
			if branch == "" {
				return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression environments[%d]: branch is required", i)
			}
			m[branch] = parsePhaseString(getString(entry, "phase"), defaultPhase)
		}
		logger.V(4).Info("Validation expression evaluated (per-branch object)", "defaultPhase", defaultPhase, "phasePerBranch", m)
		return defaultPhase, m, nil
	}

	return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression (promotionstrategy context) must return bool or object { defaultPhase?, environments? }, got %T", output)
}

func getString(m map[string]any, key string) string {
	v, _ := m[key]
	s, _ := v.(string)
	return s
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

func parsePhaseString(phaseStr string, defaultPhase promoterv1alpha1.CommitStatusPhase) promoterv1alpha1.CommitStatusPhase {
	switch phaseStr {
	case "success":
		return promoterv1alpha1.CommitPhaseSuccess
	case "pending":
		return promoterv1alpha1.CommitPhasePending
	case "failure":
		return promoterv1alpha1.CommitPhaseFailure
	default:
		return defaultPhase
	}
}

// evaluateResponseDataExpression runs the response.output expression to extract or transform data from the HTTP response.
// The expression receives Response.StatusCode, Response.Body, and Response.Headers and must return a map.
// The returned map is stored in status.responseOutput and is available to the trigger expression and to description/URL templates as ResponseOutput.
func (r *WebRequestCommitStatusReconciler) evaluateResponseDataExpression(ctx context.Context, expression string, resp httpResponse) (map[string]any, error) {
	logger := log.FromContext(ctx)

	// Get compiled expression from cache or compile it
	program, err := r.getCompiledResponseDataExpression(expression)
	if err != nil {
		return nil, fmt.Errorf("failed to compile response data expression: %w", err)
	}

	// Build environment for expression evaluation (same as validation expression)
	env := map[string]any{
		"Response": map[string]any{
			"StatusCode": resp.StatusCode,
			"Body":       resp.Body,
			"Headers":    resp.Headers,
		},
	}

	// Run the expression
	output, err := expr.Run(program, env)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate response expression: %w", err)
	}

	// Convert output to map[string]any
	result, ok := output.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("response data expression must return a map/object, got %T", output)
	}

	logger.V(4).Info("Response data expression evaluated", "responseData", result)
	return result, nil
}
