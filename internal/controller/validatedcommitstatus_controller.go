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
	"sigs.k8s.io/controller-runtime/pkg/source"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/gitauth"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/internal/validations"
)

const (
	// validatedRetryInterval is how soon the controller comes back to a tag it could not push, or a
	// validation it could not record. It is shorter than the usual requeue, and longer than a tight
	// loop against a tag the SCM refuses to move.
	validatedRetryInterval = time.Minute

	// validatedPollInterval is how often the controller looks again at the two things nothing tells it
	// about. One is a hydrator catching up with a tag: SCMs send no webhook when a hydrator pushes a git
	// note, so the environment's ChangeTransferPolicy is nudged at this spacing, and without the nudge a
	// hydration that changed nothing would wait for the next requeue. The other is an environment that
	// advances WhenIdle becoming idle.
	validatedPollInterval = 15 * time.Second

	// validatedNudgeWindow is how long after a tag moved the nudging goes on. A hydrator that has not
	// caught up by then is slow, broken, or not reading the tag; the usual requeue takes over.
	validatedNudgeWindow = 5 * time.Minute

	// validatedLostCursorWalk bounds how many commits of the validation notes are searched for the
	// validation that matches a tag when the controller has no record of how far it had applied them.
	validatedLostCursorWalk = 500

	// validatedMessageLimit bounds the git output copied into status.
	validatedMessageLimit = 1024

	// shortShaLength is how much of a SHA goes into text meant for people.
	shortShaLength = 7
)

// ValidatedCommitStatusURLTemplateData is the data passed to ValidatedCommitStatus.spec.url.template.
type ValidatedCommitStatusURLTemplateData struct {
	PromotionStrategy *promoterv1alpha1.PromotionStrategy
	Environment       string
	// Tag is the name of the environment's tag.
	Tag                   string
	ValidatedCommitStatus promoterv1alpha1.ValidatedCommitStatus
}

// ValidatedCommitStatusEnqueueFunc enqueues a reconcile of a ValidatedCommitStatus without changing the
// object. The webhook receiver uses it when the dry branch or an environment tag is pushed.
type ValidatedCommitStatusEnqueueFunc func(namespace, name string)

// ValidatedCommitStatusReconciler reconciles a ValidatedCommitStatus object
type ValidatedCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager

	// EnqueueCTP is a function to enqueue CTP reconcile requests without modifying the CTP object.
	EnqueueCTP CTPEnqueueFunc

	// enqueueFunc is set during SetupWithManager and can be retrieved via GetEnqueueFunc.
	enqueueFunc ValidatedCommitStatusEnqueueFunc

	// lastNudges records when each ChangeTransferPolicy was last nudged, so that reconciles triggered in
	// quick succession do not each nudge it. Protected by nudgeMutex.
	lastNudges map[client.ObjectKey]time.Time
	nudgeMutex sync.Mutex

	// pollInterval and retryInterval stand in for validatedPollInterval and validatedRetryInterval when
	// they are not zero. Tests shorten them.
	pollInterval  time.Duration
	retryInterval time.Duration
}

// intervalOr returns override when it is set, and otherwise fallback.
func intervalOr(override, fallback time.Duration) time.Duration {
	if override > 0 {
		return override
	}
	return fallback
}

// sooner returns the shorter of two waits, where zero means not waiting at all.
func sooner(a, b time.Duration) time.Duration {
	if a == 0 || (b != 0 && b < a) {
		return b
	}
	return a
}

// GetEnqueueFunc returns a function that enqueues ValidatedCommitStatus reconcile requests.
// This should be called after SetupWithManager has been called.
func (r *ValidatedCommitStatusReconciler) GetEnqueueFunc() ValidatedCommitStatusEnqueueFunc {
	return r.enqueueFunc
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=validatedcommitstatuses,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=validatedcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=validatedcommitstatuses/finalizers,verbs=update
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=clusterscmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile orders the environments of the referenced PromotionStrategy by moving one git tag per
// environment. Each environment's hydrator reads its own tag, and the tag moves only to a dry commit
// that was validated for that environment. On each environment's proposed hydrated commit the controller
// reports a commit status that passes when the commit was hydrated from the tag.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/reconcile
func (r *ValidatedCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := logf.FromContext(ctx)
	logger.Info("Reconciling ValidatedCommitStatus")
	startTime := time.Now()

	var vcs promoterv1alpha1.ValidatedCommitStatus
	// This applies the resource status via Server-Side Apply at the end of reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &vcs, r.Client, r.Recorder, constants.ValidatedCommitStatusControllerFieldOwner, &result, &err, &previousReady)

	if err = r.Get(ctx, req.NamespacedName, &vcs); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("ValidatedCommitStatus not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get ValidatedCommitStatus %q: %w", req.Name, err)
	}

	// Start fresh on the Ready condition each reconcile.
	previousReady = utils.RemoveReadyCondition(&vcs)

	if err := ensureControllerInstanceIDStable(ctx, r.SettingsMgr); err != nil {
		return ctrl.Result{}, err
	}

	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{Namespace: vcs.Namespace, Name: vcs.Spec.PromotionStrategyRef.Name}
	if err = r.Get(ctx, psKey, &ps); err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", vcs.Spec.PromotionStrategyRef.Name, err)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", vcs.Spec.PromotionStrategyRef.Name, err)
	}

	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.ValidatedCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration for ValidatedCommitStatus %q: %w", vcs.Name, err)
	}

	// Two gates moving the same tags would each take the other's moves for moves made by hand.
	owner, err := r.tagOwner(ctx, &vcs, &ps)
	if err != nil {
		return ctrl.Result{}, err
	}
	if owner != vcs.Name {
		meta.SetStatusCondition(vcs.GetConditions(), metav1.Condition{
			Type:               string(promoterConditions.Ready),
			Status:             metav1.ConditionFalse,
			Reason:             string(promoterConditions.EnvironmentTagsOwnedByAnotherGate),
			Message:            fmt.Sprintf("ValidatedCommitStatus %q already moves the environment tags of PromotionStrategy %q; only one may", owner, ps.Name),
			ObservedGeneration: vcs.Generation,
		})
		return ctrl.Result{RequeueAfter: requeueDuration}, nil
	}

	retryAfter, err := r.reconcileEnvironments(ctx, &vcs, &ps)
	if err != nil {
		return ctrl.Result{}, err
	}
	if retryAfter > 0 && retryAfter < requeueDuration {
		requeueDuration = retryAfter
	}
	return ctrl.Result{RequeueAfter: requeueDuration}, nil
}

// referencedValidatedCommitStatus returns the name of the ValidatedCommitStatus the PromotionStrategy
// uses as its ordering gate, "" when its ordering gate is of another kind.
func referencedValidatedCommitStatus(ps *promoterv1alpha1.PromotionStrategy) string {
	ref := ps.Spec.OrderCommitStatusRef.WithDefaults()
	if ref.Group != promoterv1alpha1.GroupVersion.Group || ref.Kind != promoterv1alpha1.ValidatedOrderCommitStatusKind {
		return ""
	}
	return ref.Name
}

