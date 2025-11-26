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
	"sync"
	"time"

	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/gitauth"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// GitCommitStatusReconciler reconciles a GitCommitStatus object
type GitCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	SettingsMgr *settings.Manager

	// expressionCache caches compiled expressions to avoid recompilation on every reconciliation
	// Key: expression string, Value: compiled *vm.Program
	expressionCache sync.Map
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitcommitstatuses/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For each configured environment in the GitCommitStatus, the controller:
// 1. Fetches the PromotionStrategy to get the proposed hydrated commit SHA
// 2. Retrieves commit details (message, author, trailers) from git
// 3. Evaluates the configured expression against the commit data
// 4. Creates/updates a CommitStatus resource with the validation result
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.22.4/pkg/reconcile
func (r *GitCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling GitCommitStatus", "name", req.Name)
	startTime := time.Now()

	var gcs promoterv1alpha1.GitCommitStatus
	defer utils.HandleReconciliationResult(ctx, startTime, &gcs, r.Client, r.Recorder, &err)

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
	meta.RemoveStatusCondition(gcs.GetConditions(), string(promoterConditions.Ready))

	// Fetch the referenced PromotionStrategy
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{
		Namespace: gcs.Namespace,
		Name:      gcs.Spec.PromotionStrategyRef.Name,
	}
	err = r.Get(ctx, psKey, &ps)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Error(err, "referenced PromotionStrategy not found", "promotionStrategy", gcs.Spec.PromotionStrategyRef.Name)
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", gcs.Spec.PromotionStrategyRef.Name, err)
		}
		logger.Error(err, "failed to get PromotionStrategy")
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", gcs.Spec.PromotionStrategyRef.Name, err)
	}

	// Process each environment and evaluate expressions
	transitionedEnvironments, commitStatuses, err := r.processEnvironments(ctx, &gcs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to process environments: %w", err)
	}

	// Inherit conditions from CommitStatus objects
	utils.InheritNotReadyConditionFromObjects(&gcs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	// Update status
	err = r.Status().Update(ctx, &gcs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update GitCommitStatus status: %w", err)
	}

	// If any validations transitioned to success, touch the corresponding ChangeTransferPolicies
	if len(transitionedEnvironments) > 0 {
		err = touchChangeTransferPolicies(ctx, r.Client, &ps, transitionedEnvironments, "validation transition")
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to touch ChangeTransferPolicies: %w", err)
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
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

// CommitData represents the data structure available to expressions during validation.
type CommitData struct {
	Trailers  map[string][]string `expr:"Trailers"`
	SHA       string              `expr:"SHA"`
	Subject   string              `expr:"Subject"`
	Body      string              `expr:"Body"`
	Author    string              `expr:"Author"`
	Committer string              `expr:"Committer"`
}

// processEnvironments processes each environment defined in the GitCommitStatus spec,
// evaluating expressions against the proposed hydrated commit for each environment.
// Returns a list of environment branches that transitioned from non-success to success
// and the CommitStatus objects created/updated.
func (r *GitCommitStatusReconciler) processEnvironments(ctx context.Context, gcs *promoterv1alpha1.GitCommitStatus, ps *promoterv1alpha1.PromotionStrategy) ([]string, []*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	// Track which environments transitioned to success
	transitionedEnvironments := []string{}
	// Track all CommitStatus objects created/updated
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(ps.Spec.Environments))

	// Save the previous status before clearing it, so we can detect transitions
	previousStatus := gcs.Status.DeepCopy()
	if previousStatus == nil {
		previousStatus = &promoterv1alpha1.GitCommitStatusStatus{}
	}

	// Build a map of environments from PromotionStrategy for efficient lookup
	envStatusMap := make(map[string]*promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for i := range ps.Status.Environments {
		envStatusMap[ps.Status.Environments[i].Branch] = &ps.Status.Environments[i]
	}

	// Initialize or clear the environments status
	gcs.Status.Environments = make([]promoterv1alpha1.GitCommitStatusEnvironmentStatus, 0, len(ps.Spec.Environments))

	// Check if this GitCommitStatus is referenced in global proposedCommitStatuses
	globallyProposed := false
	for _, selector := range ps.Spec.ProposedCommitStatuses {
		if selector.Key == gcs.Spec.Key {
			globallyProposed = true
			break
		}
	}

	// Iterate over ALL environments from the PromotionStrategy
	for _, psEnv := range ps.Spec.Environments {
		branch := psEnv.Branch

		// Check if this GitCommitStatus applies to this environment
		// Start with global proposedCommitStatuses
		appliesToEnvironment := globallyProposed

		// Check environment-specific proposedCommitStatuses
		if !appliesToEnvironment {
			for _, selector := range psEnv.ProposedCommitStatuses {
				if selector.Key == gcs.Spec.Key {
					appliesToEnvironment = true
					break
				}
			}
		}

		// Skip this environment if the GitCommitStatus doesn't apply
		if !appliesToEnvironment {
			logger.V(4).Info("GitCommitStatus does not apply to environment, skipping",
				"branch", branch,
				"key", gcs.Spec.Key)
			continue
		}

		// Look up the environment status in the map
		envStatus, found := envStatusMap[branch]
		if !found {
			logger.Info("Environment not found in PromotionStrategy status", "branch", branch)
			continue
		}

		// Get the proposed and active hydrated SHAs for this environment
		proposedSha := envStatus.Proposed.Hydrated.Sha
		activeHydratedSha := envStatus.Active.Hydrated.Sha

		// Determine which commit SHA to validate based on the ValidateCommit field
		// Default to "active" for backward compatibility
		validateCommitMode := gcs.Spec.ValidateCommit
		if validateCommitMode == "" {
			validateCommitMode = "active"
		}

		var shaToValidate string
		if validateCommitMode == "proposed" {
			shaToValidate = proposedSha
		} else {
			// Default to active for "active" or any unknown values
			shaToValidate = activeHydratedSha
		}

		// Validate we have the SHA to work with - if PromotionStrategy hasn't fully reconciled,
		// the SHA might be empty which would cause git operations to fail
		if shaToValidate == "" {
			logger.V(4).Info("Commit SHA not yet available",
				"branch", branch,
				"validateCommit", validateCommitMode)
			gcs.Status.Environments = append(gcs.Status.Environments, promoterv1alpha1.GitCommitStatusEnvironmentStatus{
				Branch:              branch,
				ProposedHydratedSha: proposedSha,
				ActiveHydratedSha:   activeHydratedSha,
				ValidatedSha:        "",
				Phase:               "pending",
				ExpressionMessage:   fmt.Sprintf("Waiting for %s commit SHA", validateCommitMode),
			})
			continue
		}

		// Get commit details for validation using the selected SHA
		commitData, err := r.getCommitData(ctx, gcs, ps, shaToValidate, branch)
		if err != nil {
			logger.Error(err, "Failed to get commit data",
				"branch", branch,
				"sha", shaToValidate,
				"validateCommit", validateCommitMode)
			// Add a pending status entry with error message
			gcs.Status.Environments = append(gcs.Status.Environments, promoterv1alpha1.GitCommitStatusEnvironmentStatus{
				Branch:              branch,
				ProposedHydratedSha: proposedSha,
				ActiveHydratedSha:   activeHydratedSha,
				ValidatedSha:        shaToValidate,
				Phase:               "pending",
				ExpressionMessage:   fmt.Sprintf("Failed to fetch commit data: %v", err),
			})
			continue
		}

		// Evaluate the same expression for all environments
		phase, message, expressionResult := r.evaluateExpression(ctx, gcs.Spec.Expression, commitData)

		// Check if this validation transitioned to success
		var previousPhase string
		for _, prevEnv := range previousStatus.Environments {
			if prevEnv.Branch == branch {
				previousPhase = prevEnv.Phase
				break
			}
		}
		if previousPhase != CommitPhaseSuccess && phase == CommitPhaseSuccess {
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
			ValidatedSha:        shaToValidate,
			Phase:               phase,
			ExpressionMessage:   message,
			ExpressionResult:    expressionResult,
		}
		gcs.Status.Environments = append(gcs.Status.Environments, envValidationStatus)

		// Create or update the CommitStatus for the proposed hydrated SHA
		// Use the same key from gcs.Spec.Key for all environments
		cs, err := r.upsertCommitStatus(ctx, gcs, ps, branch, proposedSha, phase, gcs.Spec.Key)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to upsert CommitStatus for environment %q: %w", branch, err)
		}
		commitStatuses = append(commitStatuses, cs)

		logger.Info("Processed environment validation",
			"branch", branch,
			"proposedSha", proposedSha,
			"validatedSha", shaToValidate,
			"validateCommit", validateCommitMode,
			"phase", phase,
			"key", gcs.Spec.Key,
			"expression", gcs.Spec.Expression)
	}

	return transitionedEnvironments, commitStatuses, nil
}

// getCommitData retrieves commit details from git for the given SHA.
func (r *GitCommitStatusReconciler) getCommitData(ctx context.Context, gcs *promoterv1alpha1.GitCommitStatus, ps *promoterv1alpha1.PromotionStrategy, sha string, branch string) (*CommitData, error) {
	// Get the GitRepository and SCM provider
	gitAuthProvider, repositoryRef, err := r.getGitAuthProvider(ctx, gcs, ps)
	if err != nil {
		return nil, fmt.Errorf("failed to get git auth provider: %w", err)
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{Namespace: gcs.GetNamespace(), Name: repositoryRef.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	// Create environment operations for git access
	envOps := git.NewEnvironmentOperations(gitRepo, gitAuthProvider, branch)

	// Clone the repo if needed
	err = envOps.CloneRepo(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to clone repository: %w", err)
	}

	// We don't need to explicitly fetch/checkout since GetShaMetadataFromGit will work with the SHA directly

	// Get commit metadata
	commitMeta, err := envOps.GetShaMetadataFromGit(ctx, sha)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit metadata for SHA %q: %w", sha, err)
	}

	// Get author and committer emails
	authorEmail, err := r.getCommitAuthorEmail(ctx, envOps, sha)
	if err != nil {
		return nil, fmt.Errorf("failed to get author email: %w", err)
	}

	committerEmail, err := r.getCommitCommitterEmail(ctx, envOps, sha)
	if err != nil {
		return nil, fmt.Errorf("failed to get committer email: %w", err)
	}

	// Get trailers using git interpret-trailers
	gitTrailers, err := envOps.GetTrailers(ctx, sha)
	if err != nil {
		return nil, fmt.Errorf("failed to get trailers: %w", err)
	}

	// Convert map[string]string to map[string][]string for compatibility
	trailers := make(map[string][]string, len(gitTrailers))
	for key, value := range gitTrailers {
		trailers[key] = []string{value}
	}

	return &CommitData{
		SHA:       sha,
		Subject:   commitMeta.Subject,
		Body:      commitMeta.Body,
		Author:    authorEmail,
		Committer: committerEmail,
		Trailers:  trailers,
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
	env := map[string]any{
		"Commit": (*CommitData)(nil),
	}
	program, err := expr.Compile(expression, expr.Env(env), expr.AsBool())
	if err != nil {
		return nil, fmt.Errorf("failed to compile expression: %w", err)
	}

	// Store in cache
	r.expressionCache.Store(expression, program)
	return program, nil
}

// evaluateExpression evaluates the configured expression against commit data.
// Returns the phase (success/failure/pending), a message, and the boolean result.
func (r *GitCommitStatusReconciler) evaluateExpression(ctx context.Context, expression string, commitData *CommitData) (string, string, *bool) {
	logger := log.FromContext(ctx)

	// Get compiled expression from cache or compile it
	program, err := r.getCompiledExpression(expression)
	if err != nil {
		logger.Error(err, "Failed to compile expression", "expression", expression)
		return CommitPhaseFailure, fmt.Sprintf("Expression compilation failed: %v", err), nil
	}

	// Run the expression with actual commit data
	env := map[string]any{
		"Commit": commitData,
	}
	output, err := expr.Run(program, env)
	if err != nil {
		logger.Error(err, "Failed to evaluate expression", "expression", expression)
		return CommitPhaseFailure, fmt.Sprintf("Expression evaluation failed: %v", err), nil
	}

	// Check the result
	result, ok := output.(bool)
	if !ok {
		logger.Error(errors.New("expression did not return boolean"), "Invalid expression result type",
			"expression", expression, "resultType", reflect.TypeOf(output))
		return CommitPhaseFailure, fmt.Sprintf("Expression must return boolean, got %T", output), nil
	}

	if result {
		return CommitPhaseSuccess, "Expression evaluated to true", ptr.To(true)
	}
	return CommitPhaseFailure, "Expression evaluated to false", ptr.To(false)
}

// upsertCommitStatus creates or updates a CommitStatus resource for the validation result.
func (r *GitCommitStatusReconciler) upsertCommitStatus(ctx context.Context, gcs *promoterv1alpha1.GitCommitStatus, ps *promoterv1alpha1.PromotionStrategy, branch, sha, phase, validationName string) (*promoterv1alpha1.CommitStatus, error) {
	// Generate a consistent name for the CommitStatus
	commitStatusName := utils.KubeSafeUniqueName(ctx, fmt.Sprintf("%s-%s-%s", gcs.Name, branch, validationName))

	commitStatus := promoterv1alpha1.CommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      commitStatusName,
			Namespace: gcs.Namespace,
		},
	}

	// Create or update the CommitStatus
	_, err := ctrl.CreateOrUpdate(ctx, r.Client, &commitStatus, func() error {
		// Set owner reference to the GitCommitStatus
		if err := ctrl.SetControllerReference(gcs, &commitStatus, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference: %w", err)
		}

		// Set labels for easy identification
		if commitStatus.Labels == nil {
			commitStatus.Labels = make(map[string]string)
		}
		commitStatus.Labels["promoter.argoproj.io/git-commit-status"] = utils.KubeSafeLabel(gcs.Name)
		commitStatus.Labels[promoterv1alpha1.EnvironmentLabel] = utils.KubeSafeLabel(branch)
		commitStatus.Labels[promoterv1alpha1.CommitStatusLabel] = validationName

		// Convert phase string to CommitStatusPhase
		var commitPhase promoterv1alpha1.CommitStatusPhase
		switch phase {
		case "success":
			commitPhase = promoterv1alpha1.CommitPhaseSuccess
		case "failure":
			commitPhase = promoterv1alpha1.CommitPhaseFailure
		default:
			commitPhase = promoterv1alpha1.CommitPhasePending
		}

		// Set the spec
		commitStatus.Spec.RepositoryReference = ps.Spec.RepositoryReference
		commitStatus.Spec.Name = validationName + "/" + branch
		commitStatus.Spec.Description = gcs.Spec.Description
		commitStatus.Spec.Phase = commitPhase
		commitStatus.Spec.Sha = sha

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create or update CommitStatus: %w", err)
	}

	return &commitStatus, nil
}

// getGitAuthProvider retrieves the git authentication provider for accessing the repository.
func (r *GitCommitStatusReconciler) getGitAuthProvider(ctx context.Context, gcs *promoterv1alpha1.GitCommitStatus, ps *promoterv1alpha1.PromotionStrategy) (scms.GitOperationsProvider, promoterv1alpha1.ObjectReference, error) {
	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, r.SettingsMgr.GetControllerNamespace(), ps.Spec.RepositoryReference, gcs)
	if err != nil {
		return nil, ps.Spec.RepositoryReference, fmt.Errorf("failed to get ScmProvider and secret for repo %q: %w", ps.Spec.RepositoryReference.Name, err)
	}

	provider, err := gitauth.CreateGitOperationsProvider(ctx, r.Client, scmProvider, secret, client.ObjectKey{Namespace: gcs.Namespace, Name: ps.Spec.RepositoryReference.Name})
	if err != nil {
		return nil, ps.Spec.RepositoryReference, fmt.Errorf("failed to create git operations provider: %w", err)
	}

	return provider, ps.Spec.RepositoryReference, nil
}

// enqueueGitCommitStatusForPromotionStrategy returns a handler that enqueues all GitCommitStatus resources
// that reference a PromotionStrategy when that PromotionStrategy changes.
func (r *GitCommitStatusReconciler) enqueueGitCommitStatusForPromotionStrategy() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
		if !ok {
			return nil
		}

		// List all GitCommitStatus resources in the same namespace
		var gcsList promoterv1alpha1.GitCommitStatusList
		if err := r.List(ctx, &gcsList, client.InNamespace(ps.Namespace)); err != nil {
			log.FromContext(ctx).Error(err, "failed to list GitCommitStatus resources")
			return nil
		}

		// Enqueue all GitCommitStatus resources that reference this PromotionStrategy
		var requests []ctrl.Request
		for _, gcs := range gcsList.Items {
			if gcs.Spec.PromotionStrategyRef.Name == ps.Name {
				requests = append(requests, ctrl.Request{
					NamespacedName: client.ObjectKeyFromObject(&gcs),
				})
			}
		}

		return requests
	})
}

// getCommitAuthorEmail retrieves the author email for a commit.
func (r *GitCommitStatusReconciler) getCommitAuthorEmail(ctx context.Context, envOps *git.EnvironmentOperations, sha string) (string, error) {
	email, err := envOps.GitShow(ctx, sha, "%ae")
	if err != nil {
		return "", fmt.Errorf("failed to get author email: %w", err)
	}
	return email, nil
}

// getCommitCommitterEmail retrieves the committer email for a commit.
func (r *GitCommitStatusReconciler) getCommitCommitterEmail(ctx context.Context, envOps *git.EnvironmentOperations, sha string) (string, error) {
	email, err := envOps.GitShow(ctx, sha, "%ce")
	if err != nil {
		return "", fmt.Errorf("failed to get committer email: %w", err)
	}
	return email, nil
}
