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

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"

	promoterv1alpha1 "github.com/zachaller/promoter/api/v1alpha1"
	"github.com/zachaller/promoter/internal/utils"
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
	Scheme  *runtime.Scheme
	indexer client.FieldIndexer
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
	logger.Info("Reconciling PromotionStrategy", "namespace", req.Namespace, "name", req.Name)

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

	var createProposedCommitErr []error
	for _, environment := range ps.Spec.Environments {
		pc, err := r.createProposedCommit(ctx, &ps, environment)
		if err != nil {
			logger.Error(err, "failed to create ProposedCommit", "namespace", ps.Namespace, "name", ps.Name)
			createProposedCommitErr = append(createProposedCommitErr, err)
		}

		err = r.calculateStatus(ctx, &ps, pc, environment)
		if err != nil {
			return ctrl.Result{}, err
		}

		if environment.AutoMerge {
			logger.Info("AutoMerge is enabled", "namespace", ps.Namespace, "name", ps.Name, "branch", environment.Branch)
			prl := promoterv1alpha1.PullRequestList{}
			err := r.List(ctx, &prl, &client.ListOptions{
				LabelSelector: labels.SelectorFromSet(map[string]string{
					"promoter.argoproj.io/promotion-strategy": utils.KubeSafeName(ps.Name, 63),
					"promoter.argoproj.io/proposed-commit":    utils.KubeSafeName(pc.Name, 63),
					"promoter.argoproj.io/environment":        utils.KubeSafeName(environment.Branch, 63),
				}),
			})
			if err != nil {
				return ctrl.Result{}, err
			}

			if len(prl.Items) > 0 && prl.Items[0].Spec.State == promoterv1alpha1.PullRequestOpen {
				prl.Items[0].Spec.State = promoterv1alpha1.PullRequestMerged
				err = r.Update(ctx, &prl.Items[0])
				if err != nil {
					return ctrl.Result{}, err
				}
			}
		}

		// If CommitStatus is healthy then merge the PR
		for i, statusEnvironment := range ps.Status.Environments {
			if statusEnvironment.Branch == environment.Branch {

				activeChecksPassed := len(ps.Status.Environments) > 0 && i > 0 &&
					ps.Status.Environments[i-1].Active.CommitStatus == "success" &&
					ps.Status.Environments[i-1].Active.Dry.CommitTime.After(ps.Status.Environments[i].Active.Dry.CommitTime.Time)
				//if i == 0 {
				//	//Promote the first environment, by merging the PR
				//	activeChecksPassed = true
				//}

				if activeChecksPassed {
					prl := promoterv1alpha1.PullRequestList{}
					err := r.List(ctx, &prl, &client.ListOptions{
						LabelSelector: labels.SelectorFromSet(map[string]string{
							"promoter.argoproj.io/promotion-strategy": utils.KubeSafeName(ps.Name, 63),
							"promoter.argoproj.io/proposed-commit":    utils.KubeSafeName(pc.Name, 63),
							"promoter.argoproj.io/environment":        utils.KubeSafeName(environment.Branch, 63),
						}),
					})
					if err != nil {
						return ctrl.Result{}, err
					}

					if len(prl.Items) > 0 && prl.Items[0].Spec.State == promoterv1alpha1.PullRequestOpen {
						prl.Items[0].Spec.State = promoterv1alpha1.PullRequestMerged
						err = r.Update(ctx, &prl.Items[0])
						if err != nil {
							return ctrl.Result{}, err
						}
					}
				}
			}
		}

		err = r.Status().Update(ctx, &ps)
		if err != nil {
			return ctrl.Result{}, err
		}

	}
	if len(createProposedCommitErr) > 0 {
		return ctrl.Result{}, fmt.Errorf("failed to create ProposedCommit: %v", createProposedCommitErr)
	}

	return ctrl.Result{
		Requeue:      true,
		RequeueAfter: 10 * time.Second,
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
		For(&promoterv1alpha1.PromotionStrategy{}).
		Owns(&promoterv1alpha1.ProposedCommit{}).
		Complete(r)
}

