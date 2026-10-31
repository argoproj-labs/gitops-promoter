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

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

const (
	// JSON schema keywords and values used throughout the normalizer.
	keyType                = "type"
	keyProperties          = "properties"
	keyItems               = "items"
	keyAdditionalProps     = "additionalProperties"
	keyRequired            = "required"
	keyDefault             = "default"
	keyFormat              = "format"
	keyOneOf               = "oneOf"
	keyAnyOf               = "anyOf"
	keyAllOf               = "allOf"
	keyNot                 = "not"
	keyIntOrString         = "x-kubernetes-int-or-string"
	keyPreserveUnknown     = "x-kubernetes-preserve-unknown-fields"
	keyListType            = "x-kubernetes-list-type"
	keyListMapKeys         = "x-kubernetes-list-map-keys"
	typeObject             = "object"
	typeArray              = "array"
	typeString             = "string"
	formatIntOrString      = "int-or-string"
	unionIntegerString     = "integer,string"
	unionNumberString      = "number,string"
	rootPropertyMetadata   = "metadata"
	rootPropertyStatus     = "status"
	listTypeMap            = "map"
	listTypeAtomic         = "atomic"
	permissiveSpecProperty = "spec"
)

// crdFormatSafelist lists OpenAPI formats that CRD validation understands and that are worth keeping.
// Every other format is stripped; the apiserver would ignore unknown formats anyway.
var crdFormatSafelist = map[string]bool{"date-time": true, "date": true, "byte": true}

// droppedKeywords are removed from every node: descriptive keywords that only add size, and extensions
// that CRD schemas do not support (they would be lost in the JSONSchemaProps round-trip anyway).
var droppedKeywords = []string{
	"description", "title", "example", "x-kubernetes-group-version-kind",
	"x-kubernetes-patch-strategy", "x-kubernetes-patch-merge-key", "x-kubernetes-unions",
	"$schema", "id", "$id",
}

// junctorKeywords cannot be expressed structurally without per-type analysis.
var junctorKeywords = []string{keyOneOf, keyAnyOf, keyNot, keyAllOf, keyType, keyProperties, keyItems, keyAdditionalProps, keyRequired}

// PermissiveSchema returns the fallback schema used when the source schema cannot be converted into a
// structural schema: apiVersion/kind/metadata plus an unvalidated spec.
func PermissiveSchema() *apiextensionsv1.JSONSchemaProps {
	return &apiextensionsv1.JSONSchemaProps{
		Type: typeObject,
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"apiVersion":           {Type: typeString},
			"kind":                 {Type: typeString},
			rootPropertyMetadata:   {Type: typeObject},
			permissiveSpecProperty: {Type: typeObject, XPreserveUnknownFields: new(true)},
		},
		XPreserveUnknownFields: new(true),
	}
}

// StructuralSchemaFromOpenAPISchema converts the OpenAPI schema of a built-in kind, as returned by
// k8s.io/apiserver/pkg/cel/openapi/resolver (refs inlined, allOf ref wrappers unwrapped, recursion
// collapsed to {type: object}), into a structural CRD schema suitable for a wrapped template kind.
//
// Conversion rules (verified against apiextensions structural-schema validation):
//   - IntOrString (format int-or-string, oneOf integer|string) and Quantity (oneOf string|number) become
//     x-kubernetes-int-or-string; any other oneOf/anyOf collapses to a preserve-unknown-fields object
//   - defaults are dropped (they would fail CRD default validation, e.g. spec: {} against a required
//     template, and would be materialized into stored templates), so list-type map becomes atomic
//   - descriptions and non-safelisted formats are dropped to keep the CRD small
//   - object nodes without properties (the resolver's recursion placeholder, RawExtension fields) get
//     x-kubernetes-preserve-unknown-fields so pruning does not empty them
//   - properties.status is removed and metadata collapses to {type: object}
func StructuralSchemaFromOpenAPISchema(resolved *spec.Schema) (*apiextensionsv1.JSONSchemaProps, error) {
	if resolved == nil {
		return nil, errors.New("resolved OpenAPI schema is nil")
	}
	m, err := schemaToMap(resolved)
	if err != nil {
		return nil, err
	}
	n := &normalizer{dropDefaults: true, preserveEmptyObjects: true}
	n.normalize(m, true)
	return toJSONSchemaProps(m)
}

