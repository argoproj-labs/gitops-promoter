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
	"sync"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	"sigs.k8s.io/multicluster-runtime/pkg/multicluster"
)

var _ multicluster.Provider = &ControllerConfigurationReconciler{}

type indexerConfig struct {
	object       client.Object
	extractValue client.IndexerFunc
	field        string
}

// ControllerConfigurationReconciler reconciles a ControllerConfiguration object
// and implements the multicluster.Provider interface to dynamically engage/disengage
// the local cluster based on the WatchLocalApplications configuration.
// revive:disable:exported // The name starting with "Controller" is fine. That's the kind name.
//
//nolint:containedctx // clusterContext represents cluster lifetime, not a request
type ControllerConfigurationReconciler struct {
	Client                        client.Client
	Recorder                      events.EventRecorder
	localManager                  ctrl.Manager
	mcMgr                         mcmanager.Manager
	clusterContext                context.Context
	Scheme                        *runtime.Scheme
	cancelFunc                    context.CancelFunc
	indexers                      []indexerConfig
	lock                          sync.RWMutex
	watchLocalApplicationsEngaged bool
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=controllerconfigurations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=controllerconfigurations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=controllerconfigurations/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which manages
// the local cluster engagement based on the WatchLocalApplications configuration.
func (r *ControllerConfigurationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ControllerConfiguration")
	startTime := time.Now()

	config := &promoterv1alpha1.ControllerConfiguration{}
	// This function will update the resource status at the end of the reconciliation. don't call .Status().Update manually.
	defer utils.HandleReconciliationResult(ctx, startTime, config, r.Client, r.Recorder, &err)

	if err = r.Client.Get(ctx, req.NamespacedName, config); err != nil {
		if apierrors.IsNotFound(err) {
			logger.Info("ControllerConfiguration not found, disengaging local cluster if engaged")
			r.disengageLocalCluster()
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get ControllerConfiguration: %w", err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	meta.RemoveStatusCondition(config.GetConditions(), string(promoterConditions.Ready))

	// Check if we need to engage or disengage the local cluster
	watchLocal := config.Spec.ArgoCDCommitStatus.WatchLocalApplications

	r.lock.RLock()
	currentlyEngaged := r.watchLocalApplicationsEngaged
	r.lock.RUnlock()

	if watchLocal && !currentlyEngaged {
		// Need to engage
		logger.Info("Engaging local cluster for Application watches")
		if err = r.engageLocalCluster(ctx); err != nil {
			logger.Error(err, "Failed to engage local cluster")
			meta.SetStatusCondition(config.GetConditions(), metav1.Condition{
				Type:    string(promoterConditions.Ready),
				Status:  metav1.ConditionFalse,
				Reason:  "LocalClusterEngagementFailed",
				Message: fmt.Sprintf("Failed to engage local cluster: %v", err),
			})
			config.Status.ArgoCDCommitStatus.LocalClusterEngaged = false
			return ctrl.Result{}, err
		}
		logger.Info("Successfully engaged local cluster")
	} else if !watchLocal && currentlyEngaged {
		// Need to disengage
		logger.Info("Disengaging local cluster from Application watches")
		r.disengageLocalCluster()
		logger.Info("Successfully disengaged local cluster")
	}

	// Update status to reflect current state
	r.lock.RLock()
	engaged := r.watchLocalApplicationsEngaged
	r.lock.RUnlock()

	meta.SetStatusCondition(config.GetConditions(), metav1.Condition{
		Type:    string(promoterConditions.Ready),
		Status:  metav1.ConditionTrue,
		Reason:  string(promoterConditions.ReconciliationSuccess),
		Message: "Local cluster engagement configured successfully",
	})
	config.Status.ArgoCDCommitStatus.LocalClusterEngaged = engaged

	return ctrl.Result{}, nil
}

// engageLocalCluster engages the local cluster with the multicluster manager.
func (r *ControllerConfigurationReconciler) engageLocalCluster(ctx context.Context) error {
	r.lock.Lock()
	defer r.lock.Unlock()

	if r.watchLocalApplicationsEngaged {
		return nil // Already engaged
	}

	// Create a context for the cluster that can be cancelled to trigger cleanup
	clusterCtx, cancel := context.WithCancel(ctx)

	// Apply all stored indexers to the local cluster
	for _, idx := range r.indexers {
		if err := r.localManager.GetFieldIndexer().IndexField(clusterCtx, idx.object, idx.field, idx.extractValue); err != nil {
			cancel()
			return fmt.Errorf("failed to apply indexer for field %q: %w", idx.field, err)
		}
	}

	// Engage the local cluster with the multicluster manager
	if err := r.mcMgr.Engage(clusterCtx, mcmanager.LocalCluster, r.localManager); err != nil {
		cancel()
		return fmt.Errorf("failed to engage local cluster: %w", err)
	}

	// Store state
	r.clusterContext = clusterCtx
	r.cancelFunc = cancel
	r.watchLocalApplicationsEngaged = true

	return nil
}

// disengageLocalCluster disengages the local cluster from the multicluster manager.
func (r *ControllerConfigurationReconciler) disengageLocalCluster() {
	r.lock.Lock()
	defer r.lock.Unlock()

	if !r.watchLocalApplicationsEngaged {
		return // Not engaged
	}

	// Cancel the context to trigger cleanup in multicluster-runtime
	if r.cancelFunc != nil {
		r.cancelFunc()
	}

	// Clear state
	r.watchLocalApplicationsEngaged = false
	r.clusterContext = nil
	r.cancelFunc = nil
}

// Get implements multicluster.Provider interface.
// Returns the local cluster if it is currently engaged.
func (r *ControllerConfigurationReconciler) Get(ctx context.Context, clusterName string) (cluster.Cluster, error) {
	if clusterName != mcmanager.LocalCluster {
		return nil, multicluster.ErrClusterNotFound
	}

	r.lock.RLock()
	defer r.lock.RUnlock()

	if !r.watchLocalApplicationsEngaged {
		return nil, multicluster.ErrClusterNotFound
	}

	return r.localManager, nil
}

// IndexField implements multicluster.Provider interface.
// Stores indexers and applies them immediately if the local cluster is engaged.
func (r *ControllerConfigurationReconciler) IndexField(ctx context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
	r.lock.Lock()
	defer r.lock.Unlock()

	// Store for future engagement
	r.indexers = append(r.indexers, indexerConfig{
		object:       obj,
		field:        field,
		extractValue: extractValue,
	})

	// Apply immediately if engaged
	if r.watchLocalApplicationsEngaged {
		if err := r.localManager.GetFieldIndexer().IndexField(ctx, obj, field, extractValue); err != nil {
			return fmt.Errorf("failed to index field %q: %w", field, err)
		}
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager and stores the
// local manager and multicluster manager for provider functionality.
func (r *ControllerConfigurationReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager, mcMgr mcmanager.Manager) error {
	r.localManager = mgr
	r.mcMgr = mcMgr

	err := ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.ControllerConfiguration{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("controllerconfiguration").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}
