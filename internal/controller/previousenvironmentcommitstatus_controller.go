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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	acmetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

const (
	// PreviousEnvironmentCommitStatusControllerFieldOwner is the field owner for Server-Side Apply operations
	// performed by the PreviousEnvironmentCommitStatus controller.
	PreviousEnvironmentCommitStatusControllerFieldOwner = "promoter.argoproj.io/previousenvironmentcommitstatus-controller"
)

// PreviousEnvironmentCommitStatusReconciler reconciles a PreviousEnvironmentCommitStatus object
type PreviousEnvironmentCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager
	EnqueueCTP  CTPEnqueueFunc
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=previousenvironmentcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=previousenvironmentcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=previousenvironmentcommitstatuses/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *PreviousEnvironmentCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling PreviousEnvironmentCommitStatus")
	startTime := time.Now()

	var pecs promoterv1alpha1.PreviousEnvironmentCommitStatus
	// This function will update the resource status at the end of the reconciliation. Don't call .Status().Update manually.
	defer utils.HandleReconciliationResult(ctx, startTime, &pecs, r.Client, r.Recorder, &err)

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
	meta.RemoveStatusCondition(pecs.GetConditions(), string(promoterConditions.Ready))

	// 2. Fetch the referenced PromotionStrategy
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{
		Namespace: pecs.Namespace,
		Name:      pecs.Spec.PromotionStrategyRef.Name,
	}
	err = r.Get(ctx, psKey, &ps)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// The PromotionStrategy is deleted. Since the PECS is owned by the PS,
			// it will be garbage collected. Stop reconciling.
			logger.V(4).Info("PromotionStrategy not found, PECS will be garbage collected", "promotionStrategy", pecs.Spec.PromotionStrategyRef.Name)
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get PromotionStrategy")
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", pecs.Spec.PromotionStrategyRef.Name, err)
	}

	// 3. Process each environment and create/update CommitStatuses
	transitionedEnvironments, commitStatuses, err := r.processEnvironments(ctx, &pecs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to process environments: %w", err)
	}

	// 4. Clean up orphaned CommitStatus resources that are no longer in the environment list
	err = r.cleanupOrphanedCommitStatuses(ctx, &pecs, commitStatuses)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to cleanup orphaned CommitStatus resources: %w", err)
	}

	// 5. Inherit conditions from CommitStatus objects
	utils.InheritNotReadyConditionFromObjects(&pecs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	// 6. If any gates transitioned to success, touch the corresponding ChangeTransferPolicies to trigger reconciliation
	if len(transitionedEnvironments) > 0 {
		r.touchChangeTransferPolicies(ctx, &ps, transitionedEnvironments)
	}

	// Get requeue duration from settings
	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.PromotionStrategyConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		logger.Error(err, "failed to get requeue duration, using 5 minutes")
		requeueDuration = 5 * time.Minute
	}

	return ctrl.Result{
		RequeueAfter: requeueDuration,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PreviousEnvironmentCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.PromotionStrategyConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get PreviousEnvironmentCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.PromotionStrategyConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get PreviousEnvironmentCommitStatus max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.PreviousEnvironmentCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&promoterv1alpha1.CommitStatus{}).
		Watches(&promoterv1alpha1.PromotionStrategy{}, r.enqueueForPromotionStrategy()).
		Watches(&promoterv1alpha1.ChangeTransferPolicy{}, r.enqueueForChangeTransferPolicy()).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// processEnvironments processes each environment in the spec and creates/updates CommitStatus resources.
