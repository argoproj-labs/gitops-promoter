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

package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// Field indexes on ChangeTransferPolicy used to find the policies that read a CommitStatus.
const (
	ctpProposedHydratedShaIndex = ".status.proposed.hydrated.sha"
	ctpActiveHydratedShaIndex   = ".status.active.hydrated.sha"
)

// commitStatusPhaseChangedPredicate passes the CommitStatus events that can change what a
// ChangeTransferPolicy decides: a new CommitStatus, a phase change in either direction, a move to
// another SHA, a change of the commit-status key label the policy selects by, and a delete.
//
// Description and URL edits are dropped. The policy copies them into its status, but those copies
// are informational and may lag until the policy's next reconcile. Phase changes away from success
// must not be dropped: DependentsSuccessfulCommitStatus reads upstream health from
// ChangeTransferPolicy status (via PromotionStrategy status), so a missed success-to-failure
// change would let a downstream environment promote past a degraded upstream.
//
// Creates from the informer's initial list are dropped. Every ChangeTransferPolicy is reconciled
// on controller start through its own informer and reads current CommitStatus state, so waking it
// again for each existing CommitStatus would only repeat its git work.
func commitStatusPhaseChangedPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return !e.IsInInitialList
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldCS, okOld := e.ObjectOld.(*promoterv1alpha1.CommitStatus)
			newCS, okNew := e.ObjectNew.(*promoterv1alpha1.CommitStatus)
			if !okOld || !okNew {
				return false
			}
			return oldCS.Spec.Phase != newCS.Spec.Phase ||
				oldCS.Spec.Sha != newCS.Spec.Sha ||
				oldCS.Labels[promoterv1alpha1.CommitStatusLabel] != newCS.Labels[promoterv1alpha1.CommitStatusLabel]
		},
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

// enqueueChangeTransferPoliciesForCommitStatus maps a CommitStatus event to every
// ChangeTransferPolicy whose active or proposed hydrated SHA is the CommitStatus's SHA. On an
// update that moves the CommitStatus to another SHA, policies on the previous SHA are enqueued too:
// for them the status has disappeared, which reads as pending.
//
// Policies are matched across namespaces, the same way setCommitStatusState lists CommitStatuses.
func enqueueChangeTransferPoliciesForCommitStatus(c client.Client) handler.Funcs {
	enqueue := func(ctx context.Context, q workqueue.TypedRateLimitingInterface[reconcile.Request], objs ...client.Object) {
		shas := make([]string, 0, len(objs))
		for _, obj := range objs {
			cs, ok := obj.(*promoterv1alpha1.CommitStatus)
			if !ok {
				continue
			}
			shas = append(shas, cs.Spec.Sha)
		}
		for _, key := range changeTransferPoliciesForShas(ctx, c, shas...) {
			q.Add(reconcile.Request{NamespacedName: key})
		}
	}
	return handler.Funcs{
		CreateFunc: func(ctx context.Context, e event.CreateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enqueue(ctx, q, e.Object)
		},
		UpdateFunc: func(ctx context.Context, e event.UpdateEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enqueue(ctx, q, e.ObjectOld, e.ObjectNew)
		},
		DeleteFunc: func(ctx context.Context, e event.DeleteEvent, q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			enqueue(ctx, q, e.Object)
		},
	}
}

// changeTransferPoliciesForShas returns the distinct ChangeTransferPolicies whose active or proposed
// hydrated SHA is one of shas.
//
// An empty SHA is never looked up: the hydrated-SHA indexes file every policy that has not recorded a
// hydrated SHA yet (new, or failing early) under "", across all namespaces, and enqueueing all of them
// for each new CommitStatus would cost each one a reconcile with git network calls.
//
// This runs on the CommitStatus informer's delivery goroutine. The reads skip read-your-writes
// consistency: ChangeTransferPolicy status is written continuously, and waiting for each of those
// writes to reach the cache would serialize CommitStatus event delivery behind them. The policy
// re-reads everything it needs when it reconciles.
func changeTransferPoliciesForShas(ctx context.Context, c client.Client, shas ...string) []types.NamespacedName {
	logger := log.FromContext(ctx)

	seen := make(map[types.NamespacedName]struct{})
	keys := make([]types.NamespacedName, 0, 2)
	for _, sha := range shas {
		if sha == "" {
			continue
		}
		for _, field := range []string{ctpActiveHydratedShaIndex, ctpProposedHydratedShaIndex} {
			var ctps promoterv1alpha1.ChangeTransferPolicyList
			if err := c.List(ctx, &ctps,
				client.MatchingFields{field: sha},
				client.UnsafeDisableDeepCopy,
				client.DisableReadYourWritesConsistency,
			); err != nil {
				logger.Error(err, "failed to list ChangeTransferPolicies for CommitStatus SHA", "field", field, "sha", sha)
				continue
			}
			for i := range ctps.Items {
				key := client.ObjectKeyFromObject(&ctps.Items[i])
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				keys = append(keys, key)
			}
		}
	}
	return keys
}
