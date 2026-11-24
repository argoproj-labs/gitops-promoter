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

// The literal-value and CEL-rule checks in this file are adapted from the schema validation in PR #2343
// (Zach Aller), which runs the API server's own OpenAPI and CRD CEL validators over the part of the template that
// is known before rendering.

import (
	"context"
	"regexp"
	"strings"

	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	apiextensionscel "k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	apiextensionsvalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

const (
	fieldMetadata = "metadata"
	fieldStatus   = "status"
	// maxSchemaDepth bounds schema expansion; resolved schemas replace circular references with empty objects,
	// so this only guards against unexpectedly deep documents.
	maxSchemaDepth = 64
)

// validateLiterals checks the values of template that hold no ${ } expression against the kind's schema with the
// API server's OpenAPI validator (type, enum, format, bounds, lengths, patterns, and item counts) and, for kinds
// with CEL rules, evaluates those rules over the same values with schema defaults applied. Rules on a node with an
// expression anywhere below it are skipped, since they could read a value that is unknown until rendering, and so
// are rules on the root, which can read metadata the controller sets. Missing required fields are not reported
// here (see checkRequired). Errors are reported under templatePath.
func validateLiterals(ctx context.Context, ks *KindSchema, template map[string]any, templatePath *field.Path) field.ErrorList {
	if ks == nil || ks.Schema == nil {
		return nil
	}
	literal, exprs := literalTemplate(template)
	expanded := expandSchema(ks.Schema, []any{literal}, 0)
	validator := apiextensionsvalidation.NewSchemaValidatorFromOpenAPI(expanded)
	errs := dropCompositeSummaries(apiextensionsvalidation.ValidateCustomResource(templatePath, literal, validator), templatePath)
	if ks.Structural != nil {
		errs = append(errs, validateCELRules(ctx, ks.Structural, literal, exprs, templatePath)...)
	}
	return dedupeErrors(errs)
}

// validateCELRules evaluates the CEL rules of s against the literal template, skipping rules that could see an
// expression value.
func validateCELRules(ctx context.Context, s *structuralschema.Structural, literal map[string]any, exprs *exprTree, templatePath *field.Path) field.ErrorList {
	stripped := s.DeepCopy()
	stripped.XValidations = nil
	stripRules(stripped, exprs)
	obj := runtime.DeepCopyJSON(literal)
	defaulting.Default(obj, stripped)
	celValidator := apiextensionscel.NewValidator(stripped, true, celconfig.PerCallLimit)
	if celValidator == nil {
		return nil
	}
	errs, _ := celValidator.Validate(ctx, templatePath, stripped, obj, nil, celconfig.RuntimeCELCostBudget)
	// A rule that reads a field absent from the literal template fails with "no such key". The field is either
	// missing for real, which checkRequired reports, or optional, in which case the API server's own evaluation
	// of the rendered object decides.
	out := errs[:0]
	for _, e := range errs {
		if !strings.Contains(e.Detail, "no such key") {
			out = append(out, e)
		}
	}
	return out
}

// stripRules removes the CEL rules of every node on the path to an expression: the expression's own node and all
// of its ancestors.
func stripRules(s *structuralschema.Structural, t *exprTree) {
	if s == nil || t == nil {
		return
	}
	s.XValidations = nil
	for key, child := range t.children {
		if prop, ok := s.Properties[key]; ok {
			stripRules(&prop, child)
			s.Properties[key] = prop
		} else if s.AdditionalProperties != nil && s.AdditionalProperties.Structural != nil {
			stripRules(s.AdditionalProperties.Structural, child)
		}
	}
	if t.items != nil {
		stripRules(s.Items, t.items)
	}
}

// exprTree records where a template holds ${ } expressions. A node exists for every expression and each of its
// ancestors; list elements share one items node.
type exprTree struct {
	children map[string]*exprTree
	items    *exprTree
}

func (t *exprTree) child(key string) *exprTree {
	if t.children == nil {
		t.children = map[string]*exprTree{}
	}
	if c, ok := t.children[key]; ok {
		return c
	}
	c := &exprTree{}
	t.children[key] = c
	return c
}

func (t *exprTree) item() *exprTree {
	if t.items == nil {
		t.items = &exprTree{}
	}
	return t.items
}

// literalTemplate returns the part of template whose value is known before rendering, and where the expressions
// were. Expression-valued fields are removed (a list holding an expression element is removed whole, since its
// length is unknown), nulls are removed (the API server drops them), and "$${" escapes are resolved. metadata and
// status are removed: the controller rebuilds metadata and the renderer drops status.
func literalTemplate(template map[string]any) (map[string]any, *exprTree) {
	tree := &exprTree{}
	body := make(map[string]any, len(template))
	for k, v := range template {
		if k == fieldMetadata || k == fieldStatus {
			continue
		}
		body[k] = v
	}
	out, keep := literalValue(body, tree)
	if !keep {
		return map[string]any{}, tree
	}
	m, _ := out.(map[string]any)
	return m, tree
}

func literalValue(v any, tree *exprTree) (any, bool) {
	switch t := v.(type) {
	case nil:
		return nil, false
	case string:
		text, isExpr := stringLiteral(t)
		return text, !isExpr
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			var sub *exprTree
			if tree != nil && containsExpr(child) {
				sub = tree.child(k)
			}
			if lit, keep := literalValue(child, sub); keep {
				out[k] = lit
			}
		}
		return out, true
	case []any:
		for _, child := range t {
			if s, ok := child.(string); ok {
				if _, isExpr := stringLiteral(s); isExpr {
					return nil, false
				}
			}
		}
		out := make([]any, 0, len(t))
		for _, child := range t {
			var sub *exprTree
			if tree != nil && containsExpr(child) {
				sub = tree.item()
			}
			lit, keep := literalValue(child, sub)
			if !keep {
				// A null element; keep the position so error indexes match the template.
				lit = nil
			}
			out = append(out, lit)
		}
		return out, true
	default:
		return v, true
	}
}

