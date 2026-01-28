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
	"sync"
	"time"

	"gopkg.in/yaml.v3"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	acmetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ctpEnqueueState tracks rate limiting state for enqueuing out-of-sync CTPs.
type ctpEnqueueState struct {
	lastEnqueueTime   time.Time
	hasScheduledRetry bool
}

// PromotionStrategyReconciler reconciles a PromotionStrategy object
type PromotionStrategyReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager

	// EnqueueCTP is a function to enqueue CTP reconcile requests without modifying the CTP object.
	EnqueueCTP CTPEnqueueFunc

	// enqueueStates tracks rate limiting state for out-of-sync CTP enqueues.
	// Key is client.ObjectKey of the CTP. Protected by enqueueStateMutex.
	enqueueStates     map[client.ObjectKey]*ctpEnqueueState
	enqueueStateMutex sync.Mutex
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies/finalizers,verbs=update

//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *PromotionStrategyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling PromotionStrategy")
	startTime := time.Now()

	var ps promoterv1alpha1.PromotionStrategy
	// This function will update the resource status at the end of the reconciliation. don't call .Status().Update manually.
	defer utils.HandleReconciliationResult(ctx, startTime, &ps, r.Client, r.Recorder, &err)

	err = r.Get(ctx, req.NamespacedName, &ps, &client.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("PromotionStrategy not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get PromotionStrategy")
		return ctrl.Result{}, fmt.Errorf("failed to get PromitionStrategy %q: %w", req.Name, err)
	}

	// If the resource is being deleted, stop reconciling immediately without requeuing
	if !ps.DeletionTimestamp.IsZero() {
		logger.V(4).Info("PromotionStrategy is being deleted, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Remove any existing Ready condition. We want to start fresh.
	meta.RemoveStatusCondition(ps.GetConditions(), string(promoterConditions.Ready))

	// If a ChangeTransferPolicy does not exist, create it otherwise get it and store the ChangeTransferPolicy in a slice with the same order as ps.Spec.Environments.
	ctps := make([]*promoterv1alpha1.ChangeTransferPolicy, len(ps.Spec.Environments))
	for i, environment := range ps.Spec.Environments {
		var ctp *promoterv1alpha1.ChangeTransferPolicy
		ctp, err = r.upsertChangeTransferPolicy(ctx, &ps, environment)
		if err != nil {
			logger.Error(err, "failed to upsert ChangeTransferPolicy")
			return ctrl.Result{}, fmt.Errorf("failed to create ChangeTransferPolicy for branch %q: %w", environment.Branch, err)
		}
		ctps[i] = ctp
	}

	// Clean up orphaned ChangeTransferPolicies that are no longer in the environment list
	err = r.cleanupOrphanedChangeTransferPolicies(ctx, &ps, ctps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to cleanup orphaned ChangeTransferPolicies: %w", err)
	}

	// Calculate the status of the PromotionStrategy. Updates ps in place.
	r.calculateStatus(&ps, ctps)

	err = r.updatePreviousEnvironmentCommitStatus(ctx, &ps, ctps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to merge PRs: %w", err)
	}

	// Check if any environments need to refresh their git notes.
	// SCM's do not send webhooks when git notes are pushed, so we need to
	// trigger CTP reconciliation when we detect stale NoteDrySha values.
	// This is done AFTER updating the PromotionStrategy status to avoid conflicts.
	// When CTPs reconcile and update their status, the .Owns() watch will automatically
	// trigger this PromotionStrategy to reconcile again.
	r.enqueueOutOfSyncCTPs(ctx, ctps)

	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.PromotionStrategyConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration for PromotionStrategy %q: %w", ps.Name, err)
	}

	return ctrl.Result{
		Requeue:      true,
		RequeueAfter: requeueDuration,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PromotionStrategyReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &promoterv1alpha1.CommitStatus{}, ".spec.sha", func(rawObj client.Object) []string {
		//nolint:forcetypeassert
		cs := rawObj.(*promoterv1alpha1.CommitStatus)
		return []string{cs.Spec.Sha}
	}); err != nil {
		return fmt.Errorf("failed to set field index for .spec.sha: %w", err)
	}

	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.PromotionStrategyConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get PromotionStrategy rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.PromotionStrategyConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get PromotionStrategy max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.PromotionStrategy{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&promoterv1alpha1.ChangeTransferPolicy{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

func (r *PromotionStrategyReconciler) upsertChangeTransferPolicy(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, environment promoterv1alpha1.Environment) (*promoterv1alpha1.ChangeTransferPolicy, error) {
	logger := log.FromContext(ctx)

	ctpName := utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(ps.Name, environment.Branch))

	// Build owner reference
	kind := reflect.TypeOf(promoterv1alpha1.PromotionStrategy{}).Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)

	// Build active commit status selectors
	activeCommitStatuses := make([]*acv1alpha1.CommitStatusSelectorApplyConfiguration, 0, len(environment.ActiveCommitStatuses)+len(ps.Spec.ActiveCommitStatuses))
	for _, cs := range environment.ActiveCommitStatuses {
		activeCommitStatuses = append(activeCommitStatuses, acv1alpha1.CommitStatusSelector().WithKey(cs.Key))
	}
	for _, cs := range ps.Spec.ActiveCommitStatuses {
		activeCommitStatuses = append(activeCommitStatuses, acv1alpha1.CommitStatusSelector().WithKey(cs.Key))
	}

	// Build proposed commit status selectors
	proposedCommitStatuses := make([]*acv1alpha1.CommitStatusSelectorApplyConfiguration, 0, len(environment.ProposedCommitStatuses)+len(ps.Spec.ProposedCommitStatuses))
	for _, cs := range environment.ProposedCommitStatuses {
		proposedCommitStatuses = append(proposedCommitStatuses, acv1alpha1.CommitStatusSelector().WithKey(cs.Key))
	}
	for _, cs := range ps.Spec.ProposedCommitStatuses {
		proposedCommitStatuses = append(proposedCommitStatuses, acv1alpha1.CommitStatusSelector().WithKey(cs.Key))
	}

	// Add previous environment commit status if needed
	environmentIndex, _ := utils.GetEnvironmentByBranch(*ps, environment.Branch)
	previousEnvironmentIndex := environmentIndex - 1
	if environmentIndex > 0 && len(ps.Spec.ActiveCommitStatuses) != 0 || (previousEnvironmentIndex >= 0 && len(ps.Spec.Environments[previousEnvironmentIndex].ActiveCommitStatuses) != 0) {
		// Check if already present
		found := false
		for _, cs := range proposedCommitStatuses {
			if cs.Key != nil && *cs.Key == promoterv1alpha1.PreviousEnvironmentCommitStatusKey {
				found = true
				break
			}
		}
		if !found {
			proposedCommitStatuses = append(proposedCommitStatuses, acv1alpha1.CommitStatusSelector().WithKey(promoterv1alpha1.PreviousEnvironmentCommitStatusKey))
		}
	}

	// Add required status checks if the controller is enabled
	config, configErr := settings.GetRequiredStatusCheckCommitStatusConfiguration(ctx, r.SettingsMgr)
	if configErr != nil {
		return nil, fmt.Errorf("failed to get RequiredStatusCheckCommitStatus configuration: %w", configErr)
	}
	if config.Enabled {
		logger.V(4).Info("Processing required status checks for CTP",
			"branch", environment.Branch,
			"ctpName", ctpName)
		// Get the RequiredStatusCheckCommitStatus for this PromotionStrategy
		// Use ps.Name directly (not KubeSafeUniqueName) to match how RSCCS is created
		rsccsName := ps.Name
		rsccs := &promoterv1alpha1.RequiredStatusCheckCommitStatus{}
		getErr := r.Get(ctx, client.ObjectKey{Name: rsccsName, Namespace: ps.Namespace}, rsccs)
		if getErr == nil {
			logger.V(4).Info("Found RSCCS, checking environments",
				"rsccsName", rsccsName,
				"numEnvironments", len(rsccs.Status.Environments),
				"targetBranch", environment.Branch)
			// Find the environment status for this CTP's branch
			for _, envStatus := range rsccs.Status.Environments {
				if envStatus.Branch == environment.Branch {
					logger.V(4).Info("Found matching environment",
						"branch", environment.Branch,
						"numRequiredChecks", len(envStatus.RequiredChecks))
					// Add a CommitStatusSelector for each required check
					for _, requiredCheck := range envStatus.RequiredChecks {
						// The key matches the CommitStatus label that the RSCCS controller creates
						checkKey := "required-status-check-" + requiredCheck.Name
						// Check if already present
						alreadyPresent := false
						for _, cs := range proposedCommitStatuses {
							if cs.Key != nil && *cs.Key == checkKey {
								alreadyPresent = true
								break
							}
						}
						if !alreadyPresent {
							proposedCommitStatuses = append(proposedCommitStatuses, acv1alpha1.CommitStatusSelector().WithKey(checkKey))
							logger.V(4).Info("Added required status check to ProposedCommitStatuses",
								"checkKey", checkKey,
								"branch", environment.Branch,
								"name", requiredCheck.Name)
						} else {
							logger.V(4).Info("Required status check already in ProposedCommitStatuses",
								"checkKey", checkKey,
								"branch", environment.Branch)
						}
					}
					break
				}
			}
		} else if !k8serrors.IsNotFound(getErr) {
			// Log non-NotFound errors
			logger.Error(getErr, "Failed to get RequiredStatusCheckCommitStatus",
				"name", rsccsName,
				"namespace", ps.Namespace)
		} else {
			logger.V(4).Info("RSCCS not found",
				"name", rsccsName,
				"namespace", ps.Namespace)
		}
	}

	// Build the spec
	ctpSpec := acv1alpha1.ChangeTransferPolicySpec().
		WithRepositoryReference(acv1alpha1.ObjectReference().WithName(ps.Spec.RepositoryReference.Name)).
		WithProposedBranch(fmt.Sprintf("%s-%s", environment.Branch, "next")).
		WithActiveBranch(environment.Branch).
		WithActiveCommitStatuses(activeCommitStatuses...).
		WithProposedCommitStatuses(proposedCommitStatuses...)

	if environment.AutoMerge != nil {
		ctpSpec = ctpSpec.WithAutoMerge(*environment.AutoMerge)
	}

	// Build the apply configuration
	ctpApply := acv1alpha1.ChangeTransferPolicy(ctpName, ps.Namespace).
		WithLabels(map[string]string{
			promoterv1alpha1.PromotionStrategyLabel: utils.KubeSafeLabel(ps.Name),
			promoterv1alpha1.EnvironmentLabel:       utils.KubeSafeLabel(environment.Branch),
		}).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(ps.Name).
			WithUID(ps.UID).
			WithController(true).
			WithBlockOwnerDeletion(true)).
		WithSpec(ctpSpec)

	// Apply using Server-Side Apply with Patch to get the result directly
	ctp := &promoterv1alpha1.ChangeTransferPolicy{}
	ctp.Name = ctpName
	ctp.Namespace = ps.Namespace
	if err := r.Patch(ctx, ctp, utils.ApplyPatch{ApplyConfig: ctpApply}, client.FieldOwner(constants.PromotionStrategyControllerFieldOwner), client.ForceOwnership); err != nil {
		return nil, fmt.Errorf("failed to apply ChangeTransferPolicy %q: %w", ctpName, err)
	}

	logger.V(4).Info("Applied ChangeTransferPolicy")

	return ctp, nil
}

