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
	"fmt"
	"sync"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/bitbucket_cloud"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/bitbucket_datacenter"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/forgejo"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitea"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitlab"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// SCMCheckCommitStatusReconciler reconciles a SCMCheckCommitStatus object
type SCMCheckCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager

	// EnqueueCTP is a function to enqueue CTP reconcile requests without modifying the CTP object
	EnqueueCTP CTPEnqueueFunc

	// expressionCache caches compiled expressions for filters to avoid it being recompiled on every reconcilation.
	// Key = expression string
	// Value = compiled *vm.Program
	expressionCache sync.Map
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmcheckcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmcheckcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmcheckcommitstatuses/finalizers,verbs=update
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses,verbs=get;list;watch;patch;create;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.25.0/pkg/reconcile
func (r *SCMCheckCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling SCMCheckCommitStatus", "name", req.Name)
	startTime := time.Now()

	var scs promoterv1alpha1.SCMCheckCommitStatus
	// This function applies the resource status via Server-Side Apply at the end of the reconcilation. Don't write status manually.
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &scs, r.Client, r.Recorder, constants.SCMCheckCommitStatusControllerFieldOwner, &result, &err, &previousReady)

	err = r.Get(ctx, req.NamespacedName, &scs, &client.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("SCMCheckCommitStatus not found")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "failed to get SCMCheckCommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get SCMCheckCommitStatus %q: %w", req.Name, err)
	}

	// Remove any existing Ready condition.
	previousReady = utils.RemoveReadyCondition(&scs)

	if err := ensureControllerInstanceIDStable(ctx, r.SettingsMgr); err != nil {
		return ctrl.Result{}, err
	}

	// Get the promotion strategy
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{
		Namespace: scs.Namespace,
		Name:      scs.Spec.PromotionStrategyRef.Name,
	}
	err = r.Get(ctx, psKey, &ps)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("referenced promotion strategy %s not found: %w", scs.Spec.PromotionStrategyRef.Name, err)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get promotion strategy %s: %w", scs.Spec.PromotionStrategyRef.Name, err)
	}

	// Process each environment by checking their checks on the PR
	transitionedEnvironments, commitStatuses, err := r.processEnvironments(ctx, &scs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to process environments: %w", err)
	}

	err = utils.CleanupOrphanedCommitStatuses(ctx, r.Client, r.Recorder, &scs, commitStatuses)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to cleanup orphaned CommitStatus resources: %w", err)
	}

	// Inherit conditions from CommitStatus objects
	utils.InheritNotReadyConditionFromObjects(&scs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	// If any validations transitioned to success, enqueue the corresponding ChangeTransferPolicies
	utils.EnqueueChangeTransferPolicies(ctx, r.EnqueueCTP, &ps, transitionedEnvironments, "validation transition")

	// Get the requeue duration from the controller configuration for requeue
	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.SCMCheckCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration for SCMCheckCommitStatus %s: %w", scs.Name, err)
	}

	return ctrl.Result{RequeueAfter: requeueDuration}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *SCMCheckCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.SCMCheckCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get SCMCheckCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.SCMCheckCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get SCMCheckCommitStatus max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.SCMCheckCommitStatus{}).
		Watches(&promoterv1alpha1.PromotionStrategy{}, CommitStatusGatePromotionStrategyWatchHandler[promoterv1alpha1.SCMCheckCommitStatusList](r.Client)).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Named("scmcheckcommitstatus").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

