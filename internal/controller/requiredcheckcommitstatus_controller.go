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
	"strings"
	"sync/atomic"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"golang.org/x/sync/singleflight"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	githubscm "github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// Default configuration values for RequiredCheckCommitStatus controller
const (
	DefaultRequiredCheckCacheTTL     = 24 * time.Hour
	DefaultRequiredCheckCacheMaxSize = 1000
	DefaultPendingCheckInterval      = 1 * time.Minute
	DefaultTerminalCheckInterval     = 10 * time.Minute
	DefaultSafetyNetInterval         = 1 * time.Hour
	DefaultSCMAPITimeout             = 30 * time.Second
	DefaultPollingOperationTimeout   = 5 * time.Minute

	// Index field name for efficient lookup of RCCS by PromotionStrategy
	promotionStrategyRefIndex = "spec.promotionStrategyRef.name"
)

var (
	// Thread-safe LRU cache with TTL-based expiration
	requiredCheckCache atomic.Pointer[expirable.LRU[string, []scms.RequiredCheck]]

	// requiredCheckFlight prevents duplicate concurrent API calls for the same cache key
	requiredCheckFlight singleflight.Group
)

// initializeRequiredCheckCache creates the cache with specified TTL and maxSize.
// Called at startup and when cache configuration changes.
func initializeRequiredCheckCache(ttl time.Duration, maxSize int) error {
	if maxSize <= 0 {
		return fmt.Errorf("cache maxSize must be positive, got: %d", maxSize)
	}

	cache := expirable.NewLRU[string, []scms.RequiredCheck](
		maxSize,
		nil, // No eviction callback
		ttl,
	)

	requiredCheckCache.Store(cache)
	return nil
}

// getRequiredCheckCache safely retrieves current cache instance.
// Returns an error if the cache has not been initialized.
func getRequiredCheckCache() (*expirable.LRU[string, []scms.RequiredCheck], error) {
	cache := requiredCheckCache.Load()
	if cache == nil {
		return nil, fmt.Errorf("required check cache not initialized")
	}
	return cache, nil
}

// withSCMTimeout creates a context with DefaultSCMAPITimeout to prevent goroutine leaks
// from hanging API calls. Returns the new context and cancel function.
func withSCMTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, DefaultSCMAPITimeout)
}

