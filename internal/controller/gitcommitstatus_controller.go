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

	"github.com/argoproj-labs/gitops-promoter/internal/git"
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

	// EnqueueCTP is a function to enqueue CTP reconcile requests without modifying the CTP object.
	EnqueueCTP CTPEnqueueFunc

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
// 2. Retrieves commit details (message, author, trailers) from the PromotionStrategy status
// 3. Evaluates the configured expression against the commit data
// 4. Creates/updates a CommitStatus resource with the validation result
func (r *GitCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling GitCommitStatus", "name", req.Name)
	startTime := time.Now()

	var gcs promoterv1alpha1.GitCommitStatus
	// This function will update the resource status at the end of the reconciliation. don't call .Status().Update manually.
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
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", gcs.Spec.PromotionStrategyRef.Name, err)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", gcs.Spec.PromotionStrategyRef.Name, err)
	}

	// Process each environment and evaluate expressions
	transitionedEnvironments, commitStatuses, err := r.processEnvironments(ctx, &gcs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to process environments: %w", err)
	}

	// Inherit conditions from CommitStatus objects
	utils.InheritNotReadyConditionFromObjects(&gcs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	// If any validations transitioned to success, touch the corresponding ChangeTransferPolicies
	if len(transitionedEnvironments) > 0 {
		r.touchChangeTransferPolicies(ctx, &ps, transitionedEnvironments)
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
	Trailers map[string][]string `expr:"Trailers"`
	SHA      string              `expr:"SHA"`
	Subject  string              `expr:"Subject"`
	Body     string              `expr:"Body"`
	Author   string              `expr:"Author"`
}

// getApplicableEnvironments returns the environments from the PromotionStrategy that this GitCommitStatus applies to.
// An environment is applicable if the GitCommitStatus key is referenced in either:
// - The global ps.Spec.ProposedCommitStatuses, or
// - The environment-specific psEnv.ProposedCommitStatuses
func (r *GitCommitStatusReconciler) getApplicableEnvironments(ps *promoterv1alpha1.PromotionStrategy, key string) []promoterv1alpha1.Environment {
	// Check if globally referenced
	globallyProposed := false
	for _, selector := range ps.Spec.ProposedCommitStatuses {
		if selector.Key == key {
			globallyProposed = true
			break
		}
	}

	applicable := make([]promoterv1alpha1.Environment, 0, len(ps.Spec.Environments))
	for _, env := range ps.Spec.Environments {
		if globallyProposed {
			applicable = append(applicable, env)
			continue
		}
		for _, selector := range env.ProposedCommitStatuses {
			if selector.Key == key {
				applicable = append(applicable, env)
				break
			}
		}
	}
	return applicable
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
	applicableEnvs := r.getApplicableEnvironments(ps, gcs.Spec.Key)

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
		if gcs.Spec.Target == "proposed" {
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

		// Evaluate the same expression for all environments
		phase, expressionResult, err := r.evaluateExpression(gcs.Spec.Expression, commitData)
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
		if previousPhase != string(promoterv1alpha1.CommitPhaseSuccess) && phase == string(promoterv1alpha1.CommitPhaseSuccess) {
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
			Phase:               phase,
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
			"targetedSha", shaToValidate,
			"target", gcs.Spec.Target,
			"phase", phase,
			"key", gcs.Spec.Key,
			"expression", gcs.Spec.Expression)
	}

	return transitionedEnvironments, commitStatuses, nil
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
	if gcs.Spec.Target == "proposed" {
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
// Returns the phase (success/failure) and the boolean result.
func (r *GitCommitStatusReconciler) evaluateExpression(expression string, commitData *CommitData) (string, *bool, error) {
	// Get compiled expression from cache or compile it
	program, err := r.getCompiledExpression(expression)
	if err != nil {
		return "", nil, fmt.Errorf("failed to compile expression: %w", err)
	}

	// Run the expression with actual commit data
	env := map[string]any{
		"Commit": commitData,
	}
	output, err := expr.Run(program, env)
	if err != nil {
		return "", nil, fmt.Errorf("failed to evaluate expression: %w", err)
	}

	// Check the result
	result, ok := output.(bool)
	if !ok {
		return "", nil, fmt.Errorf("expression must return boolean, got %T", output)
	}

	if result {
		return string(promoterv1alpha1.CommitPhaseSuccess), ptr.To(true), nil
	}
	return string(promoterv1alpha1.CommitPhaseFailure), ptr.To(false), nil
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
		case string(promoterv1alpha1.CommitPhaseSuccess):
			commitPhase = promoterv1alpha1.CommitPhaseSuccess
		case string(promoterv1alpha1.CommitPhaseFailure):
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

// touchChangeTransferPolicies triggers reconciliation of the ChangeTransferPolicies
// for the environments that had validations transition to success.
// This triggers the ChangeTransferPolicy controller to reconcile and potentially merge PRs.
func (r *GitCommitStatusReconciler) touchChangeTransferPolicies(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, transitionedEnvironments []string) {
	logger := log.FromContext(ctx)

	// For each transitioned environment, trigger reconciliation of the corresponding ChangeTransferPolicy
	for _, envBranch := range transitionedEnvironments {
		// Generate the ChangeTransferPolicy name using the same logic as the PromotionStrategy controller
		ctpName := utils.KubeSafeUniqueName(ctx, utils.GetChangeTransferPolicyName(ps.Name, envBranch))

		logger.Info("Triggering ChangeTransferPolicy reconciliation due to validation transition",
			"changeTransferPolicy", ctpName,
			"branch", envBranch)

		// Use the enqueue function to trigger reconciliation.
		if r.EnqueueCTP != nil {
			r.EnqueueCTP(ps.Namespace, ctpName)
		}
	}
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