// processEnvironments processes each environment if it is applicable and checks the SCM's check on the open PR
// to create CommitStatuses for.
func (r *SCMCheckCommitStatusReconciler) processEnvironments(ctx context.Context, scs *promoterv1alpha1.SCMCheckCommitStatus, ps *promoterv1alpha1.PromotionStrategy) ([]string, []*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	applicableEnvs := utils.GetApplicableEnvironments(ps, scs.Spec.Key, constants.CommitRefProposed)

	envStatusMap := make(map[string]*promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for i := range ps.Status.Environments {
		envStatusMap[ps.Status.Environments[i].Branch] = &ps.Status.Environments[i]
	}

	var previousStatus promoterv1alpha1.SCMCheckCommitStatusStatus
	scs.Status.DeepCopyInto(&previousStatus)

	checkParser, err := r.getCheckParser(ctx, ps)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get check parser for scm: %w", err)
	}

	transitionedEnvs := make([]string, 0)
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(ps.Status.Environments))
	scs.Status.Environments = make([]promoterv1alpha1.SCMCheckCommitStatusEnvironmentStatus, 0, len(applicableEnvs))

	for _, env := range applicableEnvs {
		envStatus, ok := envStatusMap[env.Branch]
		if !ok {
			logger.Info("environment %s not found in promotion strategy status", "branch", env.Branch)
			continue
		}

		proposedSha := envStatus.Proposed.Hydrated.Sha
		if proposedSha == "" {
			logger.Info("proposed hydrated sha is empty for environment %s", "branch", env.Branch)
			continue
		}

		checkResults, err := checkParser.Get(ctx, *ps, env.Branch)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get checks for environment %s: %w", env.Branch, err)
		}

		filteredResults, err := r.filterChecks(scs, checkResults)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to filter checks: %w", err)
		}

		checksToUse := filteredResults
		if len(filteredResults) == 0 && len(scs.Spec.Filters) != 0 {
			logger.Info("No checks matched any of the filters provided")
		} else if len(filteredResults) == 0 {
			checksToUse = checkResults
		}

		newEnvStatus := r.buildEnvironmentStatus(checkResults, filteredResults, env.Branch, proposedSha, len(scs.Spec.Filters) > 0)

		// Check if the new status transitioned
		var previousPhase string
		for _, prevEnv := range previousStatus.Environments {
			if prevEnv.Branch == env.Branch {
				previousPhase = prevEnv.Phase
				break
			}
		}
		if previousPhase != string(promoterv1alpha1.CommitPhaseSuccess) &&
			promoterv1alpha1.CommitStatusPhase(newEnvStatus.Phase) == promoterv1alpha1.CommitPhaseSuccess {
			transitionedEnvs = append(transitionedEnvs, env.Branch)
			logger.Info("SCM Checks are now all successful", "branch", env.Branch, "sha", proposedSha)
		}

		scs.Status.Environments = append(scs.Status.Environments, newEnvStatus)

		cs, err := r.upsertCheckCommitStatus(ctx, scs, ps, env.Branch, proposedSha, checksToUse, promoterv1alpha1.CommitStatusPhase(newEnvStatus.Phase))
		if err != nil {
			return nil, nil, fmt.Errorf("failed upserting commit statuses: %w", err)
		}
		commitStatuses = append(commitStatuses, cs)

		// Emit only after the upsert succeeded so the event always describes persisted state.
		emitCommitStatusPhaseChangedEvent(r.Recorder, scs, scs.Spec.Key, env.Branch, previousPhase, newEnvStatus.Phase)

		logger.Info("Processed SCM checks for environment",
			"branch", env.Branch,
			"proposedSha", proposedSha,
			"phase", newEnvStatus.Phase,
			"key", scs.Spec.Key)

	}

	return transitionedEnvs, commitStatuses, nil
}

