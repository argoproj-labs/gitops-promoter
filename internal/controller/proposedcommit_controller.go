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

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/record"

	"k8s.io/client-go/util/retry"

	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"k8s.io/apimachinery/pkg/api/errors"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

type ProposedCommitReconcilerConfig struct {
	RequeueDuration time.Duration
}

// ProposedCommitReconciler reconciles a ProposedCommit object
type ProposedCommitReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	PathLookup utils.PathLookup
	Recorder   record.EventRecorder
	Config     ProposedCommitReconcilerConfig
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=proposedcommits,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=proposedcommits/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=proposedcommits/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ProposedCommit object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *ProposedCommitReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pc promoterv1alpha1.ProposedCommit
	err := r.Get(ctx, req.NamespacedName, &pc, &client.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("ProposedCommit not found", "namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}

		logger.Error(err, "failed to get ProposedCommit", "namespace", req.Namespace, "name", req.Name)
		return ctrl.Result{}, err
	}

	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, *pc.Spec.RepositoryReference, &pc)
	if err != nil {
		return ctrl.Result{}, err
	}

	gitAuthProvider, err := r.getGitAuthProvider(ctx, scmProvider, secret)
	if err != nil {
		return ctrl.Result{}, err
	}
	gitOperations, err := git.NewGitOperations(ctx, r.Client, gitAuthProvider, r.PathLookup, *pc.Spec.RepositoryReference, &pc, pc.Spec.ActiveBranch)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = gitOperations.CloneRepo(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.calculateStatus(ctx, &pc, gitOperations)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.creatOrUpdatePullRequest(ctx, &pc)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.Status().Update(ctx, &pc)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{
		Requeue:      true,
		RequeueAfter: r.Config.RequeueDuration,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProposedCommitReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.ProposedCommit{}).
		Complete(r)
}

func (r *ProposedCommitReconciler) getGitAuthProvider(ctx context.Context, scmProvider *promoterv1alpha1.ScmProvider, secret *v1.Secret) (scms.GitOperationsProvider, error) {
	logger := log.FromContext(ctx)
	switch {
	case scmProvider.Spec.Fake != nil:
		logger.V(4).Info("Creating fake git authentication provider")
		return fake.NewFakeGitAuthenticationProvider(scmProvider, secret), nil
	case scmProvider.Spec.GitHub != nil:
		logger.V(4).Info("Creating GitHub git authentication provider")
		return github.NewGithubGitAuthenticationProvider(scmProvider, secret), nil
	default:
		return nil, nil
	}
}

