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

	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type PromotionStrategyReconcilerConfig struct {
	RequeueDuration time.Duration
}

// PromotionStrategyReconciler reconciles a PromotionStrategy object
type PromotionStrategyReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	Config   PromotionStrategyReconcilerConfig
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PromotionStrategy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *PromotionStrategyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.V(1).Info("Reconciling PromotionStrategy", "namespace", req.Namespace, "name", req.Name)

	var ps promoterv1alpha1.PromotionStrategy
	err := r.Get(ctx, req.NamespacedName, &ps, &client.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("PromotionStrategy not found", "namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get PromotionStrategy", "namespace", req.Namespace, "name", req.Name)
		return ctrl.Result{}, err
	}

	// If a ProposedCommit does not exist, create it otherwise get it and store the ProposedCommit in a map with the branch as the key.
	var proposedCommitMap = make(map[string]*promoterv1alpha1.ProposedCommit)
	for _, environment := range ps.Spec.Environments {
		pc, err := r.createOrGetProposedCommit(ctx, &ps, environment)
		if err != nil {
			logger.Error(err, "failed to create ProposedCommit", "namespace", ps.Namespace, "name", ps.Name)
			return ctrl.Result{}, err
		}
		proposedCommitMap[environment.Branch] = pc
	}

	// Calculate the status of the PromotionStrategy
	err = r.calculateStatus(ctx, &ps, proposedCommitMap)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.mergePullRequests(ctx, &ps, proposedCommitMap)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.Status().Update(ctx, &ps)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{
		Requeue:      true,
		RequeueAfter: r.Config.RequeueDuration,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PromotionStrategyReconciler) SetupWithManager(mgr ctrl.Manager) error {

	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &promoterv1alpha1.CommitStatus{}, ".spec.sha", func(rawObj client.Object) []string {
		cs := rawObj.(*promoterv1alpha1.CommitStatus)
		return []string{cs.Spec.Sha}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.PromotionStrategy{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		//Owns(&promoterv1alpha1.ProposedCommit{}).
		Complete(r)
}

func (r *PromotionStrategyReconciler) createOrGetProposedCommit(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, environment promoterv1alpha1.Environment) (*promoterv1alpha1.ProposedCommit, error) {
	logger := log.FromContext(ctx)

	//pc := promoterv1alpha1.ProposedCommit{}
	pcName := utils.KubeSafeUniqueName(ctx, utils.GetProposedCommitName(ps.Name, environment.Branch))

	// The code below sets the ownership for the Release Object
	kind := reflect.TypeOf(promoterv1alpha1.PromotionStrategy{}).Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)
	controllerRef := metav1.NewControllerRef(ps, gvk)

	pc := promoterv1alpha1.ProposedCommit{
		ObjectMeta: metav1.ObjectMeta{
			Name:            pcName,
			Namespace:       ps.Namespace,
			OwnerReferences: []metav1.OwnerReference{*controllerRef},
			Labels: map[string]string{
				"promoter.argoproj.io/promotion-strategy": utils.KubeSafeLabel(ctx, ps.Name),
				"promoter.argoproj.io/proposed-commit":    utils.KubeSafeLabel(ctx, pcName),
				"promoter.argoproj.io/environment":        utils.KubeSafeLabel(ctx, environment.Branch),
			},
		},
		Spec: promoterv1alpha1.ProposedCommitSpec{
			RepositoryReference:    ps.Spec.RepositoryReference,
			ProposedBranch:         fmt.Sprintf("%s-%s", environment.Branch, "next"),
			ActiveBranch:           environment.Branch,
			ActiveCommitStatuses:   append(environment.ActiveCommitStatuses, ps.Spec.ActiveCommitStatuses...),
			ProposedCommitStatuses: append(environment.ProposedCommitStatuses, ps.Spec.ProposedCommitStatuses...),
		},
	}

	pcGet := promoterv1alpha1.ProposedCommit{}
	err := r.Get(ctx, client.ObjectKey{Namespace: ps.Namespace, Name: pcName}, &pcGet, &client.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("ProposedCommit not found, creating", "namespace", ps.Namespace, "name", pcName)
			err = r.Create(ctx, &pc)
			if err != nil {
				return &pc, err
			}
		} else {
			logger.Error(err, "failed to get ProposedCommit", "namespace", ps.Namespace, "name", pcName)
			return &pc, err
		}
	} else {
		//TODO: Update the ProposedCommit with the new values, we should could add a hash status to the ProposedCommit to see if we need to update it.
		err = r.Patch(ctx, &pc, client.MergeFrom(&promoterv1alpha1.ProposedCommit{}))
		if err != nil {
			return nil, err
		}
	}
	return &pc, nil
}

