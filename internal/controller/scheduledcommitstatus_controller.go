/*
Copyright 2026.

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
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// cronParser parses standard 5-field cron expressions (minute hour dom month dow).
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ScheduledCommitStatusReconciler reconciles a ScheduledCommitStatus object.
type ScheduledCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager
	EnqueueCTP  CTPEnqueueFunc
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scheduledcommitstatuses,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scheduledcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scheduledcommitstatuses/finalizers,verbs=update
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses,verbs=get;list;watch;patch;create;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch

// Reconcile handles a single ScheduledCommitStatus resource.
//
//nolint:dupl // Gate controllers share the same reconciliation skeleton by design.
func (r *ScheduledCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ScheduledCommitStatus")
	startTime := time.Now()

	var scs promoterv1alpha1.ScheduledCommitStatus
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &scs, r.Client, r.Recorder, constants.ScheduledCommitStatusControllerFieldOwner, &result, &err, &previousReady)

	err = r.Get(ctx, req.NamespacedName, &scs, &client.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("ScheduledCommitStatus not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get ScheduledCommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get ScheduledCommitStatus %q: %w", req.Name, err)
	}

	previousReady = utils.RemoveReadyCondition(&scs)

	if err := ensureControllerInstanceIDStable(ctx, r.SettingsMgr); err != nil {
		return ctrl.Result{}, err
	}

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

	transitionedEnvironments, commitStatuses, err := r.processEnvironments(ctx, &scs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to process environments: %w", err)
	}

	err = utils.CleanupOrphanedCommitStatuses(ctx, r.Client, r.Recorder, &scs, commitStatuses)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to cleanup orphaned CommitStatus resources: %w", err)
	}

	utils.InheritNotReadyConditionFromObjects(&scs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	utils.EnqueueChangeTransferPolicies(ctx, r.EnqueueCTP, &ps, transitionedEnvironments, "scheduled window transition")

	requeueDuration := r.calculateRequeueDuration(ctx, &scs)

	return ctrl.Result{
		RequeueAfter: requeueDuration,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
//
//nolint:dupl // Gate controllers share the same SetupWithManager skeleton by design.
func (r *ScheduledCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
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
		Named("scheduledcommitstatus").
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

func (r *ScheduledCommitStatusReconciler) processEnvironments(ctx context.Context, scs *promoterv1alpha1.ScheduledCommitStatus, ps *promoterv1alpha1.PromotionStrategy) ([]string, []*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	transitionedEnvironments := []string{}
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(scs.Spec.Environments))

	previousPhases := make(map[string]string, len(scs.Status.Environments))
	for _, env := range scs.Status.Environments {
		previousPhases[env.Branch] = env.Phase
	}

	psBranches := make(map[string]bool, len(ps.Spec.Environments))
	for _, env := range ps.Spec.Environments {
		psBranches[env.Branch] = true
	}
	var mismatchedBranches []string
	for _, envConfig := range scs.Spec.Environments {
		if !psBranches[envConfig.Branch] {
			mismatchedBranches = append(mismatchedBranches, envConfig.Branch)
		}
	}
	if len(mismatchedBranches) > 0 {
		return nil, nil, fmt.Errorf("branches not found in PromotionStrategy %q: %s",
			ps.Name, strings.Join(mismatchedBranches, ", "))
	}

	envStatusMap := make(map[string]*promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for i := range ps.Status.Environments {
		envStatusMap[ps.Status.Environments[i].Branch] = &ps.Status.Environments[i]
	}

	scs.Status.Environments = make([]promoterv1alpha1.ScheduledEnvironmentStatus, 0, len(scs.Spec.Environments))

	now := time.Now()

	for _, envConfig := range scs.Spec.Environments {
		currentEnvStatus, found := envStatusMap[envConfig.Branch]
		if !found {
			logger.Info("Environment not found in PromotionStrategy status", "branch", envConfig.Branch)
			continue
		}

		proposedSha := currentEnvStatus.Proposed.Hydrated.Sha
		if proposedSha == "" {
			logger.Info("No proposed hydrated commit in current environment", "branch", envConfig.Branch)
			continue
		}

		allow := mergeWindows(scs.Spec.Allow, envConfig.Allow)
		exclude := mergeWindows(scs.Spec.Exclude, envConfig.Exclude)
		result, err := evaluateWindows(now, allow, exclude, scs.Spec.Timezone)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to evaluate windows for environment %q: %w", envConfig.Branch, err)
		}

		phase := result.Phase
		message := result.Message

		previousPhase := previousPhases[envConfig.Branch]
		if previousPhase != string(promoterv1alpha1.CommitPhaseSuccess) && phase == promoterv1alpha1.CommitPhaseSuccess {
			transitionedEnvironments = append(transitionedEnvironments, envConfig.Branch)
			logger.Info("Scheduled window transitioned to success",
				"branch", envConfig.Branch,
				"sha", proposedSha)
		}

		envWindowStatus := promoterv1alpha1.ScheduledEnvironmentStatus{
			Branch: envConfig.Branch,
			Sha:    proposedSha,
			Phase:  string(phase),
		}
		if result.Active != nil {
			ws := &promoterv1alpha1.WindowStatus{
				Allow:   result.Active.Allow,
				Exclude: result.Active.Exclude,
			}
			if result.Active.Transition != nil {
				ws.Transition = &metav1.Time{Time: result.Active.Transition.UTC()}
			}
			envWindowStatus.Active = ws
		}
		if result.Next != nil {
			ws := &promoterv1alpha1.WindowStatus{
				Allow:   result.Next.Allow,
				Exclude: result.Next.Exclude,
			}
			if result.Next.Transition != nil {
				ws.Transition = &metav1.Time{Time: result.Next.Transition.UTC()}
			}
			envWindowStatus.Next = ws
		}
		scs.Status.Environments = append(scs.Status.Environments, envWindowStatus)

		cs, err := utils.UpsertCommitStatus(ctx, r.Client, utils.UpsertCommitStatusParams{
			Parent:      scs,
			RepoRefName: ps.Spec.RepositoryReference.Name,
			Branch:      envConfig.Branch,
			Sha:         proposedSha,
			Key:         scs.Spec.Key,
			Description: message,
			Phase:       phase,
			FieldOwner:  constants.ScheduledCommitStatusControllerFieldOwner,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to upsert CommitStatus for environment %q: %w", envConfig.Branch, err)
		}
		commitStatuses = append(commitStatuses, cs)

		emitCommitStatusPhaseChangedEvent(r.Recorder, scs, scs.Spec.Key, envConfig.Branch, previousPhase, string(phase))

		logger.Info("Processed environment scheduled window",
			"branch", envConfig.Branch,
			"proposedSha", proposedSha,
			"phase", phase,
			"active", result.Active,
			"next", result.Next)
	}

	return transitionedEnvironments, commitStatuses, nil
}

func (r *ScheduledCommitStatusReconciler) calculateRequeueDuration(ctx context.Context, scs *promoterv1alpha1.ScheduledCommitStatus) time.Duration {
	logger := log.FromContext(ctx)

	var earliest *time.Time
	for i := range scs.Status.Environments {
		env := &scs.Status.Environments[i]
		if env.Active != nil && env.Active.Transition != nil {
			t := env.Active.Transition.Time
			if earliest == nil || t.Before(*earliest) {
				earliest = &t
			}
		}
		if env.Next != nil && env.Next.Transition != nil {
			t := env.Next.Transition.Time
			if earliest == nil || t.Before(*earliest) {
				earliest = &t
			}
		}
	}

	if earliest != nil {
		duration := time.Until(*earliest)
		if duration <= 0 {
			return time.Second
		}
		return duration + time.Second
	}

	defaultDuration, err := settings.GetRequeueDuration[promoterv1alpha1.ScheduledCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		logger.Error(err, "failed to get default requeue duration, using 1 hour")
		return time.Hour
	}

	return defaultDuration
}

func (r *ScheduledCommitStatusReconciler) enqueueScheduledCommitStatusForPromotionStrategy() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
		if !ok {
			return nil
		}

		var scsList promoterv1alpha1.ScheduledCommitStatusList
		if err := r.List(ctx, &scsList,
			client.InNamespace(ps.Namespace),
			client.MatchingFields{PromotionStrategyRefField: ps.Name},
		); err != nil {
			log.FromContext(ctx).Error(err, "failed to list ScheduledCommitStatus resources")
			return nil
		}

		requests := make([]ctrl.Request, 0, len(scsList.Items))
		for i := range scsList.Items {
			requests = append(requests, ctrl.Request{
				NamespacedName: client.ObjectKeyFromObject(&scsList.Items[i]),
			})
		}

		return requests
	})
}

// windowEvalResult holds the result of evaluating scheduled windows for an environment.
type windowEvalResult struct {
	Active  *windowInfo
	Next    *windowInfo
	Phase   promoterv1alpha1.CommitStatusPhase
	Message string
}

// windowInfo describes a single window state (active or upcoming).
type windowInfo struct {
	Transition *time.Time
	Allow      string
	Exclude    string
}

// transitionCandidate tracks a potential future transition with its source cron.
type transitionCandidate struct {
	time    time.Time
	allow   string
	exclude string
}

// mergeWindows combines global and per-environment cron windows.
func mergeWindows(global, local []promoterv1alpha1.CronWindow) []promoterv1alpha1.CronWindow {
	if len(global) == 0 {
		return local
	}
	if len(local) == 0 {
		return global
	}
	merged := make([]promoterv1alpha1.CronWindow, 0, len(global)+len(local))
	merged = append(merged, global...)
	merged = append(merged, local...)
	return merged
}

// resolveTimezone returns the window's timezone if set, otherwise the default.
func resolveTimezone(w promoterv1alpha1.CronWindow, defaultTZ string) string {
	if w.Timezone != "" {
		return w.Timezone
	}
	return defaultTZ
}

// evaluateWindows determines if the current time falls within any allow/exclude window.
// Each window may override the default timezone; cron expressions are evaluated in the resolved timezone.
func evaluateWindows(now time.Time, allow, exclude []promoterv1alpha1.CronWindow, defaultTimezone string) (windowEvalResult, error) {
	var nextTransitions []transitionCandidate

	// Check exclusions first — they override everything.
	// Scan all exclusion windows and track the latest-ending active one so that
	// overlapping exclusions report the correct transition time.
	var latestExclEnd time.Time
	var latestExclCron string
	exclActive := false
	for _, excl := range exclude {
		if excl.Duration.Duration <= 0 {
			return windowEvalResult{}, fmt.Errorf("exclude cron %q has non-positive duration %s", excl.Cron, excl.Duration.Duration)
		}
		sched, err := cronParser.Parse(excl.Cron)
		if err != nil {
			return windowEvalResult{}, fmt.Errorf("invalid exclude cron %q: %w", excl.Cron, err)
		}

		loc, err := time.LoadLocation(resolveTimezone(excl, defaultTimezone))
		if err != nil {
			return windowEvalResult{}, fmt.Errorf("invalid timezone %q for exclude cron %q: %w", resolveTimezone(excl, defaultTimezone), excl.Cron, err)
		}
		localNow := now.In(loc)

		active, windowStart := isInsideCronWindow(localNow, sched, excl.Duration.Duration)
		if active {
			windowEnd := windowStart.Add(excl.Duration.Duration)
			if !exclActive || windowEnd.After(latestExclEnd) {
				latestExclEnd = windowEnd
				latestExclCron = excl.Cron
			}
			exclActive = true
		} else if nextStart := sched.Next(localNow); !nextStart.IsZero() {
			nextTransitions = append(nextTransitions, transitionCandidate{time: nextStart, exclude: excl.Cron})
		}
	}
	if exclActive {
		return windowEvalResult{
			Phase:   promoterv1alpha1.CommitPhasePending,
			Message: fmt.Sprintf("Promotion blocked by exclusion window (cron: %s)", latestExclCron),
			Active:  &windowInfo{Exclude: latestExclCron, Transition: &latestExclEnd},
			Next:    &windowInfo{Exclude: latestExclCron, Transition: &latestExclEnd},
		}, nil
	}

	// If no allow windows defined, this is exclusion-only mode — allow when not excluded.
	if len(allow) == 0 {
		result := windowEvalResult{
			Phase:   promoterv1alpha1.CommitPhaseSuccess,
			Message: "Promotion allowed (no active exclusion)",
		}
		if tc := earliestTransition(nextTransitions); tc != nil {
			result.Next = &windowInfo{Exclude: tc.exclude, Transition: &tc.time}
		}
		return result, nil
	}

	// Check allow windows — any open window is sufficient (OR semantics).
	// Scan all allow windows and track the latest-ending active one so that
	// overlapping allows report the correct transition time.
	var latestAllowEnd time.Time
	var latestAllowCron string
	allowActive := false
	var nearestWindowStart *time.Time
	var nearestWindowCron string
	for _, w := range allow {
		if w.Duration.Duration <= 0 {
			return windowEvalResult{}, fmt.Errorf("allow cron %q has non-positive duration %s", w.Cron, w.Duration.Duration)
		}
		sched, err := cronParser.Parse(w.Cron)
		if err != nil {
			return windowEvalResult{}, fmt.Errorf("invalid allow cron %q: %w", w.Cron, err)
		}

		loc, err := time.LoadLocation(resolveTimezone(w, defaultTimezone))
		if err != nil {
			return windowEvalResult{}, fmt.Errorf("invalid timezone %q for allow cron %q: %w", resolveTimezone(w, defaultTimezone), w.Cron, err)
		}
		localNow := now.In(loc)

		active, windowStart := isInsideCronWindow(localNow, sched, w.Duration.Duration)
		if active {
			windowEnd := windowStart.Add(w.Duration.Duration)
			if !allowActive || windowEnd.After(latestAllowEnd) {
				latestAllowEnd = windowEnd
				latestAllowCron = w.Cron
			}
			allowActive = true
			nextTransitions = append(nextTransitions, transitionCandidate{time: windowEnd, allow: w.Cron})
		} else if nextStart := sched.Next(localNow); !nextStart.IsZero() {
			if nearestWindowStart == nil || nextStart.Before(*nearestWindowStart) {
				nearestWindowStart = &nextStart
				nearestWindowCron = w.Cron
			}
		}
	}
	if allowActive {
		tc := earliestTransition(nextTransitions)
		result := windowEvalResult{
			Phase:   promoterv1alpha1.CommitPhaseSuccess,
			Message: fmt.Sprintf("Promotion allowed (inside window: cron %s)", latestAllowCron),
			Active:  &windowInfo{Allow: latestAllowCron, Transition: &latestAllowEnd},
		}
		if tc != nil {
			result.Next = &windowInfo{Allow: tc.allow, Exclude: tc.exclude, Transition: &tc.time}
		}
		return result, nil
	}

	// Outside all allow windows.
	result := windowEvalResult{
		Phase:   promoterv1alpha1.CommitPhasePending,
		Message: "Promotion blocked (outside all allow windows)",
	}
	if nearestWindowStart != nil {
		nextTransitions = append(nextTransitions, transitionCandidate{time: *nearestWindowStart, allow: nearestWindowCron})
	}
	if tc := earliestTransition(nextTransitions); tc != nil {
		result.Next = &windowInfo{Allow: tc.allow, Exclude: tc.exclude, Transition: &tc.time}
	}
	return result, nil
}

// isInsideCronWindow checks whether now falls inside a cron-triggered window of the given duration.
// Returns (true, latestWindowStart) if inside, (false, zero) otherwise.
//
// Walk forward from (now - duration) collecting every cron occurrence ≤ now whose
// window still covers now. Return the latest one so that the active window's
// transition time (windowEnd) is as far in the future as possible, avoiding spurious reconciliation churn for
// high-frequency crons with long durations (e.g. "* * * * *" with 2h duration).
func isInsideCronWindow(now time.Time, sched cron.Schedule, duration time.Duration) (bool, time.Time) {
	maxIterations := int(duration.Minutes()) + 1
	if maxIterations > 525_600 {
		maxIterations = 525_600
	}
	checkpoint := now.Add(-duration)
	candidate := sched.Next(checkpoint)

	var latest time.Time
	found := false
	for i := 0; !candidate.IsZero() && !candidate.After(now) && i < maxIterations; i++ {
		if candidate.Add(duration).After(now) {
			latest = candidate
			found = true
		}
		next := sched.Next(candidate)
		if !next.After(candidate) {
			break
		}
		candidate = next
	}
	return found, latest
}

// earliestTransition returns a copy of the candidate with the earliest time, or nil if empty.
func earliestTransition(candidates []transitionCandidate) *transitionCandidate {
	if len(candidates) == 0 {
		return nil
	}
	earliest := candidates[0]
	for _, c := range candidates[1:] {
		if c.time.Before(earliest.time) {
			earliest = c
		}
	}
	return &earliest
}