// cleanupOrphanedChangeTransferPolicies deletes ChangeTransferPolicies that are owned by this PromotionStrategy
// but are not in the current list of valid CTPs (i.e., they correspond to removed or renamed environments).
//
//nolint:dupl // Similar to TimedCommitStatus cleanup but works with different types
func (r *PromotionStrategyReconciler) cleanupOrphanedChangeTransferPolicies(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, validCtps []*promoterv1alpha1.ChangeTransferPolicy) error {
	logger := log.FromContext(ctx)

	// Create a set of valid CTP names for quick lookup
	validCtpNames := make(map[string]bool)
	for _, ctp := range validCtps {
		validCtpNames[ctp.Name] = true
	}

	// List all CTPs in the namespace with the PromotionStrategy label
	var ctpList promoterv1alpha1.ChangeTransferPolicyList
	err := r.List(ctx, &ctpList, client.InNamespace(ps.Namespace), client.MatchingLabels{
		promoterv1alpha1.PromotionStrategyLabel: utils.KubeSafeLabel(ps.Name),
	})
	if err != nil {
		return fmt.Errorf("failed to list ChangeTransferPolicies: %w", err)
	}

	// Delete CTPs that are not in the valid list
	for _, ctp := range ctpList.Items {
		// Skip if this CTP is in the valid list
		if validCtpNames[ctp.Name] {
			continue
		}

		// Verify this CTP is owned by this PromotionStrategy before deleting
		if !metav1.IsControlledBy(&ctp, ps) {
			logger.V(4).Info("Skipping ChangeTransferPolicy not owned by this PromotionStrategy",
				"ctpName", ctp.Name,
				"promotionStrategy", ps.Name)
			continue
		}

		// Delete the orphaned CTP
		logger.Info("Deleting orphaned ChangeTransferPolicy",
			"ctpName", ctp.Name,
			"promotionStrategy", ps.Name,
			"namespace", ps.Namespace)

		if err := r.Delete(ctx, &ctp); err != nil {
			if k8serrors.IsNotFound(err) {
				// Already deleted, which is fine
				logger.V(4).Info("ChangeTransferPolicy already deleted", "ctpName", ctp.Name)
				continue
			}
			return fmt.Errorf("failed to delete orphaned ChangeTransferPolicy %q: %w", ctp.Name, err)
		}

		r.Recorder.Eventf(ps, nil, "Normal", constants.OrphanedChangeTransferPolicyDeletedReason, "CleaningOrphanedResources", constants.OrphanedChangeTransferPolicyDeletedMessage, ctp.Name)
	}

	return nil
}

