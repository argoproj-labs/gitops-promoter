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
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/cel"
	"k8s.io/apiextensions-apiserver/pkg/apiserver/schema/defaulting"
	apiextensionsvalidation "k8s.io/apiextensions-apiserver/pkg/apiserver/validation"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

const (
	extPreserveUnknownFields = "x-kubernetes-preserve-unknown-fields"
	extIntOrString           = "x-kubernetes-int-or-string"
	extEmbeddedResource      = "x-kubernetes-embedded-resource"
	// maxSchemaDepth bounds $ref resolution and schema expansion, guarding against recursive schemas.
	maxSchemaDepth = 64
	// fieldMetadata and fieldStatus are the resource fields the controller sets and the renderer drops.
	fieldMetadata = "metadata"
	fieldStatus   = "status"
	// refreshMinAge is how old a cached schema must be before unknown fields trigger a re-fetch.
	refreshMinAge = 10 * time.Second
)

// TemplatePath is the field path validation errors are reported under.
var TemplatePath = field.NewPath("spec", "template")

// Validator checks a parsed spec.template against the published OpenAPI schema of the kind it declares, before
// any expression is evaluated. It reports:
//   - field names that the schema does not declare, anywhere in the template (expression-valued fields included,
//     but not what an expression renders);
//   - values without ${ } expressions that violate the schema: type, enum, format, bounds, lengths, patterns and
//     item counts;
//   - CEL rules (x-kubernetes-validations) of CustomResourceDefinition kinds, except rules on a node that holds an
//     expression somewhere below it, since the rule could read a value that is unknown until rendering, and rules
//     on the root, which can read metadata the controller sets.
//
// A required field is reported only when nothing can supply it later: it is absent from an object written out in
// the template (an expression replaces only its own field, so it cannot add a sibling), it is not under metadata
// (the controller sets name and namespace), and, for CustomResourceDefinition kinds, it has no schema default (the
// API server applies those first). Built-in kinds publish placeholder defaults such as name: {default: ""} on
// fields the API server still requires, so their defaults do not count. Fields inside an expression's result are
// never checked. metadata is checked for field names only, and status is ignored, as the renderer drops it.
type Validator struct {
	schemas *SchemaSource
}

// NewValidator returns a Validator reading schemas from source.
func NewValidator(source *SchemaSource) *Validator {
	return &Validator{schemas: source}
}

// Validate checks template, which declares gvk. The returned list holds the template's problems, with paths under
// spec.template; the error is set when the schema could not be read.
func (v *Validator) Validate(ctx context.Context, template map[string]any, gvk schema.GroupVersionKind) (field.ErrorList, error) {
	ks, err := v.schemas.Get(gvk)
	if err != nil {
		return nil, err
	}
	errs := ValidateAgainst(ctx, template, ks)
	if len(errs) == 0 {
		return nil, nil
	}
	// A CRD may have just gained the field; judge it by a fresh schema before reporting.
	fresh, refreshed, err := v.schemas.Refresh(gvk, refreshMinAge)
	if err != nil || !refreshed {
		return errs, nil //nolint:nilerr // the cached schema's verdict stands when a re-fetch is not possible
	}
	return ValidateAgainst(ctx, template, fresh), nil
}

// ValidateAgainst checks template against ks. See Validator.
func ValidateAgainst(ctx context.Context, template map[string]any, ks *KindSchema) field.ErrorList {
	c := &schemaWalker{components: ks.Components, crd: ks.CRD}
	var errs field.ErrorList
	c.checkFields(ks.Root, template, TemplatePath, walkState{root: true, required: true}, 0, &errs)

	literal, exprs := literalTemplate(template)
	expanded := c.expand(ks.Root, []any{literal}, 0)
	validator := apiextensionsvalidation.NewSchemaValidatorFromOpenAPI(expanded)
	errs = append(errs, dropCompositeSummaries(apiextensionsvalidation.ValidateCustomResource(TemplatePath, literal, validator))...)

	if ks.Structural != nil {
		errs = append(errs, validateCEL(ctx, ks.Structural, literal, exprs)...)
	}
	return dedupe(errs)
}

