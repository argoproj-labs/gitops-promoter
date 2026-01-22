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
	"reflect"
	"time"

	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	acmetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
)

// TimedCommitStatusReconciler reconciles a TimedCommitStatus object
type TimedCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager
	EnqueueCTP  CTPEnqueueFunc
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
	// This function will update the resource status at the end of the reconciliation. don't call .Status().Update manually.
	defer utils.HandleReconciliationResult(ctx, startTime, &tcs, r.Client, r.Recorder, &err)

	// 1. Fetch the TimedCommitStatus instance
	err = r.Get(ctx, req.NamespacedName, &tcs, &client.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("TimedCommitStatus not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get TimedCommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get TimedCommitStatus %q: %w", req.Name, err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	meta.RemoveStatusCondition(tcs.GetConditions(), string(promoterConditions.Ready))

	// Check if the TimedCommitStatus is being deleted
	if !tcs.DeletionTimestamp.IsZero() {
		// Handle cleanup
		if controllerutil.ContainsFinalizer(&tcs, promoterv1alpha1.TimedCommitStatusFinalizer) {
			// Fetch the PromotionStrategy to clean up auto-configured fields
			var ps promoterv1alpha1.PromotionStrategy
			psKey := client.ObjectKey{
				Namespace: tcs.Namespace,
				Name:      tcs.Spec.PromotionStrategyRef.Name,
			}
			err = r.Get(ctx, psKey, &ps)
			if err != nil {
				if k8serrors.IsNotFound(err) {
					// PromotionStrategy already deleted, just remove finalizer
					logger.Info("PromotionStrategy not found during cleanup, removing finalizer")
				} else {
					return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy during cleanup: %w", err)
				}
			} else {
				// Clean up the auto-configured timer check
				err = r.cleanupTimerCheck(ctx, &tcs, &ps)
				if err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to cleanup timer check: %w", err)
				}
			}

			// Remove finalizer
			controllerutil.RemoveFinalizer(&tcs, promoterv1alpha1.TimedCommitStatusFinalizer)
			if err := r.Update(ctx, &tcs); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if it doesn't exist
	if !controllerutil.ContainsFinalizer(&tcs, promoterv1alpha1.TimedCommitStatusFinalizer) {
		controllerutil.AddFinalizer(&tcs, promoterv1alpha1.TimedCommitStatusFinalizer)
		if err := r.Update(ctx, &tcs); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to add finalizer: %w", err)
		}
		// Return and requeue - the update will trigger another reconciliation
		return ctrl.Result{}, nil
	}

	// 2. Fetch the referenced PromotionStrategy
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{
		Namespace: tcs.Namespace,
		Name:      tcs.Spec.PromotionStrategyRef.Name,
	}
	err = r.Get(ctx, psKey, &ps)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Error(err, "referenced PromotionStrategy not found", "promotionStrategy", tcs.Spec.PromotionStrategyRef.Name)
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", tcs.Spec.PromotionStrategyRef.Name, err)
		}
		logger.Error(err, "failed to get PromotionStrategy")
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", tcs.Spec.PromotionStrategyRef.Name, err)
	}

	// 3. Auto-configure the timer active commit status on the PromotionStrategy environments
	err = r.autoConfigureTimerCheck(ctx, &tcs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to auto-configure timer check: %w", err)
	}

	// 4. Process each environment defined in the TimedCommitStatus
	// Returns the list of environments that transitioned to success and the CommitStatus objects
	transitionedEnvironments, commitStatuses, err := r.processEnvironments(ctx, &tcs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to process environments: %w", err)
	}

	// 5. Clean up orphaned CommitStatus resources that are no longer in the environment list
	err = r.cleanupOrphanedCommitStatuses(ctx, &tcs, commitStatuses)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to cleanup orphaned CommitStatus resources: %w", err)
	}

	// 6. Inherit conditions from CommitStatus objects
	utils.InheritNotReadyConditionFromObjects(&tcs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	// 7. If any time gates transitioned to success, touch the corresponding ChangeTransferPolicies to trigger reconciliation
	if len(transitionedEnvironments) > 0 {
		r.touchChangeTransferPolicies(ctx, &ps, transitionedEnvironments)
	}

	// Requeue based on the shortest duration or default requeue duration
	requeueDuration := r.calculateRequeueDuration(ctx, &tcs)

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
// has been running for the required duration. If so, report success for the current environment's active SHA.
// This allows using the timed commit status as an active commit status gate to block promotions
// when the current environment hasn't been stable for the required duration.
// Returns a list of environment branches that transitioned from pending to success and the CommitStatus objects created/updated.
func (r *TimedCommitStatusReconciler) processEnvironments(ctx context.Context, tcs *promoterv1alpha1.TimedCommitStatus, ps *promoterv1alpha1.PromotionStrategy) ([]string, []*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	// Track which environments transitioned to success
	transitionedEnvironments := []string{}
	// Track all CommitStatus objects created/updated
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(tcs.Spec.Environments))

	// Save the previous status before clearing it, so we can detect transitions
	previousStatus := tcs.Status.DeepCopy()
	if previousStatus == nil {
		previousStatus = &promoterv1alpha1.TimedCommitStatusStatus{}
	}

	// Build a map of environments from PromotionStrategy for efficient lookup
	envStatusMap := make(map[string]*promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for i := range ps.Status.Environments {
		envStatusMap[ps.Status.Environments[i].Branch] = &ps.Status.Environments[i]
	}

	// Initialize or clear the environments status
	tcs.Status.Environments = make([]promoterv1alpha1.TimedCommitStatusEnvironmentsStatus, 0, len(tcs.Spec.Environments))

	for _, envConfig := range tcs.Spec.Environments {
		// Look up the environment in the map
		currentEnvStatus, found := envStatusMap[envConfig.Branch]
		if !found {
			logger.Info("Environment not found in PromotionStrategy status", "branch", envConfig.Branch)
			continue
		}

		// Get the current environment's active hydrated SHA and commit time
		currentActiveSha := currentEnvStatus.Active.Hydrated.Sha
		currentActiveCommitTime := currentEnvStatus.Active.Hydrated.CommitTime.Time

		if currentActiveSha == "" {
			logger.Info("No active hydrated commit in current environment", "branch", envConfig.Branch)
			continue
		}

		if currentActiveCommitTime.IsZero() {
			logger.Info("No active hydrated commit time in current environment", "branch", envConfig.Branch)
			continue
		}

		// Calculate timing information based on current environment's active commit
		elapsed := time.Since(currentActiveCommitTime)
		timeRemaining := envConfig.Duration.Duration - elapsed
		if timeRemaining < 0 {
			timeRemaining = 0
		}

		// Determine commit status phase based on time elapsed in current environment
		// This status will be reported for the current environment's active SHA
		// When a new commit is merged, the active SHA and commit time automatically update,
		// which naturally resets the timer to 0 and reports pending until the duration is met
		phase, message := r.calculateCommitStatusPhase(envConfig.Duration.Duration, elapsed, envConfig.Branch)

		// Check if this time gate transitioned to success
		// Find the previous status for this environment
		var previousPhase string
		for _, prevEnv := range previousStatus.Environments {
			if prevEnv.Branch == envConfig.Branch {
				previousPhase = prevEnv.Phase
				break
			}
		}
		if previousPhase == string(promoterv1alpha1.CommitPhasePending) && phase == promoterv1alpha1.CommitPhaseSuccess {
			transitionedEnvironments = append(transitionedEnvironments, envConfig.Branch)
			logger.Info("Time gate transitioned to success",
				"branch", envConfig.Branch,
				"sha", currentActiveSha)
		}

		// Update status for this environment
		envTimedStatus := promoterv1alpha1.TimedCommitStatusEnvironmentsStatus{
			Branch:                  envConfig.Branch,
			Sha:                     currentActiveSha,
			CommitTime:              metav1.NewTime(currentActiveCommitTime),
			RequiredDuration:        envConfig.Duration,
			Phase:                   string(phase),
			AtMostDurationRemaining: metav1.Duration{Duration: timeRemaining},
		}
		tcs.Status.Environments = append(tcs.Status.Environments, envTimedStatus)

		// Create or update the CommitStatus for the current environment's active SHA
		// This acts as an active commit status that gates promotions from this environment
		cs, err := r.upsertCommitStatus(ctx, tcs, ps, envConfig.Branch, currentActiveSha, phase, message, envConfig.Branch)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to upsert CommitStatus for environment %q: %w", envConfig.Branch, err)
		}
		commitStatuses = append(commitStatuses, cs)

		logger.Info("Processed environment time gate",
			"branch", envConfig.Branch,
			"activeSha", currentActiveSha,
			"phase", phase,
			"elapsed", elapsed.Round(time.Second),
			"required", envConfig.Duration.Duration)
	}

	return transitionedEnvironments, commitStatuses, nil
}

