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

	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// TimedCommitStatusReconciler reconciles a TimedCommitStatus object
type TimedCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	SettingsMgr *settings.Manager
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=timedcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=timedcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=timedcommitstatuses/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the TimedCommitStatus object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *TimedCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	// How this should reconcile:
	// 1. Fetch the TimedCommitStatus instance
	// 2. If not found, return (object must have been deleted)
	// 3. If found, ensure that the referenced PromotionStrategy exists
	// 4. If the PromotionStrategy does not exist, set status to error and return

	// Logic to reconcile the TimedCommitStatus resource.
	// The time commit status logic is as follows:
	// The purpose of this controller is to gate promotions based on the durations defined in the TimedCommitStatusConfiguration
	// resource for each environment/branch. The TimedCommitStatus resource references a PromotionStrategy, and the controller ensures that promotions
	// only occur if the required time has passed since the last promotion.
	// The controller will periodically check the status of the PromotionStrategy and update the TimedCommitStatus accordingly.
	// The controller will act as an activeCommitStatus, meaning it will report the state of the current environment based on the time-based rules.
	// It will manage a commitstatus with the sha of active hydrated commit.
	// If the time-based rules do not allow for a promotion, it will set the commitstatus to pending with a message indicating when the next promotion is allowed.
	// If there is an error in fetching the PromotionStrategy or any other issue, it will set the commitstatus to failure with an appropriate message.
	// If the time-based rules allow for a promotion, it will set the commitstatus to success.
	// The controller will not directly trigger promotions, but will provide the necessary status updates to inform other components of the system.
	// This ensures that promotions are gated based on time, preventing premature or unintended deployments.

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TimedCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.TimedCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get TimedCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.TimedCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get TimedCommitStatus max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.TimedCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.PromotionStrategy{}, r.enqueueTimedCommitStatusForPromotionStrategy()).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// enqueueTimedCommitStatusForPromotionStrategy returns a handler that enqueues all TimedCommitStatus resources
// that reference a PromotionStrategy when that PromotionStrategy changes
func (r *TimedCommitStatusReconciler) enqueueTimedCommitStatusForPromotionStrategy() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
		if !ok {
			return nil
		}

		// List all TimedCommitStatus resources in the same namespace
		var tcsList promoterv1alpha1.TimedCommitStatusList
		if err := r.List(ctx, &tcsList, client.InNamespace(ps.Namespace)); err != nil {
			log.FromContext(ctx).Error(err, "failed to list TimedCommitStatus resources")
			return nil
		}

		// Enqueue all TimedCommitStatus resources that reference this PromotionStrategy
		var requests []ctrl.Request
		for _, tcs := range tcsList.Items {
			if tcs.Spec.PromotionStrategyRef.Name == ps.Name {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKeyFromObject(&tcs),
				})
			}
		}

		return requests
	})
}
