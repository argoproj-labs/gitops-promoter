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
	"github.com/zachaller/promoter/internal/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/zachaller/promoter/api/v1alpha1"
)

// PromotionStrategyReconciler reconciles a PromotionStrategy object
type PromotionStrategyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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

	for _, environment := range ps.Spec.Environments {
		logger.Info("Branch", "Name", environment.Branch)

		pc := promoterv1alpha1.ProposedCommit{}
		pcName := utils.KubeSafeName(fmt.Sprintf("%s-%s", ps.Name, environment.Branch), 250)
		err := r.Get(ctx, client.ObjectKey{Namespace: ps.Namespace, Name: pcName}, &pc, &client.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				logger.Info("ProposedCommit not found creating", "namespace", ps.Namespace, "name", pcName)

				// The code below sets the ownership for the Release Object
				kind := reflect.TypeOf(promoterv1alpha1.PromotionStrategy{}).Name()
				gvk := promoterv1alpha1.GroupVersion.WithKind(kind)
				controllerRef := metav1.NewControllerRef(&ps, gvk)

				pc = promoterv1alpha1.ProposedCommit{
					ObjectMeta: metav1.ObjectMeta{
						Name:            pcName,
						Namespace:       ps.Namespace,
						OwnerReferences: []metav1.OwnerReference{*controllerRef},
					},
					Spec: promoterv1alpha1.ProposedCommitSpec{
						RepositoryReference: ps.Spec.RepositoryReference,
						ProposedBranch:      fmt.Sprintf("%s-%s", environment.Branch, "next"),
						ActiveBranch:        environment.Branch,
					},
				}

				err = r.Create(ctx, &pc)
				if err != nil {
					return ctrl.Result{}, err
				}
			} else {
				logger.Error(err, "failed to get ProposedCommit", "namespace", ps.Namespace, "name", pcName)
				return ctrl.Result{}, err
			}
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *PromotionStrategyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.PromotionStrategy{}).
		Complete(r)
}
