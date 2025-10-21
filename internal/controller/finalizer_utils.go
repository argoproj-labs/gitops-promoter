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

	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
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