// RequiredCheckCommitStatusReconciler reconciles a RequiredCheckCommitStatus object
type RequiredCheckCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager
	EnqueueCTP  CTPEnqueueFunc
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=requiredcheckcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=requiredcheckcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=requiredcheckcommitstatuses/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *RequiredCheckCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling RequiredCheckCommitStatus")
	startTime := time.Now()

	var rccs promoterv1alpha1.RequiredCheckCommitStatus
	// This function will update the resource status at the end of the reconciliation. don't call .Status().Update manually.
	defer utils.HandleReconciliationResult(ctx, startTime, &rccs, r.Client, r.Recorder, &err)

	// 1. Fetch the RequiredCheckCommitStatus instance
	err = r.Get(ctx, req.NamespacedName, &rccs, &client.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("RequiredCheckCommitStatus not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get RequiredCheckCommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get RequiredCheckCommitStatus %q: %w", req.Name, err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	meta.RemoveStatusCondition(rccs.GetConditions(), string(promoterConditions.Ready))

	// 2. Fetch the referenced PromotionStrategy
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{
		Namespace: rccs.Namespace,
		Name:      rccs.Spec.PromotionStrategyRef.Name,
	}
	err = r.Get(ctx, psKey, &ps)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Error(err, "referenced PromotionStrategy not found", "promotionStrategy", rccs.Spec.PromotionStrategyRef.Name)
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", rccs.Spec.PromotionStrategyRef.Name, err)
		}
		logger.Error(err, "failed to get PromotionStrategy")
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", rccs.Spec.PromotionStrategyRef.Name, err)
	}

	// 3. Get all ChangeTransferPolicies for this PromotionStrategy
	var ctpList promoterv1alpha1.ChangeTransferPolicyList
	err = r.List(ctx, &ctpList, &client.ListOptions{
		Namespace: ps.Namespace,
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list ChangeTransferPolicies for RCCS %s/%s: %w", rccs.Namespace, rccs.Name, err)
	}

	// Filter CTPs that belong to this PromotionStrategy
	var relevantCTPs []promoterv1alpha1.ChangeTransferPolicy
	for _, ctp := range ctpList.Items {
		if ctp.Labels[promoterv1alpha1.PromotionStrategyLabel] == ps.Name {
			relevantCTPs = append(relevantCTPs, ctp)
		}
	}

	// Save previous status to detect transitions and for per-check polling optimization
	previousStatus := rccs.Status.DeepCopy()
	if previousStatus == nil {
		previousStatus = &promoterv1alpha1.RequiredCheckCommitStatusStatus{}
	}

	// 4. Process each environment and create/update CommitStatus resources
	commitStatuses := r.processEnvironments(ctx, &rccs, &ps, relevantCTPs, previousStatus)

	// 5. Clean up orphaned CommitStatus resources
	err = r.cleanupOrphanedCommitStatuses(ctx, &rccs, commitStatuses)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to cleanup orphaned CommitStatus resources for RCCS %s/%s: %w", rccs.Namespace, rccs.Name, err)
	}

	// 6. Inherit conditions from CommitStatus objects
	utils.InheritNotReadyConditionFromObjects(&rccs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	// 7. Trigger CTP reconciliation if any environment phase changed
	err = r.triggerCTPReconciliation(ctx, &ps, previousStatus, &rccs.Status)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to trigger CTP reconciliation for RCCS %s/%s: %w", rccs.Namespace, rccs.Name, err)
	}

	// 8. Calculate dynamic requeue duration
	requeueDuration, err := r.calculateRequeueDuration(ctx, &rccs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to calculate requeue duration for RCCS %s/%s: %w", rccs.Namespace, rccs.Name, err)
	}

	return ctrl.Result{
		RequeueAfter: requeueDuration,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RequiredCheckCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.

	// Validate configuration at startup - fail fast if config is invalid
	config, err := settings.GetRequiredCheckCommitStatusConfigurationDirect(ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get RequiredCheckCommitStatus configuration: %w", err)
	}
	if err := validateRequiredCheckConfig(&config); err != nil {
		return fmt.Errorf("invalid RequiredCheckCommitStatus configuration: %w", err)
	}

	// Initialize cache configuration from settings
	cacheTTL := DefaultRequiredCheckCacheTTL
	if config.RequiredCheckCacheTTL != nil {
		cacheTTL = config.RequiredCheckCacheTTL.Duration
	}

	cacheMaxSize := DefaultRequiredCheckCacheMaxSize
	if config.RequiredCheckCacheMaxSize != nil && *config.RequiredCheckCacheMaxSize > 0 {
		cacheMaxSize = *config.RequiredCheckCacheMaxSize
	}

	// Initialize the required check cache
	if err := initializeRequiredCheckCache(cacheTTL, cacheMaxSize); err != nil {
		return fmt.Errorf("failed to initialize required check cache: %w", err)
	}

	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.RequiredCheckCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get RequiredCheckCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.RequiredCheckCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get RequiredCheckCommitStatus max concurrent reconciles: %w", err)
	}

	// Set up field index for efficient lookup of RCCS by PromotionStrategy name.
	// IndexField is idempotent in recent controller-runtime versions, but we handle
	// errors defensively in case of version differences or cache timing issues.
	err = mgr.GetFieldIndexer().IndexField(ctx, &promoterv1alpha1.RequiredCheckCommitStatus{}, promotionStrategyRefIndex, func(obj client.Object) []string {
		rccs, ok := obj.(*promoterv1alpha1.RequiredCheckCommitStatus)
		if !ok {
			return nil
		}
		return []string{rccs.Spec.PromotionStrategyRef.Name}
	})
	if err != nil {
		// In controller-runtime, IndexField typically fails only if:
		// 1. Cache is already started and this is a new index (should not happen during setup)
		// 2. Index exists with different indexer function (programming error)
		// We fail fast on any error to catch misconfigurations early.
		return fmt.Errorf("failed to create field index %q for RequiredCheckCommitStatus: %w", promotionStrategyRefIndex, err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.RequiredCheckCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.PromotionStrategy{}, r.enqueueRCCSForPromotionStrategy()).
		Watches(&promoterv1alpha1.ChangeTransferPolicy{}, r.enqueueRCCSForCTP(), builder.WithPredicates(r.ctpProposedShaChangedPredicate())).
		Watches(&promoterv1alpha1.CommitStatus{}, handler.EnqueueRequestForOwner(
			mgr.GetScheme(),
			mgr.GetRESTMapper(),
			&promoterv1alpha1.RequiredCheckCommitStatus{},
			handler.OnlyControllerOwner(),
		)).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// enqueueRCCSForPromotionStrategy enqueues all RequiredCheckCommitStatus resources for a PromotionStrategy
func (r *RequiredCheckCommitStatusReconciler) enqueueRCCSForPromotionStrategy() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
		if !ok {
			return nil
		}

		var rccs promoterv1alpha1.RequiredCheckCommitStatusList
		err := r.List(ctx, &rccs,
			client.InNamespace(ps.Namespace),
			client.MatchingFields{promotionStrategyRefIndex: ps.Name},
		)
		if err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to list RequiredCheckCommitStatus resources for PromotionStrategy watch",
				"promotionStrategy", ps.Name, "namespace", ps.Namespace)
			return nil
		}

		var requests []ctrl.Request
		for _, item := range rccs.Items {
			requests = append(requests, ctrl.Request{
				NamespacedName: client.ObjectKey{
					Namespace: item.Namespace,
					Name:      item.Name,
				},
			})
		}
		return requests
	})
}

// enqueueRCCSForCTP enqueues all RequiredCheckCommitStatus resources affected by a ChangeTransferPolicy change
func (r *RequiredCheckCommitStatusReconciler) enqueueRCCSForCTP() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ctp, ok := obj.(*promoterv1alpha1.ChangeTransferPolicy)
		if !ok {
			return nil
		}

		// Get the PromotionStrategy name from the CTP label
		psName, ok := ctp.Labels[promoterv1alpha1.PromotionStrategyLabel]
		if !ok {
			// CTP not labeled with a PromotionStrategy, nothing to enqueue
			return nil
		}

		// Find all RCCS resources that reference this PromotionStrategy
		var rccs promoterv1alpha1.RequiredCheckCommitStatusList
		err := r.List(ctx, &rccs,
			client.InNamespace(ctp.Namespace),
			client.MatchingFields{promotionStrategyRefIndex: psName},
		)
		if err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to list RequiredCheckCommitStatus resources for ChangeTransferPolicy watch",
				"changeTransferPolicy", ctp.Name, "namespace", ctp.Namespace)
			return nil
		}

		var requests []ctrl.Request
		for _, item := range rccs.Items {
			requests = append(requests, ctrl.Request{
				NamespacedName: client.ObjectKey{
					Namespace: item.Namespace,
					Name:      item.Name,
				},
			})
		}
		return requests
	})
}

