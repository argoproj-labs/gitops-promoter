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
	"errors"
	"fmt"
	"sort"

	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
)

// ensureSecretFinalizerForProvider adds a finalizer to a Secret referenced by an ScmProvider or ClusterScmProvider.
// This prevents the Secret from being deleted while the provider still references it.
func ensureSecretFinalizerForProvider(
	ctx context.Context,
	c client.Client,
	secretNamespace string,
	secretName string,
	finalizer string,
) error {
	logger := log.FromContext(ctx)

	secretKey := types.NamespacedName{
		Namespace: secretNamespace,
		Name:      secretName,
	}

	var secret v1.Secret
	if err := c.Get(ctx, secretKey, &secret); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("Secret not found, skipping finalizer", "secret", secretKey)
			return nil
		}
		return fmt.Errorf("failed to get Secret: %w", err)
	}

	// Don't add finalizer to a Secret that's already being deleted
	if !secret.DeletionTimestamp.IsZero() {
		logger.Info("Secret is being deleted, skipping finalizer", "secret", secretKey)
		return nil
	}

	if controllerutil.ContainsFinalizer(&secret, finalizer) {
		return nil
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error { //nolint:wrapcheck
		if err := c.Get(ctx, secretKey, &secret); err != nil {
			return err //nolint:wrapcheck
		}
		// Check again after getting the secret in case it was deleted during the retry
		if !secret.DeletionTimestamp.IsZero() {
			return nil
		}
		if controllerutil.AddFinalizer(&secret, finalizer) {
			return c.Update(ctx, &secret)
		}
		return nil
	})
}

// removeSecretFinalizerForProvider removes a finalizer from a Secret if no other providers are using it.
// checkOtherProviders is a function that returns true if other providers still reference this secret.
func removeSecretFinalizerForProvider(
	ctx context.Context,
	c client.Client,
	secretNamespace string,
	secretName string,
	finalizer string,
	checkOtherProviders func() (bool, error),
) error {
	logger := log.FromContext(ctx)

	secretKey := types.NamespacedName{
		Namespace: secretNamespace,
		Name:      secretName,
	}

	var secret v1.Secret
	if err := c.Get(ctx, secretKey, &secret); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("Secret not found, skipping finalizer removal", "secret", secretKey)
			return nil
		}
		return fmt.Errorf("failed to get Secret: %w", err)
	}

	if !controllerutil.ContainsFinalizer(&secret, finalizer) {
		return nil
	}

	// Check if there are other providers using this Secret
	stillUsed, err := checkOtherProviders()
	if err != nil {
		return fmt.Errorf("failed to check for other providers: %w", err)
	}

	if stillUsed {
		logger.Info("Secret still referenced by other provider, keeping finalizer",
			"secret", secretKey)
		return nil
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error { //nolint:wrapcheck
		if err := c.Get(ctx, secretKey, &secret); err != nil {
			return err //nolint:wrapcheck
		}
		if controllerutil.RemoveFinalizer(&secret, finalizer) {
			return c.Update(ctx, &secret)
		}
		return nil
	})
}

// handleResourceFinalizerWithDependencies handles the common finalizer add/remove logic for resources with dependencies.
// This function:
// - Adds finalizer on resource creation
// - Checks for dependent resources before allowing deletion
// - Emits events and metrics when deletion is blocked
// - Removes finalizer when no dependencies exist
func handleResourceFinalizerWithDependencies(
	ctx context.Context,
	c client.Client,
	recorder events.EventRecorder,
	obj client.Object,
	finalizer string,
	resourceType string,
	checkDependencies func() ([]string, error),
) (deleted bool, err error) {
	logger := log.FromContext(ctx)

	// If not being deleted, ensure finalizer exists
	if obj.GetDeletionTimestamp().IsZero() {
		if controllerutil.ContainsFinalizer(obj, finalizer) {
			return false, nil
		}

		// Add finalizer with retry logic
		return false, retry.RetryOnConflict(retry.DefaultRetry, func() error { //nolint:wrapcheck
			if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
				return err //nolint:wrapcheck
			}
			if controllerutil.AddFinalizer(obj, finalizer) {
				return c.Update(ctx, obj)
			}
			return nil
		})
	}

	// If we're here, the object is being deleted
	if !controllerutil.ContainsFinalizer(obj, finalizer) {
		return false, nil
	}

	// Check for dependent resources
	dependents, err := checkDependencies()
	if err != nil {
		return false, fmt.Errorf("failed to check for dependent resources: %w", err)
	}

	if len(dependents) > 0 {
		// Sort for deterministic error messages
		sort.Strings(dependents)

		// Format error message
		errMsg := formatDependentResourceError(obj, resourceType, dependents)

		// Emit Kubernetes event for better UX
		recorder.Eventf(obj, nil, "Warning", "DeletionBlocked", "Deletion", errMsg)

		// Update gauge metric with current count of dependents
		metrics.FinalizerDependentCount.WithLabelValues(
			resourceType,
			obj.GetName(),
			obj.GetNamespace(),
		).Set(float64(len(dependents)))

		logger.Info(resourceType+" still has dependent resources, cannot delete",
			"resource", obj.GetName(),
			"namespace", obj.GetNamespace(),
			"count", len(dependents),
			"first", dependents[0])

		return true, errors.New(errMsg)
	}

	// No dependencies, remove finalizer with retry logic
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return err //nolint:wrapcheck
		}
		if controllerutil.RemoveFinalizer(obj, finalizer) {
			return c.Update(ctx, obj)
		}
		return nil
	})
	if err != nil {
		return true, fmt.Errorf("failed to remove finalizer: %w", err)
	}

	// Clear metrics when finalizer is removed
	metrics.FinalizerDependentCount.DeleteLabelValues(
		resourceType,
		obj.GetName(),
		obj.GetNamespace(),
	)

	return true, nil
}

// formatDependentResourceError creates a user-friendly error message for blocked deletions
func formatDependentResourceError(obj client.Object, resourceType string, dependents []string) string {
	resourceName := obj.GetName()
	namespace := obj.GetNamespace()

	// Format resource identifier
	var resourceID string
	if namespace != "" {
		resourceID = fmt.Sprintf("%s %s/%s", resourceType, namespace, resourceName)
	} else {
		// Cluster-scoped resource
		resourceID = fmt.Sprintf("%s %s", resourceType, resourceName)
	}

	// Format dependent resources
	firstDependent := dependents[0]
	if len(dependents) == 1 {
		return fmt.Sprintf("%s still has dependent resource: %s", resourceID, firstDependent)
	}

	return fmt.Sprintf("%s still has dependent resource %s and %d more", resourceID, firstDependent, len(dependents)-1)
}
