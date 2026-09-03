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
	"slices"
	"strings"
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
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/gitauth"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// DryShaValidationURLTemplateData is the data passed to DryShaValidationCommitStatus.spec.url.template.
type DryShaValidationURLTemplateData struct {
	DryShaValidationCommitStatus promoterv1alpha1.DryShaValidationCommitStatus
	PromotionStrategy            *promoterv1alpha1.PromotionStrategy
	Environment                  string
	// DependsOnQuery is DependsOn encoded as repeated env= query parameters
	// (e.g. "env=e2e&env=perf"), ready to append after "?". Empty when DependsOn is empty.
	DependsOnQuery string
	// DependsOn is the current environment's immediate upstream branches (one edge away).
	DependsOn []string
}

// DryShaValidationCommitStatusReconciler reconciles a DryShaValidationCommitStatus object
type DryShaValidationCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=dryshavalidationcommitstatuses,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=dryshavalidationcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=dryshavalidationcommitstatuses/finalizers,verbs=update
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=clusterscmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile reads the referenced PromotionStrategy and, for each environment, reports whether the
// dry commit it is promoting has already been promoted and observed healthy in a lower environment.
//
// Unlike the previous-environment and DAG gates, which ask whether an upstream is running the target
// dry commit *right now*, this gate asks whether it ever did. That distinction is the point: when the
// dry branch moves faster than promotions complete, upstreams race ahead of the commit a downstream
// environment is trying to promote, and an equality check against their current state never lines up.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/reconcile
//
//nolint:dupl // The fetch/validate/requeue lifecycle mirrors DAGCommitStatus by design; extracting it would couple the two controllers and require generics.
func (r *DryShaValidationCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := logf.FromContext(ctx)
	logger.Info("Reconciling DryShaValidationCommitStatus")
	startTime := time.Now()

	var dsvcs promoterv1alpha1.DryShaValidationCommitStatus
	// This applies the resource status via Server-Side Apply at the end of reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &dsvcs, r.Client, r.Recorder, constants.DryShaValidationCommitStatusControllerFieldOwner, &result, &err, &previousReady)

	// 1. Fetch the DryShaValidationCommitStatus instance.
	if err = r.Get(ctx, req.NamespacedName, &dsvcs); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("DryShaValidationCommitStatus not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get DryShaValidationCommitStatus %q: %w", req.Name, err)
	}

	// Start fresh on the Ready condition each reconcile.
	previousReady = utils.RemoveReadyCondition(&dsvcs)

	// 2. Fetch the referenced PromotionStrategy.
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{Namespace: dsvcs.Namespace, Name: dsvcs.Spec.PromotionStrategyRef.Name}
	if err = r.Get(ctx, psKey, &ps); err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", dsvcs.Spec.PromotionStrategyRef.Name, err)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", dsvcs.Spec.PromotionStrategyRef.Name, err)
	}

	// 3. Evaluate each environment against its upstreams' promotion history and write statuses.
	if err = r.updateDryShaValidationCommitStatus(ctx, &dsvcs, &ps); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update dry sha validation commit statuses: %w", err)
	}

	// 4. Requeue using the configured requeue duration.
	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.DryShaValidationCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration for DryShaValidationCommitStatus %q: %w", dsvcs.Name, err)
	}

	return ctrl.Result{Requeue: true, RequeueAfter: requeueDuration}, nil
}

