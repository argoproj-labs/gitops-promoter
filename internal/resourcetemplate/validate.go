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
	"slices"
	"strings"

	"github.com/google/cel-go/cel"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	apiservercel "k8s.io/apiserver/pkg/cel"
	"k8s.io/kube-openapi/pkg/validation/spec"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// ValidateSpec statically checks a KubernetesResourceCommitStatus spec against the schema of the kind its
// template creates, without evaluating anything:
//   - every ${ } expression must compile against its variable set (unknown variables and unknown fields of the
//     typed variables are errors);
//   - a standalone expression, or a literal, must have the type of the field it populates;
//   - template fields must exist in the kind's schema, since the API server would silently drop them (with a
//     suggestion when the name is close to a declared one);
//   - an object written out in the template must hold the fields its schema requires, since an expression only
//     replaces its own field and cannot add a sibling (see checkRequired);
//   - values without expressions must satisfy the schema's constraints (format, bounds, lengths, patterns, item
//     counts) and, for CustomResourceDefinition kinds, the CEL rules that cannot see an expression (see
//     validateLiterals);
//   - spec.phase.expression must return a boolean or an object, and spec.description / spec.url must render to text.
//
// template is the parsed spec.template (see ParseTemplate) and ks the resolved schema of gvk; a nil ks limits the
// checks to compilation. Errors are reported under fldPath; warnings name template fields the controller ignores.
func ValidateSpec(ctx context.Context, compiler *Compiler, gvk schema.GroupVersionKind, ks *KindSchema, template map[string]any, krcsSpec *promoterv1alpha1.KubernetesResourceCommitStatusSpec, fldPath *field.Path) (warnings []string, errs field.ErrorList) {
	var decl *apiservercel.DeclType
	var sch *spec.Schema
	var crd bool
	if ks != nil {
		decl = ks.Decl
		sch = ks.Schema
		crd = ks.CRD
	}
	templatePath := fldPath.Child("template")
	in := NewInterpolator(compiler, TemplateVariables, gvk, nil)
	errs = append(errs, checkRequired(sch, template, templatePath, true, crd)...)

	for key, value := range template {
		switch key {
		case "apiVersion", "kind":
			// Validated by ParseTemplate.
		case "status":
			warnings = append(warnings, fmt.Sprintf("%s is ignored: the controller never sets status", templatePath.Child(key)))
		case "metadata":
			w, e := checkMetadata(in, value, templatePath.Child(key))
			warnings = append(warnings, w...)
			errs = append(errs, e...)
		default:
			childSchema, known := schemaChild(sch, key)
			if !known {
				errs = append(errs, unknownField(templatePath.Child(key), gvk, key, sch))
				continue
			}
			errs = append(errs, checkValue(in, gvk, crd, value, FieldType(decl, key), childSchema, isRequired(decl, key), templatePath.Child(key))...)
		}
	}
	// The literal checks repeat the type and enum checks above in the API server's wording; keep only their
	// errors at paths not reported yet.
	reported := make(map[string]struct{}, len(errs))
	for _, e := range errs {
		reported[e.Field] = struct{}{}
	}
	for _, e := range validateLiterals(ctx, ks, template, templatePath) {
		if _, ok := reported[e.Field]; !ok {
			errs = append(errs, e)
		}
	}

	phasePath := fldPath.Child("phase", "expression")
	program, err := compiler.Compile(PhaseVariables, gvk, krcsSpec.Phase.Expression)
	switch {
	case err != nil:
		errs = append(errs, field.Invalid(phasePath, krcsSpec.Phase.Expression, err.Error()))
	default:
		if err := CheckPhaseOutputType(program.OutputType); err != nil {
			errs = append(errs, field.Invalid(phasePath, krcsSpec.Phase.Expression, err.Error()))
		}
	}

	report := NewInterpolator(compiler, ReportVariables, gvk, nil)
	if krcsSpec.Description != "" {
		if err := report.CheckText(krcsSpec.Description); err != nil {
			errs = append(errs, field.Invalid(fldPath.Child("description"), krcsSpec.Description, err.Error()))
		}
	}
	if krcsSpec.Url != "" {
		if err := report.CheckText(krcsSpec.Url); err != nil {
			errs = append(errs, field.Invalid(fldPath.Child("url"), krcsSpec.Url, err.Error()))
		}
	}
	return warnings, errs
}

