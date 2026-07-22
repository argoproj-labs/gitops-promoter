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
	"reflect"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	acmetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// PreviousEnvironmentCommitStatusReconciler reconciles a PreviousEnvironmentCommitStatus object
type PreviousEnvironmentCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=previousenvironmentcommitstatuses,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=previousenvironmentcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=previousenvironmentcommitstatuses/finalizers,verbs=update
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=dagcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch

// Reconcile reconciles a PreviousEnvironmentCommitStatus: it reads the referenced PromotionStrategy
// and upserts a chain-shaped DAGCommitStatus that performs the previous-environment gating.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/reconcile
func (r *PreviousEnvironmentCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := logf.FromContext(ctx)
	logger.Info("Reconciling PreviousEnvironmentCommitStatus")
	startTime := time.Now()

	var pecs promoterv1alpha1.PreviousEnvironmentCommitStatus
	// This function applies the resource status via Server-Side Apply at the end of the reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &pecs, r.Client, r.Recorder, constants.PreviousEnvironmentCommitStatusControllerFieldOwner, &result, &err, &previousReady)

	// 1. Fetch the PreviousEnvironmentCommitStatus instance
	err = r.Get(ctx, req.NamespacedName, &pecs, &client.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("PreviousEnvironmentCommitStatus not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get PreviousEnvironmentCommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get PreviousEnvironmentCommitStatus %q: %w", req.Name, err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	previousReady = utils.RemoveReadyCondition(&pecs)

	// 2. Fetch the referenced PromotionStrategy
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{
		Namespace: pecs.Namespace,
		Name:      pecs.Spec.PromotionStrategyRef.Name,
	}
	err = r.Get(ctx, psKey, &ps)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Error(err, "referenced PromotionStrategy not found", "promotionStrategy", pecs.Spec.PromotionStrategyRef.Name)
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", pecs.Spec.PromotionStrategyRef.Name, err)
		}
		logger.Error(err, "failed to get PromotionStrategy")
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", pecs.Spec.PromotionStrategyRef.Name, err)
	}

	// 3. Generate the chain-shaped DAGCommitStatus that does the actual gating work.
	dag, err := r.upsertDAGCommitStatus(ctx, &pecs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to upsert DAG commit status: %w", err)
	}
	utils.InheritNotReadyConditionFromObjects(&pecs, promoterConditions.DAGCommitStatusNotReady, dag)

	// 4. Requeue using the configured requeue duration.
	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.PreviousEnvironmentCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration for PreviousEnvironmentCommitStatus %q: %w", pecs.Name, err)
	}

	return ctrl.Result{
		Requeue:      true,
		RequeueAfter: requeueDuration,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
//
//nolint:dupl // Controller setup mirrors DAGCommitStatus by design; extracting it would couple the two controllers and require generics.
func (r *PreviousEnvironmentCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.PreviousEnvironmentCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get PreviousEnvironmentCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.PreviousEnvironmentCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get PreviousEnvironmentCommitStatus max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.PreviousEnvironmentCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&promoterv1alpha1.DAGCommitStatus{}).
		Watches(&promoterv1alpha1.PromotionStrategy{}, r.enqueuePreviousEnvironmentCommitStatusForPromotionStrategy()).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Named("previousenvironmentcommitstatus").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// enqueuePreviousEnvironmentCommitStatusForPromotionStrategy returns a handler that enqueues all
// PreviousEnvironmentCommitStatus resources that reference a PromotionStrategy when that
// PromotionStrategy changes.
//
//nolint:dupl // Mirrors DAGCommitStatus's enqueue handler by design; extracting it would couple the two controllers and require generics.
func (r *PreviousEnvironmentCommitStatusReconciler) enqueuePreviousEnvironmentCommitStatusForPromotionStrategy() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
		if !ok {
			return nil
		}

		var pecsList promoterv1alpha1.PreviousEnvironmentCommitStatusList
		if err := r.List(ctx, &pecsList, client.InNamespace(ps.Namespace)); err != nil {
			logf.FromContext(ctx).Error(err, "failed to list PreviousEnvironmentCommitStatus resources")
			return nil
		}

		var requests []ctrl.Request
		for i := range pecsList.Items {
			if pecsList.Items[i].Spec.PromotionStrategyRef.Name == ps.Name {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKeyFromObject(&pecsList.Items[i]),
				})
			}
		}

		return requests
	})
}

