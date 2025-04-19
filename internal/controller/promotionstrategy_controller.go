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

	"k8s.io/client-go/util/retry"

	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"k8s.io/apimachinery/pkg/api/errors"
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

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *PromotionStrategyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling PromotionStrategy")
	startTime := time.Now()

	var ps promoterv1alpha1.PromotionStrategy
	err := r.Get(ctx, req.NamespacedName, &ps, &client.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("PromotionStrategy not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get PromotionStrategy")
		return ctrl.Result{}, fmt.Errorf("failed to get PromitionStrategy %q: %w", req.Name, err)
	}

	// If a ChangeTransferPolicy does not exist, create it otherwise get it and store the ChangeTransferPolicy in a map with the branch as the key.
	ctpsByBranch := make(map[string]*promoterv1alpha1.ChangeTransferPolicy, len(ps.Spec.Environments))
	for _, environment := range ps.Spec.Environments {
		var ctp *promoterv1alpha1.ChangeTransferPolicy
		ctp, err = r.upsertChangeTransferPolicy(ctx, &ps, environment)
		if err != nil {
			logger.Error(err, "failed to upsert ChangeTransferPolicy")
			return ctrl.Result{}, fmt.Errorf("failed to create ChangeTransferPolicy for branch %q: %w", environment.Branch, err)
		}
		ctpsByBranch[environment.Branch] = ctp
	}

	// Calculate the status of the PromotionStrategy
	err = r.calculateStatus(&ps, ctpsByBranch)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to calculate PromotionStrategy status: %w", err)
	}

	err = r.updatePreviousEnvironmentCommitStatus(ctx, &ps, ctpsByBranch)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to merge PRs: %w", err)
	}

	err = r.Status().Update(ctx, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update PromotionStrategy status: %w", err)
	}

	logger.Info("Reconciling PromotionStrategy End", "duration", time.Since(startTime))

	requeueDuration, err := r.SettingsMgr.GetPromotionStrategyRequeueDuration(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration for PromotionStrategy %q: %w", ps.Name, err)
	}

	return ctrl.Result{
		Requeue:      true,
		RequeueAfter: requeueDuration,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PromotionStrategyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &promoterv1alpha1.CommitStatus{}, ".spec.sha", func(rawObj client.Object) []string {
		//nolint:forcetypeassert
		cs := rawObj.(*promoterv1alpha1.CommitStatus)
		return []string{cs.Spec.Sha}
	}); err != nil {
		return fmt.Errorf("failed to set field index for .spec.sha: %w", err)
	}

	err := ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.PromotionStrategy{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&promoterv1alpha1.ChangeTransferPolicy{}).
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

	pcNew := promoterv1alpha1.ChangeTransferPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:            pcName,
			Namespace:       ps.Namespace,
			OwnerReferences: []metav1.OwnerReference{*controllerRef},
			Labels: map[string]string{
				promoterv1alpha1.PromotionStrategyLabel: utils.KubeSafeLabel(ctx, ps.Name),
				promoterv1alpha1.EnvironmentLabel:       utils.KubeSafeLabel(ctx, environment.Branch),
			},
		},
		Spec: promoterv1alpha1.ChangeTransferPolicySpec{
			RepositoryReference:    ps.Spec.RepositoryReference,
			ProposedBranch:         fmt.Sprintf("%s-%s", environment.Branch, "next"),
			ActiveBranch:           environment.Branch,
			ActiveCommitStatuses:   append(environment.ActiveCommitStatuses, ps.Spec.ActiveCommitStatuses...),
			ProposedCommitStatuses: append(environment.ProposedCommitStatuses, ps.Spec.ProposedCommitStatuses...),
			AutoMerge:              environment.AutoMerge,
		},
	}

	previousEnvironmentIndex, _ := utils.GetPreviousEnvironmentStatusByBranch(*ps, environment.Branch)
	environmentIndex, _ := utils.GetEnvironmentByBranch(*ps, environment.Branch)
	if environmentIndex > 0 && len(ps.Spec.ActiveCommitStatuses) != 0 || (previousEnvironmentIndex >= 0 && len(ps.Spec.Environments[previousEnvironmentIndex].ActiveCommitStatuses) != 0) {
		previousEnvironmentCommitStatusSelector := promoterv1alpha1.CommitStatusSelector{
			Key: promoterv1alpha1.PreviousEnvironmentCommitStatusKey,
		}
		if !slices.Contains(pcNew.Spec.ProposedCommitStatuses, previousEnvironmentCommitStatusSelector) {
			pcNew.Spec.ProposedCommitStatuses = append(pcNew.Spec.ProposedCommitStatuses, previousEnvironmentCommitStatusSelector)
		}
	}

	pc := promoterv1alpha1.ChangeTransferPolicy{}
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		// We own the CTP so what we say is way we want, this forces our changes on the CTP even if there is conflict because
		// we have the correct state. Server side apply would help here but controller-runtime does not support it yet.
		// This could be a patch as well.
		err := r.Get(ctx, client.ObjectKey{Name: pcName, Namespace: ps.Namespace}, &pc)
		if err != nil {
			if errors.IsNotFound(err) {
				logger.Info("ChangeTransferPolicy not found, creating")
				err = r.Create(ctx, &pcNew)
				if err != nil {
					return fmt.Errorf("failed to create ChangeTransferPolicy %q: %w", pc.Name, err)
				}
				pcNew.DeepCopyInto(&pc)
			} else {
				return fmt.Errorf("failed to get ChangeTransferPolicy %q: %w", pc.Name, err)
			}
		} else {
			pcNew.Spec.DeepCopyInto(&pc.Spec) // We keep the generation number and status so that update does not conflict
			// TODO: don't update if the spec is the same, the hard comparison is the arrays of commit statuses, need
			// to sort and compare them.
			err = r.Update(ctx, &pc)
			if err != nil {
				return fmt.Errorf("failed to update ChangeTransferPolicy %q: %w", pcNew.Name, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to upsert ChangeTransferPolicy %q: %w", pcName, err)
	}

	return &pc, nil
}

func (r *PromotionStrategyReconciler) calculateStatus(ps *promoterv1alpha1.PromotionStrategy, ctpsByBranch map[string]*promoterv1alpha1.ChangeTransferPolicy) error {
	for _, environment := range ps.Spec.Environments {
		ctp, ok := ctpsByBranch[environment.Branch]
		if !ok {
			return fmt.Errorf("ChangeTransferPolicy not found for branch %s", environment.Branch)
		}

		ps.Status.Environments = utils.UpsertEnvironmentStatus(ps.Status.Environments, func() promoterv1alpha1.EnvironmentStatus {
			status := promoterv1alpha1.EnvironmentStatus{
				Branch: environment.Branch,
				Active: promoterv1alpha1.PromotionStrategyBranchStateStatus{
					Dry:      promoterv1alpha1.CommitShaState{Sha: ctp.Status.Active.Dry.Sha, CommitTime: ctp.Status.Active.Dry.CommitTime},
					Hydrated: promoterv1alpha1.CommitShaState{Sha: ctp.Status.Active.Hydrated.Sha, CommitTime: ctp.Status.Active.Hydrated.CommitTime},
					CommitStatus: promoterv1alpha1.PromotionStrategyCommitStatus{
						Phase: string(promoterv1alpha1.CommitPhasePending),
						Sha:   string(promoterv1alpha1.CommitPhasePending),
					},
				},
				Proposed: promoterv1alpha1.PromotionStrategyBranchStateStatus{
					Dry:      promoterv1alpha1.CommitShaState{Sha: ctp.Status.Proposed.Dry.Sha, CommitTime: ctp.Status.Proposed.Dry.CommitTime},
					Hydrated: promoterv1alpha1.CommitShaState{Sha: ctp.Status.Proposed.Hydrated.Sha, CommitTime: ctp.Status.Proposed.Hydrated.CommitTime},
					CommitStatus: promoterv1alpha1.PromotionStrategyCommitStatus{
						Phase: string(promoterv1alpha1.CommitPhasePending),
						Sha:   string(promoterv1alpha1.CommitPhasePending),
					},
				},
			}

			return status
		}())

		i, environmentStatus := utils.GetEnvironmentStatusByBranch(*ps, environment.Branch)

		// TODO: actually implement keeping track of healthy dry sha's
		// We only want to keep the last 10 healthy dry sha's
		if environmentStatus != nil && i < len(ps.Status.Environments) && len(ps.Status.Environments[i].LastHealthyDryShas) > 10 {
			ps.Status.Environments[i].LastHealthyDryShas = ps.Status.Environments[i].LastHealthyDryShas[:10]
		}

		if ctpsByBranch[environment.Branch] == nil {
			return fmt.Errorf("ChangeTransferPolicy not found in map for branch %s while calculating activeCommitStatus", environment.Branch)
		}
		activeEnvStatus := ctpsByBranch[environment.Branch].Status.Active
		r.setEnvironmentCommitStatus(&ps.Status.Environments[i].Active.CommitStatus, len(environment.ActiveCommitStatuses)+len(ps.Spec.ActiveCommitStatuses), activeEnvStatus)
		proposedEnvStatus := ctpsByBranch[environment.Branch].Status.Proposed
		r.setEnvironmentCommitStatus(&ps.Status.Environments[i].Proposed.CommitStatus, len(environment.ProposedCommitStatuses)+len(ps.Spec.ProposedCommitStatuses), proposedEnvStatus)
	}
	return nil
}

// setEnvironmentCommitStatus sets the commit status for the environment based on the configured commit statuses.
func (r *PromotionStrategyReconciler) setEnvironmentCommitStatus(targetStatus *promoterv1alpha1.PromotionStrategyCommitStatus, statusCount int, ctpEnvStatus promoterv1alpha1.CommitBranchState) {
	if statusCount > 0 && len(ctpEnvStatus.CommitStatuses) == statusCount {
		// We have configured active commits and our count of active commits from promotion strategy matches the count of active commit resource.
		for _, status := range ctpEnvStatus.CommitStatuses {
			targetStatus.Phase = string(promoterv1alpha1.CommitPhaseSuccess)
			targetStatus.Sha = ctpEnvStatus.Hydrated.Sha
			if status.Phase != string(promoterv1alpha1.CommitPhaseSuccess) {
				targetStatus.Phase = status.Phase
				targetStatus.Sha = ctpEnvStatus.Hydrated.Sha
				break
			}
		}
	} else if statusCount == 0 && len(ctpEnvStatus.CommitStatuses) == 0 {
		// We have no configured active commits and our count of active commits from promotion strategy matches the count of active commit resource, should be 0 each.
		targetStatus.Phase = string(promoterv1alpha1.CommitPhaseSuccess)
		targetStatus.Sha = ctpEnvStatus.Hydrated.Sha
	} else {
		targetStatus.Phase = string(promoterv1alpha1.CommitPhasePending)
		targetStatus.Sha = ctpEnvStatus.Hydrated.Sha
	}
}

func (r *PromotionStrategyReconciler) createOrUpdatePreviousEnvironmentCommitStatus(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy, phase promoterv1alpha1.CommitStatusPhase, previousEnvironmentStatus *promoterv1alpha1.EnvironmentStatus, previousCRPCSPhases []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase) error {
	// TODO: do we like this name proposed-<name>?
	csName := utils.KubeSafeUniqueName(ctx, promoterv1alpha1.PreviousEnvProposedCommitPrefixNameLabel+ctp.Name)
	proposedCSObjectKey := client.ObjectKey{Namespace: ctp.Namespace, Name: csName}

	kind := reflect.TypeOf(promoterv1alpha1.ChangeTransferPolicy{}).Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)
	controllerRef := metav1.NewControllerRef(ctp, gvk)

	branch := "no previous environment"
	if previousEnvironmentStatus != nil {
		branch = previousEnvironmentStatus.Branch + " - synced and healthy"
	}

	statusMap := make(map[string]string)
	for _, status := range previousCRPCSPhases {
		statusMap[status.Key] = status.Phase
	}
	yamlStatusMap, err := yaml.Marshal(statusMap)
	if err != nil {
		return fmt.Errorf("failed to marshal previous environment commit statuses: %w", err)
	}

	commitStatus := &promoterv1alpha1.CommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name: proposedCSObjectKey.Name,
			Labels: map[string]string{
				promoterv1alpha1.CommitStatusLabel: promoterv1alpha1.PreviousEnvironmentCommitStatusKey,
			},
			Annotations: map[string]string{
				promoterv1alpha1.CommitStatusPreviousEnvironmentStatusesAnnotation: string(yamlStatusMap),
			},
			Namespace:       proposedCSObjectKey.Namespace,
			OwnerReferences: []metav1.OwnerReference{*controllerRef},
		},
		Spec: promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: ctp.Spec.RepositoryReference,
			Sha:                 ctp.Status.Proposed.Hydrated.Sha,
			Name:                branch,
			Description:         branch,
			Phase:               phase,
			// Url:                 "https://github.com/" + gitRepo.Spec.Owner + "/" + gitRepo.Spec.Name + "/commit/" + copyFromActiveHydratedSha,
		},
	}
	updatedCS := &promoterv1alpha1.CommitStatus{}
	err = r.Get(ctx, proposedCSObjectKey, updatedCS)
	if err != nil {
		if errors.IsNotFound(err) {
			err = r.Create(ctx, commitStatus)
			if err != nil {
				return fmt.Errorf("failed to create previous environments CommitStatus: %w", err)
			}
			return nil
		}
		return fmt.Errorf("failed to get previous environments CommitStatus: %w", err)
	}

	updatedYamlStatusMap := make(map[string]string)
	if _, ok := updatedCS.Annotations[promoterv1alpha1.CommitStatusPreviousEnvironmentStatusesAnnotation]; ok {
		err = yaml.Unmarshal([]byte(updatedCS.Annotations[promoterv1alpha1.CommitStatusPreviousEnvironmentStatusesAnnotation]), &updatedYamlStatusMap)
		if err != nil {
			return fmt.Errorf("failed to unmarshal previous environments CommitStatus: %w", err)
		}
	} else {
		return fmt.Errorf("previous environments CommitStatus does not have a previous environment commit statuses annotation")
	}

	if updatedCS.Spec.Phase != phase || updatedCS.Spec.Sha != ctp.Status.Proposed.Hydrated.Sha || !reflect.DeepEqual(statusMap, updatedYamlStatusMap) {
		updatedCS.Spec.Phase = phase
		updatedCS.Spec.Sha = ctp.Status.Proposed.Hydrated.Sha
		updatedCS.Spec.Description = commitStatus.Spec.Description
		updatedCS.Spec.Name = commitStatus.Spec.Name
		updatedCS.Annotations[promoterv1alpha1.CommitStatusPreviousEnvironmentStatusesAnnotation] = commitStatus.Annotations[promoterv1alpha1.CommitStatusPreviousEnvironmentStatusesAnnotation]

		err = r.Update(ctx, updatedCS)
		if err != nil {
			return fmt.Errorf("failed to update previous environments CommitStatus: %w", err)
		}
	}

	return nil
}

