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
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// DAGCommitStatusReconciler reconciles a DAGCommitStatus object
type DAGCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=dagcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=dagcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=dagcommitstatuses/finalizers,verbs=update

// Reconcile reads the referenced PromotionStrategy and, using the dependency graph declared
// in the DAGCommitStatus, determines which environments are eligible for promotion (all of
// their dependsOn upstreams are satisfied) and reports that as a per-environment commit
// status. This scaffold wires up graph construction, validation, and eligibility; writing
// the commit statuses back is added in a follow-up.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/reconcile
func (r *DAGCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := logf.FromContext(ctx)
	logger.Info("Reconciling DAGCommitStatus")
	startTime := time.Now()

	var dcs promoterv1alpha1.DAGCommitStatus
	// This applies the resource status via Server-Side Apply at the end of reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &dcs, r.Client, r.Recorder, constants.DAGCommitStatusControllerFieldOwner, &result, &err, &previousReady)

	// 1. Fetch the DAGCommitStatus instance.
	if err = r.Get(ctx, req.NamespacedName, &dcs); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("DAGCommitStatus not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get DAGCommitStatus %q: %w", req.Name, err)
	}

	// Start fresh on the Ready condition each reconcile.
	previousReady = utils.RemoveReadyCondition(&dcs)

	// 2. Fetch the referenced PromotionStrategy.
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{Namespace: dcs.Namespace, Name: dcs.Spec.PromotionStrategyRef.Name}
	if err = r.Get(ctx, psKey, &ps); err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", dcs.Spec.PromotionStrategyRef.Name, err)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", dcs.Spec.PromotionStrategyRef.Name, err)
	}

	// 3. Evaluate the dependency graph against the PromotionStrategy state and write statuses.
	if err = r.updateDAGCommitStatus(ctx, &dcs, &ps); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update DAG commit statuses: %w", err)
	}

	// 4. Requeue using the configured requeue duration.
	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.DAGCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration for DAGCommitStatus %q: %w", dcs.Name, err)
	}

	return ctrl.Result{Requeue: true, RequeueAfter: requeueDuration}, nil
}

