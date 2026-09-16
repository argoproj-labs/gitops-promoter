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
	"path"
	"reflect"
	"slices"
	"sync"
	"time"

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
	"github.com/argoproj-labs/gitops-promoter/internal/utils/ordercommitstatusgate"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	acmetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ctpEnqueueState tracks rate limiting for enqueuing out-of-sync CTPs.
type ctpEnqueueState struct {
	// lastEnqueueTime is when this CTP was last enqueued. It bounds enqueue spacing to
	// the threshold so a hot PromotionStrategy Owns loop cannot re-nudge the same CTP
	// every few milliseconds. Further retries come from a short PromotionStrategy
	// RequeueAfter while the disagreement lasts.
	lastEnqueueTime time.Time
}

const (
	// defaultEnqueueThreshold is the minimum spacing between EnqueueCTP calls for the
	// same lagging CTP, and the RequeueAfter used while any CTP still disagrees with
	// the batch target. Git notes have no SCM webhook, so a CTP fetch that finds
	// nothing usually does not change status and will not wake PromotionStrategy via
	// Owns; the short requeue is what looks again.
	defaultEnqueueThreshold = 15 * time.Second
)

// PromotionStrategyReconciler reconciles a PromotionStrategy object
type PromotionStrategyReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	RESTMapper  meta.RESTMapper
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager

	// EnqueueCTP is a function to enqueue CTP reconcile requests without modifying the CTP object.
	EnqueueCTP CTPEnqueueFunc

	// enqueueStates tracks rate limiting state for out-of-sync CTP enqueues.
	// Key is client.ObjectKey of the CTP. Protected by enqueueStateMutex.
	enqueueStates     map[client.ObjectKey]*ctpEnqueueState
	enqueueStateMutex sync.Mutex

	// enqueueThreshold is the minimum spacing between enqueues of the same CTP.
	// The zero value means the production default (defaultEnqueueThreshold); tests
	// override it so retry behavior can be exercised without real 15s waits.
	enqueueThreshold time.Duration
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=dependentssuccessfulcommitstatuses,verbs=get
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies/finalizers,verbs=update
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicies,verbs=get;list;watch;patch;create;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses,verbs=get;list;watch;patch;create
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=events,verbs=create;patch
//+kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

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
	// skipStatusWrite is set on the deletion fast-path below to suppress the deferred status
	// apply: the controller intentionally stops reconciling deleting objects, so patching
	// status (and emitting Ready events) for them is pure noise.
	skipStatusWrite := false
	// This function applies the resource status via Server-Side Apply at the end of the reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	defer func() {
		if skipStatusWrite {
			return
		}
		utils.HandleReconciliationResult(ctx, startTime, &ps, r.Client, r.Recorder, constants.PromotionStrategyControllerFieldOwner, &result, &err, &previousReady)
	}()

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
		skipStatusWrite = true
		logger.V(4).Info("PromotionStrategy is being deleted, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Remove any existing Ready condition. We want to start fresh.
	previousReady = utils.RemoveReadyCondition(&ps)

	// Safety check: resolve the ordering gate referenced by orderCommitStatusRef and verify it
	// points back at this PromotionStrategy.
	orderGateKey, err := ordercommitstatusgate.Resolve(ctx, r.Client, r.RESTMapper, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to resolve orderCommitStatusRef: %w", err)
	}

	if err := ensureControllerInstanceIDStable(ctx, r.SettingsMgr); err != nil {
		return ctrl.Result{}, err
	}

	// If a ChangeTransferPolicy does not exist, create it otherwise get it and store the ChangeTransferPolicy in a slice with the same order as ps.Spec.Environments.
	ctps := make([]*promoterv1alpha1.ChangeTransferPolicy, len(ps.Spec.Environments))
	for i, environment := range ps.Spec.Environments {
		var ctp *promoterv1alpha1.ChangeTransferPolicy
		ctp, err = r.upsertChangeTransferPolicy(ctx, &ps, environment, orderGateKey)
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

	// Check if any environments need to refresh their git notes.
	// SCM's do not send webhooks when git notes are pushed, so we need to
	// trigger CTP reconciliation when we detect stale NoteDrySha values.
	// This is done AFTER updating the PromotionStrategy status to avoid conflicts.
	// Only lagging CTPs are enqueued. If any still disagree, RequeueAfter is shortened
	// so we look again even when the CTP status did not change (Owns would not fire).
	hasDisagreement := r.enqueueOutOfSyncCTPs(ctx, ctps)

	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.PromotionStrategyConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration for PromotionStrategy %q: %w", ps.Name, err)
	}

	return ctrl.Result{
		RequeueAfter: r.requeueAfterForDisagreement(requeueDuration, hasDisagreement),
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PromotionStrategyReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(ctx, &promoterv1alpha1.CommitStatus{}, ".spec.sha", func(rawObj client.Object) []string {
		//nolint:forcetypeassert // type is guaranteed by the IndexField API
		cs := rawObj.(*promoterv1alpha1.CommitStatus)
		return []string{cs.Spec.Sha}
	}); err != nil {
		return fmt.Errorf("failed to set field index for .spec.sha: %w", err)
	}

	if err := RegisterGatePromotionStrategyRefFieldIndexes(ctx, mgr.GetFieldIndexer()); err != nil {
		return err
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

func (r *PromotionStrategyReconciler) upsertChangeTransferPolicy(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, environment promoterv1alpha1.Environment, orderGateKey string) (*promoterv1alpha1.ChangeTransferPolicy, error) {
	logger := log.FromContext(ctx)

	ctpName := utils.KubeSafeUniqueName(utils.GetChangeTransferPolicyName(ps.Name, environment.Branch))

	// Build owner reference
	kind := reflect.TypeFor[promoterv1alpha1.PromotionStrategy]().Name()
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
	if orderGateKey != "" && !slices.ContainsFunc(proposedCommitStatuses, func(sel *acv1alpha1.CommitStatusSelectorApplyConfiguration) bool {
		return sel.Key != nil && *sel.Key == orderGateKey
	}) {
		proposedCommitStatuses = append(proposedCommitStatuses, acv1alpha1.CommitStatusSelector().WithKey(orderGateKey))
	}

	activePath := ps.Spec.ActivePath
	if environment.ActivePath != "" {
		activePath = environment.ActivePath
	}

	proposedBranch := fmt.Sprintf("%s-%s", environment.Branch, "next")
	if activePath != "" {
		proposedBranch = path.Join(proposedBranch, activePath)
	}

	// Build the spec
	ctpSpec := acv1alpha1.ChangeTransferPolicySpec().
		WithRepositoryReference(acv1alpha1.ObjectReference().WithName(ps.Spec.RepositoryReference.Name)).
		WithProposedBranch(proposedBranch).
		WithActiveBranch(environment.Branch).
		WithActiveCommitStatuses(activeCommitStatuses...).
		WithProposedCommitStatuses(proposedCommitStatuses...)

	if activePath != "" {
		ctpSpec = ctpSpec.WithActivePath(activePath)
	}

	if environment.AutoMerge != nil {
		ctpSpec = ctpSpec.WithAutoMerge(*environment.AutoMerge)
	}

	if ps.Spec.PullRequest != nil {
		prPolicy := acv1alpha1.PullRequestPolicySpec()
		if ps.Spec.PullRequest.Labels != nil {
			prPolicy = prPolicy.WithLabels(
				acv1alpha1.ScmLabelsSpec().WithExpression(ps.Spec.PullRequest.Labels.Expression))
		}
		ctpSpec = ctpSpec.WithPullRequest(prPolicy)
	}

	// Build the apply configuration
	ctpLabels := utils.StampInstanceIDLabel(map[string]string{
		promoterv1alpha1.PromotionStrategyLabel: utils.KubeSafeLabel(ps.Name),
		promoterv1alpha1.EnvironmentLabel:       utils.KubeSafeLabel(environment.Branch),
	})
	ctpApply := acv1alpha1.ChangeTransferPolicy(ctpName, ps.Namespace).
		WithLabels(ctpLabels).
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
//
// Target selection: the target is the effective dry SHA of the CTP with the newest
// proposed hydrated commit — the environment with the freshest knowledge of hydrator
// output. Using the effective (note-preferred) SHA rather than the hydrator.metadata file
// means a no-op hydration (git note updated to a newer dry SHA without a new commit)
// moves the target too, so environments whose notes lag behind a sibling's are the ones
// nudged, and a batch where every note already agrees is left alone.
//
// A lagging CTP is EnqueueCTP'd at most once per threshold. The return value is
// whether any CTP still disagrees with the batch target; Reconcile uses that to
// RequeueAfter at the threshold until the notes converge.
func (r *PromotionStrategyReconciler) enqueueOutOfSyncCTPs(ctx context.Context, ctps []*promoterv1alpha1.ChangeTransferPolicy) bool {
	if len(ctps) == 0 {
		return false
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

	// Get the effective proposed dry SHA for each CTP (Note.DrySha if set, else Proposed.Dry.Sha).
	getEffectiveProposedDrySha := func(ctp *promoterv1alpha1.ChangeTransferPolicy) string {
		if ctp.Status.Proposed.Note != nil && ctp.Status.Proposed.Note.DrySha != "" {
			return ctp.Status.Proposed.Note.DrySha
		}
		return ctp.Status.Proposed.Dry.Sha
	}

	// Find the newest effective proposed dry SHA — from the CTP with the newest proposed
	// hydrated commit. CTPs whose own effective proposed dry SHA doesn't match need to
	// reconcile to fetch the updated git note. A single-environment strategy can never
	// disagree with itself: its own effective SHA is the batch target.
	var newestEffectiveProposedDrySha string
	var newestTime metav1.Time
	for _, ctp := range ctps {
		ctpEffectiveProposedDrySha := getEffectiveProposedDrySha(ctp)
		if ctpEffectiveProposedDrySha == "" {
			continue
		}
		commitTime := ctp.Status.Proposed.Hydrated.CommitTime
		if newestEffectiveProposedDrySha == "" || commitTime.After(newestTime.Time) {
			newestEffectiveProposedDrySha = ctpEffectiveProposedDrySha
			newestTime = commitTime
		}
	}

	if newestEffectiveProposedDrySha == "" {
		return false
	}

	hasDisagreement := false
	for _, ctp := range ctps {
		ctpEffectiveProposedDrySha := getEffectiveProposedDrySha(ctp)
		if ctpEffectiveProposedDrySha == newestEffectiveProposedDrySha {
			continue
		}

		hasDisagreement = true
		ctxWithLog := log.IntoContext(ctx, log.FromContext(ctx).WithValues(
			"ctpEffectiveProposedDrySha", ctpEffectiveProposedDrySha,
			"newestEffectiveProposedDrySha", newestEffectiveProposedDrySha,
		))
		r.handleRateLimitedEnqueue(ctxWithLog, ctp)
	}
	return hasDisagreement
}

// requeueAfterForDisagreement keeps PromotionStrategy on the enqueue threshold while
// any CTP still disagrees, so retries continue without an in-process AfterFunc chain.
// A configured workQueue.requeueDuration shorter than the threshold is left as-is.
func (r *PromotionStrategyReconciler) requeueAfterForDisagreement(configured time.Duration, hasDisagreement bool) time.Duration {
	if !hasDisagreement {
		return configured
	}
	threshold := r.enqueueThresholdOrDefault()
	if configured > 0 && configured < threshold {
		return configured
	}
	return threshold
}

func (r *PromotionStrategyReconciler) enqueueThresholdOrDefault() time.Duration {
	if r.enqueueThreshold > 0 {
		return r.enqueueThreshold
	}
	return defaultEnqueueThreshold
}

// startCleanupTimer starts a self-rescheduling background timer to remove stale entries
// from the enqueueStates map, preventing memory leaks from deleted CTPs.
func (r *PromotionStrategyReconciler) startCleanupTimer() {
	// Memory footprint per entry (64-bit system, measured with unsafe.Sizeof):
	//   - client.ObjectKey (2 strings): ~96 bytes
	//       * Struct: 32 bytes (2 string headers, 16 bytes each)
	//       * String content: namespace (32 chars) + name (32 chars) = 64 bytes
	//   - *ctpEnqueueState pointer: 8 bytes
	//   - ctpEnqueueState struct: ~24 bytes (time.Time)
	//   - Map overhead: ~8 bytes per entry
	//   Total: ~140 bytes per CTP.
	//
	// Memory bounds (assuming 32-char namespace and name):
	//   - 100 stale entries = ~14 KB
	//   - 1,000 stale entries = ~140 KB
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

// handleRateLimitedEnqueue nudges a lagging CTP to reconcile, at most once per
// threshold (15s by default). Repeats after the window come from the next
// PromotionStrategy reconcile (short RequeueAfter while disagreement lasts).
func (r *PromotionStrategyReconciler) handleRateLimitedEnqueue(
	ctx context.Context,
	ctp *promoterv1alpha1.ChangeTransferPolicy,
) {
	enqueueThreshold := r.enqueueThresholdOrDefault()

	logger := log.FromContext(ctx)
	key := client.ObjectKey{Namespace: ctp.Namespace, Name: ctp.Name}

	r.enqueueStateMutex.Lock()
	state := r.getOrCreateState(key)
	rateLimited := time.Since(state.lastEnqueueTime) < enqueueThreshold
	r.enqueueStateMutex.Unlock()

	if rateLimited {
		logger.V(4).Info("Enqueue skipped, within threshold", "ctp", ctp.Name)
		return
	}
	logger.V(4).Info("Enqueueing out-of-sync CTP", "ctp", ctp.Name)
	r.enqueue(key)
}

// getOrCreateState returns the enqueue state for key, creating it if absent. Callers must
// hold enqueueStateMutex.
func (r *PromotionStrategyReconciler) getOrCreateState(key client.ObjectKey) *ctpEnqueueState {
	state := r.enqueueStates[key]
	if state == nil {
		state = &ctpEnqueueState{}
		r.enqueueStates[key] = state
	}
	return state
}

// enqueue records the enqueue time and triggers a CTP reconcile. Callers must NOT hold
// enqueueStateMutex.
func (r *PromotionStrategyReconciler) enqueue(key client.ObjectKey) {
	r.enqueueStateMutex.Lock()
	r.getOrCreateState(key).lastEnqueueTime = time.Now()
	r.enqueueStateMutex.Unlock()

	if r.EnqueueCTP != nil {
		r.EnqueueCTP(key.Namespace, key.Name)
	}
}
