package labels

import (
	"errors"
	"fmt"
	"sync"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// ExpressionContext is the evaluation environment for pull request label expressions.
type ExpressionContext struct {
	Spec              promoterv1alpha1.ChangeTransferPolicySpec
	PromotionStrategy *promoterv1alpha1.PromotionStrategy
	Status            promoterv1alpha1.ChangeTransferPolicyStatus
}

// Evaluator compiles and evaluates label expressions with caching.
type Evaluator struct {
	cache sync.Map
}

// Evaluate compiles (or loads from cache) and runs expression, returning SCM label names.
func (e *Evaluator) Evaluate(expression string, evalCtx ExpressionContext) ([]string, error) {
	program, err := e.getCompiledExpression(expression)
	if err != nil {
		return nil, err
	}

	env := map[string]any{
		"Status":            evalCtx.Status,
		"Spec":              evalCtx.Spec,
		"PromotionStrategy": evalCtx.PromotionStrategy,
	}

	output, err := expr.Run(program, env)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate expression: %w", err)
	}

	names, err := coerceStringSlice(output)
	if err != nil {
		return nil, err
	}

	if err := ValidateLabelNames(names); err != nil {
		return nil, fmt.Errorf("expression returned invalid label names: %w", err)
	}

	return names, nil
}

func (e *Evaluator) getCompiledExpression(expression string) (*vm.Program, error) {
	if cached, ok := e.cache.Load(expression); ok {
		program, ok := cached.(*vm.Program)
		if !ok {
			return nil, errors.New("cached value is not a *vm.Program")
		}
		return program, nil
	}

	exprData := map[string]any{
		"Status":            promoterv1alpha1.ChangeTransferPolicyStatus{},
		"Spec":              promoterv1alpha1.ChangeTransferPolicySpec{},
		"PromotionStrategy": (*promoterv1alpha1.PromotionStrategy)(nil),
	}
	program, err := expr.Compile(expression, expr.Env(exprData))
	if err != nil {
		return nil, fmt.Errorf("failed to compile expression: %w", err)
	}

	e.cache.Store(expression, program)
	return program, nil
}

func coerceStringSlice(v any) ([]string, error) {
	switch val := v.(type) {
	case nil:
		return nil, nil
	case []string:
		return val, nil
	case []any:
		out := make([]string, len(val))
		for i, item := range val {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("expression must return []string, got %T at index %d", item, i)
			}
			out[i] = s
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expression must return []string, got %T", v)
	}
}
