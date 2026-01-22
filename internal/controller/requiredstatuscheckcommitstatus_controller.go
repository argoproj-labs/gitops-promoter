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
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	githubscm "github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/google/go-github/v71/github"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// RequiredStatusCheckCommitStatusReconciler reconciles a RequiredStatusCheckCommitStatus object
type RequiredStatusCheckCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	SettingsMgr *settings.Manager
	EnqueueCTP  CTPEnqueueFunc
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=requiredstatuscheckcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=requiredstatuscheckcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=requiredstatuscheckcommitstatuses/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *RequiredStatusCheckCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling RequiredStatusCheckCommitStatus")
	startTime := time.Now()

	var rsccs promoterv1alpha1.RequiredStatusCheckCommitStatus
	// This function will update the resource status at the end of the reconciliation. don't call .Status().Update manually.
	defer utils.HandleReconciliationResult(ctx, startTime, &rsccs, r.Client, r.Recorder, &err)

	// 1. Fetch the RequiredStatusCheckCommitStatus instance
	err = r.Get(ctx, req.NamespacedName, &rsccs, &client.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("RequiredStatusCheckCommitStatus not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get RequiredStatusCheckCommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get RequiredStatusCheckCommitStatus %q: %w", req.Name, err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	meta.RemoveStatusCondition(rsccs.GetConditions(), string(promoterConditions.Ready))

	// 2. Fetch the referenced PromotionStrategy
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{
		Namespace: rsccs.Namespace,
		Name:      rsccs.Spec.PromotionStrategyRef.Name,
	}
	err = r.Get(ctx, psKey, &ps)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Error(err, "referenced PromotionStrategy not found", "promotionStrategy", rsccs.Spec.PromotionStrategyRef.Name)
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", rsccs.Spec.PromotionStrategyRef.Name, err)
		}
		logger.Error(err, "failed to get PromotionStrategy")
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", rsccs.Spec.PromotionStrategyRef.Name, err)
	}

	// 3. Check if showRequiredStatusChecks is enabled
	if !ps.GetShowRequiredStatusChecks() {
		logger.Info("showRequiredStatusChecks is disabled, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// 4. Get all ChangeTransferPolicies for this PromotionStrategy
	var ctpList promoterv1alpha1.ChangeTransferPolicyList
	err = r.List(ctx, &ctpList, &client.ListOptions{
		Namespace: ps.Namespace,
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list ChangeTransferPolicies: %w", err)
	}

	// Filter CTPs that belong to this PromotionStrategy
	var relevantCTPs []promoterv1alpha1.ChangeTransferPolicy
	for _, ctp := range ctpList.Items {
		if ctp.Labels[promoterv1alpha1.PromotionStrategyLabel] == ps.Name {
			relevantCTPs = append(relevantCTPs, ctp)
		}
	}

	// Save previous status to detect transitions
	previousStatus := rsccs.Status.DeepCopy()
	if previousStatus == nil {
		previousStatus = &promoterv1alpha1.RequiredStatusCheckCommitStatusStatus{}
	}

	// 5. Process each environment and create/update CommitStatus resources
	commitStatuses, err := r.processEnvironments(ctx, &rsccs, &ps, relevantCTPs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to process environments: %w", err)
	}

	// 6. Clean up orphaned CommitStatus resources
	err = r.cleanupOrphanedCommitStatuses(ctx, &rsccs, commitStatuses)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to cleanup orphaned CommitStatus resources: %w", err)
	}

	// 7. Inherit conditions from CommitStatus objects
	utils.InheritNotReadyConditionFromObjects(&rsccs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	// 8. Trigger CTP reconciliation if any environment phase changed
	err = r.triggerCTPReconciliation(ctx, &ps, previousStatus, &rsccs.Status)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to trigger CTP reconciliation: %w", err)
	}

	// 9. Calculate dynamic requeue duration
	requeueDuration := r.calculateRequeueDuration(ctx, &rsccs)

	return ctrl.Result{
		RequeueAfter: requeueDuration,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RequiredStatusCheckCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.RequiredStatusCheckCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get RequiredStatusCheckCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.RequiredStatusCheckCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get RequiredStatusCheckCommitStatus max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.RequiredStatusCheckCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.PromotionStrategy{}, r.enqueueRSCCSForPromotionStrategy()).
		Watches(&promoterv1alpha1.CommitStatus{}, handler.EnqueueRequestForOwner(
			mgr.GetScheme(),
			mgr.GetRESTMapper(),
			&promoterv1alpha1.RequiredStatusCheckCommitStatus{},
			handler.OnlyControllerOwner(),
		)).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// enqueueRSCCSForPromotionStrategy enqueues all RequiredStatusCheckCommitStatus resources for a PromotionStrategy
func (r *RequiredStatusCheckCommitStatusReconciler) enqueueRSCCSForPromotionStrategy() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
		if !ok {
			return nil
		}

		var rsccs promoterv1alpha1.RequiredStatusCheckCommitStatusList
		err := r.List(ctx, &rsccs, &client.ListOptions{
			Namespace: ps.Namespace,
		})
		if err != nil {
			return nil
		}

		var requests []ctrl.Request
		for _, item := range rsccs.Items {
			if item.Spec.PromotionStrategyRef.Name == ps.Name {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKey{
						Namespace: item.Namespace,
						Name:      item.Name,
					},
				})
			}
		}
		return requests
	})
}

