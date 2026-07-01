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
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// ControllerConfigurationReconciler reconciles a ControllerConfiguration object
// revive:disable:exported // The name starting with "Controller" is fine. That's the kind name.
type ControllerConfigurationReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder

	Shutdown            context.CancelFunc
	StartupInstanceID   *string
	ControllerNamespace string
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=controllerconfigurations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=controllerconfigurations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=controllerconfigurations/finalizers,verbs=update

// Reconcile detects spec.instanceID drift from the startup bootstrap value and shuts down the
// controller so the informer cache partition is rebuilt on restart.
func (r *ControllerConfigurationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ControllerConfiguration")
	startTime := time.Now()

	// Only the shipped configuration in the install namespace drives partition management; ignore
	// any other ControllerConfiguration objects entirely (no status is written for them).
	if req.Namespace != r.ControllerNamespace || req.Name != settings.ControllerConfigurationName {
		return ctrl.Result{}, nil
	}

	var cc promoterv1alpha1.ControllerConfiguration
	// This function applies the resource status via Server-Side Apply at the end of the reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	shutdownForDrift := false
	// Registered first so it runs LAST (after HandleReconciliationResult below has persisted the
	// status). Shutdown cancels the manager context, so triggering it before the status apply would
	// abort that apply with a context-cancelled error.
	defer func() {
		if shutdownForDrift && r.Shutdown != nil {
			r.Shutdown()
		}
	}()
	defer utils.HandleReconciliationResult(ctx, startTime, &cc, r.Client, r.Recorder, constants.ControllerConfigurationControllerFieldOwner, &result, &err, &previousReady)

	if err := r.Get(ctx, req.NamespacedName, &cc); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get ControllerConfiguration: %w", err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	previousReady = utils.RemoveReadyCondition(&cc)

	if settings.InstanceIDsEqual(r.StartupInstanceID, cc.Spec.InstanceID) {
		return ctrl.Result{}, nil
	}

	logger.Info("spec.instanceID changed since startup; shutting down controller to reload informer cache partition",
		"startupInstanceID", r.StartupInstanceID,
		"specInstanceID", cc.Spec.InstanceID,
	)
	shutdownForDrift = true

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