func (r *ProposedCommitReconciler) calculateStatus(ctx context.Context, pc *promoterv1alpha1.ProposedCommit, gitOperations *git.GitOperations) error {
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

			activeCommitStatusesState := []promoterv1alpha1.ProposedCommitCommitStatusPhase{}
			for _, status := range pc.Spec.ActiveCommitStatuses {
				var csListActive promoterv1alpha1.CommitStatusList
				// Find all the replicasets that match the commit status configured name and the sha of the active hydrated commit
				err := r.List(ctx, &csListActive, &client.ListOptions{
					LabelSelector: labels.SelectorFromSet(map[string]string{
						"promoter.argoproj.io/commit-status": utils.KubeSafeLabel(ctx, status.Key),
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
					if item.Labels["promoter.argoproj.io/commit-status-copy"] != "true" {
						csListSlice = append(csListSlice, item)
					}
				}

				if len(csListSlice) == 1 {
					activeCommitStatusesState = append(activeCommitStatusesState, promoterv1alpha1.ProposedCommitCommitStatusPhase{
						Key:   status.Key,
						Phase: string(csListSlice[0].Status.Phase),
					})
				} else if len(csListSlice) > 1 {
					//TODO: decided how to bubble up errors
					activeCommitStatusesState = append(activeCommitStatusesState, promoterv1alpha1.ProposedCommitCommitStatusPhase{
						Key:   status.Key,
						Phase: "pending",
					})
					r.Recorder.Event(pc, "Warning", "ToManyMatchingSha", "There are to many matching sha's for the active commit status")
				} else if len(csListSlice) == 0 {
					//TODO: decided how to bubble up errors
					activeCommitStatusesState = append(activeCommitStatusesState, promoterv1alpha1.ProposedCommitCommitStatusPhase{
						Key:   status.Key,
						Phase: "pending",
					})
					r.Recorder.Event(pc, "Warning", "NoCommitStatusFound", "No commit status found for the active commit")
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

			allProposedCSList := []promoterv1alpha1.CommitStatus{}
			proposedCommitStatusesState := []promoterv1alpha1.ProposedCommitCommitStatusPhase{}
			for _, status := range pc.Spec.ProposedCommitStatuses {
				var csListProposed promoterv1alpha1.CommitStatusList
				// Find all the replicasets that match the commit status configured name and the sha of the active hydrated commit
				err := r.List(ctx, &csListProposed, &client.ListOptions{
					LabelSelector: labels.SelectorFromSet(map[string]string{
						"promoter.argoproj.io/commit-status": utils.KubeSafeLabel(ctx, status.Key),
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
					if item.Labels["promoter.argoproj.io/commit-status-copy"] != "true" {
						csListSlice = append(csListSlice, item)
					}
				}

				if len(csListSlice) == 1 {
					allProposedCSList = append(allProposedCSList, csListSlice[0])
					proposedCommitStatusesState = append(proposedCommitStatusesState, promoterv1alpha1.ProposedCommitCommitStatusPhase{
						Key:   status.Key,
						Phase: string(csListSlice[0].Status.Phase),
					})
				} else if len(csListSlice) > 1 {
					//TODO: decided how to bubble up errors
					proposedCommitStatusesState = append(proposedCommitStatusesState, promoterv1alpha1.ProposedCommitCommitStatusPhase{
						Key:   status.Key,
						Phase: "pending",
					})
					r.Recorder.Event(pc, "Warning", "TooManyMatchingSha", "There are to many matching sha's for the active commit status")
				} else if len(csListSlice) == 0 {
					//TODO: decided how to bubble up errors
					proposedCommitStatusesState = append(proposedCommitStatusesState, promoterv1alpha1.ProposedCommitCommitStatusPhase{
						Key:   status.Key,
						Phase: "pending",
					})
					r.Recorder.Event(pc, "Warning", "NoCommitStatusFound", "No commit status found for the active commit")
				}

			}
			pc.Status.Proposed.CommitStatuses = proposedCommitStatusesState

		}
	}

	return nil
}

func (r *ProposedCommitReconciler) creatOrUpdatePullRequest(ctx context.Context, pc *promoterv1alpha1.ProposedCommit) error {
	logger := log.FromContext(ctx)

	if pc.Status.Proposed.Dry.Sha != pc.Status.Active.Dry.Sha {
		// If the proposed dry sha is different from the active dry sha, create a pull request

		logger.V(4).Info("Proposed dry sha, does not match active", "proposedDrySha", pc.Status.Proposed.Dry.Sha, "activeDrySha", pc.Status.Active.Dry.Sha)
		prName := fmt.Sprintf("%s-%s-%s-%s", pc.Spec.RepositoryReference.Owner, pc.Spec.RepositoryReference.Name, pc.Spec.ProposedBranch, pc.Spec.ActiveBranch)
		prName = utils.KubeSafeUniqueName(ctx, prName)

		var pr promoterv1alpha1.PullRequest
		err := r.Get(ctx, client.ObjectKey{
			Namespace: pc.Namespace,
			Name:      prName,
		}, &pr)
		if err != nil {
			if errors.IsNotFound(err) {

				// The code below sets the ownership for the PullRequest Object
				kind := reflect.TypeOf(promoterv1alpha1.ProposedCommit{}).Name()
				gvk := promoterv1alpha1.GroupVersion.WithKind(kind)
				controllerRef := metav1.NewControllerRef(pc, gvk)

				pr = promoterv1alpha1.PullRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name:            prName,
						Namespace:       pc.Namespace,
						OwnerReferences: []metav1.OwnerReference{*controllerRef},
						Labels: map[string]string{
							"promoter.argoproj.io/promotion-strategy": utils.KubeSafeLabel(ctx, pc.Labels["promoter.argoproj.io/promotion-strategy"]),
							"promoter.argoproj.io/proposed-commit":    utils.KubeSafeLabel(ctx, pc.Name),
							"promoter.argoproj.io/environment":        utils.KubeSafeLabel(ctx, pc.Spec.ActiveBranch),
						},
					},
					Spec: promoterv1alpha1.PullRequestSpec{
						RepositoryReference: pc.Spec.RepositoryReference,
						Title:               fmt.Sprintf("Promote %s to `%s`", pc.Status.Proposed.DryShaShort(), pc.Spec.ActiveBranch),
						TargetBranch:        pc.Spec.ActiveBranch,
						SourceBranch:        pc.Spec.ProposedBranch,
						Description:         fmt.Sprintf("This PR is promoting the environment branch `%s` which is currently on dry sha %s to dry sha %s.", pc.Spec.ActiveBranch, pc.Status.Active.Dry.Sha, pc.Status.Proposed.Dry.Sha),
						State:               "open",
					},
				}
				err = r.Create(ctx, &pr)
				if err != nil {
					return err
				}
				r.Recorder.Event(pc, "Normal", "PullRequestCreated", fmt.Sprintf("Pull Request %s created", pr.Name))
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
				prUpdated.Spec.RepositoryReference = pc.Spec.RepositoryReference
				prUpdated.Spec.Title = fmt.Sprintf("Promote %s to `%s`", pc.Status.Proposed.DryShaShort(), pc.Spec.ActiveBranch)
				prUpdated.Spec.TargetBranch = pc.Spec.ActiveBranch
				prUpdated.Spec.SourceBranch = pc.Spec.ProposedBranch
				prUpdated.Spec.Description = fmt.Sprintf("This PR is promoting the environment branch `%s` which is currently on dry sha %s to dry sha %s.", pc.Spec.ActiveBranch, pc.Status.Active.Dry.Sha, pc.Status.Proposed.Dry.Sha)
				return r.Update(ctx, &prUpdated)
			})
			if err != nil {
				return err
			}
			r.Recorder.Event(pc, "Normal", "PullRequestUpdated", fmt.Sprintf("Pull Request %s updated", pr.Name))
			logger.V(4).Info("Updated pull request resource")
		}
	}

	return nil
}
