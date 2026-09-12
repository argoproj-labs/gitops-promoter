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
		return readOrderingGateFields(ps, ref, gate)
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

func readOrderingGateFields(
	ps *promoterv1alpha1.PromotionStrategy,
	ref promoterv1alpha1.OrderCommitStatusRef,
	gate *unstructured.Unstructured,
) (string, error) {
	key, found, err := unstructured.NestedString(gate.Object, "spec", "key")
	if err != nil {
		return "", fmt.Errorf("PromotionStrategy %q orderCommitStatusRef.name %q: failed to read spec.key: %w", ps.Name, ref.Name, err)
	}
	if !found || key == "" {
		return "", fmt.Errorf("PromotionStrategy %q orderCommitStatusRef.name %q points to %s/%s with an empty spec.key",
			ps.Name, ref.Name, ref.Group, ref.Kind)
	}

	psRef, found, err := unstructured.NestedString(gate.Object, "spec", "promotionStrategyRef", "name")
	if err != nil {
		return "", fmt.Errorf("PromotionStrategy %q orderCommitStatusRef.name %q: failed to read spec.promotionStrategyRef.name: %w",
			ps.Name, ref.Name, err)
	}
	if !found || psRef != ps.Name {
		return "", fmt.Errorf("PromotionStrategy %q orderCommitStatusRef.name %q points to %s/%s whose promotionStrategyRef.name is %q",
			ps.Name, ref.Name, ref.Group, ref.Kind, psRef)
	}
	return key, nil
}