// Returns a list of environment branches that transitioned from pending to success and the CommitStatus objects.
func (r *PreviousEnvironmentCommitStatusReconciler) processEnvironments(ctx context.Context, pecs *promoterv1alpha1.PreviousEnvironmentCommitStatus, ps *promoterv1alpha1.PromotionStrategy) ([]string, []*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	// Track which environments transitioned to success
	transitionedEnvironments := []string{}
	// Track all CommitStatus objects created/updated
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0)

	// Save the previous status before clearing it, so we can detect transitions
	previousStatus := pecs.Status.DeepCopy()
	if previousStatus == nil {
		previousStatus = &promoterv1alpha1.PreviousEnvironmentCommitStatusStatus{}
	}

	// Build maps for efficient lookups
	envStatusMap := make(map[string]*promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for i := range ps.Status.Environments {
		envStatusMap[ps.Status.Environments[i].Branch] = &ps.Status.Environments[i]
	}

	// Build a map of CTPs by branch
	ctpMap, err := r.getChangeTransferPolicies(ctx, ps)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get ChangeTransferPolicies: %w", err)
	}

	// Initialize status environments
	pecs.Status.Environments = make([]promoterv1alpha1.PreviousEnvEnvironmentStatus, 0)

	// Process each environment (except the first one which has no previous environment)
	for i, envConfig := range pecs.Spec.Environments {
		if i == 0 {
			// Skip first environment - it has no previous environment to check
			continue
		}

		// Check if there are any active commit statuses configured
		previousEnvConfig := pecs.Spec.Environments[i-1]
		hasActiveCommitStatuses := len(envConfig.ActiveCommitStatuses) > 0 || len(previousEnvConfig.ActiveCommitStatuses) > 0

		// Also check PromotionStrategy-level active commit statuses
		if !hasActiveCommitStatuses {
			hasActiveCommitStatuses = len(ps.Spec.ActiveCommitStatuses) > 0
		}

		if !hasActiveCommitStatuses {
			// No active commit statuses configured, skip this environment
			continue
		}

		// Get the CTP for this environment
		ctp, found := ctpMap[envConfig.Branch]
		if !found {
			logger.Info("ChangeTransferPolicy not found for environment", "branch", envConfig.Branch)
			continue
		}

		// Get environment statuses
		currentEnvStatus, found := envStatusMap[envConfig.Branch]
		if !found {
			logger.Info("Environment status not found", "branch", envConfig.Branch)
			continue
		}

		previousEnvStatus, found := envStatusMap[previousEnvConfig.Branch]
		if !found {
			logger.Info("Previous environment status not found", "branch", previousEnvConfig.Branch)
			continue
		}

		// Skip if there's no proposed change in the current environment
		if ctp.Status.Active.Dry.Sha == ctp.Status.Proposed.Dry.Sha {
			logger.V(4).Info("Skipping - no proposed change in current environment",
				"branch", envConfig.Branch,
				"activeDrySha", ctp.Status.Active.Dry.Sha,
				"proposedDrySha", ctp.Status.Proposed.Dry.Sha)
			continue
		}

		// Calculate if previous environment is healthy
		isPending, pendingReason := isPreviousEnvironmentPending(*previousEnvStatus, *currentEnvStatus)

		phase := promoterv1alpha1.CommitPhaseSuccess
		if isPending {
			phase = promoterv1alpha1.CommitPhasePending
		}

		logger.V(4).Info("Setting previous environment CommitStatus phase",
			"phase", phase,
			"pendingReason", pendingReason,
			"branch", envConfig.Branch,
			"previousBranch", previousEnvConfig.Branch)

		// Check if this gate transitioned to success
		var previousPhase string
		for _, prevEnv := range previousStatus.Environments {
			if prevEnv.Branch == envConfig.Branch {
				previousPhase = prevEnv.Phase
				break
			}
		}
		if previousPhase == string(promoterv1alpha1.CommitPhasePending) && phase == promoterv1alpha1.CommitPhaseSuccess {
			transitionedEnvironments = append(transitionedEnvironments, envConfig.Branch)
			logger.Info("Previous environment gate transitioned to success",
				"branch", envConfig.Branch,
				"previousBranch", previousEnvConfig.Branch)
		}

		// Get the previous environment's commit statuses for the annotation
		var previousCTPCommitStatuses []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase
		previousCTP, found := ctpMap[previousEnvConfig.Branch]
		if found {
			previousCTPCommitStatuses = previousCTP.Status.Active.CommitStatuses
		}

		// Create/update the CommitStatus
		cs, err := r.upsertCommitStatus(ctx, pecs, ctp, phase, pendingReason, previousEnvConfig.Branch, previousCTPCommitStatuses)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to upsert CommitStatus for environment %q: %w", envConfig.Branch, err)
		}
		commitStatuses = append(commitStatuses, cs)

		// Update status
		pecs.Status.Environments = append(pecs.Status.Environments, promoterv1alpha1.PreviousEnvEnvironmentStatus{
			Branch:                    envConfig.Branch,
			CommitStatusName:          cs.Name,
			Phase:                     string(phase),
			PreviousEnvironmentBranch: previousEnvConfig.Branch,
			Sha:                       ctp.Status.Proposed.Hydrated.Sha,
		})
	}

	return transitionedEnvironments, commitStatuses, nil
}

