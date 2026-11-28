/*
Copyright 2026.

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

package resourcetemplate

import (
	"fmt"

	"github.com/expr-lang/expr"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/exprcache"
)

// PhaseResult is the parsed result of spec.phase.expression.
type PhaseResult struct {
	Phase       promoterv1alpha1.CommitStatusPhase
	Description string
	URL         string
}

// phaseExpressionCachePrefix separates phase programs from other expr users sharing a cache.
const phaseExpressionCachePrefix = "kubernetesresource-phase"

// EvaluatePhaseExpression compiles (through cache) and runs expression against env, then parses the result:
// a boolean (true → success, false → pending) or an object { phase, description?, url? }.
func EvaluatePhaseExpression(cache *exprcache.Cache, expression string, env map[string]any) (PhaseResult, error) {
	program, err := cache.GetOrCompile(exprcache.Key{Prefix: phaseExpressionCachePrefix, Expression: expression})
	if err != nil {
		return PhaseResult{}, fmt.Errorf("phase expression: %w", err)
	}
	out, err := expr.Run(program, env)
	if err != nil {
		return PhaseResult{}, fmt.Errorf("phase expression failed: %w", err)
	}
	return ParsePhaseResult(out)
}

// ParsePhaseResult converts the raw expression output to a PhaseResult.
func ParsePhaseResult(out any) (PhaseResult, error) {
	switch v := out.(type) {
	case bool:
		if v {
			return PhaseResult{Phase: promoterv1alpha1.CommitPhaseSuccess}, nil
		}
		return PhaseResult{Phase: promoterv1alpha1.CommitPhasePending}, nil
	case map[string]any:
		result := PhaseResult{}
		phase, err := optionalString(v, "phase")
		if err != nil {
			return PhaseResult{}, err
		}
		switch promoterv1alpha1.CommitStatusPhase(phase) {
		case promoterv1alpha1.CommitPhaseSuccess, promoterv1alpha1.CommitPhasePending, promoterv1alpha1.CommitPhaseFailure:
			result.Phase = promoterv1alpha1.CommitStatusPhase(phase)
		case "":
			result.Phase = promoterv1alpha1.CommitPhasePending
		default:
			return PhaseResult{}, fmt.Errorf("phase expression returned invalid phase %q (expected success, pending, or failure)", phase)
		}
		if result.Description, err = optionalString(v, "description"); err != nil {
			return PhaseResult{}, err
		}
		if result.URL, err = optionalString(v, "url"); err != nil {
			return PhaseResult{}, err
		}
		return result, nil
	default:
		return PhaseResult{}, fmt.Errorf("phase expression must return a boolean or an object with a phase key, got %T", out)
	}
}

func optionalString(m map[string]any, key string) (string, error) {
	raw, ok := m[key]
	if !ok || raw == nil {
		return "", nil
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("phase expression key %q must be a string, got %T", key, raw)
	}
	return s, nil
}