// updateDryShaValidationCommitStatus builds and validates the dependency graph, then writes a
// per-environment CommitStatus: success once the environment's target dry commit is found in the
// promotion history of any upstream environment, pending otherwise.
func (r *DryShaValidationCommitStatusReconciler) updateDryShaValidationCommitStatus(ctx context.Context, dsvcs *promoterv1alpha1.DryShaValidationCommitStatus, ps *promoterv1alpha1.PromotionStrategy) error {
	logger := logf.FromContext(ctx)

	graph, err := buildDryShaGraph(dryShaEnvironments(dsvcs, ps))
	if err != nil {
		return fmt.Errorf("failed to build dependency graph: %w", err)
	}
	if err := graph.validate(); err != nil {
		return fmt.Errorf("invalid dependency graph: %w", err)
	}
	if err := graph.validateEnvironmentsMatchPS(dsvcs.Name, ps); err != nil {
		return err
	}

	// Index PromotionStrategy environment status and spec by branch so each node can look up its
	// own and its upstreams' state in O(1).
	statusByBranch := make(map[string]promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for _, envStatus := range ps.Status.Environments {
		statusByBranch[envStatus.Branch] = envStatus
	}

	var ledgers dryShaLedgerSource = newDryShaLedgerCache(r.Client, r.SettingsMgr, dsvcs, ps, statusByBranch)

	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(graph.branches))
	envStatuses := make([]promoterv1alpha1.DryShaValidationEnvironmentStatus, 0, len(graph.branches))

	// The status as last persisted, so a skipped environment can carry its previous evaluation
	// forward instead of losing it.
	previousEnvStatuses := make(map[string]promoterv1alpha1.DryShaValidationEnvironmentStatus, len(dsvcs.Status.Environments))
	for _, previous := range dsvcs.Status.Environments {
		previousEnvStatuses[previous.Branch] = previous
	}

	for _, branch := range graph.branches {
		envStatus := statusByBranch[branch]

		// Skip when there is no proposed change (active and proposed dry commits match): there is
		// no in-flight pull request to gate, so updating a CommitStatus would only cause
		// unnecessary updates. This also avoids writing a CommitStatus with an empty proposed
		// hydrated SHA.
		//
		// Keep any existing CommitStatus in the valid set so orphan cleanup leaves the last
		// evaluated (stale-but-real) gate status alone until a new proposed change appears.
		if envStatus.Active.Dry.Sha == envStatus.Proposed.Dry.Sha {
			logger.V(4).Info("Skipping environment with no proposed change", "branch", branch)
			existing, err := r.existingCommitStatus(ctx, dsvcs, branch)
			if err != nil {
				return err
			}
			if existing != nil {
				commitStatuses = append(commitStatuses, existing)
			}
			if previous, ok := previousEnvStatuses[branch]; ok {
				envStatuses = append(envStatuses, previous)
			}
			continue
		}

		evaluation, err := r.evaluateEnvironment(ctx, graph, ledgers, statusByBranch, branch)
		if err != nil {
			return fmt.Errorf("failed to evaluate dry sha validation for branch %q: %w", branch, err)
		}

		logger.V(4).Info("Evaluated dry sha validation gate for environment",
			"branch", branch,
			"dependsOn", graph.dependsOn[branch],
			"targetDrySha", evaluation.targetDrySha,
			"validatedIn", evaluation.validatedIn,
			"phase", evaluation.phase,
			"reason", evaluation.description)

		// Bind the CommitStatus to the proposed branch's hydrated SHA: that is the commit the
		// ChangeTransferPolicy inspects when gating the promotion pull request. Binding to the dry
		// SHA instead leaves the gate undetectable, so the promotion never advances.
		cs, err := r.createOrUpdateDryShaValidationCommitStatus(ctx, dsvcs, ps, branch, envStatus.Proposed.Hydrated.Sha, evaluation)
		if err != nil {
			return fmt.Errorf("failed to set dry sha validation commit status for branch %q: %w", branch, err)
		}
		commitStatuses = append(commitStatuses, cs)
		envStatuses = append(envStatuses, evaluation.toEnvironmentStatus(branch))
	}

	dsvcs.Status.Environments = envStatuses

	if err := utils.CleanupOrphanedCommitStatuses(ctx, r.Client, r.Recorder, dsvcs, commitStatuses); err != nil {
		return fmt.Errorf("failed to cleanup orphaned CommitStatus resources: %w", err)
	}

	utils.InheritNotReadyConditionFromObjects(dsvcs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	return nil
}

// existingCommitStatus returns the gate CommitStatus already written for an environment, or nil
// when none has been written yet.
func (r *DryShaValidationCommitStatusReconciler) existingCommitStatus(ctx context.Context, dsvcs *promoterv1alpha1.DryShaValidationCommitStatus, branch string) (*promoterv1alpha1.CommitStatus, error) {
	existing := &promoterv1alpha1.CommitStatus{}
	name := utils.CommitStatusResourceName(ctx, dsvcs, branch)
	if err := r.Get(ctx, client.ObjectKey{Namespace: dsvcs.Namespace, Name: name}, existing); err != nil {
		if k8serrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get existing dry sha validation CommitStatus for branch %q: %w", branch, err)
	}
	return existing, nil
}

// dryShaEnvironments returns the graph nodes to evaluate. When the spec declares none, the
// PromotionStrategy's environment order is compiled into a chain, so the common linear case needs
// no topology configuration at all.
func dryShaEnvironments(dsvcs *promoterv1alpha1.DryShaValidationCommitStatus, ps *promoterv1alpha1.PromotionStrategy) []promoterv1alpha1.DryShaValidationEnvironment {
	if len(dsvcs.Spec.Environments) > 0 {
		return dsvcs.Spec.Environments
	}

	environments := make([]promoterv1alpha1.DryShaValidationEnvironment, 0, len(ps.Spec.Environments))
	for i, env := range ps.Spec.Environments {
		node := promoterv1alpha1.DryShaValidationEnvironment{Branch: env.Branch}
		if i > 0 {
			node.DependsOn = []string{ps.Spec.Environments[i-1].Branch}
		}
		environments = append(environments, node)
	}
	return environments
}

// dryShaEvaluation is the outcome of evaluating one environment's gate.
type dryShaEvaluation struct {
	validatedAt    *metav1.Time
	targetDrySha   string
	phase          promoterv1alpha1.CommitStatusPhase
	description    string
	validatedIn    string
	commitsScanned int
}

func (e dryShaEvaluation) toEnvironmentStatus(branch string) promoterv1alpha1.DryShaValidationEnvironmentStatus {
	return promoterv1alpha1.DryShaValidationEnvironmentStatus{
		Branch:             branch,
		TargetDrySha:       e.targetDrySha,
		Phase:              e.phase,
		ValidatedIn:        e.validatedIn,
		ValidatedAt:        e.validatedAt,
		CommitsScanned:     int32(e.commitsScanned), //nolint:gosec // bounded by spec.lookbackLimit (max 100).
		LastEvaluationTime: metav1.NewTime(time.Now()),
	}
}

// evaluateEnvironment decides whether one environment's target dry commit has already been validated
// somewhere below it. Upstreams are consulted transitively and in graph order, so a fan-in
// environment is satisfied by whichever of its ancestors ran the commit first.
func (r *DryShaValidationCommitStatusReconciler) evaluateEnvironment(
	ctx context.Context,
	graph *dryShaGraph,
	ledgers dryShaLedgerSource,
	statusByBranch map[string]promoterv1alpha1.EnvironmentStatus,
	branch string,
) (dryShaEvaluation, error) {
	upstreams := graph.upstreamClosure(branch)
	if len(upstreams) == 0 {
		return dryShaEvaluation{
			phase:       promoterv1alpha1.CommitPhaseSuccess,
			description: branch + " - no lower environments to validate against",
		}, nil
	}

	// The dry commit this environment is promoting, preferring the hydrator note over the proposed
	// dry SHA: the note is what the hydrator actually processed for this hydrated commit.
	targetDrySha := getEffectiveHydratedDrySha(statusByBranch[branch])
	if targetDrySha == "" {
		return dryShaEvaluation{
			phase:       promoterv1alpha1.CommitPhasePending,
			description: "Waiting for the hydrator to finish processing the proposed dry commit",
		}, nil
	}

	scanned := 0
	for _, upstream := range upstreams {
		ledger, err := ledgers.get(ctx, upstream)
		if err != nil {
			return dryShaEvaluation{}, err
		}
		scanned = max(scanned, ledger.CommitsScanned)

		record, ok := ledger.Validated[targetDrySha]
		if !ok {
			continue
		}

		validatedAt, err := ledgers.mergeCommitTime(ctx, record)
		if err != nil {
			return dryShaEvaluation{}, err
		}

		return dryShaEvaluation{
			targetDrySha:   targetDrySha,
			phase:          promoterv1alpha1.CommitPhaseSuccess,
			description:    fmt.Sprintf("Dry commit %s was promoted and healthy in %q", shortSha(targetDrySha), upstream),
			validatedIn:    upstream,
			validatedAt:    validatedAt,
			commitsScanned: ledger.CommitsScanned,
		}, nil
	}

	return dryShaEvaluation{
		targetDrySha:   targetDrySha,
		phase:          promoterv1alpha1.CommitPhasePending,
		description:    fmt.Sprintf("Dry commit %s has not been promoted and healthy in any lower environment (%s)", shortSha(targetDrySha), strings.Join(upstreams, ", ")),
		commitsScanned: scanned,
	}, nil
}

// shortSha abbreviates a commit SHA for human-facing descriptions, mirroring CommitBranchState.DryShaShort.
func shortSha(sha string) string {
	if len(sha) < 7 {
		return sha
	}
	return sha[:7]
}

// createOrUpdateDryShaValidationCommitStatus upserts, via Server-Side Apply, the CommitStatus that
// reports whether the environment's dry commit has already been validated below it.
func (r *DryShaValidationCommitStatusReconciler) createOrUpdateDryShaValidationCommitStatus(
	ctx context.Context,
	dsvcs *promoterv1alpha1.DryShaValidationCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	branch string,
	hydratedSha string,
	evaluation dryShaEvaluation,
) (*promoterv1alpha1.CommitStatus, error) {
	key := dsvcs.Spec.Key
	commitStatusName := utils.CommitStatusResourceName(ctx, dsvcs, branch)

	kind := reflect.TypeOf(promoterv1alpha1.DryShaValidationCommitStatus{}).Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)

	labels := utils.CommitStatusStandardLabels(dsvcs, branch, key)

	// Use the stable gate key as the SCM commit status context (spec.Name) so users can reference a
	// single predictable name in branch protection rules, regardless of which environment or phase
	// produced the status. The human-readable, per-environment detail goes in the description.
	commitStatusSpec := acv1alpha1.CommitStatusSpec().
		WithRepositoryReference(acv1alpha1.ObjectReference().
			WithName(ps.Spec.RepositoryReference.Name)).
		WithSha(hydratedSha).
		WithName(key).
		WithDescription(evaluation.description).
		WithPhase(evaluation.phase)

	// Render URL from template if configured; when empty, leave CommitStatus.spec.url unset.
	if dsvcs.Spec.URL.Template != "" {
		dependsOn := dryShaDependsOnForBranch(dsvcs, ps, branch)
		data := DryShaValidationURLTemplateData{
			Environment:                  branch,
			DryShaValidationCommitStatus: *dsvcs,
			PromotionStrategy:            ps,
			DependsOn:                    dependsOn,
			DependsOnQuery:               buildDependsOnQuery(dependsOn),
		}
		renderedURL, err := utils.RenderStringTemplate(dsvcs.Spec.URL.Template, data, dsvcs.Spec.URL.Options...)
		if err != nil {
			return nil, fmt.Errorf("failed to render URL template: %w", err)
		}
		if err := utils.ValidateHTTPURL(renderedURL); err != nil {
			return nil, fmt.Errorf("invalid rendered URL for branch %q: %w", branch, err)
		}
		logf.FromContext(ctx).V(4).Info("Rendered URL template",
			"url", renderedURL,
			"environment", branch,
			"commitStatus", commitStatusName,
			"namespace", dsvcs.Namespace)
		commitStatusSpec = commitStatusSpec.WithUrl(renderedURL)
	}

	commitStatusApply := acv1alpha1.CommitStatus(commitStatusName, dsvcs.Namespace).
		WithLabels(labels).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(dsvcs.Name).
			WithUID(dsvcs.UID).
			WithController(true).
			WithBlockOwnerDeletion(true)).
		WithSpec(commitStatusSpec)

	commitStatus := &promoterv1alpha1.CommitStatus{}
	commitStatus.Name = commitStatusName
	commitStatus.Namespace = dsvcs.Namespace
	if err := r.Patch(ctx, commitStatus, utils.ApplyPatch{ApplyConfig: commitStatusApply}, client.FieldOwner(constants.DryShaValidationCommitStatusControllerFieldOwner), client.ForceOwnership); err != nil {
		return nil, fmt.Errorf("failed to apply dry sha validation CommitStatus: %w", err)
	}

	return commitStatus, nil
}

