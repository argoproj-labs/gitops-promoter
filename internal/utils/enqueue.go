package utils

import (
	"context"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// EnqueueCommitStatusGatesForPromotionStrategy lists all gate resources of type T (via list L) in the
// PromotionStrategy's namespace and returns reconcile.Request values for those whose
// PromotionStrategyRef.Name matches the given PromotionStrategy.
//
// refName must return the PromotionStrategyRef.Name for a given gate object.
// This helper is intended for use in EnqueueRequestsFromMapFunc handlers for Git, WebRequest,
// and Timed commit-status gate controllers.
func EnqueueCommitStatusGatesForPromotionStrategy[T client.Object, L client.ObjectList](
	ctx context.Context,
	c client.Client,
	ps *promoterv1alpha1.PromotionStrategy,
	list L,
	refName func(T) string,
) []reconcile.Request {
	if err := c.List(ctx, list, client.InNamespace(ps.Namespace)); err != nil {
		log.FromContext(ctx).Error(err, "failed to list resources for PromotionStrategy watch")
		return nil
	}

	rawItems, err := apimeta.ExtractList(list)
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to extract list items")
		return nil
	}

	var requests []reconcile.Request
	for _, raw := range rawItems {
		gate, ok := raw.(T)
		if !ok {
			continue
		}
		if refName(gate) == ps.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(gate),
			})
		}
	}
	return requests
}
