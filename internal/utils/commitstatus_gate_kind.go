package utils

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
)

// commitStatusGateKind returns the API Kind of a *CommitStatus gate parent.
// Uses TypeMeta.Kind when set; otherwise resolves from GetScheme via apiutil.GVKForObject.
// Panics when Kind cannot be resolved (unregistered type, ambiguous GVK, etc.).
func commitStatusGateKind(parent client.Object) string {
	kind := parent.GetObjectKind().GroupVersionKind().Kind
	if kind != "" {
		return kind
	}
	gvk, err := apiutil.GVKForObject(parent, GetScheme())
	if err != nil {
		panic(fmt.Sprintf("commitStatusGateKind: resolve kind for %T: %v", parent, err))
	}
	return gvk.Kind
}
