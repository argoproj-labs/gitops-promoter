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
	"k8s.io/client-go/util/retry"

	"k8s.io/client-go/tools/record"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"

	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// CommitStatusReconciler reconciles a CommitStatus object
type CommitStatusReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the CommitStatus object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *CommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var cs promoterv1alpha1.CommitStatus
	err := r.Get(ctx, req.NamespacedName, &cs, &client.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("CommitStatus not found")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "failed to get CommitStatus")
		return ctrl.Result{}, err
	}

	// We use observed generation pattern here to avoid provider API calls.
	if cs.Status.ObservedGeneration == cs.Generation {
		logger.Info("No need to reconcile")
		return ctrl.Result{}, nil
	}

	if cs.Spec.Sha == "" || cs.Spec.Phase == "" {
		logger.Info("Skip setting commit status, missing sha or phase")
		return ctrl.Result{}, nil
	}

	commitStatusProvider, err := r.getCommitStatusProvider(ctx, cs)
	if err != nil {
		return ctrl.Result{}, err
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		newCs := promoterv1alpha1.CommitStatus{}
		err := r.Client.Get(ctx, req.NamespacedName, &newCs, &client.GetOptions{})
		if err != nil {
			return err
		}

		_, err = commitStatusProvider.Set(ctx, &cs)
		if err != nil {
			return err
		}

		newCs.Status.ObservedGeneration = newCs.Generation
		err = r.Status().Update(ctx, &cs)
		if err != nil {
			if errors.IsConflict(err) {
				logger.Info("Conflict while updating CommitStatus status. Retrying")
			}
			return err
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	r.Recorder.Eventf(&cs, "Normal", "CommitStatusSet", "Commit status %s set to %s for hash %s", cs.Name, cs.Spec.Phase, cs.Spec.Sha)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CommitStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.CommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

func (r *CommitStatusReconciler) getCommitStatusProvider(ctx context.Context, commitStatus promoterv1alpha1.CommitStatus) (scms.CommitStatusProvider, error) {

	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, *commitStatus.Spec.RepositoryReference, &commitStatus)
	if err != nil {
		return nil, err
	}

	switch {
	case scmProvider.Spec.GitHub != nil:
		return github.NewGithubCommitStatusProvider(*secret)
	case scmProvider.Spec.Fake != nil:
		return fake.NewFakeCommitStatusProvider(*secret)
	default:
		return nil, nil
	}
}
