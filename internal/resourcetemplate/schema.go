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
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// componentsSchemasRefPrefix is the $ref prefix used by the kube-apiserver /openapi/v3 documents.
	componentsSchemasRefPrefix = "#/components/schemas/"
	// maxInlineDepth bounds $ref inlining. Deeper subtrees collapse to a permissive object.
	maxInlineDepth = 48
	// gvkExtension is the OpenAPI extension that names the group/version/kind(s) a schema component describes.
	gvkExtension = "x-kubernetes-group-version-kind"
)

// crdFormatSafelist lists OpenAPI formats that CRD validation understands and that are worth keeping.
// Every other format is stripped; the apiserver would ignore unknown formats anyway.
var crdFormatSafelist = map[string]bool{"date-time": true, "date": true, "byte": true}

// openAPIDocument is the subset of a kube-apiserver /openapi/v3 group-version document this package reads.
type openAPIDocument struct {
	Components openAPIComponents `json:"components"`
}

// openAPIComponents holds the reusable schema components of an OpenAPI v3 document.
type openAPIComponents struct {
	Schemas map[string]any `json:"schemas"`
}

// PermissiveSchema returns the fallback schema used when the source schema cannot be converted into a
// structural schema: apiVersion/kind/metadata plus an unvalidated spec.
func PermissiveSchema() *apiextensionsv1.JSONSchemaProps {
	return &apiextensionsv1.JSONSchemaProps{
		Type: "object",
		Properties: map[string]apiextensionsv1.JSONSchemaProps{
			"apiVersion": {Type: "string"},
			"kind":       {Type: "string"},
			"metadata":   {Type: "object"},
			"spec":       {Type: "object", XPreserveUnknownFields: new(true)},
		},
		XPreserveUnknownFields: new(true),
	}
}

// StructuralSchemaFromOpenAPIV3 extracts the schema component for gvk from a kube-apiserver /openapi/v3
// group-version document and converts it into a structural CRD schema suitable for a wrapped template kind.
//
// Conversion rules (verified against apiextensions structural-schema validation):
//   - $ref is inlined recursively (depth-limited, cycle-guarded) and single-element allOf wrappers are
//     flattened into their parent, because type/default/description inside allOf are rejected
//   - IntOrString (format int-or-string, oneOf integer|string) and Quantity (oneOf string|number) become
//     x-kubernetes-int-or-string; any other oneOf/anyOf collapses to a preserve-unknown-fields object
//   - defaults are dropped (they would fail CRD default validation, e.g. spec: {} against a required
//     template, and would be materialized into stored templates), so list-type map becomes atomic
//   - descriptions and non-safelisted formats are dropped to keep the CRD small
//   - properties.status is removed and metadata collapses to {type: object}
func StructuralSchemaFromOpenAPIV3(doc []byte, gvk schema.GroupVersionKind) (*apiextensionsv1.JSONSchemaProps, error) {
	var root openAPIDocument
	if err := json.Unmarshal(doc, &root); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAPI v3 document: %w", err)
	}
	if len(root.Components.Schemas) == 0 {
		return nil, errors.New("OpenAPI v3 document has no components.schemas")
	}

	name, component := findComponentForGVK(root.Components.Schemas, gvk)
	if component == nil {
		return nil, fmt.Errorf("no schema component declares %s %s", gvkExtension, gvk)
	}

	n := &normalizer{components: root.Components.Schemas, dropDefaults: true}
	inlined, err := n.inline(component, map[string]bool{name: true}, 0)
	if err != nil {
		return nil, err
	}
	rootMap, ok := inlined.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema component %s is not an object schema", name)
	}
	n.normalize(rootMap, true)
	return toJSONSchemaProps(rootMap)
}

// StructuralSchemaFromCRD adapts an existing CRD version schema for use as a wrapped template kind:
// status is removed, metadata collapses to {type: object}, and descriptions are dropped. Defaults are kept
// because they already passed CRD validation in the source CRD.
func StructuralSchemaFromCRD(source *apiextensionsv1.JSONSchemaProps) (*apiextensionsv1.JSONSchemaProps, error) {
	raw, err := json.Marshal(source)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal source CRD schema: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("failed to unmarshal source CRD schema: %w", err)
	}
	n := &normalizer{dropDefaults: false}
	n.normalize(m, true)
	return toJSONSchemaProps(m)
}

func findComponentForGVK(schemas map[string]any, gvk schema.GroupVersionKind) (string, map[string]any) {
	for name, raw := range schemas {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		gvks, ok := m[gvkExtension].([]any)
		if !ok {
			continue
		}
		for _, g := range gvks {
			gm, ok := g.(map[string]any)
			if !ok {
				continue
			}
			if asString(gm["group"]) == gvk.Group && asString(gm["version"]) == gvk.Version && asString(gm["kind"]) == gvk.Kind {
				return name, m
			}
		}
	}
	return "", nil
}

type normalizer struct {
	components   map[string]any
	dropDefaults bool
}

