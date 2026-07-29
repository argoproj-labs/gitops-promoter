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
	"sync"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/gitauth"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// GitCommitStatusReconciler reconciles a GitCommitStatus object
type GitCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager

	// EnqueueCTP is a function to enqueue CTP reconcile requests without modifying the CTP object.
	EnqueueCTP CTPEnqueueFunc

	// expressionCache caches compiled expressions to avoid recompilation on every reconciliation
	// Key: expression string, Value: compiled *vm.Program
	expressionCache sync.Map
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitcommitstatuses,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitcommitstatuses/finalizers,verbs=update
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses,verbs=get;list;watch;patch;create;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=clusterscmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For each configured environment in the GitCommitStatus, the controller:
// 1. Fetches the PromotionStrategy to get the proposed hydrated commit SHA
// 2. Retrieves commit details (message, author, trailers) from the PromotionStrategy status
// 3. Evaluates the configured expression against the commit data
// 4. Creates/updates a CommitStatus resource with the validation result
func (r *GitCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling GitCommitStatus", "name", req.Name)
	startTime := time.Now()

	var gcs promoterv1alpha1.GitCommitStatus
	// This function applies the resource status via Server-Side Apply at the end of the reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &gcs, r.Client, r.Recorder, constants.GitCommitStatusControllerFieldOwner, &result, &err, &previousReady)

	err = r.Get(ctx, req.NamespacedName, &gcs, &client.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("GitCommitStatus not found")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "failed to get GitCommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get GitCommitStatus %q: %w", req.Name, err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	previousReady = utils.RemoveReadyCondition(&gcs)

	if err := ensureControllerInstanceIDStable(ctx, r.SettingsMgr); err != nil {
		return ctrl.Result{}, err
	}

	// Fetch the referenced PromotionStrategy
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{
		Namespace: gcs.Namespace,
		Name:      gcs.Spec.PromotionStrategyRef.Name,
	}
	err = r.Get(ctx, psKey, &ps)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", gcs.Spec.PromotionStrategyRef.Name, err)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", gcs.Spec.PromotionStrategyRef.Name, err)
	}

	// Process each environment and evaluate expressions
	transitionedEnvironments, commitStatuses, err := r.processEnvironments(ctx, &gcs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to process environments: %w", err)
	}

	err = utils.CleanupOrphanedCommitStatuses(ctx, r.Client, r.Recorder, &gcs, commitStatuses)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to cleanup orphaned CommitStatus resources: %w", err)
	}

	// Inherit conditions from CommitStatus objects
	utils.InheritNotReadyConditionFromObjects(&gcs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	// If any validations transitioned to success, enqueue the corresponding ChangeTransferPolicies
	utils.EnqueueChangeTransferPolicies(ctx, r.EnqueueCTP, &ps, transitionedEnvironments, "validation transition")

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
//
//nolint:dupl // Gate controllers share the same SetupWithManager skeleton by design.
func (r *GitCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.GitCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get GitCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.GitCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get GitCommitStatus max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.GitCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.PromotionStrategy{}, r.enqueueGitCommitStatusForPromotionStrategy()).
		Named("gitcommitstatus").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
			RateLimiter:             rateLimiter,
		}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// CommitData is the single commit an expression reads, selected by spec.target. It comes from the
// PromotionStrategy status, which records one commit per branch, so it can never describe more than
// the branch tip. Promotion-wide data belongs in VerificationData instead.
type CommitData struct {
	Trailers map[string][]string `expr:"Trailers"`
	SHA      string              `expr:"SHA"`
	Subject  string              `expr:"Subject"`
	Body     string              `expr:"Body"`
	Author   string              `expr:"Author"`
}

// VerificationData is the signature state of a promotion: every commit the promotion would add,
// which is the range activeHydratedSha..proposedSha. A promotion merges that whole range under one
// CommitStatus, so verifying only the tip would leave the rest of what gets merged unchecked.
//
// It is a sibling of CommitData rather than a field on it because spec.target selects one commit
// while this covers many, and because it is the only expression data that requires a clone.
type VerificationData struct {
	Commits []VerificationCommit `expr:"Commits"`
	// Verified reports whether every commit in Commits verified. An empty range verifies vacuously:
	// a promotion that adds no commits adds nothing unsigned.
	Verified bool `expr:"Verified"`
}

