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

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// PromotionStrategyRefField is the cache field index path for gate CRDs that
// reference a PromotionStrategy via spec.promotionStrategyRef.name.
const PromotionStrategyRefField = ".spec.promotionStrategyRef.name"

// PromotionStrategyRefIndexValues returns the PromotionStrategy name referenced
// by a gate CRD for field-indexed list/watch queries.
func PromotionStrategyRefIndexValues(rawObj client.Object) []string {
	switch o := rawObj.(type) {
	case *promoterv1alpha1.ArgoCDCommitStatus:
		return []string{o.Spec.PromotionStrategyRef.Name}
	case *promoterv1alpha1.GitCommitStatus:
		return []string{o.Spec.PromotionStrategyRef.Name}
	case *promoterv1alpha1.TimedCommitStatus:
		return []string{o.Spec.PromotionStrategyRef.Name}
	case *promoterv1alpha1.WebRequestCommitStatus:
		return []string{o.Spec.PromotionStrategyRef.Name}
	case *promoterv1alpha1.ScheduledCommitStatus:
		return []string{o.Spec.PromotionStrategyRef.Name}
	default:
		return nil
	}
}

// RegisterGatePromotionStrategyRefFieldIndexes registers PromotionStrategyRefField
// on all commit-status gate CRD kinds.
func RegisterGatePromotionStrategyRefFieldIndexes(ctx context.Context, indexer client.FieldIndexer) error {
	gateKinds := []client.Object{
		&promoterv1alpha1.ArgoCDCommitStatus{},
		&promoterv1alpha1.GitCommitStatus{},
		&promoterv1alpha1.TimedCommitStatus{},
		&promoterv1alpha1.WebRequestCommitStatus{},
		&promoterv1alpha1.ScheduledCommitStatus{},
	}
	for _, obj := range gateKinds {
		if err := indexer.IndexField(ctx, obj, PromotionStrategyRefField, PromotionStrategyRefIndexValues); err != nil {
			return fmt.Errorf("failed to set field index %s for %T: %w", PromotionStrategyRefField, obj, err)
		}
	}
	return nil
}