// StructuralSchemaFromCRD adapts an existing CRD version schema for use as a wrapped template kind:
// status is removed, metadata collapses to {type: object}, and descriptions are dropped. Defaults are kept
// because they already passed CRD validation in the source CRD.
func StructuralSchemaFromCRD(source *apiextensionsv1.JSONSchemaProps) (*apiextensionsv1.JSONSchemaProps, error) {
	m, err := schemaToMap(source)
	if err != nil {
		return nil, err
	}
	n := &normalizer{dropDefaults: false}
	n.normalize(m, true)
	return toJSONSchemaProps(m)
}

// schemaToMap round-trips a typed schema (spec.Schema or JSONSchemaProps) into a generic JSON map. Both
// types marshal x-kubernetes-* extensions as top-level keys, so the normalizer sees one shape.
func schemaToMap(source any) (map[string]any, error) {
	raw, err := json.Marshal(source)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal source schema: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("failed to unmarshal source schema: %w", err)
	}
	return m, nil
}

type normalizer struct {
	dropDefaults bool
	// preserveEmptyObjects marks object nodes without properties/additionalProperties as
	// x-kubernetes-preserve-unknown-fields. Used for OpenAPI input, where such nodes stand for recursion
	// placeholders or RawExtension-like fields; CRD input is copied as is.
	preserveEmptyObjects bool
}

// normalize rewrites a schema node (and its children) in place into structural form. root marks the
// top-level object schema of the kind.
func (n *normalizer) normalize(node map[string]any, root bool) {
	n.flattenAllOf(node)
	n.scrub(node)
	n.convertUnions(node)
	n.normalizeChildren(node, root)
	n.ensureTyped(node)
}

// scrub removes keywords that are not wanted or not allowed in a structural schema.
func (n *normalizer) scrub(node map[string]any) {
	for _, k := range droppedKeywords {
		delete(node, k)
	}
	if n.dropDefaults {
		delete(node, keyDefault)
	}
	if f, ok := node[keyFormat].(string); ok && !crdFormatSafelist[f] && f != formatIntOrString {
		delete(node, keyFormat)
	}
	if ap, ok := node[keyAdditionalProps].(bool); ok && !ap {
		// additionalProperties: false is forbidden in structural schemas.
		delete(node, keyAdditionalProps)
	}
	if n.dropDefaults {
		if lt, ok := node[keyListType].(string); ok && lt == listTypeMap {
			// list-type map requires its keys to have defaults or be required; without defaults, use atomic.
			node[keyListType] = listTypeAtomic
			delete(node, keyListMapKeys)
		}
	}
}

// convertUnions turns IntOrString/Quantity into x-kubernetes-int-or-string and collapses any other
// junctor into a permissive object.
func (n *normalizer) convertUnions(node map[string]any) {
	switch {
	case isIntOrString(node), isQuantity(node):
		for _, k := range []string{keyType, keyOneOf, keyAnyOf, keyFormat, "pattern"} {
			delete(node, k)
		}
		node[keyIntOrString] = true
	case node[keyOneOf] != nil || node[keyAnyOf] != nil || node[keyNot] != nil || node[keyAllOf] != nil:
		for _, k := range junctorKeywords {
			delete(node, k)
		}
		node[keyPreserveUnknown] = true
	default:
		// Plain typed node: nothing to convert.
	}
}

