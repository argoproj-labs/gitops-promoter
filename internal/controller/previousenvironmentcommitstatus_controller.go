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

	"gopkg.in/yaml.v3"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	acmetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// PreviousEnvironmentCommitStatusReconciler reconciles a PreviousEnvironmentCommitStatus object
type PreviousEnvironmentCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=previousenvironmentcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=previousenvironmentcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=previousenvironmentcommitstatuses/finalizers,verbs=update

// Reconcile reconciles a PreviousEnvironmentCommitStatus: it reads the referenced PromotionStrategy
// and, for each environment, maintains the previous-environment CommitStatus indicating whether the
// preceding environment is synced and healthy.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/reconcile
func (r *PreviousEnvironmentCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := logf.FromContext(ctx)
	logger.Info("Reconciling PreviousEnvironmentCommitStatus")
	startTime := time.Now()

	var pecs promoterv1alpha1.PreviousEnvironmentCommitStatus
	// This function applies the resource status via Server-Side Apply at the end of the reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &pecs, r.Client, r.Recorder, constants.PreviousEnvironmentCommitStatusControllerFieldOwner, &result, &err, &previousReady)

	// 1. Fetch the PreviousEnvironmentCommitStatus instance
	err = r.Get(ctx, req.NamespacedName, &pecs, &client.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("PreviousEnvironmentCommitStatus not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get PreviousEnvironmentCommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get PreviousEnvironmentCommitStatus %q: %w", req.Name, err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	previousReady = utils.RemoveReadyCondition(&pecs)

	// 2. Fetch the referenced PromotionStrategy
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{
		Namespace: pecs.Namespace,
		Name:      pecs.Spec.PromotionStrategyRef.Name,
	}
	err = r.Get(ctx, psKey, &ps)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Error(err, "referenced PromotionStrategy not found", "promotionStrategy", pecs.Spec.PromotionStrategyRef.Name)
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", pecs.Spec.PromotionStrategyRef.Name, err)
		}
		logger.Error(err, "failed to get PromotionStrategy")
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", pecs.Spec.PromotionStrategyRef.Name, err)
	}

	// 3. Maintain the previous-environment CommitStatus for each environment.
	err = r.updatePreviousEnvironmentCommitStatus(ctx, &pecs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update previous environment commit statuses: %w", err)
	}

	// 4. Requeue using the configured requeue duration.
	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.PreviousEnvironmentCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration for PreviousEnvironmentCommitStatus %q: %w", pecs.Name, err)
	}

	return ctrl.Result{
		Requeue:      true,
		RequeueAfter: requeueDuration,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PreviousEnvironmentCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.PreviousEnvironmentCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get PreviousEnvironmentCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.PreviousEnvironmentCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get PreviousEnvironmentCommitStatus max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.PreviousEnvironmentCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.PromotionStrategy{}, r.enqueuePreviousEnvironmentCommitStatusForPromotionStrategy()).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Named("previousenvironmentcommitstatus").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// enqueuePreviousEnvironmentCommitStatusForPromotionStrategy returns a handler that enqueues all
// PreviousEnvironmentCommitStatus resources that reference a PromotionStrategy when that
// PromotionStrategy changes.
func (r *PreviousEnvironmentCommitStatusReconciler) enqueuePreviousEnvironmentCommitStatusForPromotionStrategy() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
		if !ok {
			return nil
		}

		var pecsList promoterv1alpha1.PreviousEnvironmentCommitStatusList
		if err := r.List(ctx, &pecsList, client.InNamespace(ps.Namespace)); err != nil {
			logf.FromContext(ctx).Error(err, "failed to list PreviousEnvironmentCommitStatus resources")
			return nil
		}

		var requests []ctrl.Request
		for i := range pecsList.Items {
			if pecsList.Items[i].Spec.PromotionStrategyRef.Name == ps.Name {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKeyFromObject(&pecsList.Items[i]),
				})
			}
		}

		return requests
	})
}