// getCheckParser returns which CheckParser needs to be used based on the scm found in the repository reference in the PromotionStrategy.
func (r *SCMCheckCommitStatusReconciler) getCheckParser(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy) (scms.CheckParser, error) {
	scmProvider, secret, gitRepository, err := utils.GetScmProviderSecretAndGitRepositoryFromRepositoryReference(
		ctx, r.Client, r.SettingsMgr, ps.Spec.RepositoryReference, ps)
	if err != nil {
		return nil, fmt.Errorf("failed to get CheckParser: %w", err)
	}

	// wrapcheck is ignored because factory methods return descriptive errors
	switch {
	case scmProvider.GetSpec().GitHub != nil:
		return github.NewGithubCheckParser(ctx, r.Client, scmProvider, *secret, gitRepository.Spec.GitHub.Owner) //nolint:wrapcheck
	case scmProvider.GetSpec().GitLab != nil:
		return gitlab.NewGitLabCheckParser(ctx, r.Client, *secret, scmProvider.GetSpec().GitLab.Domain) //nolint:wrapcheck
	case scmProvider.GetSpec().BitbucketCloud != nil:
		return bitbucket_cloud.NewBitbucketCloudCheckParser(ctx, r.Client, *secret, scmProvider.GetSpec().BitbucketCloud.Domain) //nolint:wrapcheck
	case scmProvider.GetSpec().BitbucketDataCenter != nil:
		return bitbucket_datacenter.NewBitbucketDataCenterCheckParser(ctx, r.Client, scmProvider, *secret) //nolint:wrapcheck
	case scmProvider.GetSpec().Forgejo != nil:
		return forgejo.NewForgejoCheckParser(ctx, r.Client, *secret, scmProvider.GetSpec().Forgejo.Domain) //nolint:wrapcheck
	case scmProvider.GetSpec().Gitea != nil:
		return gitea.NewGiteaCheckParser(ctx, r.Client, *secret, scmProvider.GetSpec().Gitea.Domain) //nolint:wrapcheck
	case scmProvider.GetSpec().AzureDevOps != nil:
		return azuredevops.NewAzdoCheckParser(ctx, r.Client, scmProvider, *secret, scmProvider.GetSpec().AzureDevOps.Organization) //nolint:wrapcheck
	default:
		return nil, fmt.Errorf("unsupported SCM provider: %s", scmProvider.GetName())
	}
}

// filterChecks filters the returned checks to only include checks based on the filters set in the SCMCheckCommitStatus.
func (r *SCMCheckCommitStatusReconciler) filterChecks(scs *promoterv1alpha1.SCMCheckCommitStatus, checks []scms.SCMCheckResult) ([]scms.SCMCheckResult, error) {
	filteredChecks := []scms.SCMCheckResult{}
	for _, check := range checks {
		matches, err := r.checkMatches(check.Name, scs.Spec.Filters)
		if err != nil {
			return nil, fmt.Errorf("failed to match check with filters: %w", err)
		}
		if matches {
			filteredChecks = append(filteredChecks, check)
		}
	}
	return filteredChecks, nil
}

// checkMatches checks if the check matches any of the filters
func (r *SCMCheckCommitStatusReconciler) checkMatches(name string, filters []string) (bool, error) {
	for _, filter := range filters {
		matches, err := r.evaluateExpression(filter, name)
		if err != nil {
			return false, fmt.Errorf("failed to evaluate expression", err)
		}
		if matches {
			return true, nil
		}
	}
	return false, nil
}

// evaluateExpression evaluates an expression to see if it matches the filter.
func (r *SCMCheckCommitStatusReconciler) evaluateExpression(expression, checkName string) (bool, error) {
	program, err := r.getCompiledExpression(expression)
	if err != nil {
		return false, fmt.Errorf("failed to get compiled expression", err)
	}

	env := map[string]any{
		"CheckName": checkName,
	}

	output, err := expr.Run(program, env)
	if err != nil {
		return false, fmt.Errorf("failed to run expr program", err)
	}

	result, ok := output.(bool)
	if !ok {
		return false, fmt.Errorf("output of expr program was not a boolean and is type %T", output)
	}
	return result, nil
}

// getCompiledExpression returns the compiled expression and compiles it if it has not been.
func (r *SCMCheckCommitStatusReconciler) getCompiledExpression(expression string) (*vm.Program, error) {
	if cached, ok := r.expressionCache.Load(expression); ok {
		program, ok := cached.(*vm.Program)
		if !ok {
			return nil, fmt.Errorf("cached data is not a *vm.Program but is type %T", program)
		}
		return program, nil
	}

	env := map[string]any{
		"CheckName": "",
	}

	program, err := expr.Compile(expression, expr.Env(env), expr.AsBool())
	if err != nil {
		return nil, fmt.Errorf("failed to compile expr expression: %w", err)
	}

	r.expressionCache.Store(expression, program)
	return program, nil
}

