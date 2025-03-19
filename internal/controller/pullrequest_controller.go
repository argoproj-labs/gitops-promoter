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

	"k8s.io/client-go/tools/record"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"k8s.io/apimachinery/pkg/api/errors"
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
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *PullRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pr promoterv1alpha1.PullRequest
	err := r.Get(ctx, req.NamespacedName, &pr, &client.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("PullRequest not found", "namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}

		logger.Error(err, "failed to get PullRequest", "namespace", req.Namespace, "name", req.Name)
		return ctrl.Result{}, fmt.Errorf("failed to get PullRequest: %w", err)
	}

	pullRequestProvider, err := r.getPullRequestProvider(ctx, pr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get PullRequest provider: %w", err)
	}
	if pullRequestProvider == nil {
		return ctrl.Result{}, fmt.Errorf("failed to get pull request provider, pullRequestProvider is nil")
	}

	deleted, err := r.handleFinalizer(ctx, &pr, pullRequestProvider)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to handle finalizer: %w", err)
	}
	if deleted {
		return ctrl.Result{}, nil
	}

	found, err := pullRequestProvider.FindOpen(ctx, &pr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check for open PR: %w", err)
	}

	// We can't find an open PR on the provider, and we have had a status state before, we should now delete it because
	// it no longer exists on provider.
	if !found && pr.Status.State != "" {
		logger.Info("Deleting Pull Request, because no open PR found on provider")
		err = r.Delete(ctx, &pr)
		if err != nil {
			if errors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to delete PullRequest: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// If we have a status state, and the status state is the same as the spec state, and the observed generation is the same as the generation, we don't need to reconcile.
	if pr.Status.State != "" && pr.Spec.State == pr.Status.State && pr.Status.ObservedGeneration == pr.Generation {
		logger.V(4).Info("Reconcile not needed")
		return ctrl.Result{}, nil
	}

	// We want the PR to be open, but it's not open on the provider, we should create it.
	if pr.Spec.State == promoterv1alpha1.PullRequestOpen && pr.Status.State != promoterv1alpha1.PullRequestOpen {
		err = r.createPullRequest(ctx, &pr, pullRequestProvider)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to create pull request: %w", err)
		}
	}

	// We want to merge the PR, but it's not merged on the provider, we should merge it on the provicer and delete on k8s.
	if pr.Spec.State == promoterv1alpha1.PullRequestMerged && pr.Status.State != promoterv1alpha1.PullRequestMerged {
		err = r.mergePullRequest(ctx, &pr, pullRequestProvider)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to merge pull request: %w", err)
		}

		err = r.Status().Update(ctx, &pr)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("merged pull request in the SCM, but failed to update PullRequest status: %w", err)
		}
		// We requeue here because we want to delete the resource on the next reconcile, this does cause more API calls to the provider
		// but is cleaner than deleting right away. Will revisit this.
		return ctrl.Result{Requeue: true}, nil
	}

	// We want to close the PR, but it's not closed on the provider based on status, we should close it on provider and delete it from k8s.
	if pr.Spec.State == promoterv1alpha1.PullRequestClosed && pr.Status.State != promoterv1alpha1.PullRequestClosed {
		err := r.closePullRequest(ctx, &pr, pullRequestProvider)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to close pull request: %w", err)
		}

		err = r.Status().Update(ctx, &pr)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("closed pull request, but failed to update PullRequest status: %w", err)
		}
		// We requeue here because we want to delete the resource on the next reconcile, this does cause more API calls to the provider
		// but is cleaner than deleting right away. Will revisit this.
		return ctrl.Result{Requeue: true}, nil
	}

	if pr.Status.ObservedGeneration != pr.Generation {
		// TODO: is there more work avoidance we can do here? Just because the generation has changed, do we know that
		//       we need to hit the SCM? For example, an annotation update probably shouldn't cause an API call.
		err = r.updatePullRequest(ctx, pr, pullRequestProvider)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update pull request: %w", err)
		}
	}

	// TODO: can we just skip this status update if the generation hasn't changed?
	pr.Status.ObservedGeneration = pr.Generation
	err = r.Status().Update(ctx, &pr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update PullRequest status: %w", err)
	}

	logger.Info("no known state transitions needed", "specState", pr.Spec.State, "statusState", pr.Status.State)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PullRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.PullRequest{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

func (r *PullRequestReconciler) getPullRequestProvider(ctx context.Context, pr promoterv1alpha1.PullRequest) (scms.PullRequestProvider, error) {
	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, pr.Spec.RepositoryReference, &pr)
	if err != nil {
		return nil, fmt.Errorf("failed to get ScmProvider and secret for repo %q: %w", pr.Spec.RepositoryReference.Name, err)
	}

	switch {
	case scmProvider.Spec.GitHub != nil:
		var p *github.PullRequest
		p, err = github.NewGithubPullRequestProvider(r.Client, *secret, scmProvider.Spec.GitHub.Domain)
		if err != nil {
			return nil, fmt.Errorf("failed to get GitHub provider for domain %q and secret %q: %w", scmProvider.Spec.GitHub.Domain, secret.Name, err)
		}
		return p, nil
	case scmProvider.Spec.Fake != nil:
		return fake.NewFakePullRequestProvider(r.Client), nil
	default:
		return nil, nil
	}
}

