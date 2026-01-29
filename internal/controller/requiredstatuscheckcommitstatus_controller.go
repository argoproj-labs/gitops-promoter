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
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

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

const (
	DefaultRequiredCheckCacheTTL     = 15 * time.Minute
	DefaultRequiredCheckCacheMaxSize = 1000
	DefaultPendingCheckInterval      = 1 * time.Minute
	DefaultTerminalCheckInterval     = 10 * time.Minute
	DefaultSafetyNetInterval         = 1 * time.Hour

	// Index field name for efficient lookup of RSCCS by PromotionStrategy
	promotionStrategyRefIndex = "spec.promotionStrategyRef.name"
)

var (
	// requiredCheckCache stores required check discovery results
	requiredCheckCache = make(map[string]*requiredCheckCacheEntry)

	// requiredCheckCacheMutex protects access to requiredCheckCache and cache configuration
	requiredCheckCacheMutex sync.RWMutex

	// requiredCheckCacheTTL is how long to cache required check discovery results
	requiredCheckCacheTTL = DefaultRequiredCheckCacheTTL

	// requiredCheckCacheMaxSize is the maximum number of entries in the cache
	requiredCheckCacheMaxSize = DefaultRequiredCheckCacheMaxSize

	// requiredCheckFlight prevents duplicate concurrent API calls for the same cache key
	requiredCheckFlight singleflight.Group
)

type requiredCheckCacheEntry struct {
	checks    []scms.RequiredCheck
	expiresAt time.Time
}

// getRequiredCheckLabelKey returns the label key for a required check in the format {provider}-{name}.
// If provider is empty, defaults to "required-status-check".
//
// Examples:
//   - provider="github", name="e2e-test" → "github-e2e-test"
//   - provider="", name="e2e-test" → "required-status-check-e2e-test"
func getRequiredCheckLabelKey(provider, name string) string {
	if provider == "" {
		provider = "required-status-check"
	}
	return provider + "-" + name
}

