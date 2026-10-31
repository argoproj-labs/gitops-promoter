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
//   - template fields must exist in the kind's schema, since the API server would silently drop them;
//   - spec.phase.expression must return a boolean or an object, and spec.description / spec.url must render to text.
//
// template is the parsed spec.template (see ParseTemplate) and ks the resolved schema of gvk; a nil ks limits the
// checks to compilation. Errors are reported under fldPath; warnings name template fields the controller ignores.
func ValidateSpec(compiler *Compiler, gvk schema.GroupVersionKind, ks *KindSchema, template map[string]any, krcsSpec *promoterv1alpha1.KubernetesResourceCommitStatusSpec, fldPath *field.Path) (warnings []string, errs field.ErrorList) {
	var decl *apiservercel.DeclType
	var sch *spec.Schema
	if ks != nil {
		decl = ks.Decl
		sch = ks.Schema
	}
	templatePath := fldPath.Child("template")
	in := NewInterpolator(compiler, TemplateVariables, gvk, nil)

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
				errs = append(errs, unknownField(templatePath.Child(key), gvk, key))
				continue
			}
			errs = append(errs, checkValue(in, gvk, value, FieldType(decl, key), childSchema, isRequired(decl, key), templatePath.Child(key))...)
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

// checkMetadata validates template metadata the way BuildObject consumes it: labels and annotations are rendered
// as text, everything else is replaced by the controller.
func checkMetadata(in *Interpolator, value any, path *field.Path) (warnings []string, errs field.ErrorList) {
	meta, ok := value.(map[string]any)
	if !ok {
		return nil, field.ErrorList{field.Invalid(path, value, "must be an object")}
	}
	for key, raw := range meta {
		switch key {
		case "labels", "annotations":
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
func checkValue(in *Interpolator, gvk schema.GroupVersionKind, v any, decl *apiservercel.DeclType, sch *spec.Schema, required bool, path *field.Path) field.ErrorList {
	var errs field.ErrorList
	switch t := v.(type) {
	case map[string]any:
		if decl != nil && !decl.IsObject() && !decl.IsMap() && decl.CelType().Kind() != cel.DynKind {
			return field.ErrorList{field.Invalid(path, "", fmt.Sprintf("the field expects %s but the value is an object", describeDeclType(decl)))}
		}
		for key, child := range t {
			childSchema, known := schemaChild(sch, key)
			if !known {
				errs = append(errs, unknownField(path.Child(key), gvk, key))
				continue
			}
			if err := checkEnum(decl, key, child, path.Child(key)); err != nil {
				errs = append(errs, err)
				continue
			}
			errs = append(errs, checkValue(in, gvk, child, FieldType(decl, key), childSchema, isRequired(decl, key), path.Child(key))...)
		}
	case []any:
		if decl != nil && !decl.IsList() && decl.CelType().Kind() != cel.DynKind {
			return field.ErrorList{field.Invalid(path, "", fmt.Sprintf("the field expects %s but the value is a list", describeDeclType(decl)))}
		}
		for i, child := range t {
			errs = append(errs, checkValue(in, gvk, child, ElemType(decl), schemaItems(sch), false, path.Index(i))...)
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
	return ok && f.Required
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

func unknownField(path *field.Path, gvk schema.GroupVersionKind, key string) *field.Error {
	return field.Invalid(path, key, fmt.Sprintf("unknown field: the schema of %s has no field %q here and the API server would drop it", gvk, key))
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