// calculateStatus calculates the status of the PromotionStrategy based on the ChangeTransferPolicies.
// ps.Spec.Environments must be the same length and in the same order as ctps.
// This function updates ps.Status.Environments to be the same length and order as ps.Spec.Environments.
func (r *PromotionStrategyReconciler) calculateStatus(ps *promoterv1alpha1.PromotionStrategy, ctps []*promoterv1alpha1.ChangeTransferPolicy) {
	// Reconstruct current environment state based on ps.Environments order. Dropped environments will effectively be
	// deleted, and new environments will be added as empty statuses. Those new environments will be populated in the
	// ctp loop.
	environmentStatuses := make([]promoterv1alpha1.EnvironmentStatus, len(ps.Spec.Environments))
	for i, environment := range ps.Spec.Environments {
		for _, environmentStatus := range ps.Status.Environments {
			if environmentStatus.Branch == environment.Branch {
				environmentStatuses[i] = environmentStatus
				break
			}
		}
	}
	ps.Status.Environments = environmentStatuses

	for i, ctp := range ctps {
		// Update fields individually to avoid overwriting existing fields.
		ps.Status.Environments[i].Branch = ctp.Spec.ActiveBranch
		ps.Status.Environments[i].Active = ctp.Status.Active
		ps.Status.Environments[i].Proposed = ctp.Status.Proposed
		ps.Status.Environments[i].PullRequest = ctp.Status.PullRequest
		ps.Status.Environments[i].History = ctp.Status.History

		// TODO: actually implement keeping track of healthy dry sha's
		// We only want to keep the last 10 healthy dry sha's
		if i < len(ps.Status.Environments) && len(ps.Status.Environments[i].LastHealthyDryShas) > 10 {
			ps.Status.Environments[i].LastHealthyDryShas = ps.Status.Environments[i].LastHealthyDryShas[:10]
		}
	}

	utils.InheritNotReadyConditionFromObjects(ps, promoterConditions.ChangeTransferPolicyNotReady, ctps...)
}

