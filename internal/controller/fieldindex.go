/*
Copyright 2024.

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

package controller

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// PromotionStrategyRefField is the cache field index path for gate CRDs that
// reference a PromotionStrategy via spec.promotionStrategyRef.name.
const PromotionStrategyRefField = ".spec.promotionStrategyRef.name"

var (
	gateCommitStatusKindsOnce sync.Once
	gateCommitStatusKinds     []client.Object
)

// GateCommitStatusKinds returns the commit-status gate CRD kinds that reference a
// PromotionStrategy via spec.promotionStrategyRef.name.
//
// Kinds are discovered from the promoter scheme (any non-List type whose Spec has
// a PromotionStrategyRef field), so adding a new gate CRD automatically includes
// it here once it is registered with SchemeBuilder. The view aggregation layer
// still needs a []T field and a buildBundle list; see the view added tests in
// internal/apiserver/view_added_test.go.
func GateCommitStatusKinds() []client.Object {
	gateCommitStatusKindsOnce.Do(func() {
		scheme := runtime.NewScheme()
		utilruntime.Must(promoterv1alpha1.AddToScheme(scheme))
		gateCommitStatusKinds = discoverPromotionStrategyRefGateKinds(scheme)
	})
	// Return copies of the prototype objects so callers cannot mutate the cache.
	out := make([]client.Object, len(gateCommitStatusKinds))
	for i, obj := range gateCommitStatusKinds {
		out[i] = obj.DeepCopyObject().(client.Object)
	}
	return out
}

// discoverPromotionStrategyRefGateKinds returns one empty instance per promoter
// kind whose Spec embeds PromotionStrategyRef (excluding *List types).
func discoverPromotionStrategyRefGateKinds(scheme *runtime.Scheme) []client.Object {
	known := scheme.KnownTypes(promoterv1alpha1.SchemeGroupVersion)
	var kindNames []string
	for kind, t := range known {
		if strings.HasSuffix(kind, "List") {
			continue
		}
		if !typeHasPromotionStrategyRef(t) {
			continue
		}
		kindNames = append(kindNames, kind)
	}
	sort.Strings(kindNames)

	out := make([]client.Object, 0, len(kindNames))
	for _, kind := range kindNames {
		obj, ok := reflect.New(known[kind]).Interface().(client.Object)
		if !ok {
			panic(fmt.Sprintf("promoter kind %s is registered but does not implement client.Object", kind))
		}
		out = append(out, obj)
	}
	return out
}

// typeHasPromotionStrategyRef reports whether t (a non-pointer struct type) has
// Spec.PromotionStrategyRef.
func typeHasPromotionStrategyRef(t reflect.Type) bool {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	spec, ok := t.FieldByName("Spec")
	if !ok {
		return false
	}
	specType := spec.Type
	if specType.Kind() == reflect.Ptr {
		specType = specType.Elem()
	}
	if specType.Kind() != reflect.Struct {
		return false
	}
	_, ok = specType.FieldByName("PromotionStrategyRef")
	return ok
}

// PromotionStrategyRefName returns spec.promotionStrategyRef.name for gate CRDs,
// or "" when the object is not a PromotionStrategyRef gate.
func PromotionStrategyRefName(obj client.Object) string {
	if obj == nil {
		return ""
	}
	v := reflect.ValueOf(obj)
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if !typeHasPromotionStrategyRef(v.Type()) {
		return ""
	}
	name := v.FieldByName("Spec").FieldByName("PromotionStrategyRef").FieldByName("Name")
	if !name.IsValid() || name.Kind() != reflect.String {
		return ""
	}
	return name.String()
}

// PromotionStrategyRefIndexValues returns the PromotionStrategy name referenced
// by a gate CRD for field-indexed list/watch queries.
func PromotionStrategyRefIndexValues(rawObj client.Object) []string {
	if name := PromotionStrategyRefName(rawObj); name != "" {
		return []string{name}
	}
	return nil
}

// RegisterGatePromotionStrategyRefFieldIndexes registers PromotionStrategyRefField
// on all commit-status gate CRD kinds.
func RegisterGatePromotionStrategyRefFieldIndexes(ctx context.Context, indexer client.FieldIndexer) error {
	for _, obj := range GateCommitStatusKinds() {
		if err := indexer.IndexField(ctx, obj, PromotionStrategyRefField, PromotionStrategyRefIndexValues); err != nil {
			return fmt.Errorf("failed to set field index %s for %T: %w", PromotionStrategyRefField, obj, err)
		}
	}
	return nil
}
