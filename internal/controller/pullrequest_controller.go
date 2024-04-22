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

	"github.com/argoproj/promoter/internal/utils"

	"github.com/argoproj/promoter/internal/scms/fake"
	"github.com/argoproj/promoter/internal/scms/github"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/argoproj/promoter/internal/scms"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj/promoter/api/v1alpha1"
)

// PullRequestReconciler reconciles a PullRequest object
type PullRequestReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	//InformerFactory informers.SharedInformerFactory
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the PullRequest object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
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
		return ctrl.Result{}, err
	}

	pullRequestProvider, err := r.getPullRequestProvider(ctx, pr)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = r.handleFinalizer(ctx, &pr, pullRequestProvider)
	if err != nil {
		return ctrl.Result{}, err
	}

	hash, err := pr.Hash()
	if err != nil {
		return ctrl.Result{}, err
	}

	if pr.Status.State != "" && pr.Spec.State == pr.Status.State && pr.Status.SpecHash == hash {
		logger.Info("Reconcile not needed")
		return ctrl.Result{}, nil
	}

	if pr.Spec.State == promoterv1alpha1.Open && pr.Status.State != promoterv1alpha1.Open {
		updatedPR, err := r.createPullRequest(ctx, pr, pullRequestProvider)
		if err != nil {
			return ctrl.Result{}, err
		}

		err = r.Status().Update(ctx, updatedPR)
		if err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	if pr.Spec.State == promoterv1alpha1.Merged && pr.Status.State != promoterv1alpha1.Merged {
		logger.Info("Merging Pull Request")

		updatedPR, err := r.mergePullRequest(ctx, pr, pullRequestProvider)
		if err != nil {
			return ctrl.Result{}, err
		}

		err = r.Status().Update(ctx, updatedPR)
		if err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	if pr.Spec.State == promoterv1alpha1.Closed && pr.Status.State != promoterv1alpha1.Closed {
		updatedPR, err := r.closePullRequest(ctx, pr, pullRequestProvider)
		if err != nil {
			return ctrl.Result{}, err
		}

		err = r.Status().Update(ctx, updatedPR)
		if err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	if pr.Status.SpecHash != hash {
		updatedPR, err := r.updatePullRequest(ctx, pr, pullRequestProvider)
		if err != nil {
			return ctrl.Result{}, err
		}
		err = r.Status().Update(ctx, updatedPR)
		if err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	logger.Info("no known states found")
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PullRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.PullRequest{}).
		Complete(r)
}

func (r *PullRequestReconciler) getPullRequestProvider(ctx context.Context, pr promoterv1alpha1.PullRequest) (scms.PullRequestProvider, error) {
	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, *pr.Spec.RepositoryReference, &pr)
	if err != nil {
		return nil, err
	}

	switch {
	case scmProvider.Spec.GitHub != nil:
		return github.NewGithubPullRequestProvider(*secret)
		//return scms.NewScmPullRequestProvider(scms.GitHub, *secret), nil
	case scmProvider.Spec.Fake != nil:
		return fake.NewFakePullRequestProvider(), nil
		//return scms.NewScmPullRequestProvider(scms.Fake, *secret), nil
	default:
		return nil, nil
	}
}

func (r *PullRequestReconciler) handleFinalizer(ctx context.Context, pr *promoterv1alpha1.PullRequest, pullRequestProvider scms.PullRequestProvider) error {
	// name of our custom finalizer
	finalizerName := "pullrequest.promoter.argoporoj.io/finalizer"

	// examine DeletionTimestamp to determine if object is under deletion
	if pr.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// to registering our finalizer.
		if !controllerutil.ContainsFinalizer(pr, finalizerName) {
			controllerutil.AddFinalizer(pr, finalizerName)
			if err := r.Update(ctx, pr); err != nil {
				return err
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(pr, finalizerName) {
			// our finalizer is present, so lets handle any external dependency
			_, err := r.closePullRequest(ctx, *pr, pullRequestProvider)
			if err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried.
				return err
			}

			// remove our finalizer from the list and update it.
			controllerutil.RemoveFinalizer(pr, finalizerName)
			if err := r.Update(ctx, pr); err != nil {
				return err
			}
		}
	}

	return nil
}

func (r *PullRequestReconciler) createPullRequest(ctx context.Context, pr promoterv1alpha1.PullRequest, pullRequestProvider scms.PullRequestProvider) (*promoterv1alpha1.PullRequest, error) {
	logger := log.FromContext(ctx)
	logger.Info("Opening Pull Request")

	updatePR, err := pullRequestProvider.Create(
		ctx,
		pr.Spec.Title,
		pr.Spec.SourceBranch,
		pr.Spec.TargetBranch,
		pr.Spec.Description,
		&pr)
	if err != nil {
		return nil, err
	}

	updatedHash, err := updatePR.Hash()
	if err != nil {
		return nil, err
	}

	updatePR.Status.SpecHash = updatedHash
	updatePR.Status.State = promoterv1alpha1.Open
	return updatePR, nil
}

func (r *PullRequestReconciler) updatePullRequest(ctx context.Context, pr promoterv1alpha1.PullRequest, pullRequestProvider scms.PullRequestProvider) (*promoterv1alpha1.PullRequest, error) {
	logger := log.FromContext(ctx)
	logger.Info("Updating Pull Request")

	updatedPR, err := pullRequestProvider.Update(ctx, pr.Spec.Title, pr.Spec.Description, &pr)
	if err != nil {
		return nil, err
	}

	updatedHash, err := updatedPR.Hash()
	if err != nil {
		return nil, err
	}

	updatedPR.Status.SpecHash = updatedHash

	return updatedPR, nil
}

func (r *PullRequestReconciler) mergePullRequest(ctx context.Context, pr promoterv1alpha1.PullRequest, pullRequestProvider scms.PullRequestProvider) (*promoterv1alpha1.PullRequest, error) {
	logger := log.FromContext(ctx)
	logger.Info("Merging Pull Request")

	updatedPR, err := pullRequestProvider.Merge(ctx, "", &pr)
	if err != nil {
		return nil, err
	}

	updatedHash, err := updatedPR.Hash()
	if err != nil {
		return nil, err
	}

	updatedPR.Status.SpecHash = updatedHash
	updatedPR.Status.State = promoterv1alpha1.Merged

	return updatedPR, nil
}

func (r *PullRequestReconciler) closePullRequest(ctx context.Context, pr promoterv1alpha1.PullRequest, pullRequestProvider scms.PullRequestProvider) (*promoterv1alpha1.PullRequest, error) {
	logger := log.FromContext(ctx)
	logger.Info("Closing Pull Request")

	updatedPR, err := pullRequestProvider.Close(ctx, &pr)
	if err != nil {
		return nil, err
	}

	updatedHash, err := updatedPR.Hash()
	if err != nil {
		return nil, err
	}

	updatedPR.Status.SpecHash = updatedHash
	updatedPR.Status.State = promoterv1alpha1.Closed
	return updatedPR, nil
}