// inline returns a deep copy of v with every $ref replaced by the referenced component. visiting holds the
// component names on the current path so recursive types collapse to a permissive object instead of looping.
func (n *normalizer) inline(v any, visiting map[string]bool, depth int) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		if ref, ok := t["$ref"].(string); ok {
			name := strings.TrimPrefix(ref, componentsSchemasRefPrefix)
			if name == ref {
				return nil, fmt.Errorf("unsupported $ref %q", ref)
			}
			target, ok := n.components[name].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("unresolved $ref %q", ref)
			}
			if visiting[name] || depth >= maxInlineDepth {
				return permissiveNode(), nil
			}
			visiting[name] = true
			out, err := n.inline(target, visiting, depth+1)
			delete(visiting, name)
			if err != nil {
				return nil, err
			}
			// Keep sibling keys of the $ref (OpenAPI 3.1 allows description/default next to $ref).
			if outMap, ok := out.(map[string]any); ok {
				for k, sibling := range t {
					if k == "$ref" {
						continue
					}
					if _, exists := outMap[k]; !exists {
						outMap[k] = sibling
					}
				}
			}
			return out, nil
		}
		out := make(map[string]any, len(t))
		for k, child := range t {
			r, err := n.inline(child, visiting, depth+1)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			r, err := n.inline(child, visiting, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

func permissiveNode() map[string]any {
	return map[string]any{"type": "object", "x-kubernetes-preserve-unknown-fields": true}
}

// normalize rewrites a schema node (and its children) in place into structural form. root marks the
// top-level object schema of the kind.
func (n *normalizer) normalize(node map[string]any, root bool) {
	n.flattenAllOf(node)

	delete(node, "description")
	delete(node, "title")
	delete(node, "example")
	delete(node, gvkExtension)
	delete(node, "x-kubernetes-patch-strategy")
	delete(node, "x-kubernetes-patch-merge-key")
	delete(node, "x-kubernetes-unions")
	delete(node, "$schema")
	delete(node, "id")
	delete(node, "$id")
	if n.dropDefaults {
		delete(node, "default")
	}
	if f, ok := node["format"].(string); ok && !crdFormatSafelist[f] {
		delete(node, "format")
	}
	if ap, ok := node["additionalProperties"].(bool); ok && !ap {
		// additionalProperties: false is forbidden in structural schemas.
		delete(node, "additionalProperties")
	}

	switch {
	case isIntOrString(node), isQuantity(node):
		delete(node, "type")
		delete(node, "oneOf")
		delete(node, "anyOf")
		delete(node, "format")
		delete(node, "pattern")
		node["x-kubernetes-int-or-string"] = true
	case node["oneOf"] != nil || node["anyOf"] != nil || node["not"] != nil || node["allOf"] != nil:
		// Remaining junctors cannot be expressed structurally without per-type analysis; accept anything.
		for _, k := range []string{"oneOf", "anyOf", "not", "allOf", "type", "properties", "items", "additionalProperties", "required"} {
			delete(node, k)
		}
		node["x-kubernetes-preserve-unknown-fields"] = true
	default:
		// Plain typed node: nothing to convert.
	}

	if n.dropDefaults {
		if lt, ok := node["x-kubernetes-list-type"].(string); ok && lt == "map" {
			node["x-kubernetes-list-type"] = "atomic"
			delete(node, "x-kubernetes-list-map-keys")
		}
	}

	if props, ok := node["properties"].(map[string]any); ok {
		if root {
			delete(props, "status")
			props["metadata"] = map[string]any{"type": "object"}
			node["required"] = removeFromStringList(node["required"], "status")
		}
		for k, child := range props {
			childMap, ok := child.(map[string]any)
			if !ok {
				continue
			}
			if root && k == "metadata" {
				continue
			}
			n.normalize(childMap, false)
		}
		if _, hasType := node["type"]; !hasType {
			node["type"] = "object"
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		n.normalize(items, false)
		if _, hasType := node["type"]; !hasType {
			node["type"] = "array"
		}
	}
	if ap, ok := node["additionalProperties"].(map[string]any); ok {
		n.normalize(ap, false)
		if _, hasType := node["type"]; !hasType {
			node["type"] = "object"
		}
	}

	if req, ok := node["required"].([]any); ok && len(req) == 0 {
		delete(node, "required")
	}

	_, hasType := node["type"]
	_, intOrString := node["x-kubernetes-int-or-string"]
	_, preserve := node["x-kubernetes-preserve-unknown-fields"]
	if !hasType && !intOrString && !preserve {
		node["x-kubernetes-preserve-unknown-fields"] = true
	}
}

// flattenAllOf merges a single-element allOf (the kube-apiserver's wrapper for struct-typed fields) into node.
// Keys present on node win over the wrapped schema, except description, which is dropped anyway.
func (n *normalizer) flattenAllOf(node map[string]any) {
	for {
		allOf, ok := node["allOf"].([]any)
		if !ok || len(allOf) != 1 {
			return
		}
		child, ok := allOf[0].(map[string]any)
		if !ok {
			return
		}
		delete(node, "allOf")
		for k, v := range child {
			if _, exists := node[k]; !exists {
				node[k] = v
			}
		}
	}
}

func isIntOrString(node map[string]any) bool {
	if f, _ := node["format"].(string); f == "int-or-string" {
		return true
	}
	if b, ok := node["x-kubernetes-int-or-string"].(bool); ok && b {
		return true
	}
	return unionTypes(node) == "integer,string"
}

func isQuantity(node map[string]any) bool {
	return unionTypes(node) == "number,string"
}

// unionTypes returns the sorted comma-joined primitive types of a oneOf/anyOf whose members are bare types.
func unionTypes(node map[string]any) string {
	members, ok := node["oneOf"].([]any)
	if !ok {
		members, ok = node["anyOf"].([]any)
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
		t, ok := mm["type"].(string)
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

func asString(v any) string {
	s, _ := v.(string)
	return s
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
