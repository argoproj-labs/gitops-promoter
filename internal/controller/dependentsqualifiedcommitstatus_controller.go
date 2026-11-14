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
	"path"
	"reflect"
	"strings"
	"sync"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	acmetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

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
	// dqSoftRetry is how soon the gate tries again after a failure it reports on its Ready condition instead
	// of returning as an error (the dry repository could not be read, or a tag push was rejected).
	dqSoftRetry = time.Minute
	// dqNudgeBaseDelay and dqNudgeMaxDelay bound how often the gate wakes a ChangeTransferPolicy whose
	// hydrator has not caught up to its environment tag yet.
	dqNudgeBaseDelay = 15 * time.Second
	dqNudgeMaxDelay  = 2 * time.Minute
	// dqNoteSearchDepth is how many commits back on an active branch the gate looks for a qualification
	// note when it has no status to start from.
	dqNoteSearchDepth = 200
	// dqShortShaLength is how much of a SHA is shown in commit status descriptions.
	dqShortShaLength = 7
)

// DependentsQualifiedURLTemplateData is the data passed to DependentsQualifiedCommitStatus.spec.url.template.
type DependentsQualifiedURLTemplateData struct {
	PromotionStrategy *promoterv1alpha1.PromotionStrategy
	Environment       string
	// DependsOnQuery is DependsOn encoded as repeated env= query parameters
	// (e.g. "env=e2e&env=perf"), ready to append after "?". Empty when DependsOn is empty.
	DependsOnQuery string
	// DependsOn is the current environment's immediate upstream branches (one edge away).
	DependsOn                       []string
	DependentsQualifiedCommitStatus promoterv1alpha1.DependentsQualifiedCommitStatus
}

// ---------------------------------------------------------------------------------------------------------
// Decisions.
//
// Everything in this section is a pure function of what the reconciler observed. The reconciler below only
// gathers the observations (PromotionStrategy status, tags, the dry commit graph) and carries the decisions
// out (pushes tags, writes notes, applies CommitStatuses).
// ---------------------------------------------------------------------------------------------------------

// dqCommitGraph answers ancestry questions about dry commits.
type dqCommitGraph interface {
	// Contains reports whether descendant is ancestor or has ancestor in its history. A commit the graph
	// does not know is contained in nothing and contains nothing.
	Contains(ctx context.Context, descendant, ancestor string) (bool, error)
	// MergeBase returns the newest commit every one of the given commits contains, or "" when there is none.
	MergeBase(ctx context.Context, shas ...string) (string, error)
}

// dqEnvironment is what the gate knows about one environment when it decides.
type dqEnvironment struct {
	// Qualified is the newest dry commit the environment has qualified, nil when it has qualified nothing.
	Qualified *promoterv1alpha1.DependentsQualifiedWatermark
	// Branch is the environment's active branch.
	Branch string
	// Tag is the name of the environment's tag; TagSha is the dry commit it points at, "" when it does not exist.
	Tag    string
	TagSha string
	// LiveDrySha is the dry commit whose manifests the environment is running (see dqLiveDrySha).
	LiveDrySha string
	// DependsOn lists the environment's direct upstreams. Empty for a root.
	DependsOn []string
	// Status is the environment's status on the PromotionStrategy.
	Status promoterv1alpha1.EnvironmentStatus
	// Manual is true when a person moves the tag (advance: Manual).
	Manual bool
	// Restored is true when the live commit was put there by a RestoreActiveCommit.
	Restored bool
}

// dqTagMove is one tag the controller should create or move.
type dqTagMove struct {
	Branch string
	Tag    string
	// From is the commit the tag points at now, "" when the tag is to be created.
	From string
	To   string
}

// dqGate is the ordering commit status for one environment's proposed commit.
type dqGate struct {
	Phase       promoterv1alpha1.CommitStatusPhase
	Description string
	Upstreams   []promoterv1alpha1.DependentsQualifiedCommitStatusUpstreamStatus
}

func dqShortSha(sha string) string {
	return utils.TruncateString(sha, dqShortShaLength)
}

// dqShortShaOrNone is dqShortSha for descriptions that may have no commit to name.
func dqShortShaOrNone(sha string) string {
	if sha == "" {
		return "none"
	}
	return dqShortSha(sha)
}

// dqLiveDrySha returns the dry commit whose manifests an environment is running.
//
// That is normally the active dry SHA. The exception is a no-op hydration: the hydrator processed a newer dry
// commit, found nothing to change, and recorded that in a git note on the proposed tip instead of pushing a
// commit. If the proposed tip is what the environment is running, the environment is running the newer
// commit's manifests too. sameManifests answers that; it is only asked when a note claims a newer dry commit
// and nothing is waiting to be promoted.
func dqLiveDrySha(status promoterv1alpha1.EnvironmentStatus, sameManifests func() (bool, error)) (string, error) {
	active := status.Active.Dry.Sha
	if active != status.Proposed.Dry.Sha {
		// A promotion is pending; the note describes the proposed commit, not what is live.
		return active, nil
	}
	reported := getEffectiveHydratedDrySha(status)
	if reported == "" || reported == active {
		return active, nil
	}
	same, err := sameManifests()
	if err != nil {
		return "", fmt.Errorf("failed to compare the proposed and active manifests of %q: %w", status.Branch, err)
	}
	if same {
		return reported, nil
	}
	// The same dry commit was hydrated again with different output, so the note is about manifests the
	// environment is not running.
	return active, nil
}

// dqQualify returns the environment's watermark after looking at what is live in it now.
//
// It returns env.Qualified itself (the same pointer) when nothing changes, a new watermark when the
// environment qualifies a newer commit or a restore pulls the watermark back, and nil when the environment
// has qualified nothing that is still on the dry branch.
func dqQualify(ctx context.Context, graph dqCommitGraph, head string, env dqEnvironment, now metav1.Time) (*promoterv1alpha1.DependentsQualifiedWatermark, error) {
	watermark := env.Qualified
	if watermark != nil {
		onBranch, err := graph.Contains(ctx, head, watermark.DrySha)
		if err != nil {
			return nil, fmt.Errorf("failed to check whether %q's qualified commit is on the dry branch: %w", env.Branch, err)
		}
		if !onBranch {
			// The dry branch was rewritten and the commit is gone from it.
			watermark = nil
		}
	}

	live := env.LiveDrySha
	if live == "" || (watermark != nil && watermark.DrySha == live) {
		return watermark, nil
	}
	liveOnBranch, err := graph.Contains(ctx, head, live)
	if err != nil {
		return nil, fmt.Errorf("failed to check whether %q's live commit is on the dry branch: %w", env.Branch, err)
	}
	if !liveOnBranch {
		return watermark, nil
	}

	next := &promoterv1alpha1.DependentsQualifiedWatermark{
		QualifiedAt: now,
		DrySha:      live,
		HydratedSha: env.Status.Active.Hydrated.Sha,
	}

	if watermark != nil {
		behind, err := graph.Contains(ctx, watermark.DrySha, live)
		if err != nil {
			return nil, fmt.Errorf("failed to compare %q's live commit with its qualified commit: %w", env.Branch, err)
		}
		if behind {
			// An older commit is live. That alone revokes nothing (qualification is latched), unless the
			// commit was restored: a restore says the commits after it should not have been trusted.
			if env.Restored {
				return next, nil
			}
			return watermark, nil
		}
	}

	if !utils.AreCommitStatusesPassing(env.Status.Active.CommitStatuses) {
		return watermark, nil
	}

	if watermark != nil {
		ahead, err := graph.Contains(ctx, live, watermark.DrySha)
		if err != nil {
			return nil, fmt.Errorf("failed to compare %q's live commit with its qualified commit: %w", env.Branch, err)
		}
		if !ahead {
			// Neither commit contains the other; moving the watermark would un-qualify commits.
			return watermark, nil
		}
	}
	return next, nil
}