// enqueueOutOfSyncCTPs checks if all CTPs have the same effective dry SHA
// (Note.DrySha if set, otherwise Proposed.Dry.Sha). If they differ, the CTPs with
// different values need to reconcile to fetch updated git notes or proposed dry sha. This is needed
// because GitHub doesn't send webhooks when git notes are pushed.
// Rate limiting: Only enqueues a CTP once per 15 seconds. If rate limited, schedules a delayed enqueue.
func (r *PromotionStrategyReconciler) enqueueOutOfSyncCTPs(ctx context.Context, ctps []*promoterv1alpha1.ChangeTransferPolicy) {
	if len(ctps) == 0 {
		return
	}

	// Initialize state map lazily
	if r.enqueueStates == nil {
		r.enqueueStateMutex.Lock()
		if r.enqueueStates == nil {
			r.enqueueStates = make(map[client.ObjectKey]*ctpEnqueueState)
		}
		r.enqueueStateMutex.Unlock()

		r.startCleanupTimer()
	}

	// Get the effective dry SHA for each CTP (Note.DrySha if set, otherwise Proposed.Dry.Sha)
	getEffectiveDrySha := func(ctp *promoterv1alpha1.ChangeTransferPolicy) string {
		if ctp.Status.Proposed.Note != nil && ctp.Status.Proposed.Note.DrySha != "" {
			return ctp.Status.Proposed.Note.DrySha
		}
		return ctp.Status.Proposed.Dry.Sha
	}

	// Find the target SHA - the Proposed.Dry.Sha from the CTP with the newest proposed hydrated commit.
	// We use the hydrated commit time to find the most recently hydrated environment, then use its
	// Proposed.Dry.Sha as the target. CTPs whose effective dry SHA (from git note) doesn't match
	// this target need to reconcile to fetch the updated git note.
	var targetSha string
	var newestTime metav1.Time
	for _, ctp := range ctps {
		proposedDrySha := ctp.Status.Proposed.Dry.Sha
		if proposedDrySha == "" {
			continue
		}
		commitTime := ctp.Status.Proposed.Hydrated.CommitTime
		if targetSha == "" || commitTime.After(newestTime.Time) {
			targetSha = proposedDrySha
			newestTime = commitTime
		}
	}

	if targetSha == "" {
		return
	}

	// Trigger reconcile only for CTPs that have a different effective dry SHA.
	// Rate limiting: Only enqueue if not enqueued recently.
	for _, ctp := range ctps {
		effectiveSha := getEffectiveDrySha(ctp)
		if effectiveSha == targetSha {
			continue
		}

		// Add SHA information to context for logging
		ctxWithLog := log.IntoContext(ctx, log.FromContext(ctx).WithValues(
			"effectiveSha", effectiveSha,
			"targetSha", targetSha,
		))
		r.handleRateLimitedEnqueue(ctxWithLog, ctp)
	}
}

