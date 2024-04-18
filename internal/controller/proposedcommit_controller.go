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
	"github.com/argoproj/promoter/internal/git"
	"github.com/argoproj/promoter/internal/scms/fake"
	"github.com/argoproj/promoter/internal/utils"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"regexp"
	"time"

	"github.com/argoproj/promoter/internal/scms"
	"github.com/argoproj/promoter/internal/scms/github"
	"k8s.io/apimachinery/pkg/api/errors"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj/promoter/api/v1alpha1"
)

// ProposedCommitReconciler reconciles a ProposedCommit object
type ProposedCommitReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	PathLookup utils.PathLookup
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
	_ = log.FromContext(ctx)

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

	gitAuthProvider, err := r.getGitAuthProvider(ctx, scmProvider, secret, pc)
	if err != nil {
		return ctrl.Result{}, err
	}
	gitOperations, err := git.NewGitOperations(ctx, r.Client, gitAuthProvider, r.PathLookup, *pc.Spec.RepositoryReference, &pc)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = gitOperations.CloneRepo(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	dryBranchShas, hydratedBranchShas, err := gitOperations.GetBranchShas(ctx, []string{pc.Spec.ActiveBranch, pc.Spec.ProposedBranch})
	if err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("Branch SHAs", "dryBranchShas", dryBranchShas, "hydratedBranchShas", hydratedBranchShas)

	for branch, _ := range hydratedBranchShas {
		if pc.Status.Active == nil {
			pc.Status.Active = &promoterv1alpha1.BranchState{}
		}

		if pc.Status.Proposed == nil {
			pc.Status.Proposed = &promoterv1alpha1.BranchState{}
		}

		if branch == pc.Spec.ActiveBranch {
			pc.Status.Active.HydratedSha = hydratedBranchShas[branch]
			pc.Status.Active.DrySha = dryBranchShas[branch]
		}
		if branch == pc.Spec.ProposedBranch {
			pc.Status.Proposed.HydratedSha = hydratedBranchShas[branch]
			pc.Status.Proposed.DrySha = dryBranchShas[branch]
		}
	}

	if pc.Status.Proposed.DrySha != pc.Status.Active.DrySha {
		logger.Info("Proposed dry sha, does not match active", "proposedDrySha", pc.Status.Proposed.DrySha, "activeDrySha", pc.Status.Active.DrySha)
		prName := fmt.Sprintf("%s-%s-%s-%s", pc.Spec.RepositoryReference.Owner, pc.Spec.RepositoryReference.Name, pc.Spec.ProposedBranch, pc.Spec.ActiveBranch)
		prName = utils.TruncateString(prName, 250)
		m1 := regexp.MustCompile("[^a-zA-Z0-9]+")
		prName = m1.ReplaceAllString(prName, "-")

		var pr promoterv1alpha1.PullRequest
		err = r.Get(ctx, client.ObjectKey{
			Namespace: pc.Namespace,
			Name:      prName,
		}, &pr)
		if err != nil {
			if errors.IsNotFound(err) {
				pr = promoterv1alpha1.PullRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name:      prName,
						Namespace: pc.Namespace,
						OwnerReferences: []metav1.OwnerReference{{
							APIVersion:         pc.APIVersion,
							Kind:               pc.Kind,
							Name:               pc.Name,
							UID:                pc.UID,
							BlockOwnerDeletion: pointer.Bool(true),
						}},
					},
					Spec: promoterv1alpha1.PullRequestSpec{
						RepositoryReference: pc.Spec.RepositoryReference,
						Title:               fmt.Sprintf("Promotion of `%s` to dry sha `%s`", pc.Spec.ActiveBranch, pc.Status.Proposed.DrySha),
						TargetBranch:        pc.Spec.ActiveBranch,
						SourceBranch:        pc.Spec.ProposedBranch,
						Description:         fmt.Sprintf("This pr is promoting the environment branch `%s` which currently on dry sha `%s` to dry sha `%s`.", pc.Spec.ActiveBranch, pc.Status.Active.DrySha, pc.Status.Proposed.DrySha),
						State:               "open",
					},
				}
				err = r.Create(ctx, &pr)
				if err != nil {
					return ctrl.Result{}, err
				}
				logger.Info("Created pull request")
			} else {
				return ctrl.Result{}, err
			}
		} else {
			pr.Spec = promoterv1alpha1.PullRequestSpec{
				RepositoryReference: pc.Spec.RepositoryReference,
				Title:               fmt.Sprintf("Promotion of `%s` to dry sha `%s`", pc.Spec.ActiveBranch, pc.Status.Proposed.DrySha),
				TargetBranch:        pc.Spec.ActiveBranch,
				SourceBranch:        pc.Spec.ProposedBranch,
				Description:         fmt.Sprintf("This pr is promoting the environment branch `%s` which currently on dry sha `%s` to dry sha `%s`.", pc.Spec.ActiveBranch, pc.Status.Active.DrySha, pc.Status.Proposed.DrySha),
				State:               "open",
			}
			err = r.Update(ctx, &pr)
			if err != nil {
				return ctrl.Result{}, err
			}
			logger.Info("Updated pull request")
		}
	}

	err = r.Status().Update(ctx, &pc)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{
		Requeue:      true,
		RequeueAfter: 1 * time.Minute,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ProposedCommitReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.ProposedCommit{}).
		Complete(r)
}

func (r *ProposedCommitReconciler) getGitAuthProvider(ctx context.Context, scmProvider *promoterv1alpha1.ScmProvider, secret *v1.Secret, pc promoterv1alpha1.ProposedCommit) (scms.GitOperationsProvider, error) {
	switch {
	case scmProvider.Spec.Fake != nil:
		return fake.NewFakeGitAuthenticationProvider(scmProvider, secret), nil
	case scmProvider.Spec.GitHub != nil:
		return github.NewGithubGitAuthenticationProvider(scmProvider, secret), nil
	default:
		return nil, nil
	}
}
