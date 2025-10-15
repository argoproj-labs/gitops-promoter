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

	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
func (r *TimedCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling TimedCommitStatus")
	startTime := time.Now()

	var tcs promoterv1alpha1.TimedCommitStatus
	defer utils.HandleReconciliationResult(ctx, startTime, &tcs, r.Client, r.Recorder, &err)

	// 1. Fetch the TimedCommitStatus instance
	err = r.Get(ctx, req.NamespacedName, &tcs, &client.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("TimedCommitStatus not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get TimedCommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get TimedCommitStatus %q: %w", req.Name, err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	meta.RemoveStatusCondition(tcs.GetConditions(), string(promoterConditions.Ready))

	// 2. Fetch the referenced PromotionStrategy
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{
		Namespace: tcs.Namespace,
		Name:      tcs.Spec.PromotionStrategyRef.Name,
	}
	err = r.Get(ctx, psKey, &ps)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Error(err, "referenced PromotionStrategy not found", "promotionStrategy", tcs.Spec.PromotionStrategyRef.Name)
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", tcs.Spec.PromotionStrategyRef.Name, err)
		}
		logger.Error(err, "failed to get PromotionStrategy")
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", tcs.Spec.PromotionStrategyRef.Name, err)
	}

	// 3. Process each environment defined in the TimedCommitStatus
	err = r.processEnvironments(ctx, &tcs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to process environments: %w", err)
	}

	// 4. Update status
	err = r.Status().Update(ctx, &tcs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update TimedCommitStatus status: %w", err)
	}

	// Requeue periodically to check time-based conditions
	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.TimedCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration for TimedCommitStatus %q: %w", tcs.Name, err)
	}

	return ctrl.Result{
		RequeueAfter: requeueDuration,
	}, nil
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

// processEnvironments processes each environment defined in the TimedCommitStatus spec,
// creating or updating CommitStatus resources based on time-based rules.
// The logic is: for each environment, check if the commit in the current environment's active branch
// has been running for the required duration. If so, report success for the NEXT environment's proposed SHA.
// This gates promotions by ensuring commits "bake" in lower environments before moving up.
func (r *TimedCommitStatusReconciler) processEnvironments(ctx context.Context, tcs *promoterv1alpha1.TimedCommitStatus, ps *promoterv1alpha1.PromotionStrategy) error {
	logger := log.FromContext(ctx)

	// Initialize or clear the environments status
	tcs.Status.Environments = make([]promoterv1alpha1.TimedCommitStatusEnvironmentsStatus, 0, len(tcs.Spec.Environments))

	for _, envConfig := range tcs.Spec.Environments {
		// Find the current environment index in the PromotionStrategy
		currentEnvIndex := -1
		var currentEnvStatus *promoterv1alpha1.EnvironmentStatus
		for i := range ps.Status.Environments {
			if ps.Status.Environments[i].Branch == envConfig.Branch {
				currentEnvIndex = i
				currentEnvStatus = &ps.Status.Environments[i]
				break
			}
		}

		if currentEnvStatus == nil {
			logger.Info("Environment not found in PromotionStrategy status", "branch", envConfig.Branch)
			continue
		}

		// Find the next (higher) environment in the promotion sequence
		nextEnvIndex := currentEnvIndex + 1
		if nextEnvIndex >= len(ps.Status.Environments) {
			// This is the last environment, no next environment to gate
			logger.V(1).Info("Skipping last environment in promotion sequence", "branch", envConfig.Branch)
			continue
		}

		nextEnvStatus := &ps.Status.Environments[nextEnvIndex]

		// Get the current environment's active hydrated SHA and commit time
		currentActiveSha := currentEnvStatus.Active.Hydrated.Sha
		currentActiveCommitTime := currentEnvStatus.Active.Hydrated.CommitTime.Time

		// Get the next environment's proposed hydrated SHA
		nextProposedSha := nextEnvStatus.Proposed.Hydrated.Sha

		if currentActiveSha == "" {
			logger.Info("No active hydrated commit in current environment", "branch", envConfig.Branch)
			continue
		}

		if nextProposedSha == "" {
			logger.V(1).Info("No proposed hydrated commit in next environment",
				"currentBranch", envConfig.Branch,
				"nextBranch", nextEnvStatus.Branch)
			continue
		}

		// Calculate timing information based on current environment's active commit
		var elapsed time.Duration
		if !currentActiveCommitTime.IsZero() {
			elapsed = time.Since(currentActiveCommitTime)
		}

		// Determine commit status phase based on time elapsed in current environment
		// This status will be reported for the next environment's proposed SHA
		var phase promoterv1alpha1.CommitStatusPhase
		var message string
		// If there is a pending promotion in the lower (current) environment (proposed != active), report pending
		// This indicates there's an open PR that hasn't been merged yet
		if currentEnvStatus.Proposed.Dry.Sha != "" && currentEnvStatus.Proposed.Dry.Sha != currentEnvStatus.Active.Dry.Sha {
			phase = promoterv1alpha1.CommitPhasePending
			message = fmt.Sprintf("Waiting for pending promotion in %s environment to be merged before allowing promotion", envConfig.Branch)
		} else {
			phase, message = r.calculateCommitStatusPhase(currentActiveCommitTime, envConfig.Duration.Duration, elapsed, envConfig.Branch)
		}

		// Update status for this environment
		envTimedStatus := promoterv1alpha1.TimedCommitStatusEnvironmentsStatus{
			Branch:           envConfig.Branch,
			Sha:              currentActiveSha,
			CommitTime:       metav1.NewTime(currentActiveCommitTime),
			RequiredDuration: envConfig.Duration,
			Phase:            string(phase),
			TimeElapsed:      metav1.Duration{Duration: elapsed},
		}
		tcs.Status.Environments = append(tcs.Status.Environments, envTimedStatus)

		// Create or update the CommitStatus for the NEXT environment's proposed SHA
		// This gates the promotion to the next environment
		// Pass the current (lower) environment branch so the commit status name reflects which env we're waiting on
		err := r.upsertCommitStatus(ctx, tcs, ps, nextEnvStatus.Branch, nextProposedSha, phase, message, envConfig.Branch)
		if err != nil {
			return fmt.Errorf("failed to upsert CommitStatus for next environment %q: %w", nextEnvStatus.Branch, err)
		}

		logger.Info("Processed environment time gate",
			"currentBranch", envConfig.Branch,
			"currentActiveSha", currentActiveSha,
			"nextBranch", nextEnvStatus.Branch,
			"nextProposedSha", nextProposedSha,
			"phase", phase,
			"elapsed", elapsed.Round(time.Second),
			"required", envConfig.Duration.Duration)
	}

	return nil
}

