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
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// touchChangeTransferPolicies adds or updates the ReconcileAtAnnotation on the ChangeTransferPolicies
// for the environments that had validations/time gates transition to success.
// This is a shared utility function used by commit status controllers.
func touchChangeTransferPolicies(ctx context.Context, k8sClient client.Client, ps *promoterv1alpha1.PromotionStrategy, transitionedEnvironments []string, reason string) error {
	logger := log.FromContext(ctx)

	for _, envBranch := range transitionedEnvironments {
		// Generate the ChangeTransferPolicy name using the same logic as the PromotionStrategy controller
		ctpName := utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(ps.Name, envBranch))

		// Fetch the ChangeTransferPolicy
		var ctp promoterv1alpha1.ChangeTransferPolicy
		err := k8sClient.Get(ctx, client.ObjectKey{Namespace: ps.Namespace, Name: ctpName}, &ctp)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				logger.Info("ChangeTransferPolicy not found for environment, skipping touch",
					"branch", envBranch,
					"ctpName", ctpName)
				continue
			}
			return fmt.Errorf("failed to get ChangeTransferPolicy %q for environment %q: %w", ctpName, envBranch, err)
		}

		// Update the annotation to trigger reconciliation
		ctpUpdated := ctp.DeepCopy()
		if ctpUpdated.Annotations == nil {
			ctpUpdated.Annotations = make(map[string]string)
		}
		ctpUpdated.Annotations[promoterv1alpha1.ReconcileAtAnnotation] = time.Now().Format(time.RFC3339Nano)

		err = k8sClient.Patch(ctx, ctpUpdated, client.MergeFrom(&ctp))
		if err != nil {
			return fmt.Errorf("failed to update ChangeTransferPolicy %q annotation for environment %q: %w", ctpName, envBranch, err)
		}

		logger.Info("Triggered ChangeTransferPolicy reconciliation",
			"changeTransferPolicy", ctpName,
			"branch", envBranch,
			"reason", reason)
	}

	return nil
}