// getChangeTransferPolicies returns a map of CTPs by branch for the given PromotionStrategy
func (r *PreviousEnvironmentCommitStatusReconciler) getChangeTransferPolicies(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy) (map[string]*promoterv1alpha1.ChangeTransferPolicy, error) {
	var ctpList promoterv1alpha1.ChangeTransferPolicyList
	err := r.List(ctx, &ctpList, client.InNamespace(ps.Namespace), client.MatchingLabels{
		promoterv1alpha1.PromotionStrategyLabel: utils.KubeSafeLabel(ps.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list ChangeTransferPolicies: %w", err)
	}

	ctpMap := make(map[string]*promoterv1alpha1.ChangeTransferPolicy, len(ctpList.Items))
	for i := range ctpList.Items {
		ctpMap[ctpList.Items[i].Spec.ActiveBranch] = &ctpList.Items[i]
	}

	return ctpMap, nil
}

// upsertCommitStatus creates or updates a CommitStatus for the previous environment check
func (r *PreviousEnvironmentCommitStatusReconciler) upsertCommitStatus(ctx context.Context, pecs *promoterv1alpha1.PreviousEnvironmentCommitStatus, ctp *promoterv1alpha1.ChangeTransferPolicy, phase promoterv1alpha1.CommitStatusPhase, pendingReason string, previousEnvironmentBranch string, previousCRPCSPhases []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase) (*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	// Generate a consistent name for the CommitStatus
	csName := utils.KubeSafeUniqueName(ctx, promoterv1alpha1.PreviousEnvProposedCommitPrefixNameLabel+ctp.Name)

	// Build owner reference to PECS
	kind := reflect.TypeOf(promoterv1alpha1.PreviousEnvironmentCommitStatus{}).Name()
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
	commitStatusApply := acv1alpha1.CommitStatus(csName, pecs.Namespace).
		WithLabels(map[string]string{
			promoterv1alpha1.CommitStatusLabel: promoterv1alpha1.PreviousEnvironmentCommitStatusKey,
		}).
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
				WithName(ctp.Spec.RepositoryReference.Name)).
			WithSha(ctp.Status.Proposed.Hydrated.Sha).
			WithName(previousEnvironmentBranch + " - synced and healthy").
			WithDescription(description).
			WithPhase(phase).
			WithUrl(url))

	// Apply using Server-Side Apply with Patch to get the result directly
	commitStatus := &promoterv1alpha1.CommitStatus{}
	commitStatus.Name = csName
	commitStatus.Namespace = pecs.Namespace
	if err = r.Patch(ctx, commitStatus, utils.ApplyPatch{ApplyConfig: commitStatusApply}, client.FieldOwner(PreviousEnvironmentCommitStatusControllerFieldOwner), client.ForceOwnership); err != nil {
		return nil, fmt.Errorf("failed to apply CommitStatus: %w", err)
	}

	logger.V(4).Info("Applied previous environment CommitStatus",
		"commitStatus", csName,
		"phase", phase)

	return commitStatus, nil
}