func (r *PromotionStrategyReconciler) calculateStatus(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, pcMap map[string]*promoterv1alpha1.ProposedCommit) error {
	logger := log.FromContext(ctx)

	for _, environment := range ps.Spec.Environments {
		pc, ok := pcMap[environment.Branch]
		if !ok {
			return fmt.Errorf("ProposedCommit not found for branch %s", environment.Branch)
		}

		ps.Status.Environments = utils.UpsertEnvironmentStatus(ps.Status.Environments, func() promoterv1alpha1.EnvironmentStatus {
			status := promoterv1alpha1.EnvironmentStatus{
				Branch: environment.Branch,
				Active: promoterv1alpha1.PromotionStrategyBranchStateStatus{
					Dry:      promoterv1alpha1.CommitShaState{Sha: pc.Status.Active.Dry.Sha, CommitTime: pc.Status.Active.Dry.CommitTime},
					Hydrated: promoterv1alpha1.CommitShaState{Sha: pc.Status.Active.Hydrated.Sha, CommitTime: pc.Status.Active.Hydrated.CommitTime},
					CommitStatus: promoterv1alpha1.PromotionStrategyCommitStatus{
						Phase: string(promoterv1alpha1.CommitPhasePending),
						Sha:   string(promoterv1alpha1.CommitPhasePending),
					},
				},
				Proposed: promoterv1alpha1.PromotionStrategyBranchStateStatus{
					Dry:      promoterv1alpha1.CommitShaState{Sha: pc.Status.Proposed.Dry.Sha, CommitTime: pc.Status.Proposed.Dry.CommitTime},
					Hydrated: promoterv1alpha1.CommitShaState{Sha: pc.Status.Proposed.Hydrated.Sha, CommitTime: pc.Status.Proposed.Hydrated.CommitTime},
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

		activeCommitStatusCount := len(environment.ActiveCommitStatuses) + len(ps.Spec.ActiveCommitStatuses)
		if activeCommitStatusCount > 0 && len(pcMap[environment.Branch].Status.Active.CommitStatuses) == activeCommitStatusCount {
			// We have configured active commits and our count of active commits from promotion strategy matches the count of active commit resource.
			for _, status := range pcMap[environment.Branch].Status.Active.CommitStatuses {
				ps.Status.Environments[i].Active.CommitStatus.Phase = string(promoterv1alpha1.CommitPhaseSuccess)
				ps.Status.Environments[i].Active.CommitStatus.Sha = pcMap[environment.Branch].Status.Active.Hydrated.Sha
				if status.Phase != string(promoterv1alpha1.CommitPhaseSuccess) {
					ps.Status.Environments[i].Active.CommitStatus.Phase = status.Phase
					ps.Status.Environments[i].Active.CommitStatus.Sha = pcMap[environment.Branch].Status.Active.Hydrated.Sha
					logger.Info("Active commit status not success", "branch", environment.Branch, "phase", status.Phase, "sha", pcMap[environment.Branch].Status.Active.Hydrated.Sha, "key", status.Key)

					break
				}
			}
		} else if activeCommitStatusCount == 0 && len(pcMap[environment.Branch].Status.Active.CommitStatuses) == 0 {
			// We have no configured active commits and our count of active commits from promotion strategy matches the count of active commit resource, should be 0 each.
			ps.Status.Environments[i].Active.CommitStatus.Phase = string(promoterv1alpha1.CommitPhaseSuccess)
			ps.Status.Environments[i].Active.CommitStatus.Sha = pcMap[environment.Branch].Status.Active.Hydrated.Sha
			//logger.Info("No active commit statuses configured, assuming success", "branch", environment.Branch)
		} else {
			ps.Status.Environments[i].Active.CommitStatus.Phase = string(promoterv1alpha1.CommitPhasePending)
			ps.Status.Environments[i].Active.CommitStatus.Sha = pcMap[environment.Branch].Status.Active.Hydrated.Sha
			logger.Info("Active commit status pending", "branch", environment.Branch)
		}

		proposedCommitStatusCount := len(environment.ProposedCommitStatuses) + len(ps.Spec.ProposedCommitStatuses)
		if proposedCommitStatusCount > 0 && len(pcMap[environment.Branch].Status.Proposed.CommitStatuses) == proposedCommitStatusCount {
			// We have configured proposed commits and our count of proposed commits from promotion strategy matches the count of proposed commit resource.
			for _, status := range pcMap[environment.Branch].Status.Proposed.CommitStatuses {
				ps.Status.Environments[i].Proposed.CommitStatus.Phase = string(promoterv1alpha1.CommitPhaseSuccess)
				ps.Status.Environments[i].Proposed.CommitStatus.Sha = pcMap[environment.Branch].Status.Proposed.Hydrated.Sha
				if status.Phase != string(promoterv1alpha1.CommitPhaseSuccess) {
					ps.Status.Environments[i].Proposed.CommitStatus.Phase = status.Phase
					ps.Status.Environments[i].Proposed.CommitStatus.Sha = pcMap[environment.Branch].Status.Proposed.Hydrated.Sha
					logger.Info("Proposed commit status not success", "branch", environment.Branch, "phase", status.Phase, "sha", pcMap[environment.Branch].Status.Proposed.Hydrated.Sha, "key", status.Key)
					break
				}
			}
		} else if proposedCommitStatusCount == 0 && len(pcMap[environment.Branch].Status.Proposed.CommitStatuses) == 0 {
			// We have no configured proposed commits and our count of proposed commits from promotion strategy matches the count of proposed commit resource, should be 0 each.
			ps.Status.Environments[i].Proposed.CommitStatus.Phase = string(promoterv1alpha1.CommitPhaseSuccess)
			ps.Status.Environments[i].Proposed.CommitStatus.Sha = pcMap[environment.Branch].Status.Proposed.Hydrated.Sha
			//logger.Info("No proposed commit statuses configured, assuming success", "branch", environment.Branch)
		} else {
			ps.Status.Environments[i].Proposed.CommitStatus.Phase = string(promoterv1alpha1.CommitPhasePending)
			ps.Status.Environments[i].Proposed.CommitStatus.Sha = pcMap[environment.Branch].Status.Proposed.Hydrated.Sha
			logger.Info("Proposed commit status pending", "branch", environment.Branch)
		}
	}
	return nil
}

// copyCommitStatuses copies the commit statuses from one sha to another sha. This is mainly used to show the previous environments commit statuses on the current environments PR.
func (r *PromotionStrategyReconciler) copyCommitStatuses(ctx context.Context, csSelector []promoterv1alpha1.CommitStatusSelector, copyFromActiveHydratedSha string, copyToProposedHydratedSha string, branch string) error {
	logger := log.FromContext(ctx)

	for _, value := range csSelector {
		var commitStatuses promoterv1alpha1.CommitStatusList
		err := r.List(ctx, &commitStatuses, &client.ListOptions{
			LabelSelector: labels.SelectorFromSet(map[string]string{
				promoterv1alpha1.CommitStatusLabel: utils.KubeSafeLabel(ctx, value.Key),
			}),
			FieldSelector: fields.SelectorFromSet(map[string]string{
				".spec.sha": copyFromActiveHydratedSha,
			}),
		})
		if err != nil {
			return err
		}

		for _, commitStatus := range commitStatuses.Items {
			if commitStatus.Labels[promoterv1alpha1.CommitStatusLabelCopy] == "true" {
				continue
			}

			copiedCommitStatus := &promoterv1alpha1.CommitStatus{}
			//TODO: do we like this name proposed-<name>?
			copiedCSName := utils.KubeSafeUniqueName(ctx, promoterv1alpha1.CopiedProposedCommitPrefixName+commitStatus.Name)
			proposedCSObjectKey := client.ObjectKey{Namespace: commitStatus.Namespace, Name: copiedCSName}

			copiedCommitStatus = &promoterv1alpha1.CommitStatus{
				ObjectMeta: metav1.ObjectMeta{
					Name:        proposedCSObjectKey.Name,
					Annotations: commitStatus.Annotations,
					Labels:      commitStatus.Labels,
					Namespace:   commitStatus.Namespace,
				},
				Spec: promoterv1alpha1.CommitStatusSpec{
					RepositoryReference: commitStatus.Spec.RepositoryReference,
					Sha:                 copyToProposedHydratedSha,
					Name:                branch + " - " + commitStatus.Spec.Name,
					Description:         commitStatus.Spec.Description,
					Phase:               commitStatus.Spec.Phase,
					Url:                 "https://github.com/" + commitStatus.Spec.RepositoryReference.Owner + "/" + commitStatus.Spec.RepositoryReference.Name + "/commit/" + copyFromActiveHydratedSha,
				},
			}
			if copiedCommitStatus.Labels == nil {
				copiedCommitStatus.Labels = make(map[string]string)
			}
			copiedCommitStatus.Labels[promoterv1alpha1.CommitStatusLabelCopy] = "true"
			copiedCommitStatus.Labels["promoter.argoproj.io/commit-status-copy-from"] = utils.KubeSafeLabel(ctx, commitStatus.Spec.Name)
			copiedCommitStatus.Labels["promoter.argoproj.io/commit-status-copy-from-sha"] = utils.KubeSafeLabel(ctx, copyFromActiveHydratedSha)
			copiedCommitStatus.Labels["promoter.argoproj.io/commit-status-copy-from-branch"] = utils.KubeSafeLabel(ctx, branch)

			err = r.Patch(ctx, copiedCommitStatus, client.MergeFrom(&promoterv1alpha1.CommitStatus{}))
			if err != nil {
				if errors.IsNotFound(err) {
					errCreate := r.Create(ctx, copiedCommitStatus)
					if errCreate != nil {
						logger.Error(errCreate, "failed to create copied CommitStatus", "namespace", copiedCommitStatus.Namespace, "name", copiedCommitStatus.Name)
						return errCreate
					}
				}
			}
		}
	}

	return nil
}

// mergePullRequests checks if any environment is ready to be merged and if so, merges the pull request. It does this by looking at any active and proposed commit statuses.
func (r *PromotionStrategyReconciler) mergePullRequests(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, proposedCommitMap map[string]*promoterv1alpha1.ProposedCommit) error {

	logger := log.FromContext(ctx)
	// Go through each environment and copy any commit statuses from the previous environment if the previous environment's running dry commit is the same as the
	// currently processing environments proposed dry sha.
	// We then look at the status of the current environment and if all checks have passed and the environment is set to auto merge, we merge the pull request.
	for _, environment := range ps.Spec.Environments {
		_, previousEnvironmentStatus := utils.GetPreviousEnvironmentStatusByBranch(*ps, environment.Branch)
		environmentIndex, environmentStatus := utils.GetEnvironmentStatusByBranch(*ps, environment.Branch)
		if environmentStatus == nil {
			return fmt.Errorf("EnvironmentStatus not found for branch %s", environment.Branch)
		}

		if previousEnvironmentStatus != nil {
			// There is no previous environment to compare to, so we can't copy the commit statuses.
			if proposedCommitMap[environment.Branch].Status.Proposed.Hydrated.Sha != "" && previousEnvironmentStatus.Active.Dry.Sha == proposedCommitMap[environment.Branch].Status.Proposed.Dry.Sha {
				// If the previous environment's running commit is the same as the current proposed commit, copy the commit statuses.
				err := r.copyCommitStatuses(ctx, append(environment.ActiveCommitStatuses, ps.Spec.ActiveCommitStatuses...), previousEnvironmentStatus.Active.Hydrated.Sha, proposedCommitMap[environment.Branch].Status.Proposed.Hydrated.Sha, previousEnvironmentStatus.Branch) //pc.Status.Active.Hydrated.Sha
				if err != nil {
					return err
				}
			}
		}

		activeChecksPassed := previousEnvironmentStatus != nil &&
			previousEnvironmentStatus.Active.CommitStatus.Phase == string(promoterv1alpha1.CommitPhaseSuccess) &&
			previousEnvironmentStatus.Active.Dry.Sha == proposedCommitMap[environment.Branch].Status.Proposed.Dry.Sha &&
			previousEnvironmentStatus.Active.Dry.CommitTime.After(environmentStatus.Active.Dry.CommitTime.Time)

		proposedChecksPassed := environmentStatus.Proposed.CommitStatus.Phase == string(promoterv1alpha1.CommitPhaseSuccess) &&
			environmentStatus.Proposed.Dry.Sha == proposedCommitMap[environment.Branch].Status.Proposed.Dry.Sha

		if (environmentIndex == 0 && proposedChecksPassed || (activeChecksPassed && proposedChecksPassed)) && environment.GetAutoMerge() {
			// We are either in the first environment or all checks have passed and the environment is set to auto merge.
			prl := promoterv1alpha1.PullRequestList{}
			// Find the PRs that match the proposed commit and the environment. There should only be one.
			err := r.List(ctx, &prl, &client.ListOptions{
				LabelSelector: labels.SelectorFromSet(map[string]string{
					"promoter.argoproj.io/promotion-strategy": utils.KubeSafeLabel(ctx, ps.Name),
					"promoter.argoproj.io/proposed-commit":    utils.KubeSafeLabel(ctx, proposedCommitMap[environment.Branch].Name),
					"promoter.argoproj.io/environment":        utils.KubeSafeLabel(ctx, environment.Branch),
				}),
			})
			if err != nil {
				return err
			}

			if len(prl.Items) > 1 {
				return fmt.Errorf("More than one PullRequest found for ProposedCommit %s and Environment %s", proposedCommitMap[environment.Branch].Name, environment.Branch)
			}

			if len(prl.Items) == 1 {
				// We found 1 pull request process it.
				pullRequest := prl.Items[0]
				if pullRequest.Status.State == promoterv1alpha1.PullRequestOpen {
					if previousEnvironmentStatus != nil {
						logger.Info("Active checks passed", "branch", environment.Branch,
							"autoMerge", environment.AutoMerge,
							"previousEnvironmentState", previousEnvironmentStatus.Active.CommitStatus.Phase,
							"previousEnvironmentSha", previousEnvironmentStatus.Active.CommitStatus.Sha,
							"previousEnvironmentCommitTime", previousEnvironmentStatus.Active.Dry.CommitTime,
							"currentEnvironmentCommitTime", environmentStatus.Active.Dry.CommitTime)
					} else {
						// There is no previous environment to log information about.
						logger.Info("Active checks passed without previous environment", "branch", environment.Branch,
							"autoMerge", environment.AutoMerge,
							"numberOfActiveCommitStatuses", len(append(environment.ActiveCommitStatuses, ps.Spec.ActiveCommitStatuses...)))
					}
				}

				if pullRequest.Spec.State == promoterv1alpha1.PullRequestOpen && pullRequest.Status.State == promoterv1alpha1.PullRequestOpen {
					err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
						var pr promoterv1alpha1.PullRequest
						err := r.Get(ctx, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Name}, &pr, &client.GetOptions{})
						if err != nil {
							return err
						}
						pr.Spec.State = promoterv1alpha1.PullRequestMerged
						return r.Update(ctx, &pr)
					})
					if err != nil {
						return err
					}
					r.Recorder.Event(ps, "Normal", "PullRequestMerged", fmt.Sprintf("Pull Request %s merged", pullRequest.Name))
					logger.V(4).Info("Merged pull request", "namespace", pullRequest.Namespace, "name", pullRequest.Name)
				} else if pullRequest.Status.State == promoterv1alpha1.PullRequestOpen {
					logger.Info("Pull request not ready to merge yet", "namespace", pullRequest.Namespace, "name", pullRequest.Name)
				}
			}
		}

	}

	return nil
}