// ctpProposedShaChangedPredicate returns a predicate that filters CTP events to only trigger
// when the proposed SHA changes, which is the field that RCCS cares about
func (r *RequiredCheckCommitStatusReconciler) ctpProposedShaChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			ctp, ok := e.Object.(*promoterv1alpha1.ChangeTransferPolicy)
			if !ok {
				return false
			}
			// Trigger if CTP is created with a proposed SHA
			return ctp.Status.Proposed.Hydrated.Sha != ""
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldCTP, ok := e.ObjectOld.(*promoterv1alpha1.ChangeTransferPolicy)
			if !ok {
				return false
			}
			newCTP, ok := e.ObjectNew.(*promoterv1alpha1.ChangeTransferPolicy)
			if !ok {
				return false
			}
			// Only trigger if proposed SHA changed (the field we care about)
			return oldCTP.Status.Proposed.Hydrated.Sha != newCTP.Status.Proposed.Hydrated.Sha
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			// Don't trigger on delete - RCCS will handle missing CTPs during reconciliation
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			// Don't trigger on generic events
			return false
		},
	}
}

// processEnvironments processes each environment and creates/updates CommitStatus resources
func (r *RequiredCheckCommitStatusReconciler) processEnvironments(ctx context.Context, rccs *promoterv1alpha1.RequiredCheckCommitStatus, ps *promoterv1alpha1.PromotionStrategy, ctps []promoterv1alpha1.ChangeTransferPolicy, previousStatus *promoterv1alpha1.RequiredCheckCommitStatusStatus) []*promoterv1alpha1.CommitStatus {
	logger := log.FromContext(ctx)

	// Initialize environments status
	rccs.Status.Environments = make([]promoterv1alpha1.RequiredCheckEnvironmentStatus, 0)

	// Track all CommitStatus objects created/updated
	var commitStatuses []*promoterv1alpha1.CommitStatus

	// Build a map of CTPs by environment branch
	ctpByBranch := make(map[string]*promoterv1alpha1.ChangeTransferPolicy)
	for i := range ctps {
		ctp := &ctps[i]
		ctpByBranch[ctp.Spec.ActiveBranch] = ctp
	}

	// Build a map of previous environment status by branch for per-check polling optimization
	previousEnvByBranch := make(map[string]*promoterv1alpha1.RequiredCheckEnvironmentStatus)
	if previousStatus != nil {
		for i := range previousStatus.Environments {
			env := &previousStatus.Environments[i]
			previousEnvByBranch[env.Branch] = env
		}
	}

	// Process each environment
	for _, env := range ps.Spec.Environments {
		// Check for context cancellation to support graceful shutdown
		// NOTE: If cancelled, returns partial results (already processed environments).
		// Remaining environments will be processed on next reconciliation.
		select {
		case <-ctx.Done():
			return commitStatuses
		default:
		}

		ctp, found := ctpByBranch[env.Branch]
		if !found {
			logger.Info("ChangeTransferPolicy not found for environment",
				"promotionStrategy", ps.Name,
				"branch", env.Branch)
			continue
		}

		// Get proposed SHA from CTP status
		proposedSha := ctp.Status.Proposed.Hydrated.Sha
		if proposedSha == "" {
			logger.Info("No proposed SHA found for environment",
				"promotionStrategy", ps.Name,
				"branch", env.Branch)
			continue
		}

		// Discover required checks for this environment
		requiredChecks, err := r.discoverRequiredChecksForEnvironment(ctx, ps, env)
		if err != nil {
			logger.Error(err, "failed to discover required checks",
				"promotionStrategy", ps.Name,
				"repository", ps.Spec.RepositoryReference.Name,
				"branch", env.Branch)
			// Continue to process other environments
			continue
		}

		// Get previous environment status for per-check polling optimization
		previousEnv := previousEnvByBranch[env.Branch]

		// Poll check status for each required check
		checkStatuses, err := r.pollCheckStatusForEnvironment(ctx, ps, requiredChecks, proposedSha, previousEnv)
		if err != nil {
			// Provide more context for timeout errors
			if errors.Is(err, context.DeadlineExceeded) {
				logger.Error(err, "polling operation timeout exceeded (some checks may not have been polled)",
					"promotionStrategy", ps.Name,
					"repository", ps.Spec.RepositoryReference.Name,
					"branch", env.Branch,
					"sha", proposedSha,
					"checkCount", len(requiredChecks))
			} else {
				logger.Error(err, "failed to poll check status",
					"promotionStrategy", ps.Name,
					"repository", ps.Spec.RepositoryReference.Name,
					"branch", env.Branch,
					"sha", proposedSha)
			}
			// Continue to process other environments
			continue
		}

		// Create/update CommitStatus resources for each check
		var envCheckStatuses []promoterv1alpha1.RequiredCheckStatus
		for _, check := range requiredChecks {
			// Check for context cancellation to support graceful shutdown
			// NOTE: If cancelled, returns partial results (already processed checks and environments).
			// Remaining checks will have CommitStatus resources created/updated on next reconciliation.
			select {
			case <-ctx.Done():
				return commitStatuses
			default:
			}

			chkStatus, ok := checkStatuses[check.Key]
			if !ok {
				// If we couldn't get the status, default to pending
				// This can happen if polling was interrupted (e.g., context cancellation, timeout)
				logger.Info("Check status missing from polling results, defaulting to pending (will retry on next reconciliation)",
					"check", check.Name,
					"key", check.Key,
					"repository", ps.Spec.RepositoryReference.Name,
					"branch", env.Branch,
					"sha", proposedSha)
				chkStatus = checkStatus{
					phase:        promoterv1alpha1.CommitPhasePending,
					lastPolledAt: nil,
				}
			}

			cs, err := r.updateCommitStatusForCheck(ctx, rccs, ctp, env.Branch, check, proposedSha, chkStatus.phase)
			if err != nil {
				logger.Error(err, "failed to update CommitStatus",
					"check", check.Key,
					"repository", ps.Spec.RepositoryReference.Name,
					"branch", env.Branch,
					"sha", proposedSha)
				// Continue to process other checks
				continue
			}

			commitStatuses = append(commitStatuses, cs)
			envCheckStatuses = append(envCheckStatuses, promoterv1alpha1.RequiredCheckStatus{
				Name:         check.Name,
				Key:          check.Key,
				Phase:        chkStatus.phase,
				LastPolledAt: chkStatus.lastPolledAt,
			})
		}

		// Calculate aggregated phase for this environment
		aggregatedPhase := r.calculateAggregatedPhase(envCheckStatuses)

		// Update environment status
		rccs.Status.Environments = append(rccs.Status.Environments, promoterv1alpha1.RequiredCheckEnvironmentStatus{
			Branch:         env.Branch,
			Sha:            proposedSha,
			RequiredChecks: envCheckStatuses,
			Phase:          aggregatedPhase,
		})
	}

	return commitStatuses
}