// updateDAGCommitStatus builds the dependency graph from the spec, validates it (unknown
// references, cycles), derives which environments are satisfied (synced and healthy) from the
// PromotionStrategy state, and writes a per-environment CommitStatus: success once all of an
// environment's dependsOn upstreams are satisfied, pending otherwise.
func (r *DAGCommitStatusReconciler) updateDAGCommitStatus(ctx context.Context, dcs *promoterv1alpha1.DAGCommitStatus, ps *promoterv1alpha1.PromotionStrategy) error {
	graph, err := buildDAG(dcs.Spec.Environments)
	if err != nil {
		return fmt.Errorf("failed to build dependency graph: %w", err)
	}
	if err := graph.validateDAG(); err != nil {
		return fmt.Errorf("invalid dependency graph: %w", err)
	}
	if _, err := graph.topologicalSort(); err != nil {
		return fmt.Errorf("invalid dependency graph: %w", err)
	}

	// Index PromotionStrategy environment status by branch so each DAG node can look up its own
	// and its upstreams' state in O(1).
	statusByBranch := make(map[string]promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for _, envStatus := range ps.Status.Environments {
		statusByBranch[envStatus.Branch] = envStatus
	}

	// Write a CommitStatus for every environment: success once every one of its dependsOn
	// upstreams has promoted and become healthy for the SAME dry SHA this environment is
	// promoting, pending otherwise.
	logger := logf.FromContext(ctx)
	for _, branch := range graph.branches {
		envStatus := statusByBranch[branch]

		// Skip if there's no proposed change for this environment (active and proposed dry SHAs
		// match): there is no in-flight PR to gate, so creating/updating a commit status would
		// only churn an already-merged change. This mirrors the PreviousEnvironmentCommitStatus
		// controller and, since a proposed change implies the hydrator has produced a SHA, also
		// keeps us from writing a CommitStatus with an empty sha.
		if envStatus.Active.Dry.Sha == envStatus.Proposed.Dry.Sha {
			logger.V(4).Info("Skipping environment with no proposed change", "branch", branch)
			continue
		}

		// The gate is keyed to the dry SHA this environment is promoting. An upstream counts as
		// satisfied only when it has itself promoted and become healthy for that SAME dry SHA —
		// not merely because it is in some healthy state from a previous round. Checking upstreams
		// against the target dry SHA (rather than a target-less "is it healthy") is what prevents a
		// downstream from merging a new change ahead of upstreams that have not yet taken it.
		targetDrySha := getEffectiveHydratedDrySha(envStatus)
		isPending, reason := upstreamsPending(graph, branch, targetDrySha, envStatus.Active.Dry.CommitTime, statusByBranch)

		phase := promoterv1alpha1.CommitPhaseSuccess
		if isPending {
			phase = promoterv1alpha1.CommitPhasePending
		}

		logger.V(4).Info("Evaluated DAG gate for environment",
			"branch", branch,
			"dependsOn", graph.dependsOn[branch],
			"targetDrySha", targetDrySha,
			"pending", isPending,
			"reason", reason,
			"phase", phase)

		// Bind the CommitStatus to the proposed branch's hydrated SHA: that is the commit the
		// ChangeTransferPolicy inspects when gating the promotion PR. Binding to the dry SHA
		// instead leaves the gate undetectable, so the promotion never advances. Mirrors the
		// PreviousEnvironmentCommitStatus controller.
		proposedHydratedSha := envStatus.Proposed.Hydrated.Sha
		if _, err := r.createOrUpdateDAGCommitStatus(ctx, dcs, ps, branch, proposedHydratedSha, phase); err != nil {
			return fmt.Errorf("failed to set DAG commit status for branch %q: %w", branch, err)
		}
	}

	return nil
}

// upstreamsPending reports whether ANY of branch's direct dependsOn upstreams is not yet ready for
// targetDrySha. An upstream is ready when it has hydrated and merged the target dry SHA (with a
// commit time no older than the current environment's) and is healthy. Upstreams for which the
// target dry SHA is a no-op (git note advanced without a new commit) are transparently skipped by
// recursing into their own upstreams. This mirrors the per-environment checks in the
// PreviousEnvironmentCommitStatus controller's isPreviousEnvironmentPending, adapted from a linear
// chain to the DAG's dependsOn edges (all upstreams must be ready for a fan-in to pass).
func upstreamsPending(g *dag, branch, targetDrySha string, currentActiveCommitTime metav1.Time, statusByBranch map[string]promoterv1alpha1.EnvironmentStatus) (isPending bool, reason string) {
	for _, upstream := range g.dependsOn[branch] {
		if pending, r := upstreamPending(g, upstream, targetDrySha, currentActiveCommitTime, statusByBranch); pending {
			return true, r
		}
	}
	return false, ""
}

// upstreamPending checks a single upstream (and, for no-op upstreams, its own upstreams) against
// targetDrySha. The checks are a direct port of isPreviousEnvironmentPending's per-environment
// logic.
func upstreamPending(g *dag, branch, targetDrySha string, currentActiveCommitTime metav1.Time, statusByBranch map[string]promoterv1alpha1.EnvironmentStatus) (isPending bool, reason string) {
	envStatus := statusByBranch[branch]
	envHydratedForDrySha := getEffectiveHydratedDrySha(envStatus)
	envProposedDrySha := envStatus.Proposed.Dry.Sha

	// The upstream's hydrator must have processed the same dry SHA the current environment is
	// promoting.
	if envHydratedForDrySha != targetDrySha {
		return true, "Waiting for the hydrator to finish processing the proposed dry commit"
	}

	// If the upstream has merged the target dry SHA, verify commit-time ordering and health.
	if envStatus.Active.Dry.Sha == targetDrySha {
		envDryShaEqualOrNewer := envStatus.Active.Dry.CommitTime.Equal(&metav1.Time{Time: currentActiveCommitTime.Time}) ||
			envStatus.Active.Dry.CommitTime.After(currentActiveCommitTime.Time)
		if !envDryShaEqualOrNewer {
			// This should basically never happen.
			return true, "Previous environment's commit is older than current environment's commit"
		}
		return checkCommitStatusesPassing(envStatus.Active.CommitStatuses, envStatus.Branch)
	}

	// The upstream has not merged the target. It is only skippable if it is a clean no-op (git note
	// advanced without a new commit) with no pending changes of its own.
	envIsNoOp := envHydratedForDrySha != envProposedDrySha
	envHasPendingChanges := envStatus.Active.Dry.Sha != envProposedDrySha
	if !envIsNoOp || envHasPendingChanges {
		return true, "Waiting for previous environment to be promoted"
	}

	// Even a clean no-op must be healthy before we look past it.
	if isPend, r := checkCommitStatusesPassing(envStatus.Active.CommitStatuses, envStatus.Branch); isPend {
		return isPend, r
	}

	// Clean, healthy no-op: recurse into this upstream's own upstreams.
	return upstreamsPending(g, branch, targetDrySha, currentActiveCommitTime, statusByBranch)
}

// checkCommitStatusesPassing reports whether an environment's active commit statuses are all
// passing, returning a pending reason if not. Ported unchanged from the
// PreviousEnvironmentCommitStatus controller.
func checkCommitStatusesPassing(commitStatuses []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase, branch string) (isPending bool, reason string) {
	if utils.AreCommitStatusesPassing(commitStatuses) {
		return false, ""
	}
	envDesc := fmt.Sprintf("%q environment's", branch)
	if branch == "" {
		envDesc = "previous environment's"
	}
	if len(commitStatuses) == 1 {
		return true, fmt.Sprintf("Waiting for %s %q commit status to be successful", envDesc, commitStatuses[0].Key)
	}
	return true, fmt.Sprintf("Waiting for %s commit statuses to be successful", envDesc)
}

// createOrUpdateDAGCommitStatus upserts, via Server-Side Apply, the CommitStatus that reports
// whether the given environment's DAG dependencies are satisfied.
func (r *DAGCommitStatusReconciler) createOrUpdateDAGCommitStatus(
	ctx context.Context,
	dcs *promoterv1alpha1.DAGCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	branch string,
	hydratedSha string,
	phase promoterv1alpha1.CommitStatusPhase,
) (*promoterv1alpha1.CommitStatus, error) {
	key := dcs.Spec.Key
	if key == "" {
		// Spec.Key is defaulted by the CRD on the API-server write path; fall back here so
		// objects built directly (e.g. in tests) still get the canonical gate key.
		key = promoterv1alpha1.DAGCommitStatusKey
	}
	commitStatusName := utils.CommitStatusResourceName(ctx, dcs, branch)

	kind := reflect.TypeOf(promoterv1alpha1.DAGCommitStatus{}).Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)

	description := branch + " - dependencies satisfied"
	if phase != promoterv1alpha1.CommitPhaseSuccess {
		description = branch + " - waiting for dependencies"
	}

	labels := utils.CommitStatusStandardLabels(dcs, branch, key)

	commitStatusApply := acv1alpha1.CommitStatus(commitStatusName, dcs.Namespace).
		WithLabels(labels).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(dcs.Name).
			WithUID(dcs.UID).
			WithController(true).
			WithBlockOwnerDeletion(true)).
		WithSpec(acv1alpha1.CommitStatusSpec().
			WithRepositoryReference(acv1alpha1.ObjectReference().
				WithName(ps.Spec.RepositoryReference.Name)).
			WithSha(hydratedSha).
			// Use the stable gate key as the SCM commit status context (spec.Name) so users can
			// reference a single predictable name in branch protection rules, regardless of which
			// environment or phase produced the status. The human-readable, per-environment detail
			// goes in the description instead.
			WithName(key).
			WithDescription(description).
			WithPhase(phase))

	commitStatus := &promoterv1alpha1.CommitStatus{}
	commitStatus.Name = commitStatusName
	commitStatus.Namespace = dcs.Namespace
	if err := r.Patch(ctx, commitStatus, utils.ApplyPatch{ApplyConfig: commitStatusApply}, client.FieldOwner(constants.DAGCommitStatusControllerFieldOwner), client.ForceOwnership); err != nil {
		return nil, fmt.Errorf("failed to apply DAG CommitStatus: %w", err)
	}

	return commitStatus, nil
}

