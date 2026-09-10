package reviewers

import (
	"errors"
	"fmt"
	"sync"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// ExpressionContext is the evaluation environment for pull request reviewer expressions.
type ExpressionContext struct {
	Spec              promoterv1alpha1.ChangeTransferPolicySpec
	PromotionStrategy *promoterv1alpha1.PromotionStrategy
	Status            promoterv1alpha1.ChangeTransferPolicyStatus
}

// Evaluator compiles and evaluates reviewer expressions with caching.
type Evaluator struct {
	cache sync.Map
}

// Evaluate compiles (or loads from cache) and runs expression, returning the desired reviewers.
func (e *Evaluator) Evaluate(expression string, evalCtx ExpressionContext) ([]promoterv1alpha1.PullRequestReviewer, error) {
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

	result, err := coerceReviewers(output)
	if err != nil {
		return nil, err
	}

	if err := Validate(result); err != nil {
		return nil, fmt.Errorf("expression returned invalid reviewers: %w", err)
	}

	return result, nil
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

// coerceReviewers converts expression output into typed reviewers. A bare string is shorthand for
// a username; an object selects a reviewer by a single supported identifier key.
func coerceReviewers(v any) ([]promoterv1alpha1.PullRequestReviewer, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("expression must return a list of reviewers, got %T", v)
	}

	out := make([]promoterv1alpha1.PullRequestReviewer, 0, len(list))
	for i, item := range list {
		switch value := item.(type) {
		case string:
			out = append(out, promoterv1alpha1.PullRequestReviewer{User: value})
		case map[string]any:
			reviewer, err := coerceReviewerObject(value)
			if err != nil {
				return nil, fmt.Errorf("reviewer at index %d: %w", i, err)
			}
			out = append(out, reviewer)
		default:
			return nil, fmt.Errorf("reviewer at index %d must be a string or an object, got %T", i, item)
		}
	}
	return out, nil
}

func coerceReviewerObject(value map[string]any) (promoterv1alpha1.PullRequestReviewer, error) {
	if len(value) != 1 {
		return promoterv1alpha1.PullRequestReviewer{}, fmt.Errorf("must have exactly one key, got %d", len(value))
	}

	for key, raw := range value {
		name, ok := raw.(string)
		if !ok {
			return promoterv1alpha1.PullRequestReviewer{}, fmt.Errorf("value of %q must be a string, got %T", key, raw)
		}
		switch key {
		case "user":
			return promoterv1alpha1.PullRequestReviewer{User: name}, nil
		case "group":
			return promoterv1alpha1.PullRequestReviewer{Group: name}, nil
		default:
			return promoterv1alpha1.PullRequestReviewer{}, fmt.Errorf("unsupported key %q (supported keys: user, group)", key)
		}
	}

	return promoterv1alpha1.PullRequestReviewer{}, errors.New("must have exactly one key")
}
