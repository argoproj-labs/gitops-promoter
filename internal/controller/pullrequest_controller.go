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
	"sort"
	"time"

	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/forgejo"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitlab"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// PullRequestReconciler reconciles a PullRequest object
type PullRequestReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	SettingsMgr *settings.Manager
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *PullRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling PullRequest")
	startTime := time.Now()

	var pr promoterv1alpha1.PullRequest
	defer utils.HandleReconciliationResult(ctx, startTime, &pr, r.Client, r.Recorder, &err)

	if err := r.Get(ctx, req.NamespacedName, &pr); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("PullRequest not found", "namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PullRequest: %w", err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	meta.RemoveStatusCondition(pr.GetConditions(), string(promoterConditions.Ready))

	provider, err := r.getPullRequestProvider(ctx, pr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get PullRequest provider: %w", err)
	}

	if deleted, err := r.handleFinalizer(ctx, &pr, provider); err != nil || deleted {
		return ctrl.Result{}, err
	}

	if pr.Status.State == promoterv1alpha1.PullRequestMerged || pr.Status.State == promoterv1alpha1.PullRequestClosed {
		logger.Info("Cleaning up close and merged pull request", "pullRequestID", pr.Status.ID)
		if err := r.Delete(ctx, &pr); err != nil && !errors.IsNotFound(err) {
			logger.Error(err, "Failed to delete PullRequest")
			return ctrl.Result{}, fmt.Errorf("failed to delete PullRequest: %w", err)
		}
		return ctrl.Result{}, nil
	}

	logger.Info("Checking for open PR on provider")
	found, foundState, err := provider.FindOpen(ctx, pr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check for open PR: %w", err)
	}

	// Calculate the state of the PR based on the provider, if found we have to be open
	if found {
		pr.Status.State = promoterv1alpha1.PullRequestOpen
		pr.Status.ID = foundState.ID
		pr.Status.PRCreationTime = foundState.PRCreationTime
		pr.Status.Url = foundState.Url
	} else if pr.Status.ID != "" {
		// If we don't find the PR, but we have an ID, it means it was deleted on the provider side
		if err := r.Delete(ctx, &pr); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to delete PullRequest resource due to SCM not found: %w", err)
		}
		return ctrl.Result{}, nil
	}

	logger.Info("Reconciling PullRequest state", "desired", pr.Spec.State, "current", pr.Status.State)

	//nolint:nestif // There's not a great way to simplify this section.
	if pr.Status.State != pr.Spec.State {
		switch pr.Spec.State {
		case promoterv1alpha1.PullRequestOpen:
			if pr.Status.ID == "" {
				// Because status id is empty, we need to create a new pull request
				logger.Info("Creating PullRequest")
				if err := r.createPullRequest(ctx, &pr, provider); err != nil {
					return ctrl.Result{}, fmt.Errorf("failed to create pull request: %w", err)
				}
			}
		case promoterv1alpha1.PullRequestMerged:
			logger.Info("Merging PullRequest")
			if err := r.mergePullRequest(ctx, &pr, provider); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to merge pull request: %w", err)
			}
			if err := r.Delete(ctx, &pr); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete PullRequest: %w", err)
			}
			return ctrl.Result{}, nil
		case promoterv1alpha1.PullRequestClosed:
			logger.Info("Closing PullRequest")
			if err := r.closePullRequest(ctx, &pr, provider); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to close pull request: %w", err)
			}
			if err := r.Delete(ctx, &pr); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete PullRequest: %w", err)
			}
			return ctrl.Result{}, nil
		}
	} else {
		logger.Info("Updating PullRequest")
		if err := r.updatePullRequest(ctx, pr, provider); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update pull request: %w", err)
		}
	}

	if err := r.Status().Update(ctx, &pr); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update PullRequest status: %w", err)
	}

	logger.Info("no known state transitions needed", "specState", pr.Spec.State, "statusState", pr.Status.State)

	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.PullRequestConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get pull request requeue duration: %w", err)
	}

	return ctrl.Result{RequeueAfter: requeueDuration}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PullRequestReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.PullRequestConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get pull request rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.PullRequestConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get pull request max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.PullRequest{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

func (r *PullRequestReconciler) getPullRequestProvider(ctx context.Context, pr promoterv1alpha1.PullRequest) (scms.PullRequestProvider, error) {
	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, r.SettingsMgr.GetControllerNamespace(), pr.Spec.RepositoryReference, &pr)
	if err != nil {
		return nil, fmt.Errorf("failed to get ScmProvider and secret: %w", err)
	}

	gitRepository, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{Namespace: pr.Namespace, Name: pr.Spec.RepositoryReference.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	switch {
	case scmProvider.GetSpec().GitHub != nil:
		return github.NewGithubPullRequestProvider(ctx, r.Client, scmProvider, *secret, gitRepository.Spec.GitHub.Owner) //nolint:wrapcheck
	case scmProvider.GetSpec().GitLab != nil:
		return gitlab.NewGitlabPullRequestProvider(r.Client, *secret, scmProvider.GetSpec().GitLab.Domain) //nolint:wrapcheck
	case scmProvider.GetSpec().Forgejo != nil:
		return forgejo.NewForgejoPullRequestProvider(r.Client, *secret, scmProvider.GetSpec().Forgejo.Domain) //nolint:wrapcheck
	case scmProvider.GetSpec().AzureDevOps != nil:
		return azuredevops.NewAzdoPullRequestProvider(ctx, r.Client, scmProvider, *secret, scmProvider.GetSpec().AzureDevOps.Organization) //nolint:wrapcheck
	case scmProvider.GetSpec().Fake != nil:
		return fake.NewFakePullRequestProvider(r.Client), nil
	default:
		return nil, fmt.Errorf("unsupported SCM provider: %s", scmProvider.GetName())
	}
}

func (r *PullRequestReconciler) handleFinalizer(ctx context.Context, pr *promoterv1alpha1.PullRequest, provider scms.PullRequestProvider) (bool, error) {
	finalizer := "pullrequest.promoter.argoporoj.io/finalizer"

	if pr.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(pr, finalizer) {
			// Not being deleted and already has finalizer, nothing to do.
			return false, nil
		}

		// Finalizer is missing, add it.
		return false, retry.RetryOnConflict(retry.DefaultRetry, func() error { //nolint:wrapcheck
			if err := r.Get(ctx, client.ObjectKeyFromObject(pr), pr); err != nil {
				return err //nolint:wrapcheck
			}
			if controllerutil.AddFinalizer(pr, finalizer) {
				return r.Update(ctx, pr)
			}
			return nil
		})
	}

	// If we're here, the object is being deleted
	if !controllerutil.ContainsFinalizer(pr, finalizer) {
		// Finalizer already removed, nothing to do.
		return false, nil
	}

	if err := r.closePullRequest(ctx, pr, provider); err != nil {
		return false, fmt.Errorf("failed to close pull request: %w", err)
	}
	controllerutil.RemoveFinalizer(pr, finalizer)
	if err := r.Update(ctx, pr); err != nil {
		return true, fmt.Errorf("failed to remove finalizer: %w", err)
	}
	return true, nil
}

