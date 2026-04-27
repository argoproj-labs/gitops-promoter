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
	"fmt"
	"maps"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// ExpressionEvaluator compiles and runs expr expressions used by WebRequestCommitStatus. It caches compiled
// programs in an LRU so each (prefix, expression) pair is only compiled once while it remains in cache.
// Safe for concurrent use.
//
// Reconcile paths use defaultExpressionEvaluator (package singleton). Tests may call NewExpressionEvaluator for isolation.
type ExpressionEvaluator struct {
	cache *compileCache
}

// NewExpressionEvaluator returns a new ExpressionEvaluator with an empty compile cache.
func NewExpressionEvaluator() *ExpressionEvaluator {
	return &ExpressionEvaluator{cache: newCompileCache(maxCompiledExpressionCacheEntries)}
}

// defaultExpressionEvaluator is the process-wide compile cache used by ReconcileWebRequestCommitStatus*.
var defaultExpressionEvaluator = NewExpressionEvaluator()

// getCompiledExpression returns a compiled expr program from the evaluator's cache, or compiles the expression and caches it.
// key.Prefix distinguishes cache entries (e.g. trigger vs validation); opts are passed through to expr.Compile.
func (e *ExpressionEvaluator) getCompiledExpression(key expressionCacheKey, opts ...expr.Option) (*vm.Program, error) {
	if program, ok := e.cache.get(key); ok {
		return program, nil
	}

	program, err := expr.Compile(key.Expression, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to compile expression: %w", err)
	}

	e.cache.put(key, program)
	return program, nil
}

// responseExprData builds the expression data map for validation and response-output expressions.
func responseExprData(resp HTTPResponse) map[string]any {
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
func (e *ExpressionEvaluator) getCompiledTriggerExpression(expression string) (*vm.Program, error) {
	return e.getCompiledExpression(expressionCacheKey{Prefix: "trigger", Expression: expression}, expr.AsBool())
}

// getCompiledTriggerDataExpression returns a cached or newly compiled trigger data expression program.
// Used by evaluateTriggerDataExpression. Compiled without a result type constraint; the expression is expected to return a map.
func (e *ExpressionEvaluator) getCompiledTriggerDataExpression(expression string) (*vm.Program, error) {
	return e.getCompiledExpression(expressionCacheKey{Prefix: "triggerdata", Expression: expression})
}

// getCompiledValidationExpression returns a cached or newly compiled validation expression program.
// Used by evaluateValidationExpression. Compiled with expr.AsBool() so the expression must return a boolean.
func (e *ExpressionEvaluator) getCompiledValidationExpression(expression string) (*vm.Program, error) {
	return e.getCompiledExpression(expressionCacheKey{Prefix: "validation", Expression: expression}, expr.AsBool())
}

// getCompiledValidationExpressionForPromotionStrategy returns a cached or newly compiled validation expression without AsBool.
// Used by evaluateValidationExpressionForPromotionStrategy when context is promotionstrategy: expression may return bool or object { defaultPhase?, environments? }.
func (e *ExpressionEvaluator) getCompiledValidationExpressionForPromotionStrategy(expression string) (*vm.Program, error) {
	return e.getCompiledExpression(expressionCacheKey{Prefix: "validation_promotionstrategy", Expression: expression})
}

// getCompiledResponseDataExpression returns a cached or newly compiled response output expression program.
// Used by evaluateResponseDataExpression. Compiled without a result type constraint; the expression is expected to return a map for ResponseOutput.
func (e *ExpressionEvaluator) getCompiledResponseDataExpression(expression string) (*vm.Program, error) {
	return e.getCompiledExpression(expressionCacheKey{Prefix: "responsedata", Expression: expression})
}

// getCompiledWhenVariablesExpression returns a cached or newly compiled when.variables expression program.
// The expression must return a map/object; the result is exposed as Variables to when.expression and when.output.expression.
func (e *ExpressionEvaluator) getCompiledWhenVariablesExpression(expression string) (*vm.Program, error) {
	return e.getCompiledExpression(expressionCacheKey{Prefix: "whenvariables", Expression: expression})
}

// getCompiledSuccessDataExpression returns a cached or newly compiled success output expression program.
// Used by evaluateSuccessDataExpression. Compiled without a result type constraint; the expression is expected to return a map for SuccessOutput.
func (e *ExpressionEvaluator) getCompiledSuccessDataExpression(expression string) (*vm.Program, error) {
	return e.getCompiledExpression(expressionCacheKey{Prefix: "successdata", Expression: expression})
}

// evaluateWhenVariablesExpression runs spec.when.variables.expression against base env (no Variables binding yet).
// The expression must return map[string]any.
func (e *ExpressionEvaluator) evaluateWhenVariablesExpression(ctx context.Context, expression string, base map[string]any) (map[string]any, error) {
	logger := log.FromContext(ctx)

	program, err := e.getCompiledWhenVariablesExpression(expression)
	if err != nil {
		return nil, fmt.Errorf("failed to compile when.variables expression: %w", err)
	}

	output, err := expr.Run(program, base)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate when.variables expression: %w", err)
	}

	result, ok := output.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("when.variables expression must return a map/object, got %T", output)
	}

	logger.V(4).Info("When variables expression evaluated", "whenVariables", result)
	return result, nil
}