// cleanupOrphanedCommitStatuses deletes CommitStatus resources that are owned by this TimedCommitStatus
// but are not in the current list of valid CommitStatus resources (i.e., they correspond to removed or renamed environments).
//
//nolint:dupl // Similar to PromotionStrategy cleanup but works with different types
func (r *TimedCommitStatusReconciler) cleanupOrphanedCommitStatuses(ctx context.Context, tcs *promoterv1alpha1.TimedCommitStatus, validCommitStatuses []*promoterv1alpha1.CommitStatus) error {
	logger := log.FromContext(ctx)

	// Create a set of valid CommitStatus names for quick lookup
	validCommitStatusNames := make(map[string]bool)
	for _, cs := range validCommitStatuses {
		validCommitStatusNames[cs.Name] = true
	}

	// List all CommitStatus resources in the namespace with the TimedCommitStatus label
	var commitStatusList promoterv1alpha1.CommitStatusList
	err := r.List(ctx, &commitStatusList, client.InNamespace(tcs.Namespace), client.MatchingLabels{
		promoterv1alpha1.TimedCommitStatusLabel: utils.KubeSafeLabel(tcs.Name),
	})
	if err != nil {
		return fmt.Errorf("failed to list CommitStatus resources: %w", err)
	}

	// Delete CommitStatus resources that are not in the valid list
	for _, cs := range commitStatusList.Items {
		// Skip if this CommitStatus is in the valid list
		if validCommitStatusNames[cs.Name] {
			continue
		}

		// Verify this CommitStatus is owned by this TimedCommitStatus before deleting
		if !metav1.IsControlledBy(&cs, tcs) {
			logger.V(4).Info("Skipping CommitStatus not owned by this TimedCommitStatus",
				"commitStatusName", cs.Name,
				"timedCommitStatus", tcs.Name)
			continue
		}

		// Delete the orphaned CommitStatus
		logger.Info("Deleting orphaned CommitStatus",
			"commitStatusName", cs.Name,
			"timedCommitStatus", tcs.Name,
			"namespace", tcs.Namespace)

		if err := r.Delete(ctx, &cs); err != nil {
			if k8serrors.IsNotFound(err) {
				// Already deleted, which is fine
				logger.V(4).Info("CommitStatus already deleted", "commitStatusName", cs.Name)
				continue
			}
			return fmt.Errorf("failed to delete orphaned CommitStatus %q: %w", cs.Name, err)
		}

		r.Recorder.Eventf(tcs, nil, "Normal", constants.OrphanedCommitStatusDeletedReason, "CleaningOrphanedResources", constants.OrphanedCommitStatusDeletedMessage, cs.Name)
	}

	return nil
}