// upsertDAGCommitStatus translates the PreviousEnvironmentCommitStatus into a chain-shaped
// DAGCommitStatus (each environment depends on the one before it) and applies it via Server-Side
// Apply. The DAGCommitStatus controller does the actual gating; this controller only builds the
// chain. The generated object is owned by the PreviousEnvironmentCommitStatus and reuses its
// commit status key.
func (r *PreviousEnvironmentCommitStatusReconciler) upsertDAGCommitStatus(ctx context.Context, pecs *promoterv1alpha1.PreviousEnvironmentCommitStatus, ps *promoterv1alpha1.PromotionStrategy) (*promoterv1alpha1.DAGCommitStatus, error) {
	key := pecs.Spec.Key
	if key == "" {
		// Spec.Key is defaulted by the CRD on the API-server write path; fall back here so objects
		// built directly (e.g. in tests) still get the canonical gate key.
		key = promoterv1alpha1.PreviousEnvironmentCommitStatusKey
	}

	// Build the dependsOn chain: each environment depends on the one before it in spec order; the
	// first environment is a root with no dependency.
	environments := make([]*acv1alpha1.DAGEnvironmentApplyConfiguration, 0, len(ps.Spec.Environments))
	for i, env := range ps.Spec.Environments {
		dagEnv := acv1alpha1.DAGEnvironment().WithBranch(env.Branch)
		if i > 0 {
			dagEnv = dagEnv.WithDependsOn(ps.Spec.Environments[i-1].Branch)
		}
		environments = append(environments, dagEnv)
	}

	kind := reflect.TypeOf(promoterv1alpha1.PreviousEnvironmentCommitStatus{}).Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)

	dagSpec := acv1alpha1.DAGCommitStatusSpec().
		WithPromotionStrategyRef(acv1alpha1.ObjectReference().WithName(ps.Name)).
		WithKey(key).
		WithEnvironments(environments...)

	// Pass URL config through to the owned DAG; the DAG controller renders it onto child CommitStatuses.
	if pecs.Spec.URL.Template != "" {
		urlConfig := acv1alpha1.URLConfig().WithTemplate(pecs.Spec.URL.Template)
		if len(pecs.Spec.URL.Options) > 0 {
			urlConfig = urlConfig.WithOptions(pecs.Spec.URL.Options...)
		}
		dagSpec = dagSpec.WithURL(urlConfig)
	}

	dagApply := acv1alpha1.DAGCommitStatus(pecs.Name, pecs.Namespace).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(pecs.Name).
			WithUID(pecs.UID).
			WithController(true).
			WithBlockOwnerDeletion(true)).
		WithSpec(dagSpec)

	dag := &promoterv1alpha1.DAGCommitStatus{}
	dag.Name = pecs.Name
	dag.Namespace = pecs.Namespace
	if err := r.Patch(ctx, dag, utils.ApplyPatch{ApplyConfig: dagApply}, client.FieldOwner(constants.PreviousEnvironmentCommitStatusControllerFieldOwner), client.ForceOwnership); err != nil {
		return nil, fmt.Errorf("failed to apply DAGCommitStatus %q: %w", pecs.Name, err)
	}

	return dag, nil
}

func getNoteDrySha(note *promoterv1alpha1.HydratorMetadata) string {
	if note == nil {
		return ""
	}
	return note.DrySha
}

// getEffectiveHydratedDrySha returns the dry SHA that an environment's hydrator has processed.
// Uses Note.DrySha if available (git note), otherwise falls back to Proposed.Dry.Sha (hydrator.metadata).
func getEffectiveHydratedDrySha(envStatus promoterv1alpha1.EnvironmentStatus) string {
	noteSha := getNoteDrySha(envStatus.Proposed.Note)
	if noteSha != "" {
		return noteSha
	}
	return envStatus.Proposed.Dry.Sha
}
