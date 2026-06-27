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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
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
func (r *DAGCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := logf.FromContext(ctx)
	logger.Info("Reconciling DAGCommitStatus")

	// 1. Fetch the DAGCommitStatus instance.
	var dcs promoterv1alpha1.DAGCommitStatus
	if err := r.Get(ctx, req.NamespacedName, &dcs); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("DAGCommitStatus not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get DAGCommitStatus %q: %w", req.Name, err)
	}

	// 2. Fetch the referenced PromotionStrategy.
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{Namespace: dcs.Namespace, Name: dcs.Spec.PromotionStrategyRef.Name}
	if err := r.Get(ctx, psKey, &ps); err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", dcs.Spec.PromotionStrategyRef.Name, err)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", dcs.Spec.PromotionStrategyRef.Name, err)
	}

	// 3. Evaluate the dependency graph against the PromotionStrategy state.
	if err := r.updateDAGCommitStatus(ctx, &dcs, &ps); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update DAG commit statuses: %w", err)
	}

	// 4. Requeue periodically. The configurable requeue duration (settings.Manager) is wired
	// in alongside the commit status write-back; for now use a fixed interval.
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// updateDAGCommitStatus builds the dependency graph from the spec, validates it (unknown
// references, cycles), and computes which environments are currently eligible for promotion
// given the satisfied set derived from the PromotionStrategy state. For now it logs the
// result; writing the per-environment commit statuses back is added in a follow-up.
func (r *DAGCommitStatusReconciler) updateDAGCommitStatus(ctx context.Context, dcs *promoterv1alpha1.DAGCommitStatus, ps *promoterv1alpha1.PromotionStrategy) error {
	logger := logf.FromContext(ctx)

	graph, err := buildDAG(dcs.Spec.Environments)
	if err != nil {
		return fmt.Errorf("failed to build dependency graph: %w", err)
	}
	if err := graph.validateDAG(); err != nil {
		return fmt.Errorf("invalid dependency graph: %w", err)
	}
	order, err := graph.topologicalSort()
	if err != nil {
		return fmt.Errorf("invalid dependency graph: %w", err)
	}

	// satisfied[branch] is true when the environment is synced and healthy. Deriving this from
	// the PromotionStrategy status is added in the write-back step; for now treat nothing as
	// satisfied so only graph roots come back eligible.
	satisfied := make(map[string]bool, len(ps.Status.Environments))
	eligible := graph.eligibleEnvironments(satisfied)

	logger.Info("Evaluated DAG", "order", order, "eligible", eligible)
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *DAGCommitStatusReconciler) SetupWithManager(_ context.Context, mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.DAGCommitStatus{}).
		Named("dagcommitstatus").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
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

// eligibleEnvironments returns the branches that are ready to be promoted given the set of
// already-satisfied branches: every one of their dependsOn upstreams is satisfied and they
// are not themselves satisfied yet. Roots (no dependsOn) are eligible immediately. The
// result is in spec order. Unlike the build/validate/sort steps, this is evaluated every
// reconcile against live environment state.
func (g *dag) eligibleEnvironments(satisfied map[string]bool) []string {
	eligible := make([]string, 0, len(g.branches))
	for _, branch := range g.branches {
		if satisfied[branch] {
			continue
		}
		ready := true
		for _, upstream := range g.dependsOn[branch] {
			if !satisfied[upstream] {
				ready = false
				break
			}
		}
		if ready {
			eligible = append(eligible, branch)
		}
	}
	return eligible
}
