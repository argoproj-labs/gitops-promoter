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
	"slices"
	"time"

	"gopkg.in/yaml.v3"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// PromotionStrategyReconciler reconciles a PromotionStrategy object
type PromotionStrategyReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	SettingsMgr *settings.Manager
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

	// Calculate the status of the PromotionStrategy. Updates ps in place.
	r.calculateStatus(&ps, ctps)

	err = r.updatePreviousEnvironmentCommitStatus(ctx, &ps, ctps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to merge PRs: %w", err)
	}

	err = r.Status().Update(ctx, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update PromotionStrategy status: %w", err)
	}

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

	pcName := utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(ps.Name, environment.Branch))

	// The code below sets the ownership for the Release Object
	kind := reflect.TypeOf(promoterv1alpha1.PromotionStrategy{}).Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)
	controllerRef := metav1.NewControllerRef(ps, gvk)

	ctp := promoterv1alpha1.ChangeTransferPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pcName,
			Namespace: ps.Namespace,
		},
	}
	res, err := controllerutil.CreateOrUpdate(ctx, r.Client, &ctp, func() error {
		ctp.OwnerReferences = []metav1.OwnerReference{*controllerRef}
		ctp.Labels = map[string]string{
			promoterv1alpha1.PromotionStrategyLabel: utils.KubeSafeLabel(ps.Name),
			promoterv1alpha1.EnvironmentLabel:       utils.KubeSafeLabel(environment.Branch),
		}
		ctp.Spec.RepositoryReference = ps.Spec.RepositoryReference
		ctp.Spec.ProposedBranch = fmt.Sprintf("%s-%s", environment.Branch, "next")
		ctp.Spec.ActiveBranch = environment.Branch

		ctp.Spec.ActiveCommitStatuses = make([]promoterv1alpha1.CommitStatusSelector, 0, len(environment.ActiveCommitStatuses)+len(ps.Spec.ActiveCommitStatuses))
		ctp.Spec.ActiveCommitStatuses = append(ctp.Spec.ActiveCommitStatuses, environment.ActiveCommitStatuses...)
		ctp.Spec.ActiveCommitStatuses = append(ctp.Spec.ActiveCommitStatuses, ps.Spec.ActiveCommitStatuses...)

		ctp.Spec.ProposedCommitStatuses = make([]promoterv1alpha1.CommitStatusSelector, 0, len(environment.ProposedCommitStatuses)+len(ps.Spec.ProposedCommitStatuses))
		ctp.Spec.ProposedCommitStatuses = append(ctp.Spec.ProposedCommitStatuses, environment.ProposedCommitStatuses...)
		ctp.Spec.ProposedCommitStatuses = append(ctp.Spec.ProposedCommitStatuses, ps.Spec.ProposedCommitStatuses...)

		ctp.Spec.AutoMerge = environment.AutoMerge

		environmentIndex, _ := utils.GetEnvironmentByBranch(*ps, environment.Branch)
		previousEnvironmentIndex := environmentIndex - 1
		if environmentIndex > 0 && len(ps.Spec.ActiveCommitStatuses) != 0 || (previousEnvironmentIndex >= 0 && len(ps.Spec.Environments[previousEnvironmentIndex].ActiveCommitStatuses) != 0) {
			previousEnvironmentCommitStatusSelector := promoterv1alpha1.CommitStatusSelector{
				Key: promoterv1alpha1.PreviousEnvironmentCommitStatusKey,
			}
			if !slices.Contains(ctp.Spec.ProposedCommitStatuses, previousEnvironmentCommitStatusSelector) {
				ctp.Spec.ProposedCommitStatuses = append(ctp.Spec.ProposedCommitStatuses, previousEnvironmentCommitStatusSelector)
			}
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create or update ChangeTransferPolicy %q: %w", ctp.Name, err)
	}
	logger.V(4).Info("CreateOrUpdate ChangeTransferPolicy result", "result", res)

	return &ctp, nil
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

func (r *PromotionStrategyReconciler) createOrUpdatePreviousEnvironmentCommitStatus(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy, phase promoterv1alpha1.CommitStatusPhase, pendingReason string, previousEnvironmentBranch string, previousCRPCSPhases []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase) (*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	// TODO: do we like this name proposed-<name>?
	csName := utils.KubeSafeUniqueName(ctx, promoterv1alpha1.PreviousEnvProposedCommitPrefixNameLabel+ctp.Name)
	proposedCSObjectKey := client.ObjectKey{Namespace: ctp.Namespace, Name: csName}

	kind := reflect.TypeOf(promoterv1alpha1.ChangeTransferPolicy{}).Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)
	controllerRef := metav1.NewControllerRef(ctp, gvk)

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

	commitStatus := &promoterv1alpha1.CommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proposedCSObjectKey.Name,
			Namespace: proposedCSObjectKey.Namespace,
		},
	}

	res, err := controllerutil.CreateOrUpdate(ctx, r.Client, commitStatus, func() error {
		commitStatus.Labels = map[string]string{
			promoterv1alpha1.CommitStatusLabel: promoterv1alpha1.PreviousEnvironmentCommitStatusKey,
		}
		commitStatus.Annotations = map[string]string{
			promoterv1alpha1.CommitStatusPreviousEnvironmentStatusesAnnotation: string(yamlStatusMap),
		}
		commitStatus.OwnerReferences = []metav1.OwnerReference{*controllerRef}
		commitStatus.Spec.RepositoryReference = ctp.Spec.RepositoryReference
		commitStatus.Spec.Sha = ctp.Status.Proposed.Hydrated.Sha
		commitStatus.Spec.Name = previousEnvironmentBranch + " - synced and healthy"
		commitStatus.Spec.Description = description
		commitStatus.Spec.Phase = phase
		commitStatus.Spec.Url = url
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create or update previous environments CommitStatus: %w", err)
	}
	logger.V(4).Info("CreateOrUpdate previous environment CommitStatus result", "result", res)

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

		isPending, pendingReason := isPreviousEnvironmentPending(previousEnvironmentStatus, currentEnvironmentStatus, ctp.Status.Proposed.Dry.Sha)

		commitStatusPhase := promoterv1alpha1.CommitPhaseSuccess
		if isPending {
			commitStatusPhase = promoterv1alpha1.CommitPhasePending
		}

		logger.V(4).Info("Setting previous environment CommitStatus phase",
			"phase", commitStatusPhase,
			"activeBranch", ctp.Spec.ActiveBranch,
			"proposedDrySha", ctp.Status.Proposed.Dry.Sha,
			"proposedHydratedSha", ctp.Status.Proposed.Hydrated.Sha,
			"previousEnvironmentActiveDrySha", previousEnvironmentStatus.Active.Dry.Sha,
			"previousEnvironmentActiveHydratedSha", previousEnvironmentStatus.Active.Hydrated.Sha,
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

// isPreviousEnvironmentPending returns whether the previous environment is pending and a reason string if it is pending.
func isPreviousEnvironmentPending(previousEnvironmentStatus, currentEnvironmentStatus promoterv1alpha1.EnvironmentStatus, proposedDrySha string) (isPending bool, reason string) {
	if previousEnvironmentStatus.Active.Dry.Sha != proposedDrySha {
		return true, "Waiting for previous environment's active commit to match proposed commit"
	}

	// The previous environment's dry commit time must be equal or newer than the current environment's dry commit
	// time. Basically, we can't move back in time.
	previousEnvironmentDryShaEqualOrNewer := previousEnvironmentStatus.Active.Dry.CommitTime.Equal(&metav1.Time{Time: currentEnvironmentStatus.Active.Dry.CommitTime.Time}) ||
		previousEnvironmentStatus.Active.Dry.CommitTime.After(currentEnvironmentStatus.Active.Dry.CommitTime.Time)

	if !previousEnvironmentDryShaEqualOrNewer {
		// This should basically never happen.
		return true, "Previous environment's commit is older than current environment's commit"
	}

	previousEnvironmentPassing := utils.AreCommitStatusesPassing(previousEnvironmentStatus.Active.CommitStatuses)

	if !previousEnvironmentPassing {
		if len(previousEnvironmentStatus.Active.CommitStatuses) == 1 {
			return true, fmt.Sprintf("Waiting for previous environment's %q commit status to be successful", previousEnvironmentStatus.Active.CommitStatuses[0].Key)
		}

		return true, "Waiting for previous environment's commit statuses to be successful"
	}

	return false, ""
}