// updatePreviousEnvironmentCommitStatus goes through each environment and, for environments that have
// active commit statuses configured on the preceding environment, sets the previous-environment
// CommitStatus phase based on whether the preceding environment is synced and healthy.
// It reads everything from the PromotionStrategy status, which aggregates the CTP state.
func (r *PreviousEnvironmentCommitStatusReconciler) updatePreviousEnvironmentCommitStatus(ctx context.Context, pecs *promoterv1alpha1.PreviousEnvironmentCommitStatus, ps *promoterv1alpha1.PromotionStrategy) error {
	logger := logf.FromContext(ctx)

	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(ps.Status.Environments))
	for i := range ps.Status.Environments {
		if i == 0 {
			// Skip, there's no previous environment.
			continue
		}

		if len(ps.Spec.ActiveCommitStatuses) == 0 && len(ps.Spec.Environments[i-1].ActiveCommitStatuses) == 0 {
			// Skip, there aren't any active commit statuses configured for the PromotionStrategy or the previous environment.
			continue
		}

		previousEnvironmentStatus := ps.Status.Environments[i-1]
		currentEnvironmentStatus := ps.Status.Environments[i]

		// Skip if there's no proposed change in the current environment (i.e., active and proposed are the same).
		// In this case, there's no PR to put a commit status on, so we shouldn't create/update one.
		// This prevents updating commit status on already-merged PRs when the previous environment state changes.
		if currentEnvironmentStatus.Active.Dry.Sha == currentEnvironmentStatus.Proposed.Dry.Sha {
			logger.V(4).Info("Skipping previous environment commit status update - no proposed change in current environment",
				"activeBranch", currentEnvironmentStatus.Branch,
				"activeDrySha", currentEnvironmentStatus.Active.Dry.Sha,
				"proposedDrySha", currentEnvironmentStatus.Proposed.Dry.Sha,
				"previousEnvironmentActiveDrySha", previousEnvironmentStatus.Active.Dry.Sha,
			)
			continue
		}

		// Determine which dry SHA the current environment's hydrator has processed.
		// The Note.DrySha (from git note) is the authoritative source because when manifests don't change
		// between dry commits, the hydrator may only update the git note without creating a new commit.
		// For legacy hydrators that don't use git notes, fall back to Proposed.Dry.Sha.
		currentEnvHydratedForDrySha := getEffectiveHydratedDrySha(currentEnvironmentStatus)

		// Pass all preceding environment statuses so we can look back past no-op hydrations.
		precedingEnvStatuses := ps.Status.Environments[:i]

		// Recursively check ALL preceding environments to:
		// 1. Check that each has been hydrated for the same dry SHA
		// 2. Find the first environment that actually deployed this change (not a no-op)
		// 3. Check that environment's commit statuses
		isPending, pendingReason := isPreviousEnvironmentPending(precedingEnvStatuses, currentEnvHydratedForDrySha, currentEnvironmentStatus.Active.Dry.CommitTime)

		commitStatusPhase := promoterv1alpha1.CommitPhaseSuccess
		if isPending {
			commitStatusPhase = promoterv1alpha1.CommitPhasePending
		}

		logger.V(4).Info("Setting previous environment CommitStatus phase",
			"phase", commitStatusPhase,
			"pendingReason", pendingReason,
			"activeBranch", currentEnvironmentStatus.Branch,
			"proposedDrySha", currentEnvironmentStatus.Proposed.Dry.Sha,
			"proposedHydratedSha", currentEnvironmentStatus.Proposed.Hydrated.Sha,
			"previousEnvironmentActiveDrySha", previousEnvironmentStatus.Active.Dry.Sha,
			"previousEnvironmentActiveBranch", previousEnvironmentStatus.Branch)

		cs, err := r.createOrUpdatePreviousEnvironmentCommitStatus(ctx, pecs, ps,
			currentEnvironmentStatus.Branch,
			currentEnvironmentStatus.Proposed.Hydrated.Sha,
			commitStatusPhase,
			pendingReason,
			previousEnvironmentInfo{
				branch:         previousEnvironmentStatus.Branch,
				commitStatuses: previousEnvironmentStatus.Active.CommitStatuses,
			})
		if err != nil {
			return fmt.Errorf("failed to create or update previous environment commit status for branch %s: %w", currentEnvironmentStatus.Branch, err)
		}
		commitStatuses = append(commitStatuses, cs)
	}

	utils.InheritNotReadyConditionFromObjects(pecs, promoterConditions.PreviousEnvironmentCommitStatusNotReady, commitStatuses...)

	return nil
}