// normalizeChildren recurses into properties, items, and additionalProperties, applying the root-only rules
// (drop status, collapse metadata) on the way.
func (n *normalizer) normalizeChildren(node map[string]any, root bool) {
	if props, ok := node[keyProperties].(map[string]any); ok {
		if root {
			delete(props, rootPropertyStatus)
			props[rootPropertyMetadata] = map[string]any{keyType: typeObject}
			node[keyRequired] = removeFromStringList(node[keyRequired], rootPropertyStatus)
		}
		for k, child := range props {
			childMap, ok := child.(map[string]any)
			if !ok || (root && k == rootPropertyMetadata) {
				continue
			}
			n.normalize(childMap, false)
		}
		setTypeIfMissing(node, typeObject)
	}
	if items, ok := node[keyItems].(map[string]any); ok {
		n.normalize(items, false)
		setTypeIfMissing(node, typeArray)
	}
	if ap, ok := node[keyAdditionalProps].(map[string]any); ok {
		n.normalize(ap, false)
		setTypeIfMissing(node, typeObject)
	}
	if req, ok := node[keyRequired].([]any); ok && len(req) == 0 {
		delete(node, keyRequired)
	}
}

// ensureTyped gives untyped leaves a permissive marker so the schema stays structural, and (for OpenAPI
// input) lets property-less objects keep their contents.
func (n *normalizer) ensureTyped(node map[string]any) {
	t, hasType := node[keyType].(string)
	_, intOrString := node[keyIntOrString]
	_, preserve := node[keyPreserveUnknown]
	if !hasType && !intOrString && !preserve {
		node[keyPreserveUnknown] = true
		return
	}
	if !n.preserveEmptyObjects || t != typeObject || preserve {
		return
	}
	_, hasProps := node[keyProperties]
	_, hasAdditional := node[keyAdditionalProps]
	if !hasProps && !hasAdditional {
		node[keyPreserveUnknown] = true
	}
}

func setTypeIfMissing(node map[string]any, t string) {
	if _, hasType := node[keyType]; !hasType {
		node[keyType] = t
	}
}

// flattenAllOf merges a single-element allOf into node. The resolver already unwraps the kube-apiserver's
// allOf ref wrappers; this covers any remaining single-element allOf. Keys present on node win.
func (n *normalizer) flattenAllOf(node map[string]any) {
	for {
		allOf, ok := node[keyAllOf].([]any)
		if !ok || len(allOf) != 1 {
			return
		}
		child, ok := allOf[0].(map[string]any)
		if !ok {
			return
		}
		delete(node, keyAllOf)
		for k, v := range child {
			if _, exists := node[k]; !exists {
				node[k] = v
			}
		}
	}
}

func isIntOrString(node map[string]any) bool {
	if f, _ := node[keyFormat].(string); f == formatIntOrString {
		return true
	}
	if b, ok := node[keyIntOrString].(bool); ok && b {
		return true
	}
	return unionTypes(node) == unionIntegerString
}

func isQuantity(node map[string]any) bool {
	return unionTypes(node) == unionNumberString
}

// unionTypes returns the sorted comma-joined primitive types of a oneOf/anyOf whose members are bare types.
func unionTypes(node map[string]any) string {
	members, ok := node[keyOneOf].([]any)
	if !ok {
		members, ok = node[keyAnyOf].([]any)
	}
	if !ok || len(members) != 2 {
		return ""
	}
	types := make([]string, 0, 2)
	for _, m := range members {
		mm, ok := m.(map[string]any)
		if !ok || len(mm) != 1 {
			return ""
		}
		t, ok := mm[keyType].(string)
		if !ok {
			return ""
		}
		types = append(types, t)
	}
	if types[0] > types[1] {
		types[0], types[1] = types[1], types[0]
	}
	return types[0] + "," + types[1]
}

func removeFromStringList(raw any, remove string) any {
	list, ok := raw.([]any)
	if !ok {
		return raw
	}
	out := make([]any, 0, len(list))
	for _, v := range list {
		if s, ok := v.(string); ok && s == remove {
			continue
		}
		out = append(out, v)
	}
	return out
}

func toJSONSchemaProps(m map[string]any) (*apiextensionsv1.JSONSchemaProps, error) {
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal normalized schema: %w", err)
	}
	props := &apiextensionsv1.JSONSchemaProps{}
	if err := json.Unmarshal(raw, props); err != nil {
		return nil, fmt.Errorf("failed to convert normalized schema to JSONSchemaProps: %w", err)
	}
	return props, nil
}
