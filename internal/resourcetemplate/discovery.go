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
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/cel/openapi/resolver"
	"k8s.io/client-go/discovery"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

const (
	componentsRefPrefix = "#/components/schemas/"
	gvkExtension        = "x-kubernetes-group-version-kind"
	// crdDocumentTitle is the info.title the API server gives the OpenAPI v3 document of a CustomResourceDefinition
	// group version.
	crdDocumentTitle = "Kubernetes CRD Swagger"
)

// OriginResolver is a SchemaResolver that also reports whether the kind is served from a CustomResourceDefinition.
// The API server applies a CRD's schema defaults before it checks required fields, while built-in kinds publish
// placeholder defaults (such as name: {default: ""}) on fields that are still required, so the static checks
// treat defaults differently for the two.
type OriginResolver interface {
	resolver.SchemaResolver
	ResolveSchemaWithOrigin(gvk schema.GroupVersionKind) (s *spec.Schema, crd bool, err error)
}

// DiscoveryResolver resolves kind schemas from the API server's /openapi/v3 documents, like
// resolver.ClientDiscoveryResolver, and reports CRD origin from the document title. The documents are readable
// through system:discovery, so it needs no RBAC.
type DiscoveryResolver struct {
	Discovery discovery.DiscoveryInterface
}

var _ OriginResolver = (*DiscoveryResolver)(nil)

// ResolveSchema implements resolver.SchemaResolver.
func (r *DiscoveryResolver) ResolveSchema(gvk schema.GroupVersionKind) (*spec.Schema, error) {
	s, _, err := r.ResolveSchemaWithOrigin(gvk)
	return s, err
}

// ResolveSchemaWithOrigin implements OriginResolver.
func (r *DiscoveryResolver) ResolveSchemaWithOrigin(gvk schema.GroupVersionKind) (*spec.Schema, bool, error) {
	paths, err := r.Discovery.OpenAPIV3().Paths()
	if err != nil {
		return nil, false, fmt.Errorf("failed to list OpenAPI v3 documents: %w", err)
	}
	path := "apis/" + gvk.Group + "/" + gvk.Version
	if gvk.Group == "" {
		path = "api/" + gvk.Version
	}
	gv, ok := paths[path]
	if !ok {
		return nil, false, fmt.Errorf("cannot resolve group version %q: %w", gvk.GroupVersion(), resolver.ErrSchemaNotFound)
	}
	raw, err := gv.Schema(runtime.ContentTypeJSON)
	if err != nil {
		return nil, false, fmt.Errorf("failed to read the OpenAPI v3 document of %s: %w", gvk.GroupVersion(), err)
	}
	var doc openAPIDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, false, fmt.Errorf("failed to decode the OpenAPI v3 document of %s: %w", gvk.GroupVersion(), err)
	}
	root, err := findKindRef(doc.Components.Schemas, gvk)
	if err != nil {
		return nil, false, err
	}
	s, err := resolver.PopulateRefs(func(ref string) (*spec.Schema, bool) {
		s, ok := doc.Components.Schemas[strings.TrimPrefix(ref, componentsRefPrefix)]
		return s, ok
	}, root)
	if err != nil {
		return nil, false, fmt.Errorf("failed to resolve the schema of %s: %w", gvk, err)
	}
	return s, doc.Info.Title == crdDocumentTitle, nil
}

// openAPIDocument is the part of an OpenAPI v3 group-version document the resolver reads.
type openAPIDocument struct {
	Components openAPIComponents `json:"components"`
	Info       openAPIInfo       `json:"info"`
}

type openAPIInfo struct {
	Title string `json:"title"`
}

type openAPIComponents struct {
	Schemas map[string]*spec.Schema `json:"schemas"`
}

// findKindRef returns the component name whose x-kubernetes-group-version-kind lists gvk.
func findKindRef(schemas map[string]*spec.Schema, gvk schema.GroupVersionKind) (string, error) {
	for name, s := range schemas {
		var gvks []schema.GroupVersionKind
		if err := s.Extensions.GetObject(gvkExtension, &gvks); err != nil {
			continue
		}
		for _, g := range gvks {
			if g == gvk {
				return name, nil
			}
		}
	}
	return "", fmt.Errorf("the API server publishes no OpenAPI schema for %s: %w", gvk, resolver.ErrSchemaNotFound)
}
