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

	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

type ChangeTransferPolicyReconcilerConfig struct {
	RequeueDuration time.Duration
}

// ChangeTransferPolicyReconciler reconciles a ChangeTransferPolicy object
type ChangeTransferPolicyReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	PathLookup utils.PathLookup
	Recorder   record.EventRecorder
	Config     ChangeTransferPolicyReconcilerConfig
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicies/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ChangeTransferPolicy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *ChangeTransferPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var ctp promoterv1alpha1.ChangeTransferPolicy
	err := r.Get(ctx, req.NamespacedName, &ctp, &client.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("ChangeTransferPolicy not found")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "failed to get ChangeTransferPolicy")
		return ctrl.Result{}, err
	}

	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, ctp.Spec.RepositoryReference, &ctp)
	if err != nil {
		return ctrl.Result{}, err
	}

	gitAuthProvider, err := r.getGitAuthProvider(ctx, scmProvider, secret)
	if err != nil {
		return ctrl.Result{}, err
	}
	gitOperations, err := git.NewGitOperations(ctx, r.Client, gitAuthProvider, r.PathLookup, ctp.Spec.RepositoryReference, &ctp, ctp.Spec.ActiveBranch)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = gitOperations.CloneRepo(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.calculateStatus(ctx, &ctp, gitOperations)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.mergeOrPullRequestPromote(ctx, gitOperations, &ctp)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.Status().Update(ctx, &ctp)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{
		Requeue:      true,
		RequeueAfter: r.Config.RequeueDuration,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ChangeTransferPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.ChangeTransferPolicy{}).
		Complete(r)
}

func (r *ChangeTransferPolicyReconciler) getGitAuthProvider(ctx context.Context, scmProvider *promoterv1alpha1.ScmProvider, secret *v1.Secret) (scms.GitOperationsProvider, error) {
	logger := log.FromContext(ctx)
	switch {
	case scmProvider.Spec.Fake != nil:
		logger.V(4).Info("Creating fake git authentication provider")
		return fake.NewFakeGitAuthenticationProvider(scmProvider, secret), nil
	case scmProvider.Spec.GitHub != nil:
		logger.V(4).Info("Creating GitHub git authentication provider")
		return github.NewGithubGitAuthenticationProvider(scmProvider, secret), nil
	default:
		return nil, fmt.Errorf("no supported git authentication provider found")
	}
}