// processEnvironments processes each environment and creates/updates CommitStatus resources
func (r *RequiredStatusCheckCommitStatusReconciler) processEnvironments(ctx context.Context, rsccs *promoterv1alpha1.RequiredStatusCheckCommitStatus, ps *promoterv1alpha1.PromotionStrategy, ctps []promoterv1alpha1.ChangeTransferPolicy) ([]*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	// Initialize environments status
	rsccs.Status.Environments = make([]promoterv1alpha1.RequiredStatusCheckEnvironmentStatus, 0)

	// Track all CommitStatus objects created/updated
	var commitStatuses []*promoterv1alpha1.CommitStatus

	// Build a map of CTPs by environment branch
	ctpByBranch := make(map[string]*promoterv1alpha1.ChangeTransferPolicy)
	for i := range ctps {
		ctp := &ctps[i]
		ctpByBranch[ctp.Spec.ActiveBranch] = ctp
	}

	// Process each environment
	for _, env := range ps.Spec.Environments {
		ctp, found := ctpByBranch[env.Branch]
		if !found {
			logger.Info("ChangeTransferPolicy not found for environment", "branch", env.Branch)
			continue
		}

		// Get proposed SHA from CTP status
		proposedSha := ctp.Status.Proposed.Hydrated.Sha
		if proposedSha == "" {
			logger.Info("No proposed SHA found for environment", "branch", env.Branch)
			continue
		}

		// Discover required checks for this environment
		requiredChecks, err := r.discoverRequiredChecksForEnvironment(ctx, ps, ctp, env)
		if err != nil {
			logger.Error(err, "failed to discover required checks", "branch", env.Branch)
			// Continue to process other environments
			continue
		}

		// Poll check status for each required check
		checkStatuses, err := r.pollCheckStatusForEnvironment(ctx, ps, ctp, requiredChecks, proposedSha)
		if err != nil {
			logger.Error(err, "failed to poll check status", "branch", env.Branch)
			// Continue to process other environments
			continue
		}

		// Create/update CommitStatus resources for each check
		var envCheckStatuses []promoterv1alpha1.RequiredCheckStatus
		for _, checkContext := range requiredChecks {
			phase, ok := checkStatuses[checkContext]
			if !ok {
				// If we couldn't get the status, default to pending
				phase = promoterv1alpha1.CommitPhasePending
			}

			cs, err := r.updateCommitStatusForCheck(ctx, rsccs, ctp, env.Branch, checkContext, proposedSha, phase)
			if err != nil {
				logger.Error(err, "failed to update CommitStatus", "check", checkContext)
				// Continue to process other checks
				continue
			}

			commitStatuses = append(commitStatuses, cs)
			envCheckStatuses = append(envCheckStatuses, promoterv1alpha1.RequiredCheckStatus{
				Context:          checkContext,
				Phase:            phase,
				CommitStatusName: cs.Name,
			})
		}

		// Calculate aggregated phase for this environment
		aggregatedPhase := r.calculateAggregatedPhase(envCheckStatuses)

		// Update environment status
		rsccs.Status.Environments = append(rsccs.Status.Environments, promoterv1alpha1.RequiredStatusCheckEnvironmentStatus{
			Branch:         env.Branch,
			Sha:            proposedSha,
			RequiredChecks: envCheckStatuses,
			Phase:          aggregatedPhase,
		})
	}

	return commitStatuses, nil
}