// validatedCommitStatusFor returns the one ValidatedCommitStatus that moves the environment tags of ps:
// the one the strategy references as its ordering gate, or else the oldest that references the strategy.
// It returns nil when no ValidatedCommitStatus references the strategy.
func validatedCommitStatusFor(ctx context.Context, c client.Client, ps *promoterv1alpha1.PromotionStrategy) (*promoterv1alpha1.ValidatedCommitStatus, error) {
	var gates promoterv1alpha1.ValidatedCommitStatusList
	if err := c.List(ctx, &gates, client.InNamespace(ps.Namespace), client.MatchingFields{PromotionStrategyRefField: ps.Name}); err != nil {
		return nil, fmt.Errorf("failed to list ValidatedCommitStatuses for PromotionStrategy %q: %w", ps.Name, err)
	}

	referenced := referencedValidatedCommitStatus(ps)
	var oldest *promoterv1alpha1.ValidatedCommitStatus
	for i := range gates.Items {
		gate := &gates.Items[i]
		if gate.Name == referenced {
			return gate, nil
		}
		if oldest == nil || gate.CreationTimestamp.Before(&oldest.CreationTimestamp) ||
			(gate.CreationTimestamp.Equal(&oldest.CreationTimestamp) && gate.Name < oldest.Name) {
			oldest = gate
		}
	}
	return oldest, nil
}

// tagOwner returns the name of the one ValidatedCommitStatus that moves the environment tags of ps.
func (r *ValidatedCommitStatusReconciler) tagOwner(ctx context.Context, vcs *promoterv1alpha1.ValidatedCommitStatus, ps *promoterv1alpha1.PromotionStrategy) (string, error) {
	owner, err := validatedCommitStatusFor(ctx, r.Client, ps)
	if err != nil {
		return "", err
	}
	if owner == nil {
		// The list is read from a cache that has not caught up with this gate yet.
		return vcs.Name, nil
	}
	return owner.Name, nil
}

// environmentActivePaths returns the activePath of every environment of the PromotionStrategy, by
// branch: the environment's own, or else the strategy's. An environment with neither maps to "".
func environmentActivePaths(ps *promoterv1alpha1.PromotionStrategy) map[string]string {
	activePaths := make(map[string]string, len(ps.Spec.Environments))
	for _, environment := range ps.Spec.Environments {
		activePaths[environment.Branch] = ps.Spec.ActivePath
		if environment.ActivePath != "" {
			activePaths[environment.Branch] = environment.ActivePath
		}
	}
	return activePaths
}

// validatedEnvironment carries one environment through a reconcile.
type validatedEnvironment struct {
	// held is the automatic validation that is waiting, after this reconcile.
	held *validations.Held

	branch     string
	activePath string
	// tagName is the tag the environment's hydrator reads.
	tagName string
	// seed is where the tag starts when it does not exist: the dry commit that is live in the
	// environment, or the head of the dry branch for an environment that follows it and has nothing
	// live. Empty when the tag exists and the controller knows of it, or when there is nothing to
	// start from.
	seed string
	// cleared is the newest dry commit the upstream environments cleared, "" when there is none. For
	// an environment with no upstreams it is the head of the dry branch.
	cleared string
	// rootCandidate is the head of the dry branch when it is new to an environment that follows it.
	rootCandidate string
	// desired is where the tag should point after this reconcile.
	desired string
	// desiredSource is the kind of validation that asks for desired. Empty when no validation moved
	// the tag from where it was, or from where it is seeded.
	desiredSource promoterv1alpha1.ValidatedTagSource

	validation promoterv1alpha1.ValidationPolicy
	advance    promoterv1alpha1.AdvancePolicy

	// upstreams are the environments this one directly depends on.
	upstreams []string
	// unapplied are the validations for this environment's tag that have not been applied.
	unapplied []validations.Validation

	// remote is the tag as it was read from the repository.
	remote git.TagState

	// observed is the environment as the PromotionStrategy reports it.
	observed promoterv1alpha1.EnvironmentStatus
	// previous is the environment's status from the last reconcile, zero for a new environment.
	previous promoterv1alpha1.ValidatedCommitStatusEnvironmentStatus
	// next is the environment's status after this reconcile.
	next promoterv1alpha1.ValidatedCommitStatusEnvironmentStatus
}

// automaticRoot reports whether the environment follows the dry branch.
func (e *validatedEnvironment) automaticRoot() bool {
	return len(e.upstreams) == 0 && e.validation == promoterv1alpha1.ValidationAutomatic
}

// previousTagSha returns the commit the tag was on when the controller last reconciled, "" when it has
// no record of the tag.
func (e *validatedEnvironment) previousTagSha() string {
	if e.previous.Tag == nil {
		return ""
	}
	return e.previous.Tag.Sha
}

// start returns where the tag is taken to point before validations are applied: where it is, or where
// it is seeded when it does not exist.
func (e *validatedEnvironment) start() string {
	if e.remote.Commit != "" {
		return e.remote.Commit
	}
	return e.seed
}

// validatedRun is one reconcile of a ValidatedCommitStatus.
type validatedRun struct {
	r   *ValidatedCommitStatusReconciler
	vcs *promoterv1alpha1.ValidatedCommitStatus
	ps  *promoterv1alpha1.PromotionStrategy

	repo *promoterv1alpha1.GitRepository
	ops  *git.EnvironmentOperations

	byBranch map[string]*validatedEnvironment
	// available caches which commits could be brought into the clone.
	available map[string]bool

	gap scms.GitOperationsProvider

	// refs is where the dry branch, the environment tags and the validation notes are.
	refs git.DryRefs

	now metav1.Time

	// cursor is how far into the validation notes this reconcile has applied.
	cursor string
	// notReadyReason is why the gate is not Ready although the reconcile went through.
	notReadyReason promoterConditions.CommonReason

	// envs are the environments in the order the PromotionStrategy lists them.
	envs []*validatedEnvironment
	// problems are failures that leave the gate not Ready without stopping the reconcile.
	problems []string

	// firstRun is true when the controller has no status for this gate: it is new, or its status was lost.
	firstRun bool
}

// Contains implements validations.Ancestry on the gate's clone.
func (run *validatedRun) Contains(ctx context.Context, descendant, ancestor string) (bool, error) {
	contains, err := run.ops.ContainsCommit(ctx, descendant, ancestor)
	if err != nil {
		return false, fmt.Errorf("failed to compare dry commits: %w", err)
	}
	return contains, nil
}