// updatePreviousEnvironmentCommitStatus checks if any environment is ready to be merged and if so, merges the pull request. It does this by looking at any active and proposed commit statuses.
func (r *PromotionStrategyReconciler) updatePreviousEnvironmentCommitStatus(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, ctpMap map[string]*promoterv1alpha1.ChangeTransferPolicy) error {
	logger := log.FromContext(ctx)
	// Go through each environment and copy any commit statuses from the previous environment if the previous environment's running dry commit is the same as the
	// currently processing environments proposed dry sha.
	// We then look at the status of the current environment and if all checks have passed and the environment is set to auto merge, we merge the pull request.
	for _, environment := range ps.Spec.Environments {
		previousEnvironmentIndex, previousEnvironmentStatus := utils.GetPreviousEnvironmentStatusByBranch(*ps, environment.Branch)
		environmentIndex, environmentStatus := utils.GetEnvironmentStatusByBranch(*ps, environment.Branch)
		if environmentStatus == nil {
			return fmt.Errorf("EnvironmentStatus not found for branch %s", environment.Branch)
		}

		if ctpMap[environment.Branch] == nil {
			return fmt.Errorf("ChangeTransferPolicy not found in map for branch %s while merging pull requests", environment.Branch)
		}

		activeChecksPassed := previousEnvironmentStatus != nil &&
			previousEnvironmentStatus.Active.CommitStatus.Phase == string(promoterv1alpha1.CommitPhaseSuccess) &&
			previousEnvironmentStatus.Active.Dry.Sha == ctpMap[environment.Branch].Status.Proposed.Dry.Sha &&
			previousEnvironmentStatus.Active.Dry.CommitTime.After(environmentStatus.Active.Dry.CommitTime.Time)

		if previousEnvironmentStatus != nil {
			logger.Info(
				"Previous environment status",
				"branch", environment.Branch,
				"activeChecksPassed", activeChecksPassed,
				"sha", previousEnvironmentStatus.Active.Dry.Sha == ctpMap[environment.Branch].Status.Proposed.Dry.Sha,
				"time", previousEnvironmentStatus.Active.Dry.CommitTime.After(environmentStatus.Active.Dry.CommitTime.Time),
				"phase", previousEnvironmentStatus.Active.CommitStatus.Phase == string(promoterv1alpha1.CommitPhaseSuccess))
		}
		commitStatusPhase := promoterv1alpha1.CommitPhasePending
		if environmentIndex == 0 || activeChecksPassed {
			logger.V(4).Info("Checks passed, setting previous environment check to success", "branch", environment.Branch)
			commitStatusPhase = promoterv1alpha1.CommitPhaseSuccess
		}

		if environmentIndex > 0 && len(ps.Spec.ActiveCommitStatuses) != 0 || (previousEnvironmentIndex >= 0 && len(ps.Spec.Environments[previousEnvironmentIndex].ActiveCommitStatuses) != 0) {
			// Since there is at least one configured active check, and since this is not the first environment,
			// we should not create a commit status for the previous environment.
			err := r.createOrUpdatePreviousEnvironmentCommitStatus(ctx, ctpMap[environment.Branch], commitStatusPhase, previousEnvironmentStatus, ctpMap[ps.Spec.Environments[previousEnvironmentIndex].Branch].Status.Active.CommitStatuses)
			if err != nil {
				return fmt.Errorf("failed to create or update previous environment commit status for branch %s: %w", environment.Branch, err)
			}
		}
	}

	return nil
}
