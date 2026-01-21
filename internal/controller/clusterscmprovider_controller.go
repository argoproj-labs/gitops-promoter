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
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// ClusterScmProviderReconciler reconciles a ClusterScmProvider object
type ClusterScmProviderReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=clusterscmproviders,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=clusterscmproviders/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=clusterscmproviders/finalizers,verbs=update
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ClusterScmProviderReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ClusterScmProvider")
	startTime := time.Now()

	var clusterScmProvider promoterv1alpha1.ClusterScmProvider
	// This function will update the resource status at the end of the reconciliation. don't call .Status().Update manually.
	defer utils.HandleReconciliationResult(ctx, startTime, &clusterScmProvider, r.Client, r.Recorder, &err)

	if err := r.Get(ctx, req.NamespacedName, &clusterScmProvider); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("ClusterScmProvider not found", "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get ClusterScmProvider: %w", err)
	}

	if deleted, err := r.handleFinalizer(ctx, &clusterScmProvider); err != nil || deleted {
		return ctrl.Result{}, err
	}

	// Add finalizer to referenced Secret if it exists
	if err := r.ensureSecretFinalizer(ctx, &clusterScmProvider); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to ensure Secret finalizer: %w", err)
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ClusterScmProviderReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.ClusterScmProvider{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("clusterscmprovider").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

func (r *ClusterScmProviderReconciler) handleFinalizer(ctx context.Context, clusterScmProvider *promoterv1alpha1.ClusterScmProvider) (bool, error) {
	// Check for dependent GitRepositories across all namespaces before allowing deletion
	checkDependencies := func() ([]string, error) {
		var gitRepos promoterv1alpha1.GitRepositoryList
		if err := r.List(ctx, &gitRepos); err != nil {
			return nil, fmt.Errorf("failed to list GitRepositories: %w", err)
		}

		var dependentRepos []string
		for _, gitRepo := range gitRepos.Items {
			// Skip GitRepositories that are also being deleted (allows cascade deletion)
			if !gitRepo.DeletionTimestamp.IsZero() {
				continue
			}
			if gitRepo.Spec.ScmProviderRef.Name == clusterScmProvider.Name &&
				gitRepo.Spec.ScmProviderRef.Kind == promoterv1alpha1.ClusterScmProviderKind {
				// Include namespace in identifier since this is cluster-scoped
				dependentRepos = append(dependentRepos, fmt.Sprintf("%s/%s", gitRepo.Namespace, gitRepo.Name))
			}
		}
		return dependentRepos, nil
	}

	deleted, err := handleResourceFinalizerWithDependencies(
		ctx,
		r.Client,
		r.Recorder,
		clusterScmProvider,
		promoterv1alpha1.ClusterScmProviderFinalizer,
		"ClusterScmProvider",
		checkDependencies,
	)

	// If we're being deleted and finalizer was removed, also remove Secret finalizer
	if deleted && err == nil {
		if err := r.removeSecretFinalizer(ctx, clusterScmProvider); err != nil {
			return false, fmt.Errorf("failed to remove Secret finalizer: %w", err)
		}
	}

	return deleted, err
}

func (r *ClusterScmProviderReconciler) ensureSecretFinalizer(ctx context.Context, clusterScmProvider *promoterv1alpha1.ClusterScmProvider) error {
	if clusterScmProvider.Spec.SecretRef == nil {
		return nil
	}

	return ensureSecretFinalizerForProvider(
		ctx,
		r.Client,
		r.SettingsMgr.GetControllerNamespace(),
		clusterScmProvider.Spec.SecretRef.Name,
		promoterv1alpha1.ClusterScmProviderSecretFinalizer,
	)
}

func (r *ClusterScmProviderReconciler) removeSecretFinalizer(ctx context.Context, clusterScmProvider *promoterv1alpha1.ClusterScmProvider) error {
	if clusterScmProvider.Spec.SecretRef == nil {
		return nil
	}

	// Check if there are other ClusterScmProviders using this Secret
	checkOtherProviders := func() (bool, error) {
		var clusterScmProviders promoterv1alpha1.ClusterScmProviderList
		if err := r.List(ctx, &clusterScmProviders); err != nil {
			return false, fmt.Errorf("failed to list ClusterScmProviders: %w", err)
		}

		for _, csp := range clusterScmProviders.Items {
			// Skip the ClusterScmProvider being deleted
			if csp.Name == clusterScmProvider.Name {
				continue
			}
			if csp.Spec.SecretRef != nil && csp.Spec.SecretRef.Name == clusterScmProvider.Spec.SecretRef.Name {
				return true, nil
			}
		}
		return false, nil
	}

	return removeSecretFinalizerForProvider(
		ctx,
		r.Client,
		r.SettingsMgr.GetControllerNamespace(),
		clusterScmProvider.Spec.SecretRef.Name,
		promoterv1alpha1.ClusterScmProviderSecretFinalizer,
		checkOtherProviders,
	)
}