// getRequiredCheckProvider creates the appropriate required check provider based on the SCM type.
func (r *RequiredCheckCommitStatusReconciler) getRequiredCheckProvider(
	ctx context.Context,
	repoRef promoterv1alpha1.ObjectReference,
	namespace string,
) (scms.RequiredCheckProvider, error) {
	logger := log.FromContext(ctx)

	// Get GitRepository
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{
		Namespace: namespace,
		Name:      repoRef.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository %s/%s for required check provider: %w", namespace, repoRef.Name, err)
	}

	// Get ScmProvider and Secret
	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, r.SettingsMgr.GetControllerNamespace(), repoRef, gitRepo)
	if err != nil {
		return nil, fmt.Errorf("failed to get ScmProvider and secret for GitRepository %s/%s: %w", namespace, repoRef.Name, err)
	}

	// Create provider based on SCM type
	if scmProvider.GetSpec().GitHub != nil {
		provider, err := githubscm.NewGithubRequiredCheckProvider(ctx, r.Client, scmProvider, *secret, gitRepo.Spec.GitHub.Owner)
		if err != nil {
			return nil, fmt.Errorf("failed to create GitHub required check provider for repo %s/%s (owner: %s): %w", namespace, repoRef.Name, gitRepo.Spec.GitHub.Owner, err)
		}
		return provider, nil
	}

	// For other SCM providers, we'll add support later
	// For now, log that it's not supported and return nil
	logger.V(4).Info("Required check discovery not supported for this SCM provider", "scmProvider", scmProvider.GetName())
	return nil, scms.ErrNotSupported
}

