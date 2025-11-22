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
	"github.com/robfig/cron/v3"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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

// ScheduledCommitStatusReconciler reconciles a ScheduledCommitStatus object
type ScheduledCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	SettingsMgr *settings.Manager
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scheduledcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scheduledcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scheduledcommitstatuses/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *ScheduledCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ScheduledCommitStatus")
	startTime := time.Now()

	var scs promoterv1alpha1.ScheduledCommitStatus
	defer utils.HandleReconciliationResult(ctx, startTime, &scs, r.Client, r.Recorder, &err)

	// 1. Fetch the ScheduledCommitStatus instance
	err = r.Get(ctx, req.NamespacedName, &scs, &client.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("ScheduledCommitStatus not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get ScheduledCommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get ScheduledCommitStatus %q: %w", req.Name, err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	meta.RemoveStatusCondition(scs.GetConditions(), string(promoterConditions.Ready))

	// 2. Fetch the referenced PromotionStrategy
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{
		Namespace: scs.Namespace,
		Name:      scs.Spec.PromotionStrategyRef.Name,
	}
	err = r.Get(ctx, psKey, &ps)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Error(err, "referenced PromotionStrategy not found", "promotionStrategy", scs.Spec.PromotionStrategyRef.Name)
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", scs.Spec.PromotionStrategyRef.Name, err)
		}
		logger.Error(err, "failed to get PromotionStrategy")
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", scs.Spec.PromotionStrategyRef.Name, err)
	}

	// 3. Process each environment defined in the ScheduledCommitStatus
	commitStatuses, err := r.processEnvironments(ctx, &scs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to process environments: %w", err)
	}

	// 4. Inherit conditions from CommitStatus objects
	utils.InheritNotReadyConditionFromObjects(&scs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	// 5. Update status
	err = r.Status().Update(ctx, &scs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ScheduledCommitStatus status: %w", err)
	}

	// 6. Calculate smart requeue duration based on next window boundaries
	requeueDuration := r.calculateRequeueDuration(ctx, &scs)

	return ctrl.Result{
		RequeueAfter: requeueDuration,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ScheduledCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.ScheduledCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get ScheduledCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.ScheduledCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get ScheduledCommitStatus max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.ScheduledCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.PromotionStrategy{}, r.enqueueScheduledCommitStatusForPromotionStrategy()).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// processEnvironments processes each environment defined in the ScheduledCommitStatus spec,
// creating or updating CommitStatus resources based on schedule-based rules.
// The logic is: for each environment, check if the current time is within the configured deployment window.
// If so, report success for the proposed hydrated SHA. If not, report pending.
// This allows using the scheduled commit status as a proposed commit status gate to block promotions
// when the current time is outside the deployment window.
// Returns the CommitStatus objects created/updated.
func (r *ScheduledCommitStatusReconciler) processEnvironments(ctx context.Context, scs *promoterv1alpha1.ScheduledCommitStatus, ps *promoterv1alpha1.PromotionStrategy) ([]*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	// Track all CommitStatus objects created/updated
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(scs.Spec.Environments))

	// Build a map of environments from PromotionStrategy for efficient lookup
	envStatusMap := make(map[string]*promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for i := range ps.Status.Environments {
		envStatusMap[ps.Status.Environments[i].Branch] = &ps.Status.Environments[i]
	}

	// Initialize or clear the environments status
	scs.Status.Environments = make([]promoterv1alpha1.ScheduledCommitStatusEnvironmentStatus, 0, len(scs.Spec.Environments))

	for _, envConfig := range scs.Spec.Environments {
		// Look up the environment in the map
		envStatus, found := envStatusMap[envConfig.Branch]
		if !found {
			logger.Info("Environment not found in PromotionStrategy status", "branch", envConfig.Branch)
			continue
		}

		// Get the proposed hydrated SHA
		proposedHydratedSha := envStatus.Proposed.Hydrated.Sha

		if proposedHydratedSha == "" {
			logger.Info("No proposed hydrated commit in environment", "branch", envConfig.Branch)
			continue
		}

		// Parse the schedule configuration
		windowInfo, err := r.calculateWindowInfo(ctx, envConfig.Schedule)
		if err != nil {
			logger.Error(err, "failed to calculate window info", "branch", envConfig.Branch)
			// Continue with other environments even if one fails
			continue
		}

		// Determine commit status phase based on whether we're in the deployment window
		phase, message := r.calculateCommitStatusPhase(windowInfo.InWindow, envConfig.Branch)

		// Update status for this environment
		envScheduledStatus := promoterv1alpha1.ScheduledCommitStatusEnvironmentStatus{
			Branch:            envConfig.Branch,
			Sha:               proposedHydratedSha,
			Phase:             string(phase),
			CurrentlyInWindow: windowInfo.InWindow,
			NextWindowStart:   windowInfo.NextWindowStart,
			NextWindowEnd:     windowInfo.NextWindowEnd,
		}
		scs.Status.Environments = append(scs.Status.Environments, envScheduledStatus)

		// Create or update the CommitStatus for the proposed hydrated SHA
		// This acts as a proposed commit status that gates promotions to this environment
		cs, err := r.upsertCommitStatus(ctx, scs, ps, envConfig.Branch, proposedHydratedSha, phase, message)
		if err != nil {
			return nil, fmt.Errorf("failed to upsert CommitStatus for environment %q: %w", envConfig.Branch, err)
		}
		commitStatuses = append(commitStatuses, cs)

		logger.Info("Processed environment schedule gate",
			"branch", envConfig.Branch,
			"proposedSha", proposedHydratedSha,
			"phase", phase,
			"inWindow", windowInfo.InWindow,
			"nextWindowStart", windowInfo.NextWindowStart,
			"nextWindowEnd", windowInfo.NextWindowEnd)
	}

	return commitStatuses, nil
}

// WindowInfo contains information about the deployment window
type WindowInfo struct {
	NextWindowStart *metav1.Time
	NextWindowEnd   *metav1.Time
	InWindow        bool
}

// calculateWindowInfo determines if the current time is within a deployment window
// and calculates the next window start and end times.
func (r *ScheduledCommitStatusReconciler) calculateWindowInfo(ctx context.Context, schedule promoterv1alpha1.Schedule) (*WindowInfo, error) {
	logger := log.FromContext(ctx)

	// Parse the window duration
	windowDuration, err := time.ParseDuration(schedule.Window)
	if err != nil {
		return nil, fmt.Errorf("failed to parse window duration %q: %w", schedule.Window, err)
	}

	// Load the timezone
	timezone := schedule.Timezone
	if timezone == "" {
		timezone = "UTC"
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("failed to load timezone %q: %w", timezone, err)
	}

	// Parse the cron expression
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	cronSchedule, err := parser.Parse(schedule.Cron)
	if err != nil {
		return nil, fmt.Errorf("failed to parse cron expression %q: %w", schedule.Cron, err)
	}

	// Get current time in the configured timezone
	now := time.Now().In(loc)

	// Find the most recent window start (could be in the past or now)
	// We need to check if we're currently in a window
	// Start by finding the next occurrence from a point in the past
	checkTime := now.Add(-windowDuration) // Go back by window duration to catch current window
	lastWindowStart := cronSchedule.Next(checkTime)

	// Check if we're currently in a window
	inWindow := false
	var nextWindowStart, nextWindowEnd time.Time

	if now.After(lastWindowStart) && now.Before(lastWindowStart.Add(windowDuration)) {
		// We're in the current window
		inWindow = true
		nextWindowStart = lastWindowStart
		nextWindowEnd = lastWindowStart.Add(windowDuration)
	} else {
		// We're not in a window, find the next one
		inWindow = false
		nextWindowStart = cronSchedule.Next(now)
		nextWindowEnd = nextWindowStart.Add(windowDuration)
	}

	logger.V(4).Info("Calculated window info",
		"now", now,
		"inWindow", inWindow,
		"nextWindowStart", nextWindowStart,
		"nextWindowEnd", nextWindowEnd,
		"timezone", timezone)

	return &WindowInfo{
		InWindow:        inWindow,
		NextWindowStart: &metav1.Time{Time: nextWindowStart},
		NextWindowEnd:   &metav1.Time{Time: nextWindowEnd},
	}, nil
}

// calculateCommitStatusPhase determines the commit status phase based on whether we're in a deployment window
func (r *ScheduledCommitStatusReconciler) calculateCommitStatusPhase(inWindow bool, envBranch string) (promoterv1alpha1.CommitStatusPhase, string) {
	if inWindow {
		// We're in the deployment window
		return promoterv1alpha1.CommitPhaseSuccess, fmt.Sprintf("Deployment window is open for %s environment", envBranch)
	}

	// We're outside the deployment window
	return promoterv1alpha1.CommitPhasePending, fmt.Sprintf("Deployment window is closed for %s environment", envBranch)
}

func (r *ScheduledCommitStatusReconciler) upsertCommitStatus(ctx context.Context, scs *promoterv1alpha1.ScheduledCommitStatus, ps *promoterv1alpha1.PromotionStrategy, branch, sha string, phase promoterv1alpha1.CommitStatusPhase, message string) (*promoterv1alpha1.CommitStatus, error) {
	// Generate a consistent name for the CommitStatus
	commitStatusName := utils.KubeSafeUniqueName(ctx, fmt.Sprintf("%s-%s-scheduled", scs.Name, branch))

	commitStatus := promoterv1alpha1.CommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      commitStatusName,
			Namespace: scs.Namespace,
		},
	}

	// Create or update the CommitStatus
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, &commitStatus, func() error {
		// Set owner reference to the ScheduledCommitStatus
		if err := ctrl.SetControllerReference(scs, &commitStatus, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference: %w", err)
		}

		// Set labels for easy identification
		if commitStatus.Labels == nil {
			commitStatus.Labels = make(map[string]string)
		}
		commitStatus.Labels["promoter.argoproj.io/scheduled-commit-status"] = utils.KubeSafeLabel(scs.Name)
		commitStatus.Labels[promoterv1alpha1.EnvironmentLabel] = utils.KubeSafeLabel(branch)
		commitStatus.Labels[promoterv1alpha1.CommitStatusLabel] = "schedule"

		// Set the spec
		commitStatus.Spec.RepositoryReference = ps.Spec.RepositoryReference
		// Use the environment branch name to show which environment's schedule gate this is checking
		commitStatus.Spec.Name = "schedule/" + branch
		commitStatus.Spec.Description = message
		commitStatus.Spec.Phase = phase
		commitStatus.Spec.Sha = sha

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create or update CommitStatus: %w", err)
	}

	return &commitStatus, nil
}