// dqPlanTags returns the tags to create or move, in environment order.
//
// A root's tag follows the dry branch HEAD. A dependent's tag follows the newest commit every direct upstream
// has qualified. A tag only moves forward, except that a tag which is no longer on the dry branch (the branch
// was force-pushed) is reset. An advance: Manual tag is created when missing and never moved.
func dqPlanTags(ctx context.Context, graph dqCommitGraph, head string, envs []dqEnvironment) ([]dqTagMove, error) {
	byBranch := make(map[string]dqEnvironment, len(envs))
	for _, env := range envs {
		byBranch[env.Branch] = env
	}

	var moves []dqTagMove
	for _, env := range envs {
		target, err := dqTagTarget(ctx, graph, head, env, byBranch)
		if err != nil {
			return nil, err
		}
		var to string
		if env.TagSha == "" {
			to, err = dqNewTagSha(ctx, graph, head, env, target)
		} else {
			to, err = dqMovedTagSha(ctx, graph, head, env, target)
		}
		if err != nil {
			return nil, err
		}
		if to != "" && to != env.TagSha {
			moves = append(moves, dqTagMove{Branch: env.Branch, Tag: env.Tag, From: env.TagSha, To: to})
		}
	}
	return moves, nil
}

// dqTagTarget returns the commit an automatically advanced tag wants to be at: the dry branch HEAD for a
// root, the newest commit every direct upstream has qualified for a dependent, and "" for a dependent with an
// upstream that has qualified nothing.
func dqTagTarget(ctx context.Context, graph dqCommitGraph, head string, env dqEnvironment, byBranch map[string]dqEnvironment) (string, error) {
	if len(env.DependsOn) == 0 {
		return head, nil
	}
	qualified := make([]string, 0, len(env.DependsOn))
	for _, upstream := range env.DependsOn {
		watermark := byBranch[upstream].Qualified
		if watermark == nil {
			return "", nil
		}
		qualified = append(qualified, watermark.DrySha)
	}
	target, err := graph.MergeBase(ctx, qualified...)
	if err != nil {
		return "", fmt.Errorf("failed to find the newest commit every upstream of %q has qualified: %w", env.Branch, err)
	}
	return target, nil
}

// dqNewTagSha returns where to create a missing tag: at the commit that is live in the environment, so the
// first hydration from the tag changes nothing, or at the dry branch HEAD when nothing on the branch is live.
// An automatically advanced tag is created directly at its target when that is ahead.
func dqNewTagSha(ctx context.Context, graph dqCommitGraph, head string, env dqEnvironment, target string) (string, error) {
	base := head
	if env.LiveDrySha != "" {
		onBranch, err := graph.Contains(ctx, head, env.LiveDrySha)
		if err != nil {
			return "", fmt.Errorf("failed to check whether %q's live commit is on the dry branch: %w", env.Branch, err)
		}
		if onBranch {
			base = env.LiveDrySha
		}
	}
	if env.Manual || target == "" || target == base {
		return base, nil
	}
	ahead, err := graph.Contains(ctx, target, base)
	if err != nil {
		return "", fmt.Errorf("failed to compare the target of tag %q with %q's live commit: %w", env.Tag, env.Branch, err)
	}
	if ahead {
		return target, nil
	}
	return base, nil
}

// dqMovedTagSha returns where an existing tag should point; env.TagSha when it should stay.
func dqMovedTagSha(ctx context.Context, graph dqCommitGraph, head string, env dqEnvironment, target string) (string, error) {
	if env.Manual {
		return env.TagSha, nil
	}
	onBranch, err := graph.Contains(ctx, head, env.TagSha)
	if err != nil {
		return "", fmt.Errorf("failed to check whether tag %q is on the dry branch: %w", env.Tag, err)
	}
	if target == "" || target == env.TagSha {
		return env.TagSha, nil
	}
	if !onBranch {
		// The dry branch was rewritten. The tag points at history that no longer exists there, so
		// "forward" has no meaning; put it back on the branch.
		return target, nil
	}
	ahead, err := graph.Contains(ctx, target, env.TagSha)
	if err != nil {
		return "", fmt.Errorf("failed to compare tag %q with its target: %w", env.Tag, err)
	}
	if ahead {
		return target, nil
	}
	return env.TagSha, nil
}

// dqEvaluateGate decides whether an environment's proposed commit may be promoted: the hydrator has caught up
// to the environment's tag, the tagged commit is on the dry branch, and every direct upstream has qualified
// the tagged commit or a newer one that contains it.
func dqEvaluateGate(ctx context.Context, graph dqCommitGraph, head, dryBranch string, env dqEnvironment, byBranch map[string]dqEnvironment) (dqGate, error) {
	pending := func(description string) dqGate {
		return dqGate{Phase: promoterv1alpha1.CommitPhasePending, Description: description}
	}

	if env.TagSha == "" {
		return pending(fmt.Sprintf("Waiting for tag %q to be created", env.Tag)), nil
	}

	hydrated := getEffectiveHydratedDrySha(env.Status)
	if hydrated != env.TagSha {
		return pending(fmt.Sprintf("Waiting for hydrator to process tag %q at %s (currently %s)", env.Tag, dqShortSha(env.TagSha), dqShortShaOrNone(hydrated))), nil
	}

	onBranch, err := graph.Contains(ctx, head, env.TagSha)
	if err != nil {
		return dqGate{}, fmt.Errorf("failed to check whether tag %q is on the dry branch: %w", env.Tag, err)
	}
	if !onBranch {
		return pending(fmt.Sprintf("Tag %q at %s is not on dry branch %q", env.Tag, dqShortSha(env.TagSha), dryBranch)), nil
	}

	gate := dqGate{
		Phase:       promoterv1alpha1.CommitPhaseSuccess,
		Description: fmt.Sprintf("Tag %q at %s is hydrated", env.Tag, dqShortSha(env.TagSha)),
	}
	if len(env.DependsOn) == 0 {
		return gate, nil
	}
	gate.Description += " and qualified upstream"

	gate.Upstreams = make([]promoterv1alpha1.DependentsQualifiedCommitStatusUpstreamStatus, 0, len(env.DependsOn))
	for _, upstream := range env.DependsOn {
		newest := ""
		satisfied := false
		if watermark := byBranch[upstream].Qualified; watermark != nil {
			newest = watermark.DrySha
			satisfied, err = graph.Contains(ctx, watermark.DrySha, env.TagSha)
			if err != nil {
				return dqGate{}, fmt.Errorf("failed to check whether %q has qualified tag %q: %w", upstream, env.Tag, err)
			}
		}
		if satisfied {
			gate.Upstreams = append(gate.Upstreams, promoterv1alpha1.DependentsQualifiedCommitStatusUpstreamStatus{Branch: upstream, Satisfied: true})
			continue
		}
		reason := fmt.Sprintf("Waiting for %q to qualify dry %s (newest qualified: %s)", upstream, dqShortSha(env.TagSha), dqShortShaOrNone(newest))
		gate.Upstreams = append(gate.Upstreams, promoterv1alpha1.DependentsQualifiedCommitStatusUpstreamStatus{Branch: upstream, Reason: reason})
		if gate.Phase != promoterv1alpha1.CommitPhasePending {
			gate.Phase = promoterv1alpha1.CommitPhasePending
			gate.Description = reason
		}
	}
	return gate, nil
}