// discoverRequiredChecksForEnvironment queries the SCM's protection rules to discover required checks.
// Results are cached to reduce API calls to the SCM provider.
func (r *RequiredCheckCommitStatusReconciler) discoverRequiredChecksForEnvironment(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, env promoterv1alpha1.Environment) ([]scms.RequiredCheck, error) {
	logger := log.FromContext(ctx)

	// Get GitRepository
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{
		Namespace: ps.Namespace,
		Name:      ps.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository %s/%s for required check discovery on branch %s: %w", ps.Namespace, ps.Spec.RepositoryReference.Name, env.Branch, err)
	}

	// Get required check provider
	provider, err := r.getRequiredCheckProvider(ctx, ps.Spec.RepositoryReference, ps.Namespace)
	if err != nil {
		if errors.Is(err, scms.ErrNotSupported) {
			logger.V(4).Info("Required check discovery not supported for this SCM provider, skipping",
				"repository", gitRepo.Name,
				"branch", env.Branch)
			return []scms.RequiredCheck{}, nil
		}
		return nil, fmt.Errorf("failed to get required check provider for repo %s/%s, branch %s: %w", ps.Namespace, ps.Spec.RepositoryReference.Name, env.Branch, err)
	}

	// Build cache key: include repository identity and branch
	// For GitHub: domain|owner|name|branch
	cacheKey := r.buildCacheKey(gitRepo, env.Branch)

	// Fast path: check cache (thread-safe)
	cache, err := getRequiredCheckCache()
	if err != nil {
		return nil, fmt.Errorf("failed to get required check cache: %w", err)
	}
	if checks, ok := cache.Get(cacheKey); ok {
		logger.V(4).Info("Using cached required check discovery",
			"repository", gitRepo.Name,
			"branch", env.Branch,
			"checks", len(checks))
		return checks, nil
	}

	// Cache miss or expired - use singleflight to prevent duplicate API calls.
	// Singleflight deduplicates concurrent requests for the same cache key, ensuring
	// only one API call is made even if multiple reconciliations happen simultaneously.
	result, err, shared := requiredCheckFlight.Do(cacheKey, func() (any, error) {
		// Add timeout to prevent goroutine leaks from hanging API calls
		ctx, cancel := withSCMTimeout(ctx)
		defer cancel()

		// Call provider to discover checks
		checks, err := provider.DiscoverRequiredChecks(ctx, gitRepo, env.Branch)
		if err != nil {
			if !errors.Is(err, scms.ErrNoProtection) {
				return nil, fmt.Errorf("failed to discover required checks for repo %s/%s, branch %s: %w", gitRepo.Namespace, gitRepo.Name, env.Branch, err)
			}
			logger.V(4).Info("No protection rules configured for branch",
				"repository", gitRepo.Name,
				"branch", env.Branch)
			// Cache empty result for branches with no protection
			checks = []scms.RequiredCheck{}
		}

		// Store in cache (automatic TTL and eviction)
		cache, err := getRequiredCheckCache()
		if err != nil {
			// Log error but don't fail - caching is optional optimization
			logger.Error(err, "Failed to get cache for storing results, continuing without cache")
		} else {
			cache.Add(cacheKey, checks)
			logger.V(4).Info("Cached required check discovery",
				"repository", gitRepo.Name,
				"branch", env.Branch,
				"checks", len(checks))
		}

		return checks, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to discover required checks for repo %s/%s, branch %s: %w", gitRepo.Namespace, gitRepo.Name, env.Branch, err)
	}

	// Log when singleflight deduplication occurred to provide visibility into
	// concurrent request patterns and potential memory pressure from result sharing
	if shared {
		logger.V(4).Info("Reused singleflight result (concurrent reconciliations deduplicated)",
			"cacheKey", cacheKey,
			"repository", gitRepo.Name,
			"branch", env.Branch)
	}

	checks, ok := result.([]scms.RequiredCheck)
	if !ok {
		return nil, errors.New("unexpected type from singleflight result")
	}
	return checks, nil
}

// pollCheckStatusForEnvironment polls the SCM to get the status of each required check.
// Uses per-check polling optimization: terminal checks (success/failure) are only polled
// if they haven't been polled recently (based on terminalCheckInterval), while pending
// checks are always polled.
func (r *RequiredCheckCommitStatusReconciler) pollCheckStatusForEnvironment(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, requiredChecks []scms.RequiredCheck, sha string, previousEnv *promoterv1alpha1.RequiredCheckEnvironmentStatus) (map[string]checkStatus, error) {
	logger := log.FromContext(ctx)

	if len(requiredChecks) == 0 {
		return map[string]checkStatus{}, nil
	}

	// Get configuration for polling intervals
	config, err := settings.GetRequiredCheckCommitStatusConfiguration(ctx, r.SettingsMgr)
	if err != nil {
		return nil, fmt.Errorf("failed to get RequiredCheckCommitStatus configuration: %w", err)
	}

	// Apply overall timeout for the entire polling operation to prevent excessive reconciliation times
	pollingTimeout := DefaultPollingOperationTimeout
	if config.PollingOperationTimeout != nil && config.PollingOperationTimeout.Duration > 0 {
		pollingTimeout = config.PollingOperationTimeout.Duration
	}
	if pollingTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, pollingTimeout)
		defer cancel()
		logger.V(4).Info("Applied overall polling operation timeout",
			"timeout", pollingTimeout,
			"checkCount", len(requiredChecks))
	}

	terminalInterval := DefaultTerminalCheckInterval
	if config.TerminalCheckInterval != nil {
		terminalInterval = config.TerminalCheckInterval.Duration
	}

	// Build a map of previous check statuses by key
	previousCheckByKey := make(map[string]*promoterv1alpha1.RequiredCheckStatus)
	if previousEnv != nil && previousEnv.Sha == sha {
		// Only use previous checks if SHA hasn't changed
		for i := range previousEnv.RequiredChecks {
			check := &previousEnv.RequiredChecks[i]
			previousCheckByKey[check.Key] = check
		}
	}

	// Get GitRepository
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{
		Namespace: ps.Namespace,
		Name:      ps.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository %s/%s for check status polling on SHA %s: %w", ps.Namespace, ps.Spec.RepositoryReference.Name, sha, err)
	}

	// Get required check provider
	provider, err := r.getRequiredCheckProvider(ctx, ps.Spec.RepositoryReference, ps.Namespace)
	if err != nil {
		if errors.Is(err, scms.ErrNotSupported) {
			return map[string]checkStatus{}, nil
		}
		return nil, fmt.Errorf("failed to get required check provider for repo %s/%s, SHA %s: %w", ps.Namespace, ps.Spec.RepositoryReference.Name, sha, err)
	}

	now := metav1.Now()
	checkStatuses := make(map[string]checkStatus)

	// Query check status for each required check (with per-check polling optimization)
	for _, check := range requiredChecks {
		// Check for context cancellation to support graceful shutdown
		// NOTE: If cancelled during polling, returns error and partial results are discarded.
		// Checks that weren't polled will be retried on next reconciliation.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		previousCheck := previousCheckByKey[check.Key]

		// Determine if we should skip polling this check
		shouldSkip := false
		if previousCheck != nil {
			isTerminal := previousCheck.Phase == promoterv1alpha1.CommitPhaseSuccess ||
				previousCheck.Phase == promoterv1alpha1.CommitPhaseFailure

			if isTerminal && previousCheck.LastPolledAt != nil {
				timeSinceLastPoll := now.Time.Sub(previousCheck.LastPolledAt.Time)
				if timeSinceLastPoll < terminalInterval {
					// Terminal check was polled recently, skip and reuse previous status
					shouldSkip = true
					logger.V(4).Info("Skipping terminal check poll (recently checked)",
						"check", check.Name,
						"key", check.Key,
						"phase", previousCheck.Phase,
						"lastPolledAt", previousCheck.LastPolledAt.Time,
						"timeSinceLastPoll", timeSinceLastPoll.Round(time.Second),
						"terminalInterval", terminalInterval)
				}
			}
		}

		var phase promoterv1alpha1.CommitStatusPhase
		var lastPolledAt *metav1.Time

		if shouldSkip {
			// Reuse previous phase and timestamp
			phase = previousCheck.Phase
			lastPolledAt = previousCheck.LastPolledAt
		} else {
			// Poll the check from SCM with timeout to prevent goroutine leaks
			pollCtx, pollCancel := withSCMTimeout(ctx)
			polledPhase, err := provider.PollCheckStatus(pollCtx, gitRepo, sha, check)
			pollCancel() // Release resources immediately after call
			if err != nil {
				if errors.Is(err, scms.ErrNotSupported) {
					// If not supported, default to pending
					phase = promoterv1alpha1.CommitPhasePending
				} else {
					// Log the error but continue processing other checks
					// This provides graceful degradation during transient failures (network issues, partial outages)
					logger.Error(err, "Failed to poll check status, marking as pending for graceful degradation",
						"check", check.Name,
						"key", check.Key,
						"repository", gitRepo.Name,
						"sha", sha)

					// Mark as pending so we maintain visibility into other working checks
					// The check will be retried on next reconciliation
					phase = promoterv1alpha1.CommitPhasePending
				}
			} else {
				phase = polledPhase
			}

			// Update lastPolledAt to current time since we just polled
			lastPolledAt = &now
		}

		checkStatuses[check.Key] = checkStatus{
			phase:        phase,
			lastPolledAt: lastPolledAt,
		}
	}

	return checkStatuses, nil
}