// startCleanupTimer starts a self-rescheduling background timer to remove stale entries
// from the enqueueStates map, preventing memory leaks from deleted CTPs.
func (r *PromotionStrategyReconciler) startCleanupTimer() {
	// Memory footprint per entry (64-bit system, measured with unsafe.Sizeof):
	//   - client.ObjectKey (2 strings): ~96 bytes
	//       * Struct: 32 bytes (2 string headers, 16 bytes each)
	//       * String content: namespace (32 chars) + name (32 chars) = 64 bytes
	//   - *ctpEnqueueState pointer: 8 bytes
	//   - ctpEnqueueState struct: 32 bytes
	//       * time.Time: 24 bytes
	//       * bool: 1 byte + 7 bytes padding = 8 bytes
	//   - Map overhead: ~8 bytes per entry
	//   Total: ~144 bytes per CTP
	//
	// Memory bounds (assuming 32-char namespace and name):
	//   - 100 stale entries = ~14 KB
	//   - 1,000 stale entries = ~144 KB
	//   - 10,000 stale entries = ~1.4 MB
	//
	// With 1 hour cleanup interval, worst case is 1 hour of deleted CTPs in memory.
	var scheduleCleanup func()
	scheduleCleanup = func() {
		time.AfterFunc(1*time.Hour, func() {
			r.enqueueStateMutex.Lock()
			for key, state := range r.enqueueStates {
				if time.Since(state.lastEnqueueTime) > 1*time.Hour {
					delete(r.enqueueStates, key)
				}
			}
			r.enqueueStateMutex.Unlock()
			scheduleCleanup() // Reschedule for next hour
		})
	}
	scheduleCleanup()
}