// SetupWithManager sets up the controller with the Manager.
//
//nolint:dupl // Controller setup mirrors PreviousEnvironmentCommitStatus by design; extracting it would couple the two controllers and require generics.
func (r *DAGCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.DAGCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get DAGCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.DAGCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get DAGCommitStatus max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.DAGCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.PromotionStrategy{}, r.enqueueDAGCommitStatusForPromotionStrategy()).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Named("dagcommitstatus").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// enqueueDAGCommitStatusForPromotionStrategy returns a handler that enqueues all
// DAGCommitStatus resources that reference a PromotionStrategy when that PromotionStrategy changes.
//
//nolint:dupl // Mirrors PreviousEnvironmentCommitStatus's enqueue handler by design; extracting it would couple the two controllers and require generics.
func (r *DAGCommitStatusReconciler) enqueueDAGCommitStatusForPromotionStrategy() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
		if !ok {
			return nil
		}

		var dcsList promoterv1alpha1.DAGCommitStatusList
		if err := r.List(ctx, &dcsList, client.InNamespace(ps.Namespace)); err != nil {
			logf.FromContext(ctx).Error(err, "failed to list DAGCommitStatus resources")
			return nil
		}

		var requests []ctrl.Request
		for i := range dcsList.Items {
			if dcsList.Items[i].Spec.PromotionStrategyRef.Name == ps.Name {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKeyFromObject(&dcsList.Items[i]),
				})
			}
		}

		return requests
	})
}