// reconcileEnvironments reads the dry branch, the environment tags and the validation notes, moves the
// tags that validations ask to be moved, and reports the gate on every environment's proposed commit. It
// returns how soon the gate should be reconciled again, zero for the usual requeue.
func (r *ValidatedCommitStatusReconciler) reconcileEnvironments(ctx context.Context, vcs *promoterv1alpha1.ValidatedCommitStatus, ps *promoterv1alpha1.PromotionStrategy) (time.Duration, error) {
	run, err := r.newRun(ctx, vcs, ps)
	if err != nil {
		return 0, err
	}

	// Nothing is pushed to a tag before this point, so each of these steps may fail the reconcile and
	// leave status as it was: the next reconcile starts from the same place.
	run.observe()
	run.clear()
	if err := run.collect(ctx); err != nil {
		return 0, err
	}
	if err := run.seed(ctx); err != nil {
		return 0, err
	}
	if err := run.plan(ctx); err != nil {
		return 0, err
	}

	// From here on status is written whatever happens. A tag this reconcile moved and did not record
	// would look like a tag someone moved by hand to the next one.
	run.move(ctx)
	commitStatuses, gateErr := run.gate(ctx)
	run.record()

	if gateErr != nil {
		return 0, gateErr
	}
	if err := utils.CleanupOrphanedCommitStatuses(ctx, r.Client, r.Recorder, vcs, commitStatuses); err != nil {
		return 0, fmt.Errorf("failed to cleanup orphaned CommitStatus resources: %w", err)
	}
	utils.InheritNotReadyConditionFromObjects(vcs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	retryAfter := run.nudge(ctx)
	if run.waitsForIdle() {
		// Nothing announces an environment becoming idle: its pull request is deleted after the last
		// change to the PromotionStrategy's status that would bring the controller back.
		retryAfter = sooner(retryAfter, intervalOr(r.pollInterval, validatedPollInterval))
	}
	if len(run.problems) > 0 {
		meta.SetStatusCondition(vcs.GetConditions(), metav1.Condition{
			Type:               string(promoterConditions.Ready),
			Status:             metav1.ConditionFalse,
			Reason:             string(run.notReadyReason),
			Message:            utils.TruncateString(strings.Join(run.problems, "; "), validatedMessageLimit),
			ObservedGeneration: vcs.Generation,
		})
		retryAfter = sooner(retryAfter, intervalOr(r.retryInterval, validatedRetryInterval))
	}
	return retryAfter, nil
}

// waitsForIdle reports whether an automatic validation is waiting for its environment to be idle.
func (run *validatedRun) waitsForIdle() bool {
	return slices.ContainsFunc(run.envs, func(env *validatedEnvironment) bool {
		return env.next.Held != nil && env.next.Held.Reason == promoterv1alpha1.ValidatedHeldReasonBusy
	})
}

// newRun resolves the environments of the PromotionStrategy and brings the gate's clone up to date
// with the dry branch, the environment tags and the validation notes.
func (r *ValidatedCommitStatusReconciler) newRun(ctx context.Context, vcs *promoterv1alpha1.ValidatedCommitStatus, ps *promoterv1alpha1.PromotionStrategy) (*validatedRun, error) {
	run := &validatedRun{
		r:              r,
		vcs:            vcs,
		ps:             ps,
		now:            metav1.Now(),
		available:      map[string]bool{},
		firstRun:       vcs.Status.DryBranch == nil,
		notReadyReason: promoterConditions.EnvironmentTagPushFailed,
	}
	if err := run.resolveEnvironments(); err != nil {
		return nil, err
	}

	scmProvider, secret, gitRepo, err := utils.GetScmProviderSecretAndGitRepositoryFromRepositoryReference(ctx, r.Client, r.SettingsMgr, ps.Spec.RepositoryReference, vcs)
	if err != nil {
		return nil, fmt.Errorf("failed to get ScmProvider and secret for repo %q: %w", ps.Spec.RepositoryReference.Name, err)
	}
	gap, err := gitauth.CreateGitOperationsProvider(ctx, r.Client, scmProvider, secret, client.ObjectKey{Namespace: vcs.Namespace, Name: ps.Spec.RepositoryReference.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to create git auth provider for ScmProvider %q: %w", scmProvider.GetName(), err)
	}
	run.gap, run.repo = gap, gitRepo

	// The identity names the kind, so this clone is never shared with the clone of a ChangeTransferPolicy
	// or a CommitValidation that happens to have the same name.
	run.ops = git.NewEnvironmentOperations(gitRepo, gap, reflect.TypeFor[promoterv1alpha1.ValidatedCommitStatus]().Name()+"/"+vcs.Namespace+"/"+vcs.Name)
	if err := run.ops.CloneRepo(ctx); err != nil {
		return nil, fmt.Errorf("failed to clone repo %q: %w", ps.Spec.RepositoryReference.Name, err)
	}

	tags := make([]string, 0, len(run.envs))
	for _, env := range run.envs {
		tags = append(tags, env.tagName)
	}
	run.refs, err = run.ops.SyncDryRefs(ctx, vcs.Spec.DryBranch, tags)
	if err != nil {
		return nil, fmt.Errorf("failed to read the dry branch, environment tags and validation notes: %w", err)
	}
	run.cursor = run.refs.NotesTip
	return run, nil
}

// resolveEnvironments lists the environments of the PromotionStrategy with their upstreams, their tag
// names and how this gate validates and advances them.
func (run *validatedRun) resolveEnvironments() error {
	dependents, err := resolveDependentEnvironments(run.ps)
	if err != nil {
		return err
	}
	graph, err := buildDAG(dependents)
	if err != nil {
		return fmt.Errorf("failed to build dependency graph: %w", err)
	}
	if err := graph.validateDAG(); err != nil {
		return fmt.Errorf("invalid dependency graph: %w", err)
	}

	activePaths := environmentActivePaths(run.ps)
	policies := make(map[string]promoterv1alpha1.ValidatedCommitStatusEnvironment, len(run.vcs.Spec.Environments))
	for _, policy := range run.vcs.Spec.Environments {
		if _, known := activePaths[policy.Branch]; !known {
			// Moving tags while a branch is misspelled would run that environment with the wrong policy.
			return fmt.Errorf("spec.environments names branch %q, which is not an environment of PromotionStrategy %q", policy.Branch, run.ps.Name)
		}
		policies[policy.Branch] = policy
	}
	observed := make(map[string]promoterv1alpha1.EnvironmentStatus, len(run.ps.Status.Environments))
	for _, status := range run.ps.Status.Environments {
		observed[status.Branch] = status
	}
	previous := make(map[string]promoterv1alpha1.ValidatedCommitStatusEnvironmentStatus, len(run.vcs.Status.Environments))
	for _, status := range run.vcs.Status.Environments {
		previous[status.Branch] = status
	}

	run.envs = make([]*validatedEnvironment, 0, len(graph.branches))
	run.byBranch = make(map[string]*validatedEnvironment, len(graph.branches))
	for _, branch := range graph.branches {
		env := &validatedEnvironment{
			branch:     branch,
			activePath: activePaths[branch],
			tagName:    validations.TagName(branch, activePaths[branch]),
			upstreams:  graph.dependsOn[branch],
			validation: promoterv1alpha1.ValidationAutomatic,
			advance:    promoterv1alpha1.AdvanceImmediately,
			observed:   observed[branch],
			previous:   previous[branch],
		}
		if policy, ok := policies[branch]; ok {
			if policy.Validation != "" {
				env.validation = policy.Validation
			}
			if policy.Advance != "" {
				env.advance = policy.Advance
			}
		}
		if env.previous.Tag != nil && env.previous.Tag.Name != env.tagName {
			// The environment got a different tag (its activePath changed). What was recorded about the
			// old tag says nothing about the new one. What the environment succeeded on still stands.
			env.previous = promoterv1alpha1.ValidatedCommitStatusEnvironmentStatus{
				Branch:         branch,
				SuccessfulShas: env.previous.SuccessfulShas,
			}
		}
		run.envs = append(run.envs, env)
		run.byBranch[branch] = env
	}
	return nil
}

// succeededOn returns the dry commit an environment has succeeded on, "" when it has not succeeded on
// what it runs. An environment succeeds on a commit when the commit is live in it, or was hydrated for
// it and changed nothing, and every active commit status passes.
func succeededOn(environment promoterv1alpha1.EnvironmentStatus) string {
	if environment.Active.Dry.Sha == "" || !utils.AreCommitStatusesPassing(environment.Active.CommitStatuses) {
		return ""
	}
	succeeded := environment.Active.Dry.Sha
	if environment.Proposed.Dry.Sha == environment.Active.Dry.Sha {
		// Nothing is proposed, so a hydrator note that is ahead of the live commit records a dry commit
		// that produced the same manifests.
		succeeded = getEffectiveHydratedDrySha(environment)
	}
	// Hydrators write the dry SHA, and nothing checks what they write. Anything that is not a commit ID
	// could not be validated for the next environment, and status would not accept it.
	if !git.IsFullObjectID(succeeded) {
		return ""
	}
	return succeeded
}

// observe records what each environment succeeded on and where its tag is, and notices tags that
// were moved by someone else.
func (run *validatedRun) observe() {
	for _, env := range run.envs {
		env.remote = run.refs.Tags[env.tagName]
		env.next = promoterv1alpha1.ValidatedCommitStatusEnvironmentStatus{
			Branch:          env.branch,
			Tag:             &promoterv1alpha1.ValidatedCommitStatusTagStatus{Name: env.tagName, Sha: env.remote.Commit},
			UpstreamCleared: env.previous.UpstreamCleared.DeepCopy(),
			Pending:         env.previous.Pending.DeepCopy(),
			SuccessfulShas:  validations.RecordSuccess(slices.Clone(env.previous.SuccessfulShas), succeededOn(env.observed), run.now),
		}
		if env.previous.Held != nil {
			env.held = &validations.Held{Sha: env.previous.Held.Sha, Reason: env.previous.Held.Reason}
		}
		if env.previous.Tag != nil && env.previous.Tag.Sha == env.remote.Commit {
			env.next.Tag.Source = env.previous.Tag.Source
			env.next.Tag.MovedAt = env.previous.Tag.MovedAt.DeepCopy()
		}
	}
}

// movedByHand reports whether the tag is somewhere the controller did not leave it. On a first run
// there is no record to compare with, so every tag is taken as found.
func (run *validatedRun) movedByHand(env *validatedEnvironment) bool {
	return !run.firstRun && env.remote.Commit != env.previousTagSha()
}

// seed decides where the tag of an environment starts when it does not exist: at the dry commit that is
// live in the environment, so that the tag starts out describing what is deployed. Where nothing is
// live, an environment that follows the dry branch starts at its head, and any other environment waits
// for a validation.
func (run *validatedRun) seed(ctx context.Context) error {
	logger := logf.FromContext(ctx)
	for _, env := range run.envs {
		if !run.needsSeed(env) {
			continue
		}
		// What is live is read from the active branch itself. The PromotionStrategy reports nothing for
		// an environment until its proposed branch exists, which a hydrator that reads the tag cannot
		// create before the tag does.
		live, err := git.LiveDrySha(ctx, run.gap, run.repo, env.branch, env.activePath)
		if _, malformed := errors.AsType[*git.MalformedHydratorMetadataError](err); malformed {
			// Nothing can be promoted to this environment until its metadata is repaired, so there is
			// nothing a tag could roll back. Holding every other environment's tag for it would not help.
			logger.Info("Cannot read what is live in the environment; treating it as empty", "branch", env.branch, "error", err.Error())
			live, err = "", nil
		}
		if err != nil {
			return fmt.Errorf("failed to read what is live in environment %q to seed tag %q: %w", env.branch, env.tagName, err)
		}
		if live != "" {
			found, err := run.ensureCommit(ctx, live)
			if err != nil {
				return err
			}
			if !found {
				logger.Info("The dry commit live in the environment is not in the repository; treating the environment as empty",
					"branch", env.branch, "drySha", live)
				live = ""
			}
		}
		if live == "" && env.automaticRoot() {
			live = run.refs.Head
		}
		env.seed = live
	}
	return nil
}

// needsSeed reports whether to work out where an environment's tag would be seeded. That takes a read
// of the active branch, so it is only done when the answer can matter.
func (run *validatedRun) needsSeed(env *validatedEnvironment) bool {
	if env.remote.Commit != "" {
		// A tag that exists needs no seed, unless the controller has no record of it. The seed then tells
		// a tag the controller created, and could not record, from one made by hand.
		return !run.firstRun && env.previousTagSha() == ""
	}
	switch {
	case env.previous.Tag == nil:
		// The controller has not looked at this environment before.
		return true
	case env.previous.Tag.Sha != "":
		// The tag was there, and is gone.
		return true
	case env.observed.Active.Dry.Sha != "":
		// Something has become live in the environment since the controller last looked.
		return true
	default:
		// Something is about to create the tag. It must not be created behind what is live.
		return len(env.unapplied) > 0 || env.rootCandidate != "" || env.next.Pending != nil
	}
}

// ensureCommit reports whether the commit is in the clone, fetching it when it is not. A commit the
// repository does not have, or does not serve, is reported as missing rather than as an error.
func (run *validatedRun) ensureCommit(ctx context.Context, sha string) (bool, error) {
	if found, known := run.available[sha]; known {
		return found, nil
	}
	found, err := run.ops.EnsureCommit(ctx, sha)
	if err != nil {
		return false, fmt.Errorf("failed to fetch dry commit %q: %w", sha, err)
	}
	run.available[sha] = found
	return found, nil
}

// clear works out, for every environment, the newest dry commit its upstream environments have all
// succeeded on. An environment with no upstreams is cleared for the head of the dry branch.
func (run *validatedRun) clear() {
	for _, env := range run.envs {
		if len(env.upstreams) == 0 {
			env.cleared = run.refs.Head
			continue
		}
		memories := make([][]promoterv1alpha1.ValidatedCommitStatusSuccessfulSha, 0, len(env.upstreams))
		for _, upstream := range env.upstreams {
			// The dependency graph was validated, so every upstream is an environment. Should one not be,
			// its empty memory means that nothing is cleared.
			var succeeded []promoterv1alpha1.ValidatedCommitStatusSuccessfulSha
			if upstreamEnv, known := run.byBranch[upstream]; known {
				succeeded = upstreamEnv.next.SuccessfulShas
			}
			memories = append(memories, succeeded)
		}
		env.cleared = validations.UpstreamCleared(memories...)
	}
}

// collect gathers the validations each environment's tag has not seen yet. It also writes the
// automatic validations: one for an environment whose upstreams have cleared a commit they had not
// cleared before. A new head of the dry branch counts as an automatic validation for an environment
// that follows it, and no note is written for those.
func (run *validatedRun) collect(ctx context.Context) error {
	if err := run.collectUnapplied(ctx); err != nil {
		return err
	}

	var entries []git.ValidationEntry
	var validated []*validatedEnvironment
	for _, env := range run.envs {
		if env.cleared == "" {
			// The upstreams have no success in common. What they last cleared stands.
			continue
		}
		cleared := &promoterv1alpha1.ValidatedCommitStatusUpstreamCleared{Sha: env.cleared}
		isNew := env.previous.UpstreamCleared == nil || env.previous.UpstreamCleared.Sha != env.cleared
		switch {
		case env.validation != promoterv1alpha1.ValidationAutomatic:
			// A person validates for this environment. The controller only reports what is ready.
			env.next.UpstreamCleared = cleared
		case len(env.upstreams) == 0:
			if isNew {
				env.rootCandidate = env.cleared
			}
			env.next.UpstreamCleared = cleared
		case !isNew:
			// Already validated.
		case slices.Contains(env.unapplied, validations.Validation{Sha: env.cleared, Automatic: true}):
			// An earlier reconcile wrote the validation and did not get to record that it had.
			env.next.UpstreamCleared = cleared
		default:
			entries = append(entries, git.ValidationEntry{
				Sha: env.cleared,
				Line: validations.Line{
					Target: env.tagName,
					Source: validations.SourceAutomatic,
					By:     strings.Join(env.upstreams, ", ") + " successful",
					At:     run.now.Time,
				},
			})
			validated = append(validated, env)
		}
	}
	if len(entries) == 0 {
		return nil
	}

	synced := run.cursor
	tip, err := run.ops.AppendValidations(ctx, entries)
	if err != nil {
		// upstreamCleared is left where it was, so the next reconcile writes these again.
		run.notReadyReason = promoterConditions.ValidationNotRecorded
		run.problems = append(run.problems, fmt.Sprintf("failed to record automatic validations: %v", err))
		return nil
	}
	run.cursor = tip
	for _, env := range validated {
		env.next.UpstreamCleared = &promoterv1alpha1.ValidatedCommitStatusUpstreamCleared{Sha: env.cleared}
	}

	// Read back everything written since the notes were synced: the validations above, and any that
	// someone else pushed in between, in the order they were written.
	written, ok, err := run.ops.ListValidations(ctx, synced, 0)
	if err != nil {
		return fmt.Errorf("failed to list validations: %w", err)
	}
	if !ok {
		// The notes were rewritten while this reconcile ran. Apply at least what it wrote itself.
		for i, env := range validated {
			env.unapplied = append(env.unapplied, validations.Validation{Sha: entries[i].Sha, Automatic: true})
		}
		return nil
	}
	run.distribute(written)
	return nil
}

// collectUnapplied lists the validations written after status.validationsCursor. When the controller
// has no cursor to go by, each environment resumes after the validation that matches its tag.
func (run *validatedRun) collectUnapplied(ctx context.Context) error {
	if !run.firstRun {
		unapplied, ok, err := run.ops.ListValidations(ctx, run.vcs.Status.ValidationsCursor, 0)
		if err != nil {
			return fmt.Errorf("failed to list validations: %w", err)
		}
		if ok {
			run.distribute(unapplied)
			return nil
		}
		logf.FromContext(ctx).Info("The validation notes no longer contain the commit the controller had applied them up to; resuming from the environment tags",
			"validationsCursor", run.vcs.Status.ValidationsCursor)
	}

	recent, _, err := run.ops.ListValidations(ctx, "", validatedLostCursorWalk)
	if err != nil {
		return fmt.Errorf("failed to list validations: %w", err)
	}
	for _, env := range run.envs {
		// Resume after the newest validation of the commit the tag is on. With none, resume from the
		// head of the notes: validations of other commits are older than whatever put the tag there.
		matched := false
		source := promoterv1alpha1.ValidatedTagSourceManual
		var later []validations.Validation
		for _, validation := range recent {
			if validation.Line.Target != env.tagName {
				continue
			}
			if env.remote.Commit != "" && validation.Sha == env.remote.Commit {
				// A newer validation of the tag's commit: whatever came before it has been applied.
				matched, later = true, nil
				source = promoterv1alpha1.ValidatedTagSourceManual
				if validation.Line.Automatic() {
					source = promoterv1alpha1.ValidatedTagSourceAutomatic
				}
				continue
			}
			later = append(later, validations.Validation{Sha: validation.Sha, Automatic: validation.Line.Automatic()})
		}
		if !matched {
			continue
		}
		if env.next.Tag.Source == "" {
			env.next.Tag.Source = source
		}
		env.unapplied = append(env.unapplied, later...)
	}
	return nil
}

// distribute hands each validation to the environment whose tag it names. Validations for tags that
// are not this gate's are someone else's.
func (run *validatedRun) distribute(written []git.ValidationEvent) {
	for _, validation := range written {
		for _, env := range run.envs {
			if validation.Line.Target == env.tagName {
				env.unapplied = append(env.unapplied, validations.Validation{Sha: validation.Sha, Automatic: validation.Line.Automatic()})
			}
		}
	}
}

// plan decides where each environment's tag should point.
func (run *validatedRun) plan(ctx context.Context) error {
	for _, env := range run.envs {
		if err := run.planEnvironment(ctx, env); err != nil {
			return fmt.Errorf("failed to decide where tag %q should point: %w", env.tagName, err)
		}
	}
	return nil
}

func (run *validatedRun) planEnvironment(ctx context.Context, env *validatedEnvironment) error {
	in, err := run.planInput(ctx, env)
	if err != nil {
		return err
	}

	if run.movedByHand(env) {
		if run.movedByController(ctx, env, in) {
			return nil
		}
		// The tag stays where it was put until a newer validation arrives. Whatever was waiting for the
		// old position is void.
		run.r.Recorder.Eventf(run.vcs, nil, "Normal", constants.EnvironmentTagMovedByHandReason, "ObservingEnvironmentTag",
			constants.EnvironmentTagMovedByHandMessage, env.tagName, shortOrNone(env.previousTagSha()), shortOrNone(env.remote.Commit))
		if env.remote.Commit != "" {
			env.next.Tag.Source = promoterv1alpha1.ValidatedTagSourceExternal
			env.next.Tag.MovedAt = &run.now
		}
		env.next.Pending = nil
		in.Held, in.Pending = nil, nil
	}

	result, err := run.replay(ctx, in)
	if err != nil {
		return err
	}
	env.held, env.desired, env.desiredSource = result.Held, result.Tag, result.Source
	return nil
}

// movedByController reports whether a tag that is not where status says it is was put there by this
// controller: the reconcile that pushed the tag did not get to write status. In that case the same
// validations, applied to where the tag was, lead to where it is now, and the move is recorded now.
func (run *validatedRun) movedByController(ctx context.Context, env *validatedEnvironment, in validations.Input) bool {
	if env.remote.Commit == "" {
		return false
	}
	in.Tag = env.previousTagSha()
	if in.Tag == "" {
		in.Tag = env.seed
	}
	// Had the controller moved the tag, the environment would have been idle then, whatever it is now
	// that it has a new commit to promote.
	in.Idle = true
	own, err := run.replay(ctx, in)
	if err != nil || own.Tag != env.remote.Commit {
		return false
	}

	env.next.Tag.Source = own.Source
	if own.Source == "" {
		env.next.Tag.Source = promoterv1alpha1.ValidatedTagSourceSeeded
	}
	env.next.Tag.MovedAt = &run.now
	env.next.Pending = nil
	env.held, env.desired = own.Held, own.Tag
	return true
}

// planInput gathers what the tag rules need for one environment. Commits that are not in the
// repository are left out: nothing can be hydrated from them, and ancestry cannot be asked of them.
func (run *validatedRun) planInput(ctx context.Context, env *validatedEnvironment) (validations.Input, error) {
	in := validations.Input{Tag: env.start(), RootCandidate: env.rootCandidate}

	if env.advance == promoterv1alpha1.AdvanceWhenIdle {
		idle, err := run.idle(ctx, env)
		if err != nil {
			return validations.Input{}, err
		}
		in.WhenIdle, in.Idle = true, idle
	}
	if env.held != nil {
		found, err := run.ensureCommit(ctx, env.held.Sha)
		if err != nil {
			return validations.Input{}, err
		}
		if found {
			in.Held = env.held
		}
	}
	if pending := env.next.Pending; pending != nil {
		found, err := run.ensureCommit(ctx, pending.Sha)
		if err != nil {
			return validations.Input{}, err
		}
		if found {
			in.Pending = &validations.Pending{Sha: pending.Sha, Source: pending.Source}
		}
	}
	for _, validation := range env.unapplied {
		found, err := run.ensureCommit(ctx, validation.Sha)
		if err != nil {
			return validations.Input{}, err
		}
		if !found {
			run.r.Recorder.Eventf(run.vcs, nil, "Warning", constants.ValidationIgnoredReason, "ApplyingValidations",
				constants.ValidationIgnoredMessage, validation.Sha, env.tagName)
			continue
		}
		in.Validations = append(in.Validations, validation)
	}
	return in, nil
}

// idle reports whether an environment has finished whatever its tag last asked of it: its hydrator has
// processed the tag's commit, nothing is proposed, no promotion pull request is open, and no active
// commit status is pending. A failed commit status counts as idle, so that a validated fix can roll
// forward.
//
// It reads the ChangeTransferPolicy and its PullRequest rather than the PromotionStrategy's copy of
// them. The copy trails by a reconcile, and the pull request exists before the ChangeTransferPolicy
// reports it.
func (run *validatedRun) idle(ctx context.Context, env *validatedEnvironment) (bool, error) {
	if env.remote.Commit == "" {
		// The tag does not exist, so nothing has been asked of the environment.
		return true, nil
	}

	var ctp promoterv1alpha1.ChangeTransferPolicy
	key := client.ObjectKey{Namespace: run.ps.Namespace, Name: utils.ChangeTransferPolicyNameForEnvironment(run.ps.Name, env.branch)}
	if err := run.r.Get(ctx, key, &ctp); err != nil {
		if k8serrors.IsNotFound(err) {
			// Nothing can be said about an environment that is not set up yet.
			return false, nil
		}
		return false, fmt.Errorf("failed to get ChangeTransferPolicy %q: %w", key.Name, err)
	}
	current := promoterv1alpha1.EnvironmentStatus{Active: ctp.Status.Active, Proposed: ctp.Status.Proposed}
	if getEffectiveHydratedDrySha(current) != env.remote.Commit || current.Proposed.Dry.Sha != current.Active.Dry.Sha {
		return false, nil
	}
	for _, status := range current.Active.CommitStatuses {
		if status.Phase == string(promoterv1alpha1.CommitPhasePending) {
			return false, nil
		}
	}

	var pullRequests promoterv1alpha1.PullRequestList
	if err := run.r.List(ctx, &pullRequests, ctpPullRequestListOptions(&ctp)); err != nil {
		return false, fmt.Errorf("failed to list PullRequests of ChangeTransferPolicy %q: %w", key.Name, err)
	}
	return len(pullRequests.Items) == 0, nil
}

// replay applies the tag rules. Ancestry is only asked of commits that are in the clone.
func (run *validatedRun) replay(ctx context.Context, in validations.Input) (validations.Result, error) {
	if in.Tag != "" {
		found, err := run.ensureCommit(ctx, in.Tag)
		if err != nil {
			return validations.Result{}, err
		}
		if !found {
			return validations.Result{}, fmt.Errorf("commit %q is not in the repository", in.Tag)
		}
	}
	result, err := validations.Plan(ctx, run, in)
	if err != nil {
		return validations.Result{}, fmt.Errorf("failed to apply validations: %w", err)
	}
	return result, nil
}

// move pushes every tag that should point somewhere else, once. A tag that someone moved in the meantime
// is left where they put it. A push that fails for any other reason is kept as pending and tried again.
func (run *validatedRun) move(ctx context.Context) {
	logger := logf.FromContext(ctx)
	for _, env := range run.envs {
		env.next.Held = run.heldStatus(env)
		if env.desired == env.remote.Commit {
			env.next.Pending = nil
			continue
		}

		source := env.desiredSource
		if source == "" {
			// Nothing moved the tag from where it was seeded.
			source = promoterv1alpha1.ValidatedTagSourceSeeded
		}

		// The gate goes first. Once the tag has moved, a proposal hydrated from where it was must not
		// merge on the strength of a status that was true a moment ago.
		if err := run.holdProposal(ctx, env); err != nil {
			run.keepPending(env, source, fmt.Errorf("failed to hold the open proposal before moving the tag: %w", err))
			continue
		}

		err := run.ops.MoveTag(ctx, env.tagName, env.desired, env.remote.Raw)
		if err == nil {
			logger.Info("Moved environment tag", "tag", env.tagName, "from", env.remote.Commit, "to", env.desired, "source", source)
			run.r.Recorder.Eventf(run.vcs, nil, "Normal", constants.EnvironmentTagMovedReason, "MovingEnvironmentTag",
				constants.EnvironmentTagMovedMessage, env.tagName, shortOrNone(env.remote.Commit), shortOrNone(env.desired), source)
			env.next.Tag = &promoterv1alpha1.ValidatedCommitStatusTagStatus{Name: env.tagName, Sha: env.desired, Source: source, MovedAt: &run.now}
			env.next.Pending = nil
			if run.r.EnqueueCTP != nil {
				run.r.EnqueueCTP(run.ps.Namespace, utils.ChangeTransferPolicyNameForEnvironment(run.ps.Name, env.branch))
			}
			continue
		}

		// The push did not go through. If the tag is somewhere else than where it was read, someone
		// moved it first, and it stays where they put it.
		current, readErr := run.ops.ReadTag(ctx, env.tagName)
		if readErr == nil && current.Raw != env.remote.Raw {
			logger.Info("Environment tag was moved while it was being pushed; leaving it", "tag", env.tagName, "to", current.Commit)
			run.r.Recorder.Eventf(run.vcs, nil, "Normal", constants.EnvironmentTagMovedByHandReason, "ObservingEnvironmentTag",
				constants.EnvironmentTagMovedByHandMessage, env.tagName, shortOrNone(env.remote.Commit), shortOrNone(current.Commit))
			env.next.Tag = &promoterv1alpha1.ValidatedCommitStatusTagStatus{Name: env.tagName, Sha: current.Commit}
			if current.Commit != "" {
				env.next.Tag.Source = promoterv1alpha1.ValidatedTagSourceExternal
				env.next.Tag.MovedAt = &run.now
			}
			env.next.Pending, env.next.Held = nil, nil
			continue
		}
		run.keepPending(env, source, err)
	}
}

// keepPending records a tag move that could not be made, so that the next reconcile tries it again. A
// tag that could not be seeded needs no record: it still does not exist, and is seeded again.
func (run *validatedRun) keepPending(env *validatedEnvironment, source promoterv1alpha1.ValidatedTagSource, err error) {
	run.problems = append(run.problems, fmt.Sprintf("failed to move tag %q to %s: %v", env.tagName, utils.TruncateString(env.desired, shortShaLength), err))
	if source == promoterv1alpha1.ValidatedTagSourceSeeded {
		return
	}
	if env.next.Pending == nil || env.next.Pending.Sha != env.desired {
		run.r.Recorder.Eventf(run.vcs, nil, "Warning", constants.EnvironmentTagPushFailedReason, "MovingEnvironmentTag",
			constants.EnvironmentTagPushFailedMessage, env.tagName, utils.TruncateString(env.desired, shortShaLength), err)
	}
	env.next.Pending = &promoterv1alpha1.ValidatedCommitStatusPending{
		Sha:     env.desired,
		Source:  source,
		Message: utils.TruncateString(err.Error(), validatedMessageLimit),
	}
}

// heldStatus describes the automatic validation that is waiting for an environment, if any.
func (run *validatedRun) heldStatus(env *validatedEnvironment) *promoterv1alpha1.ValidatedCommitStatusHeld {
	if env.held == nil {
		return nil
	}
	held := &promoterv1alpha1.ValidatedCommitStatusHeld{Sha: env.held.Sha, Reason: env.held.Reason}
	switch env.held.Reason {
	case promoterv1alpha1.ValidatedHeldReasonPinned:
		held.Message = "Waiting for the dry branch to contain " + utils.TruncateString(env.desired, shortShaLength)
	case promoterv1alpha1.ValidatedHeldReasonBusy:
		held.Message = "Waiting for the environment to finish promoting " + utils.TruncateString(env.desired, shortShaLength)
	default:
		// No other reason exists; a new one would get no message until it is given one here.
	}
	return held
}

// record writes what this reconcile found and did into status.
func (run *validatedRun) record() {
	environments := make([]promoterv1alpha1.ValidatedCommitStatusEnvironmentStatus, 0, len(run.envs))
	for _, env := range run.envs {
		environments = append(environments, env.next)
	}
	run.vcs.Status.Environments = environments
	run.vcs.Status.DryBranch = &promoterv1alpha1.ValidatedCommitStatusDryBranchStatus{Sha: run.refs.Head}
	run.vcs.Status.ValidationsCursor = run.cursor
}

// gateReport is what the gate says about an environment's proposed commit for a given position of its tag.
func gateReport(env *validatedEnvironment, tagSha string) (promoterv1alpha1.CommitStatusPhase, string) {
	hydratedFrom := getEffectiveHydratedDrySha(env.observed)
	switch {
	case tagSha == "":
		return promoterv1alpha1.CommitPhasePending, fmt.Sprintf("Waiting for a validation: %s does not exist yet", env.tagName)
	case hydratedFrom == tagSha:
		return promoterv1alpha1.CommitPhaseSuccess, "Proposed commit matches " + env.tagName
	default:
		return promoterv1alpha1.CommitPhasePending, fmt.Sprintf("Waiting for hydrator to process %s at %s (currently %s)",
			env.tagName, utils.TruncateString(tagSha, shortShaLength), shortOrNone(hydratedFrom))
	}
}

// hasProposal reports whether the environment has a proposed change for the gate to report on.
func hasProposal(env *validatedEnvironment) bool {
	return env.observed.Proposed.Hydrated.Sha != "" && env.observed.Active.Dry.Sha != env.observed.Proposed.Dry.Sha
}

// holdProposal sets the gate to pending on an environment's open proposal when the tag is about to
// leave the commit the proposal was hydrated from.
func (run *validatedRun) holdProposal(ctx context.Context, env *validatedEnvironment) error {
	if !hasProposal(env) {
		return nil
	}
	phase, description := gateReport(env, env.desired)
	if phase == promoterv1alpha1.CommitPhaseSuccess {
		return nil
	}
	_, _, err := run.r.applyGateCommitStatus(ctx, run.vcs, run.ps, env, phase, description)
	return err
}

// gate reports, on every environment's proposed hydrated commit, whether the commit was hydrated from the
// environment's tag. It goes on to the next environment when one fails, and returns the failures together.
func (run *validatedRun) gate(ctx context.Context) ([]*promoterv1alpha1.CommitStatus, error) {
	logger := logf.FromContext(ctx)
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(run.envs))
	var errs []error
	for _, env := range run.envs {
		// Skip when there is no proposed change (active and proposed dry SHAs match): there is no
		// in-flight PR to gate. Keep any existing CommitStatus in the valid set so orphan cleanup leaves
		// the last report alone, and mirror it so operators still see it after promotion completes.
		if !hasProposal(env) {
			logger.V(4).Info("Skipping environment with no proposed change", "branch", env.branch)
			existing := &promoterv1alpha1.CommitStatus{}
			name := utils.CommitStatusResourceName(ctx, run.vcs, env.branch)
			if err := run.r.Get(ctx, client.ObjectKey{Namespace: run.vcs.Namespace, Name: name}, existing); err != nil {
				if !k8serrors.IsNotFound(err) {
					errs = append(errs, fmt.Errorf("failed to get existing CommitStatus for branch %q: %w", env.branch, err))
				}
				continue
			}
			commitStatuses = append(commitStatuses, existing)
			env.next.GateEnvironmentCommitStatus = utils.GateEnvironmentCommitStatusFromCommitStatus(existing)
			continue
		}

		phase, description := gateReport(env, env.next.Tag.Sha)
		commitStatus, report, err := run.r.applyGateCommitStatus(ctx, run.vcs, run.ps, env, phase, description)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to set commit status for branch %q: %w", env.branch, err))
			env.next.GateEnvironmentCommitStatus = env.previous.GateEnvironmentCommitStatus
			continue
		}
		commitStatuses = append(commitStatuses, commitStatus)
		env.next.GateEnvironmentCommitStatus = report
	}
	return commitStatuses, errors.Join(errs...)
}