// handleRateLimitedEnqueue applies rate limiting to a CTP enqueue request.
// It checks if the CTP was enqueued recently and either:
// - Skips if a delayed retry is already scheduled
// - Schedules a delayed retry if within the rate limit threshold
// - Enqueues immediately if not rate limited
func (r *PromotionStrategyReconciler) handleRateLimitedEnqueue(
	ctx context.Context,
	ctp *promoterv1alpha1.ChangeTransferPolicy,
) {
	const enqueueThreshold = 15 * time.Second

	logger := log.FromContext(ctx)
	now := time.Now()
	key := client.ObjectKey{Namespace: ctp.Namespace, Name: ctp.Name}

	// Helper to get or create state for a CTP (must be called with lock held)
	getOrCreateState := func(key client.ObjectKey) *ctpEnqueueState {
		state := r.enqueueStates[key]
		if state == nil {
			state = &ctpEnqueueState{}
			r.enqueueStates[key] = state
		}
		return state
	}

	r.enqueueStateMutex.Lock()
	state := getOrCreateState(key)

	// Check if rate limited
	timeSinceLastEnqueue := now.Sub(state.lastEnqueueTime)
	if timeSinceLastEnqueue < enqueueThreshold {
		// Already have a delayed enqueue scheduled, nothing to do
		if state.hasScheduledRetry {
			r.enqueueStateMutex.Unlock()
			logger.V(4).Info("Rate limited, delayed enqueue already scheduled",
				"ctp", ctp.Name,
				"lastEnqueuedAgo", timeSinceLastEnqueue)
			return
		}

		// Schedule a delayed enqueue
		state.hasScheduledRetry = true
		timeUntilThreshold := enqueueThreshold - timeSinceLastEnqueue
		r.enqueueStateMutex.Unlock()

		logger.V(4).Info("Rate limited, scheduling delayed enqueue",
			"ctp", ctp.Name,
			"lastEnqueuedAgo", timeSinceLastEnqueue,
			"retryIn", timeUntilThreshold)

		time.AfterFunc(timeUntilThreshold, func() {
			r.enqueueStateMutex.Lock()
			state := getOrCreateState(key)

			// Update state and enqueue
			state.lastEnqueueTime = time.Now()
			state.hasScheduledRetry = false
			r.enqueueStateMutex.Unlock()

			if r.EnqueueCTP != nil {
				r.EnqueueCTP(key.Namespace, key.Name)
				logger.V(4).Info("Delayed enqueue succeeded",
					"ctp", key.Name)
			}
		})

		return
	}

	// Not rate limited - enqueue immediately
	state.lastEnqueueTime = now
	state.hasScheduledRetry = false
	r.enqueueStateMutex.Unlock()

	logger.V(4).Info("Enqueueing out-of-sync CTP",
		"ctp", ctp.Name)

	if r.EnqueueCTP != nil {
		r.EnqueueCTP(key.Namespace, key.Name)
	}
}