// dag is the in-memory dependency graph built from a DAGCommitStatus's environments.
// Nodes are environment branches; "v depends on u" means u must be satisfied before v
// becomes eligible. The graph is keyed by branch name so lookups during reconciliation
// are O(1).
type dag struct {
	// dependsOn maps a branch to the upstream branches it directly depends on.
	dependsOn map[string][]string

	// branches is the set of all environment branches declared in the spec, preserved in
	// spec order so traversal output is deterministic.
	branches []string
}

// buildDAG constructs a dag from a DAGCommitStatus's environments. It rejects a duplicate
// branch (the same branch declared more than once), which would otherwise make the
// dependency relationships ambiguous. Validation that dependsOn references resolve to real
// branches is done separately in validateDAG.
func buildDAG(environments []promoterv1alpha1.DAGEnvironment) (*dag, error) {
	g := &dag{
		branches:  make([]string, 0, len(environments)),
		dependsOn: make(map[string][]string, len(environments)),
	}
	for _, env := range environments {
		if _, exists := g.dependsOn[env.Branch]; exists {
			return nil, fmt.Errorf("duplicate branch %q in environments", env.Branch)
		}
		g.branches = append(g.branches, env.Branch)
		g.dependsOn[env.Branch] = env.DependsOn
	}
	return g, nil
}

// validateDAG checks that every dependsOn entry references a branch declared in the graph.
// A dependsOn pointing at an unknown branch can never be satisfied, so the depending
// environment would silently stall; surfacing it as an error is clearer than letting it
// hang. Cycle detection is handled by topologicalSort.
func (g *dag) validateDAG() error {
	for _, branch := range g.branches {
		for _, upstream := range g.dependsOn[branch] {
			if _, exists := g.dependsOn[upstream]; !exists {
				return fmt.Errorf("branch %q depends on unknown branch %q", branch, upstream)
			}
		}
	}
	return nil
}

// topologicalSort returns the branches in an order where every branch appears after all of
// its dependsOn upstreams, using Kahn's algorithm. It also detects cycles: if the graph
// contains a dependency cycle, those branches can never reach in-degree zero, so fewer than
// len(branches) are emitted and an error is returned. Branches with equal depth are emitted
// in spec order, keeping the result deterministic.
func (g *dag) topologicalSort() ([]string, error) {
	// inDegree[b] = number of upstreams b still depends on. downstream[u] = branches that
	// depend on u (the reverse of dependsOn), needed to relax edges as we emit nodes.
	inDegree := make(map[string]int, len(g.branches))
	downstream := make(map[string][]string, len(g.branches))
	for _, branch := range g.branches {
		inDegree[branch] = len(g.dependsOn[branch])
		for _, upstream := range g.dependsOn[branch] {
			downstream[upstream] = append(downstream[upstream], branch)
		}
	}

	// Seed the queue with roots (no upstreams), walking branches in spec order so the output
	// is deterministic rather than dependent on map iteration order.
	queue := make([]string, 0, len(g.branches))
	for _, branch := range g.branches {
		if inDegree[branch] == 0 {
			queue = append(queue, branch)
		}
	}

	sorted := make([]string, 0, len(g.branches))
	for len(queue) > 0 {
		branch := queue[0]
		queue = queue[1:]
		sorted = append(sorted, branch)
		// Removing branch satisfies one dependency for each downstream; any that reach zero
		// are now roots themselves.
		for _, dependent := range downstream[branch] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	if len(sorted) != len(g.branches) {
		return nil, errors.New("environments contain a dependency cycle")
	}
	return sorted, nil
}