// RequiredStatusCheckCommitStatusReconciler reconciles a RequiredStatusCheckCommitStatus object
type RequiredStatusCheckCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager
	EnqueueCTP  CTPEnqueueFunc
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=requiredstatuscheckcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=requiredstatuscheckcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=requiredstatuscheckcommitstatuses/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *RequiredStatusCheckCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling RequiredStatusCheckCommitStatus")
	startTime := time.Now()

	var rsccs promoterv1alpha1.RequiredStatusCheckCommitStatus
	// This function will update the resource status at the end of the reconciliation. don't call .Status().Update manually.
	defer utils.HandleReconciliationResult(ctx, startTime, &rsccs, r.Client, r.Recorder, &err)

	// 1. Fetch the RequiredStatusCheckCommitStatus instance
	err = r.Get(ctx, req.NamespacedName, &rsccs, &client.GetOptions{})
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("RequiredStatusCheckCommitStatus not found")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get RequiredStatusCheckCommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get RequiredStatusCheckCommitStatus %q: %w", req.Name, err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	meta.RemoveStatusCondition(rsccs.GetConditions(), string(promoterConditions.Ready))

	// 2. Fetch the referenced PromotionStrategy
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{
		Namespace: rsccs.Namespace,
		Name:      rsccs.Spec.PromotionStrategyRef.Name,
	}
	err = r.Get(ctx, psKey, &ps)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Error(err, "referenced PromotionStrategy not found", "promotionStrategy", rsccs.Spec.PromotionStrategyRef.Name)
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", rsccs.Spec.PromotionStrategyRef.Name, err)
		}
		logger.Error(err, "failed to get PromotionStrategy")
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", rsccs.Spec.PromotionStrategyRef.Name, err)
	}

	// 3. Get all ChangeTransferPolicies for this PromotionStrategy
	var ctpList promoterv1alpha1.ChangeTransferPolicyList
	err = r.List(ctx, &ctpList, &client.ListOptions{
		Namespace: ps.Namespace,
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list ChangeTransferPolicies: %w", err)
	}

	// Filter CTPs that belong to this PromotionStrategy
	var relevantCTPs []promoterv1alpha1.ChangeTransferPolicy
	for _, ctp := range ctpList.Items {
		if ctp.Labels[promoterv1alpha1.PromotionStrategyLabel] == ps.Name {
			relevantCTPs = append(relevantCTPs, ctp)
		}
	}

	// Save previous status to detect transitions
	previousStatus := rsccs.Status.DeepCopy()
	if previousStatus == nil {
		previousStatus = &promoterv1alpha1.RequiredStatusCheckCommitStatusStatus{}
	}

	// 4. Process each environment and create/update CommitStatus resources
	commitStatuses, err := r.processEnvironments(ctx, &rsccs, &ps, relevantCTPs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to process environments: %w", err)
	}

	// 5. Clean up orphaned CommitStatus resources
	err = r.cleanupOrphanedCommitStatuses(ctx, &rsccs, commitStatuses)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to cleanup orphaned CommitStatus resources: %w", err)
	}

	// 6. Inherit conditions from CommitStatus objects
	utils.InheritNotReadyConditionFromObjects(&rsccs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	// 7. Trigger CTP reconciliation if any environment phase changed
	err = r.triggerCTPReconciliation(ctx, &ps, previousStatus, &rsccs.Status)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to trigger CTP reconciliation: %w", err)
	}

	// 8. Calculate dynamic requeue duration
	requeueDuration, err := r.calculateRequeueDuration(ctx, &rsccs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to calculate requeue duration: %w", err)
	}

	return ctrl.Result{
		RequeueAfter: requeueDuration,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RequiredStatusCheckCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.

	// Validate configuration at startup - fail fast if config is invalid
	config, err := settings.GetRequiredStatusCheckCommitStatusConfigurationDirect(ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get RequiredStatusCheckCommitStatus configuration: %w", err)
	}
	if err := validateRequiredStatusCheckConfig(&config); err != nil {
		return fmt.Errorf("invalid RequiredStatusCheckCommitStatus configuration: %w", err)
	}

	// Initialize cache configuration from settings
	requiredCheckCacheMutex.Lock()
	if config.RequiredCheckCacheTTL != nil {
		requiredCheckCacheTTL = config.RequiredCheckCacheTTL.Duration
	}
	if config.RequiredCheckCacheMaxSize != nil && *config.RequiredCheckCacheMaxSize > 0 {
		requiredCheckCacheMaxSize = *config.RequiredCheckCacheMaxSize
	}
	requiredCheckCacheMutex.Unlock()

	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.RequiredStatusCheckCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get RequiredStatusCheckCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.RequiredStatusCheckCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get RequiredStatusCheckCommitStatus max concurrent reconciles: %w", err)
	}

	// Set up field index for efficient lookup of RSCCS by PromotionStrategy name
	if err := mgr.GetFieldIndexer().IndexField(ctx, &promoterv1alpha1.RequiredStatusCheckCommitStatus{}, promotionStrategyRefIndex, func(obj client.Object) []string {
		rsccs := obj.(*promoterv1alpha1.RequiredStatusCheckCommitStatus)
		return []string{rsccs.Spec.PromotionStrategyRef.Name}
	}); err != nil {
		return fmt.Errorf("failed to create field index for PromotionStrategyRef: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.RequiredStatusCheckCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.PromotionStrategy{}, r.enqueueRSCCSForPromotionStrategy()).
		Watches(&promoterv1alpha1.ChangeTransferPolicy{}, r.enqueueRSCCSForCTP(), builder.WithPredicates(r.ctpProposedShaChangedPredicate())).
		Watches(&promoterv1alpha1.CommitStatus{}, handler.EnqueueRequestForOwner(
			mgr.GetScheme(),
			mgr.GetRESTMapper(),
			&promoterv1alpha1.RequiredStatusCheckCommitStatus{},
			handler.OnlyControllerOwner(),
		)).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// enqueueRSCCSForPromotionStrategy enqueues all RequiredStatusCheckCommitStatus resources for a PromotionStrategy
func (r *RequiredStatusCheckCommitStatusReconciler) enqueueRSCCSForPromotionStrategy() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		ps, ok := obj.(*promoterv1alpha1.PromotionStrategy)
		if !ok {
			return nil
		}

		var rsccs promoterv1alpha1.RequiredStatusCheckCommitStatusList
		err := r.List(ctx, &rsccs,
			client.InNamespace(ps.Namespace),
			client.MatchingFields{promotionStrategyRefIndex: ps.Name},
		)
		if err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to list RequiredStatusCheckCommitStatus resources for PromotionStrategy watch",
				"promotionStrategy", ps.Name, "namespace", ps.Namespace)
			return nil
		}

		var requests []ctrl.Request
		for _, item := range rsccs.Items {
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

// enqueueRSCCSForCTP enqueues all RequiredStatusCheckCommitStatus resources affected by a ChangeTransferPolicy change
func (r *RequiredStatusCheckCommitStatusReconciler) enqueueRSCCSForCTP() handler.EventHandler {
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

		// Find all RSCCS resources that reference this PromotionStrategy
		var rsccs promoterv1alpha1.RequiredStatusCheckCommitStatusList
		err := r.List(ctx, &rsccs,
			client.InNamespace(ctp.Namespace),
			client.MatchingFields{promotionStrategyRefIndex: psName},
		)
		if err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to list RequiredStatusCheckCommitStatus resources for ChangeTransferPolicy watch",
				"changeTransferPolicy", ctp.Name, "namespace", ctp.Namespace)
			return nil
		}

		var requests []ctrl.Request
		for _, item := range rsccs.Items {
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
// when the proposed SHA changes, which is the field that RSCCS cares about
func (r *RequiredStatusCheckCommitStatusReconciler) ctpProposedShaChangedPredicate() predicate.Predicate {
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
			// Don't trigger on delete - RSCCS will handle missing CTPs during reconciliation
			return false
		},
		GenericFunc: func(e event.GenericEvent) bool {
			// Don't trigger on generic events
			return false
		},
	}
}