func (r *PromotionStrategyReconciler) createOrUpdatePreviousEnvironmentCommitStatus(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy, phase promoterv1alpha1.CommitStatusPhase, pendingReason string, previousEnvironmentBranch string, previousCRPCSPhases []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase) (*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	// TODO: do we like this name proposed-<name>?
	csName := utils.KubeSafeUniqueName(ctx, promoterv1alpha1.PreviousEnvProposedCommitPrefixNameLabel+ctp.Name)

	kind := reflect.TypeOf(promoterv1alpha1.ChangeTransferPolicy{}).Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)

	// If there is only one commit status, use the URL from that commit status.
	var url string
	if len(previousCRPCSPhases) == 1 {
		url = previousCRPCSPhases[0].Url
	}

	statusMap := make(map[string]string)
	for _, status := range previousCRPCSPhases {
		statusMap[status.Key] = status.Phase
	}
	yamlStatusMap, err := yaml.Marshal(statusMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal previous environment commit statuses: %w", err)
	}

	description := previousEnvironmentBranch + " - synced and healthy"
	if phase == promoterv1alpha1.CommitPhasePending && pendingReason != "" {
		description = pendingReason
	}

	// Build the apply configuration
	commitStatusApply := acv1alpha1.CommitStatus(csName, ctp.Namespace).
		WithLabels(map[string]string{
			promoterv1alpha1.CommitStatusLabel: promoterv1alpha1.PreviousEnvironmentCommitStatusKey,
		}).
		WithAnnotations(map[string]string{
			promoterv1alpha1.CommitStatusPreviousEnvironmentStatusesAnnotation: string(yamlStatusMap),
		}).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(ctp.Name).
			WithUID(ctp.UID).
			WithController(true).
			WithBlockOwnerDeletion(true)).
		WithSpec(acv1alpha1.CommitStatusSpec().
			WithRepositoryReference(acv1alpha1.ObjectReference().
				WithName(ctp.Spec.RepositoryReference.Name)).
			WithSha(ctp.Status.Proposed.Hydrated.Sha).
			WithName(previousEnvironmentBranch + " - synced and healthy").
			WithDescription(description).
			WithPhase(phase).
			WithUrl(url))

	// Apply using Server-Side Apply with Patch to get the result directly
	commitStatus := &promoterv1alpha1.CommitStatus{}
	commitStatus.Name = csName
	commitStatus.Namespace = ctp.Namespace
	if err = r.Patch(ctx, commitStatus, utils.ApplyPatch{ApplyConfig: commitStatusApply}, client.FieldOwner(constants.PromotionStrategyControllerFieldOwner), client.ForceOwnership); err != nil {
		return nil, fmt.Errorf("failed to apply previous environments CommitStatus: %w", err)
	}

	logger.V(4).Info("Applied previous environment CommitStatus")

	return commitStatus, nil
}