// calculateCommitStatusPhase determines the commit status phase based on time elapsed since deployment
func (r *TimedCommitStatusReconciler) calculateCommitStatusPhase(commitTime time.Time, requiredDuration time.Duration, elapsed time.Duration, lowerEnvBranch string) (promoterv1alpha1.CommitStatusPhase, string) {
	if commitTime.IsZero() {
		// If we don't have a commit time, we can't make a decision
		return promoterv1alpha1.CommitPhasePending, fmt.Sprintf("Waiting for commit time to be available in %s environment", lowerEnvBranch)
	}

	if elapsed >= requiredDuration {
		// Sufficient time has passed
		return promoterv1alpha1.CommitPhaseSuccess, fmt.Sprintf("Time-based gate requirement met for %s environment", lowerEnvBranch)
	}

	// Not enough time has passed yet
	return promoterv1alpha1.CommitPhasePending, fmt.Sprintf("Waiting for time-based gate on %s environment", lowerEnvBranch)
}

func (r *TimedCommitStatusReconciler) upsertCommitStatus(ctx context.Context, tcs *promoterv1alpha1.TimedCommitStatus, ps *promoterv1alpha1.PromotionStrategy, branch, sha string, phase promoterv1alpha1.CommitStatusPhase, message string, lowerEnvBranch string) error {
	logger := log.FromContext(ctx)

	// Generate a consistent name for the CommitStatus
	commitStatusName := utils.KubeSafeUniqueName(ctx, fmt.Sprintf("%s-%s-timed", tcs.Name, branch))

	commitStatus := promoterv1alpha1.CommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      commitStatusName,
			Namespace: tcs.Namespace,
		},
	}

	// Create or update the CommitStatus
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, &commitStatus, func() error {
		// Set owner reference to the TimedCommitStatus
		if err := ctrl.SetControllerReference(tcs, &commitStatus, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference: %w", err)
		}

		// Set labels for easy identification
		if commitStatus.Labels == nil {
			commitStatus.Labels = make(map[string]string)
		}
		commitStatus.Labels["promoter.argoproj.io/timed-commit-status"] = utils.KubeSafeLabel(tcs.Name)
		commitStatus.Labels["promoter.argoproj.io/branch"] = utils.KubeSafeLabel(branch)
		commitStatus.Labels["promoter.argoproj.io/commit-status"] = "timed"

		// Set the spec
		commitStatus.Spec.RepositoryReference = ps.Spec.RepositoryReference
		// Use the lower environment branch name to show which environment we're waiting on
		commitStatus.Spec.Name = "timed-gate/" + lowerEnvBranch
		commitStatus.Spec.Description = message
		commitStatus.Spec.Phase = phase
		commitStatus.Spec.Sha = sha

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to create or update CommitStatus: %w", err)
	}

	logger.V(1).Info("Upserted CommitStatus", "name", commitStatusName, "phase", phase)
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