// previousEnvironmentInfo holds the preceding environment's data used to build the
// previous-environment CommitStatus (its branch and its active commit statuses).
type previousEnvironmentInfo struct {
	// branch is the branch of the preceding environment being checked.
	branch string
	// commitStatuses are the preceding environment's active commit statuses, aggregated into the annotation.
	commitStatuses []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase
}

// createOrUpdatePreviousEnvironmentCommitStatus creates or updates the previous-environment CommitStatus
// for a single environment. The CommitStatus is attached to the current environment's proposed
// hydrated SHA and is owned by the PreviousEnvironmentCommitStatus CR.
func (r *PreviousEnvironmentCommitStatusReconciler) createOrUpdatePreviousEnvironmentCommitStatus(
	ctx context.Context,
	pecs *promoterv1alpha1.PreviousEnvironmentCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	currentBranch string,
	proposedHydratedSha string,
	phase promoterv1alpha1.CommitStatusPhase,
	pendingReason string,
	previousEnv previousEnvironmentInfo,
) (*promoterv1alpha1.CommitStatus, error) {
	logger := logf.FromContext(ctx)

	key := pecs.Spec.Key
	commitStatusName := utils.CommitStatusResourceName(ctx, pecs, currentBranch)

	kind := reflect.TypeOf(promoterv1alpha1.PreviousEnvironmentCommitStatus{}).Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)

	// If there is only one commit status, use the URL from that commit status.
	var url string
	if len(previousEnv.commitStatuses) == 1 {
		url = previousEnv.commitStatuses[0].Url
	}

	statusMap := make(map[string]string)
	for _, status := range previousEnv.commitStatuses {
		statusMap[status.Key] = status.Phase
	}
	yamlStatusMap, err := yaml.Marshal(statusMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal previous environment commit statuses: %w", err)
	}

	description := previousEnv.branch + " - synced and healthy"
	if phase == promoterv1alpha1.CommitPhasePending && pendingReason != "" {
		description = pendingReason
	}

	labels := utils.CommitStatusStandardLabels(pecs, currentBranch, key)

	// Build the apply configuration
	commitStatusApply := acv1alpha1.CommitStatus(commitStatusName, pecs.Namespace).
		WithLabels(labels).
		WithAnnotations(map[string]string{
			promoterv1alpha1.CommitStatusPreviousEnvironmentStatusesAnnotation: string(yamlStatusMap),
		}).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(pecs.Name).
			WithUID(pecs.UID).
			WithController(true).
			WithBlockOwnerDeletion(true)).
		WithSpec(acv1alpha1.CommitStatusSpec().
			WithRepositoryReference(acv1alpha1.ObjectReference().
				WithName(ps.Spec.RepositoryReference.Name)).
			WithSha(proposedHydratedSha).
			WithName(previousEnv.branch + " - synced and healthy").
			WithDescription(description).
			WithPhase(phase).
			WithUrl(url))

	// Apply using Server-Side Apply with Patch to get the result directly
	commitStatus := &promoterv1alpha1.CommitStatus{}
	commitStatus.Name = commitStatusName
	commitStatus.Namespace = pecs.Namespace
	if err = r.Patch(ctx, commitStatus, utils.ApplyPatch{ApplyConfig: commitStatusApply}, client.FieldOwner(constants.PreviousEnvironmentCommitStatusControllerFieldOwner), client.ForceOwnership); err != nil {
		return nil, fmt.Errorf("failed to apply previous environment CommitStatus: %w", err)
	}

	logger.V(4).Info("Applied previous environment CommitStatus")

	return commitStatus, nil
}

// getNoteDrySha safely returns the DrySha from a HydratorMetadata pointer, or empty string if nil.
func getNoteDrySha(note *promoterv1alpha1.HydratorMetadata) string {
	if note == nil {
		return ""
	}
	return note.DrySha
}

// getEffectiveHydratedDrySha returns the dry SHA that an environment's hydrator has processed.
// Uses Note.DrySha if available (git note), otherwise falls back to Proposed.Dry.Sha (hydrator.metadata).
func getEffectiveHydratedDrySha(envStatus promoterv1alpha1.EnvironmentStatus) string {
	noteSha := getNoteDrySha(envStatus.Proposed.Note)
	if noteSha != "" {
		return noteSha
	}
	return envStatus.Proposed.Dry.Sha
}

