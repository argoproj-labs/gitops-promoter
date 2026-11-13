/*
Copyright 2026.

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

const (
	// defaultTagOrderedTagTemplate is used when spec.tagTemplate is empty (the CRD default normally fills it).
	defaultTagOrderedTagTemplate = "promoter/{{ .Environment }}"

	// maxTagOrderedProvenShas bounds status.environments[].provenShas.
	maxTagOrderedProvenShas = 10

	// tagOrderedRequeueAfterRelease is how soon the gate reconciles again after it moved a tag. A release can
	// make the environment healthy without any PromotionStrategy change (a tag seeded at the commit that is
	// already live), so the follow-up must not wait for the next poll of the DRY branch.
	tagOrderedRequeueAfterRelease = time.Second
)

// TagTemplateData is the data passed to TagOrderedCommitStatus.spec.tagTemplate.
type TagTemplateData struct {
	// Environment is the environment's branch name.
	Environment string
}

// TagOrderedCommitStatusReconciler reconciles a TagOrderedCommitStatus object
type TagOrderedCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager
}

// tagOrderedEnv is the working state of one environment during a reconcile.
type tagOrderedEnv struct {
	// status is the environment's state as reported by the PromotionStrategy.
	status promoterv1alpha1.EnvironmentStatus
	branch string
	tag    string
	// releasedSha is the DRY commit the environment's tag points at ("" when the tag does not exist).
	releasedSha string
	// candidate is the newest DRY commit eligible for this environment ("" when none is).
	candidate string
	state     promoterv1alpha1.TagOrderedEnvironmentState
	message   string
	// proven lists the DRY commits this environment ran healthy, newest first.
	proven []promoterv1alpha1.TagOrderedProvenSha
	// release is true when the plan is to move the tag to candidate.
	release bool
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=tagorderedcommitstatuses,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=tagorderedcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=tagorderedcommitstatuses/finalizers,verbs=update
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=clusterscmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile orders promotions by deciding which DRY commit each environment hydrates.
//
// Every environment of the referenced PromotionStrategy has a tag, and the hydrator for that environment
// hydrates the tag instead of the DRY branch. On each reconcile the controller:
//
//  1. reads the tip of the DRY branch and every environment tag with one ls-remote,
//  2. works out where each environment is with the commit its tag points at (hydrating, promoting,
//     verifying, healthy, failed) and records the commits an environment ran healthy,
//  3. moves a root environment's tag to the tip of the DRY branch, and a downstream environment's tag to
//     the newest commit that every upstream has run healthy, subject to spec.releasePolicy,
//  4. reports a CommitStatus per environment that passes only when the proposed change was hydrated from
//     the commit the environment's tag points at.
//
// A new commit on the DRY branch therefore changes only the root environments. Downstream environments keep
// the commit they are working on until an upstream proves a newer one.
func (r *TagOrderedCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := logf.FromContext(ctx)
	logger.Info("Reconciling TagOrderedCommitStatus")
	startTime := time.Now()

	var tocs promoterv1alpha1.TagOrderedCommitStatus
	// This applies the resource status via Server-Side Apply at the end of reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &tocs, r.Client, r.Recorder, constants.TagOrderedCommitStatusControllerFieldOwner, &result, &err, &previousReady)

	if err = r.Get(ctx, req.NamespacedName, &tocs); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("TagOrderedCommitStatus not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get TagOrderedCommitStatus %q: %w", req.Name, err)
	}

	// Start fresh on the Ready condition each reconcile.
	previousReady = utils.RemoveReadyCondition(&tocs)

	if err := ensureControllerInstanceIDStable(ctx, r.SettingsMgr); err != nil {
		return ctrl.Result{}, err
	}

	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{Namespace: tocs.Namespace, Name: tocs.Spec.PromotionStrategyRef.Name}
	if err = r.Get(ctx, psKey, &ps); err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", tocs.Spec.PromotionStrategyRef.Name, err)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", tocs.Spec.PromotionStrategyRef.Name, err)
	}

	released, err := r.reconcileTags(ctx, &tocs, &ps)
	if err != nil {
		return ctrl.Result{}, err
	}

	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.TagOrderedCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration for TagOrderedCommitStatus %q: %w", tocs.Name, err)
	}
	if released {
		requeueDuration = min(requeueDuration, tagOrderedRequeueAfterRelease)
	}

	// The requeue is what notices new commits on the DRY branch: nothing in the cluster changes when one lands.
	return ctrl.Result{RequeueAfter: requeueDuration}, nil
}

// reconcileTags plans and performs the tag moves, then reports the per-environment gate. It returns whether
// any tag was moved.
func (r *TagOrderedCommitStatusReconciler) reconcileTags(ctx context.Context, tocs *promoterv1alpha1.TagOrderedCommitStatus, ps *promoterv1alpha1.PromotionStrategy) (bool, error) {
	environments, err := resolveDependentEnvironments(ps)
	if err != nil {
		return false, err
	}
	graph, err := buildDAG(environments)
	if err != nil {
		return false, fmt.Errorf("failed to build dependency graph: %w", err)
	}
	if err := graph.validateDAG(); err != nil {
		return false, fmt.Errorf("invalid dependency graph: %w", err)
	}

	tags, err := renderEnvironmentTags(tocs.Spec.TagTemplate, graph.branches)
	if err != nil {
		return false, err
	}

	gitOperations, gitAuthProvider, gitRepo, err := r.gitOperationsFor(ctx, tocs, ps)
	if err != nil {
		return false, err
	}

	// One ls-remote reads the DRY branch tip and every environment tag.
	dryBranchRef := git.BranchRef(tocs.Spec.DryBranch)
	refs := make([]string, 0, len(graph.branches)+1)
	refs = append(refs, dryBranchRef)
	for _, branch := range graph.branches {
		refs = append(refs, git.TagRef(tags[branch]))
	}
	remoteShas, err := git.LsRemoteRefs(ctx, gitAuthProvider, gitRepo, refs...)
	if err != nil {
		return false, fmt.Errorf("failed to read the DRY branch and environment tags from the remote: %w", err)
	}
	headSha := remoteShas[dryBranchRef]
	if headSha == "" {
		return false, fmt.Errorf("DRY branch %q was not found in repository %q", tocs.Spec.DryBranch, ps.Spec.RepositoryReference.Name)
	}
	tocs.Status.DryBranchSha = headSha

	statusByBranch := make(map[string]promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for _, envStatus := range ps.Status.Environments {
		statusByBranch[envStatus.Branch] = envStatus
	}
	previousByBranch := make(map[string]promoterv1alpha1.TagOrderedCommitStatusEnvironmentStatus, len(tocs.Status.Environments))
	for _, previous := range tocs.Status.Environments {
		previousByBranch[previous.Branch] = previous
	}

	envs := make(map[string]*tagOrderedEnv, len(graph.branches))
	for _, branch := range graph.branches {
		envs[branch] = &tagOrderedEnv{
			branch:      branch,
			tag:         tags[branch],
			releasedSha: remoteShas[git.TagRef(tags[branch])],
			status:      statusByBranch[branch],
			proven:      slices.Clone(previousByBranch[branch].ProvenShas),
		}
	}

	planTagOrderedReleases(graph, envs, headSha, tocs.Spec.ReleasePolicy, metav1.Now())

	// Record the plan before touching the remote, so a failed push still leaves an accurate status behind.
	tocs.Status.Environments = tagOrderedEnvironmentStatuses(graph, envs, previousByBranch)

	released, err := r.releaseCommits(ctx, tocs, graph, envs, gitOperations, headSha)
	tocs.Status.Environments = tagOrderedEnvironmentStatuses(graph, envs, previousByBranch)
	if err != nil {
		return released, err
	}

	commitStatuses, err := r.reportGates(ctx, tocs, ps, graph, envs)
	if err != nil {
		return released, err
	}

	if err := utils.CleanupOrphanedCommitStatuses(ctx, r.Client, r.Recorder, tocs, commitStatuses); err != nil {
		return released, fmt.Errorf("failed to cleanup orphaned CommitStatus resources: %w", err)
	}

	utils.InheritNotReadyConditionFromObjects(tocs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	return released, nil
}

// gitOperationsFor builds the git plumbing for the PromotionStrategy's repository. The clone is not created
// here: a reconcile that moves no tag never needs one.
func (r *TagOrderedCommitStatusReconciler) gitOperationsFor(ctx context.Context, tocs *promoterv1alpha1.TagOrderedCommitStatus, ps *promoterv1alpha1.PromotionStrategy) (*git.EnvironmentOperations, gitOperationsProvider, *promoterv1alpha1.GitRepository, error) {
	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, r.SettingsMgr, ps.Spec.RepositoryReference, tocs)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get ScmProvider and secret for repo %q: %w", ps.Spec.RepositoryReference.Name, err)
	}
	repoKey := client.ObjectKey{Namespace: tocs.Namespace, Name: ps.Spec.RepositoryReference.Name}
	gitAuthProvider, err := gitauth.CreateGitOperationsProvider(ctx, r.Client, scmProvider, secret, repoKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create git auth provider for ScmProvider %q: %w", scmProvider.GetName(), err)
	}
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, repoKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}
	// The suffix keeps this clone apart from the ChangeTransferPolicy clones of the same repository.
	gitOperations := git.NewEnvironmentOperations(gitRepo, gitAuthProvider, tocs.Namespace+"/"+tocs.Name+"-tag-ordered")
	return gitOperations, gitAuthProvider, gitRepo, nil
}

// releaseCommits moves the tag of every environment the plan marked for release. It returns whether any
// tag was moved.
//
// A candidate that is not reachable from the DRY branch is never released: the tags decide what every
// environment runs, so they must not carry a commit that did not go through the DRY branch.
func (r *TagOrderedCommitStatusReconciler) releaseCommits(
	ctx context.Context,
	tocs *promoterv1alpha1.TagOrderedCommitStatus,
	graph *dag,
	envs map[string]*tagOrderedEnv,
	gitOperations *git.EnvironmentOperations,
	headSha string,
) (bool, error) {
	logger := logf.FromContext(ctx)

	fetched := false
	released := false
	for _, branch := range graph.branches {
		env := envs[branch]
		if !env.release {
			continue
		}

		if !fetched {
			if err := gitOperations.CloneRepo(ctx); err != nil {
				return released, fmt.Errorf("failed to clone repo: %w", err)
			}
			if err := gitOperations.FetchBranch(ctx, tocs.Spec.DryBranch); err != nil {
				return released, fmt.Errorf("failed to fetch DRY branch %q: %w", tocs.Spec.DryBranch, err)
			}
			fetched = true
		}

		onDryBranch, err := commitIsOnBranchTip(ctx, gitOperations, env.candidate, headSha)
		if err != nil {
			return released, fmt.Errorf("failed to check that %s is on DRY branch %q: %w", env.candidate, tocs.Spec.DryBranch, err)
		}
		if !onDryBranch {
			env.release = false
			env.message = fmt.Sprintf("Refusing to release %s: it is not reachable from DRY branch %q", shortSha(env.candidate), tocs.Spec.DryBranch)
			logger.Info("Candidate commit is not on the DRY branch, not releasing it", "branch", branch, "candidate", env.candidate, "dryBranch", tocs.Spec.DryBranch)
			r.Recorder.Eventf(tocs, nil, "Warning", constants.DryCommitNotOnDryBranchReason, "ReleasingDryCommit",
				constants.DryCommitNotOnDryBranchMessage, env.candidate, branch, tocs.Spec.DryBranch)
			continue
		}

		if err := gitOperations.PushTag(ctx, env.tag, env.candidate, env.releasedSha); err != nil {
			return released, fmt.Errorf("failed to release %s to environment %q: %w", env.candidate, branch, err)
		}

		previous := env.releasedSha
		if previous == "" {
			previous = "none"
		}
		logger.Info("Released DRY commit to environment", "branch", branch, "tag", env.tag, "sha", env.candidate, "previous", previous)
		r.Recorder.Eventf(tocs, nil, "Normal", constants.DryCommitReleasedReason, "ReleasingDryCommit",
			constants.DryCommitReleasedMessage, env.candidate, branch, env.tag, previous)

		env.releasedSha = env.candidate
		env.release = false
		env.state = classifyTagOrderedEnvironment(env.status, env.releasedSha)
		env.message = tagOrderedStateMessage(env.state, env.releasedSha)
		released = true
	}
	return released, nil
}

// commitIsOnBranchTip reports whether sha is the branch tip or one of its ancestors. Both commits must be
// in the clone; a commit the clone does not have is reported as not on the branch.
func commitIsOnBranchTip(ctx context.Context, gitOperations *git.EnvironmentOperations, sha, tipSha string) (bool, error) {
	if sha == tipSha {
		return true, nil
	}
	if !gitOperations.CommitExists(ctx, sha) {
		return false, nil
	}
	isAncestor, err := gitOperations.CommitIsAncestor(ctx, sha, tipSha)
	if err != nil {
		return false, fmt.Errorf("failed to check ancestry: %w", err)
	}
	return isAncestor, nil
}

// reportGates writes the per-environment CommitStatus that the ChangeTransferPolicy gates on. The status
// passes only when the proposed change was hydrated from the commit the environment's tag points at, so a
// change hydrated from anything else (for example a hydrator still following the DRY branch) cannot merge.
func (r *TagOrderedCommitStatusReconciler) reportGates(
	ctx context.Context,
	tocs *promoterv1alpha1.TagOrderedCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	graph *dag,
	envs map[string]*tagOrderedEnv,
) ([]*promoterv1alpha1.CommitStatus, error) {
	logger := logf.FromContext(ctx)
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(graph.branches))

	for i, branch := range graph.branches {
		env := envs[branch]
		entry := &tocs.Status.Environments[i]

		// Skip when there is no proposed change: there is no in-flight PR to gate, and the proposed
		// hydrated SHA may be empty. Keep any existing CommitStatus in the valid set so orphan cleanup
		// leaves the last report alone, and mirror it onto status.environments[].
		if env.status.Proposed.Hydrated.Sha == "" || env.status.Active.Dry.Sha == env.status.Proposed.Dry.Sha {
			logger.V(4).Info("Skipping environment with no proposed change", "branch", branch)
			existing := &promoterv1alpha1.CommitStatus{}
			name := utils.CommitStatusResourceName(ctx, tocs, branch)
			if err := r.Get(ctx, client.ObjectKey{Namespace: tocs.Namespace, Name: name}, existing); err != nil {
				if !k8serrors.IsNotFound(err) {
					return nil, fmt.Errorf("failed to get existing CommitStatus for branch %q: %w", branch, err)
				}
			} else {
				commitStatuses = append(commitStatuses, existing)
				entry.GateEnvironmentCommitStatus = utils.GateEnvironmentCommitStatusFromCommitStatus(existing)
			}
			continue
		}

		phase, description := tagOrderedGate(env)
		cs, gate, err := r.upsertGateCommitStatus(ctx, tocs, ps, branch, env.status.Proposed.Hydrated.Sha, phase, description)
		if err != nil {
			return nil, fmt.Errorf("failed to set CommitStatus for branch %q: %w", branch, err)
		}
		commitStatuses = append(commitStatuses, cs)
		entry.GateEnvironmentCommitStatus = gate
	}

	return commitStatuses, nil
}

// upsertGateCommitStatus upserts, via Server-Side Apply, the CommitStatus that reports whether the proposed
// change of an environment was hydrated from the commit released to it.
func (r *TagOrderedCommitStatusReconciler) upsertGateCommitStatus(
	ctx context.Context,
	tocs *promoterv1alpha1.TagOrderedCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	branch string,
	reportedSha string,
	phase promoterv1alpha1.CommitStatusPhase,
	description string,
) (*promoterv1alpha1.CommitStatus, promoterv1alpha1.GateEnvironmentCommitStatus, error) {
	key := tocs.Spec.Key
	commitStatusName := utils.CommitStatusResourceName(ctx, tocs, branch)

	kind := reflect.TypeFor[promoterv1alpha1.TagOrderedCommitStatus]().Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)

	// Use the stable gate key as the SCM commit status context so users can reference a single predictable
	// name in branch protection rules. The per-environment detail goes in the description.
	commitStatusSpec := acv1alpha1.CommitStatusSpec().
		WithRepositoryReference(acv1alpha1.ObjectReference().
			WithName(ps.Spec.RepositoryReference.Name)).
		WithSha(reportedSha).
		WithName(key).
		WithDescription(description).
		WithPhase(phase)
	if tocs.Spec.PublishTo != nil {
		commitStatusSpec = commitStatusSpec.WithPublishTo(*tocs.Spec.PublishTo)
	}

	commitStatusApply := acv1alpha1.CommitStatus(commitStatusName, tocs.Namespace).
		WithLabels(utils.CommitStatusStandardLabels(tocs, branch, key)).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(tocs.Name).
			WithUID(tocs.UID).
			WithController(true).
			WithBlockOwnerDeletion(true)).
		WithSpec(commitStatusSpec)

	// Read the previously persisted phase so the phase-change event below stays transition-only.
	previousPhase := ""
	existingCommitStatus := &promoterv1alpha1.CommitStatus{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: tocs.Namespace, Name: commitStatusName}, existingCommitStatus); err == nil {
		previousPhase = string(existingCommitStatus.Spec.Phase)
	} else if !k8serrors.IsNotFound(err) {
		return nil, promoterv1alpha1.GateEnvironmentCommitStatus{}, fmt.Errorf("failed to get existing CommitStatus %q: %w", commitStatusName, err)
	}

	commitStatus := &promoterv1alpha1.CommitStatus{}
	commitStatus.Name = commitStatusName
	commitStatus.Namespace = tocs.Namespace
	if err := r.Patch(ctx, commitStatus, utils.ApplyPatch{ApplyConfig: commitStatusApply}, client.FieldOwner(constants.TagOrderedCommitStatusControllerFieldOwner), client.ForceOwnership); err != nil {
		return nil, promoterv1alpha1.GateEnvironmentCommitStatus{}, fmt.Errorf("failed to apply CommitStatus: %w", err)
	}

	emitCommitStatusPhaseChangedEvent(r.Recorder, tocs, key, branch, previousPhase, string(phase))

	return commitStatus, promoterv1alpha1.GateEnvironmentCommitStatus{
		Phase:       phase,
		Description: description,
		ReportedSha: reportedSha,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TagOrderedCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.TagOrderedCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get TagOrderedCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.TagOrderedCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get TagOrderedCommitStatus max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.TagOrderedCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.PromotionStrategy{}, CommitStatusGatePromotionStrategyWatchHandler[promoterv1alpha1.TagOrderedCommitStatusList](r.Client)).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Named("tagorderedcommitstatus").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// planTagOrderedReleases works out, for every environment, where it is with the commit its tag points at,
// which commit is the newest one eligible for it, and whether to release that commit now. It reads and
// updates envs in place and performs no I/O.
//
// Environments are visited upstream-first, so a commit that an upstream is recorded as having proven in
// this pass is immediately a candidate for its downstreams.
func planTagOrderedReleases(
	graph *dag,
	envs map[string]*tagOrderedEnv,
	dryBranchSha string,
	policy promoterv1alpha1.TagOrderedReleasePolicy,
	now metav1.Time,
) {
	for _, branch := range topologicalOrder(graph) {
		env := envs[branch]
		upstreams := graph.dependsOn[branch]

		env.state = classifyTagOrderedEnvironment(env.status, env.releasedSha)
		if env.state == promoterv1alpha1.TagOrderedEnvironmentStateHealthy {
			env.proven = recordProvenSha(env.proven, env.releasedSha, now)
		}
		env.message = tagOrderedStateMessage(env.state, env.releasedSha)

		seeded := false
		if len(upstreams) == 0 {
			// Root environments follow the DRY branch.
			env.candidate = dryBranchSha
		} else {
			proven := make([][]promoterv1alpha1.TagOrderedProvenSha, 0, len(upstreams))
			for _, upstream := range upstreams {
				proven = append(proven, releasableProvenShas(envs[upstream]))
			}
			env.candidate = newestCommonProvenSha(proven)

			// Nothing has been proven upstream and the environment has no tag yet. Point the tag at the
			// commit that is already live, so the hydrator has a revision to resolve and adopting this
			// gate on a running environment changes nothing.
			if env.candidate == "" && env.releasedSha == "" && env.status.Active.Dry.Sha != "" {
				env.candidate = env.status.Active.Dry.Sha
				seeded = true
			}
		}

		switch {
		case env.candidate == "":
			if env.releasedSha == "" {
				env.message = "Waiting for upstream environments to prove a commit"
			}
		case env.candidate == env.releasedSha:
			// Up to date.
		case !seeded && holdRelease(policy, env):
			env.message = fmt.Sprintf("Holding %s until active commit statuses on %s settle", shortSha(env.candidate), shortSha(env.releasedSha))
		default:
			env.release = true
		}
	}
}

// holdRelease reports whether a newer commit must wait because the environment is still verifying the commit
// it has. Only a first verification is protected: once the environment has proven the commit, later health
// flapping does not hold anything back.
func holdRelease(policy promoterv1alpha1.TagOrderedReleasePolicy, env *tagOrderedEnv) bool {
	if policy == promoterv1alpha1.TagOrderedReleasePolicyImmediately {
		return false
	}
	return env.state == promoterv1alpha1.TagOrderedEnvironmentStateVerifying && !isProvenSha(env.proven, env.releasedSha)
}

// classifyTagOrderedEnvironment works out where an environment is with the commit its tag points at.
func classifyTagOrderedEnvironment(envStatus promoterv1alpha1.EnvironmentStatus, releasedSha string) promoterv1alpha1.TagOrderedEnvironmentState {
	if releasedSha == "" {
		return promoterv1alpha1.TagOrderedEnvironmentStateUnreleased
	}

	if envStatus.Active.Dry.Sha != releasedSha {
		if getEffectiveHydratedDrySha(envStatus) != releasedSha {
			return promoterv1alpha1.TagOrderedEnvironmentStateHydrating
		}
		// The hydrator has processed the released commit, but the active branch does not carry it. That is
		// only "done" for a clean no-op: the hydrator advanced its git note without a new hydrated commit
		// (the released commit does not change this environment), and nothing else is waiting to merge.
		isNoOp := envStatus.Proposed.Dry.Sha != releasedSha
		hasPendingChange := envStatus.Active.Dry.Sha != envStatus.Proposed.Dry.Sha
		if !isNoOp || hasPendingChange {
			return promoterv1alpha1.TagOrderedEnvironmentStatePromoting
		}
	}

	for _, commitStatus := range envStatus.Active.CommitStatuses {
		if commitStatus.Phase == string(promoterv1alpha1.CommitPhaseFailure) {
			return promoterv1alpha1.TagOrderedEnvironmentStateFailed
		}
	}
	if !utils.AreCommitStatusesPassing(envStatus.Active.CommitStatuses) {
		return promoterv1alpha1.TagOrderedEnvironmentStateVerifying
	}
	return promoterv1alpha1.TagOrderedEnvironmentStateHealthy
}

// tagOrderedStateMessage describes an environment state for status.environments[].message.
func tagOrderedStateMessage(state promoterv1alpha1.TagOrderedEnvironmentState, releasedSha string) string {
	sha := shortSha(releasedSha)
	switch state {
	case promoterv1alpha1.TagOrderedEnvironmentStateUnreleased:
		return "Waiting for a commit to be released"
	case promoterv1alpha1.TagOrderedEnvironmentStateHydrating:
		return fmt.Sprintf("Waiting for hydrator to process %s", sha)
	case promoterv1alpha1.TagOrderedEnvironmentStatePromoting:
		return fmt.Sprintf("Waiting for %s to be merged", sha)
	case promoterv1alpha1.TagOrderedEnvironmentStateVerifying:
		return fmt.Sprintf("Waiting for active commit statuses on %s", sha)
	case promoterv1alpha1.TagOrderedEnvironmentStateHealthy:
		return fmt.Sprintf("%s is live and healthy", sha)
	case promoterv1alpha1.TagOrderedEnvironmentStateFailed:
		return fmt.Sprintf("%s is live with a failing active commit status", sha)
	default:
		return ""
	}
}

// tagOrderedGate returns the phase and description of the gate for an environment with a proposed change.
func tagOrderedGate(env *tagOrderedEnv) (promoterv1alpha1.CommitStatusPhase, string) {
	if env.releasedSha == "" {
		return promoterv1alpha1.CommitPhasePending, fmt.Sprintf("Waiting for a commit to be released to %q", env.branch)
	}
	hydratedDrySha := getEffectiveHydratedDrySha(env.status)
	if hydratedDrySha != env.releasedSha {
		return promoterv1alpha1.CommitPhasePending, hydratorPendingReason(env.branch, env.releasedSha, hydratedDrySha)
	}
	return promoterv1alpha1.CommitPhaseSuccess, fmt.Sprintf("%s is released to %q", shortSha(env.releasedSha), env.branch)
}

// recordProvenSha puts sha at the front of the proven list, dropping any older record of it and trimming
// the list to maxTagOrderedProvenShas. A sha that is already the newest entry keeps its original time.
func recordProvenSha(proven []promoterv1alpha1.TagOrderedProvenSha, sha string, now metav1.Time) []promoterv1alpha1.TagOrderedProvenSha {
	if sha == "" {
		return proven
	}
	if len(proven) > 0 && proven[0].Sha == sha {
		return proven
	}
	out := make([]promoterv1alpha1.TagOrderedProvenSha, 0, len(proven)+1)
	out = append(out, promoterv1alpha1.TagOrderedProvenSha{Sha: sha, Time: now})
	for _, entry := range proven {
		if entry.Sha != sha {
			out = append(out, entry)
		}
	}
	if len(out) > maxTagOrderedProvenShas {
		out = out[:maxTagOrderedProvenShas]
	}
	return out
}

func isProvenSha(proven []promoterv1alpha1.TagOrderedProvenSha, sha string) bool {
	return slices.ContainsFunc(proven, func(entry promoterv1alpha1.TagOrderedProvenSha) bool {
		return entry.Sha == sha
	})
}

// releasableProvenShas returns the commits an upstream has proven and that may be released downstream. A
// commit the upstream is failing on right now is left out, even if it passed earlier.
func releasableProvenShas(upstream *tagOrderedEnv) []promoterv1alpha1.TagOrderedProvenSha {
	if upstream.state != promoterv1alpha1.TagOrderedEnvironmentStateFailed {
		return upstream.proven
	}
	out := make([]promoterv1alpha1.TagOrderedProvenSha, 0, len(upstream.proven))
	for _, entry := range upstream.proven {
		if entry.Sha != upstream.releasedSha {
			out = append(out, entry)
		}
	}
	return out
}

// newestCommonProvenSha returns the commit that every upstream has proven and that became proven by all of
// them most recently, or "" when there is none. Each list is newest first.
func newestCommonProvenSha(upstreams [][]promoterv1alpha1.TagOrderedProvenSha) string {
	if len(upstreams) == 0 {
		return ""
	}

	best := ""
	var bestTime metav1.Time
	for _, entry := range upstreams[0] {
		// A commit is proven by all upstreams at the moment the last of them proves it.
		provenByAllAt := entry.Time
		common := true
		for _, other := range upstreams[1:] {
			idx := slices.IndexFunc(other, func(o promoterv1alpha1.TagOrderedProvenSha) bool { return o.Sha == entry.Sha })
			if idx < 0 {
				common = false
				break
			}
			if other[idx].Time.After(provenByAllAt.Time) {
				provenByAllAt = other[idx].Time
			}
		}
		if !common {
			continue
		}
		// Lists are newest first, so on a tie the entry seen first is kept.
		if best == "" || provenByAllAt.After(bestTime.Time) {
			best = entry.Sha
			bestTime = provenByAllAt
		}
	}
	return best
}

// topologicalOrder returns the graph's branches with every branch after all of its upstreams. Branches with
// no ordering between them keep their spec order. The graph must be acyclic (see validateDAG).
func topologicalOrder(g *dag) []string {
	order := make([]string, 0, len(g.branches))
	placed := make(map[string]bool, len(g.branches))
	for len(order) < len(g.branches) {
		progressed := false
		for _, branch := range g.branches {
			if placed[branch] {
				continue
			}
			ready := true
			for _, upstream := range g.dependsOn[branch] {
				if !placed[upstream] {
					ready = false
					break
				}
			}
			if ready {
				placed[branch] = true
				order = append(order, branch)
				progressed = true
			}
		}
		if !progressed {
			// Unreachable for a validated graph; avoid spinning if it is ever called on a cyclic one.
			break
		}
	}
	return order
}

// tagOrderedEnvironmentStatuses builds status.environments[] in the PromotionStrategy's environment order.
// Gate report fields are carried over from the previous status until reportGates refreshes them.
func tagOrderedEnvironmentStatuses(
	graph *dag,
	envs map[string]*tagOrderedEnv,
	previousByBranch map[string]promoterv1alpha1.TagOrderedCommitStatusEnvironmentStatus,
) []promoterv1alpha1.TagOrderedCommitStatusEnvironmentStatus {
	out := make([]promoterv1alpha1.TagOrderedCommitStatusEnvironmentStatus, 0, len(graph.branches))
	for _, branch := range graph.branches {
		env := envs[branch]
		out = append(out, promoterv1alpha1.TagOrderedCommitStatusEnvironmentStatus{
			Branch:                      branch,
			GateEnvironmentCommitStatus: previousByBranch[branch].GateEnvironmentCommitStatus,
			Tag:                         env.tag,
			ReleasedSha:                 env.releasedSha,
			CandidateSha:                env.candidate,
			State:                       env.state,
			Message:                     env.message,
			ProvenShas:                  env.proven,
		})
	}
	return out
}

// renderEnvironmentTags renders the tag name of every environment branch and checks that the names are
// valid and distinct.
func renderEnvironmentTags(tagTemplate string, branches []string) (map[string]string, error) {
	if tagTemplate == "" {
		tagTemplate = defaultTagOrderedTagTemplate
	}

	tags := make(map[string]string, len(branches))
	branchByTag := make(map[string]string, len(branches))
	for _, branch := range branches {
		tag, err := utils.RenderStringTemplate(tagTemplate, TagTemplateData{Environment: branch}, "missingkey=error")
		if err != nil {
			return nil, fmt.Errorf("failed to render tagTemplate for environment %q: %w", branch, err)
		}
		if err := validateTagName(tag); err != nil {
			return nil, fmt.Errorf("tagTemplate rendered an invalid tag %q for environment %q: %w", tag, branch, err)
		}
		if other, exists := branchByTag[tag]; exists {
			return nil, fmt.Errorf("tagTemplate rendered the same tag %q for environments %q and %q; include .Environment in the template", tag, other, branch)
		}
		branchByTag[tag] = branch
		tags[branch] = tag
	}
	return tags, nil
}

// validateTagName applies the git ref name rules that matter for a tag name (see git-check-ref-format).
func validateTagName(tag string) error {
	if tag == "" {
		return errors.New("tag is empty")
	}
	if strings.HasPrefix(tag, "-") {
		return errors.New("tag must not start with '-'")
	}
	if strings.HasPrefix(tag, "/") || strings.HasSuffix(tag, "/") || strings.Contains(tag, "//") {
		return errors.New("tag must not start or end with '/' or contain '//'")
	}
	if strings.HasSuffix(tag, ".") || strings.Contains(tag, "..") || strings.Contains(tag, "@{") {
		return errors.New("tag must not end with '.' or contain '..' or '@{'")
	}
	for _, component := range strings.Split(tag, "/") {
		if strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return errors.New("tag path components must not start with '.' or end with '.lock'")
		}
	}
	for _, char := range tag {
		if char <= ' ' || char == 0x7f || strings.ContainsRune(`~^:?*[\`, char) {
			return fmt.Errorf("tag must not contain %q", char)
		}
	}
	return nil
}

// shortSha abbreviates a commit SHA for messages.
func shortSha(sha string) string {
	if sha == "" {
		return "none"
	}
	return utils.TruncateString(sha, 7)
}
