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

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
)

// ControllerConfigurationReconciler reconciles a ControllerConfiguration object
// revive:disable:exported // The name starting with "Controller" is fine. That's the kind name.
type ControllerConfigurationReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	Shutdown            context.CancelFunc
	StartupInstanceID   *string
	ControllerNamespace string
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=controllerconfigurations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=controllerconfigurations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=controllerconfigurations/finalizers,verbs=update

// Reconcile detects spec.instanceID drift from the startup bootstrap value and shuts down the
// controller so the informer cache partition is rebuilt on restart.
func (r *ControllerConfigurationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if req.Namespace != r.ControllerNamespace || req.Name != settings.ControllerConfigurationName {
		return ctrl.Result{}, nil
	}

	cc := &promoterv1alpha1.ControllerConfiguration{}
	if err := r.Get(ctx, req.NamespacedName, cc); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get ControllerConfiguration: %w", err)
	}

	if settings.InstanceIDsEqual(r.StartupInstanceID, cc.Spec.InstanceID) {
		return ctrl.Result{}, nil
	}

	logger.Info("spec.instanceID changed since startup; shutting down controller to reload informer cache partition",
		"startupInstanceID", r.StartupInstanceID,
		"specInstanceID", cc.Spec.InstanceID,
	)
	if r.Shutdown != nil {
		r.Shutdown()
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ControllerConfigurationReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.ControllerConfiguration{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("controllerconfiguration").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}