// validateCEL evaluates the CEL rules of s against the literal template, skipping rules that could see an
// expression value.
func validateCEL(ctx context.Context, s *structuralschema.Structural, literal map[string]any, exprs *exprTree) field.ErrorList {
	stripped := s.DeepCopy()
	stripped.XValidations = nil
	stripRules(stripped, exprs)
	obj := runtime.DeepCopyJSON(literal)
	defaulting.Default(obj, stripped)
	celValidator := cel.NewValidator(stripped, true, celconfig.PerCallLimit)
	if celValidator == nil {
		return nil
	}
	errs, _ := celValidator.Validate(ctx, TemplatePath, stripped, obj, nil, celconfig.RuntimeCELCostBudget)
	return errs
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
			lit, keep := literalValue(child, sub)
			if keep {
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
// parse is treated as an expression: rendering reports its error.
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

// schemaWalker walks a template alongside an OpenAPI v3 schema, resolving $refs into components.
type schemaWalker struct {
	components map[string]*spec.Schema
	// crd is true for CustomResourceDefinition kinds, whose schema defaults the API server applies before it
	// checks required fields.
	crd bool
}

// walkState is what checkFields carries down the template.
type walkState struct {
	// root is true for the template's top-level object.
	root bool
	// required reports missing required fields; it is off under metadata, which the controller completes.
	required bool
}

// resolve follows $ref and the single-$ref allOf wrapper the API server publishes for nested structs.
func (w *schemaWalker) resolve(s *spec.Schema) *spec.Schema {
	for range maxSchemaDepth {
		if s == nil {
			return nil
		}
		if ref := s.Ref.String(); ref != "" {
			target, ok := w.components[strings.TrimPrefix(ref, componentsRefPrefix)]
			if !ok {
				return nil
			}
			s = target
			continue
		}
		if len(s.AllOf) == 1 && s.AllOf[0].Ref.String() != "" && len(s.Properties) == 0 && len(s.Type) == 0 {
			s = &s.AllOf[0]
			continue
		}
		return s
	}
	return nil
}

func boolExtension(s *spec.Schema, name string) bool {
	v, ok := s.Extensions[name].(bool)
	return ok && v
}

// properties returns the declared properties of s, including those of its allOf, oneOf and anyOf branches.
func (w *schemaWalker) properties(s *spec.Schema, depth int) map[string]*spec.Schema {
	out := map[string]*spec.Schema{}
	if depth > maxSchemaDepth {
		return out
	}
	for name := range s.Properties {
		prop := s.Properties[name]
		out[name] = &prop
	}
	for _, branches := range [][]spec.Schema{s.AllOf, s.OneOf, s.AnyOf} {
		for i := range branches {
			if b := w.resolve(&branches[i]); b != nil {
				for name, prop := range w.properties(b, depth+1) {
					if _, exists := out[name]; !exists {
						out[name] = prop
					}
				}
			}
		}
	}
	return out
}

// checkFields reports every map key in v that s does not declare. It does not look inside expression strings,
// free-form maps, preserve-unknown-fields or int-or-string nodes.
func (w *schemaWalker) checkFields(s *spec.Schema, v any, path *field.Path, state walkState, depth int, errs *field.ErrorList) {
	s = w.resolve(s)
	if s == nil || depth > maxSchemaDepth {
		return
	}
	switch t := v.(type) {
	case map[string]any:
		w.checkObject(s, t, path, state, depth, errs)
	case []any:
		if s.Items == nil || s.Items.Schema == nil {
			return
		}
		for i, item := range t {
			w.checkFields(s.Items.Schema, item, path.Index(i), walkState{required: state.required}, depth+1, errs)
		}
	default:
	}
}

// checkObject checks the keys of one object of the template against s (see checkFields).
func (w *schemaWalker) checkObject(s *spec.Schema, object map[string]any, path *field.Path, state walkState, depth int, errs *field.ErrorList) {
	if boolExtension(s, extPreserveUnknownFields) || boolExtension(s, extIntOrString) || s.Format == "int-or-string" {
		return
	}
	props := w.properties(s, 0)
	var additional *spec.Schema
	if s.AdditionalProperties != nil {
		if s.AdditionalProperties.Schema == nil {
			if s.AdditionalProperties.Allows {
				return
			}
		} else {
			additional = s.AdditionalProperties.Schema
		}
	}
	if len(props) == 0 && additional == nil {
		return
	}
	resourceRoot := state.root || boolExtension(s, extEmbeddedResource)
	if state.required {
		w.checkRequired(s, object, props, path, resourceRoot, errs)
	}
	keys := make([]string, 0, len(object))
	for k := range object {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if state.root && k == fieldStatus {
			continue
		}
		child := walkState{required: state.required && (!resourceRoot || k != fieldMetadata)}
		if prop, ok := props[k]; ok {
			w.checkFields(prop, object[k], path.Child(k), child, depth+1, errs)
			continue
		}
		if additional != nil {
			w.checkFields(additional, object[k], path.Child(k), child, depth+1, errs)
			continue
		}
		if resourceRoot && isTypeOrObjectMeta(k) {
			continue
		}
		*errs = append(*errs, unknownField(path.Child(k), k, props))
	}
}

// isTypeOrObjectMeta reports whether key is apiVersion, kind, or metadata, which every resource root accepts.
func isTypeOrObjectMeta(key string) bool {
	return key == "apiVersion" || key == "kind" || key == fieldMetadata
}

// checkRequired reports the required properties of s that object lacks. object is written out in the template, so
// nothing can add a missing key later: an expression only replaces its own field's value. On a resource root the
// controller sets metadata and the renderer drops status, so those are skipped there.
func (w *schemaWalker) checkRequired(s *spec.Schema, object map[string]any, props map[string]*spec.Schema, path *field.Path, resourceRoot bool, errs *field.ErrorList) {
	for _, name := range w.required(s, 0) {
		if _, ok := object[name]; ok {
			continue
		}
		if resourceRoot && (isTypeOrObjectMeta(name) || name == fieldStatus) {
			continue
		}
		if w.crd && hasDefault(props[name], w) {
			continue
		}
		*errs = append(*errs, field.Required(path.Child(name), ""))
	}
}

// required returns the required property names of s, including those of its allOf branches, sorted.
func (w *schemaWalker) required(s *spec.Schema, depth int) []string {
	if depth > maxSchemaDepth {
		return nil
	}
	names := slices.Clone(s.Required)
	for i := range s.AllOf {
		if b := w.resolve(&s.AllOf[i]); b != nil {
			names = append(names, w.required(b, depth+1)...)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// hasDefault reports whether the property schema, or the schema it references, declares a default.
func hasDefault(prop *spec.Schema, w *schemaWalker) bool {
	if prop == nil {
		return false
	}
	if prop.Default != nil {
		return true
	}
	resolved := w.resolve(prop)
	return resolved != nil && resolved.Default != nil
}

func unknownField(path *field.Path, key string, props map[string]*spec.Schema) *field.Error {
	detail := "unknown field: the schema does not declare it"
	if suggestion := closestKey(key, props); suggestion != "" {
		detail += fmt.Sprintf(" (did you mean %q?)", suggestion)
	}
	return &field.Error{Type: field.ErrorTypeInvalid, Field: path.String(), BadValue: field.OmitValueType{}, Detail: detail}
}

// closestKey returns the declared property nearest to key by edit distance, when it is close enough to be a typo.
func closestKey(key string, props map[string]*spec.Schema) string {
	best, bestDistance := "", -1
	for name := range props {
		d := editDistance(strings.ToLower(key), strings.ToLower(name))
		if bestDistance < 0 || d < bestDistance || (d == bestDistance && name < best) {
			best, bestDistance = name, d
		}
	}
	if bestDistance < 0 || bestDistance > max(2, len(key)/3) {
		return ""
	}
	return best
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// expand returns a copy of s with every $ref the values reach resolved inline, so the kube-openapi validator,
// which does not follow references, can check them. Only the properties, items and additional properties the
// values use are expanded, which keeps recursive schemas finite. required is cleared: an expression or the
// controller may supply a missing field. Formats the API server does not enforce are stripped, as it does for CRDs.
func (w *schemaWalker) expand(s *spec.Schema, values []any, depth int) *spec.Schema {
	s = w.resolve(s)
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
				out.Properties[k] = *w.expand(&prop, vals, depth+1)
			}
		}
		if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil {
			out.AdditionalProperties = &spec.SchemaOrBool{Allows: true, Schema: w.expand(s.AdditionalProperties.Schema, additionalValues, depth+1)}
		}
	}

	if s.Items != nil && s.Items.Schema != nil {
		var elements []any
		for _, l := range lists {
			elements = append(elements, l...)
		}
		out.Items = &spec.SchemaOrArray{Schema: w.expand(s.Items.Schema, elements, depth+1)}
	}

	expandBranches := func(branches []spec.Schema) []spec.Schema {
		if len(branches) == 0 {
			return nil
		}
		result := make([]spec.Schema, 0, len(branches))
		for i := range branches {
			result = append(result, *w.expand(&branches[i], values, depth+1))
		}
		return result
	}
	out.AllOf = expandBranches(s.AllOf)
	out.OneOf = expandBranches(s.OneOf)
	out.AnyOf = expandBranches(s.AnyOf)

	// The validator treats a schema without a type as accepting anything, which is what int-or-string and
	// preserve-unknown-fields nodes need; their published oneOf (int-or-string) is kept above.
	_ = apiextensionsvalidation.StripUnsupportedFormatsPostProcess(&out)
	return &out
}

// compositeSummary matches the summary error kube-openapi adds, at the document root, for a oneOf or anyOf node
// none of whose branches matched, quoting the node's path.
var compositeSummary = regexp.MustCompile(`^"([^"]+)" must validate (?:one and only one|at least one) schema`)

// dropCompositeSummaries removes missing-required errors (an expression or the controller may supply the field)
// and oneOf/anyOf summaries for nodes that already have an error of their own, which says more.
func dropCompositeSummaries(errs field.ErrorList) field.ErrorList {
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
			if _, ok := byField[TemplatePath.Child(m[1]).String()]; ok {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// dedupe drops errors repeated at the same path with the same detail, which oneOf branches can produce.
func dedupe(errs field.ErrorList) field.ErrorList {
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