// dqNeedsNudge reports whether the environment's ChangeTransferPolicy should be woken to look at the proposed
// branch again: its tag points at a commit the hydrator has not reported yet.
func dqNeedsNudge(env dqEnvironment) bool {
	return env.TagSha != "" && getEffectiveHydratedDrySha(env.Status) != env.TagSha
}

// dqBackoff spaces out nudges for one (key, target) pair: immediately, then after base, doubling up to max.
// A new target for a key starts over.
type dqBackoff struct {
	entries map[string]dqBackoffEntry
	base    time.Duration
	max     time.Duration
	mu      sync.Mutex
}

type dqBackoffEntry struct {
	nextAt time.Time
	target string
	delay  time.Duration
}

func newDQBackoff(base, maxDelay time.Duration) *dqBackoff {
	return &dqBackoff{entries: map[string]dqBackoffEntry{}, base: base, max: maxDelay}
}

// next reports whether to act now for key, and how long to wait before asking again.
func (b *dqBackoff) next(key, target string, now time.Time) (fire bool, wait time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry, ok := b.entries[key]
	if !ok || entry.target != target {
		b.entries[key] = dqBackoffEntry{target: target, delay: min(2*b.base, b.max), nextAt: now.Add(b.base)}
		return true, b.base
	}
	if now.Before(entry.nextAt) {
		return false, entry.nextAt.Sub(now)
	}
	wait = entry.delay
	entry.nextAt = now.Add(wait)
	entry.delay = min(2*wait, b.max)
	b.entries[key] = entry
	return true, wait
}

// forget drops the state for key, so the next call to next fires immediately.
func (b *dqBackoff) forget(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.entries, key)
}

// dqValidateEnvironments checks that spec.environments has exactly one entry per PromotionStrategy
// environment. (The CRD already guarantees the entries have distinct branches and distinct tags.)
func dqValidateEnvironments(specEnvs []promoterv1alpha1.DependentsQualifiedEnvironment, branches []string) error {
	declared := make(map[string]struct{}, len(specEnvs))
	for _, env := range specEnvs {
		declared[env.Branch] = struct{}{}
	}
	known := make(map[string]struct{}, len(branches))
	for _, branch := range branches {
		known[branch] = struct{}{}
		if _, ok := declared[branch]; !ok {
			return fmt.Errorf("spec.environments has no entry for PromotionStrategy environment %q", branch)
		}
	}
	for _, env := range specEnvs {
		if _, ok := known[env.Branch]; !ok {
			return fmt.Errorf("spec.environments lists %q, which is not an environment of the PromotionStrategy", env.Branch)
		}
	}
	return nil
}

// dqNoteKey is the key of an environment's entry in a qualification note. Environments that share an active
// branch (each with its own activePath) annotate the same commits, so the path is part of the key.
func dqNoteKey(branch, activePath string) string {
	if activePath == "" {
		return branch
	}
	return branch + ":" + activePath
}

// dqRestored reports whether the commit that is live on branch was put there by a RestoreActiveCommit.
func dqRestored(racs []promoterv1alpha1.RestoreActiveCommit, branch, activeHydratedSha string) bool {
	if activeHydratedSha == "" {
		return false
	}
	for _, rac := range racs {
		if rac.Spec.Branch == branch && rac.Status.ActiveSha == activeHydratedSha {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------------------------------------
// Reconciler.
// ---------------------------------------------------------------------------------------------------------

// DependentsQualifiedCommitStatusReconciler reconciles a DependentsQualifiedCommitStatus object
type DependentsQualifiedCommitStatusReconciler struct {
	client.Client
	Recorder    events.EventRecorder
	Scheme      *runtime.Scheme
	SettingsMgr *settings.Manager

	// EnqueueCTP wakes a ChangeTransferPolicy so it looks at its proposed branch again. Optional.
	EnqueueCTP CTPEnqueueFunc

	// enqueueFunc requests a reconcile of a gate from outside the controller (the webhook receiver).
	enqueueFunc func(namespace, name string)

	nudges     *dqBackoff
	nudgesOnce sync.Once
}

// GetEnqueueFunc returns a function that requests a reconcile of a DependentsQualifiedCommitStatus. The
// webhook receiver uses it when the dry branch or an environment tag is pushed. It should be called after
// SetupWithManager.
func (r *DependentsQualifiedCommitStatusReconciler) GetEnqueueFunc() func(namespace, name string) {
	return r.enqueueFunc
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=dependentsqualifiedcommitstatuses,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=dependentsqualifiedcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=dependentsqualifiedcommitstatuses/finalizers,verbs=update
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=restoreactivecommits,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=clusterscmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/reconcile
//
//nolint:dupl // Ordering gate controllers share the same Reconcile skeleton by design.
func (r *DependentsQualifiedCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := logf.FromContext(ctx)
	logger.Info("Reconciling DependentsQualifiedCommitStatus")
	startTime := time.Now()

	var dqcs promoterv1alpha1.DependentsQualifiedCommitStatus
	// This applies the resource status via Server-Side Apply at the end of reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &dqcs, r.Client, r.Recorder, constants.DependentsQualifiedCommitStatusControllerFieldOwner, &result, &err, &previousReady)

	// 1. Fetch the DependentsQualifiedCommitStatus instance.
	if err = r.Get(ctx, req.NamespacedName, &dqcs); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("DependentsQualifiedCommitStatus not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get DependentsQualifiedCommitStatus %q: %w", req.Name, err)
	}

	// Start fresh on the Ready condition each reconcile.
	previousReady = utils.RemoveReadyCondition(&dqcs)

	// 2. Fetch the referenced PromotionStrategy.
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{Namespace: dqcs.Namespace, Name: dqcs.Spec.PromotionStrategyRef.Name}
	if err = r.Get(ctx, psKey, &ps); err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", dqcs.Spec.PromotionStrategyRef.Name, err)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", dqcs.Spec.PromotionStrategyRef.Name, err)
	}

	// 3. Advance tags, record qualifications, and write the gate for every environment.
	retryAfter, err := r.reconcileEnvironments(ctx, &dqcs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile environment tags: %w", err)
	}

	// 4. Requeue using the configured requeue duration, or sooner when something is waiting on a retry.
	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.DependentsQualifiedCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration for DependentsQualifiedCommitStatus %q: %w", dqcs.Name, err)
	}
	if retryAfter > 0 && retryAfter < requeueDuration {
		requeueDuration = retryAfter
	}

	return ctrl.Result{RequeueAfter: requeueDuration}, nil
}