// ValidateSpecWithRefresh is ValidateSpec, except that when it finds errors against a cached schema older than
// KindSchemaRefreshMinAge it re-resolves the schema once and validates again: a CRD may have just gained the field
// (or a CEL rule may have just changed), and the cached schema would otherwise reject the gate until KindSchemaTTL
// expires. The verdict against the cached schema stands when the schema cannot be re-resolved or is unchanged.
func ValidateSpecWithRefresh(ctx context.Context, compiler *Compiler, gvk schema.GroupVersionKind, ks *KindSchema, template map[string]any, krcsSpec *promoterv1alpha1.KubernetesResourceCommitStatusSpec, fldPath *field.Path) (warnings []string, errs field.ErrorList) {
	warnings, errs = ValidateSpec(ctx, compiler, gvk, ks, template, krcsSpec, fldPath)
	if len(errs) == 0 || ks == nil {
		return warnings, errs
	}
	fresh, changed, err := compiler.RefreshKindSchema(gvk, KindSchemaRefreshMinAge)
	if err != nil || !changed {
		return warnings, errs
	}
	return ValidateSpec(ctx, compiler, gvk, fresh, template, krcsSpec, fldPath)
}

// checkMetadata validates template metadata the way BuildObject consumes it: labels and annotations are rendered
// as text, everything else is replaced by the controller.
func checkMetadata(in *Interpolator, value any, path *field.Path) (warnings []string, errs field.ErrorList) {
	meta, ok := value.(map[string]any)
	if !ok {
		return nil, field.ErrorList{field.Invalid(path, value, "must be an object")}
	}
	for key, raw := range meta {
		switch key {
		case labelsKey, annotationsKey:
			entries, ok := raw.(map[string]any)
			if !ok {
				errs = append(errs, field.Invalid(path.Child(key), raw, "must be an object of strings"))
				continue
			}
			for name, v := range entries {
				s, ok := v.(string)
				if !ok {
					errs = append(errs, field.Invalid(path.Child(key).Key(name), v, "must be a string"))
					continue
				}
				if err := in.CheckText(s); err != nil {
					errs = append(errs, field.Invalid(path.Child(key).Key(name), s, err.Error()))
				}
			}
		case "name", "namespace":
			warnings = append(warnings, fmt.Sprintf("%s is ignored: the controller names each resource after the gate, environment, and SHA, in the gate's namespace", path.Child(key)))
		default:
			warnings = append(warnings, fmt.Sprintf("%s is ignored: only metadata.labels and metadata.annotations are copied from the template", path.Child(key)))
		}
	}
	return warnings, errs
}

// checkValue walks v in parallel with the kind's declaration and schema, checking expressions and literals
// against the declared type at each path and flagging fields the schema does not know. required says whether
// the parent declares the field required, in which case an expression that can remove it is rejected.
func checkValue(in *Interpolator, gvk schema.GroupVersionKind, crd bool, v any, decl *apiservercel.DeclType, sch *spec.Schema, required bool, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	switch t := v.(type) {
	case map[string]any:
		if decl != nil && !decl.IsObject() && !decl.IsMap() && decl.CelType().Kind() != cel.DynKind {
			return field.ErrorList{field.Invalid(path, "", fmt.Sprintf("the field expects %s but the value is an object", describeDeclType(decl)))}
		}
		errs = append(errs, checkRequired(sch, t, path, isEmbeddedResource(sch), crd)...)
		for key, child := range t {
			childSchema, known := schemaChild(sch, key)
			if !known {
				errs = append(errs, unknownField(path.Child(key), gvk, key, sch))
				continue
			}
			if err := checkEnum(decl, key, child, path.Child(key)); err != nil {
				errs = append(errs, err)
				continue
			}
			errs = append(errs, checkValue(in, gvk, crd, child, FieldType(decl, key), childSchema, isRequired(decl, key), path.Child(key))...)
		}
	case []any:
		if decl != nil && !decl.IsList() && decl.CelType().Kind() != cel.DynKind {
			return field.ErrorList{field.Invalid(path, "", fmt.Sprintf("the field expects %s but the value is a list", describeDeclType(decl)))}
		}
		for i, child := range t {
			errs = append(errs, checkValue(in, gvk, crd, child, ElemType(decl), schemaItems(sch), false, path.Index(i))...)
		}
	case string:
		if strings.Contains(t, "${") {
			if err := in.Check(t, decl, required); err != nil {
				errs = append(errs, field.Invalid(path, t, err.Error()))
			}
			return errs
		}
		errs = append(errs, checkLiteral(cel.StringType, t, decl, path)...)
	case bool:
		errs = append(errs, checkLiteral(cel.BoolType, t, decl, path)...)
	case int64:
		errs = append(errs, checkLiteral(cel.IntType, t, decl, path)...)
	case float64:
		errs = append(errs, checkLiteral(cel.DoubleType, t, decl, path)...)
	default:
		// null and anything else are copied verbatim; the API server validates them.
	}
	return errs
}

// isRequired reports whether parent declares key as a required field.
func isRequired(parent *apiservercel.DeclType, key string) bool {
	if parent == nil || !parent.IsObject() {
		return false
	}
	f, ok := parent.FindField(key)
	return ok && f != nil && f.Required
}

