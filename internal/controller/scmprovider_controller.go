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

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	// This function will update the resource status at the end of the reconciliation. don't call .Status().Update manually.
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
	// Check for dependent GitRepositories before allowing deletion
	checkDependencies := func() ([]string, error) {
		var gitRepos promoterv1alpha1.GitRepositoryList
		if err := r.List(ctx, &gitRepos, client.InNamespace(scmProvider.Namespace)); err != nil {
			return nil, fmt.Errorf("failed to list GitRepositories: %w", err)
		}

		var dependentRepos []string
		for _, gitRepo := range gitRepos.Items {
			// Skip GitRepositories that are also being deleted (allows cascade deletion)
			if !gitRepo.DeletionTimestamp.IsZero() {
				continue
			}
			if gitRepo.Spec.ScmProviderRef.Name == scmProvider.Name &&
				gitRepo.Spec.ScmProviderRef.Kind == promoterv1alpha1.ScmProviderKind {
				dependentRepos = append(dependentRepos, gitRepo.Name)
			}
		}
		return dependentRepos, nil
	}

	deleted, err := handleResourceFinalizerWithDependencies(
		ctx,
		r.Client,
		r.Recorder,
		scmProvider,
		promoterv1alpha1.ScmProviderFinalizer,
		"ScmProvider",
		checkDependencies,
	)

	// If we're being deleted and finalizer was removed, also remove Secret finalizer
	if deleted && err == nil {
		if err := r.removeSecretFinalizer(ctx, scmProvider); err != nil {
			return false, fmt.Errorf("failed to remove Secret finalizer: %w", err)
		}
	}

	return deleted, err
}

func (r *ScmProviderReconciler) ensureSecretFinalizer(ctx context.Context, scmProvider *promoterv1alpha1.ScmProvider) error {
	if scmProvider.Spec.SecretRef == nil {
		return nil
	}

	return ensureSecretFinalizerForProvider(
		ctx,
		r.Client,
		scmProvider.Namespace,
		scmProvider.Spec.SecretRef.Name,
		promoterv1alpha1.ScmProviderSecretFinalizer,
	)
}

func (r *ScmProviderReconciler) removeSecretFinalizer(ctx context.Context, scmProvider *promoterv1alpha1.ScmProvider) error {
	if scmProvider.Spec.SecretRef == nil {
		return nil
	}

	// Check if there are other ScmProviders using this Secret
	checkOtherProviders := func() (bool, error) {
		var scmProviders promoterv1alpha1.ScmProviderList
		if err := r.List(ctx, &scmProviders, client.InNamespace(scmProvider.Namespace)); err != nil {
			return false, fmt.Errorf("failed to list ScmProviders: %w", err)
		}

		for _, sp := range scmProviders.Items {
			// Skip the ScmProvider being deleted
			if sp.Name == scmProvider.Name {
				continue
			}
			if sp.Spec.SecretRef != nil && sp.Spec.SecretRef.Name == scmProvider.Spec.SecretRef.Name {
				return true, nil
			}
		}
		return false, nil
	}

	return removeSecretFinalizerForProvider(
		ctx,
		r.Client,
		scmProvider.Namespace,
		scmProvider.Spec.SecretRef.Name,
		promoterv1alpha1.ScmProviderSecretFinalizer,
		checkOtherProviders,
	)
}