// dqRepositories opens the git clones one reconcile needs, at most once each. The dry repository and the
// PromotionStrategy's (hydrated) repository are usually the same GitRepository and then share a clone.
type dqRepositories struct {
	reconciler *DependentsQualifiedCommitStatusReconciler
	dqcs       *promoterv1alpha1.DependentsQualifiedCommitStatus
	ps         *promoterv1alpha1.PromotionStrategy
	opened     map[string]*git.EnvironmentOperations
	// notesFetched is true once the qualification notes of the hydrated repository were fetched.
	notesFetched bool
}

func (q *dqRepositories) dryRepositoryName() string {
	if ref := q.dqcs.Spec.DrySource.GitRepositoryRef; ref != nil && ref.Name != "" {
		return ref.Name
	}
	return q.ps.Spec.RepositoryReference.Name
}

// dry returns the clone that holds the dry branch and the environment tags.
func (q *dqRepositories) dry(ctx context.Context) (*git.EnvironmentOperations, error) {
	return q.open(ctx, q.dryRepositoryName())
}

// hydrated returns the clone that holds the environment branches.
func (q *dqRepositories) hydrated(ctx context.Context) (*git.EnvironmentOperations, error) {
	return q.open(ctx, q.ps.Spec.RepositoryReference.Name)
}

func (q *dqRepositories) open(ctx context.Context, repositoryName string) (*git.EnvironmentOperations, error) {
	if ops, ok := q.opened[repositoryName]; ok {
		return ops, nil
	}
	r := q.reconciler
	repoRef := promoterv1alpha1.ObjectReference{Name: repositoryName}
	repoKey := client.ObjectKey{Namespace: q.dqcs.Namespace, Name: repositoryName}

	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, r.SettingsMgr, repoRef, q.dqcs)
	if err != nil {
		return nil, fmt.Errorf("failed to get ScmProvider and secret for GitRepository %q: %w", repositoryName, err)
	}
	gitAuthProvider, err := gitauth.CreateGitOperationsProvider(ctx, r.Client, scmProvider, secret, repoKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create git operations provider for GitRepository %q: %w", repositoryName, err)
	}
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, repoKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository %q: %w", repositoryName, err)
	}

	// The clone on disk is keyed by this identity. controller-runtime never reconciles one gate
	// concurrently with itself, so a per-gate identity gives each clone a single writer.
	ops := git.NewEnvironmentOperations(gitRepo, gitAuthProvider, q.dqcs.Namespace+"/"+q.dqcs.Name+"/"+repositoryName)
	if err := ops.CloneRepo(ctx); err != nil {
		return nil, fmt.Errorf("failed to clone GitRepository %q: %w", repositoryName, err)
	}
	if q.opened == nil {
		q.opened = map[string]*git.EnvironmentOperations{}
	}
	q.opened[repositoryName] = ops
	return ops, nil
}

// dqGitGraph answers ancestry questions from the dry clone.
type dqGitGraph struct {
	ops *git.EnvironmentOperations
}

func (g dqGitGraph) Contains(ctx context.Context, descendant, ancestor string) (bool, error) {
	contains, err := g.ops.CommitIsAncestorOrSame(ctx, ancestor, descendant)
	if err != nil {
		return false, fmt.Errorf("failed to check whether %s contains %s: %w", descendant, ancestor, err)
	}
	return contains, nil
}

func (g dqGitGraph) MergeBase(ctx context.Context, shas ...string) (string, error) {
	base, err := g.ops.MergeBase(ctx, shas...)
	if err != nil {
		return "", fmt.Errorf("failed to find merge base: %w", err)
	}
	return base, nil
}

// dqDryState is what the gate read from the dry repository.
type dqDryState struct {
	ops *git.EnvironmentOperations
	// tags maps each environment tag to the commit it points at, "" when the tag does not exist.
	tags map[string]string
	// head is the HEAD of the dry branch.
	head string
}

// readDryRepository reads the dry branch HEAD and every environment tag from the remote.
func (r *DependentsQualifiedCommitStatusReconciler) readDryRepository(ctx context.Context, dqcs *promoterv1alpha1.DependentsQualifiedCommitStatus, repos *dqRepositories) (*dqDryState, error) {
	ops, err := repos.dry(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open the dry repository: %w", err)
	}

	lastKnownHead := ""
	if dqcs.Status.Dry != nil {
		lastKnownHead = dqcs.Status.Dry.Sha
	}
	branch := dqcs.Spec.DrySource.Branch
	head, err := ops.GetBranchSha(ctx, branch, lastKnownHead)
	if err != nil {
		return nil, fmt.Errorf("failed to read dry branch %q: %w", branch, err)
	}

	names := make([]string, 0, len(dqcs.Spec.Environments))
	for _, env := range dqcs.Spec.Environments {
		names = append(names, env.Tag)
	}
	// Tags are read from the remote on every reconcile: a person may move one at any time, and not every
	// SCM tells us when they do.
	tags, err := ops.FetchTags(ctx, names...)
	if err != nil {
		return nil, fmt.Errorf("failed to read environment tags: %w", err)
	}
	return &dqDryState{ops: ops, head: head, tags: tags}, nil
}

