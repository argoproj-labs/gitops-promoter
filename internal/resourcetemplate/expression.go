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

	"github.com/google/cel-go/cel"

	"github.com/google/cel-go/common/types"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apiservercel "k8s.io/apiserver/pkg/cel"
)

// segment is one piece of a string that may contain ${ } expressions: either literal text or the source of
// one expression.
type segment struct {
	text   string
	isExpr bool
}

// parseSegments splits s into literal and expression segments. An expression starts at "${" and ends at the
// matching "}", skipping braces inside CEL string literals ('...', "...", `...`). "$${" is the escape for a
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

// Interpolator evaluates ${ } CEL expressions against a fixed variable set. Expressions are compiled against the
// declared variables, so unknown variables and unknown fields of typed variables (for example
// Environment.propsed) are compile errors rather than runtime nils, and a standalone expression's output type is
// checked against the target field's declared type when the kind's schema is known.
type Interpolator struct {
	compiler   *Compiler
	activation map[string]any
	gvk        schema.GroupVersionKind
	set        VariableSet
}

// NewInterpolator returns an Interpolator over set with the given activation (variable values).
func NewInterpolator(compiler *Compiler, set VariableSet, gvk schema.GroupVersionKind, activation map[string]any) *Interpolator {
	return &Interpolator{compiler: compiler, set: set, gvk: gvk, activation: activation}
}

// Value renders s. A string that is exactly one expression (ignoring surrounding whitespace) evaluates to the
// expression's result with its type preserved (int64, float64, bool, string, nil, []any, map[string]any). Any
// other string concatenates its literal text with the stringified expression results. Strings without "${" are
// returned unchanged. expected, when known, is the declared type of the field receiving the value.
func (in *Interpolator) Value(s string, expected *apiservercel.DeclType) (any, error) {
	return in.render(s, expected, renderOptions{evaluate: true})
}

// Check compiles the expressions in s and checks them against expected exactly as Value does before evaluating,
// without evaluating anything, so it can run at admission time. required additionally rejects a standalone
// expression that can remove the field (a null or an optional result).
func (in *Interpolator) Check(s string, expected *apiservercel.DeclType, required bool) error {
	_, err := in.render(s, expected, renderOptions{required: required})
	return err
}

// CheckText is Check for a value that String will render: a standalone expression must produce text or a scalar
// (string, int, double, bool), not a list, map, null, or an optional that may be absent.
func (in *Interpolator) CheckText(s string) error {
	_, err := in.render(s, nil, renderOptions{text: true})
	return err
}

type renderOptions struct {
	evaluate bool
	required bool
	text     bool
}

func (in *Interpolator) render(s string, expected *apiservercel.DeclType, opts renderOptions) (any, error) {
	if !strings.Contains(s, "${") {
		return s, nil
	}
	segments, err := parseSegments(s)
	if err != nil {
		return nil, err
	}
	if expression, ok := standaloneExpression(segments); ok {
		program, err := in.checkStandalone(expression, expected, opts)
		if err != nil || !opts.evaluate {
			return nil, err
		}
		return in.eval(program, expression)
	}
	if expected != nil && !acceptsText(expected) {
		return nil, fmt.Errorf("the field expects %s but the value is text with embedded expressions; use a single ${ } expression", describeDeclType(expected))
	}
	var b strings.Builder
	for _, seg := range segments {
		if !seg.isExpr {
			b.WriteString(seg.text)
			continue
		}
		program, err := in.compiler.Compile(in.set, in.gvk, seg.text)
		if err != nil {
			return nil, err
		}
		if program.OutputType.Kind() == types.NullTypeKind {
			return nil, fmt.Errorf("expression %q: result is null; use orValue() to provide a default", strings.TrimSpace(seg.text))
		}
		if !opts.evaluate {
			continue
		}
		out, err := in.eval(program, seg.text)
		if err != nil {
			return nil, err
		}
		text, err := stringify(out)
		if err != nil {
			return nil, fmt.Errorf("expression %q: %w", strings.TrimSpace(seg.text), err)
		}
		b.WriteString(text)
	}
	if !opts.evaluate {
		return nil, nil
	}
	return b.String(), nil
}

// checkStandalone compiles a standalone expression and checks its output type against expected and opts.
func (in *Interpolator) checkStandalone(expression string, expected *apiservercel.DeclType, opts renderOptions) (*Program, error) {
	program, err := in.compiler.Compile(in.set, in.gvk, expression)
	if err != nil {
		return nil, err
	}
	if err := CheckOutputType(program.OutputType, expected); err != nil {
		return nil, fmt.Errorf("expression %q %w", strings.TrimSpace(expression), err)
	}
	if opts.required && canDropField(program.OutputType) {
		return nil, fmt.Errorf("expression %q returns %s, which would remove a required field", strings.TrimSpace(expression), describeCelType(program.OutputType))
	}
	if opts.text && !rendersAsText(program.OutputType) {
		return nil, fmt.Errorf("expression %q returns %s, but this value must be text", strings.TrimSpace(expression), describeCelType(program.OutputType))
	}
	return program, nil
}

// canDropField reports whether a standalone expression of type t can evaluate to "no value": null, or an optional.
func canDropField(t *cel.Type) bool {
	return t.Kind() == types.NullTypeKind || isOptionalType(t)
}

// rendersAsText reports whether a standalone expression of type t always stringifies to a sensible value.
func rendersAsText(t *cel.Type) bool {
	switch t.Kind() {
	case types.StringKind, types.IntKind, types.UintKind, types.DoubleKind, types.BoolKind, types.DynKind, types.AnyKind:
		return true
	default:
		return false
	}
}

func isOptionalType(t *cel.Type) bool {
	return t.Kind() == types.OpaqueKind && t.TypeName() == "optional_type"
}

// String renders s to text: like Value, but a standalone expression's result is stringified as well.
func (in *Interpolator) String(s string) (string, error) {
	v, err := in.Value(s, nil)
	if err != nil {
		return "", err
	}
	text, err := stringify(v)
	if err != nil {
		return "", fmt.Errorf("expression in %q: %w", s, err)
	}
	return text, nil
}

func (in *Interpolator) eval(program *Program, expression string) (any, error) {
	out, err := program.Eval(in.activation)
	if err != nil {
		return nil, fmt.Errorf("expression %q failed: %w", strings.TrimSpace(expression), err)
	}
	return out, nil
}

// acceptsText reports whether a field declared as t can hold a rendered string.
func acceptsText(t *apiservercel.DeclType) bool {
	k := t.CelType().Kind()
	return k == types.StringKind || k == types.DynKind || k == types.AnyKind
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

// stringify converts an expression result to the text used inside a string with embedded expressions.
func stringify(v any) (string, error) {
	switch t := v.(type) {
	case string:
		return t, nil
	case nil:
		return "", errors.New("result is null; use orValue() to provide a default")
	case bool:
		return strconv.FormatBool(t), nil
	case int64:
		return strconv.FormatInt(t, 10), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("result of type %T cannot be encoded as JSON: %w", v, err)
		}
		return string(raw), nil
	}
}