// discoverRequiredChecksForEnvironment queries GitHub Rulesets API to discover required checks
func (r *RequiredStatusCheckCommitStatusReconciler) discoverRequiredChecksForEnvironment(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, ctp *promoterv1alpha1.ChangeTransferPolicy, env promoterv1alpha1.Environment) ([]string, error) {
	logger := log.FromContext(ctx)

	// Get GitRepository
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{
		Namespace: ps.Namespace,
		Name:      ps.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	// Get ScmProvider and Secret
	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, r.SettingsMgr.GetControllerNamespace(), ps.Spec.RepositoryReference, ps)
	if err != nil {
		return nil, fmt.Errorf("failed to get ScmProvider and secret: %w", err)
	}

	// Only support GitHub for now
	if scmProvider.GetSpec().GitHub == nil {
		logger.V(4).Info("ScmProvider is not GitHub, skipping required checks discovery")
		return []string{}, nil
	}

	// Create GitHub client
	githubProvider, err := githubscm.NewGithubCommitStatusProvider(ctx, r.Client, scmProvider, *secret, gitRepo.Spec.GitHub.Owner)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub provider: %w", err)
	}

	// Get the underlying GitHub client
	// Note: We need to expose GetClient() method on the GitHub provider
	githubClient := githubProvider.GetClient()

	// Query GetRulesForBranch
	rules, _, err := githubClient.Repositories.GetRulesForBranch(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, env.Branch)
	if err != nil {
		// If the branch doesn't have rulesets, return empty list
		logger.V(4).Info("No rulesets found for branch", "branch", env.Branch, "error", err)
		return []string{}, nil
	}

	// Extract required status checks from BranchRules
	var requiredChecks []string
	if rules != nil && rules.RequiredStatusChecks != nil {
		for _, ruleStatusCheck := range rules.RequiredStatusChecks {
			if ruleStatusCheck.Parameters.RequiredStatusChecks != nil {
				for _, check := range ruleStatusCheck.Parameters.RequiredStatusChecks {
					if check.Context != "" {
						requiredChecks = append(requiredChecks, check.Context)
					}
				}
			}
		}
	}

	// Apply exclusions
	if len(env.ExcludedRequiredStatusChecks) > 0 {
		var filteredChecks []string
		for _, check := range requiredChecks {
			if !slices.Contains(env.ExcludedRequiredStatusChecks, check) {
				filteredChecks = append(filteredChecks, check)
			}
		}
		requiredChecks = filteredChecks
	}

	return requiredChecks, nil
}

// pollCheckStatusForEnvironment polls GitHub Checks API to get the status of each required check
func (r *RequiredStatusCheckCommitStatusReconciler) pollCheckStatusForEnvironment(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, ctp *promoterv1alpha1.ChangeTransferPolicy, requiredChecks []string, sha string) (map[string]promoterv1alpha1.CommitStatusPhase, error) {
	logger := log.FromContext(ctx)

	if len(requiredChecks) == 0 {
		return map[string]promoterv1alpha1.CommitStatusPhase{}, nil
	}

	// Get GitRepository
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{
		Namespace: ps.Namespace,
		Name:      ps.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	// Get ScmProvider and Secret
	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, r.SettingsMgr.GetControllerNamespace(), ps.Spec.RepositoryReference, ps)
	if err != nil {
		return nil, fmt.Errorf("failed to get ScmProvider and secret: %w", err)
	}

	// Only support GitHub for now
	if scmProvider.GetSpec().GitHub == nil {
		return map[string]promoterv1alpha1.CommitStatusPhase{}, nil
	}

	// Create GitHub client
	githubProvider, err := githubscm.NewGithubCommitStatusProvider(ctx, r.Client, scmProvider, *secret, gitRepo.Spec.GitHub.Owner)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitHub provider: %w", err)
	}

	githubClient := githubProvider.GetClient()

	// Query check status for each required check
	checkStatuses := make(map[string]promoterv1alpha1.CommitStatusPhase)
	for _, checkContext := range requiredChecks {
		opts := &github.ListCheckRunsOptions{
			CheckName: github.Ptr(checkContext),
		}

		checkRuns, _, err := githubClient.Checks.ListCheckRunsForRef(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, sha, opts)
		if err != nil {
			logger.Error(err, "failed to list check runs", "check", checkContext, "sha", sha)
			// Default to pending if we can't get the status
			checkStatuses[checkContext] = promoterv1alpha1.CommitPhasePending
			continue
		}

		// If no check runs found, status is pending
		if checkRuns == nil || len(checkRuns.CheckRuns) == 0 {
			checkStatuses[checkContext] = promoterv1alpha1.CommitPhasePending
			continue
		}

		// Get the latest check run
		checkRun := checkRuns.CheckRuns[0]

		// Map GitHub check status to CommitStatusPhase
		phase := r.mapGitHubCheckStatusToPhase(checkRun)
		checkStatuses[checkContext] = phase
	}

	return checkStatuses, nil
}