// reconcileEnvironments does the work of one reconcile. It returns how soon to reconcile again when
// something is waiting on a retry (0 when nothing is), and an error for failures that should surface as
// Ready=False with reason ReconciliationError.
func (r *DependentsQualifiedCommitStatusReconciler) reconcileEnvironments(ctx context.Context, dqcs *promoterv1alpha1.DependentsQualifiedCommitStatus, ps *promoterv1alpha1.PromotionStrategy) (time.Duration, error) {
	logger := logf.FromContext(ctx)

	environments, err := resolveDependentEnvironments(ps)
	if err != nil {
		return 0, err
	}
	dependencies, err := buildDAG(environments)
	if err != nil {
		return 0, fmt.Errorf("failed to build dependency graph: %w", err)
	}
	if err := dependencies.validateDAG(); err != nil {
		return 0, fmt.Errorf("invalid dependency graph: %w", err)
	}
	if err := dqValidateEnvironments(dqcs.Spec.Environments, dependencies.branches); err != nil {
		return 0, err
	}

	repos := &dqRepositories{reconciler: r, dqcs: dqcs, ps: ps}
	dry, err := r.readDryRepository(ctx, dqcs, repos)
	if err != nil {
		// Without the dry repository nothing can be decided. Leave the tags, the child CommitStatuses, and
		// the last reported status alone, and say why on the Ready condition.
		logger.Info("Dry repository is unavailable", "error", err.Error())
		dqSetNotReady(dqcs, promoterConditions.DryRepositoryUnavailable, err.Error())
		return dqSoftRetry, nil
	}
	graph := dqGitGraph{ops: dry.ops}

	// Observe.
	envs, err := r.observeEnvironments(ctx, dqcs, ps, dependencies, dry, repos)
	if err != nil {
		return 0, err
	}

	// Qualify. A failure to persist a qualification does not stop the rest of the reconcile; it is returned
	// at the end so the status still reflects everything else.
	activePaths := dqActivePaths(ps)
	var qualifyErrs []error
	now := metav1.Now()
	for i := range envs {
		env := &envs[i]
		qualified, err := dqQualify(ctx, graph, dry.head, *env, now)
		if err != nil {
			return 0, err
		}
		if qualified != nil && qualified != env.Qualified {
			if err := r.recordQualification(ctx, repos, *env, activePaths[env.Branch], qualified); err != nil {
				// The note is the durable record, so do not act on a qualification that was not written.
				// Keep what was qualified before (if it is still on the dry branch) and try again later.
				qualifyErrs = append(qualifyErrs, err)
				qualified, err = dqStillQualified(ctx, graph, dry.head, *env)
				if err != nil {
					return 0, err
				}
			} else {
				logger.Info("Environment qualified a dry commit", "branch", env.Branch, "drySha", qualified.DrySha, "restored", env.Restored)
				r.Recorder.Eventf(dqcs, nil, "Normal", constants.EnvironmentQualifiedReason, "QualifyingEnvironment",
					constants.EnvironmentQualifiedMessage, env.Branch, qualified.DrySha)
			}
		}
		env.Qualified = qualified
	}

	// Move tags.
	dqcs.Status.Dry = &promoterv1alpha1.DependentsQualifiedDryStatus{Sha: dry.head}
	planEnvs, err := r.withLiveDryShasFromGit(ctx, repos, envs, activePaths)
	if err != nil {
		return 0, err
	}
	moves, err := dqPlanTags(ctx, graph, dry.head, planEnvs)
	if err != nil {
		return 0, err
	}
	pushErrs := r.pushTags(ctx, dqcs, dry.ops, moves, envs)

	// Gate.
	commitStatuses, err := r.writeGates(ctx, dqcs, ps, graph, dry.head, envs)
	if err != nil {
		return 0, err
	}
	if err := utils.CleanupOrphanedCommitStatuses(ctx, r.Client, r.Recorder, dqcs, commitStatuses); err != nil {
		return 0, fmt.Errorf("failed to cleanup orphaned CommitStatus resources: %w", err)
	}
	utils.InheritNotReadyConditionFromObjects(dqcs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	retryAfter := r.nudgeHydration(ctx, dqcs, ps, envs)

	if len(pushErrs) > 0 {
		dqSetNotReady(dqcs, promoterConditions.TagPushFailed, errors.Join(pushErrs...).Error())
		retryAfter = dqSooner(retryAfter, dqSoftRetry)
	}
	if len(qualifyErrs) > 0 {
		return 0, errors.Join(qualifyErrs...)
	}
	return retryAfter, nil
}

// dqSooner returns the shorter of two waits, where 0 means "no wait requested".
func dqSooner(a, b time.Duration) time.Duration {
	if a == 0 || (b != 0 && b < a) {
		return b
	}
	return a
}

// dqSetNotReady sets Ready=False with the given reason. HandleReconciliationResult keeps a Ready condition
// the reconciler set itself as long as the reconcile returns no error.
func dqSetNotReady(dqcs *promoterv1alpha1.DependentsQualifiedCommitStatus, reason promoterConditions.CommonReason, message string) {
	meta.SetStatusCondition(dqcs.GetConditions(), metav1.Condition{
		Type:               string(promoterConditions.Ready),
		Status:             metav1.ConditionFalse,
		Reason:             string(reason),
		Message:            message,
		ObservedGeneration: dqcs.Generation,
	})
}

// dqActivePaths returns the effective activePath of every environment of a PromotionStrategy, by branch.
func dqActivePaths(ps *promoterv1alpha1.PromotionStrategy) map[string]string {
	paths := make(map[string]string, len(ps.Spec.Environments))
	for _, environment := range ps.Spec.Environments {
		activePath := ps.Spec.ActivePath
		if environment.ActivePath != "" {
			activePath = environment.ActivePath
		}
		paths[environment.Branch] = activePath
	}
	return paths
}

// dqStillQualified returns the environment's previous watermark if it is still on the dry branch.
func dqStillQualified(ctx context.Context, graph dqCommitGraph, head string, env dqEnvironment) (*promoterv1alpha1.DependentsQualifiedWatermark, error) {
	if env.Qualified == nil {
		return nil, nil
	}
	onBranch, err := graph.Contains(ctx, head, env.Qualified.DrySha)
	if err != nil {
		return nil, fmt.Errorf("failed to check whether %q's qualified commit is on the dry branch: %w", env.Branch, err)
	}
	if !onBranch {
		return nil, nil
	}
	return env.Qualified, nil
}

// observeEnvironments gathers what the decisions need for every environment, in PromotionStrategy order.
func (r *DependentsQualifiedCommitStatusReconciler) observeEnvironments(
	ctx context.Context,
	dqcs *promoterv1alpha1.DependentsQualifiedCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	dependencies *dag,
	dry *dqDryState,
	repos *dqRepositories,
) ([]dqEnvironment, error) {
	specByBranch := make(map[string]promoterv1alpha1.DependentsQualifiedEnvironment, len(dqcs.Spec.Environments))
	for _, env := range dqcs.Spec.Environments {
		specByBranch[env.Branch] = env
	}
	statusByBranch := make(map[string]promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for _, envStatus := range ps.Status.Environments {
		statusByBranch[envStatus.Branch] = envStatus
	}
	previousByBranch := make(map[string]promoterv1alpha1.DependentsQualifiedCommitStatusEnvironmentStatus, len(dqcs.Status.Environments))
	for _, previous := range dqcs.Status.Environments {
		previousByBranch[previous.Branch] = previous
	}
	activePaths := dqActivePaths(ps)

	var racList promoterv1alpha1.RestoreActiveCommitList
	if err := r.List(ctx, &racList, client.InNamespace(dqcs.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list RestoreActiveCommits: %w", err)
	}
	racs := make([]promoterv1alpha1.RestoreActiveCommit, 0, len(racList.Items))
	for _, rac := range racList.Items {
		if rac.Spec.PromotionStrategyRef.Name == ps.Name {
			racs = append(racs, rac)
		}
	}

	envs := make([]dqEnvironment, 0, len(dependencies.branches))
	for _, branch := range dependencies.branches {
		spec := specByBranch[branch]
		status := statusByBranch[branch]
		activePath := activePaths[branch]

		env := dqEnvironment{
			Branch:    branch,
			Tag:       spec.Tag,
			TagSha:    dry.tags[spec.Tag],
			Manual:    spec.Advance == promoterv1alpha1.TagAdvanceManual,
			DependsOn: dependencies.dependsOn[branch],
			Status:    status,
			Restored:  dqRestored(racs, branch, status.Active.Hydrated.Sha),
		}

		live, err := dqLiveDrySha(status, func() (bool, error) {
			return r.sameManifests(ctx, repos, branch, activePath, status)
		})
		if err != nil {
			return nil, err
		}
		env.LiveDrySha = live

		if previous, ok := previousByBranch[branch]; ok {
			env.Qualified = previous.Qualified
		} else if status.Active.Hydrated.Sha != "" {
			// Nothing in status to start from (a new gate, or status was lost): rebuild from the notes.
			env.Qualified, err = r.loadQualification(ctx, repos, branch, activePath)
			if err != nil {
				return nil, err
			}
		}
		envs = append(envs, env)
	}
	return envs, nil
}

// withLiveDryShasFromGit returns envs with the live dry SHA filled in from git for every environment whose
// tag is missing and whose active commit the PromotionStrategy has not reported yet.
//
// Where a missing tag is created depends on what is live in the environment, and the gate cannot wait for the
// PromotionStrategy to say: on a new setup the hydrator needs the tag before it can create the proposed
// branch, and the ChangeTransferPolicy reports nothing until that branch exists. So the gate reads the active
// branch itself.
//
// The result is only for planning tags. What an environment qualifies is decided from the commit statuses the
// PromotionStrategy reports, never from this reading, so envs itself is left alone.
func (r *DependentsQualifiedCommitStatusReconciler) withLiveDryShasFromGit(ctx context.Context, repos *dqRepositories, envs []dqEnvironment, activePaths map[string]string) ([]dqEnvironment, error) {
	planned := make([]dqEnvironment, len(envs))
	copy(planned, envs)
	for i, env := range envs {
		if env.TagSha != "" || env.Status.Active.Hydrated.Sha != "" {
			continue
		}
		live, err := r.liveDryShaFromGit(ctx, repos, env.Branch, activePaths[env.Branch])
		if err != nil {
			return nil, err
		}
		if live != "" {
			planned[i].LiveDrySha = live
		}
	}
	return planned, nil
}

// liveDryShaFromGit returns the dry SHA recorded in hydrator.metadata at the tip of an environment's active
// branch, or "" when the branch does not exist or its tip has no metadata.
func (r *DependentsQualifiedCommitStatusReconciler) liveDryShaFromGit(ctx context.Context, repos *dqRepositories, branch, activePath string) (string, error) {
	ops, err := repos.hydrated(ctx)
	if err != nil {
		return "", err
	}
	tip, err := ops.GetBranchSha(ctx, branch, "")
	if err != nil {
		if strings.Contains(err.Error(), "couldn't find remote ref") {
			// The environment has never been promoted to, so nothing is live in it.
			return "", nil
		}
		return "", fmt.Errorf("failed to read active branch %q: %w", branch, err)
	}
	metadata, err := ops.GetShaMetadataFromFile(ctx, tip, activePath)
	if err != nil {
		return "", fmt.Errorf("failed to read what is live on active branch %q: %w", branch, err)
	}
	return metadata.Sha, nil
}

// sameManifests reports whether the environment's proposed tip has the same content as what is live on its
// active branch.
func (r *DependentsQualifiedCommitStatusReconciler) sameManifests(ctx context.Context, repos *dqRepositories, branch, activePath string, status promoterv1alpha1.EnvironmentStatus) (bool, error) {
	proposed, active := status.Proposed.Hydrated.Sha, status.Active.Hydrated.Sha
	if proposed == "" || active == "" {
		return false, nil
	}
	if proposed == active {
		return true, nil
	}
	ops, err := repos.hydrated(ctx)
	if err != nil {
		return false, err
	}

	proposedBranch := branch + "-next"
	if activePath != "" {
		proposedBranch = path.Join(proposedBranch, activePath)
	}
	for sha, from := range map[string]string{proposed: proposedBranch, active: branch} {
		if ops.CommitExists(ctx, sha) {
			continue
		}
		if err := ops.FetchBranch(ctx, from); err != nil {
			return false, fmt.Errorf("failed to fetch branch %q: %w", from, err)
		}
		if !ops.CommitExists(ctx, sha) {
			// The branch moved on since the PromotionStrategy reported; the next reconcile sees the new state.
			logf.FromContext(ctx).V(4).Info("Hydrated commit is not on its branch, not comparing manifests", "branch", from, "sha", sha)
			return false, nil
		}
	}

	proposedTree, err := ops.TreeSha(ctx, proposed, activePath)
	if err != nil {
		return false, fmt.Errorf("failed to read the proposed tree of %q: %w", branch, err)
	}
	activeTree, err := ops.TreeSha(ctx, active, activePath)
	if err != nil {
		return false, fmt.Errorf("failed to read the active tree of %q: %w", branch, err)
	}
	return proposedTree != "" && proposedTree == activeTree, nil
}

// loadQualification reads the environment's newest qualification from the notes on its active branch. It
// returns nil when there is none.
func (r *DependentsQualifiedCommitStatusReconciler) loadQualification(ctx context.Context, repos *dqRepositories, branch, activePath string) (*promoterv1alpha1.DependentsQualifiedWatermark, error) {
	ops, err := repos.hydrated(ctx)
	if err != nil {
		return nil, err
	}
	if err := ops.FetchBranch(ctx, branch); err != nil {
		return nil, fmt.Errorf("failed to fetch active branch %q: %w", branch, err)
	}
	if !repos.notesFetched {
		if err := ops.FetchQualifiedNotes(ctx); err != nil {
			return nil, fmt.Errorf("failed to fetch qualification notes: %w", err)
		}
		repos.notesFetched = true
	}
	entry, hydratedSha, err := ops.FindNewestQualifiedNoteEntry(ctx, "origin/"+branch, dqNoteKey(branch, activePath), dqNoteSearchDepth)
	if err != nil {
		return nil, fmt.Errorf("failed to read qualification notes of %q: %w", branch, err)
	}
	if entry == nil || entry.DrySha == "" {
		return nil, nil
	}
	return &promoterv1alpha1.DependentsQualifiedWatermark{
		QualifiedAt: entry.QualifiedAt,
		DrySha:      entry.DrySha,
		HydratedSha: hydratedSha,
	}, nil
}

// recordQualification writes the environment's qualification to a note on the active commit that was live.
func (r *DependentsQualifiedCommitStatusReconciler) recordQualification(ctx context.Context, repos *dqRepositories, env dqEnvironment, activePath string, watermark *promoterv1alpha1.DependentsQualifiedWatermark) error {
	if watermark.HydratedSha == "" {
		// Nothing to annotate. A live dry SHA without an active hydrated commit is not something a
		// ChangeTransferPolicy reports; status alone carries the watermark if it ever does.
		return nil
	}
	ops, err := repos.hydrated(ctx)
	if err != nil {
		return err
	}
	if !ops.CommitExists(ctx, watermark.HydratedSha) {
		if err := ops.FetchBranch(ctx, env.Branch); err != nil {
			return fmt.Errorf("failed to fetch active branch %q: %w", env.Branch, err)
		}
	}
	entry := git.QualifiedNoteEntry{
		QualifiedAt:    watermark.QualifiedAt,
		DrySha:         watermark.DrySha,
		CommitStatuses: make([]git.QualifiedNoteCommitStatus, 0, len(env.Status.Active.CommitStatuses)),
	}
	for _, commitStatus := range env.Status.Active.CommitStatuses {
		entry.CommitStatuses = append(entry.CommitStatuses, git.QualifiedNoteCommitStatus{Key: commitStatus.Key, Phase: commitStatus.Phase})
	}
	if err := ops.SetQualifiedNoteEntry(ctx, watermark.HydratedSha, dqNoteKey(env.Branch, activePath), entry); err != nil {
		return fmt.Errorf("failed to record that %q qualified dry %s: %w", env.Branch, watermark.DrySha, err)
	}
	return nil
}

// pushTags carries out the planned tag moves and updates envs with the tags' new positions. A failed push
// does not stop the others; the failures are returned.
func (r *DependentsQualifiedCommitStatusReconciler) pushTags(
	ctx context.Context,
	dqcs *promoterv1alpha1.DependentsQualifiedCommitStatus,
	ops *git.EnvironmentOperations,
	moves []dqTagMove,
	envs []dqEnvironment,
) []error {
	logger := logf.FromContext(ctx)
	indexByBranch := make(map[string]int, len(envs))
	for i, env := range envs {
		indexByBranch[env.Branch] = i
	}

	var errs []error
	for _, move := range moves {
		// Tags are pushed one at a time: GitHub sends no push event when more than three tags are pushed
		// at once, and one rejected tag must not hold back the others.
		if err := ops.PushTag(ctx, move.Tag, move.To); err != nil {
			logger.Error(err, "Failed to push environment tag", "branch", move.Branch, "tag", move.Tag, "to", move.To)
			r.Recorder.Eventf(dqcs, nil, "Warning", constants.EnvironmentTagPushFailedReason, "AdvancingTag",
				constants.EnvironmentTagPushFailedMessage, move.Tag, move.Branch, err)
			errs = append(errs, err)
			continue
		}
		envs[indexByBranch[move.Branch]].TagSha = move.To

		from := move.From
		if from == "" {
			from = constants.EnvironmentTagNoPreviousSha
		}
		logger.Info("Moved environment tag", "branch", move.Branch, "tag", move.Tag, "from", from, "to", move.To)
		r.Recorder.Eventf(dqcs, nil, "Normal", constants.EnvironmentTagMovedReason, "AdvancingTag",
			constants.EnvironmentTagMovedMessage, move.Tag, move.Branch, from, move.To)
	}
	return errs
}

// writeGates writes status.environments and the child CommitStatus of every environment that has a proposed
// change. It returns the child CommitStatuses that are still wanted.
func (r *DependentsQualifiedCommitStatusReconciler) writeGates(
	ctx context.Context,
	dqcs *promoterv1alpha1.DependentsQualifiedCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	graph dqCommitGraph,
	head string,
	envs []dqEnvironment,
) ([]*promoterv1alpha1.CommitStatus, error) {
	logger := logf.FromContext(ctx)
	byBranch := make(map[string]dqEnvironment, len(envs))
	for _, env := range envs {
		byBranch[env.Branch] = env
	}

	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(envs))
	dqcs.Status.Environments = make([]promoterv1alpha1.DependentsQualifiedCommitStatusEnvironmentStatus, 0, len(envs))
	for _, env := range envs {
		entry := promoterv1alpha1.DependentsQualifiedCommitStatusEnvironmentStatus{
			Branch:               env.Branch,
			Tag:                  &promoterv1alpha1.DependentsQualifiedTagStatus{Name: env.Tag, Sha: env.TagSha},
			Qualified:            env.Qualified,
			ActiveCommitStatuses: env.Status.Active.CommitStatuses,
		}

		// With no proposed change (active and proposed dry SHAs match) there is no promotion to gate.
		// Keep any existing CommitStatus so orphan cleanup leaves the last report alone, and mirror it
		// onto status so operators still see it after the promotion completes.
		proposedHydratedSha := env.Status.Proposed.Hydrated.Sha
		if env.Status.Active.Dry.Sha == env.Status.Proposed.Dry.Sha || proposedHydratedSha == "" {
			logger.V(4).Info("Skipping environment with no proposed change", "branch", env.Branch)
			existing := &promoterv1alpha1.CommitStatus{}
			name := utils.CommitStatusResourceName(ctx, dqcs, env.Branch)
			if err := r.Get(ctx, client.ObjectKey{Namespace: dqcs.Namespace, Name: name}, existing); err != nil {
				if !k8serrors.IsNotFound(err) {
					return nil, fmt.Errorf("failed to get existing CommitStatus for branch %q: %w", env.Branch, err)
				}
			} else {
				commitStatuses = append(commitStatuses, existing)
				entry.GateEnvironmentCommitStatus = utils.GateEnvironmentCommitStatusFromCommitStatus(existing)
			}
			dqcs.Status.Environments = append(dqcs.Status.Environments, entry)
			continue
		}

		gate, err := dqEvaluateGate(ctx, graph, head, dqcs.Spec.DrySource.Branch, env, byBranch)
		if err != nil {
			return nil, err
		}
		logger.V(4).Info("Evaluated gate for environment",
			"branch", env.Branch,
			"tag", env.Tag,
			"tagSha", env.TagSha,
			"phase", gate.Phase,
			"description", gate.Description)

		// Bind the CommitStatus to the proposed branch's hydrated SHA: that is the commit the
		// ChangeTransferPolicy inspects when gating the promotion PR.
		commitStatus, report, err := r.applyCommitStatus(ctx, dqcs, ps, env, gate, proposedHydratedSha)
		if err != nil {
			return nil, fmt.Errorf("failed to set commit status for branch %q: %w", env.Branch, err)
		}
		commitStatuses = append(commitStatuses, commitStatus)
		entry.GateEnvironmentCommitStatus = report
		entry.Upstreams = gate.Upstreams
		dqcs.Status.Environments = append(dqcs.Status.Environments, entry)
	}
	return commitStatuses, nil
}

// applyCommitStatus applies the child CommitStatus that carries an environment's gate.
func (r *DependentsQualifiedCommitStatusReconciler) applyCommitStatus(
	ctx context.Context,
	dqcs *promoterv1alpha1.DependentsQualifiedCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	env dqEnvironment,
	gate dqGate,
	reportedSha string,
) (*promoterv1alpha1.CommitStatus, promoterv1alpha1.GateEnvironmentCommitStatus, error) {
	key := dqcs.Spec.Key
	commitStatusName := utils.CommitStatusResourceName(ctx, dqcs, env.Branch)

	kind := reflect.TypeFor[promoterv1alpha1.DependentsQualifiedCommitStatus]().Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)

	var renderedURL string
	if dqcs.Spec.URL.Template != "" {
		data := DependentsQualifiedURLTemplateData{
			Environment:                     env.Branch,
			DependentsQualifiedCommitStatus: *dqcs,
			PromotionStrategy:               ps,
			DependsOn:                       env.DependsOn,
			DependsOnQuery:                  buildDependsOnQuery(env.DependsOn),
		}
		var err error
		renderedURL, err = renderGateURL(dqcs.Spec.URL.Template, dqcs.Spec.URL.Options, data)
		if err != nil {
			return nil, promoterv1alpha1.GateEnvironmentCommitStatus{}, err
		}
	}

	// The stable gate key is the SCM commit status context (spec.name), so branch protection rules can
	// reference one predictable name. The per-environment detail goes in the description.
	commitStatusSpec := acv1alpha1.CommitStatusSpec().
		WithRepositoryReference(acv1alpha1.ObjectReference().
			WithName(ps.Spec.RepositoryReference.Name)).
		WithSha(reportedSha).
		WithName(key).
		WithDescription(gate.Description).
		WithPhase(gate.Phase)
	if dqcs.Spec.PublishTo != nil {
		commitStatusSpec = commitStatusSpec.WithPublishTo(*dqcs.Spec.PublishTo)
	}
	if renderedURL != "" {
		commitStatusSpec = commitStatusSpec.WithUrl(renderedURL)
	}

	commitStatusApply := acv1alpha1.CommitStatus(commitStatusName, dqcs.Namespace).
		WithLabels(utils.CommitStatusStandardLabels(dqcs, env.Branch, key)).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(dqcs.Name).
			WithUID(dqcs.UID).
			WithController(true).
			WithBlockOwnerDeletion(true)).
		WithSpec(commitStatusSpec)

	// Read the previously persisted phase (NotFound means this branch is being gated for the first time)
	// so the phase-change event below stays transition-only.
	previousPhase := ""
	existingCommitStatus := &promoterv1alpha1.CommitStatus{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: dqcs.Namespace, Name: commitStatusName}, existingCommitStatus); err == nil {
		previousPhase = string(existingCommitStatus.Spec.Phase)
	} else if !k8serrors.IsNotFound(err) {
		return nil, promoterv1alpha1.GateEnvironmentCommitStatus{}, fmt.Errorf("failed to get existing CommitStatus %q: %w", commitStatusName, err)
	}

	commitStatus := &promoterv1alpha1.CommitStatus{}
	commitStatus.Name = commitStatusName
	commitStatus.Namespace = dqcs.Namespace
	if err := r.Patch(ctx, commitStatus, utils.ApplyPatch{ApplyConfig: commitStatusApply}, client.FieldOwner(constants.DependentsQualifiedCommitStatusControllerFieldOwner), client.ForceOwnership); err != nil {
		return nil, promoterv1alpha1.GateEnvironmentCommitStatus{}, fmt.Errorf("failed to apply CommitStatus: %w", err)
	}

	emitCommitStatusPhaseChangedEvent(r.Recorder, dqcs, key, env.Branch, previousPhase, string(gate.Phase))

	return commitStatus, promoterv1alpha1.GateEnvironmentCommitStatus{
		Phase:       gate.Phase,
		Description: gate.Description,
		Url:         renderedURL,
		ReportedSha: reportedSha,
	}, nil
}