func (r *PullRequestReconciler) handleFinalizer(ctx context.Context, pr *promoterv1alpha1.PullRequest, pullRequestProvider scms.PullRequestProvider) (bool, error) {
	// name of our custom finalizer
	finalizerName := "pullrequest.promoter.argoporoj.io/finalizer"

	var err error
	// examine DeletionTimestamp to determine if object is under deletion
	if pr.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// to registering our finalizer.
		if !controllerutil.ContainsFinalizer(pr, finalizerName) {
			err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
				// TODO: consider skipping the Get on the initial attempt, the object may already be up to date.
				err = r.Get(ctx, client.ObjectKeyFromObject(pr), pr)
				if err != nil {
					return fmt.Errorf("failed to get PullRequest %q: %w", pr.Name, err)
				}
				updated := controllerutil.AddFinalizer(pr, finalizerName)
				if !updated {
					// Nothing to update.
					return nil
				}
				// No need to wrap the error, we'll wrap it outside the retry.
				return r.Update(ctx, pr)
			})
			if err != nil {
				return false, fmt.Errorf("failed to add finalizer: %w", err)
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(pr, finalizerName) {
			// our finalizer is present, so lets handle any external dependency
			err = r.closePullRequest(ctx, pr, pullRequestProvider)
			if err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried.
				return false, fmt.Errorf("failed to close pull request %q: %w", pr.Status.ID, err)
			}

			// remove our finalizer from the list and update it.
			controllerutil.RemoveFinalizer(pr, finalizerName)
			if err = r.Update(ctx, pr); err != nil {
				return true, fmt.Errorf("failed to remove finalizer: %w", err)
			}
			return true, nil
		}
	}

	return false, nil
}

func (r *PullRequestReconciler) createPullRequest(ctx context.Context, pr *promoterv1alpha1.PullRequest, pullRequestProvider scms.PullRequestProvider) error {
	logger := log.FromContext(ctx)
	logger.Info("Opening Pull Request")

	if pr == nil {
		return fmt.Errorf("failed to get pull request provider, pullRequestProvider is nil in createPullRequest")
	}

	id, err := pullRequestProvider.Create(
		ctx,
		pr.Spec.Title,
		pr.Spec.SourceBranch,
		pr.Spec.TargetBranch,
		pr.Spec.Description,
		pr)
	if err != nil {
		return fmt.Errorf("failed to create pull request: %w", err)
	}

	pr.Status.State = promoterv1alpha1.PullRequestOpen
	pr.Status.PRCreationTime = metav1.Now()
	pr.Status.ID = id
	return nil
}

func (r *PullRequestReconciler) updatePullRequest(ctx context.Context, pr promoterv1alpha1.PullRequest, pullRequestProvider scms.PullRequestProvider) error {
	logger := log.FromContext(ctx)
	logger.Info("Updating Pull Request")

	if pullRequestProvider == nil {
		return fmt.Errorf("failed to get pull request provider, pullRequestProvider is nil in updatePullRequest")
	}

	err := pullRequestProvider.Update(ctx, pr.Spec.Title, pr.Spec.Description, &pr)
	r.Recorder.Event(&pr, "Normal", "PullRequestUpdated", fmt.Sprintf("Pull Request %s updated", pr.Name))
	if err != nil {
		return fmt.Errorf("failed to update pull request %q: %w", pr.Status.ID, err)
	}

	return nil
}

func (r *PullRequestReconciler) mergePullRequest(ctx context.Context, pr *promoterv1alpha1.PullRequest, pullRequestProvider scms.PullRequestProvider) error {
	logger := log.FromContext(ctx)

	if pullRequestProvider == nil {
		return fmt.Errorf("failed to get pull request provider, pullRequestProvider is nil in mergePullRequest")
	}

	logger.Info("Merging Pull Request", "namespace", pr.Namespace, "name", pr.Name)
	err := pullRequestProvider.Merge(ctx, "", pr)
	if err != nil {
		return fmt.Errorf("failed to merge pull request %q: %w", pr.Status.ID, err)
	}

	pr.Status.State = promoterv1alpha1.PullRequestMerged

	return nil
}

func (r *PullRequestReconciler) closePullRequest(ctx context.Context, pr *promoterv1alpha1.PullRequest, pullRequestProvider scms.PullRequestProvider) error {
	logger := log.FromContext(ctx)

	if pullRequestProvider == nil {
		return fmt.Errorf("failed to get pull request provider, pullRequestProvider is nil in closePullRequest")
	}

	if pr.Status.State == promoterv1alpha1.PullRequestMerged {
		return nil
	}

	logger.Info("Closing Pull Request")
	err := pullRequestProvider.Close(ctx, pr)
	if err != nil {
		return fmt.Errorf("failed to close pull request %q: %w", pr.Status.ID, err)
	}

	pr.Status.State = promoterv1alpha1.PullRequestClosed
	return nil
}
