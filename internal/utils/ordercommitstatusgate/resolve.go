package ordercommitstatusgate

import (
	"context"
	"fmt"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Resolve loads the ordering gate referenced by ps.spec.orderCommitStatusRef and returns its
// spec.key for injection onto ChangeTransferPolicies. The gate CR must expose spec.key and
// spec.promotionStrategyRef.name; API version is resolved from cluster discovery when a REST
// mapper is available.
//
// In-tree orderCommitStatus gates must use a typed Get so they hit the existing
// instance-id-partitioned informer instead of a second unstructured store.
// DependentsSuccessfulCommitStatus is the current in-tree ordering gate; add a typed
// branch here for any new in-tree kind that can be referenced from orderCommitStatusRef.
//
// Out-of-tree kinds are fetched as unstructured objects. The manager client caches those
// Gets (Cache.Unstructured) and partitions the informer with the same instance-id selector
// as promoter CRDs. The ServiceAccount needs get/list/watch on the gate CRD.
func Resolve(ctx context.Context, c client.Client, mapper meta.RESTMapper, ps *promoterv1alpha1.PromotionStrategy) (string, error) {
	ref := ps.Spec.OrderCommitStatusRef.WithDefaults()
	if ref.Name == "" {
		return "", fmt.Errorf("PromotionStrategy %q orderCommitStatusRef.name is required", ps.Name)
	}
	return resolve(ctx, c, mapper, ps, ref)
}

func resolve(
	ctx context.Context,
	c client.Client,
	mapper meta.RESTMapper,
	ps *promoterv1alpha1.PromotionStrategy,
	ref promoterv1alpha1.OrderCommitStatusRef,
) (string, error) {
	// In-tree orderCommitStatus gates use a typed Get (existing partitioned informer).
	// Add a branch here for any new in-tree kind; do not send them through unstructured.
	if ref.Group == promoterv1alpha1.DefaultOrderCommitStatusGroup &&
		ref.Kind == promoterv1alpha1.DefaultOrderCommitStatusKind {
		return resolveDependentsSuccessful(ctx, c, ps, ref)
	}
	return resolveUnstructured(ctx, c, mapper, ps, ref)
}

func resolveDependentsSuccessful(
	ctx context.Context,
	c client.Client,
	ps *promoterv1alpha1.PromotionStrategy,
	ref promoterv1alpha1.OrderCommitStatusRef,
) (string, error) {
	var gate promoterv1alpha1.DependentsSuccessfulCommitStatus
	getErr := c.Get(ctx, client.ObjectKey{Namespace: ps.Namespace, Name: ref.Name}, &gate)
	if getErr != nil {
		if k8serrors.IsNotFound(getErr) {
			return "", fmt.Errorf("PromotionStrategy %q references %s/%s %q via orderCommitStatusRef, but it was not found",
				ps.Name, ref.Group, ref.Kind, ref.Name)
		}
		return "", fmt.Errorf("failed to get %s/%s %q for PromotionStrategy %q: %w",
			ref.Group, ref.Kind, ref.Name, ps.Name, getErr)
	}
	return orderingGateKey(ps, ref, gate.Spec.Key, gate.Spec.PromotionStrategyRef.Name)
}

func resolveUnstructured(
	ctx context.Context,
	c client.Client,
	mapper meta.RESTMapper,
	ps *promoterv1alpha1.PromotionStrategy,
	ref promoterv1alpha1.OrderCommitStatusRef,
) (string, error) {
	gvks := orderingGateGVKs(mapper, ref.GroupKind())

	var lastNotFound error
	for _, gvk := range gvks {
		gate := &unstructured.Unstructured{}
		gate.SetGroupVersionKind(gvk)
		getErr := c.Get(ctx, client.ObjectKey{Namespace: ps.Namespace, Name: ref.Name}, gate)
		if getErr != nil {
			if k8serrors.IsNotFound(getErr) || meta.IsNoMatchError(getErr) {
				lastNotFound = getErr
				continue
			}
			return "", fmt.Errorf("failed to get %s %q for PromotionStrategy %q: %w", gvk, ref.Name, ps.Name, getErr)
		}
		key, found, err := unstructured.NestedString(gate.Object, "spec", "key")
		if err != nil {
			return "", fmt.Errorf("PromotionStrategy %q orderCommitStatusRef.name %q: failed to read spec.key: %w", ps.Name, ref.Name, err)
		}
		if !found {
			key = ""
		}
		psRef, found, err := unstructured.NestedString(gate.Object, "spec", "promotionStrategyRef", "name")
		if err != nil {
			return "", fmt.Errorf("PromotionStrategy %q orderCommitStatusRef.name %q: failed to read spec.promotionStrategyRef.name: %w",
				ps.Name, ref.Name, err)
		}
		if !found {
			psRef = ""
		}
		return orderingGateKey(ps, ref, key, psRef)
	}

	if lastNotFound != nil {
		if k8serrors.IsNotFound(lastNotFound) {
			return "", fmt.Errorf("PromotionStrategy %q references %s/%s %q via orderCommitStatusRef, but it was not found",
				ps.Name, ref.Group, ref.Kind, ref.Name)
		}
		return "", unsupportedOrderingGateResourceErr(ref.GroupKind(), lastNotFound)
	}

	return "", fmt.Errorf("PromotionStrategy %q references %s/%s %q via orderCommitStatusRef, but it was not found",
		ps.Name, ref.Group, ref.Kind, ref.Name)
}

func orderingGateKey(
	ps *promoterv1alpha1.PromotionStrategy,
	ref promoterv1alpha1.OrderCommitStatusRef,
	key string,
	psRef string,
) (string, error) {
	if key == "" {
		return "", fmt.Errorf("PromotionStrategy %q orderCommitStatusRef.name %q points to %s/%s with an empty spec.key",
			ps.Name, ref.Name, ref.Group, ref.Kind)
	}
	if psRef != ps.Name {
		return "", fmt.Errorf("PromotionStrategy %q orderCommitStatusRef.name %q points to %s/%s whose promotionStrategyRef.name is %q",
			ps.Name, ref.Name, ref.Group, ref.Kind, psRef)
	}
	return key, nil
}