// applyGateCommitStatus upserts, via Server-Side Apply, the CommitStatus that reports on the
// environment's proposed hydrated commit.
func (r *ValidatedCommitStatusReconciler) applyGateCommitStatus(
	ctx context.Context,
	vcs *promoterv1alpha1.ValidatedCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	env *validatedEnvironment,
	phase promoterv1alpha1.CommitStatusPhase,
	description string,
) (*promoterv1alpha1.CommitStatus, promoterv1alpha1.GateEnvironmentCommitStatus, error) {
	key := vcs.Spec.Key
	commitStatusName := utils.CommitStatusResourceName(ctx, vcs, env.branch)
	// Bind the CommitStatus to the proposed branch's hydrated SHA: that is the commit the
	// ChangeTransferPolicy inspects when gating the promotion PR.
	reportedSha := env.observed.Proposed.Hydrated.Sha

	kind := reflect.TypeFor[promoterv1alpha1.ValidatedCommitStatus]().Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)

	var renderedURL string
	if vcs.Spec.URL.Template != "" {
		data := ValidatedCommitStatusURLTemplateData{
			Environment:           env.branch,
			Tag:                   env.tagName,
			ValidatedCommitStatus: *vcs,
			PromotionStrategy:     ps,
		}
		var err error
		renderedURL, err = utils.RenderURLTemplate(vcs.Spec.URL.Template, data, vcs.Spec.URL.Options...)
		if err != nil {
			return nil, promoterv1alpha1.GateEnvironmentCommitStatus{}, err //nolint:wrapcheck // RenderURLTemplate says which step failed
		}
	}

	// Use the stable gate key as the SCM commit status context (spec.Name) so users can reference a
	// single predictable name in branch protection rules. The per-environment detail goes in the
	// description instead.
	commitStatusSpec := acv1alpha1.CommitStatusSpec().
		WithRepositoryReference(acv1alpha1.ObjectReference().
			WithName(ps.Spec.RepositoryReference.Name)).
		WithSha(reportedSha).
		WithName(key).
		WithDescription(description).
		WithPhase(phase)
	if vcs.Spec.PublishTo != nil {
		commitStatusSpec = commitStatusSpec.WithPublishTo(*vcs.Spec.PublishTo)
	}
	if renderedURL != "" {
		commitStatusSpec = commitStatusSpec.WithUrl(renderedURL)
	}

	commitStatusApply := acv1alpha1.CommitStatus(commitStatusName, vcs.Namespace).
		WithLabels(utils.CommitStatusStandardLabels(vcs, env.branch, key)).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(vcs.Name).
			WithUID(vcs.UID).
			WithController(true).
			WithBlockOwnerDeletion(true)).
		WithSpec(commitStatusSpec)

	// Read the previously persisted phase from the child CommitStatus (NotFound means this branch
	// is being gated for the first time) so the phase-change event below stays transition-only.
	previousPhase := ""
	existingCommitStatus := &promoterv1alpha1.CommitStatus{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: vcs.Namespace, Name: commitStatusName}, existingCommitStatus); err == nil {
		previousPhase = string(existingCommitStatus.Spec.Phase)
	} else if !k8serrors.IsNotFound(err) {
		return nil, promoterv1alpha1.GateEnvironmentCommitStatus{}, fmt.Errorf("failed to get existing CommitStatus %q: %w", commitStatusName, err)
	}

	commitStatus := &promoterv1alpha1.CommitStatus{}
	commitStatus.Name = commitStatusName
	commitStatus.Namespace = vcs.Namespace
	if err := r.Patch(ctx, commitStatus, utils.ApplyPatch{ApplyConfig: commitStatusApply}, client.FieldOwner(constants.ValidatedCommitStatusControllerFieldOwner), client.ForceOwnership); err != nil {
		return nil, promoterv1alpha1.GateEnvironmentCommitStatus{}, fmt.Errorf("failed to apply CommitStatus: %w", err)
	}

	emitCommitStatusPhaseChangedEvent(r.Recorder, vcs, key, env.branch, previousPhase, string(phase))

	return commitStatus, promoterv1alpha1.GateEnvironmentCommitStatus{
		Phase:       phase,
		Description: description,
		Url:         renderedURL,
		ReportedSha: reportedSha,
	}, nil
}