// dryShaDependsOnForBranch returns the direct upstreams of branch as the gate evaluates them,
// including the chain synthesized when the spec declares no environments.
func dryShaDependsOnForBranch(dsvcs *promoterv1alpha1.DryShaValidationCommitStatus, ps *promoterv1alpha1.PromotionStrategy, branch string) []string {
	for _, env := range dryShaEnvironments(dsvcs, ps) {
		if env.Branch == branch {
			return env.DependsOn
		}
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
//
//nolint:dupl // Controller setup mirrors DAGCommitStatus by design; extracting it would couple the two controllers and require generics.
func (r *DryShaValidationCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.DryShaValidationCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get DryShaValidationCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.DryShaValidationCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get DryShaValidationCommitStatus max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.DryShaValidationCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.PromotionStrategy{}, r.enqueueDryShaValidationCommitStatusForPromotionStrategy()).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Named("dryshavalidationcommitstatus").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// enqueueDryShaValidationCommitStatusForPromotionStrategy returns a handler that enqueues all
// DryShaValidationCommitStatus resources that reference a PromotionStrategy when it changes.
//
//nolint:dupl // Mirrors DAGCommitStatus's enqueue handler by design; extracting it would couple the two controllers and require generics.
func (r *DryShaValidationCommitStatusReconciler) enqueueDryShaValidationCommitStatusForPromotionStrategy() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
		if !ok {
			return nil
		}

		var dsvcsList promoterv1alpha1.DryShaValidationCommitStatusList
		if err := r.List(ctx, &dsvcsList, client.InNamespace(ps.Namespace)); err != nil {
			logf.FromContext(ctx).Error(err, "failed to list DryShaValidationCommitStatus resources")
			return nil
		}

		var requests []ctrl.Request
		for i := range dsvcsList.Items {
			if dsvcsList.Items[i].Spec.PromotionStrategyRef.Name == ps.Name {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKeyFromObject(&dsvcsList.Items[i]),
				})
			}
		}

		return requests
	})
}

