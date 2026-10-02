package ordercommitstatusgate

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// orderingGateGVKs returns API versions to try for an out-of-tree ordering gate, preferred version first
// when a REST mapper is available.
func orderingGateGVKs(mapper meta.RESTMapper, gk schema.GroupKind) []schema.GroupVersionKind {
	if mapper != nil {
		mappings, err := mapper.RESTMappings(gk)
		if err == nil && len(mappings) > 0 {
			gvks := make([]schema.GroupVersionKind, 0, len(mappings))
			for _, mapping := range mappings {
				gvks = append(gvks, mapping.GroupVersionKind)
			}
			return gvks
		}
	}

	// Unit tests and other environments without discovery fall back to common version names.
	versions := []string{"v1", "v1beta2", "v1beta1", "v1alpha2", "v1alpha1"}
	gvks := make([]schema.GroupVersionKind, len(versions))
	for i, version := range versions {
		gvks[i] = schema.GroupVersionKind{Group: gk.Group, Version: version, Kind: gk.Kind}
	}
	return gvks
}

func unsupportedOrderingGateResourceErr(gk schema.GroupKind, err error) error {
	return fmt.Errorf("no API resource found for ordering gate %s: %w", gk, err)
}
