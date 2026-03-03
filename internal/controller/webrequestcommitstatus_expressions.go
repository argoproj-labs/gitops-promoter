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
)

// getCompiledExpression returns a compiled expr program from the reconciler's cache, or compiles the expression and caches it.
// cacheKeyPrefix (e.g. "trigger:", "validation:", "response:") ensures the same expression string compiled with different
// options (e.g. expr.AsBool() for validation) gets distinct cache entries. opts are passed through to expr.Compile.
func (r *WebRequestCommitStatusReconciler) getCompiledExpression(expression string, cacheKeyPrefix string, opts ...expr.Option) (*vm.Program, error) {
	cacheKey := cacheKeyPrefix + expression

	if cached, ok := r.expressionCache.Load(cacheKey); ok {
		program, ok := cached.(*vm.Program)
		if !ok {
			return nil, errors.New("cached value is not a *vm.Program")
		}
		return program, nil
	}

	program, err := expr.Compile(expression, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to compile expression: %w", err)
	}

	r.expressionCache.Store(cacheKey, program)
	return program, nil
}

// getCompiledTriggerExpression returns a cached or newly compiled trigger expression program.
// Used by evaluateTriggerExpression. Compiled with expr.AsBool() so the expression must return a boolean.
func (r *WebRequestCommitStatusReconciler) getCompiledTriggerExpression(expression string) (*vm.Program, error) {
	return r.getCompiledExpression(expression, "trigger:", expr.AsBool())
}

// getCompiledTriggerDataExpression returns a cached or newly compiled trigger data expression program.
// Used by evaluateTriggerDataExpression. Compiled without a result type constraint; the expression is expected to return a map.
func (r *WebRequestCommitStatusReconciler) getCompiledTriggerDataExpression(expression string) (*vm.Program, error) {
	return r.getCompiledExpression(expression, "triggerdata:")
}

// getCompiledValidationExpression returns a cached or newly compiled validation expression program.
// Used by evaluateValidationExpression. Compiled with expr.AsBool() so the expression must return a boolean.
func (r *WebRequestCommitStatusReconciler) getCompiledValidationExpression(expression string) (*vm.Program, error) {
	return r.getCompiledExpression(expression, "validation:", expr.AsBool())
}

// getCompiledResponseDataExpression returns a cached or newly compiled response data expression program.
// Used by evaluateResponseDataExpression. Compiled without a result type constraint; the expression is expected to return a map for ResponseData.
func (r *WebRequestCommitStatusReconciler) getCompiledResponseDataExpression(expression string) (*vm.Program, error) {
	return r.getCompiledExpression(expression, "responsedata:")
}

// evaluateTriggerExpression runs the trigger expression to decide whether to perform the HTTP request for this environment.
// It is called before each potential request; the expression has access to ReportedSha, LastSuccessfulSha, Phase, PromotionStrategy, Environment, TriggerData, and ResponseData.
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
		"TriggerData":       td.TriggerData,
		"ResponseData":      td.ResponseData,
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

// evaluateTriggerDataExpression runs the trigger data expression to produce state that is persisted
// across reconcile cycles as TriggerData. The expression has the same variable set as the trigger
// expression. It must return a map[string]any; every key in that map is stored as TriggerData.
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
		"TriggerData":       td.TriggerData,
		"ResponseData":      td.ResponseData,
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
func (r *WebRequestCommitStatusReconciler) evaluateValidationExpression(_ context.Context, expression string, resp httpResponse) (bool, error) {
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

	return result, nil
}

// evaluateResponseDataExpression runs the response data expression to extract or transform data from the HTTP response.
// The expression receives Response.StatusCode, Response.Body, and Response.Headers and must return a map.
// The returned map is stored as ResponseData and is available to the trigger expression and to description/URL templates.
func (r *WebRequestCommitStatusReconciler) evaluateResponseDataExpression(_ context.Context, expression string, resp httpResponse) (map[string]any, error) {
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

	return result, nil
}