// nudge enqueues the ChangeTransferPolicy of every environment whose hydrator has not caught up with a
// tag that moved recently, and returns how soon to look again, zero when there is nothing to wait for.
//
// It only runs while this gate is the strategy's ordering gate. Until then the PromotionStrategy
// controller does its own nudging, for hydrators that read the dry branch.
func (run *validatedRun) nudge(ctx context.Context) time.Duration {
	if run.r.EnqueueCTP == nil || referencedValidatedCommitStatus(run.ps) != run.vcs.Name {
		return 0
	}
	interval := intervalOr(run.r.pollInterval, validatedPollInterval)

	var wait time.Duration
	for _, env := range run.envs {
		tag := env.next.Tag
		if tag == nil || tag.Sha == "" || tag.MovedAt == nil || run.now.Sub(tag.MovedAt.Time) > validatedNudgeWindow {
			continue
		}
		if getEffectiveHydratedDrySha(env.observed) == tag.Sha {
			continue
		}
		wait = interval
		key := client.ObjectKey{Namespace: run.ps.Namespace, Name: utils.ChangeTransferPolicyNameForEnvironment(run.ps.Name, env.branch)}
		if !run.r.shouldNudge(key, run.now.Time, interval) {
			continue
		}
		logf.FromContext(ctx).V(4).Info("Nudging ChangeTransferPolicy to pick up hydration for a moved tag",
			"changeTransferPolicy", key.Name, "tag", env.tagName, "tagSha", tag.Sha)
		run.r.EnqueueCTP(key.Namespace, key.Name)
	}
	return wait
}