// calculateCommitStatusPhase determines the commit status phase based on time elapsed since deployment
func (r *TimedCommitStatusReconciler) calculateCommitStatusPhase(requiredDuration time.Duration, elapsed time.Duration, envBranch string) (promoterv1alpha1.CommitStatusPhase, string) {
	if elapsed >= requiredDuration {
		// Sufficient time has passed
		return promoterv1alpha1.CommitPhaseSuccess, fmt.Sprintf("Time-based gate requirement met for %s environment", envBranch)
	}

	// Not enough time has passed yet
	return promoterv1alpha1.CommitPhasePending, fmt.Sprintf("Waiting for time-based gate on %s environment", envBranch)
}

func (r *TimedCommitStatusReconciler) upsertCommitStatus(ctx context.Context, tcs *promoterv1alpha1.TimedCommitStatus, ps *promoterv1alpha1.PromotionStrategy, branch, sha string, phase promoterv1alpha1.CommitStatusPhase, message string, envBranch string) (*promoterv1alpha1.CommitStatus, error) {
	// Generate a consistent name for the CommitStatus
	commitStatusName := utils.KubeSafeUniqueName(ctx, fmt.Sprintf("%s-%s-timed", tcs.Name, branch))

	// Build owner reference
	kind := reflect.TypeOf(promoterv1alpha1.TimedCommitStatus{}).Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)

	// Build the apply configuration
	commitStatusApply := acv1alpha1.CommitStatus(commitStatusName, tcs.Namespace).
		WithLabels(map[string]string{
			promoterv1alpha1.TimedCommitStatusLabel: utils.KubeSafeLabel(tcs.Name),
			promoterv1alpha1.EnvironmentLabel:       utils.KubeSafeLabel(branch),
			promoterv1alpha1.CommitStatusLabel:      promoterv1alpha1.TimerCommitStatusKey,
		}).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(tcs.Name).
			WithUID(tcs.UID).
			WithController(true).
			WithBlockOwnerDeletion(true)).
		WithSpec(acv1alpha1.CommitStatusSpec().
			WithRepositoryReference(acv1alpha1.ObjectReference().WithName(ps.Spec.RepositoryReference.Name)).
			WithName(promoterv1alpha1.TimerCommitStatusKey + "/" + envBranch).
			WithDescription(message).
			WithPhase(phase).
			WithSha(sha))

	// Apply using Server-Side Apply with Patch to get the result directly
	commitStatus := &promoterv1alpha1.CommitStatus{}
	commitStatus.Name = commitStatusName
	commitStatus.Namespace = tcs.Namespace
	if err := r.Patch(ctx, commitStatus, utils.ApplyPatch{ApplyConfig: commitStatusApply}, client.FieldOwner(constants.TimedCommitStatusControllerFieldOwner), client.ForceOwnership); err != nil {
		return nil, fmt.Errorf("failed to apply CommitStatus: %w", err)
	}

	return commitStatus, nil
}

