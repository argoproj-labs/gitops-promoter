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
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/record"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// ScmProviderReconciler reconciles a ScmProvider object
type ScmProviderReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmproviders,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmproviders/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmproviders/finalizers,verbs=update
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ScmProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ScmProvider")
	startTime := time.Now()

	var scmProvider promoterv1alpha1.ScmProvider
	defer utils.HandleReconciliationResult(ctx, startTime, &scmProvider, r.Client, r.Recorder, &err)

	if err := r.Get(ctx, req.NamespacedName, &scmProvider); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("ScmProvider not found", "namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get ScmProvider: %w", err)
	}

	if deleted, err := r.handleFinalizer(ctx, &scmProvider); err != nil || deleted {
		return ctrl.Result{}, err
	}

	// Add finalizer to referenced Secret if it exists
	if err := r.ensureSecretFinalizer(ctx, &scmProvider); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure Secret finalizer: %w", err)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ScmProviderReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.ScmProvider{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

func (r *ScmProviderReconciler) handleFinalizer(ctx context.Context, scmProvider *promoterv1alpha1.ScmProvider) (bool, error) {
	logger := log.FromContext(ctx)
	finalizer := promoterv1alpha1.ScmProviderFinalizer

	if scmProvider.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(scmProvider, finalizer) {
			// Not being deleted and already has finalizer, nothing to do.
			return false, nil
		}

		// Finalizer is missing, add it.
		return false, retry.RetryOnConflict(retry.DefaultRetry, func() error { //nolint:wrapcheck
			if err := r.Get(ctx, client.ObjectKeyFromObject(scmProvider), scmProvider); err != nil {
				return err //nolint:wrapcheck
			}
			if controllerutil.AddFinalizer(scmProvider, finalizer) {
				return r.Update(ctx, scmProvider)
			}
			return nil
		})
	}

	// If we're here, the object is being deleted
	if !controllerutil.ContainsFinalizer(scmProvider, finalizer) {
		// Finalizer already removed, nothing to do.
		return false, nil
	}

	// Check if there are any GitRepositories referencing this ScmProvider
	var gitRepos promoterv1alpha1.GitRepositoryList
	if err := r.List(ctx, &gitRepos, client.InNamespace(scmProvider.Namespace)); err != nil {
		return false, fmt.Errorf("failed to list GitRepositories: %w", err)
	}

	var dependentRepos []string
	for _, gitRepo := range gitRepos.Items {
		if gitRepo.Spec.ScmProviderRef.Name == scmProvider.Name &&
			gitRepo.Spec.ScmProviderRef.Kind == promoterv1alpha1.ScmProviderKind {
			dependentRepos = append(dependentRepos, gitRepo.Name)
		}
	}

	if len(dependentRepos) > 0 {
		// Sort for deterministic error messages
		sort.Strings(dependentRepos)
		firstRepo := dependentRepos[0]
		var errMsg string
		if len(dependentRepos) == 1 {
			errMsg = fmt.Sprintf("ScmProvider %s/%s still has dependent GitRepository %s",
				scmProvider.Namespace, scmProvider.Name, firstRepo)
		} else {
			errMsg = fmt.Sprintf("ScmProvider %s/%s still has dependent GitRepository %s and %d more",
				scmProvider.Namespace, scmProvider.Name, firstRepo, len(dependentRepos)-1)
		}
		logger.Info("ScmProvider still has dependent GitRepositories, cannot delete",
			"scmProvider", scmProvider.Name, "count", len(dependentRepos), "first", firstRepo)
		return true, errors.New(errMsg)
	}

	// Remove finalizer from Secret if it exists
	if err := r.removeSecretFinalizer(ctx, scmProvider); err != nil {
		return false, fmt.Errorf("failed to remove Secret finalizer: %w", err)
	}

	// No dependent GitRepositories, remove finalizer
	controllerutil.RemoveFinalizer(scmProvider, finalizer)
	if err := r.Update(ctx, scmProvider); err != nil {
		return true, fmt.Errorf("failed to remove finalizer: %w", err)
	}
	return true, nil
}

func (r *ScmProviderReconciler) ensureSecretFinalizer(ctx context.Context, scmProvider *promoterv1alpha1.ScmProvider) error {
	logger := log.FromContext(ctx)

	if scmProvider.Spec.SecretRef == nil {
		return nil
	}

	finalizer := promoterv1alpha1.ScmProviderSecretFinalizer
	secretKey := types.NamespacedName{
		Namespace: scmProvider.Namespace,
		Name:      scmProvider.Spec.SecretRef.Name,
	}

	var secret v1.Secret
	if err := r.Get(ctx, secretKey, &secret); err != nil {
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
		if err := r.Get(ctx, secretKey, &secret); err != nil {
			return err //nolint:wrapcheck
		}
		// Check again after getting the secret in case it was deleted during the retry
		if !secret.DeletionTimestamp.IsZero() {
			return nil
		}
		if controllerutil.AddFinalizer(&secret, finalizer) {
			return r.Update(ctx, &secret)
		}
		return nil
	})
}

func (r *ScmProviderReconciler) removeSecretFinalizer(ctx context.Context, scmProvider *promoterv1alpha1.ScmProvider) error {
	logger := log.FromContext(ctx)

	if scmProvider.Spec.SecretRef == nil {
		return nil
	}

	finalizer := promoterv1alpha1.ScmProviderSecretFinalizer
	secretKey := types.NamespacedName{
		Namespace: scmProvider.Namespace,
		Name:      scmProvider.Spec.SecretRef.Name,
	}

	var secret v1.Secret
	if err := r.Get(ctx, secretKey, &secret); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("Secret not found, skipping finalizer removal", "secret", secretKey)
			return nil
		}
		return fmt.Errorf("failed to get Secret: %w", err)
	}

	if !controllerutil.ContainsFinalizer(&secret, finalizer) {
		return nil
	}

	// Check if there are other ScmProviders using this Secret
	var scmProviders promoterv1alpha1.ScmProviderList
	if err := r.List(ctx, &scmProviders, client.InNamespace(scmProvider.Namespace)); err != nil {
		return fmt.Errorf("failed to list ScmProviders: %w", err)
	}

	for _, sp := range scmProviders.Items {
		// Skip the ScmProvider being deleted
		if sp.Name == scmProvider.Name {
			continue
		}
		if sp.Spec.SecretRef != nil && sp.Spec.SecretRef.Name == secret.Name {
			logger.Info("Secret still referenced by other ScmProvider, keeping finalizer",
				"secret", secretKey, "scmProvider", sp.Name)
			return nil
		}
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error { //nolint:wrapcheck
		if err := r.Get(ctx, secretKey, &secret); err != nil {
			return err //nolint:wrapcheck
		}
		if controllerutil.RemoveFinalizer(&secret, finalizer) {
			return r.Update(ctx, &secret)
		}
		return nil
	})
}