// upsertCheckCommitStatus upserts the aggregated commit status for all of the checks.
func (r *SCMCheckCommitStatusReconciler) upsertCheckCommitStatus(ctx context.Context, scs *promoterv1alpha1.SCMCheckCommitStatus, ps *promoterv1alpha1.PromotionStrategy,
	branch, sha string, checks []scms.SCMCheckResult, aggregatedPhase promoterv1alpha1.CommitStatusPhase,
) (*promoterv1alpha1.CommitStatus, error) {
	phaseToUse := aggregatedPhase
	description := "Aggregated SCM Checks Commit Status, see status of parent SCMCheckCommitStatus for individual check states."
	if len(checks) == 0 {
		phaseToUse = promoterv1alpha1.CommitPhaseSuccess
		description = "No checks exist or were matched"
	}

	upsertParams := utils.UpsertCommitStatusParams{
		Parent:      scs,
		RepoRefName: ps.Spec.RepositoryReference.Name,
		Branch:      branch,
		Sha:         sha,
		Key:         scs.Spec.Key,
		Description: description,
		Phase:       phaseToUse,
		PublishTo:   scs.Spec.PublishTo,
		FieldOwner:  constants.SCMCheckCommitStatusControllerFieldOwner,
	}

	cs, err := utils.UpsertCommitStatus(ctx, r.Client, upsertParams)
	if err != nil {
		return cs, fmt.Errorf("failed to upsert CommitStatus for environment %s: %w", branch, err)
	}
	return cs, nil
}

// buildEnvironmentStatus builds the current status of the enviornment by building a list of all checks including ones
// that did not match the filter to show what the values were for those.
func (r *SCMCheckCommitStatusReconciler) buildEnvironmentStatus(checks, filtered []scms.SCMCheckResult, branch, sha string, includeFiltered bool) promoterv1alpha1.SCMCheckCommitStatusEnvironmentStatus {
	// Make a map of filtered checks for effective lookup
	// int is the value in the map as we are basically just using this for lookup
	filteredChecks := make(map[string]int, len(filtered))
	for i := range filtered {
		filteredChecks[filtered[i].Name] = 1
	}

	environmentStatus := promoterv1alpha1.SCMCheckCommitStatusEnvironmentStatus{
		Branch:              branch,
		ProposedHydratedSha: sha,
	}

	failureFound := false
	successFound := false
	pendingFound := false
	for _, check := range checks {
		scmCheck := promoterv1alpha1.SCMCheckResults{
			Name:     check.Name,
			Status:   promoterv1alpha1.SCMCheckStatus(check.Status),
			URL:      check.URL,
		}

		matched := true
		if _, ok := filteredChecks[check.Name]; !ok && includeFiltered {
			matched = false
		}
		scmCheck.Matched = matched

		environmentStatus.SCMChecks = append(environmentStatus.SCMChecks, scmCheck)

		if includeFiltered && !matched {
			continue
		}

		switch utils.CheckStatusToCommitStatusPhase(check.Status) {
		case promoterv1alpha1.CommitPhaseSuccess:
			successFound = true
		case promoterv1alpha1.CommitPhaseFailure:
			failureFound = true
		default:
			pendingFound = true
		}
	}

	if failureFound {
		environmentStatus.Phase = string(promoterv1alpha1.CommitPhaseFailure)
	} else if pendingFound {
		environmentStatus.Phase = string(promoterv1alpha1.CommitPhasePending)
	} else if successFound {
		environmentStatus.Phase = string(promoterv1alpha1.CommitPhaseSuccess)
	} else {
		// Put environment as success because if here that means there were no checks
		// so this will be considered a success so it does not block
		environmentStatus.Phase = string(promoterv1alpha1.CommitPhaseSuccess)
	}

	return environmentStatus
}
