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
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// GitRepositoryReconciler reconciles a GitRepository object
type GitRepositoryReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories/finalizers,verbs=update
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *GitRepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling GitRepository")
	startTime := time.Now()

	var gitRepo promoterv1alpha1.GitRepository
	// This function will update the resource status at the end of the reconciliation. don't call .Status().Update manually.
	defer utils.HandleReconciliationResult(ctx, startTime, &gitRepo, r.Client, r.Recorder, &err)

	if err := r.Get(ctx, req.NamespacedName, &gitRepo); err != nil {
		if k8serrors.IsNotFound(err) {
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
	// Check for dependent PullRequests before allowing deletion
	checkDependencies := func() ([]string, error) {
		var pullRequests promoterv1alpha1.PullRequestList
		if err := r.List(ctx, &pullRequests, client.InNamespace(gitRepo.Namespace)); err != nil {
			return nil, fmt.Errorf("failed to list PullRequests: %w", err)
		}

		var dependentPRs []string
		for _, pr := range pullRequests.Items {
			// Skip PullRequests that are also being deleted (allows cascade deletion)
			if !pr.DeletionTimestamp.IsZero() {
				continue
			}
			if pr.Spec.RepositoryReference.Name == gitRepo.Name {
				dependentPRs = append(dependentPRs, pr.Name)
			}
		}
		return dependentPRs, nil
	}

	return handleResourceFinalizerWithDependencies(
		ctx,
		r.Client,
		r.Recorder,
		gitRepo,
		promoterv1alpha1.GitRepositoryFinalizer,
		"GitRepository",
		checkDependencies,
	)
}
