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
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	apiextensionsinternal "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	structuralschema "k8s.io/apiextensions-apiserver/pkg/apiserver/schema"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/openapi"
	"k8s.io/client-go/openapi3"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

const (
	componentsRefPrefix = "#/components/schemas/"
	gvkExtension        = "x-kubernetes-group-version-kind"
	// DefaultSchemaTTL is how long a GroupVersion's published OpenAPI document is reused before it is fetched again.
	DefaultSchemaTTL = 5 * time.Minute
)

// KindSchema is the published OpenAPI v3 schema of one kind, with the components its $refs point into.
type KindSchema struct {
	// Root is the kind's top-level schema.
	Root *spec.Schema
	// Components are all schemas of the kind's GroupVersion document, keyed by name; $refs resolve here.
	Components map[string]*spec.Schema
	// Structural is the kind's schema as a structural schema, set only when the schema carries
	// x-kubernetes-validations (CEL rules, which only CustomResourceDefinitions publish). Nil otherwise, or when
	// the schema cannot be made structural, in which case CEL rules are not evaluated.
	Structural *structuralschema.Structural
	// fetched is when the GroupVersion document was read.
	fetched time.Time
	// GVK is the kind the schema describes.
	GVK schema.GroupVersionKind
}

// SchemaSource reads kind schemas from the API server's /openapi/v3 endpoint and caches each GroupVersion
// document for a TTL. The endpoint is readable by every authenticated user (system:discovery), so no RBAC is
// needed. It is safe for concurrent use.
type SchemaSource struct {
	client openapi.Client
	now    func() time.Time
	kinds  map[schema.GroupVersionKind]*KindSchema
	ttl    time.Duration
	mu     sync.Mutex
}

// NewSchemaSource returns a SchemaSource reading from client (usually discovery.OpenAPIV3()). A ttl of zero uses
// DefaultSchemaTTL.
func NewSchemaSource(client openapi.Client, ttl time.Duration) *SchemaSource {
	if ttl <= 0 {
		ttl = DefaultSchemaTTL
	}
	return &SchemaSource{client: client, ttl: ttl, now: time.Now, kinds: map[schema.GroupVersionKind]*KindSchema{}}
}

// Get returns the schema of gvk, from the cache when it is younger than the TTL.
func (s *SchemaSource) Get(gvk schema.GroupVersionKind) (*KindSchema, error) {
	s.mu.Lock()
	cached, ok := s.kinds[gvk]
	s.mu.Unlock()
	if ok && s.now().Sub(cached.fetched) < s.ttl {
		return cached, nil
	}
	return s.fetch(gvk)
}

// Refresh fetches gvk's schema again unless the cached copy is younger than minAge, and reports whether it did.
// Callers use it once after finding unknown fields, so a CRD that was just upgraded is not judged by its old schema.
func (s *SchemaSource) Refresh(gvk schema.GroupVersionKind, minAge time.Duration) (*KindSchema, bool, error) {
	s.mu.Lock()
	cached, ok := s.kinds[gvk]
	s.mu.Unlock()
	if ok && s.now().Sub(cached.fetched) < minAge {
		return cached, false, nil
	}
	ks, err := s.fetch(gvk)
	return ks, err == nil, err
}

func (s *SchemaSource) fetch(gvk schema.GroupVersionKind) (*KindSchema, error) {
	doc, err := openapi3.NewRoot(s.client).GVSpec(gvk.GroupVersion())
	if err != nil {
		return nil, fmt.Errorf("failed to read the OpenAPI schema of %s: %w", gvk.GroupVersion(), err)
	}
	if doc.Components == nil {
		return nil, fmt.Errorf("the OpenAPI document of %s has no schemas", gvk.GroupVersion())
	}
	var found *KindSchema
	for _, root := range doc.Components.Schemas {
		if slices.Contains(schemaGVKs(root), gvk) {
			found = &KindSchema{GVK: gvk, Root: root, Components: doc.Components.Schemas, fetched: s.now()}
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("the API server publishes no OpenAPI schema for %s", gvk)
	}
	found.Structural = celStructural(found.Root)

	s.mu.Lock()
	s.kinds[gvk] = found
	s.mu.Unlock()
	return found, nil
}

// schemaGVKs reads the x-kubernetes-group-version-kind extension of a component schema.
func schemaGVKs(s *spec.Schema) []schema.GroupVersionKind {
	raw, ok := s.Extensions[gvkExtension]
	if !ok {
		return nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]schema.GroupVersionKind, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		group, _ := m["group"].(string)
		version, _ := m["version"].(string)
		kind, _ := m["kind"].(string)
		if version == "" || kind == "" {
			continue
		}
		out = append(out, schema.GroupVersionKind{Group: group, Version: version, Kind: kind})
	}
	return out
}

// celStructural converts a kind's published schema into a structural schema for CEL evaluation. It returns nil
// when the schema has no CEL rules (every built-in kind) or cannot be converted. Published CRD schemas reference
// ObjectMeta for metadata, which is replaced by a plain object: the controller sets metadata itself and rules on
// the root, the only node that could read it, are never evaluated (see stripRules).
func celStructural(root *spec.Schema) *structuralschema.Structural {
	raw, err := json.Marshal(root)
	if err != nil || !strings.Contains(string(raw), `"x-kubernetes-validations"`) {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	delete(m, gvkExtension)
	if props, ok := m["properties"].(map[string]any); ok {
		if _, ok := props["metadata"]; ok {
			props["metadata"] = map[string]any{"type": "object"}
		}
	}
	raw, err = json.Marshal(m)
	if err != nil {
		return nil
	}
	var v1Props apiextensionsv1.JSONSchemaProps
	if err := json.Unmarshal(raw, &v1Props); err != nil {
		return nil
	}
	var internal apiextensionsinternal.JSONSchemaProps
	if err := apiextensionsv1.Convert_v1_JSONSchemaProps_To_apiextensions_JSONSchemaProps(&v1Props, &internal, nil); err != nil {
		return nil
	}
	structural, err := structuralschema.NewStructural(&internal)
	if err != nil {
		return nil
	}
	return structural
}