// mapGitHubCheckStatusToPhase maps GitHub check run status to CommitStatusPhase
func (r *RequiredStatusCheckCommitStatusReconciler) mapGitHubCheckStatusToPhase(checkRun *github.CheckRun) promoterv1alpha1.CommitStatusPhase {
	if checkRun.Status == nil {
		return promoterv1alpha1.CommitPhasePending
	}

	status := *checkRun.Status

	if status == "completed" {
		if checkRun.Conclusion == nil {
			return promoterv1alpha1.CommitPhasePending
		}

		conclusion := *checkRun.Conclusion
		switch conclusion {
		case "success", "neutral", "skipped":
			return promoterv1alpha1.CommitPhaseSuccess
		case "failure", "cancelled", "timed_out", "action_required":
			return promoterv1alpha1.CommitPhaseFailure
		default:
			return promoterv1alpha1.CommitPhasePending
		}
	}

	// Status is queued or in_progress
	return promoterv1alpha1.CommitPhasePending
}

// updateCommitStatusForCheck creates or updates a CommitStatus resource for a required check
func (r *RequiredStatusCheckCommitStatusReconciler) updateCommitStatusForCheck(ctx context.Context, rsccs *promoterv1alpha1.RequiredStatusCheckCommitStatus, ctp *promoterv1alpha1.ChangeTransferPolicy, branch string, checkContext string, sha string, phase promoterv1alpha1.CommitStatusPhase) (*promoterv1alpha1.CommitStatus, error) {
	// Generate CommitStatus name: required-status-check-{context}-{hash}
	name := generateCommitStatusName(checkContext, branch)

	cs := &promoterv1alpha1.CommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: rsccs.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cs, func() error {
		// Set owner reference
		if err := controllerutil.SetControllerReference(rsccs, cs, r.Scheme); err != nil {
			return err
		}

		// Set labels
		if cs.Labels == nil {
			cs.Labels = make(map[string]string)
		}
		cs.Labels[promoterv1alpha1.CommitStatusLabel] = fmt.Sprintf("required-status-check-%s", checkContext)
		cs.Labels[promoterv1alpha1.EnvironmentLabel] = utils.KubeSafeLabel(branch)
		cs.Labels[promoterv1alpha1.RequiredStatusCheckCommitStatusLabel] = rsccs.Name

		// Set spec
		cs.Spec = promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: ctp.Spec.RepositoryReference,
			Sha:                 sha,
			Name:                fmt.Sprintf("Required Check: %s", checkContext),
			Description:         fmt.Sprintf("GitHub required status check: %s", checkContext),
			Phase:               phase,
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create or update CommitStatus: %w", err)
	}

	r.Recorder.Eventf(cs, "Normal", constants.CommitStatusSetReason, "Required check %s status set to %s for hash %s", checkContext, phase, sha)

	return cs, nil
}

// generateCommitStatusName generates a unique name for a CommitStatus resource
func generateCommitStatusName(checkContext string, branch string) string {
	// Create a hash of the check context and branch to ensure uniqueness
	h := sha256.New()
	h.Write([]byte(checkContext + "-" + branch))
	hash := fmt.Sprintf("%x", h.Sum(nil))[:8]

	// Normalize the check context to be a valid Kubernetes name
	normalized := strings.ToLower(checkContext)
	normalized = strings.ReplaceAll(normalized, "/", "-")
	normalized = strings.ReplaceAll(normalized, "_", "-")
	normalized = strings.ReplaceAll(normalized, ".", "-")

	return fmt.Sprintf("required-status-check-%s-%s", normalized, hash)
}

// calculateAggregatedPhase calculates the aggregated phase for an environment
func (r *RequiredStatusCheckCommitStatusReconciler) calculateAggregatedPhase(checks []promoterv1alpha1.RequiredCheckStatus) promoterv1alpha1.CommitStatusPhase {
	if len(checks) == 0 {
		return promoterv1alpha1.CommitPhaseSuccess
	}

	// If any check is failing, the aggregate is failure
	for _, check := range checks {
		if check.Phase == promoterv1alpha1.CommitPhaseFailure {
			return promoterv1alpha1.CommitPhaseFailure
		}
	}

	// If any check is pending, the aggregate is pending
	for _, check := range checks {
		if check.Phase == promoterv1alpha1.CommitPhasePending {
			return promoterv1alpha1.CommitPhasePending
		}
	}

	// All checks are success
	return promoterv1alpha1.CommitPhaseSuccess
}

