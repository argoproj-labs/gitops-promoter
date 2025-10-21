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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// GitRepositoryReconciler reconciles a GitRepository object
type GitRepositoryReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories/finalizers,verbs=update
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *GitRepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling GitRepository")

	var gitRepo promoterv1alpha1.GitRepository
	if err := r.Get(ctx, req.NamespacedName, &gitRepo); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("GitRepository not found", "namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	if deleted, err := r.handleFinalizer(ctx, &gitRepo); err != nil || deleted {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GitRepositoryReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.GitRepository{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

func (r *GitRepositoryReconciler) handleFinalizer(ctx context.Context, gitRepo *promoterv1alpha1.GitRepository) (bool, error) {
	logger := log.FromContext(ctx)
	finalizer := "gitrepository.promoter.argoproj.io/finalizer"

	if gitRepo.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(gitRepo, finalizer) {
			// Not being deleted and already has finalizer, nothing to do.
			return false, nil
		}

		// Finalizer is missing, add it.
		return false, retry.RetryOnConflict(retry.DefaultRetry, func() error { //nolint:wrapcheck
			if err := r.Get(ctx, client.ObjectKeyFromObject(gitRepo), gitRepo); err != nil {
				return err //nolint:wrapcheck
			}
			if controllerutil.AddFinalizer(gitRepo, finalizer) {
				return r.Update(ctx, gitRepo)
			}
			return nil
		})
	}

	// If we're here, the object is being deleted
	if !controllerutil.ContainsFinalizer(gitRepo, finalizer) {
		// Finalizer already removed, nothing to do.
		return false, nil
	}

	// Check if there are any PullRequests referencing this GitRepository
	var pullRequests promoterv1alpha1.PullRequestList
	if err := r.List(ctx, &pullRequests, client.InNamespace(gitRepo.Namespace)); err != nil {
		return false, fmt.Errorf("failed to list PullRequests: %w", err)
	}

	for _, pr := range pullRequests.Items {
		if pr.Spec.RepositoryReference.Name == gitRepo.Name {
			logger.Info("GitRepository still has dependent PullRequests, cannot delete",
				"gitRepository", gitRepo.Name, "pullRequest", pr.Name)
			return true, fmt.Errorf("GitRepository %s/%s still has dependent PullRequest %s",
				gitRepo.Namespace, gitRepo.Name, pr.Name)
		}
	}

	// No dependent PullRequests, remove finalizer
	controllerutil.RemoveFinalizer(gitRepo, finalizer)
	if err := r.Update(ctx, gitRepo); err != nil {
		return true, fmt.Errorf("failed to remove finalizer: %w", err)
	}
	return true, nil
}