func (r *ChangeTransferPolicyReconciler) calculateStatus(ctx context.Context, pc *promoterv1alpha1.ChangeTransferPolicy, gitOperations *git.GitOperations) error {
	logger := log.FromContext(ctx)

	branchShas, err := gitOperations.GetBranchShas(ctx, []string{pc.Spec.ActiveBranch, pc.Spec.ProposedBranch})
	if err != nil {
		return err
	}
	logger.Info("Branch SHAs", "branchShas", branchShas)

	for branch, shas := range branchShas {
		if branch == pc.Spec.ActiveBranch {

			pc.Status.Active.Hydrated.Sha = shas.Hydrated
			commitTime, err := gitOperations.GetShaTime(ctx, shas.Hydrated)
			if err != nil {
				return err
			}
			pc.Status.Active.Hydrated.CommitTime = commitTime

			pc.Status.Active.Dry.Sha = shas.Dry
			commitTime, err = gitOperations.GetShaTime(ctx, shas.Dry)
			if err != nil {
				return err
			}
			pc.Status.Active.Dry.CommitTime = commitTime

			activeCommitStatusesState := []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{}
			for _, status := range pc.Spec.ActiveCommitStatuses {
				var csListActive promoterv1alpha1.CommitStatusList
				// Find all the replicasets that match the commit status configured name and the sha of the active hydrated commit
				err := r.List(ctx, &csListActive, &client.ListOptions{
					LabelSelector: labels.SelectorFromSet(map[string]string{
						promoterv1alpha1.CommitStatusLabel: utils.KubeSafeLabel(ctx, status.Key),
					}),
					FieldSelector: fields.SelectorFromSet(map[string]string{
						".spec.sha": pc.Status.Active.Hydrated.Sha,
					}),
				})
				if err != nil {
					return err
				}

				// We don't want to capture any of the copied commits statuses that are used for GitHub/Provider UI experience only.
				csListSlice := []promoterv1alpha1.CommitStatus{}
				for _, item := range csListActive.Items {
					if item.Labels[promoterv1alpha1.CommitStatusCopyLabel] != "true" {
						csListSlice = append(csListSlice, item)
					}
				}

				if len(csListSlice) == 1 {
					activeCommitStatusesState = append(activeCommitStatusesState, promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						Key:   status.Key,
						Phase: string(csListSlice[0].Status.Phase),
					})
				} else if len(csListSlice) > 1 {
					//TODO: decided how to bubble up errors
					activeCommitStatusesState = append(activeCommitStatusesState, promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						Key:   status.Key,
						Phase: string(promoterv1alpha1.CommitPhasePending),
					})
					r.Recorder.Event(pc, "Warning", "ToManyMatchingSha", "There are to many matching sha's for the active commit status")
				} else if len(csListSlice) == 0 {
					//TODO: decided how to bubble up errors
					activeCommitStatusesState = append(activeCommitStatusesState, promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						Key:   status.Key,
						Phase: string(promoterv1alpha1.CommitPhasePending),
					})
					// We might not want to event here because of the potential for a lot of events, when say ArgoCD is slow at updating the status
					//r.Recorder.Event(pc, "Warning", "NoCommitStatusFound", "No commit status found for the active commit")
				}

			}
			pc.Status.Active.CommitStatuses = activeCommitStatusesState

		}

		if branch == pc.Spec.ProposedBranch {
			pc.Status.Proposed.Hydrated.Sha = shas.Hydrated
			commitTime, err := gitOperations.GetShaTime(ctx, shas.Hydrated)
			if err != nil {
				return err
			}
			pc.Status.Proposed.Hydrated.CommitTime = commitTime

			pc.Status.Proposed.Dry.Sha = shas.Dry
			commitTime, err = gitOperations.GetShaTime(ctx, shas.Dry)
			if err != nil {
				return err
			}
			pc.Status.Proposed.Dry.CommitTime = commitTime

			proposedCommitStatusesState := []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{}
			for _, status := range pc.Spec.ProposedCommitStatuses {
				var csListProposed promoterv1alpha1.CommitStatusList
				// Find all the replicasets that match the commit status configured name and the sha of the active hydrated commit
				err := r.List(ctx, &csListProposed, &client.ListOptions{
					LabelSelector: labels.SelectorFromSet(map[string]string{
						promoterv1alpha1.CommitStatusLabel: utils.KubeSafeLabel(ctx, status.Key),
					}),
					FieldSelector: fields.SelectorFromSet(map[string]string{
						".spec.sha": pc.Status.Proposed.Hydrated.Sha,
					}),
				})
				if err != nil {
					return err
				}

				// We don't want to capture any of the copied commits statuses that are used for GitHub/Provider UI experience only.
				csListSlice := []promoterv1alpha1.CommitStatus{}
				for _, item := range csListProposed.Items {
					if item.Labels[promoterv1alpha1.CommitStatusCopyLabel] != "true" {
						csListSlice = append(csListSlice, item)
					}
				}

				if len(csListSlice) == 1 {
					proposedCommitStatusesState = append(proposedCommitStatusesState, promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						Key:   status.Key,
						Phase: string(csListSlice[0].Status.Phase),
					})
				} else if len(csListSlice) > 1 {
					//TODO: decided how to bubble up errors
					proposedCommitStatusesState = append(proposedCommitStatusesState, promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						Key:   status.Key,
						Phase: string(promoterv1alpha1.CommitPhasePending),
					})
					r.Recorder.Event(pc, "Warning", "TooManyMatchingSha", "There are to many matching sha's for the proposed commit status")
				} else if len(csListSlice) == 0 {
					//TODO: decided how to bubble up errors
					proposedCommitStatusesState = append(proposedCommitStatusesState, promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
						Key:   status.Key,
						Phase: string(promoterv1alpha1.CommitPhasePending),
					})
					// We might not want to event here because of the potential for a lot of events, when say ArgoCD is slow at updating the status
					//r.Recorder.Event(pc, "Warning", "NoCommitStatusFound", "No commit status found for the active commit")
				}

			}
			pc.Status.Proposed.CommitStatuses = proposedCommitStatusesState

		}
	}

	return nil
}

func (r *ChangeTransferPolicyReconciler) mergeOrPullRequestPromote(ctx context.Context, gitOperations *git.GitOperations, ctp *promoterv1alpha1.ChangeTransferPolicy) error {
	if ctp.Status.Proposed.Dry.Sha == ctp.Status.Active.Dry.Sha {
		return nil
	}

	prRequired, err := gitOperations.IsPullRequestRequired(ctx, ctp.Spec.ActiveBranch, ctp.Spec.ProposedBranch)
	if err != nil {
		return err
	}

	if prRequired {
		err = r.creatOrUpdatePullRequest(ctx, ctp)
		if err != nil {
			return err
		}
	} else {
		err = gitOperations.PromoteEnvironmentWithMerge(ctx, ctp.Spec.ActiveBranch, ctp.Spec.ProposedBranch)
		if err != nil {
			return err
		}
	}

	return nil
}

