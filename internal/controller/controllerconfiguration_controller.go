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
	"errors"
	"fmt"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// ControllerConfigurationReconciler reconciles a ControllerConfiguration object
// revive:disable:exported // The name starting with "Controller" is fine. That's the kind name.
type ControllerConfigurationReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder

	Shutdown          context.CancelFunc
	StartupInstanceID *string

	// LocalApplications is engaged or disengaged to follow spec.argocdCommitStatus.watchLocalApplications.
	// Nil when the ArgoCDCommitStatus controller is not running, in which case the setting is ignored.
	LocalApplications *LocalApplicationsProvider

	ControllerNamespace string
}

// localApplicationsCRDRetryInterval is how often to retry engaging local Applications while
// watchLocalApplications is true but the Application CRD is not installed.
const localApplicationsCRDRetryInterval = time.Minute

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=controllerconfigurations,resourceNames=promoter-controller-configuration,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=controllerconfigurations/status,resourceNames=promoter-controller-configuration,verbs=get;update;patch

// Reconcile applies spec.metrics, engages or disengages local Argo CD Application watches to follow
// spec.argocdCommitStatus.watchLocalApplications, and detects spec.instanceID drift from the startup bootstrap
// value and shuts down the controller so the informer cache partition is rebuilt on restart.
func (r *ControllerConfigurationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ControllerConfiguration")
	startTime := time.Now()

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
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("get ControllerConfiguration: %w", err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	previousReady = utils.RemoveReadyCondition(&cc)

	metrics.SetGitRepositoryLabels(cc.Spec.Metrics.Labels.GitRepository)

	result, err = r.reconcileLocalApplications(ctx, &cc)
	if err != nil {
		return ctrl.Result{}, err
	}

	if ptr.Equal(r.StartupInstanceID, cc.Spec.InstanceID) {
		return result, nil
	}

	logger.Info("spec.instanceID changed since startup; shutting down controller to reload informer cache partition",
		"startupInstanceID", r.StartupInstanceID,
		"specInstanceID", cc.Spec.InstanceID,
	)
	shutdownForDrift = true

	return result, nil
}

// reconcileLocalApplications engages or disengages the local cluster's Argo CD Applications to match
// spec.argocdCommitStatus.watchLocalApplications and records the outcome in status.
func (r *ControllerConfigurationReconciler) reconcileLocalApplications(ctx context.Context, cc *promoterv1alpha1.ControllerConfiguration) (ctrl.Result, error) {
	if r.LocalApplications == nil {
		return ctrl.Result{}, nil
	}

	var result ctrl.Result
	if cc.Spec.ArgoCDCommitStatus.WatchLocalApplications {
		err := r.LocalApplications.Engage(ctx)
		switch {
		case err == nil:
		case errors.Is(err, ErrApplicationCRDNotInstalled):
			// Not a reconcile error: the setting is valid, the cluster just isn't ready for it yet. Keep
			// retrying so installing Argo CD later starts the watch without a restart.
			meta.SetStatusCondition(cc.GetConditions(), metav1.Condition{
				Type:               string(promoterConditions.Ready),
				Status:             metav1.ConditionFalse,
				Reason:             string(promoterConditions.ArgoCDApplicationCRDNotInstalled),
				Message:            "spec.argocdCommitStatus.watchLocalApplications is true, but the Argo CD Application CRD is not installed on the local cluster",
				ObservedGeneration: cc.Generation,
			})
			result = ctrl.Result{RequeueAfter: localApplicationsCRDRetryInterval}
		default:
			return ctrl.Result{}, fmt.Errorf("failed to watch local Argo CD Applications: %w", err)
		}
	} else {
		r.LocalApplications.Disengage()
	}

	cc.Status.ArgoCDCommitStatus = &promoterv1alpha1.ArgoCDCommitStatusControllerStatus{
		LocalClusterEngaged: r.LocalApplications.Engaged(),
	}
	return result, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ControllerConfigurationReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.ControllerConfiguration{}, builder.WithPredicates(predicate.And(
			predicate.GenerationChangedPredicate{},
			shippedControllerConfigurationPredicate(r.ControllerNamespace),
		))).
		Named("controllerconfiguration").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

// shippedControllerConfigurationPredicate admits only the ControllerConfiguration the controller reads its settings
// from. Reconciles change process-wide state (local Application watches, shutdown on instanceID drift), so no other
// ControllerConfiguration may drive them.
func shippedControllerConfigurationPredicate(namespace string) predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetNamespace() == namespace && obj.GetName() == settings.ControllerConfigurationName
	})
}