// autoConfigureTimerCheck uses server-side apply to add the "timer" active commit status
// to each environment configured in the TimedCommitStatus. This eliminates the need for users
// to manually add `activeCommitStatuses: [{key: "timer"}]` to their PromotionStrategy.
func (r *TimedCommitStatusReconciler) autoConfigureTimerCheck(ctx context.Context, tcs *promoterv1alpha1.TimedCommitStatus, ps *promoterv1alpha1.PromotionStrategy) error {
	logger := log.FromContext(ctx)

	// Build a map of branches configured in the TimedCommitStatus for quick lookup
	timedBranches := make(map[string]bool, len(tcs.Spec.Environments))
	for _, env := range tcs.Spec.Environments {
		timedBranches[env.Branch] = true
	}

	// Build the apply configuration for the PromotionStrategy
	// We only touch the spec.environments field to add activeCommitStatuses to environments
	// that have timed gates configured
	psApply := acv1alpha1.PromotionStrategy(ps.Name, ps.Namespace)

	specApply := acv1alpha1.PromotionStrategySpec()
	envApplyConfigs := make([]*acv1alpha1.EnvironmentApplyConfiguration, 0, len(ps.Spec.Environments))

	for _, env := range ps.Spec.Environments {
		// Only apply activeCommitStatuses to environments that are configured in the TimedCommitStatus
		if timedBranches[env.Branch] {
			envApply := acv1alpha1.Environment().
				WithBranch(env.Branch).
				WithActiveCommitStatuses(acv1alpha1.CommitStatusSelector().WithKey(promoterv1alpha1.TimerCommitStatusKey))

			envApplyConfigs = append(envApplyConfigs, envApply)
		}
	}

	// Only apply if we have environments to configure
	if len(envApplyConfigs) > 0 {
		specApply = specApply.WithEnvironments(envApplyConfigs...)
		psApply = psApply.WithSpec(specApply)

		// Apply using Server-Side Apply
		if err := r.Patch(ctx, ps, utils.ApplyPatch{ApplyConfig: psApply}, client.FieldOwner(constants.TimedCommitStatusControllerFieldOwner), client.ForceOwnership); err != nil {
			return fmt.Errorf("failed to apply timer check to PromotionStrategy: %w", err)
		}

		logger.V(4).Info("Auto-configured timer check on PromotionStrategy environments",
			"promotionStrategy", ps.Name,
			"configuredEnvironments", len(envApplyConfigs))
	}

	return nil
}

