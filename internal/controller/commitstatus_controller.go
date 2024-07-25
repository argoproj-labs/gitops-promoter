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
	Scheme *runtime.Scheme
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
			logger.Info("CommitStatus not found", "namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}

		logger.Error(err, "failed to get CommitStatus", "namespace", req.Namespace, "name", req.Name)
		return ctrl.Result{}, err
	}

	if cs.Generation == cs.Status.ObservedGeneration {
		logger.V(4).Info("Reconcile not needed", "namespace", req.Namespace, "name", req.Name)
		return ctrl.Result{}, nil
	}

	commitStatusProvider, err := r.getCommitStatusProvider(ctx, cs)
	if err != nil {
		return ctrl.Result{}, err
	}

	commitStatus, err := commitStatusProvider.Set(ctx, &cs)
	if err != nil {
		return ctrl.Result{}, err
	}

	commitStatus.Status.ObservedGeneration = commitStatus.Generation
	err = r.Status().Update(ctx, commitStatus)
	if err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CommitStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.CommitStatus{}).
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