// VerificationCommit is one commit's signature verdict. Every field other than SHA and Verified is
// empty unless Verified, because git reports the issuer a signature claims even with no key to
// check that claim against.
type VerificationCommit struct {
	SHA      string `expr:"SHA"`
	Typ      string `expr:"Type"`
	KeyID    string `expr:"KeyID"`
	Signer   string `expr:"Signer"`
	Verified bool   `expr:"Verified"`
}

// processEnvironments processes each environment defined in the GitCommitStatus spec,
// evaluating expressions against the proposed hydrated commit for each environment.
// Returns a list of environment branches that transitioned from non-success to success
// and the CommitStatus objects created/updated.
func (r *GitCommitStatusReconciler) processEnvironments(ctx context.Context, gcs *promoterv1alpha1.GitCommitStatus, ps *promoterv1alpha1.PromotionStrategy) ([]string, []*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	// Save the previous status before clearing it, so we can detect transitions
	previousStatus := gcs.Status.DeepCopy()
	if previousStatus == nil {
		previousStatus = &promoterv1alpha1.GitCommitStatusStatus{}
	}

	// Build a map of environment statuses for efficient lookup
	envStatusMap := make(map[string]*promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for i := range ps.Status.Environments {
		envStatusMap[ps.Status.Environments[i].Branch] = &ps.Status.Environments[i]
	}

	// Get environments this GitCommitStatus applies to
	applicableEnvs := utils.GetApplicableEnvironments(ps, gcs.Spec.Key, constants.CommitRefProposed)

	// Signature verification is the only feature needing git access, so the repo is cloned only when
	// it is configured. Expressions see a nil Verification otherwise.
	var gitOperations *git.EnvironmentOperations
	var keyring *git.GPGKeyring
	if gcs.Spec.Verification != nil {
		var err error
		gitOperations, err = r.setupGitOperations(ctx, gcs, ps)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set up git operations: %w", err)
		}

		keyring, err = git.NewGPGKeyring(ctx, gpgPublicKeys(gcs.Spec.Verification))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to build GPG keyring: %w", err)
		}
		defer func() {
			if err := keyring.Close(); err != nil {
				logger.Error(err, "failed to remove temporary GPG keyring")
			}
		}()
	}

	// Initialize tracking variables
	transitionedEnvironments := make([]string, 0)
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(applicableEnvs))
	gcs.Status.Environments = make([]promoterv1alpha1.GitCommitStatusEnvironmentStatus, 0, len(applicableEnvs))

	for _, env := range applicableEnvs {
		branch := env.Branch

		// Look up the environment status
		envStatus, found := envStatusMap[branch]
		if !found {
			return nil, nil, fmt.Errorf("environment %q not found in PromotionStrategy status", branch)
		}

		// Get the proposed and active hydrated SHAs for this environment
		proposedSha := envStatus.Proposed.Hydrated.Sha
		activeHydratedSha := envStatus.Active.Hydrated.Sha

		// Determine which commit SHA to validate based on the Target field
		// The field is defaulted to "active" by the API server and validated to be "active" or "proposed"
		shaToValidate := activeHydratedSha
		if gcs.Spec.Target == constants.CommitRefProposed {
			shaToValidate = proposedSha
		}

		// Validate we have the SHA to work with - if PromotionStrategy hasn't fully reconciled,
		// the SHA might be empty which would cause git operations to fail
		if shaToValidate == "" {
			return nil, nil, fmt.Errorf("commit SHA not yet available for branch %q (target=%s): PromotionStrategy may not be fully reconciled", branch, gcs.Spec.Target)
		}

		// Get commit details for validation using the selected SHA
		commitData, err := r.getCommitData(ctx, gcs, ps, shaToValidate, branch)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get commit data for branch %q at SHA %q: %w", branch, shaToValidate, err)
		}

		var verification *VerificationData
		if keyring != nil {
			// Verification always covers the promotion range, independent of Target: Target selects
			// whose commit message the expression reads, but a promotion merges the whole range.
			verification, err = verifyPromotionRange(ctx, gitOperations, keyring,
				commitRef{sha: activeHydratedSha, branch: env.Branch},
				commitRef{sha: proposedSha, branch: utils.ProposedBranchName(ps, env)})
			if err != nil {
				return nil, nil, fmt.Errorf("failed to verify signatures for branch %q over range %q..%q: %w", branch, activeHydratedSha, proposedSha, err)
			}
		}

		// Evaluate the same expression for all environments
		phase, expressionResult, err := r.evaluateExpression(gcs.Spec.Expression, commitData, verification)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to evaluate expression for branch %q: %w", branch, err)
		}

		// Check if this validation transitioned to success
		var previousPhase string
		for _, prevEnv := range previousStatus.Environments {
			if prevEnv.Branch == branch {
				previousPhase = prevEnv.Phase
				break
			}
		}
		if previousPhase != string(promoterv1alpha1.CommitPhaseSuccess) && phase == promoterv1alpha1.CommitPhaseSuccess {
			transitionedEnvironments = append(transitionedEnvironments, branch)
			logger.Info("Validation transitioned to success",
				"branch", branch,
				"sha", proposedSha)
		}

		// Update status for this environment
		envValidationStatus := promoterv1alpha1.GitCommitStatusEnvironmentStatus{
			Branch:              branch,
			ProposedHydratedSha: proposedSha,
			ActiveHydratedSha:   activeHydratedSha,
			TargetedSha:         shaToValidate,
			Phase:               string(phase),
			ExpressionResult:    expressionResult,
		}
		gcs.Status.Environments = append(gcs.Status.Environments, envValidationStatus)

		// Create or update the CommitStatus for the proposed hydrated SHA
		// Use the same key from gcs.Spec.Key for all environments
		cs, err := utils.UpsertCommitStatus(ctx, r.Client, utils.UpsertCommitStatusParams{
			Parent:      gcs,
			RepoRefName: ps.Spec.RepositoryReference.Name,
			Branch:      branch,
			Sha:         proposedSha,
			Key:         gcs.Spec.Key,
			Description: gcs.Spec.Description,
			Phase:       phase,
			FieldOwner:  constants.GitCommitStatusControllerFieldOwner,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to upsert CommitStatus for environment %q: %w", branch, err)
		}
		commitStatuses = append(commitStatuses, cs)

		// Emit only after the upsert succeeded so the event always describes persisted state.
		emitCommitStatusPhaseChangedEvent(r.Recorder, gcs, gcs.Spec.Key, branch, previousPhase, string(phase))

		logger.Info("Processed environment validation",
			"branch", branch,
			"proposedSha", proposedSha,
			"targetedSha", shaToValidate,
			"target", gcs.Spec.Target,
			"phase", phase,
			"key", gcs.Spec.Key,
			"expression", gcs.Spec.Expression)
	}

	return transitionedEnvironments, commitStatuses, nil
}