// processEnvironments processes each environment and creates/updates CommitStatus resources
func (r *RequiredStatusCheckCommitStatusReconciler) processEnvironments(ctx context.Context, rsccs *promoterv1alpha1.RequiredStatusCheckCommitStatus, ps *promoterv1alpha1.PromotionStrategy, ctps []promoterv1alpha1.ChangeTransferPolicy) ([]*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	// Initialize environments status
	rsccs.Status.Environments = make([]promoterv1alpha1.RequiredStatusCheckEnvironmentStatus, 0)

	// Track all CommitStatus objects created/updated
	var commitStatuses []*promoterv1alpha1.CommitStatus

	// Build a map of CTPs by environment branch
	ctpByBranch := make(map[string]*promoterv1alpha1.ChangeTransferPolicy)
	for i := range ctps {
		ctp := &ctps[i]
		ctpByBranch[ctp.Spec.ActiveBranch] = ctp
	}

	// Process each environment
	for _, env := range ps.Spec.Environments {
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
		requiredChecks, err := r.discoverRequiredChecksForEnvironment(ctx, ps, ctp, env)
		if err != nil {
			logger.Error(err, "failed to discover required checks",
				"promotionStrategy", ps.Name,
				"repository", ps.Spec.RepositoryReference.Name,
				"branch", env.Branch)
			// Continue to process other environments
			continue
		}

		// Poll check status for each required check
		checkStatuses, err := r.pollCheckStatusForEnvironment(ctx, ps, ctp, requiredChecks, proposedSha)
		if err != nil {
			logger.Error(err, "failed to poll check status",
				"promotionStrategy", ps.Name,
				"repository", ps.Spec.RepositoryReference.Name,
				"branch", env.Branch,
				"sha", proposedSha)
			// Continue to process other environments
			continue
		}

		// Create/update CommitStatus resources for each check
		var envCheckStatuses []promoterv1alpha1.RequiredCheckStatus
		for _, check := range requiredChecks {
			phase, ok := checkStatuses[check.Key]
			if !ok {
				// If we couldn't get the status, default to pending
				phase = promoterv1alpha1.CommitPhasePending
			}

			cs, err := r.updateCommitStatusForCheck(ctx, rsccs, ctp, env.Branch, check, proposedSha, phase)
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
				Name:  check.Name,
				Key:   check.Key,
				Phase: phase,
			})
		}

		// Calculate aggregated phase for this environment
		aggregatedPhase := r.calculateAggregatedPhase(envCheckStatuses)

		// Update environment status
		rsccs.Status.Environments = append(rsccs.Status.Environments, promoterv1alpha1.RequiredStatusCheckEnvironmentStatus{
			Branch:         env.Branch,
			Sha:            proposedSha,
			RequiredChecks: envCheckStatuses,
			Phase:          aggregatedPhase,
		})
	}

	return commitStatuses, nil
}