// shouldNudge reports whether the ChangeTransferPolicy was last nudged at least an interval ago, and
// records the nudge when it was.
func (r *ValidatedCommitStatusReconciler) shouldNudge(key client.ObjectKey, now time.Time, interval time.Duration) bool {
	r.nudgeMutex.Lock()
	defer r.nudgeMutex.Unlock()

	if r.lastNudges == nil {
		r.lastNudges = map[client.ObjectKey]time.Time{}
	}
	// Entries that can no longer hold a nudge back are dropped here, so the map stays as small as the
	// set of environments with a recently moved tag.
	for nudged, at := range r.lastNudges {
		if now.Sub(at) > validatedNudgeWindow {
			delete(r.lastNudges, nudged)
		}
	}
	if last, nudged := r.lastNudges[key]; nudged && now.Sub(last) < interval {
		return false
	}
	r.lastNudges[key] = now
	return true
}

// shortOrNone shortens a SHA for a message, with "none" standing in for no commit.
func shortOrNone(sha string) string {
	if sha == "" {
		return "none"
	}
	return utils.TruncateString(sha, shortShaLength)
}

// validatedCommitStatusRefShas returns the commits the gate last saw at the head of the dry branch and
// on each environment tag. A push webhook names the commit a ref pointed at before the push, which is
// how the webhook receiver finds the gates that read the pushed ref.
func validatedCommitStatusRefShas(obj client.Object) []string {
	//nolint:forcetypeassert // type is guaranteed by the IndexField API
	vcs := obj.(*promoterv1alpha1.ValidatedCommitStatus)
	var shas []string
	if vcs.Status.DryBranch != nil && vcs.Status.DryBranch.Sha != "" {
		shas = append(shas, vcs.Status.DryBranch.Sha)
	}
	for _, environment := range vcs.Status.Environments {
		if environment.Tag != nil && environment.Tag.Sha != "" && !slices.Contains(shas, environment.Tag.Sha) {
			shas = append(shas, environment.Tag.Sha)
		}
	}
	return shas
}