// updatePreviousEnvironmentCommitStatus checks if any environment is ready to be merged and if so, merges the pull request. It does this by looking at any active and proposed commit statuses.
// ps.Spec.Environments and ps.Status.Environments must be the same length and in the same order as ctps.
func (r *PromotionStrategyReconciler) updatePreviousEnvironmentCommitStatus(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, ctps []*promoterv1alpha1.ChangeTransferPolicy) error {
	logger := log.FromContext(ctx)
	// Go through each environment and copy any commit statuses from the previous environment if the previous environment's running dry commit is the same as the
	// currently processing environments proposed dry sha.
	// We then look at the status of the current environment and if all checks have passed and the environment is set to auto merge, we merge the pull request.
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(ctps))
	for i, ctp := range ctps {
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
		if ctp.Status.Active.Dry.Sha == ctp.Status.Proposed.Dry.Sha {
			logger.V(4).Info("Skipping previous environment commit status update - no proposed change in current environment",
				"activeBranch", ctp.Spec.ActiveBranch,
				"activeDrySha", ctp.Status.Active.Dry.Sha,
				"proposedDrySha", ctp.Status.Proposed.Dry.Sha,
				"previousEnvironmentActiveDrySha", previousEnvironmentStatus.Active.Dry.Sha,
				"currentEnvironmentActiveDrySha", ctp.Status.Proposed.Dry.Sha,
			)
			continue
		}

		// Determine which dry SHA the current environment's hydrator has processed.
		// The Note.DrySha (from git note) is the authoritative source because when manifests don't change
		// between dry commits, the hydrator may only update the git note without creating a new commit.
		// For legacy hydrators that don't use git notes, fall back to Proposed.Dry.Sha.
		currentEnvHydratedForDrySha := getEffectiveHydratedDrySha(currentEnvironmentStatus)

		// Pass all preceding environment statuses so we can look back past no-op hydrations
		precedingEnvStatuses := ps.Status.Environments[:i]

		// Recursively check ALL preceding environments to:
		// 1. Check that each has been hydrated for the same dry SHA
		// 2. Find the first environment that actually deployed this change (not a no-op)
		// 3. Check that environment's commit statuses
		//
		// This handles cases like dev -> staging -> prod where:
		// - A change affects dev and prod but staging is a no-op
		// - We need to ensure dev has been hydrated, promoted, AND is healthy before prod can promote
		isPending, pendingReason := isPreviousEnvironmentPending(precedingEnvStatuses, currentEnvHydratedForDrySha, currentEnvironmentStatus.Active.Dry.CommitTime)

		commitStatusPhase := promoterv1alpha1.CommitPhaseSuccess
		if isPending {
			commitStatusPhase = promoterv1alpha1.CommitPhasePending
		}

		logger.V(4).Info("Setting previous environment CommitStatus phase",
			"phase", commitStatusPhase,
			"pendingReason", pendingReason,
			"activeBranch", ctp.Spec.ActiveBranch,
			"proposedDrySha", ctp.Status.Proposed.Dry.Sha,
			"proposedHydratedSha", ctp.Status.Proposed.Hydrated.Sha,
			"previousEnvironmentActiveDrySha", previousEnvironmentStatus.Active.Dry.Sha,
			"previousEnvironmentActiveHydratedSha", previousEnvironmentStatus.Active.Hydrated.Sha,
			"previousEnvironmentProposedDrySha", previousEnvironmentStatus.Proposed.Dry.Sha,
			"previousEnvironmentProposedNoteSha", getNoteDrySha(previousEnvironmentStatus.Proposed.Note),
			"previousEnvironmentActiveBranch", previousEnvironmentStatus.Branch)

		// Since there is at least one configured active check, and since this is not the first environment,
		// we should not create a commit status for the previous environment.
		cs, err := r.createOrUpdatePreviousEnvironmentCommitStatus(ctx, ctp, commitStatusPhase, pendingReason, previousEnvironmentStatus.Branch, ctps[i-1].Status.Active.CommitStatuses)
		if err != nil {
			return fmt.Errorf("failed to create or update previous environment commit status for branch %s: %w", ctp.Spec.ActiveBranch, err)
		}
		commitStatuses = append(commitStatuses, cs)
	}

	utils.InheritNotReadyConditionFromObjects(ps, promoterConditions.PreviousEnvironmentCommitStatusNotReady, commitStatuses...)

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
// 3. If the environment is a no-op, recurse to check earlier environments
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

	// If this environment is NOT a no-op (i.e., it has real changes to deploy),
	// but it hasn't merged yet, we need to wait for it.
	if !envIsNoOp {
		return true, "Waiting for previous environment to be promoted"
	}

	// This environment is a no-op - recurse to check earlier environments
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