// nudgeHydration wakes the ChangeTransferPolicy of every environment whose hydrator has not reported its
// tag's commit yet, so the policy notices the hydrator's output even when no webhook announces it. It
// returns how soon the gate should reconcile again to nudge the next time, 0 when no environment is waiting.
func (r *DependentsQualifiedCommitStatusReconciler) nudgeHydration(ctx context.Context, dqcs *promoterv1alpha1.DependentsQualifiedCommitStatus, ps *promoterv1alpha1.PromotionStrategy, envs []dqEnvironment) time.Duration {
	if r.EnqueueCTP == nil {
		return 0
	}
	r.nudgesOnce.Do(func() {
		r.nudges = newDQBackoff(dqNudgeBaseDelay, dqNudgeMaxDelay)
	})

	var soonest time.Duration
	now := time.Now()
	for _, env := range envs {
		ctpName := utils.KubeSafeUniqueName(utils.GetChangeTransferPolicyName(ps.Name, env.Branch))
		// The gate's UID is part of the key so two gates (or a gate deleted and recreated) do not share state.
		key := string(dqcs.UID) + "/" + ctpName
		if !dqNeedsNudge(env) {
			r.nudges.forget(key)
			continue
		}
		fire, wait := r.nudges.next(key, env.TagSha, now)
		if fire {
			logf.FromContext(ctx).V(4).Info("Waking ChangeTransferPolicy to look for the hydrator's output", "changeTransferPolicy", ctpName, "tag", env.Tag, "tagSha", env.TagSha)
			r.EnqueueCTP(dqcs.Namespace, ctpName)
		}
		soonest = dqSooner(soonest, wait)
	}
	return soonest
}