// setupGitOperations clones the PromotionStrategy's repository under an identity owned by this
// GitCommitStatus. The clone is deliberately not shared with the ChangeTransferPolicy controller:
// EnvironmentOperations is not safe for concurrent use within a single identity, and the two
// controllers reconcile on independent workqueues.
func (r *GitCommitStatusReconciler) setupGitOperations(ctx context.Context, gcs *promoterv1alpha1.GitCommitStatus, ps *promoterv1alpha1.PromotionStrategy) (*git.EnvironmentOperations, error) {
	repoRef := ps.Spec.RepositoryReference

	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, r.SettingsMgr.GetControllerNamespace(), repoRef, gcs)
	if err != nil {
		return nil, fmt.Errorf("failed to get ScmProvider and secret for repo %q: %w", repoRef.Name, err)
	}

	gitAuthProvider, err := gitauth.CreateGitOperationsProvider(ctx, r.Client, scmProvider, secret, client.ObjectKey{Namespace: gcs.Namespace, Name: repoRef.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to create git auth provider for ScmProvider %q: %w", scmProvider.GetName(), err)
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{Namespace: gcs.Namespace, Name: repoRef.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository %q: %w", repoRef.Name, err)
	}

	gitOperations := git.NewEnvironmentOperations(gitRepo, gitAuthProvider, "gitcommitstatus/"+gcs.Namespace+"/"+gcs.Name)
	if err := gitOperations.CloneRepo(ctx); err != nil {
		return nil, fmt.Errorf("failed to clone repo %q: %w", repoRef.Name, err)
	}
	return gitOperations, nil
}

// commitRef is a SHA together with the branch it is reachable from, which is what a fetch needs when
// the SHA is not yet in the clone.
type commitRef struct {
	sha    string
	branch string
}

// verifyPromotionRange checks every commit the promotion would add — from's SHA exclusive to to's SHA
// inclusive — against keyring.
//
// An empty from.sha means the environment has never been promoted, so there is no verified boundary
// to walk back to and only to.sha is checked. Walking to the repository root instead would verify
// the entire hydrated history the first time an environment is gated.
func verifyPromotionRange(ctx context.Context, ops *git.EnvironmentOperations, keyring *git.GPGKeyring, from, to commitRef) (*VerificationData, error) {
	for _, ref := range []commitRef{from, to} {
		if ref.sha == "" {
			continue
		}
		present, err := ensureCommit(ctx, ops, ref)
		if err != nil {
			return nil, err
		}
		if !present {
			// The commit is unreachable from its branch, so refetching cannot recover it. Returning an
			// error here would requeue and refetch indefinitely, so report unverified and let the
			// expression fail closed until a new SHA arrives.
			log.FromContext(ctx).Info("commit not found after fetching branch, reporting the range as unverified",
				"sha", ref.sha, "branch", ref.branch)
			return &VerificationData{}, nil
		}
	}

	signatures, err := ops.VerifyCommitRange(ctx, from.sha, to.sha, keyring)
	if err != nil {
		return nil, fmt.Errorf("failed to verify range %q..%q: %w", from.sha, to.sha, err)
	}

	result := &VerificationData{
		Verified: true,
		Commits:  make([]VerificationCommit, 0, len(signatures)),
	}
	for _, sig := range signatures {
		result.Verified = result.Verified && sig.Verified
		result.Commits = append(result.Commits, VerificationCommit{
			SHA:      sig.SHA,
			Verified: sig.Verified,
			Typ:      sig.Type,
			KeyID:    sig.KeyID,
			Signer:   sig.Signer,
		})
	}
	return result, nil
}

// ensureCommit reports whether ref's SHA is in ops' clone, fetching its branch once if it is not.
//
// The clone is created once and never refreshed, so a SHA newer than the clone is absent locally.
// Because a SHA is immutable, presence is sufficient: only a miss requires fetching its branch.
func ensureCommit(ctx context.Context, ops *git.EnvironmentOperations, ref commitRef) (bool, error) {
	hasCommit, err := ops.HasCommit(ctx, ref.sha)
	if err != nil {
		return false, fmt.Errorf("failed to check for commit %q: %w", ref.sha, err)
	}
	if hasCommit {
		return true, nil
	}

	if err := ops.FetchBranch(ctx, ref.branch); err != nil {
		return false, fmt.Errorf("failed to fetch branch %q for commit %q: %w", ref.branch, ref.sha, err)
	}
	hasCommit, err = ops.HasCommit(ctx, ref.sha)
	if err != nil {
		return false, fmt.Errorf("failed to check for commit %q after fetching branch %q: %w", ref.sha, ref.branch, err)
	}
	return hasCommit, nil
}

func gpgPublicKeys(verification *promoterv1alpha1.GitCommitVerification) []string {
	if verification == nil || verification.GPG == nil {
		return nil
	}
	keys := make([]string, 0, len(verification.GPG.PublicKeys))
	for _, key := range verification.GPG.PublicKeys {
		keys = append(keys, key.Armored)
	}
	return keys
}

// getCommitData retrieves commit details from the PromotionStrategy status.
// This function pulls data from the already-computed status rather than making git calls.
func (r *GitCommitStatusReconciler) getCommitData(ctx context.Context, gcs *promoterv1alpha1.GitCommitStatus, ps *promoterv1alpha1.PromotionStrategy, sha string, branch string) (*CommitData, error) {
	logger := log.FromContext(ctx)

	// Find the environment status in the PromotionStrategy
	var envStatus *promoterv1alpha1.EnvironmentStatus
	for i := range ps.Status.Environments {
		if ps.Status.Environments[i].Branch == branch {
			envStatus = &ps.Status.Environments[i]
			break
		}
	}
	if envStatus == nil {
		return nil, fmt.Errorf("environment status for branch %q not found in PromotionStrategy", branch)
	}

	// Determine which commit state to use based on the target
	var commitState *promoterv1alpha1.CommitShaState
	if gcs.Spec.Target == constants.CommitRefProposed {
		commitState = &envStatus.Proposed.Hydrated
	} else {
		// Default to active
		commitState = &envStatus.Active.Hydrated
	}

	// Validate that the SHA matches what we expect
	if commitState.Sha != sha {
		return nil, fmt.Errorf("SHA mismatch: expected %q from PromotionStrategy status but got %q", commitState.Sha, sha)
	}

	// Parse trailers from the commit body without needing git operations.
	// The Body field contains everything after the subject line, including trailers.
	trailers, err := git.ParseTrailersFromMessage(ctx, commitState.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to parse trailers from commit message: %w", err)
	}

	logger.V(4).Info("Retrieved commit data from PromotionStrategy status",
		"sha", sha,
		"branch", branch,
		"target", gcs.Spec.Target,
		"subject", commitState.Subject,
		"author", commitState.Author,
		"trailerCount", len(trailers))

	return &CommitData{
		SHA:      sha,
		Subject:  commitState.Subject,
		Body:     commitState.Body,
		Author:   commitState.Author,
		Trailers: trailers,
	}, nil
}

// getCompiledExpression retrieves a compiled expression from the cache or compiles and caches it.
// This avoids recompiling the same expression on every reconciliation.
func (r *GitCommitStatusReconciler) getCompiledExpression(expression string) (*vm.Program, error) {
	// Check cache first
	if cached, ok := r.expressionCache.Load(expression); ok {
		program, ok := cached.(*vm.Program)
		if !ok {
			return nil, errors.New("cached value is not a *vm.Program")
		}
		return program, nil
	}

	// Compile with type information (using nil pointer provides type info without actual data)
	exprData := map[string]any{
		"Commit":       (*CommitData)(nil),
		"Verification": (*VerificationData)(nil),
	}
	program, err := expr.Compile(expression, expr.Env(exprData), expr.AsBool())
	if err != nil {
		return nil, fmt.Errorf("failed to compile expression: %w", err)
	}

	// Store in cache
	r.expressionCache.Store(expression, program)
	return program, nil
}

// evaluateExpression evaluates the configured expression against commit data.
// Returns the phase (success/failure) and the boolean result.
//
// A nil verification is passed through as a typed nil so that `Verification == nil` holds for
// resources that do not configure spec.verification.
func (r *GitCommitStatusReconciler) evaluateExpression(expression string, commitData *CommitData, verification *VerificationData) (promoterv1alpha1.CommitStatusPhase, *bool, error) {
	// Get compiled expression from cache or compile it
	program, err := r.getCompiledExpression(expression)
	if err != nil {
		return "", nil, fmt.Errorf("failed to compile expression: %w", err)
	}

	// Run the expression with actual commit data
	exprData := map[string]any{
		"Commit":       commitData,
		"Verification": verification,
	}
	output, err := expr.Run(program, exprData)
	if err != nil {
		return "", nil, fmt.Errorf("failed to evaluate expression: %w", err)
	}

	// Check the result
	result, ok := output.(bool)
	if !ok {
		return "", nil, fmt.Errorf("expression must return boolean, got %T", output)
	}

	if result {
		return promoterv1alpha1.CommitPhaseSuccess, new(true), nil
	}
	return promoterv1alpha1.CommitPhaseFailure, new(false), nil
}

// enqueueGitCommitStatusForPromotionStrategy returns a handler that enqueues all GitCommitStatus resources
// that reference a PromotionStrategy when that PromotionStrategy changes.
func (r *GitCommitStatusReconciler) enqueueGitCommitStatusForPromotionStrategy() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
		if !ok {
			return nil
		}

		var gcsList promoterv1alpha1.GitCommitStatusList
		if err := r.List(ctx, &gcsList,
			client.InNamespace(ps.Namespace),
			client.MatchingFields{PromotionStrategyRefField: ps.Name},
		); err != nil {
			log.FromContext(ctx).Error(err, "failed to list GitCommitStatus resources")
			return nil
		}

		requests := make([]ctrl.Request, 0, len(gcsList.Items))
		for i := range gcsList.Items {
			requests = append(requests, ctrl.Request{
				NamespacedName: client.ObjectKeyFromObject(&gcsList.Items[i]),
			})
		}

		return requests
	})
}
