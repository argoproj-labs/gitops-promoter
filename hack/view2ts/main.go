// Command view2ts extracts an OpenAPI 3.0 schema bundle for UI TypeScript codegen.
// It walks the openapi-gen definitions rooted at PromotionStrategyDetails and
// PromotionStrategyDetailsList (embedded types such as PromotionStrategy are included
// transitively) and emits a minimal components.schemas document for openapi-typescript.
//
// Requires committed api/view/v1alpha1/zz_generated.openapi.go (make generate-apiserver).
//
// Usage (from repo root):
//
//	go run ./hack/view2ts -o hack/view2ts/dist/view.openapi.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	viewv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/view/v1alpha1"
	"k8s.io/kube-openapi/pkg/common"
	"k8s.io/kube-openapi/pkg/validation/spec"
)

var rootModels = []string{
	"io.argoproj.promoter.view.v1alpha1.PromotionStrategyDetails",
	"io.argoproj.promoter.view.v1alpha1.PromotionStrategyDetailsList",
}

func main() {
	out := flag.String("o", "hack/view2ts/dist/view.openapi.json", "output OpenAPI 3.0 JSON file")
	flag.Parse()

	all := viewv1alpha1.GetOpenAPIDefinitions(spec.MustCreateRef)

	shortNames := buildShortNames(all)
	closure, err := collectClosure(all, rootModels)
	if err != nil {
		fmt.Fprintf(os.Stderr, "collect closure: %v\n", err)
		os.Exit(1)
	}

	schemas := make(map[string]any, len(closure))
	for fullName := range closure {
		short := shortNames[fullName]
		def, ok := all[fullName]
		if !ok {
			fmt.Fprintf(os.Stderr, "missing definition %q\n", fullName)
			os.Exit(1)
		}
		raw, err := json.Marshal(def.Schema)
		if err != nil {
			fmt.Fprintf(os.Stderr, "marshal %q: %v\n", fullName, err)
			os.Exit(1)
		}
		var schema map[string]any
		if err := json.Unmarshal(raw, &schema); err != nil {
			fmt.Fprintf(os.Stderr, "unmarshal %q: %v\n", fullName, err)
			os.Exit(1)
		}
		if schema == nil {
			schema = map[string]any{}
		}
		sanitizeSchema(schema)
		rewriteRefs(schema, shortNames)
		if short == "PromotionStrategyDetails" || short == "PromotionStrategy" {
			postProcessRootSchema(schema)
		}
		schemas[short] = schema
	}

	doc := map[string]any{
		"openapi": "3.0.0",
		"info": map[string]any{
			"title":   "view.promoter.argoproj.io",
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

func buildShortNames(all map[string]common.OpenAPIDefinition) map[string]string {
	byShort := make(map[string][]string)
	for full := range all {
		short := shortName(full)
		byShort[short] = append(byShort[short], full)
	}
	out := make(map[string]string, len(all))
	for short, fulls := range byShort {
		slices.Sort(fulls)
		if len(fulls) == 1 {
			out[fulls[0]] = short
			continue
		}
		for _, full := range fulls {
			out[full] = strings.ReplaceAll(full, ".", "_")
		}
	}
	return out
}

func shortName(full string) string {
	if i := strings.LastIndex(full, "."); i >= 0 {
		return full[i+1:]
	}
	return full
}

func collectClosure(all map[string]common.OpenAPIDefinition, roots []string) (map[string]struct{}, error) {
	closure := make(map[string]struct{})
	var walk func(full string) error
	walk = func(full string) error {
		if _, ok := closure[full]; ok {
			return nil
		}
		def, ok := all[full]
		if !ok {
			return nil
		}
		closure[full] = struct{}{}
		raw, err := json.Marshal(def.Schema)
		if err != nil {
			return fmt.Errorf("marshal schema %q: %w", full, err)
		}
		var node any
		if err := json.Unmarshal(raw, &node); err != nil {
			return fmt.Errorf("unmarshal schema %q: %w", full, err)
		}
		for _, ref := range findRefs(node) {
			if err := walk(ref); err != nil {
				return err
			}
		}
		return nil
	}
	for _, root := range roots {
		if err := walk(root); err != nil {
			return nil, err
		}
	}
	return closure, nil
}

func findRefs(v any) []string {
	var refs []string
	var visit func(any)
	visit = func(node any) {
		switch n := node.(type) {
		case map[string]any:
			if ref, ok := n["$ref"].(string); ok {
				if full := refToModelName(ref); full != "" {
					refs = append(refs, full)
				}
			}
			for _, child := range n {
				visit(child)
			}
		case []any:
			for _, item := range n {
				visit(item)
			}
		default:
		}
	}
	visit(v)
	return refs
}

func refToModelName(ref string) string {
	ref = strings.TrimPrefix(ref, "#/definitions/")
	ref = strings.TrimPrefix(ref, "#/components/schemas/")
	return ref
}

func rewriteRefs(v any, shortNames map[string]string) {
	switch node := v.(type) {
	case map[string]any:
		if node == nil {
			return
		}
		if ref, ok := node["$ref"].(string); ok {
			if full := refToModelName(ref); full != "" {
				if short, ok := shortNames[full]; ok {
					node["$ref"] = "#/components/schemas/" + short
				}
			}
		}
		for _, child := range node {
			rewriteRefs(child, shortNames)
		}
	case []any:
		for _, item := range node {
			rewriteRefs(item, shortNames)
		}
	default:
	}
}

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
	default:
	}
}

func postProcessRootSchema(schema map[string]any) {
	if schema == nil {
		return
	}
	rootRequired := []string{"apiVersion", "kind", "metadata"}
	existing, _ := schema["required"].([]any)
	seen := make(map[string]struct{})
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
	slices.Sort(required)
	out := make([]any, len(required))
	for i, name := range required {
		out[i] = name
	}
	schema["required"] = out
}

func writeJSON(path string, doc map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal JSON: %w", err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}