// SetupWithManager sets up the controller with the Manager.
//
//nolint:dupl // Gate controllers share the same SetupWithManager skeleton by design.
func (r *DependentsQualifiedCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.DependentsQualifiedCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get DependentsQualifiedCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.DependentsQualifiedCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get DependentsQualifiedCommitStatus max concurrent reconciles: %w", err)
	}

	// The webhook receiver finds the gates to wake for a push by the dry SHAs they last observed.
	if err := mgr.GetFieldIndexer().IndexField(ctx, &promoterv1alpha1.DependentsQualifiedCommitStatus{}, promoterv1alpha1.DependentsQualifiedObservedDryShaField, func(rawObj client.Object) []string {
		dqcs, ok := rawObj.(*promoterv1alpha1.DependentsQualifiedCommitStatus)
		if !ok {
			return nil
		}
		return dqcs.ObservedDryShas()
	}); err != nil {
		return fmt.Errorf("failed to set field index for %s: %w", promoterv1alpha1.DependentsQualifiedObservedDryShaField, err)
	}

	externalEnqueueChan := make(chan event.GenericEvent, 1024)
	r.enqueueFunc = func(namespace, name string) {
		dqcs := &promoterv1alpha1.DependentsQualifiedCommitStatus{}
		dqcs.SetNamespace(namespace)
		dqcs.SetName(name)

		select {
		case externalEnqueueChan <- event.GenericEvent{Object: dqcs}:
		default:
			logf.FromContext(ctx).Info("DependentsQualifiedCommitStatus enqueue channel is full, blocking until space is available",
				"namespace", namespace, "name", name)
			externalEnqueueChan <- event.GenericEvent{Object: dqcs}
		}
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.DependentsQualifiedCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.PromotionStrategy{}, CommitStatusGatePromotionStrategyWatchHandler[promoterv1alpha1.DependentsQualifiedCommitStatusList](r.Client)).
		// A restore revokes qualifications, so look again when one appears or reports what it restored.
		Watches(&promoterv1alpha1.RestoreActiveCommit{}, handler.EnqueueRequestsFromMapFunc(r.gatesForRestoreActiveCommit)).
		WatchesRawSource(source.Channel(externalEnqueueChan, &handler.EnqueueRequestForObject{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Named("dependentsqualifiedcommitstatus").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// gatesForRestoreActiveCommit maps a RestoreActiveCommit to the gates that order its PromotionStrategy.
func (r *DependentsQualifiedCommitStatusReconciler) gatesForRestoreActiveCommit(ctx context.Context, obj client.Object) []reconcile.Request {
	rac, ok := obj.(*promoterv1alpha1.RestoreActiveCommit)
	if !ok {
		return nil
	}
	var gates promoterv1alpha1.DependentsQualifiedCommitStatusList
	if err := r.List(ctx, &gates, client.InNamespace(rac.Namespace), client.MatchingFields{PromotionStrategyRefField: rac.Spec.PromotionStrategyRef.Name}); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to list DependentsQualifiedCommitStatuses for RestoreActiveCommit", "restoreActiveCommit", rac.Name)
		return nil
	}
	requests := make([]reconcile.Request, 0, len(gates.Items))
	for i := range gates.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&gates.Items[i])})
	}
	return requests
}