// SetupWithManager sets up the controller with the Manager.
func (r *ValidatedCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// This index is used by the webhook receiver to find the gates that read a pushed dry branch or tag.
	if err := mgr.GetFieldIndexer().IndexField(ctx, &promoterv1alpha1.ValidatedCommitStatus{}, constants.ValidatedCommitStatusRefShaField, validatedCommitStatusRefShas); err != nil {
		return fmt.Errorf("failed to set field index for %s: %w", constants.ValidatedCommitStatusRefShaField, err)
	}

	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.ValidatedCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get ValidatedCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.ValidatedCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get ValidatedCommitStatus max concurrent reconciles: %w", err)
	}

	// Create a channel for external enqueue requests, so that the webhook receiver can trigger a
	// reconcile without modifying the object. The buffer matches the default internal buffer size of
	// source.Channel; sends block when it is full, which is the backpressure callers get.
	externalEnqueueChan := make(chan event.GenericEvent, 1024)
	r.enqueueFunc = func(namespace, name string) {
		vcs := &promoterv1alpha1.ValidatedCommitStatus{}
		vcs.SetNamespace(namespace)
		vcs.SetName(name)
		externalEnqueueChan <- event.GenericEvent{Object: vcs}
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.ValidatedCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.PromotionStrategy{}, CommitStatusGatePromotionStrategyWatchHandler[promoterv1alpha1.ValidatedCommitStatusList](r.Client)).
		WatchesRawSource(source.Channel(externalEnqueueChan, &handler.EnqueueRequestForObject{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Named("validatedcommitstatus").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}