// checkStatus holds check status along with the timestamp when it was last polled
type checkStatus struct {
	phase        promoterv1alpha1.CommitStatusPhase
	lastPolledAt *metav1.Time
}

// updateCommitStatusForCheck creates or updates a CommitStatus resource for a required check
func (r *RequiredCheckCommitStatusReconciler) updateCommitStatusForCheck(ctx context.Context, rccs *promoterv1alpha1.RequiredCheckCommitStatus, ctp *promoterv1alpha1.ChangeTransferPolicy, branch string, check scms.RequiredCheck, sha string, phase promoterv1alpha1.CommitStatusPhase) (*promoterv1alpha1.CommitStatus, error) {
	// Use the pre-computed key from the check (e.g., "github-smoke" or "github-smoke-15368")
	labelKey := check.Key

	// Generate CommitStatus name with hash for uniqueness using standard utility
	name := utils.KubeSafeUniqueName(ctx, fmt.Sprintf("required-check-%s-%s", labelKey, branch))

	cs := &promoterv1alpha1.CommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: rccs.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cs, func() error {
		// Set owner reference
		if err := controllerutil.SetControllerReference(rccs, cs, r.Scheme); err != nil {
			return fmt.Errorf("failed to set controller reference: %w", err)
		}

		// Set labels
		if cs.Labels == nil {
			cs.Labels = make(map[string]string)
		}
		cs.Labels[promoterv1alpha1.CommitStatusLabel] = labelKey // e.g., "github-e2e-test"
		cs.Labels[promoterv1alpha1.EnvironmentLabel] = utils.KubeSafeLabel(branch)
		cs.Labels[promoterv1alpha1.RequiredCheckCommitStatusLabel] = rccs.Name

		// Set spec with phase-appropriate description
		description := generateRequiredCheckDescription(check.Name, phase)
		cs.Spec = promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: ctp.Spec.RepositoryReference,
			Sha:                 sha,
			Name:                "Required Check: " + check.Name,
			Description:         description,
			Phase:               phase,
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create or update CommitStatus for check %s on branch %s, SHA %s: %w", check.Name, branch, sha, err)
	}

	r.Recorder.Eventf(cs, nil, "Normal", constants.CommitStatusSetReason, "SettingStatus", "Required check %s status set to %s for hash %s", check.Name, string(phase), sha)

	return cs, nil
}

// generateRequiredCheckDescription generates a phase-appropriate description for a required check
// following the best practices for action-oriented, present-tense language
func generateRequiredCheckDescription(checkName string, phase promoterv1alpha1.CommitStatusPhase) string {
	switch phase {
	case promoterv1alpha1.CommitPhasePending:
		// Pending: Use progressive verbs (-ing) to show active monitoring
		return "Waiting for " + checkName + " to pass"
	case promoterv1alpha1.CommitPhaseSuccess:
		// Success: Use present tense to describe current healthy state
		return "Check " + checkName + " is passing"
	case promoterv1alpha1.CommitPhaseFailure:
		// Failure: Use present tense to describe current problem
		return "Check " + checkName + " is failing"
	default:
		// Fallback for unknown phases
		return "Required check: " + checkName
	}
}

// calculateAggregatedPhase calculates the aggregated phase for an environment
func (r *RequiredCheckCommitStatusReconciler) calculateAggregatedPhase(checks []promoterv1alpha1.RequiredCheckStatus) promoterv1alpha1.CommitStatusPhase {
	if len(checks) == 0 {
		return promoterv1alpha1.CommitPhaseSuccess
	}

	// If any check is failing, the aggregate is failure
	for _, check := range checks {
		if check.Phase == promoterv1alpha1.CommitPhaseFailure {
			return promoterv1alpha1.CommitPhaseFailure
		}
	}

	// If any check is pending, the aggregate is pending
	for _, check := range checks {
		if check.Phase == promoterv1alpha1.CommitPhasePending {
			return promoterv1alpha1.CommitPhasePending
		}
	}

	// All checks are success
	return promoterv1alpha1.CommitPhaseSuccess
}

// cleanupOrphanedCommitStatuses removes CommitStatus resources that are no longer needed
func (r *RequiredCheckCommitStatusReconciler) cleanupOrphanedCommitStatuses(ctx context.Context, rccs *promoterv1alpha1.RequiredCheckCommitStatus, validCommitStatuses []*promoterv1alpha1.CommitStatus) error {
	logger := log.FromContext(ctx)

	// List only CommitStatus resources owned by this RCCS using label selector
	var csList promoterv1alpha1.CommitStatusList
	err := r.List(ctx, &csList, &client.ListOptions{
		Namespace: rccs.Namespace,
	}, client.MatchingLabels{
		promoterv1alpha1.RequiredCheckCommitStatusLabel: rccs.Name,
	})
	if err != nil {
		return fmt.Errorf("failed to list CommitStatus resources for RCCS %s/%s: %w", rccs.Namespace, rccs.Name, err)
	}

	// Build a set of valid CommitStatus names
	validNames := make(map[string]bool)
	for _, cs := range validCommitStatuses {
		validNames[cs.Name] = true
	}

	// Delete orphaned CommitStatus resources
	for _, cs := range csList.Items {
		// Check for context cancellation to support graceful shutdown
		// NOTE: If context is cancelled during cleanup, some orphaned resources may remain.
		// This is expected behavior - graceful shutdown takes priority over complete cleanup.
		// Remaining orphaned resources will be cleaned up on the next reconciliation.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// If not in valid names, delete it
		if !validNames[cs.Name] {
			logger.Info("Deleting orphaned CommitStatus", "name", cs.Name)
			err := r.Delete(ctx, &cs)
			if err != nil && !k8serrors.IsNotFound(err) {
				logger.Error(err, "failed to delete orphaned CommitStatus", "name", cs.Name)
				// Continue to try deleting other orphaned resources
				continue
			}
			r.Recorder.Eventf(rccs, nil, "Normal", "CommitStatusDeleted", "DeletingOrphanedResource", "Deleted orphaned CommitStatus %s", cs.Name)
		}
	}

	return nil
}