// cleanupTimerCheck removes the auto-configured "timer" active commit status
// from PromotionStrategy environments when the TimedCommitStatus is deleted.
func (r *TimedCommitStatusReconciler) cleanupTimerCheck(ctx context.Context, tcs *promoterv1alpha1.TimedCommitStatus, ps *promoterv1alpha1.PromotionStrategy) error {
	logger := log.FromContext(ctx)

	// Build a map of branches configured in the TimedCommitStatus for quick lookup
	timedBranches := make(map[string]bool, len(tcs.Spec.Environments))
	for _, env := range tcs.Spec.Environments {
		timedBranches[env.Branch] = true
	}

	// Build the apply configuration to remove timer check from environments
	psApply := acv1alpha1.PromotionStrategy(ps.Name, ps.Namespace)
	specApply := acv1alpha1.PromotionStrategySpec()
	envApplyConfigs := make([]*acv1alpha1.EnvironmentApplyConfiguration, 0)

	for _, env := range ps.Spec.Environments {
		// Only clean up environments that were configured in the TimedCommitStatus
		if timedBranches[env.Branch] {
			// Create environment config with empty activeCommitStatuses to remove the timer check
			// Server-side apply with our field owner will remove our managed field
			envApply := acv1alpha1.Environment().
				WithBranch(env.Branch).
				WithActiveCommitStatuses() // Empty slice removes the field we manage

			envApplyConfigs = append(envApplyConfigs, envApply)
		}
	}

	// Only apply if we have environments to clean up
	if len(envApplyConfigs) > 0 {
		specApply = specApply.WithEnvironments(envApplyConfigs...)
		psApply = psApply.WithSpec(specApply)

		// Apply using Server-Side Apply - this will remove the fields managed by our field owner
		if err := r.Patch(ctx, ps, utils.ApplyPatch{ApplyConfig: psApply}, client.FieldOwner(constants.TimedCommitStatusControllerFieldOwner)); err != nil {
			return fmt.Errorf("failed to cleanup timer check from PromotionStrategy: %w", err)
		}

		logger.Info("Cleaned up timer check from PromotionStrategy environments",
			"promotionStrategy", ps.Name,
			"cleanedEnvironments", len(envApplyConfigs))
	}

	return nil
}

// touchChangeTransferPolicies triggers reconciliation of the ChangeTransferPolicies
// for the environments that had time gates transition to success.
// This triggers the ChangeTransferPolicy controller to reconcile and potentially merge PRs.
func (r *TimedCommitStatusReconciler) touchChangeTransferPolicies(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, transitionedEnvironments []string) {
	logger := log.FromContext(ctx)

	// For each transitioned environment, trigger reconciliation of the corresponding ChangeTransferPolicy
	for _, envBranch := range transitionedEnvironments {
		// Generate the ChangeTransferPolicy name using the same logic as the PromotionStrategy controller
		ctpName := utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(ps.Name, envBranch))

		logger.Info("Triggering ChangeTransferPolicy reconciliation due to time gate transition",
			"changeTransferPolicy", ctpName,
			"branch", envBranch)

		// Use the enqueue function to trigger reconciliation.
		if r.EnqueueCTP != nil {
			r.EnqueueCTP(ps.Namespace, ctpName)
		}
	}
}

// calculateRequeueDuration determines when to requeue based on whether there are pending time gates.
// If there are pending time gates where the duration has not been met, requeue every 1 minute for regular status updates.
// If there are pending time gates where the duration has been met (waiting for open PR to merge), use the default duration.
// Otherwise, use the default requeue duration from settings.
func (r *TimedCommitStatusReconciler) calculateRequeueDuration(ctx context.Context, tcs *promoterv1alpha1.TimedCommitStatus) time.Duration {
	logger := log.FromContext(ctx)

	// Check if there are any pending time gates and whether their duration has been met
	hasPendingGatesNotMet := false

	for _, envStatus := range tcs.Status.Environments {
		if envStatus.Phase == string(promoterv1alpha1.CommitPhasePending) {
			// Check if there is still time remaining before the gate is satisfied
			if envStatus.AtMostDurationRemaining.Duration > 0 {
				hasPendingGatesNotMet = true
				break
			}
		}
	}

	// If there are pending gates where duration hasn't been met, requeue every minute for regular status updates
	if hasPendingGatesNotMet {
		logger.V(4).Info("Requeuing in 1 minute due to pending time gates with unmet duration")
		return time.Minute
	}

	// Otherwise use the default requeue duration
	defaultDuration, err := settings.GetRequeueDuration[promoterv1alpha1.TimedCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		logger.Error(err, "failed to get default requeue duration, using 1 hour")
		return time.Hour
	}

	return defaultDuration
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
