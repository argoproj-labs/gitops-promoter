package controller

import (
	"context"
	"encoding/json"
	"fmt"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// createWithPublishTo creates obj with spec.publishTo set to publishTo.
// An empty publishTo is sent as [] so the API server does not apply the [scm] default.
func createWithPublishTo(ctx context.Context, obj client.Object, publishTo []any) error {
	gvk, err := apiutil.GVKForObject(obj, k8sClient.Scheme())
	if err != nil {
		return fmt.Errorf("resolve GVK: %w", err)
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("marshal object: %w", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return fmt.Errorf("unmarshal object: %w", err)
	}
	manifest["apiVersion"] = gvk.GroupVersion().String()
	manifest["kind"] = gvk.Kind
	spec, ok := manifest["spec"].(map[string]any)
	if !ok {
		return fmt.Errorf("object spec is missing")
	}
	spec["publishTo"] = publishTo
	return k8sClient.Create(ctx, &unstructured.Unstructured{Object: manifest})
}
