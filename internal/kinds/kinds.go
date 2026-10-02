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

// Package kinds discovers promoter.argoproj.io root API types from a runtime.Scheme.
// Gate-manager discovery (Spec.PromotionStrategyRef) lives in
// internal/controller.GateCommitStatusKinds — keep that as the source of truth for
// view/field-index wiring.
package kinds

import (
	"cmp"
	"reflect"
	"slices"
	"strings"
	"sync"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ControllerConfigurationKind is the Kind for ControllerConfiguration.
const ControllerConfigurationKind = "ControllerConfiguration"

// stableObjects caches empty client.Object instances by Go pointer type so
// callers (especially cache.ByObject) get pointer-identical keys across calls.
var stableObjects sync.Map // reflect.Type -> client.Object

// All returns a stable empty instance of every promoter.argoproj.io/v1alpha1 root
// kind registered on scheme (List types excluded). Order is by Kind name.
func All(scheme *runtime.Scheme) []client.Object {
	seen := map[reflect.Type]struct{}{}
	var out []client.Object

	for gvk, t := range scheme.AllKnownTypes() {
		if gvk.Group != promoterv1alpha1.GroupVersion.Group || gvk.Version != promoterv1alpha1.GroupVersion.Version {
			continue
		}
		if strings.HasSuffix(gvk.Kind, "List") {
			continue
		}

		obj, ok := stableObject(t)
		if !ok {
			continue
		}
		ptrType := reflect.TypeOf(obj)
		if _, ok := seen[ptrType]; ok {
			continue
		}
		seen[ptrType] = struct{}{}
		out = append(out, obj)
	}

	slices.SortFunc(out, func(a, b client.Object) int {
		return cmp.Compare(Kind(scheme, a), Kind(scheme, b))
	})
	return out
}

// Kind returns the Kubernetes Kind for obj using scheme, falling back to the Go type name.
func Kind(scheme *runtime.Scheme, obj client.Object) string {
	gvks, _, err := scheme.ObjectKinds(obj)
	if err != nil || len(gvks) == 0 {
		t := reflect.TypeOf(obj)
		if t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		return t.Name()
	}
	return gvks[0].Kind
}

func stableObject(t reflect.Type) (client.Object, bool) {
	if t.Kind() != reflect.Pointer {
		t = reflect.PointerTo(t)
	}
	if v, ok := stableObjects.Load(t); ok {
		obj, ok := v.(client.Object)
		return obj, ok
	}
	obj, ok := reflect.New(t.Elem()).Interface().(client.Object)
	if !ok {
		return nil, false
	}
	actual, _ := stableObjects.LoadOrStore(t, obj)
	stored, ok := actual.(client.Object)
	return stored, ok
}
