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

	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// EnqueueCommitStatusGatesForPromotionStrategy lists gate resources in the
// PromotionStrategy namespace that reference ps via spec.promotionStrategyRef.name
// and returns reconcile requests for each match.
func EnqueueCommitStatusGatesForPromotionStrategy[
	GateList any,
	GateListPtr interface {
		*GateList
		client.ObjectList
	},
](
	ctx context.Context,
	c client.Client,
	ps *promoterv1alpha1.PromotionStrategy,
) []reconcile.Request {
	list := GateListPtr(new(GateList))
	if err := c.List(ctx, list,
		client.InNamespace(ps.Namespace),
		client.MatchingFields{PromotionStrategyRefField: ps.Name},
	); err != nil {
		log.FromContext(ctx).Error(err, fmt.Sprintf("failed to list %T resources for PromotionStrategy watch", list))
		return nil
	}

	rawItems, err := meta.ExtractList(list)
	if err != nil {
		log.FromContext(ctx).Error(err, fmt.Sprintf("failed to extract list items from %T for PromotionStrategy watch", list))
		return nil
	}

	requests := make([]reconcile.Request, 0, len(rawItems))
	for _, raw := range rawItems {
		obj, ok := raw.(client.Object)
		if !ok {
			continue
		}
		requests = append(requests, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(obj),
		})
	}

	return requests
}

// CommitStatusGatePromotionStrategyWatchHandler returns a watch handler that enqueues
// every gate of type GateList in the PromotionStrategy namespace that references that strategy.
func CommitStatusGatePromotionStrategyWatchHandler[
	GateList any,
	GateListPtr interface {
		*GateList
		client.ObjectList
	},
](c client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
		if !ok {
			return nil
		}
		return EnqueueCommitStatusGatesForPromotionStrategy[GateList, GateListPtr](ctx, c, ps)
	})
}
