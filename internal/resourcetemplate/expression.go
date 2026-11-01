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
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/expr-lang/expr"
	k8sjson "k8s.io/apimachinery/pkg/util/json"

	"github.com/argoproj-labs/gitops-promoter/internal/exprcache"
)

const (
	// TemplateExpressionCachePrefix separates spec.template programs from other expr users sharing a cache.
	TemplateExpressionCachePrefix = "kubernetesresource-template"
	// ReportExpressionCachePrefix separates spec.description / spec.url programs, which see more variables than
	// templates do, so a program compiled for one variable set is never reused for the other.
	ReportExpressionCachePrefix = "kubernetesresource-report"
)

// segment is one piece of a string that may contain ${ } expressions: either literal text or the source of
// one expression.
type segment struct {
	text   string
	isExpr bool
}

// parseSegments splits s into literal and expression segments. An expression starts at "${" and ends at the
// matching "}", skipping braces inside expr string literals ('...', "...", `...`). "$${" is the escape for a
// literal "${".
func parseSegments(s string) ([]segment, error) {
	var segments []segment
	var literal strings.Builder
	flush := func() {
		if literal.Len() > 0 {
			segments = append(segments, segment{text: literal.String()})
			literal.Reset()
		}
	}
	for i := 0; i < len(s); {
		if s[i] == '$' && strings.HasPrefix(s[i:], "$${") {
			literal.WriteString("${")
			i += 3
			continue
		}
		if s[i] == '$' && strings.HasPrefix(s[i:], "${") {
			end, err := findClosingBrace(s, i+2)
			if err != nil {
				return nil, err
			}
			flush()
			segments = append(segments, segment{text: s[i+2 : end], isExpr: true})
			i = end + 1
			continue
		}
		literal.WriteByte(s[i])
		i++
	}
	flush()
	return segments, nil
}

// findClosingBrace returns the index of the "}" that closes the expression starting at start (just after
// "${"), accounting for nested braces and quoted strings.
func findClosingBrace(s string, start int) (int, error) {
	depth := 1
	for i := start; i < len(s); i++ {
		switch c := s[i]; c {
		case '\'', '"', '`':
			end, ok := skipQuoted(s, i)
			if !ok {
				return 0, fmt.Errorf("unterminated string literal in expression starting at offset %d", start-2)
			}
			i = end
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, nil
			}
		default:
		}
	}
	return 0, fmt.Errorf("unterminated expression starting at offset %d: missing closing }", start-2)
}

// skipQuoted returns the index of the quote that closes the string literal opening at s[i]. Backslash escapes
// are honored inside single- and double-quoted strings.
func skipQuoted(s string, i int) (int, bool) {
	quote := s[i]
	for j := i + 1; j < len(s); j++ {
		switch {
		case s[j] == '\\' && quote != '`':
			j++
		case s[j] == quote:
			return j, true
		default:
		}
	}
	return 0, false
}

// Interpolator evaluates ${ } expressions against a fixed variable set using a shared compile cache.
// Expressions are compiled with the variable set declared, so unknown variables and unknown fields of typed
// variables (for example Environment.Nope) are compile errors rather than silent nils.
type Interpolator struct {
	cache  *exprcache.Cache
	env    map[string]any
	prefix string
}

// NewInterpolator returns an Interpolator that evaluates expressions against env, compiling through cache
// under prefix. Callers with different variable sets must use different prefixes.
func NewInterpolator(cache *exprcache.Cache, prefix string, env map[string]any) *Interpolator {
	return &Interpolator{cache: cache, prefix: prefix, env: env}
}

// Value renders s. A string that is exactly one expression (ignoring surrounding whitespace) evaluates to the
// expression's result, converted to the JSON-compatible value set (int64, float64, bool, string, nil, []any,
// map[string]any). Any other string concatenates its literal text with the stringified expression results.
// Strings without "${" are returned unchanged.
func (in *Interpolator) Value(s string) (any, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	segments, err := parseSegments(s)
	if err != nil {
		return nil, err
	}
	if expression, ok := standaloneExpression(segments); ok {
		out, err := in.eval(expression)
		if err != nil {
			return nil, err
		}
		return normalizeJSON(out)
	}
	var b strings.Builder
	for _, seg := range segments {
		if !seg.isExpr {
			b.WriteString(seg.text)
			continue
		}
		out, err := in.eval(seg.text)
		if err != nil {
			return nil, err
		}
		text, err := stringify(out)
		if err != nil {
			return nil, fmt.Errorf("expression %q: %w", strings.TrimSpace(seg.text), err)
		}
		b.WriteString(text)
	}
	return b.String(), nil
}

// String renders s to text: like Value, but a standalone expression's result is stringified as well.
func (in *Interpolator) String(s string) (string, error) {
	v, err := in.Value(s)
	if err != nil {
		return "", err
	}
	text, err := stringify(v)
	if err != nil {
		return "", fmt.Errorf("expression in %q: %w", s, err)
	}
	return text, nil
}

// standaloneExpression reports whether segments consist of one expression and only whitespace literals.
func standaloneExpression(segments []segment) (string, bool) {
	expression := ""
	count := 0
	for _, seg := range segments {
		if seg.isExpr {
			expression = seg.text
			count++
			continue
		}
		if strings.TrimSpace(seg.text) != "" {
			return "", false
		}
	}
	return expression, count == 1
}

func (in *Interpolator) eval(expression string) (any, error) {
	program, err := in.cache.GetOrCompile(exprcache.Key{Prefix: in.prefix, Expression: expression}, expr.Env(in.env))
	if err != nil {
		return nil, fmt.Errorf("expression %q: %w", strings.TrimSpace(expression), err)
	}
	out, err := expr.Run(program, in.env)
	if err != nil {
		return nil, fmt.Errorf("expression %q failed: %w", strings.TrimSpace(expression), err)
	}
	return out, nil
}

// normalizeJSON converts an arbitrary expr result into the value set unstructured objects use, through a JSON
// round-trip: typed structs become maps keyed by their JSON names, integers become int64, and floats float64.
func normalizeJSON(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("expression result of type %T cannot be encoded as JSON: %w", v, err)
	}
	var out any
	if err := k8sjson.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("failed to decode expression result: %w", err)
	}
	return out, nil
}

// stringify converts an expression result to the text used inside a string with embedded expressions.
func stringify(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case nil:
		return "", errors.New("result is nil; use ?? to provide a default")
	case bool:
		return strconv.FormatBool(t), nil
	case int:
		return strconv.Itoa(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case int32:
		return strconv.FormatInt(int64(t), 10), nil
	case uint:
		return strconv.FormatUint(uint64(t), 10), nil
	case uint64:
		return strconv.FormatUint(t, 10), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	case float32:
		return strconv.FormatFloat(float64(t), 'f', -1, 32), nil
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("result of type %T cannot be encoded as JSON: %w", v, err)
		}
		return string(raw), nil
	}
}
