// Command crd2ts extracts OpenAPI 3.0 component schemas from Kubernetes CRD YAML
// files for TypeScript codegen via openapi-typescript.
//
// Usage (from hack/crd2ts):
//
//	go run . -crds ../../config/crd/bases -o dist/crds.openapi.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"sigs.k8s.io/yaml"
)

func main() {
	crdsDir := flag.String("crds", "config/crd/bases", "directory containing CRD YAML files")
	out := flag.String("o", "dist/crds.openapi.json", "output OpenAPI 3.0 JSON file")
	flag.Parse()

	files, err := filepath.Glob(filepath.Join(*crdsDir, "promoter.argoproj.io_*.yaml"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "glob: %v\n", err)
		os.Exit(1)
	}
	sort.Strings(files)
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "no CRD files found in %s\n", *crdsDir)
		os.Exit(1)
	}

	schemas := make(map[string]any, len(files))
	for _, f := range files {
		kind, schema, err := extractCRDSchema(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", f, err)
			os.Exit(1)
		}
		if _, exists := schemas[kind]; exists {
			fmt.Fprintf(os.Stderr, "duplicate kind %q from %s\n", kind, f)
			os.Exit(1)
		}
		schemas[kind] = schema
	}

	doc := map[string]any{
		"openapi": "3.0.0",
		"info": map[string]any{
			"title":   "promoter.argoproj.io",
			"version": "v1alpha1",
		},
		"components": map[string]any{
			"schemas": schemas,
		},
	}

	if err := writeJSON(*out, doc); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "wrote %s (%d schemas)\n", *out, len(schemas))
}

func extractCRDSchema(path string) (kind string, schema map[string]any, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	var crd map[string]any
	if err := yaml.Unmarshal(b, &crd); err != nil {
		return "", nil, fmt.Errorf("unmarshal: %w", err)
	}
	if crd["kind"] != "CustomResourceDefinition" {
		return "", nil, fmt.Errorf("not a CustomResourceDefinition")
	}

	spec, ok := crd["spec"].(map[string]any)
	if !ok {
		return "", nil, fmt.Errorf("missing spec")
	}
	names, ok := spec["names"].(map[string]any)
	if !ok {
		return "", nil, fmt.Errorf("missing spec.names")
	}
	kind, ok = names["kind"].(string)
	if !ok || kind == "" {
		return "", nil, fmt.Errorf("missing spec.names.kind")
	}

	openAPISchema, err := storageVersionSchema(spec)
	if err != nil {
		return "", nil, err
	}
	schema, ok = openAPISchema.(map[string]any)
	if !ok {
		return "", nil, fmt.Errorf("openAPIV3Schema is not an object")
	}
	sanitizeSchema(schema)
	postProcessRootSchema(schema)
	return kind, schema, nil
}

// postProcessRootSchema marks standard Kubernetes resource identity fields as required
// in OpenAPI so generated TypeScript treats them as non-optional (apiVersion, kind, metadata).
// Other fields (e.g. spec) come from controller-gen required lists when Go omitempty is omitted.
func postProcessRootSchema(schema map[string]any) {
	rootRequired := []string{"apiVersion", "kind", "metadata"}
	existing, _ := schema["required"].([]any)
	seen := make(map[string]struct{}, len(existing)+len(rootRequired))
	for _, item := range existing {
		if s, ok := item.(string); ok {
			seen[s] = struct{}{}
		}
	}
	for _, name := range rootRequired {
		seen[name] = struct{}{}
	}
	required := make([]string, 0, len(seen))
	for name := range seen {
		required = append(required, name)
	}
	sort.Strings(required)
	out := make([]any, len(required))
	for i, name := range required {
		out[i] = name
	}
	schema["required"] = out
}

func storageVersionSchema(spec map[string]any) (any, error) {
	versions, ok := spec["versions"].([]any)
	if !ok || len(versions) == 0 {
		return nil, fmt.Errorf("missing spec.versions")
	}

	var fallback map[string]any
	for _, item := range versions {
		v, ok := item.(map[string]any)
		if !ok {
			continue
		}
		schemaObj, ok := v["schema"].(map[string]any)
		if !ok {
			continue
		}
		openAPI, ok := schemaObj["openAPIV3Schema"]
		if !ok {
			continue
		}
		if fallback == nil {
			if m, ok := openAPI.(map[string]any); ok {
				fallback = m
			}
		}
		if storage, _ := v["storage"].(bool); storage {
			return openAPI, nil
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, fmt.Errorf("no openAPIV3Schema found")
}

// sanitizeSchema removes Kubernetes-only OpenAPI extensions that confuse TS generators.
func sanitizeSchema(v any) {
	switch node := v.(type) {
	case map[string]any:
		for k, child := range node {
			if strings.HasPrefix(k, "x-kubernetes-") {
				delete(node, k)
				continue
			}
			sanitizeSchema(child)
		}
	case []any:
		for _, item := range node {
			sanitizeSchema(item)
		}
	}
}

func writeJSON(path string, doc map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}