// isPreviousEnvironmentPending recursively checks preceding environments (from last to first) to verify:
// 1. The environment has been hydrated for the target dry SHA
// 2. If the environment has real changes (not a no-op), it has been promoted and is healthy
// 3. If the environment is a no-op, verify it is healthy, then recurse to check earlier environments
func isPreviousEnvironmentPending(precedingEnvStatuses []promoterv1alpha1.EnvironmentStatus, targetDrySha string, currentActiveCommitTime metav1.Time) (isPending bool, reason string) {
	// Base case: no more environments to check - all were no-ops
	// This is valid - e.g., a change that only affects production. Allow promotion.
	if len(precedingEnvStatuses) == 0 {
		return false, ""
	}

	// Check the last (most recent) preceding environment
	envStatus := precedingEnvStatuses[len(precedingEnvStatuses)-1]
	envHydratedForDrySha := getEffectiveHydratedDrySha(envStatus)
	envProposedDrySha := envStatus.Proposed.Dry.Sha

	// Check if hydrator has processed the same dry SHA as the current environment
	if envHydratedForDrySha != targetDrySha {
		return true, "Waiting for the hydrator to finish processing the proposed dry commit"
	}

	// Check if this environment has merged the target dry SHA
	envMergedTarget := envStatus.Active.Dry.Sha == targetDrySha

	if envMergedTarget {
		// Verify commit time ordering (merged env should be equal or newer)
		envDryShaEqualOrNewer := envStatus.Active.Dry.CommitTime.Equal(&metav1.Time{Time: currentActiveCommitTime.Time}) ||
			envStatus.Active.Dry.CommitTime.After(currentActiveCommitTime.Time)
		if !envDryShaEqualOrNewer {
			// This should basically never happen.
			return true, "Previous environment's commit is older than current environment's commit"
		}

		// This environment actually merged the target dry SHA - check its commit statuses
		return checkCommitStatusesPassing(envStatus.Active.CommitStatuses, envStatus.Branch)
	}

	// Check if this environment is a no-op (git note updated but no new commit).
	// A no-op is when Note.DrySha differs from Proposed.Dry.Sha - the git note was updated
	// to a newer dry SHA, but hydrator.metadata still has the old value because no new commit was created.
	envIsNoOp := envHydratedForDrySha != envProposedDrySha

	// Check if this environment has pending changes (PR not yet merged).
	// This catches the case where:
	// - Commit 1 changed this env (autoMerge=false, PR not merged)
	// - Commit 2 did NOT change this env (no-op for commit 2)
	// - Downstream envs should still wait for commit 1's PR to be merged
	envHasPendingChanges := envStatus.Active.Dry.Sha != envProposedDrySha

	// Only recurse (skip this environment) if it's a no-op AND has no pending changes.
	// If it's not a no-op OR has pending changes, we need to wait for it.
	if !envIsNoOp || envHasPendingChanges {
		return true, "Waiting for previous environment to be promoted"
	}

	// Even for no-op environments with no pending changes, verify that the active
	// deployment is healthy. This catches the case where a newer no-op dry SHA arrives
	// while a real promotion is still deploying — without this check, every environment
	// looks like a "no-op with no pending changes" and the recursion skips all health
	// checks, allowing downstream environments to promote prematurely.
	if isPend, reason := checkCommitStatusesPassing(envStatus.Active.CommitStatuses, envStatus.Branch); isPend {
		return isPend, reason
	}

	// This environment is a no-op with no pending changes and is healthy - recurse to check earlier environments
	return isPreviousEnvironmentPending(precedingEnvStatuses[:len(precedingEnvStatuses)-1], targetDrySha, currentActiveCommitTime)
}

// checkCommitStatusesPassing checks if all commit statuses are passing and returns an appropriate
// pending status and reason if not. If branch is empty, it uses "previous environment" as the description.
func checkCommitStatusesPassing(commitStatuses []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase, branch string) (isPending bool, reason string) {
	if utils.AreCommitStatusesPassing(commitStatuses) {
		return false, ""
	}
	envDesc := fmt.Sprintf("%q environment's", branch)
	if branch == "" {
		envDesc = "previous environment's"
	}
	if len(commitStatuses) == 1 {
		return true, fmt.Sprintf("Waiting for %s %q commit status to be successful", envDesc, commitStatuses[0].Key)
	}
	return true, fmt.Sprintf("Waiting for %s commit statuses to be successful", envDesc)
}