// dryShaLedgerSource supplies the per-environment ledgers a gate evaluation consults. The
// production implementation walks git; taking an interface keeps evaluateEnvironment testable
// without a clone.
type dryShaLedgerSource interface {
	get(ctx context.Context, branch string) (dryShaLedger, error)
	mergeCommitTime(ctx context.Context, record validatedDryShaRecord) (*metav1.Time, error)
}

// dryShaLedgerCache builds each upstream environment's ledger at most once per reconcile, and only
// for branches actually consulted. Git setup is deferred until the first ledger is needed, so a
// PromotionStrategy with nothing in flight costs no clone.
type dryShaLedgerCache struct {
	c              client.Client
	settingsMgr    *settings.Manager
	dsvcs          *promoterv1alpha1.DryShaValidationCommitStatus
	ps             *promoterv1alpha1.PromotionStrategy
	statusByBranch map[string]promoterv1alpha1.EnvironmentStatus

	gitOperations *git.EnvironmentOperations
	ledgers       map[string]dryShaLedger
}

func newDryShaLedgerCache(
	c client.Client,
	settingsMgr *settings.Manager,
	dsvcs *promoterv1alpha1.DryShaValidationCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	statusByBranch map[string]promoterv1alpha1.EnvironmentStatus,
) *dryShaLedgerCache {
	return &dryShaLedgerCache{
		c:              c,
		settingsMgr:    settingsMgr,
		dsvcs:          dsvcs,
		ps:             ps,
		statusByBranch: statusByBranch,
		ledgers:        map[string]dryShaLedger{},
	}
}