func (r *ChangeTransferPolicyReconciler) creatOrUpdatePullRequest(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy) error {
	logger := log.FromContext(ctx)
	if ctp.Status.Proposed.Dry.Sha == ctp.Status.Active.Dry.Sha {
		// If the proposed dry sha is different from the active dry sha, create a pull request
		return nil
	}

	logger.V(4).Info("Proposed dry sha, does not match active", "proposedDrySha", ctp.Status.Proposed.Dry.Sha, "activeDrySha", ctp.Status.Active.Dry.Sha)
	gitRepo, err := utils.GetGitRepositorytFromObjectKey(ctx, r.Client, client.ObjectKey{Namespace: ctp.Namespace, Name: ctp.Spec.RepositoryReference.Name})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository: %w", err)
	}
	prName := utils.KubeSafeUniqueName(ctx, utils.GetPullRequestName(ctx, gitRepo.Spec.Owner, gitRepo.Spec.Name, ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch))

	var pr promoterv1alpha1.PullRequest
	err = r.Get(ctx, client.ObjectKey{
		Namespace: ctp.Namespace,
		Name:      prName,
	}, &pr)
	if err != nil {
		if errors.IsNotFound(err) {
			// The code below sets the ownership for the PullRequest Object
			kind := reflect.TypeOf(promoterv1alpha1.ChangeTransferPolicy{}).Name()
			gvk := promoterv1alpha1.GroupVersion.WithKind(kind)
			controllerRef := metav1.NewControllerRef(ctp, gvk)

			pr = promoterv1alpha1.PullRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:            prName,
					Namespace:       ctp.Namespace,
					OwnerReferences: []metav1.OwnerReference{*controllerRef},
					Labels: map[string]string{
						promoterv1alpha1.PromotionStrategyLabel:    utils.KubeSafeLabel(ctx, ctp.Labels["promoter.argoproj.io/promotion-strategy"]),
						promoterv1alpha1.ChangeTransferPolicyLabel: utils.KubeSafeLabel(ctx, ctp.Name),
						promoterv1alpha1.EnvironmentLabel:          utils.KubeSafeLabel(ctx, ctp.Spec.ActiveBranch),
					},
				},
				Spec: promoterv1alpha1.PullRequestSpec{
					RepositoryReference: ctp.Spec.RepositoryReference,
					Title:               fmt.Sprintf("Promote %s to `%s`", ctp.Status.Proposed.DryShaShort(), ctp.Spec.ActiveBranch),
					TargetBranch:        ctp.Spec.ActiveBranch,
					SourceBranch:        ctp.Spec.ProposedBranch,
					Description:         fmt.Sprintf("This PR is promoting the environment branch `%s` which is currently on dry sha %s to dry sha %s.", ctp.Spec.ActiveBranch, ctp.Status.Active.Dry.Sha, ctp.Status.Proposed.Dry.Sha),
					State:               "open",
				},
			}
			err = r.Create(ctx, &pr)
			if err != nil {
				return err
			}
			r.Recorder.Event(ctp, "Normal", "PullRequestCreated", fmt.Sprintf("Pull Request %s created", pr.Name))
			logger.V(4).Info("Created pull request")
		} else {
			return err
		}
	} else {
		// Pull request already exists, update it.
		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			prUpdated := promoterv1alpha1.PullRequest{}
			err := r.Get(ctx, client.ObjectKey{Namespace: pr.Namespace, Name: pr.Name}, &prUpdated)
			if err != nil {
				return err
			}
			prUpdated.Spec.RepositoryReference = ctp.Spec.RepositoryReference
			prUpdated.Spec.Title = fmt.Sprintf("Promote %s to `%s`", ctp.Status.Proposed.DryShaShort(), ctp.Spec.ActiveBranch)
			prUpdated.Spec.TargetBranch = ctp.Spec.ActiveBranch
			prUpdated.Spec.SourceBranch = ctp.Spec.ProposedBranch
			prUpdated.Spec.Description = fmt.Sprintf("This PR is promoting the environment branch `%s` which is currently on dry sha %s to dry sha %s.", ctp.Spec.ActiveBranch, ctp.Status.Active.Dry.Sha, ctp.Status.Proposed.Dry.Sha)
			return r.Update(ctx, &prUpdated)
		})
		if err != nil {
			return err
		}
		//r.Recorder.Event(ctp, "Normal", "PullRequestUpdated", fmt.Sprintf("Pull Request %s updated", pr.Name))
		logger.V(4).Info("Updated pull request resource")
	}

	return nil
}
