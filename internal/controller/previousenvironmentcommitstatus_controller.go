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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
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

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PreviousEnvironmentCommitStatus object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.3/pkg/reconcile
func (r *PreviousEnvironmentCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)

	// TODO: This is a scaffold. The logic for managing the previous-environment
	// commit status will be migrated here from the PromotionStrategy controller
	// incrementally. For now this is intentionally dead code.
	logger.Info("hello world from PreviousEnvironmentCommitStatus controller", "request", req.NamespacedName)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PreviousEnvironmentCommitStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.PreviousEnvironmentCommitStatus{}).
		Named("previousenvironmentcommitstatus").
		Complete(r); err != nil {
		return fmt.Errorf("setting up previousenvironmentcommitstatus controller: %w", err)
	}
	return nil
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