// getRequiredCheckProvider creates the appropriate required check provider based on the SCM type.
func (r *RequiredStatusCheckCommitStatusReconciler) getRequiredCheckProvider(
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
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	// Get ScmProvider and Secret
	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, r.SettingsMgr.GetControllerNamespace(), repoRef, gitRepo)
	if err != nil {
		return nil, fmt.Errorf("failed to get ScmProvider and secret: %w", err)
	}

	// Create provider based on SCM type
	if scmProvider.GetSpec().GitHub != nil {
		provider, err := githubscm.NewGithubRequiredCheckProvider(ctx, r.Client, scmProvider, *secret, gitRepo.Spec.GitHub.Owner)
		if err != nil {
			return nil, fmt.Errorf("failed to create GitHub required check provider: %w", err)
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
func (r *RequiredStatusCheckCommitStatusReconciler) discoverRequiredChecksForEnvironment(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, ctp *promoterv1alpha1.ChangeTransferPolicy, env promoterv1alpha1.Environment) ([]scms.RequiredCheck, error) {
	logger := log.FromContext(ctx)

	// Get GitRepository
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{
		Namespace: ps.Namespace,
		Name:      ps.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	// Get required check provider
	provider, err := r.getRequiredCheckProvider(ctx, ps.Spec.RepositoryReference, ps.Namespace)
	if err != nil {
		if err == scms.ErrNotSupported {
			logger.V(4).Info("Required check discovery not supported for this SCM provider, skipping",
				"repository", gitRepo.Name,
				"branch", env.Branch)
			return []scms.RequiredCheck{}, nil
		}
		return nil, fmt.Errorf("failed to get required check provider: %w", err)
	}

	// Build cache key: include repository identity and branch
	// For GitHub: domain|owner|name|branch
	cacheKey := r.buildCacheKey(gitRepo, env.Branch)

	// Fast path: check cache with read lock first
	requiredCheckCacheMutex.RLock()
	if entry, found := requiredCheckCache[cacheKey]; found {
		if time.Now().Before(entry.expiresAt) {
			// Cache hit with valid entry
			requiredCheckCacheMutex.RUnlock()
			logger.V(4).Info("Using cached required check discovery",
				"repository", gitRepo.Name,
				"branch", env.Branch,
				"checks", len(entry.checks),
				"expiresIn", time.Until(entry.expiresAt).Round(time.Second))
			return entry.checks, nil
		}
	}
	requiredCheckCacheMutex.RUnlock()

	// Cache miss or expired - use singleflight to prevent duplicate API calls
	result, err, _ := requiredCheckFlight.Do(cacheKey, func() (interface{}, error) {
		// Call provider to discover checks
		checks, err := provider.DiscoverRequiredChecks(ctx, gitRepo, env.Branch)
		if err != nil {
			if err == scms.ErrNoProtection {
				logger.V(4).Info("No protection rules configured for branch",
					"repository", gitRepo.Name,
					"branch", env.Branch)
				// Cache empty result for branches with no protection
				checks = []scms.RequiredCheck{}
			} else {
				return nil, fmt.Errorf("failed to discover required checks: %w", err)
			}
		}

		// Store in cache before returning (write lock)
		requiredCheckCacheMutex.Lock()

		requiredCheckCache[cacheKey] = &requiredCheckCacheEntry{
			checks:    checks,
			expiresAt: time.Now().Add(requiredCheckCacheTTL),
		}

		// Check if we need to enforce cache size limit after insertion
		// to avoid race condition where cache temporarily exceeds max size
		if requiredCheckCacheMaxSize > 0 && len(requiredCheckCache) > requiredCheckCacheMaxSize {
			evictExpiredOrOldestEntries(ctx)
		}
		requiredCheckCacheMutex.Unlock()

		logger.V(4).Info("Cached required check discovery",
			"repository", gitRepo.Name,
			"branch", env.Branch,
			"checks", len(checks),
			"ttl", requiredCheckCacheTTL)

		return checks, nil
	})

	if err != nil {
		return nil, err
	}

	return result.([]scms.RequiredCheck), nil
}

// pollCheckStatusForEnvironment polls the SCM to get the status of each required check
func (r *RequiredStatusCheckCommitStatusReconciler) pollCheckStatusForEnvironment(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, ctp *promoterv1alpha1.ChangeTransferPolicy, requiredChecks []scms.RequiredCheck, sha string) (map[string]promoterv1alpha1.CommitStatusPhase, error) {
	logger := log.FromContext(ctx)

	if len(requiredChecks) == 0 {
		return map[string]promoterv1alpha1.CommitStatusPhase{}, nil
	}

	// Get GitRepository
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{
		Namespace: ps.Namespace,
		Name:      ps.Spec.RepositoryReference.Name,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	// Get required check provider
	provider, err := r.getRequiredCheckProvider(ctx, ps.Spec.RepositoryReference, ps.Namespace)
	if err != nil {
		if err == scms.ErrNotSupported {
			return map[string]promoterv1alpha1.CommitStatusPhase{}, nil
		}
		return nil, fmt.Errorf("failed to get required check provider: %w", err)
	}

	// Query check status for each required check
	checkStatuses := make(map[string]promoterv1alpha1.CommitStatusPhase)
	for _, check := range requiredChecks {
		phase, err := provider.PollCheckStatus(ctx, gitRepo, sha, check)
		if err != nil {
			if err == scms.ErrNotSupported {
				// If not supported, default to pending
				checkStatuses[check.Key] = promoterv1alpha1.CommitPhasePending
				continue
			}

			// Log the error but continue processing other checks
			// This provides graceful degradation during transient failures (network issues, partial outages)
			logger.Error(err, "Failed to poll check status, marking as pending for graceful degradation",
				"check", check.Name,
				"key", check.Key,
				"repository", gitRepo.Name,
				"sha", sha)

			// Mark as pending so we maintain visibility into other working checks
			// The check will be retried on next reconciliation
			checkStatuses[check.Key] = promoterv1alpha1.CommitPhasePending
			continue
		}
		checkStatuses[check.Key] = phase
	}

	return checkStatuses, nil
}

// updateCommitStatusForCheck creates or updates a CommitStatus resource for a required check
func (r *RequiredStatusCheckCommitStatusReconciler) updateCommitStatusForCheck(ctx context.Context, rsccs *promoterv1alpha1.RequiredStatusCheckCommitStatus, ctp *promoterv1alpha1.ChangeTransferPolicy, branch string, check scms.RequiredCheck, sha string, phase promoterv1alpha1.CommitStatusPhase) (*promoterv1alpha1.CommitStatus, error) {
	// Use the pre-computed key from the check (e.g., "github-smoke" or "github-smoke-15368")
	labelKey := check.Key

	// Generate CommitStatus name with hash for uniqueness
	name := generateCommitStatusName(labelKey, branch)

	cs := &promoterv1alpha1.CommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: rsccs.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cs, func() error {
		// Set owner reference
		if err := controllerutil.SetControllerReference(rsccs, cs, r.Scheme); err != nil {
			return err
		}

		// Set labels
		if cs.Labels == nil {
			cs.Labels = make(map[string]string)
		}
		cs.Labels[promoterv1alpha1.CommitStatusLabel] = labelKey // e.g., "github-e2e-test"
		cs.Labels[promoterv1alpha1.EnvironmentLabel] = utils.KubeSafeLabel(branch)
		cs.Labels[promoterv1alpha1.RequiredStatusCheckCommitStatusLabel] = rsccs.Name

		// Set spec with phase-appropriate description
		description := generateRequiredCheckDescription(check.Name, phase)
		cs.Spec = promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: ctp.Spec.RepositoryReference,
			Sha:                 sha,
			Name:                fmt.Sprintf("Required Check: %s", check.Name),
			Description:         description,
			Phase:               phase,
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create or update CommitStatus: %w", err)
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
		return fmt.Sprintf("Waiting for %s to pass", checkName)
	case promoterv1alpha1.CommitPhaseSuccess:
		// Success: Use present tense to describe current healthy state
		return fmt.Sprintf("Check %s is passing", checkName)
	case promoterv1alpha1.CommitPhaseFailure:
		// Failure: Use present tense to describe current problem
		return fmt.Sprintf("Check %s is failing", checkName)
	default:
		// Fallback for unknown phases
		return fmt.Sprintf("Required status check: %s", checkName)
	}
}

// generateCommitStatusName generates a unique name for a CommitStatus resource
// The checkKey already includes integration ID suffix if needed (e.g., "github-smoke-15368")
func generateCommitStatusName(checkKey string, branch string) string {
	// Create a hash of the check key and branch to ensure uniqueness
	h := sha256.New()
	hashInput := checkKey + "-" + branch
	h.Write([]byte(hashInput))
	hash := fmt.Sprintf("%x", h.Sum(nil))[:8]

	// Normalize the check key to be a valid Kubernetes name
	normalized := strings.ToLower(checkKey)
	normalized = strings.ReplaceAll(normalized, "/", "-")
	normalized = strings.ReplaceAll(normalized, "_", "-")
	normalized = strings.ReplaceAll(normalized, ".", "-")

	return fmt.Sprintf("required-status-check-%s-%s", normalized, hash)
}

// calculateAggregatedPhase calculates the aggregated phase for an environment
func (r *RequiredStatusCheckCommitStatusReconciler) calculateAggregatedPhase(checks []promoterv1alpha1.RequiredCheckStatus) promoterv1alpha1.CommitStatusPhase {
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
func (r *RequiredStatusCheckCommitStatusReconciler) cleanupOrphanedCommitStatuses(ctx context.Context, rsccs *promoterv1alpha1.RequiredStatusCheckCommitStatus, validCommitStatuses []*promoterv1alpha1.CommitStatus) error {
	logger := log.FromContext(ctx)

	// List all CommitStatus resources owned by this RSCCS
	var csList promoterv1alpha1.CommitStatusList
	err := r.List(ctx, &csList, &client.ListOptions{
		Namespace: rsccs.Namespace,
	})
	if err != nil {
		return fmt.Errorf("failed to list CommitStatus resources: %w", err)
	}

	// Build a set of valid CommitStatus names
	validNames := make(map[string]bool)
	for _, cs := range validCommitStatuses {
		validNames[cs.Name] = true
	}

	// Delete orphaned CommitStatus resources
	for _, cs := range csList.Items {
		// Check if this CommitStatus is owned by this RSCCS
		isOwned := false
		for _, ownerRef := range cs.OwnerReferences {
			if ownerRef.UID == rsccs.UID {
				isOwned = true
				break
			}
		}

		if !isOwned {
			continue
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
			r.Recorder.Eventf(rsccs, nil, "Normal", "CommitStatusDeleted", "DeletingOrphanedResource", "Deleted orphaned CommitStatus %s", cs.Name)
		}
	}

	return nil
}

// calculateRequeueDuration calculates the dynamic requeue duration based on check status
func (r *RequiredStatusCheckCommitStatusReconciler) calculateRequeueDuration(ctx context.Context, rsccs *promoterv1alpha1.RequiredStatusCheckCommitStatus) (time.Duration, error) {
	logger := log.FromContext(ctx)

	// Get configuration (validated at startup)
	config, err := settings.GetRequiredStatusCheckCommitStatusConfiguration(ctx, r.SettingsMgr)
	if err != nil {
		return 0, fmt.Errorf("failed to get RequiredStatusCheckCommitStatus configuration: %w", err)
	}

	// Check the status of all environments
	hasPendingChecks := false
	hasAnyChecks := false

	for _, env := range rsccs.Status.Environments {
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
func (r *RequiredStatusCheckCommitStatusReconciler) triggerCTPReconciliation(ctx context.Context, ps *promoterv1alpha1.PromotionStrategy, previousStatus *promoterv1alpha1.RequiredStatusCheckCommitStatusStatus, currentStatus *promoterv1alpha1.RequiredStatusCheckCommitStatusStatus) error {
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
func (r *RequiredStatusCheckCommitStatusReconciler) buildCacheKey(gitRepo *promoterv1alpha1.GitRepository, branch string) string {
	return fmt.Sprintf("%s|%s|%s", gitRepo.Namespace, gitRepo.Name, branch)
}

// evictExpiredOrOldestEntries removes expired entries and, if still over limit, evicts oldest entries.
// Must be called with requiredCheckCacheMutex write lock held.
func evictExpiredOrOldestEntries(ctx context.Context) {
	logger := log.FromContext(ctx)
	now := time.Now()
	initialSize := len(requiredCheckCache)

	// First pass: remove expired entries
	expiredCount := 0
	for key, entry := range requiredCheckCache {
		if now.After(entry.expiresAt) {
			delete(requiredCheckCache, key)
			expiredCount++
		}
	}

	// If still over limit, remove oldest entries
	if len(requiredCheckCache) >= requiredCheckCacheMaxSize {
		// Create slice of keys sorted by expiration time (oldest first)
		type cacheItem struct {
			key       string
			expiresAt time.Time
		}
		items := make([]cacheItem, 0, len(requiredCheckCache))
		for key, entry := range requiredCheckCache {
			items = append(items, cacheItem{key: key, expiresAt: entry.expiresAt})
		}

		// Sort by expiration time (oldest first)
		sort.Slice(items, func(i, j int) bool {
			return items[i].expiresAt.Before(items[j].expiresAt)
		})

		// Remove oldest entries until we're under the limit
		// Keep 10% headroom to avoid frequent evictions
		targetSize := int(float64(requiredCheckCacheMaxSize) * 0.9)
		for i := 0; i < len(items) && len(requiredCheckCache) > targetSize; i++ {
			delete(requiredCheckCache, items[i].key)
		}
	}

	if expiredCount > 0 || len(requiredCheckCache) < initialSize {
		logger.V(4).Info("Cache eviction summary",
			"initialSize", initialSize,
			"finalSize", len(requiredCheckCache),
			"totalEvicted", initialSize-len(requiredCheckCache),
			"expiredEvicted", expiredCount,
			"lruEvicted", initialSize-len(requiredCheckCache)-expiredCount)
	}
}

// validateRequiredStatusCheckConfig validates the RequiredStatusCheckCommitStatus configuration
// to prevent misconfiguration that could cause rate limiting or illogical behavior.
func validateRequiredStatusCheckConfig(config *promoterv1alpha1.RequiredStatusCheckCommitStatusConfiguration) error {
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
			errs = append(errs, fmt.Errorf("pendingCheckInterval (%v) should be <= terminalCheckInterval (%v) since pending checks need faster polling",
				pendingInterval, terminalInterval))
		}
	}

	if config.SafetyNetInterval != nil {
		safetyNetInterval := config.SafetyNetInterval.Duration
		if safetyNetInterval > 0 {
			if config.PendingCheckInterval != nil {
				pendingInterval := config.PendingCheckInterval.Duration
				if pendingInterval > 0 && safetyNetInterval <= pendingInterval {
					errs = append(errs, fmt.Errorf("safetyNetInterval (%v) should be > pendingCheckInterval (%v)",
						safetyNetInterval, pendingInterval))
				}
			}
			if config.TerminalCheckInterval != nil {
				terminalInterval := config.TerminalCheckInterval.Duration
				if terminalInterval > 0 && safetyNetInterval <= terminalInterval {
					errs = append(errs, fmt.Errorf("safetyNetInterval (%v) should be > terminalCheckInterval (%v)",
						safetyNetInterval, terminalInterval))
				}
			}
		}
	}

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

	if len(errs) > 0 {
		// Combine all errors into one
		var errMsg string
		for i, err := range errs {
			if i > 0 {
				errMsg += "; "
			}
			errMsg += err.Error()
		}
		return fmt.Errorf("configuration validation failed: %s", errMsg)
	}

	return nil
}
