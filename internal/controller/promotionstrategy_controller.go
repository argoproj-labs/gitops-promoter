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
	"errors"
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
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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
				promoterv1alpha1.PromotionStrategyLabel: utils.KubeSafeLabel(ps.Name),
				promoterv1alpha1.EnvironmentLabel:       utils.KubeSafeLabel(environment.Branch),
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

	environmentIndex, _ := utils.GetEnvironmentByBranch(*ps, environment.Branch)
	previousEnvironmentIndex := environmentIndex - 1
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
			if k8serrors.IsNotFound(err) {
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

		// TODO: actually implement keeping track of healthy dry sha's
		// We only want to keep the last 10 healthy dry sha's
		if i < len(ps.Status.Environments) && len(ps.Status.Environments[i].LastHealthyDryShas) > 10 {
			ps.Status.Environments[i].LastHealthyDryShas = ps.Status.Environments[i].LastHealthyDryShas[:10]
		}
	}
}

func (r *PromotionStrategyReconciler) createOrUpdatePreviousEnvironmentCommitStatus(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy, phase promoterv1alpha1.CommitStatusPhase, previousEnvironmentBranch string, previousCRPCSPhases []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase) error {
	// TODO: do we like this name proposed-<name>?
	csName := utils.KubeSafeUniqueName(ctx, promoterv1alpha1.PreviousEnvProposedCommitPrefixNameLabel+ctp.Name)
	proposedCSObjectKey := client.ObjectKey{Namespace: ctp.Namespace, Name: csName}

	kind := reflect.TypeOf(promoterv1alpha1.ChangeTransferPolicy{}).Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)
	controllerRef := metav1.NewControllerRef(ctp, gvk)

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
			Name:                previousEnvironmentBranch + " - synced and healthy",
			Description:         previousEnvironmentBranch + " - synced and healthy",
			Phase:               phase,
			// Url:                 "https://github.com/" + gitRepo.Spec.Owner + "/" + gitRepo.Spec.Name + "/commit/" + copyFromActiveHydratedSha,
		},
	}
	updatedCS := &promoterv1alpha1.CommitStatus{}
	err = r.Get(ctx, proposedCSObjectKey, updatedCS)
	if err != nil {
		if k8serrors.IsNotFound(err) {
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
		return errors.New("previous environments CommitStatus does not have a previous environment commit statuses annotation")
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
// ps.Spec.Environments and ps.Status.Environments must be the same length and in the same order as ctps.
func (r *PromotionStrategyReconciler) updatePreviousEnvironmentCommitStatus(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, ctps []*promoterv1alpha1.ChangeTransferPolicy) error {
	logger := log.FromContext(ctx)
	// Go through each environment and copy any commit statuses from the previous environment if the previous environment's running dry commit is the same as the
	// currently processing environments proposed dry sha.
	// We then look at the status of the current environment and if all checks have passed and the environment is set to auto merge, we merge the pull request.
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

		activeChecksPassed := utils.AreCommitStatusesPassing(previousEnvironmentStatus.Active.CommitStatuses) &&
			previousEnvironmentStatus.Active.Dry.Sha == ctp.Status.Proposed.Dry.Sha &&
			(previousEnvironmentStatus.Active.Dry.CommitTime.After(currentEnvironmentStatus.Active.Dry.CommitTime.Time) ||
				previousEnvironmentStatus.Active.Dry.CommitTime.Equal(&metav1.Time{Time: previousEnvironmentStatus.Active.Dry.CommitTime.Time}))

		commitStatusPhase := promoterv1alpha1.CommitPhasePending
		if activeChecksPassed {
			logger.V(4).Info("Checks passed, setting previous environment check to success", "branch", ctp.Spec.ActiveBranch)
			commitStatusPhase = promoterv1alpha1.CommitPhaseSuccess
		}

		// Since there is at least one configured active check, and since this is not the first environment,
		// we should not create a commit status for the previous environment.
		err := r.createOrUpdatePreviousEnvironmentCommitStatus(ctx, ctp, commitStatusPhase, previousEnvironmentStatus.Branch, ctps[i-1].Status.Active.CommitStatuses)
		if err != nil {
			return fmt.Errorf("failed to create or update previous environment commit status for branch %s: %w", ctp.Spec.ActiveBranch, err)
		}
	}

	return nil
}