// calculateRequeueDuration determines when to requeue based on the next window boundary.
// If currently in a window, requeue shortly after the window ends.
// If outside a window, requeue shortly before the next window starts.
// This provides timely status updates when windows open or close.
func (r *ScheduledCommitStatusReconciler) calculateRequeueDuration(ctx context.Context, scs *promoterv1alpha1.ScheduledCommitStatus) time.Duration {
	logger := log.FromContext(ctx)

	var nextBoundary time.Time
	hasValidBoundary := false

	// Find the earliest next boundary (either window start or end) across all environments
	for _, envStatus := range scs.Status.Environments {
		if envStatus.CurrentlyInWindow {
			// We're in a window, requeue when it ends
			if envStatus.NextWindowEnd != nil {
				endTime := envStatus.NextWindowEnd.Time
				if !hasValidBoundary || endTime.Before(nextBoundary) {
					nextBoundary = endTime
					hasValidBoundary = true
				}
			}
		} else {
			// We're outside a window, requeue when the next one starts
			if envStatus.NextWindowStart != nil {
				startTime := envStatus.NextWindowStart.Time
				if !hasValidBoundary || startTime.Before(nextBoundary) {
					nextBoundary = startTime
					hasValidBoundary = true
				}
			}
		}
	}

	if hasValidBoundary {
		// Calculate duration until the boundary, add 1 minute buffer for safety
		durationUntilBoundary := time.Until(nextBoundary)
		if durationUntilBoundary < 0 {
			// Boundary is in the past, requeue immediately
			logger.V(4).Info("Next boundary is in the past, requeuing immediately")
			return time.Minute
		}

		// Add 1 minute buffer to ensure we're past the boundary
		requeueDuration := durationUntilBoundary + time.Minute

		// Cap at a reasonable maximum (e.g., 24 hours) to ensure periodic reconciliation
		maxDuration := 24 * time.Hour
		if requeueDuration > maxDuration {
			logger.V(4).Info("Capping requeue duration at 24 hours", "calculated", requeueDuration)
			return maxDuration
		}

		logger.V(4).Info("Requeuing at next window boundary",
			"boundary", nextBoundary,
			"duration", requeueDuration)
		return requeueDuration
	}

	// No valid boundary found, use default requeue duration
	defaultDuration, err := settings.GetRequeueDuration[promoterv1alpha1.ScheduledCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		logger.Error(err, "failed to get default requeue duration, using 1 hour")
		return time.Hour
	}

	return defaultDuration
}

// enqueueScheduledCommitStatusForPromotionStrategy returns a handler that enqueues all ScheduledCommitStatus resources
// that reference a PromotionStrategy when that PromotionStrategy changes
func (r *ScheduledCommitStatusReconciler) enqueueScheduledCommitStatusForPromotionStrategy() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
		if !ok {
			return nil
		}

		// List all ScheduledCommitStatus resources in the same namespace
		var scsList promoterv1alpha1.ScheduledCommitStatusList
		if err := r.List(ctx, &scsList, client.InNamespace(ps.Namespace)); err != nil {
			log.FromContext(ctx).Error(err, "failed to list ScheduledCommitStatus resources")
			return nil
		}

		// Enqueue all ScheduledCommitStatus resources that reference this PromotionStrategy
		var requests []ctrl.Request
		for _, scs := range scsList.Items {
			if scs.Spec.PromotionStrategyRef.Name == ps.Name {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKeyFromObject(&scs),
				})
			}
		}

		return requests
	})
}