// calculateRequeueDuration calculates the dynamic requeue duration based on check status
func (r *RequiredCheckCommitStatusReconciler) calculateRequeueDuration(ctx context.Context, rccs *promoterv1alpha1.RequiredCheckCommitStatus) (time.Duration, error) {
	logger := log.FromContext(ctx)

	// Get configuration (validated at startup)
	config, err := settings.GetRequiredCheckCommitStatusConfiguration(ctx, r.SettingsMgr)
	if err != nil {
		return 0, fmt.Errorf("failed to get RequiredCheckCommitStatus configuration: %w", err)
	}

	// Check the status of all environments
	hasPendingChecks := false
	hasAnyChecks := false

	for _, env := range rccs.Status.Environments {
		hasAnyChecks = true
		if env.Phase == promoterv1alpha1.CommitPhasePending {
			hasPendingChecks = true
			break
		}
	}

	// If no environments or checks, use safety net interval to protect against missed watch events
	if !hasAnyChecks {
		safetyNetInterval := DefaultSafetyNetInterval
		if config.SafetyNetInterval != nil {
			safetyNetInterval = config.SafetyNetInterval.Duration
		}

		if safetyNetInterval > 0 {
			logger.V(4).Info("No checks to monitor, using safety net interval",
				"interval", safetyNetInterval)
			return safetyNetInterval, nil
		}

		// Safety net disabled (interval is 0) - rely entirely on watches
		logger.Info("No checks to monitor and safety net disabled, relying on watches only")
		return 0, nil
	}

	pendingInterval := DefaultPendingCheckInterval
	if config.PendingCheckInterval != nil {
		pendingInterval = config.PendingCheckInterval.Duration
	}

	terminalInterval := DefaultTerminalCheckInterval
	if config.TerminalCheckInterval != nil {
		terminalInterval = config.TerminalCheckInterval.Duration
	}

	// If any checks are pending, use aggressive polling
	if hasPendingChecks {
		return pendingInterval, nil
	}

	// All checks in terminal state (success/failure)
	// Use longer interval - less likely to change
	// Still poll to catch reruns or new check runs
	return terminalInterval, nil
}

// triggerCTPReconciliation triggers CTP reconciliation if any environment phase changed
func (r *RequiredCheckCommitStatusReconciler) triggerCTPReconciliation(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, previousStatus *promoterv1alpha1.RequiredCheckCommitStatusStatus, currentStatus *promoterv1alpha1.RequiredCheckCommitStatusStatus) error {
	logger := log.FromContext(ctx)

	// Build maps of environment phases
	previousPhases := make(map[string]promoterv1alpha1.CommitStatusPhase)
	for _, env := range previousStatus.Environments {
		previousPhases[env.Branch] = env.Phase
	}

	currentPhases := make(map[string]promoterv1alpha1.CommitStatusPhase)
	for _, env := range currentStatus.Environments {
		currentPhases[env.Branch] = env.Phase
	}

	// Check for phase transitions
	changedBranches := make(map[string]bool)
	for branch, currentPhase := range currentPhases {
		previousPhase, ok := previousPhases[branch]
		if !ok || previousPhase != currentPhase {
			changedBranches[branch] = true
		}
	}

	// Trigger CTP reconciliation for changed branches
	if len(changedBranches) > 0 && r.EnqueueCTP != nil {
		logger.Info("Triggering CTP reconciliation for changed branches", "branches", changedBranches)

		// List CTPs to find the ones that match the changed branches
		var ctpList promoterv1alpha1.ChangeTransferPolicyList
		err := r.List(ctx, &ctpList, &client.ListOptions{
			Namespace: ps.Namespace,
		})
		if err != nil {
			return fmt.Errorf("failed to list ChangeTransferPolicies for CTP enqueue: %w", err)
		}

		// Find CTPs that match changed branches and enqueue them
		for _, ctp := range ctpList.Items {
			// Check if this CTP belongs to this PromotionStrategy
			if ctp.Labels[promoterv1alpha1.PromotionStrategyLabel] != ps.Name {
				continue
			}

			// Check if this CTP's branch has changed
			if changedBranches[ctp.Spec.ActiveBranch] {
				r.EnqueueCTP(ctp.Namespace, ctp.Name)
				logger.V(4).Info("Enqueued CTP for reconciliation", "ctp", ctp.Name, "branch", ctp.Spec.ActiveBranch)
			}
		}
	}

	return nil
}

// buildCacheKey builds a unique cache key for required check discovery results.
// The key uses the GitRepository resource identity (namespace/name) and branch.
// This ensures proper cache isolation - each GitRepository resource represents a unique
// repository configuration, even if the underlying SCM repository is the same.
func (r *RequiredCheckCommitStatusReconciler) buildCacheKey(gitRepo *promoterv1alpha1.GitRepository, branch string) string {
	return fmt.Sprintf("%s|%s|%s", gitRepo.Namespace, gitRepo.Name, branch)
}