// get returns the validated-dry-commit ledger for an environment, building it on first use.
func (l *dryShaLedgerCache) get(ctx context.Context, branch string) (dryShaLedger, error) {
	if ledger, ok := l.ledgers[branch]; ok {
		return ledger, nil
	}

	gitOperations, err := l.git(ctx)
	if err != nil {
		return dryShaLedger{}, err
	}

	// CloneRepo is blob-less and does not bring the environment branches down, so the branch this
	// ledger walks has to be fetched explicitly before rev-list can see it.
	if err := gitOperations.FetchBranch(ctx, branch); err != nil {
		return dryShaLedger{}, fmt.Errorf("failed to fetch active branch %q: %w", branch, err)
	}

	envStatus := l.statusByBranch[branch]
	// An environment that configures no active commit statuses has nothing to be healthy about, so
	// having gone live is the whole signal — the same allowance the previous-environment gate makes.
	//
	// This reads the PromotionStrategy's configuration rather than the environment's live status: a
	// status that has not been populated yet carries no commit statuses either, and treating that as
	// "gates on nothing" would credit every promotion in the window without ever checking health.
	requireHealth := l.gatesOnActiveCommitStatuses(branch)

	ledger, err := buildDryShaLedger(ctx, gitOperations, branch, l.activePath(branch), l.dsvcs.GetLookbackLimit(), envStatus.Active, requireHealth)
	if err != nil {
		return dryShaLedger{}, fmt.Errorf("failed to build validated dry commit ledger for branch %q: %w", branch, err)
	}

	l.ledgers[branch] = ledger
	return ledger, nil
}

