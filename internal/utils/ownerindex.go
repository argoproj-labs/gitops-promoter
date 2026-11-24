package utils

import (
	"context"
	"fmt"
	"reflect"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ControllerOwnerUIDField is the cache field index that maps an object to the UID of its controller
// owner reference (the one with controller: true).
//
// Listing by this index returns only the objects one parent controls, instead of every object of the
// kind in the namespace. Without it, a label-selector List on the informer cache visits every object in
// the namespace, so per-parent cleanup costs grow with the total object count.
const ControllerOwnerUIDField = ".metadata.ownerReferences.controllerUID"

// ControllerOwnerUIDIndexValues returns the UID of obj's controller owner reference for
// ControllerOwnerUIDField, or nil when obj has no controller owner.
func ControllerOwnerUIDIndexValues(obj client.Object) []string {
	if ref := metav1.GetControllerOf(obj); ref != nil && ref.UID != "" {
		return []string{string(ref.UID)}
	}
	return nil
}

type ownerIndexKey struct {
	indexer client.FieldIndexer
	objType reflect.Type
}

var (
	ownerIndexMu         sync.Mutex
	ownerIndexRegistered = map[ownerIndexKey]struct{}{}
)

// RegisterControllerOwnerUIDFieldIndex registers ControllerOwnerUIDField for obj's type on indexer.
//
// Several controllers list by this index, and a manager may run only some of them, so each registers
// it from its own SetupWithManager. The informer rejects a second indexer with the same name, so calls
// after the first for the same indexer and type are no-ops.
func RegisterControllerOwnerUIDFieldIndex(ctx context.Context, indexer client.FieldIndexer, obj client.Object) error {
	objType := reflect.TypeOf(obj)
	if !reflect.TypeOf(indexer).Comparable() {
		// Cannot remember this indexer; register directly. Only reachable with an unusual indexer type.
		return indexField(ctx, indexer, obj, objType)
	}

	key := ownerIndexKey{indexer: indexer, objType: objType}
	ownerIndexMu.Lock()
	defer ownerIndexMu.Unlock()
	if _, ok := ownerIndexRegistered[key]; ok {
		return nil
	}
	if err := indexField(ctx, indexer, obj, objType); err != nil {
		return err
	}
	ownerIndexRegistered[key] = struct{}{}
	return nil
}

func indexField(ctx context.Context, indexer client.FieldIndexer, obj client.Object, objType reflect.Type) error {
	if err := indexer.IndexField(ctx, obj, ControllerOwnerUIDField, ControllerOwnerUIDIndexValues); err != nil {
		return fmt.Errorf("failed to set field index %s for %s: %w", ControllerOwnerUIDField, objType, err)
	}
	return nil
}