// enrichWhenExprEnv returns a copy of base with Variables set when whenSpec.Variables is non-empty.
// When Variables is unset or expression is empty, returns base unchanged.
func (e *ExpressionEvaluator) enrichWhenExprEnv(ctx context.Context, whenSpec promoterv1alpha1.WhenWithOutputSpec, base map[string]any) (map[string]any, error) {
	if whenSpec.Variables == nil || strings.TrimSpace(whenSpec.Variables.Expression) == "" {
		return base, nil
	}
	varsResult, err := e.evaluateWhenVariablesExpression(ctx, whenSpec.Variables.Expression, base)
	if err != nil {
		return nil, err
	}
	out := maps.Clone(base)
	if out == nil {
		out = make(map[string]any)
	}
	out["Variables"] = varsResult
	return out, nil
}

// evaluateTriggerExpression runs the trigger expression to decide whether to perform the HTTP request.
// Returns true when the controller should issue the request; false keeps the phase from the last reconcile and skips.
func (e *ExpressionEvaluator) evaluateTriggerExpression(ctx context.Context, expression string, exprEnv map[string]any) (triggerResult, error) {
	logger := log.FromContext(ctx)

	program, err := e.getCompiledTriggerExpression(expression)
	if err != nil {
		return triggerResult{}, fmt.Errorf("failed to compile trigger expression: %w", err)
	}

	output, err := expr.Run(program, exprEnv)
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
func (e *ExpressionEvaluator) evaluateTriggerDataExpression(ctx context.Context, expression string, exprEnv map[string]any) (map[string]any, error) {
	logger := log.FromContext(ctx)

	program, err := e.getCompiledTriggerDataExpression(expression)
	if err != nil {
		return nil, fmt.Errorf("failed to compile trigger data expression: %w", err)
	}

	output, err := expr.Run(program, exprEnv)
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

// evaluateValidationExpression runs the success.when expression to determine the commit status phase.
// The exprData map is built by successWhenExprData and includes Response (nil when no request was made),
// Branch, Phase, PromotionStrategy, WebRequestCommitStatus, and output variables.
// Its boolean return is used directly: true → Success, false → Pending.
func (e *ExpressionEvaluator) evaluateValidationExpression(ctx context.Context, expression string, exprData map[string]any) (bool, error) {
	logger := log.FromContext(ctx)

	program, err := e.getCompiledValidationExpression(expression)
	if err != nil {
		return false, fmt.Errorf("failed to compile validation expression: %w", err)
	}

	output, err := expr.Run(program, exprData)
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

// evaluateValidationExpressionForPromotionStrategy runs the success.when expression when mode.context is promotionstrategy.
// The exprData map is built by successWhenExprData and includes Response (nil when no request was made),
// PromotionStrategy, and other trigger-expression variables.
// The expression may return: a boolean (one phase for all); or an object { defaultPhase?, environments? }.
// defaultPhase defaults to "pending" when omitted; it is used for all when environments is empty, or for branches not in environments.
// environments is an optional array of { branch, phase }. Returns (phase, phasePerBranch, err).
func (e *ExpressionEvaluator) evaluateValidationExpressionForPromotionStrategy(ctx context.Context, expression string, exprData map[string]any) (phase promoterv1alpha1.CommitStatusPhase, phasePerBranch map[string]promoterv1alpha1.CommitStatusPhase, err error) {
	logger := log.FromContext(ctx)

	program, err := e.getCompiledValidationExpressionForPromotionStrategy(expression)
	if err != nil {
		return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("failed to compile validation expression: %w", err)
	}

	output, err := expr.Run(program, exprData)
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
		defaultPhase, phases, parseErr := parsePerBranchPhases(obj)
		if parseErr != nil {
			return promoterv1alpha1.CommitPhasePending, nil, parseErr
		}
		logger.V(4).Info("Validation expression evaluated (per-branch object)", "defaultPhase", defaultPhase, "phasePerBranch", phases)
		return defaultPhase, phases, nil
	}

	return promoterv1alpha1.CommitPhasePending, nil, fmt.Errorf("validation expression (promotionstrategy context) must return bool or object { defaultPhase?, environments? }, got %T", output)
}

// evaluateResponseDataExpression runs the response.output expression to extract or transform data from the HTTP response.
// The expression receives Response.StatusCode, Response.Body, and Response.Headers and must return a map.
// The returned map is stored in status.responseOutput and is available to the trigger expression and to description/URL templates as ResponseOutput.
func (e *ExpressionEvaluator) evaluateResponseDataExpression(ctx context.Context, expression string, resp HTTPResponse) (map[string]any, error) {
	logger := log.FromContext(ctx)

	program, err := e.getCompiledResponseDataExpression(expression)
	if err != nil {
		return nil, fmt.Errorf("failed to compile response data expression: %w", err)
	}

	output, err := expr.Run(program, responseExprData(resp))
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

// evaluateSuccessDataExpression runs the success.when.output expression to produce state that is persisted
// across reconcile cycles in status.successOutput. Must return a map[string]any.
// The exprData map is the same one used for success.when.expression (built by successWhenExprData).
func (e *ExpressionEvaluator) evaluateSuccessDataExpression(ctx context.Context, expression string, exprData map[string]any) (map[string]any, error) {
	logger := log.FromContext(ctx)

	program, err := e.getCompiledSuccessDataExpression(expression)
	if err != nil {
		return nil, fmt.Errorf("failed to compile success data expression: %w", err)
	}

	output, err := expr.Run(program, exprData)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate success data expression: %w", err)
	}

	result, ok := output.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("success data expression must return a map/object, got %T", output)
	}

	logger.V(4).Info("Success data expression evaluated", "successData", result)
	return result, nil
}

// evaluateTriggerWhenBranch evaluates trigger.when (variables, bool expression, optional output map).
// It returns whether the trigger should fire and the optional output map to persist in status.triggerOutput.
func (e *ExpressionEvaluator) evaluateTriggerWhenBranch(
	ctx context.Context,
	trigger *promoterv1alpha1.TriggerModeSpec,
	td TemplateData,
) (shouldFire bool, newTriggerData map[string]any, err error) {
	base := td.triggerExprData()
	exprEnv, err := e.enrichWhenExprEnv(ctx, trigger.When, base)
	if err != nil {
		return false, nil, fmt.Errorf("failed to evaluate trigger.when.variables: %w", err)
	}
	tr, err := e.evaluateTriggerExpression(ctx, trigger.When.Expression, exprEnv)
	if err != nil {
		return false, nil, fmt.Errorf("failed to evaluate trigger expression: %w", err)
	}
	shouldFire = tr.Trigger

	out := trigger.When.Output
	if out == nil || strings.TrimSpace(out.Expression) == "" {
		return shouldFire, nil, nil
	}
	newTriggerData, err = e.evaluateTriggerDataExpression(ctx, out.Expression, exprEnv)
	if err != nil {
		return false, nil, fmt.Errorf("failed to evaluate trigger data expression: %w", err)
	}
	return shouldFire, newTriggerData, nil
}