// mergeCommitTime resolves when the promotion that validated a dry commit landed. It is looked up
// only for the record that actually satisfies a gate, so the walk itself stays cheap.
func (l *dryShaLedgerCache) mergeCommitTime(ctx context.Context, record validatedDryShaRecord) (*metav1.Time, error) {
	gitOperations, err := l.git(ctx)
	if err != nil {
		return nil, err
	}

	meta, err := gitOperations.GetShaMetadataFromGit(ctx, record.MergeSha)
	if err != nil {
		// Best effort: the gate's decision does not depend on the timestamp, so fall back to the
		// dry commit's own time rather than failing the reconcile over a display field.
		logf.FromContext(ctx).V(4).Info("failed to read merge commit time for validated dry commit", "sha", record.MergeSha, "err", err)
		if record.DryCommitTime.IsZero() {
			return nil, nil
		}
		return record.DryCommitTime.DeepCopy(), nil
	}
	if meta.CommitTime.IsZero() {
		return nil, nil
	}
	return meta.CommitTime.DeepCopy(), nil
}

// gatesOnActiveCommitStatuses reports whether an environment has any active commit statuses
// configured, combining the strategy-wide selectors with the environment's own — the same merge the
// PromotionStrategy controller performs when it builds the environment's ChangeTransferPolicy.
func (l *dryShaLedgerCache) gatesOnActiveCommitStatuses(branch string) bool {
	if len(l.ps.Spec.ActiveCommitStatuses) > 0 {
		return true
	}
	for _, env := range l.ps.Spec.Environments {
		if env.Branch == branch {
			return len(env.ActiveCommitStatuses) > 0
		}
	}
	return false
}

// activePath resolves the hydrator metadata path for an environment, applying the per-environment
// override the same way the PromotionStrategy controller does when it creates ChangeTransferPolicies.
func (l *dryShaLedgerCache) activePath(branch string) string {
	activePath := l.ps.Spec.ActivePath
	for _, env := range l.ps.Spec.Environments {
		if env.Branch == branch && env.ActivePath != "" {
			return env.ActivePath
		}
	}
	return activePath
}