// checkEnum rejects a literal string that is not one of the field's enum values, when the schema declares any.
func checkEnum(parent *apiservercel.DeclType, key string, v any, path *field.Path) *field.Error {
	s, ok := v.(string)
	if !ok || strings.Contains(s, "${") || parent == nil || !parent.IsObject() {
		return nil
	}
	f, ok := parent.FindField(key)
	if !ok || len(f.EnumValues()) == 0 {
		return nil
	}
	allowed := make([]string, 0, len(f.EnumValues()))
	for _, e := range f.EnumValues() {
		if es, ok := e.Value().(string); ok {
			if es == s {
				return nil
			}
			allowed = append(allowed, es)
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	return field.NotSupported(path, s, allowed)
}

func checkLiteral(t *cel.Type, v any, decl *apiservercel.DeclType, path *field.Path) field.ErrorList {
	if err := CheckOutputType(t, decl); err != nil {
		return field.ErrorList{field.Invalid(path, v, "literal "+err.Error())}
	}
	return nil
}

func unknownField(path *field.Path, gvk schema.GroupVersionKind, key string, parent *spec.Schema) *field.Error {
	detail := fmt.Sprintf("unknown field: the schema of %s has no field %q here and the API server would drop it", gvk, key)
	if suggestion := closestKey(key, parent); suggestion != "" {
		detail += fmt.Sprintf(" (did you mean %q?)", suggestion)
	}
	return field.Invalid(path, key, detail)
}

// closestKey returns the property of parent nearest to key by edit distance, when it is close enough to be a typo.
// Adapted from PR #2343.
func closestKey(key string, parent *spec.Schema) string {
	if parent == nil {
		return ""
	}
	best, bestDistance := "", -1
	for name := range parent.Properties {
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

// checkRequired reports the required fields of sch that object, which is written out in the template, lacks.
// Nothing can add them later: an expression only replaces its own field's value. Fields inside an expression's
// result are never walked, so they are not checked. On a resource root (the template itself or an embedded
// resource) apiVersion, kind, metadata, and status are skipped, since the controller sets metadata and the renderer
// drops status. For CustomResourceDefinition kinds a field with a schema default is skipped, because the API server
// applies defaults first; built-in kinds publish placeholder defaults on fields that are still required, so their
// defaults do not count. Objects that accept unknown fields are not checked.
func checkRequired(sch *spec.Schema, object map[string]any, path *field.Path, resourceRoot, crd bool) field.ErrorList {
	if sch == nil || len(sch.Required) == 0 || preservesUnknownFieldsOnly(sch) {
		return nil
	}
	names := slices.Clone(sch.Required)
	slices.Sort(names)
	var errs field.ErrorList
	for _, name := range slices.Compact(names) {
		if _, ok := object[name]; ok {
			continue
		}
		if resourceRoot && (name == "apiVersion" || name == "kind" || name == fieldMetadata || name == fieldStatus) {
			continue
		}
		if crd {
			if prop, ok := sch.Properties[name]; ok && prop.Default != nil {
				continue
			}
		}
		errs = append(errs, field.Required(path.Child(name), ""))
	}
	return errs
}

// preservesUnknownFieldsOnly reports whether s accepts arbitrary content (preserve-unknown-fields or
// int-or-string). Unlike preservesUnknownFields it does not count embedded resources, whose declared fields are
// still checked.
func preservesUnknownFieldsOnly(s *spec.Schema) bool {
	for _, ext := range []string{"x-kubernetes-preserve-unknown-fields", "x-kubernetes-int-or-string"} {
		if v, ok := s.Extensions.GetBool(ext); ok && v {
			return true
		}
	}
	return false
}

func isEmbeddedResource(s *spec.Schema) bool {
	if s == nil {
		return false
	}
	v, ok := s.Extensions.GetBool("x-kubernetes-embedded-resource")
	return ok && v
}

// schemaChild returns the schema of key under parent and whether key is permitted there. An unknown parent, an
// object that preserves unknown fields, an embedded resource, a union, an int-or-string, or an object without
// declared properties all permit any key.
func schemaChild(parent *spec.Schema, key string) (*spec.Schema, bool) {
	if parent == nil {
		return nil, true
	}
	if prop, ok := parent.Properties[key]; ok {
		return &prop, true
	}
	if parent.AdditionalProperties != nil {
		return parent.AdditionalProperties.Schema, true
	}
	if len(parent.Properties) == 0 || !slices.Contains(parent.Type, "object") || preservesUnknownFields(parent) ||
		len(parent.AllOf) > 0 || len(parent.AnyOf) > 0 || len(parent.OneOf) > 0 {
		return nil, true
	}
	return nil, false
}

// schemaItems returns the item schema of a list, or nil when unknown.
func schemaItems(parent *spec.Schema) *spec.Schema {
	if parent == nil || parent.Items == nil {
		return nil
	}
	return parent.Items.Schema
}

func preservesUnknownFields(s *spec.Schema) bool {
	for _, ext := range []string{"x-kubernetes-preserve-unknown-fields", "x-kubernetes-embedded-resource", "x-kubernetes-int-or-string"} {
		if v, ok := s.Extensions.GetBool(ext); ok && v {
			return true
		}
	}
	return false
}