// cleanupOrphanedCommitStatuses deletes CommitStatus resources that are owned by this PreviousEnvironmentCommitStatus
// but are not in the current list of valid CommitStatus resources.
func (r *PreviousEnvironmentCommitStatusReconciler) cleanupOrphanedCommitStatuses(ctx context.Context, pecs *promoterv1alpha1.PreviousEnvironmentCommitStatus, validCommitStatuses []*promoterv1alpha1.CommitStatus) error {
	logger := log.FromContext(ctx)

	// Create a set of valid CommitStatus names for quick lookup
	validCommitStatusNames := make(map[string]bool)
	for _, cs := range validCommitStatuses {
		validCommitStatusNames[cs.Name] = true
	}

	// List all previous-environment CommitStatus resources in the namespace
	var commitStatusList promoterv1alpha1.CommitStatusList
	err := r.List(ctx, &commitStatusList, client.InNamespace(pecs.Namespace), client.MatchingLabels{
		promoterv1alpha1.CommitStatusLabel: promoterv1alpha1.PreviousEnvironmentCommitStatusKey,
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

		// Verify this CommitStatus is owned by this PreviousEnvironmentCommitStatus before deleting
		if !metav1.IsControlledBy(&cs, pecs) {
			logger.V(4).Info("Skipping CommitStatus not owned by this PreviousEnvironmentCommitStatus",
				"commitStatusName", cs.Name,
				"previousEnvironmentCommitStatus", pecs.Name)
			continue
		}

		// Delete the orphaned CommitStatus
		logger.Info("Deleting orphaned CommitStatus",
			"commitStatusName", cs.Name,
			"previousEnvironmentCommitStatus", pecs.Name,
			"namespace", pecs.Namespace)

		if err := r.Delete(ctx, &cs); err != nil {
			if k8serrors.IsNotFound(err) {
				// Already deleted, which is fine
				logger.V(4).Info("CommitStatus already deleted", "commitStatusName", cs.Name)
				continue
			}
			return fmt.Errorf("failed to delete orphaned CommitStatus %q: %w", cs.Name, err)
		}

		r.Recorder.Eventf(pecs, nil, "Normal", constants.OrphanedCommitStatusDeletedReason, "CleaningOrphanedResources", constants.OrphanedCommitStatusDeletedMessage, cs.Name)
	}

	return nil
}

// touchChangeTransferPolicies triggers reconciliation of the ChangeTransferPolicies
// for the environments that had gates transition to success.
func (r *PreviousEnvironmentCommitStatusReconciler) touchChangeTransferPolicies(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, transitionedEnvironments []string) {
	logger := log.FromContext(ctx)

	for _, envBranch := range transitionedEnvironments {
		ctpName := utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(ps.Name, envBranch))

		logger.Info("Triggering ChangeTransferPolicy reconciliation due to previous environment gate transition",
			"changeTransferPolicy", ctpName,
			"branch", envBranch)

		if r.EnqueueCTP != nil {
			r.EnqueueCTP(ps.Namespace, ctpName)
		}
	}
}

// enqueueForPromotionStrategy returns a handler that enqueues all PreviousEnvironmentCommitStatus resources
// that reference a PromotionStrategy when that PromotionStrategy changes
func (r *PreviousEnvironmentCommitStatusReconciler) enqueueForPromotionStrategy() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
		if !ok {
			return nil
		}

		// List all PreviousEnvironmentCommitStatus resources in the same namespace
		var pecsList promoterv1alpha1.PreviousEnvironmentCommitStatusList
		if err := r.List(ctx, &pecsList, client.InNamespace(ps.Namespace)); err != nil {
			log.FromContext(ctx).Error(err, "failed to list PreviousEnvironmentCommitStatus resources")
			return nil
		}

		// Enqueue all PreviousEnvironmentCommitStatus resources that reference this PromotionStrategy
		var requests []ctrl.Request
		for _, pecs := range pecsList.Items {
			if pecs.Spec.PromotionStrategyRef.Name == ps.Name {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKeyFromObject(&pecs),
				})
			}
		}

		return requests
	})
}

// enqueueForChangeTransferPolicy returns a handler that enqueues PreviousEnvironmentCommitStatus resources
// when a ChangeTransferPolicy changes
func (r *PreviousEnvironmentCommitStatusReconciler) enqueueForChangeTransferPolicy() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ctp, ok := obj.(*promoterv1alpha1.ChangeTransferPolicy)
		if !ok {
			return nil
		}

		// Get the PromotionStrategy label from the CTP
		psName, found := ctp.Labels[promoterv1alpha1.PromotionStrategyLabel]
		if !found {
			return nil
		}

		// List all PreviousEnvironmentCommitStatus resources in the same namespace
		var pecsList promoterv1alpha1.PreviousEnvironmentCommitStatusList
		if err := r.List(ctx, &pecsList, client.InNamespace(ctp.Namespace)); err != nil {
			log.FromContext(ctx).Error(err, "failed to list PreviousEnvironmentCommitStatus resources")
			return nil
		}

		// Enqueue all PreviousEnvironmentCommitStatus resources that reference the same PromotionStrategy
		var requests []ctrl.Request
		for _, pecs := range pecsList.Items {
			if utils.KubeSafeLabel(pecs.Spec.PromotionStrategyRef.Name) == psName {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKeyFromObject(&pecs),
				})
			}
		}

		return requests
	})
}