// validateRequiredCheckConfig validates the RequiredCheckCommitStatus configuration
// to prevent misconfiguration that could cause rate limiting or illogical behavior.
func validateRequiredCheckConfig(config *promoterv1alpha1.RequiredCheckCommitStatusConfiguration) error {
	const minInterval = 10 * time.Second
	const minCacheTTL = 0 * time.Second // 0 is allowed (disables caching)

	var errs []error

	// Validate PendingCheckInterval
	if config.PendingCheckInterval != nil {
		interval := config.PendingCheckInterval.Duration
		if interval < 0 {
			errs = append(errs, fmt.Errorf("pendingCheckInterval must not be negative, got: %v", interval))
		} else if interval > 0 && interval < minInterval {
			errs = append(errs, fmt.Errorf("pendingCheckInterval must be >= %v to avoid rate limiting, got: %v", minInterval, interval))
		}
	}

	// Validate TerminalCheckInterval
	if config.TerminalCheckInterval != nil {
		interval := config.TerminalCheckInterval.Duration
		if interval < 0 {
			errs = append(errs, fmt.Errorf("terminalCheckInterval must not be negative, got: %v", interval))
		} else if interval > 0 && interval < minInterval {
			errs = append(errs, fmt.Errorf("terminalCheckInterval must be >= %v to avoid rate limiting, got: %v", minInterval, interval))
		}
	}

	// Validate interval relationships
	if config.PendingCheckInterval != nil && config.TerminalCheckInterval != nil {
		pendingInterval := config.PendingCheckInterval.Duration
		terminalInterval := config.TerminalCheckInterval.Duration
		if pendingInterval > 0 && terminalInterval > 0 && pendingInterval > terminalInterval {
			errs = append(errs, fmt.Errorf("pendingCheckInterval (%v) must not be greater than terminalCheckInterval (%v); typically pending checks should have smaller intervals for faster polling (equal intervals are acceptable if needed)",
				pendingInterval, terminalInterval))
		}
	}

	// Validate SafetyNetInterval relationships
	errs = append(errs, validateSafetyNetInterval(config)...)

	// Validate RequiredCheckCacheTTL
	if config.RequiredCheckCacheTTL != nil {
		cacheTTL := config.RequiredCheckCacheTTL.Duration
		if cacheTTL < minCacheTTL {
			errs = append(errs, fmt.Errorf("requiredCheckCacheTTL must not be negative, got: %v", cacheTTL))
		}

		// Warn if cache TTL is unreasonably short (defeats caching purpose and may cause excessive API calls)
		if cacheTTL > 0 && cacheTTL < minInterval {
			errs = append(errs, fmt.Errorf("requiredCheckCacheTTL (%v) is very short; recommended: >= %v to avoid excessive API calls",
				cacheTTL, minInterval))
		}
	}

	// Validate RequiredCheckCacheMaxSize
	if config.RequiredCheckCacheMaxSize != nil {
		maxSize := *config.RequiredCheckCacheMaxSize
		if maxSize < 10 {
			errs = append(errs, fmt.Errorf("requiredCheckCacheMaxSize is very small (%v), may cause frequent evictions; recommended: >= 100", maxSize))
		}
	}

	// Validate PollingOperationTimeout
	if config.PollingOperationTimeout != nil {
		timeout := config.PollingOperationTimeout.Duration
		if timeout < 0 {
			errs = append(errs, fmt.Errorf("pollingOperationTimeout must not be negative, got: %v", timeout))
		}
		// Warn if timeout is too short (should be at least longer than a single API call timeout)
		minPollingTimeout := DefaultSCMAPITimeout
		if timeout > 0 && timeout < minPollingTimeout {
			errs = append(errs, fmt.Errorf("pollingOperationTimeout (%v) is too short; recommended: >= %v (DefaultSCMAPITimeout) to allow at least one check to complete",
				timeout, minPollingTimeout))
		}
	}

	if len(errs) > 0 {
		// Combine all errors into one
		errMsgs := make([]string, 0, len(errs))
		for _, err := range errs {
			errMsgs = append(errMsgs, err.Error())
		}
		errMsg := strings.Join(errMsgs, "; ")
		return fmt.Errorf("configuration validation failed: %s", errMsg)
	}

	return nil
}

// validateSafetyNetInterval validates the SafetyNetInterval configuration and its relationships.
func validateSafetyNetInterval(config *promoterv1alpha1.RequiredCheckCommitStatusConfiguration) []error {
	var errs []error

	if config.SafetyNetInterval == nil {
		return errs
	}

	safetyNetInterval := config.SafetyNetInterval.Duration
	if safetyNetInterval <= 0 {
		// Safety net disabled, no validation needed
		return errs
	}

	// Check relationship with PendingCheckInterval
	if config.PendingCheckInterval != nil {
		pendingInterval := config.PendingCheckInterval.Duration
		if pendingInterval > 0 && safetyNetInterval <= pendingInterval {
			errs = append(errs, fmt.Errorf("safetyNetInterval (%v) should be > pendingCheckInterval (%v)",
				safetyNetInterval, pendingInterval))
		}
	}

	// Check relationship with TerminalCheckInterval
	if config.TerminalCheckInterval != nil {
		terminalInterval := config.TerminalCheckInterval.Duration
		if terminalInterval > 0 && safetyNetInterval <= terminalInterval {
			errs = append(errs, fmt.Errorf("safetyNetInterval (%v) should be > terminalCheckInterval (%v)",
				safetyNetInterval, terminalInterval))
		}
	}

	return errs
}