// git lazily clones the repository and fetches the notes refs the ledger reads.
func (l *dryShaLedgerCache) git(ctx context.Context) (*git.EnvironmentOperations, error) {
	if l.gitOperations != nil {
		return l.gitOperations, nil
	}

	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, l.c, l.settingsMgr.GetControllerNamespace(), l.ps.Spec.RepositoryReference, l.dsvcs)
	if err != nil {
		return nil, fmt.Errorf("failed to get ScmProvider and secret for repo %q: %w", l.ps.Spec.RepositoryReference.Name, err)
	}

	gitAuthProvider, err := gitauth.CreateGitOperationsProvider(ctx, l.c, scmProvider, secret, client.ObjectKey{Namespace: l.dsvcs.Namespace, Name: l.ps.Spec.RepositoryReference.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to create git auth provider for ScmProvider %q: %w", scmProvider.GetName(), err)
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, l.c, client.ObjectKey{Namespace: l.dsvcs.Namespace, Name: l.ps.Spec.RepositoryReference.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	gitOperations := git.NewEnvironmentOperations(gitRepo, gitAuthProvider, l.dsvcs.Namespace+"/"+l.dsvcs.Name)
	if err := gitOperations.CloneRepo(ctx); err != nil {
		return nil, fmt.Errorf("failed to clone repo %q: %w", l.ps.Spec.RepositoryReference.Name, err)
	}
	// The ledger reads both notes refs: hydrator metadata for which dry commit went live, and the
	// promotion history note for the commit statuses that judge it.
	if err := gitOperations.FetchNotes(ctx); err != nil {
		return nil, fmt.Errorf("failed to fetch git notes: %w", err)
	}

	l.gitOperations = gitOperations
	return l.gitOperations, nil
}

// dryShaGraph is the in-memory dependency graph built from a DryShaValidationCommitStatus's
// environments. Nodes are environment branches; "v depends on u" means u is lower than v, so a dry
// commit validated in u unblocks v. The graph is keyed by branch name so lookups are O(1).
type dryShaGraph struct {
	// dependsOn maps a branch to the lower branches it directly depends on.
	dependsOn map[string][]string

	// branches is the set of all environment branches declared in the spec, preserved in spec order
	// so traversal output is deterministic.
	branches []string
}

// buildDryShaGraph constructs a dryShaGraph from the environment list. It rejects a duplicate branch
// (the same branch declared more than once), which would otherwise make the dependency relationships
// ambiguous. Validation that dependsOn references resolve to real branches is done in validate.
func buildDryShaGraph(environments []promoterv1alpha1.DryShaValidationEnvironment) (*dryShaGraph, error) {
	g := &dryShaGraph{
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

// upstreamClosure returns every branch reachable from branch by following dependsOn, in
// breadth-first spec order. These are the "lower" environments whose promotion history can satisfy
// this environment's gate. The graph is acyclic (validate rejects cycles), and the visited set makes
// the walk terminate regardless.
func (g *dryShaGraph) upstreamClosure(branch string) []string {
	visited := map[string]bool{branch: true}
	closure := make([]string, 0, len(g.branches))

	queue := slices.Clone(g.dependsOn[branch])
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if visited[current] {
			continue
		}
		visited[current] = true
		closure = append(closure, current)
		queue = append(queue, g.dependsOn[current]...)
	}

	return closure
}

// validateEnvironmentsMatchPS checks that the graph's declared branches are exactly the set of
// environments on the referenced PromotionStrategy. A mismatch would otherwise stall promotions with
// no clear error: an unknown branch never gets a usable CommitStatus, and a PromotionStrategy
// environment omitted from the graph waits forever on "Waiting for status to be reported" when the
// gate key is in global proposedCommitStatuses.
//
//nolint:dupl // Mirrors DAGCommitStatus's validation by design; sharing it would couple the two controllers.
func (g *dryShaGraph) validateEnvironmentsMatchPS(name string, ps *promoterv1alpha1.PromotionStrategy) error {
	psBranches := make(map[string]bool, len(ps.Spec.Environments))
	for _, env := range ps.Spec.Environments {
		psBranches[env.Branch] = true
	}
	for _, branch := range g.branches {
		if !psBranches[branch] {
			return fmt.Errorf("DryShaValidationCommitStatus %q declares branch %q, but PromotionStrategy %q has no such environment",
				name, branch, ps.Name)
		}
		delete(psBranches, branch)
	}
	if len(psBranches) > 0 {
		missing := make([]string, 0, len(psBranches))
		for branch := range psBranches {
			missing = append(missing, branch)
		}
		slices.Sort(missing)
		return fmt.Errorf("DryShaValidationCommitStatus %q is missing PromotionStrategy %q environment branches: %s",
			name, ps.Name, strings.Join(missing, ", "))
	}
	return nil
}

// validate checks the dependency graph for two failure modes that would otherwise let an environment
// silently stall:
//   - a dependsOn entry that references a branch not declared in the graph (never satisfiable), and
//   - a dependency cycle (the branches in the cycle can never all be satisfied).
//
// Surfacing either as an error is clearer than letting the depending environment hang.
//
//nolint:dupl // Mirrors DAGCommitStatus's cycle detection by design; sharing it would couple the two controllers.
func (g *dryShaGraph) validate() error {
	// Every dependsOn must resolve to a declared branch.
	for _, branch := range g.branches {
		for _, upstream := range g.dependsOn[branch] {
			if _, exists := g.dependsOn[upstream]; !exists {
				return fmt.Errorf("branch %q depends on unknown branch %q", branch, upstream)
			}
		}
	}

	// Cycle detection via Kahn's algorithm: repeatedly remove branches whose upstreams have all been
	// removed. inDegree[b] = upstreams b still depends on; downstream[u] = branches that depend on u
	// (the reverse of dependsOn), used to relax edges as branches are removed. If a cycle exists, its
	// branches never reach in-degree zero, so fewer than len(branches) are removed.
	inDegree := make(map[string]int, len(g.branches))
	downstream := make(map[string][]string, len(g.branches))
	for _, branch := range g.branches {
		inDegree[branch] = len(g.dependsOn[branch])
		for _, upstream := range g.dependsOn[branch] {
			downstream[upstream] = append(downstream[upstream], branch)
		}
	}

	// Seed the queue with roots (no upstreams).
	queue := make([]string, 0, len(g.branches))
	for _, branch := range g.branches {
		if inDegree[branch] == 0 {
			queue = append(queue, branch)
		}
	}

	removed := 0
	for len(queue) > 0 {
		branch := queue[0]
		queue = queue[1:]
		removed++
		for _, dependent := range downstream[branch] {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
			}
		}
	}

	if removed != len(g.branches) {
		return errors.New("environments contain a dependency cycle")
	}
	return nil
}