func (r *PullRequestReconciler) createPullRequest(ctx context.Context, pr *promoterv1alpha1.PullRequest, provider scms.PullRequestProvider) error {
	id, err := provider.Create(ctx, pr.Spec.Title, pr.Spec.SourceBranch, pr.Spec.TargetBranch, pr.Spec.Description, *pr)
	if err != nil {
		return fmt.Errorf("failed to create pull request: %w", err)
	}
	pr.Status.State = promoterv1alpha1.PullRequestOpen
	pr.Status.PRCreationTime = metav1.Now()
	pr.Status.ID = id

	url, err := provider.GetUrl(ctx, *pr)
	if err != nil {
		return fmt.Errorf("failed to get pull request URL: %w", err)
	}
	pr.Status.Url = url

	return nil
}

func (r *PullRequestReconciler) updatePullRequest(ctx context.Context, pr promoterv1alpha1.PullRequest, provider scms.PullRequestProvider) error {
	if err := provider.Update(ctx, pr.Spec.Title, pr.Spec.Description, pr); err != nil {
		return fmt.Errorf("failed to update pull request: %w", err)
	}
	r.Recorder.Event(&pr, "Normal", constants.PullRequestUpdatedReason, fmt.Sprintf("Pull Request %s updated", pr.Name))
	return nil
}

func (r *PullRequestReconciler) mergePullRequest(ctx context.Context, pr *promoterv1alpha1.PullRequest, provider scms.PullRequestProvider) error {
	mergedTime := metav1.Now()

	updatedMessage, err := git.AddTrailerToCommitMessage(
		pr.Spec.Commit.Message,
		constants.TrailerPullRequestMergeTime,
		mergedTime.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("failed to add trailer to commit message: %w", err)
	}

	// Update the commit message with the new trailers
	pr.Spec.Commit.Message = updatedMessage

	if err := provider.Merge(ctx, *pr); err != nil {
		return fmt.Errorf("failed to merge pull request: %w", err)
	}
	pr.Status.State = promoterv1alpha1.PullRequestMerged
	return nil
}

type trailers map[string]string

func (t trailers) String() string {
	keys := make([]string, 0, len(t))

	for k := range t {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var result string
	for _, k := range keys {
		result += fmt.Sprintf("%s: %s\n", k, t[k])
	}
	return result
}

func (r *PullRequestReconciler) closePullRequest(ctx context.Context, pr *promoterv1alpha1.PullRequest, provider scms.PullRequestProvider) error {
	if pr.Status.State == promoterv1alpha1.PullRequestMerged {
		return nil
	}
	if err := provider.Close(ctx, *pr); err != nil {
		return fmt.Errorf("failed to close pull request: %w", err)
	}
	pr.Status.State = promoterv1alpha1.PullRequestClosed
	return nil
}