func (r *PromotionStrategyReconciler) createProposedCommit(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, environment promoterv1alpha1.Environment) (*promoterv1alpha1.ProposedCommit, error) {
	logger := log.FromContext(ctx)

	pc := promoterv1alpha1.ProposedCommit{}
	pcName := utils.KubeSafeName(fmt.Sprintf("%s-%s", ps.Name, environment.Branch), 250)
	err := r.Get(ctx, client.ObjectKey{Namespace: ps.Namespace, Name: pcName}, &pc, &client.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("ProposedCommit not found creating", "namespace", ps.Namespace, "name", pcName)

			// The code below sets the ownership for the Release Object
			kind := reflect.TypeOf(promoterv1alpha1.PromotionStrategy{}).Name()
			gvk := promoterv1alpha1.GroupVersion.WithKind(kind)
			controllerRef := metav1.NewControllerRef(ps, gvk)

			pc = promoterv1alpha1.ProposedCommit{
				ObjectMeta: metav1.ObjectMeta{
					Name:            pcName,
					Namespace:       ps.Namespace,
					OwnerReferences: []metav1.OwnerReference{*controllerRef},
					Labels: map[string]string{
						"promoter.argoproj.io/promotion-strategy": utils.KubeSafeName(ps.Name, 63),
						"promoter.argoproj.io/proposed-commit":    utils.KubeSafeName(pc.Name, 63),
						"promoter.argoproj.io/environment":        utils.KubeSafeName(environment.Branch, 63),
					},
				},
				Spec: promoterv1alpha1.ProposedCommitSpec{
					RepositoryReference: ps.Spec.RepositoryReference,
					ProposedBranch:      fmt.Sprintf("%s-%s", environment.Branch, "next"),
					ActiveBranch:        environment.Branch,
				},
			}

			err = r.Create(ctx, &pc)
			if err != nil {
				return &pc, err
			}
		} else {
			logger.Error(err, "failed to get ProposedCommit", "namespace", ps.Namespace, "name", pcName)
			return &pc, err
		}
	}

	return &pc, nil
}