// cleanupOrphanedCommitStatuses removes CommitStatus resources that are no longer needed
func (r *RequiredStatusCheckCommitStatusReconciler) cleanupOrphanedCommitStatuses(ctx context.Context, rsccs *promoterv1alpha1.RequiredStatusCheckCommitStatus, validCommitStatuses []*promoterv1alpha1.CommitStatus) error {
	logger := log.FromContext(ctx)

	// List all CommitStatus resources owned by this RSCCS
	var csList promoterv1alpha1.CommitStatusList
	err := r.List(ctx, &csList, &client.ListOptions{
		Namespace: rsccs.Namespace,
	})
	if err != nil {
		return fmt.Errorf("failed to list CommitStatus resources: %w", err)
	}

	// Build a set of valid CommitStatus names
	validNames := make(map[string]bool)
	for _, cs := range validCommitStatuses {
		validNames[cs.Name] = true
	}

	// Delete orphaned CommitStatus resources
	for _, cs := range csList.Items {
		// Check if this CommitStatus is owned by this RSCCS
		isOwned := false
		for _, ownerRef := range cs.OwnerReferences {
			if ownerRef.UID == rsccs.UID {
				isOwned = true
				break
			}
		}

		if !isOwned {
			continue
		}

		// If not in valid names, delete it
		if !validNames[cs.Name] {
			logger.Info("Deleting orphaned CommitStatus", "name", cs.Name)
			err := r.Delete(ctx, &cs)
			if err != nil && !k8serrors.IsNotFound(err) {
				logger.Error(err, "failed to delete orphaned CommitStatus", "name", cs.Name)
				// Continue to try deleting other orphaned resources
				continue
			}
			r.Recorder.Eventf(rsccs, "Normal", "CommitStatusDeleted", "Deleted orphaned CommitStatus %s", cs.Name)
		}
	}

	return nil
}

// calculateRequeueDuration calculates the dynamic requeue duration
func (r *RequiredStatusCheckCommitStatusReconciler) calculateRequeueDuration(ctx context.Context, rsccs *promoterv1alpha1.RequiredStatusCheckCommitStatus) time.Duration {
	// Check if any environment has pending checks
	for _, env := range rsccs.Status.Environments {
		if env.Phase == promoterv1alpha1.CommitPhasePending {
			// If any checks are pending, requeue after 1 minute
			return 1 * time.Minute
		}
	}

	// Otherwise, use the configured polling interval
	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.RequiredStatusCheckCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		// Default to 5 minutes if we can't get the configuration
		return 5 * time.Minute
	}

	return requeueDuration
}

// triggerCTPReconciliation triggers CTP reconciliation if any environment phase changed
func (r *RequiredStatusCheckCommitStatusReconciler) triggerCTPReconciliation(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, previousStatus *promoterv1alpha1.RequiredStatusCheckCommitStatusStatus, currentStatus *promoterv1alpha1.RequiredStatusCheckCommitStatusStatus) error {
	logger := log.FromContext(ctx)

	// Build maps of environment phases
	previousPhases := make(map[string]promoterv1alpha1.CommitStatusPhase)
	for _, env := range previousStatus.Environments {
		previousPhases[env.Branch] = env.Phase
	}

	currentPhases := make(map[string]promoterv1alpha1.CommitStatusPhase)
	for _, env := range currentStatus.Environments {
		currentPhases[env.Branch] = env.Phase
	}

	// Check for phase transitions
	changedBranches := make(map[string]bool)
	for branch, currentPhase := range currentPhases {
		previousPhase, ok := previousPhases[branch]
		if !ok || previousPhase != currentPhase {
			changedBranches[branch] = true
		}
	}

	// Trigger CTP reconciliation for changed branches
	if len(changedBranches) > 0 && r.EnqueueCTP != nil {
		logger.Info("Triggering CTP reconciliation for changed branches", "branches", changedBranches)

		// List CTPs to find the ones that match the changed branches
		var ctpList promoterv1alpha1.ChangeTransferPolicyList
		err := r.List(ctx, &ctpList, &client.ListOptions{
			Namespace: ps.Namespace,
		})
		if err != nil {
			return fmt.Errorf("failed to list ChangeTransferPolicies for CTP enqueue: %w", err)
		}

		// Find CTPs that match changed branches and enqueue them
		for _, ctp := range ctpList.Items {
			// Check if this CTP belongs to this PromotionStrategy
			if ctp.Labels[promoterv1alpha1.PromotionStrategyLabel] != ps.Name {
				continue
			}

			// Check if this CTP's branch has changed
			if changedBranches[ctp.Spec.ActiveBranch] {
				r.EnqueueCTP(ctp.Namespace, ctp.Name)
				logger.V(4).Info("Enqueued CTP for reconciliation", "ctp", ctp.Name, "branch", ctp.Spec.ActiveBranch)
			}
		}
	}

	return nil
}