// getNoteDrySha safely returns the DrySha from a HydratorMetadata pointer, or empty string if nil.
func getNoteDrySha(note *promoterv1alpha1.HydratorMetadata) string {
	if note == nil {
		return ""
	}
	return note.DrySha
}

// isPreviousEnvironmentPending returns whether the previous environment is pending and a reason string if it is pending.
func isPreviousEnvironmentPending(previousEnvironmentStatus, currentEnvironmentStatus promoterv1alpha1.EnvironmentStatus) (isPending bool, reason string) {
	previousEnvProposedNoteSha := getNoteDrySha(previousEnvironmentStatus.Proposed.Note)
	previousEnvProposedDrySha := previousEnvironmentStatus.Proposed.Dry.Sha

	// Determine which dry SHA each environment's hydrator has processed.
	// The Note.DrySha (from git note) is the authoritative source because when manifests don't change
	// between dry commits, the hydrator may only update the git note without creating a new commit.
	// In that case, hydrator.metadata (Proposed.Dry.Sha) still has the old SHA, but the git note
	// confirms hydration is complete for the new dry SHA.
	// For legacy hydrators that don't use git notes, fall back to Proposed.Dry.Sha.
	previousEnvHydratedForDrySha := previousEnvProposedNoteSha
	if previousEnvHydratedForDrySha == "" {
		previousEnvHydratedForDrySha = previousEnvProposedDrySha
	}
	currentEnvHydratedForDrySha := getNoteDrySha(currentEnvironmentStatus.Proposed.Note)
	if currentEnvHydratedForDrySha == "" {
		currentEnvHydratedForDrySha = currentEnvironmentStatus.Proposed.Dry.Sha
	}

	// Check if hydrator has processed the same dry SHA as the current environment.
	if previousEnvHydratedForDrySha != currentEnvHydratedForDrySha {
		return true, "Waiting for the hydrator to finish processing the proposed dry commit"
	}

	// Check if the previous environment has completed its promotion.
	// There are two ways promotion can be "complete":
	//
	// 1. prMerged: A PR was created and merged, so Active.Dry.Sha now matches the target.
	//
	// 2. noOpHydration: The hydrator determined the manifests were unchanged between the
	//    old and new dry commits, so it only updated the git note (Note.DrySha) without creating
	//    a new hydrated commit. We detect this by comparing:
	//    - previousEnvHydratedForDrySha: The dry SHA the hydrator has processed (from Note.DrySha)
	//    - previousEnvProposedDrySha: The dry SHA in hydrator.metadata (Proposed.Dry.Sha)
	//    When these differ, it means the git note was updated to a newer dry SHA, but
	//    hydrator.metadata still has the old value because no new commit was created.
	//    In this case, there's no PR to merge, so we shouldn't block waiting for one.
	//
	prMerged := previousEnvironmentStatus.Active.Dry.Sha == currentEnvHydratedForDrySha
	noOpHydration := previousEnvProposedDrySha != previousEnvHydratedForDrySha
	promotionComplete := prMerged || noOpHydration
	if !promotionComplete {
		return true, "Waiting for previous environment to be promoted"
	}

	// Only check commit times if the previous environment actually merged the exact SHA (not no-op).
	prWasMerged := previousEnvironmentStatus.Active.Dry.Sha == currentEnvHydratedForDrySha
	if prWasMerged {
		previousEnvironmentDryShaEqualOrNewer := previousEnvironmentStatus.Active.Dry.CommitTime.Equal(&metav1.Time{Time: currentEnvironmentStatus.Active.Dry.CommitTime.Time}) ||
			previousEnvironmentStatus.Active.Dry.CommitTime.After(currentEnvironmentStatus.Active.Dry.CommitTime.Time)
		if !previousEnvironmentDryShaEqualOrNewer {
			// This should basically never happen.
			return true, "Previous environment's commit is older than current environment's commit"
		}
	}

	// Finally, check that the previous environment's commit statuses are passing.
	previousEnvironmentPassing := utils.AreCommitStatusesPassing(previousEnvironmentStatus.Active.CommitStatuses)
	if !previousEnvironmentPassing {
		if len(previousEnvironmentStatus.Active.CommitStatuses) == 1 {
			return true, fmt.Sprintf("Waiting for previous environment's %q commit status to be successful", previousEnvironmentStatus.Active.CommitStatuses[0].Key)
		}
		return true, "Waiting for previous environment's commit statuses to be successful"
	}

	return false, ""
}