func (r *PromotionStrategyReconciler) calculateStatus(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, pc *promoterv1alpha1.ProposedCommit, environment promoterv1alpha1.Environment) error {
	if slices.ContainsFunc(ps.Status.Environments, func(e promoterv1alpha1.EnvironmentStatus) bool {
		return e.Branch == environment.Branch
	}) {
		for i := range ps.Status.Environments {
			if ps.Status.Environments[i].Branch == environment.Branch {
				//if pc.Status.Active == nil || pc.Status.Proposed == nil {
				//	continue
				//}

				ps.Status.Environments[i].Active.Dry.Sha = pc.Status.Active.Dry.Sha
				ps.Status.Environments[i].Active.Dry.CommitTime = pc.Status.Active.Dry.CommitTime

				ps.Status.Environments[i].Active.Hydrated.Sha = pc.Status.Active.Hydrated.Sha
				ps.Status.Environments[i].Active.Hydrated.CommitTime = pc.Status.Active.Hydrated.CommitTime

				ps.Status.Environments[i].Proposed.Dry.Sha = pc.Status.Proposed.Dry.Sha
				ps.Status.Environments[i].Proposed.Dry.CommitTime = pc.Status.Proposed.Dry.CommitTime

				ps.Status.Environments[i].Proposed.Hydrated.Sha = pc.Status.Proposed.Hydrated.Sha
				ps.Status.Environments[i].Proposed.Hydrated.CommitTime = pc.Status.Proposed.Hydrated.CommitTime

				if len(ps.Status.Environments[i].LastHealthyDryShas) > 10 {
					ps.Status.Environments[i].LastHealthyDryShas = ps.Status.Environments[i].LastHealthyDryShas[:10]
				}
			}
		}
	} else {
		ps.Status.Environments = append(ps.Status.Environments, func() promoterv1alpha1.EnvironmentStatus {
			status := promoterv1alpha1.EnvironmentStatus{
				Branch: environment.Branch,
				Active: promoterv1alpha1.PromotionStrategyBranchStateStatus{
					Dry:          promoterv1alpha1.ProposedCommitShaState{Sha: pc.Status.Active.Dry.Sha, CommitTime: pc.Status.Active.Dry.CommitTime},
					Hydrated:     promoterv1alpha1.ProposedCommitShaState{Sha: pc.Status.Active.Hydrated.Sha, CommitTime: pc.Status.Active.Hydrated.CommitTime},
					CommitStatus: "unknown",
				},
				Proposed: promoterv1alpha1.PromotionStrategyBranchStateStatus{
					Dry:          promoterv1alpha1.ProposedCommitShaState{Sha: pc.Status.Proposed.Dry.Sha, CommitTime: pc.Status.Proposed.Dry.CommitTime},
					Hydrated:     promoterv1alpha1.ProposedCommitShaState{Sha: pc.Status.Proposed.Hydrated.Sha, CommitTime: pc.Status.Proposed.Hydrated.CommitTime},
					CommitStatus: "unknown",
				},
			}
			return status
		}())
	}

	//Bumble up CommitStatus to PromotionStrategy Status
	for _, status := range ps.Spec.ActiveCommitStatuses {
		var csList promoterv1alpha1.CommitStatusList
		err := r.List(ctx, &csList, &client.ListOptions{
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"promoter.argoproj.io/commit-status": status.Key,
			}),
			FieldSelector: fields.SelectorFromSet(map[string]string{
				".spec.sha": pc.Status.Active.Hydrated.Sha,
			}),
		})
		if err != nil {
			return err
		}

		for i := range ps.Status.Environments {
			if ps.Status.Environments[i].Branch == environment.Branch {
				if len(csList.Items) == 1 {
					//ps.Status.Environments[i].Active.CommitStatus = string(csList.Items[0].Spec.State)
					ps.Status.Environments[i].Active.CommitStatus = "success"
					if string(csList.Items[0].Spec.State) != "success" {
						ps.Status.Environments[i].Active.CommitStatus = string(csList.Items[0].Spec.State)
						break
					}
				} else if len(csList.Items) > 1 {
					ps.Status.Environments[i].Active.CommitStatus = "to-many-matching-sha"
				} else if len(csList.Items) == 0 {
					ps.Status.Environments[i].Active.CommitStatus = "unknown"
				}
			}
		}
	}

	//statusStatesProposed := []string{}
	for _, status := range ps.Spec.ProposedCommitStatuses {
		var csList promoterv1alpha1.CommitStatusList
		err := r.List(ctx, &csList, &client.ListOptions{
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"promoter.argoproj.io/commit-status": status.Key,
			}),
			FieldSelector: fields.SelectorFromSet(map[string]string{
				".spec.sha": pc.Status.Proposed.Hydrated.Sha,
			}),
		})
		if err != nil {
			return err
		}

		for i := range ps.Status.Environments {
			if ps.Status.Environments[i].Branch == environment.Branch {
				if len(csList.Items) > 0 {
					//ps.Status.Environments[i].Proposed.CommitStatus = string(csList.Items[0].Spec.State)
					ps.Status.Environments[i].Proposed.CommitStatus = "success"
					if string(csList.Items[0].Spec.State) != "success" {
						ps.Status.Environments[i].Proposed.CommitStatus = string(csList.Items[0].Spec.State)
						break
					}
					//statusStatesProposed = append(statusStatesProposed, string(csList.Items[0].Spec.State))
				} else if len(csList.Items) > 1 {
					ps.Status.Environments[i].Active.CommitStatus = "to-many-matching-sha"
				} else if len(csList.Items) == 0 {
					ps.Status.Environments[i].Proposed.CommitStatus = "unknown"
				}
			}
		}
	}

	return nil
}