// stringLiteral resolves s's "$${" escapes and reports whether s holds an expression. A string that does not
// parse is treated as an expression: the compile checks report its error.
func stringLiteral(s string) (string, bool) {
	if !strings.Contains(s, "${") {
		return s, false
	}
	segments, err := parseSegments(s)
	if err != nil {
		return "", true
	}
	var b strings.Builder
	for _, seg := range segments {
		if seg.isExpr {
			return "", true
		}
		b.WriteString(seg.text)
	}
	return b.String(), false
}

func containsExpr(v any) bool {
	switch t := v.(type) {
	case string:
		_, isExpr := stringLiteral(t)
		return isExpr
	case map[string]any:
		for _, c := range t {
			if containsExpr(c) {
				return true
			}
		}
	case []any:
		for _, c := range t {
			if containsExpr(c) {
				return true
			}
		}
	default:
	}
	return false
}

// expandSchema returns a copy of s limited to the properties, items, and additional properties the values use,
// which keeps the validator's work proportional to the template rather than to the kind's whole schema. required
// is cleared (an expression or the controller may supply a missing field; checkRequired reports the rest), and so
// are defaults. Formats the API server does not enforce are stripped, as it does for CRDs.
func expandSchema(s *spec.Schema, values []any, depth int) *spec.Schema {
	if s == nil || depth > maxSchemaDepth {
		return &spec.Schema{}
	}
	out := *s
	out.Ref = spec.Ref{}
	out.Required = nil
	out.Properties = nil
	out.PatternProperties = nil
	out.Dependencies = nil
	out.Definitions = nil
	out.Not = nil
	out.Items = nil
	out.AllOf, out.OneOf, out.AnyOf = nil, nil, nil
	out.Default = nil

	var maps []map[string]any
	var lists [][]any
	for _, v := range values {
		switch t := v.(type) {
		case map[string]any:
			maps = append(maps, t)
		case []any:
			lists = append(lists, t)
		default:
		}
	}

	if len(s.Properties) > 0 || (s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil) {
		byKey := map[string][]any{}
		var additionalValues []any
		for _, m := range maps {
			for k, v := range m {
				if _, ok := s.Properties[k]; ok {
					byKey[k] = append(byKey[k], v)
				} else {
					additionalValues = append(additionalValues, v)
				}
			}
		}
		if len(byKey) > 0 {
			out.Properties = make(map[string]spec.Schema, len(byKey))
			for k, vals := range byKey {
				prop := s.Properties[k]
				out.Properties[k] = *expandSchema(&prop, vals, depth+1)
			}
		}
		if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
			out.AdditionalProperties = &spec.SchemaOrBool{Allows: true, Schema: expandSchema(s.AdditionalProperties.Schema, additionalValues, depth+1)}
		}
	}

	if s.Items != nil && s.Items.Schema != nil {
		var elements []any
		for _, l := range lists {
			elements = append(elements, l...)
		}
		out.Items = &spec.SchemaOrArray{Schema: expandSchema(s.Items.Schema, elements, depth+1)}
	}

	expandBranches := func(branches []spec.Schema) []spec.Schema {
		if len(branches) == 0 {
			return nil
		}
		result := make([]spec.Schema, 0, len(branches))
		for i := range branches {
			result = append(result, *expandSchema(&branches[i], values, depth+1))
		}
		return result
	}
	out.AllOf = expandBranches(s.AllOf)
	out.OneOf = expandBranches(s.OneOf)
	out.AnyOf = expandBranches(s.AnyOf)

	_ = apiextensionsvalidation.StripUnsupportedFormatsPostProcess(&out)
	return &out
}

// compositeSummary matches the summary error kube-openapi adds, at the document root, for a oneOf or anyOf node
// none of whose branches matched, quoting the node's path.
var compositeSummary = regexp.MustCompile(`^"([^"]+)" must validate (?:one and only one|at least one) schema`)

// dropCompositeSummaries removes missing-required errors (checkRequired reports those that cannot be supplied
// later) and oneOf/anyOf summaries for nodes that already have an error of their own, which says more.
func dropCompositeSummaries(errs field.ErrorList, templatePath *field.Path) field.ErrorList {
	byField := map[string]struct{}{}
	for _, e := range errs {
		byField[e.Field] = struct{}{}
	}
	out := make(field.ErrorList, 0, len(errs))
	for _, e := range errs {
		if e.Type == field.ErrorTypeRequired {
			continue
		}
		if m := compositeSummary.FindStringSubmatch(e.Detail); m != nil {
			if _, ok := byField[templatePath.Child(m[1]).String()]; ok {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// dedupeErrors drops errors repeated at the same path with the same detail, which oneOf branches can produce.
func dedupeErrors(errs field.ErrorList) field.ErrorList {
	if len(errs) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make(field.ErrorList, 0, len(errs))
	for _, e := range errs {
		key := e.Error()
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, e)
	}
	return out
}
