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

	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/gitauth"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	v1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
)

// CTPEnqueueFunc is a function type that can be used to enqueue CTP reconcile requests
// without modifying the CTP object. This is used by other controllers (like PromotionStrategy)
// to trigger CTP reconciliation without causing object conflicts.
type CTPEnqueueFunc func(namespace, name string)

// ChangeTransferPolicyReconciler reconciles a ChangeTransferPolicy object
type ChangeTransferPolicyReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	SettingsMgr *settings.Manager

	// enqueueFunc is set during SetupWithManager and can be retrieved via GetEnqueueFunc.
	// It allows other controllers to enqueue CTP reconcile requests.
	enqueueFunc CTPEnqueueFunc
}

// GetEnqueueFunc returns a function that can be used to enqueue CTP reconcile requests.
// This should be called after SetupWithManager has been called.
func (r *ChangeTransferPolicyReconciler) GetEnqueueFunc() CTPEnqueueFunc {
	return r.enqueueFunc
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicies/finalizers,verbs=update
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests,verbs=get;list;watch;create;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ChangeTransferPolicy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *ChangeTransferPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ChangeTransferPolicy")
	startTime := time.Now()

	var ctp promoterv1alpha1.ChangeTransferPolicy
	// This function will update the resource status at the end of the reconciliation. don't call .Status().Update manually.
	defer utils.HandleReconciliationResult(ctx, startTime, &ctp, r.Client, r.Recorder, &err)

	err = r.Get(ctx, req.NamespacedName, &ctp, &client.GetOptions{})
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			logger.Info("ChangeTransferPolicy not found")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "failed to get ChangeTransferPolicy")
		return ctrl.Result{}, fmt.Errorf("failed to get ChangeTransferPolicy: %w", err)
	}

	// Handle PR finalizer removal if PR is being deleted and CTP status is already synced
	err = r.handlePRFinalizerRemoval(ctx, &ctp)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to handle PR finalizer removal: %w", err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	meta.RemoveStatusCondition(ctp.GetConditions(), string(promoterConditions.Ready))

	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, r.SettingsMgr.GetControllerNamespace(), ctp.Spec.RepositoryReference, &ctp)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get ScmProvider and secret for repo %q: %w", ctp.Spec.RepositoryReference.Name, err)
	}

	gitAuthProvider, err := r.getGitAuthProvider(ctx, scmProvider, secret, ctp.Namespace, ctp.Spec.RepositoryReference)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get git auth provider for ScmProvider %q: %w", scmProvider.GetName(), err)
	}
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{Namespace: ctp.GetNamespace(), Name: ctp.Spec.RepositoryReference.Name})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get GitRepository: %w", err)
	}
	gitOperations := git.NewEnvironmentOperations(gitRepo, gitAuthProvider, ctp.Spec.ActiveBranch)

	// TODO: could probably short circuit the clone and use an ls-remote to compare the sha's of the current ctp status,
	// this would help with slamming the git provider with clone requests on controller restarts.

	err = gitOperations.CloneRepo(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to clone repo %q: %w", ctp.Spec.RepositoryReference.Name, err)
	}

	// Fetch git notes for hydrator metadata (used to track hydration completion)
	err = gitOperations.FetchNotes(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to fetch git notes: %w", err)
	}

	err = r.calculateStatus(ctx, &ctp, gitOperations)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to calculate ChangeTransferPolicy status: %w", err)
	}

	err = r.gitMergeStrategyOurs(ctx, gitOperations, &ctp)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to git merge for conflict resolution: %w", err)
	}

	pr, err := r.creatOrUpdatePullRequest(ctx, &ctp)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set promotion state: %w", err)
	}

	if pr != nil {
		utils.InheritNotReadyConditionFromObjects(&ctp, promoterConditions.PullRequestNotReady, pr)
	}

	pr, err = r.mergePullRequests(ctx, &ctp)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to merge pull requests: %w", err)
	}

	if pr != nil {
		utils.InheritNotReadyConditionFromObjects(&ctp, promoterConditions.PullRequestNotReady, pr)
	}

	// calculateHistory is done at a best effort so we do not return any errors here, we just log them instead.
	r.calculateHistory(ctx, &ctp, gitOperations)

	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.ChangeTransferPolicyConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get global promotion configuration: %w", err)
	}

	return ctrl.Result{
		RequeueAfter: requeueDuration,
	}, nil
}

// calculateHistory this function calculates the history by getting the first parents on the active branch and using the trailers to reconstruct the history.
// calculateHistory calculates the history by getting the first parents on the active branch and using the trailers to reconstruct the history.
// This function is best effort and will log errors but continue processing if it encounters issues with individual commits. This is because history is stored in git
// in order to get out of a bad state requires re-writing git history or pushing a bunch of commits greater than the max history limit.
func (r *ChangeTransferPolicyReconciler) calculateHistory(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy, gitOperations *git.EnvironmentOperations) {
	logger := log.FromContext(ctx)

	shaListActive, err := gitOperations.GetRevListFirstParent(ctx, "origin/"+ctp.Spec.ActiveBranch, 5)
	if err != nil {
		logger.V(4).Info("failed to get rev-list commit history for active branch", "branch", ctp.Spec.ActiveBranch, "err", err)
		return
	}
	logger.V(4).Info("Rev-list history for active branch", "shaList", shaListActive)

	history := make([]promoterv1alpha1.History, 0, len(shaListActive))
	for _, sha := range shaListActive {
		historyEntry, shouldInclude, err := r.buildHistoryEntry(ctx, sha, gitOperations)
		if err != nil {
			logger.V(4).Info("failed to build history entry", "sha", sha, "err", err)
			continue
		}

		if shouldInclude {
			history = append(history, historyEntry)
		}
	}

	ctp.Status.History = history
}

// buildHistoryEntry creates a single history entry for the given SHA
func (r *ChangeTransferPolicyReconciler) buildHistoryEntry(ctx context.Context, sha string, gitOperations *git.EnvironmentOperations) (promoterv1alpha1.History, bool, error) {
	activeTrailers, err := gitOperations.GetTrailers(ctx, sha)
	if err != nil {
		return promoterv1alpha1.History{}, false, fmt.Errorf("failed to get trailers for SHA %q: %w", sha, err)
	}

	historyEntry := promoterv1alpha1.History{
		Proposed:    promoterv1alpha1.CommitBranchStateHistoryProposed{},
		Active:      promoterv1alpha1.CommitBranchState{},
		PullRequest: &promoterv1alpha1.PullRequestCommonStatus{},
	}

	r.populateActiveMetadata(ctx, &historyEntry, sha, gitOperations)
	r.populateProposedMetadata(ctx, &historyEntry, activeTrailers, gitOperations)
	r.populatePullRequestMetadata(ctx, &historyEntry, activeTrailers)
	r.populateCommitStatuses(ctx, &historyEntry, activeTrailers)

	return historyEntry, true, nil
}

// getFirstTrailerValue returns the first value for a given trailer key, or an empty string if not found.
func getFirstTrailerValue(trailers map[string][]string, key string) string {
	if values, ok := trailers[key]; ok && len(values) > 0 {
		return values[0]
	}
	return ""
}

// populateActiveMetadata populates the active metadata for a history entry
func (r *ChangeTransferPolicyReconciler) populateActiveMetadata(ctx context.Context, h *promoterv1alpha1.History, sha string, gitOperations *git.EnvironmentOperations) {
	logger := log.FromContext(ctx)
	activeHydrated, err := gitOperations.GetShaMetadataFromGit(ctx, sha)
	if err != nil {
		logger.V(4).Info("failed to get active historic metadata from git", "sha", sha, "error", err)
	}
	h.Active.Hydrated = activeHydrated
	h.Active.Hydrated.Body = removeKnownTrailers(h.Active.Hydrated.Body)

	activeDry, err := gitOperations.GetShaMetadataFromFile(ctx, sha)
	if err != nil {
		logger.V(4).Info("failed to get active historic metadata from file", "sha", sha, "error", err)
	}
	h.Active.Dry = activeDry
}

// populateProposedMetadata populates the proposed metadata for a history entry
func (r *ChangeTransferPolicyReconciler) populateProposedMetadata(ctx context.Context, h *promoterv1alpha1.History, activeTrailers map[string][]string, gitOperations *git.EnvironmentOperations) {
	logger := log.FromContext(ctx)

	proposedHydratedSha := getFirstTrailerValue(activeTrailers, constants.TrailerShaHydratedProposed)
	if proposedHydratedSha == "" {
		logger.V(4).Info("No " + constants.TrailerShaHydratedProposed + " trailer found")
		return
	}

	meta, err := gitOperations.GetShaMetadataFromGit(ctx, proposedHydratedSha)
	if err != nil {
		logger.V(4).Info("failed to get proposed historic metadata from git", "sha", proposedHydratedSha, "error", err)
	}
	h.Proposed.Hydrated = meta
}

// populatePullRequestMetadata populates the pull request metadata for a history entry
func (r *ChangeTransferPolicyReconciler) populatePullRequestMetadata(ctx context.Context, h *promoterv1alpha1.History, activeTrailers map[string][]string) {
	logger := log.FromContext(ctx)

	if pullRequestID := getFirstTrailerValue(activeTrailers, constants.TrailerPullRequestID); pullRequestID != "" {
		h.PullRequest.ID = pullRequestID
	} else {
		logger.V(4).Info("No " + constants.TrailerPullRequestID + " found in trailers")
	}

	if pullRequestUrl := getFirstTrailerValue(activeTrailers, constants.TrailerPullRequestUrl); pullRequestUrl != "" {
		if !strings.HasPrefix(pullRequestUrl, "http://") && !strings.HasPrefix(pullRequestUrl, "https://") {
			logger.V(4).Info("pull request URL does not start with http:// or https://", "url", pullRequestUrl)
		} else {
			h.PullRequest.Url = pullRequestUrl
		}
	} else {
		logger.V(4).Info("No " + constants.TrailerPullRequestUrl + " found in trailers")
	}

	if timeStr := getFirstTrailerValue(activeTrailers, constants.TrailerPullRequestCreationTime); timeStr != "" {
		if creationTime, err := time.Parse(time.RFC3339, timeStr); err != nil {
			logger.V(4).Info("failed to parse "+constants.TrailerPullRequestCreationTime, "time", timeStr, "err", err)
		} else {
			h.PullRequest.PRCreationTime = metav1.NewTime(creationTime)
		}
	} else {
		logger.V(4).Info("No " + constants.TrailerPullRequestCreationTime + " found in trailers")
	}

	if timeStr := getFirstTrailerValue(activeTrailers, constants.TrailerPullRequestMergeTime); timeStr != "" {
		if mergeTime, err := time.Parse(time.RFC3339, timeStr); err != nil {
			logger.V(4).Info("failed to parse "+constants.TrailerPullRequestMergeTime, "time", timeStr, "err", err)
		} else {
			h.PullRequest.PRMergeTime = metav1.NewTime(mergeTime)
		}
	} else {
		logger.V(4).Info("No " + constants.TrailerPullRequestMergeTime + " found in trailers")
	}
}

// populateCommitStatuses populates the commit statuses for a history entry
func (r *ChangeTransferPolicyReconciler) populateCommitStatuses(ctx context.Context, h *promoterv1alpha1.History, activeTrailers map[string][]string) {
	activeKeys, proposedKeys := getCommitStatusKeysFromTrailers(ctx, activeTrailers)

	h.Active.CommitStatuses = make([]promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase, 0, len(activeKeys))
	for _, key := range activeKeys {
		url := getFirstTrailerValue(activeTrailers, constants.TrailerCommitStatusActivePrefix+key+"-url")
		if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			log.FromContext(ctx).Error(errors.New("invalid URL"), "active commit status URL does not start with http:// or https://", "url", url, "key", key)
			url = ""
		}
		h.Active.CommitStatuses = append(h.Active.CommitStatuses, promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
			Key:   key,
			Phase: getFirstTrailerValue(activeTrailers, constants.TrailerCommitStatusActivePrefix+key+"-phase"),
			Url:   url,
		})
	}

	h.Proposed.CommitStatuses = make([]promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase, 0, len(proposedKeys))
	for _, key := range proposedKeys {
		url := getFirstTrailerValue(activeTrailers, constants.TrailerCommitStatusProposedPrefix+key+"-url")
		if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			log.FromContext(ctx).Error(errors.New("invalid URL"), "proposed commit status URL does not start with http:// or https://", "url", url, "key", key)
			url = ""
		}
		h.Proposed.CommitStatuses = append(h.Proposed.CommitStatuses, promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
			Key:   key,
			Phase: getFirstTrailerValue(activeTrailers, constants.TrailerCommitStatusProposedPrefix+key+"-phase"),
			Url:   url,
		})
	}
}

// getCommitStatusKeysFromTrailers extracts the commit status keys from the trailers in the given context.
func getCommitStatusKeysFromTrailers(ctx context.Context, trailers map[string][]string) (activeKeys []string, proposedKeys []string) {
	logger := log.FromContext(ctx)

	// This function extracts commit status keys from trailers with the given prefix.
	// It looks for keys that start with the prefix, trims the prefix, splits by "-", and joins all but the last part to form the commit status key.
	// This is under the assumption that the last part is always "-phase" or "-url" today and that it does not go over multiple "-" aka the ending can not be
	// -what-am-i-doing. This would return a bad key because it would contain -what-am-i.
	extractKeys := func(prefix string) []string {
		keys := []string{}
		for key := range trailers {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			key = strings.TrimPrefix(key, prefix)
			if key == "" {
				logger.V(4).Info("Skipping empty trailer key", "key", key)
				continue
			}
			parts := strings.Split(key, "-")
			if len(parts) < 2 {
				logger.V(4).Info("Skipping trailer with unexpected format", "key", key)
				continue
			}
			csKey := strings.Join(parts[:len(parts)-1], "-")
			// Append if it does not exist in keys
			if !slices.Contains(keys, csKey) {
				keys = append(keys, csKey)
			}
		}
		return keys
	}

	activeKeys = extractKeys(constants.TrailerCommitStatusActivePrefix)
	proposedKeys = extractKeys(constants.TrailerCommitStatusProposedPrefix)

	return activeKeys, proposedKeys
}

func removeKnownTrailers(input string) string {
	toRemove := []string{
		constants.TrailerPullRequestID,
		constants.TrailerPullRequestSourceBranch,
		constants.TrailerPullRequestTargetBranch,
		constants.TrailerPullRequestCreationTime,
		constants.TrailerPullRequestUrl,
		constants.TrailerCommitStatusActivePrefix,
		constants.TrailerCommitStatusProposedPrefix,
		constants.TrailerShaHydratedActive,
		constants.TrailerShaHydratedProposed,
		constants.TrailerShaDryActive,
		constants.TrailerShaDryProposed,
	}

	lines := strings.Split(input, "\n")
	filtered := make([]string, 0, len(lines))

	for _, line := range lines {
		shouldKeep := true
		for _, rm := range toRemove {
			if strings.HasPrefix(line, rm) {
				shouldKeep = false
				break
			}
		}
		if shouldKeep {
			filtered = append(filtered, line)
		}
	}

	result := strings.Join(filtered, "\n")
	result = strings.TrimSpace(result)
	return result
}

// SetupWithManager sets up the controller with the Manager.
func (r *ChangeTransferPolicyReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// This index gets used by the CommitStatus controller and the webhook server to find the ChangeTransferPolicy to trigger reconcile
	if err := mgr.GetFieldIndexer().IndexField(ctx, &promoterv1alpha1.ChangeTransferPolicy{}, ".status.proposed.hydrated.sha", func(rawObj client.Object) []string {
		//nolint:forcetypeassert
		ctp := rawObj.(*promoterv1alpha1.ChangeTransferPolicy)
		return []string{ctp.Status.Proposed.Hydrated.Sha}
	}); err != nil {
		return fmt.Errorf("failed to set field index for .status.proposed.hydrated.sha: %w", err)
	}

	// This gets used by the CommitStatus controller to find the ChangeTransferPolicy to trigger reconcile
	if err := mgr.GetFieldIndexer().IndexField(ctx, &promoterv1alpha1.ChangeTransferPolicy{}, ".status.active.hydrated.sha", func(rawObj client.Object) []string {
		//nolint:forcetypeassert
		ctp := rawObj.(*promoterv1alpha1.ChangeTransferPolicy)
		return []string{ctp.Status.Active.Hydrated.Sha}
	}); err != nil {
		return fmt.Errorf("failed to set field index for .status.active.hydrated.sha: %w", err)
	}

	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.ChangeTransferPolicyConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get ChangeTransferPolicy rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.ChangeTransferPolicyConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get ChangeTransferPolicy max concurrent reconciles: %w", err)
	}

	// Create a channel for external enqueue requests. This allows other controllers
	// to trigger CTP reconciliation without modifying the CTP object.
	// The channel uses GenericEvent with a minimal CTP object containing just the namespace/name.
	// We use a buffer of 1024 to match the default internal buffer size of source.Channel.
	// Sends will block if the buffer is full, providing natural backpressure to callers.
	externalEnqueueChan := make(chan event.GenericEvent, 1024)

	// Store the enqueue function so it can be retrieved by other controllers.
	// This is a blocking send - callers will wait if the channel buffer is full.
	r.enqueueFunc = func(namespace, name string) {
		ctp := &promoterv1alpha1.ChangeTransferPolicy{}
		ctp.SetNamespace(namespace)
		ctp.SetName(name)

		select {
		case externalEnqueueChan <- event.GenericEvent{Object: ctp}:
			// Sent successfully
		default:
			// Channel is full, log a warning and block until space is available
			log.FromContext(ctx).Info("CTP enqueue channel is full, blocking until space is available",
				"namespace", namespace, "name", name)
			externalEnqueueChan <- event.GenericEvent{Object: ctp}
		}
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.ChangeTransferPolicy{},
			builder.WithPredicates(predicate.Or(
				predicate.GenerationChangedPredicate{},
				// Webhooks trigger reconciliations by bumping an annotation.
				// TODO: use a custom predicate to only trigger on the specific annotation change.
				predicate.AnnotationChangedPredicate{},
			))).
		// This controller intentionally doesn't have a .Owns for CommitStatuses. Every reconcile of a CommitStatus
		// checks whether it needs to update a related ChangeTransferPolicy by setting an annotation. Avoiding .Owns
		// here avoids duplicate reconciliations.
		Owns(&promoterv1alpha1.PullRequest{}).
		// Watch for external enqueue requests from other controllers (e.g., PromotionStrategy).
		// The handler.EnqueueRequestForObject extracts the namespace/name from the GenericEvent.
		WatchesRawSource(source.Channel(externalEnqueueChan, &handler.EnqueueRequestForObject{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

func (r *ChangeTransferPolicyReconciler) getGitAuthProvider(ctx context.Context, scmProvider promoterv1alpha1.GenericScmProvider, secret *v1.Secret, namespace string, repoRef promoterv1alpha1.ObjectReference) (scms.GitOperationsProvider, error) {
	provider, err := gitauth.CreateGitOperationsProvider(ctx, r.Client, scmProvider, secret, client.ObjectKey{Namespace: namespace, Name: repoRef.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to create git operations provider: %w", err)
	}
	return provider, nil
}

func (r *ChangeTransferPolicyReconciler) calculateStatus(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy, gitOperations *git.EnvironmentOperations) error {
	logger := log.FromContext(ctx)

	// TODO: consider parallelizing parts of this function that are network-bound work.

	proposedShas, err := gitOperations.GetBranchShas(ctx, ctp.Spec.ProposedBranch)
	if err != nil {
		return fmt.Errorf("failed to get SHAs for proposed branch %q: %w", ctp.Spec.ProposedBranch, err)
	}

	activeShas, err := gitOperations.GetBranchShas(ctx, ctp.Spec.ActiveBranch)
	if err != nil {
		return fmt.Errorf("failed to get SHAs for active branch %q: %w", ctp.Spec.ActiveBranch, err)
	}

	logger.Info("Branch SHAs", "branchShas", map[string]git.BranchShas{
		ctp.Spec.ActiveBranch:   activeShas,
		ctp.Spec.ProposedBranch: proposedShas,
	})

	err = r.setCommitMetadata(ctx, ctp, gitOperations, activeShas.Hydrated, proposedShas.Hydrated)
	if err != nil {
		return fmt.Errorf("failed to set commit metadata: %w", err)
	}

	err = r.setCommitStatusState(ctx, &ctp.Status.Active, ctp.Spec.ActiveCommitStatuses)
	if err != nil {
		var tooManyMatchingShaError *TooManyMatchingShaError
		if errors.As(err, &tooManyMatchingShaError) {
			r.Recorder.Event(ctp, "Warning", constants.TooManyMatchingShaReason, constants.TooManyMatchingShaActiveMessage)
		}
		return fmt.Errorf("failed to set active commit status state: %w", err)
	}

	err = r.setCommitStatusState(ctx, &ctp.Status.Proposed, ctp.Spec.ProposedCommitStatuses)
	if err != nil {
		var tooManyMatchingShaError *TooManyMatchingShaError
		if errors.As(err, &tooManyMatchingShaError) {
			r.Recorder.Event(ctp, "Warning", constants.TooManyMatchingShaReason, constants.TooManyMatchingShaProposedMessage)
		}
		return fmt.Errorf("failed to set proposed commit status state: %w", err)
	}

	err = r.setPullRequestState(ctx, ctp)
	if err != nil {
		return fmt.Errorf("failed to set pull request status state: %w", err)
	}

	return nil
}

// NewTooManyMatchingShaError creates a new TooManyMatchingShaError. This error indicates that there are too many
// commit status resources matching the given SHA and key.
func NewTooManyMatchingShaError(commitStatusKey string, commitStatuses []promoterv1alpha1.CommitStatus) error {
	return &TooManyMatchingShaError{
		commitStatusKey: commitStatusKey,
		commitStatuses:  commitStatuses,
	}
}

// TooManyMatchingShaError is an error type that indicates that there are too many matching SHAs for a commit status.
type TooManyMatchingShaError struct {
	commitStatusKey string
	commitStatuses  []promoterv1alpha1.CommitStatus
}

// Error implements the error interface for TooManyMatchingShaError.
func (e *TooManyMatchingShaError) Error() string {
	// Construct a message that includes the namespace/name of each commit status.
	// If there are more than two, finish the message with "and X more..."
	var msg strings.Builder
	msg.WriteString("there are to many matching SHAs for the '" + e.commitStatusKey + "' commit status: ")
	for i, cs := range e.commitStatuses {
		if i > 0 {
			msg.WriteString(", ")
		}
		if i >= 2 {
			fmt.Fprintf(&msg, "and %d more...", len(e.commitStatuses)-i)
			break
		}
		fmt.Fprintf(&msg, "%s/%s", cs.Namespace, cs.Name)
	}
	return msg.String()
}

func (r *ChangeTransferPolicyReconciler) setCommitMetadata(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy, gitOperations *git.EnvironmentOperations, activeHydratedSha, proposedHydratedSha string) error {
	logger := log.FromContext(ctx)

	activeCommitMetadata, err := gitOperations.GetShaMetadataFromFile(ctx, activeHydratedSha)
	if err != nil {
		return fmt.Errorf("failed to get commit metadata for hydrated SHA %q: %w", activeHydratedSha, err)
	}
	ctp.Status.Active.Dry = activeCommitMetadata

	proposedCommitMetadata, err := gitOperations.GetShaMetadataFromFile(ctx, proposedHydratedSha)
	if err != nil {
		return fmt.Errorf("failed to get commit metadata for hydrated SHA %q: %w", activeHydratedSha, err)
	}
	ctp.Status.Proposed.Dry = proposedCommitMetadata

	activeCommitMetadata, err = gitOperations.GetShaMetadataFromGit(ctx, activeHydratedSha)
	if err != nil {
		return fmt.Errorf("failed to get commit active metadata for hydrated SHA %q: %w", activeHydratedSha, err)
	}
	ctp.Status.Active.Hydrated = activeCommitMetadata
	ctp.Status.Active.Hydrated.Body = removeKnownTrailers(ctp.Status.Active.Hydrated.Body)
	proposedCommitMetadata, err = gitOperations.GetShaMetadataFromGit(ctx, proposedHydratedSha)
	if err != nil {
		return fmt.Errorf("failed to get commit proposed metadata for hydrated SHA %q: %w", proposedHydratedSha, err)
	}
	ctp.Status.Proposed.Hydrated = proposedCommitMetadata

	// Read the git note for the proposed hydrated commit to get the Note.DrySha.
	// This is used by downstream environments to verify that hydration is complete
	// for a given dry commit before allowing promotion.
	proposedNote, err := gitOperations.GetHydratorNote(ctx, proposedHydratedSha)
	if err != nil {
		return fmt.Errorf("failed to get hydrator note for proposed hydrated SHA %q: %w", proposedHydratedSha, err)
	}
	ctp.Status.Proposed.Note = &promoterv1alpha1.HydratorMetadata{
		DrySha: proposedNote.DrySha,
	}
	logger.V(4).Info("Set proposed Note.DrySha from git note",
		"proposedHydratedSha", proposedHydratedSha,
		"noteDrySha", proposedNote.DrySha)

	return nil
}

// setCommitStatusState sets the hydrated and dry SHAs and commit times for the target commit branch state and sets the
// commit statuses.
func (r *ChangeTransferPolicyReconciler) setCommitStatusState(ctx context.Context, targetCommitBranchState *promoterv1alpha1.CommitBranchState, commitStatuses []promoterv1alpha1.CommitStatusSelector) error {
	logger := log.FromContext(ctx)

	commitStatusesState := []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{}
	var tooManyMatchingShaError error
	for _, status := range commitStatuses {
		var csList promoterv1alpha1.CommitStatusList
		// Find all the replicasets that match the commit status configured name and the sha of the hydrated commit
		err := r.List(ctx, &csList, &client.ListOptions{
			LabelSelector: labels.SelectorFromSet(map[string]string{
				promoterv1alpha1.CommitStatusLabel: utils.KubeSafeLabel(status.Key),
			}),
			FieldSelector: fields.SelectorFromSet(map[string]string{
				".spec.sha": targetCommitBranchState.Hydrated.Sha,
			}),
		})
		if err != nil {
			return fmt.Errorf("failed to list CommitStatuses for key %q and SHA %q: %w", status.Key, targetCommitBranchState.Hydrated.Sha, err)
		}

		found := false
		phase := promoterv1alpha1.CommitPhasePending
		if len(csList.Items) == 1 {
			commitStatusesState = append(commitStatusesState, promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
				Key:   status.Key,
				Phase: string(csList.Items[0].Status.Phase),
				Url:   csList.Items[0].Spec.Url,
			})
			found = true
			phase = csList.Items[0].Status.Phase
		} else if len(csList.Items) > 1 {
			// TODO: decided how to bubble up errors
			commitStatusesState = append(commitStatusesState, promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
				Key:   status.Key,
				Phase: string(promoterv1alpha1.CommitPhasePending),
			})
			tooManyMatchingShaError = NewTooManyMatchingShaError(status.Key, csList.Items)
			phase = promoterv1alpha1.CommitPhasePending
		} else if len(csList.Items) == 0 {
			// TODO: decided how to bubble up errors
			commitStatusesState = append(commitStatusesState, promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
				Key:   status.Key,
				Phase: string(promoterv1alpha1.CommitPhasePending),
			})
			found = false
			phase = promoterv1alpha1.CommitPhasePending
			// We might not want to event here because of the potential for a lot of events, when say ArgoCD is slow at updating the status
		}
		logger.Info("CommitStatus State",
			"key", status.Key,
			"sha", targetCommitBranchState.Hydrated.Sha,
			"phase", phase,
			"found", found,
			"toManyMatchingSha", tooManyMatchingShaError != nil,
			"foundCount", len(csList.Items))
	}

	// Keep the URL from previous reconciliation where the phase was a success, if the commit status was not found, likely due to a sha mismatch.
	// This is to ensure that the URL is not lost when the commit status is not found in the current reconciliation.
	// We do not want to solve this with the code below please do no uncomment it. A better solution would be to come up with
	// a standard that CommitStatus managers can use to informer the CTPs the URLs for the commit statuses for each environment.
	// for _, ctpStatusState := range targetCommitBranchState.CommitStatuses { // nolint:gocritic
	//	for i, calculatedCSState := range commitStatusesState {
	//		if calculatedCSState.Key == ctpStatusState.Key && ctpStatusState.Url != "" {
	//			commitStatusesState[i].Url = ctpStatusState.Url
	//		}
	//	}
	//}
	targetCommitBranchState.CommitStatuses = commitStatusesState

	return tooManyMatchingShaError
}

func (r *ChangeTransferPolicyReconciler) setPullRequestState(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy) error {
	logger := log.FromContext(ctx)

	pr := &promoterv1alpha1.PullRequestList{}
	err := r.List(ctx, pr, &client.ListOptions{
		Namespace: ctp.Namespace,
		LabelSelector: labels.SelectorFromSet(map[string]string{
			promoterv1alpha1.PromotionStrategyLabel:    utils.KubeSafeLabel(ctp.Labels[promoterv1alpha1.PromotionStrategyLabel]),
			promoterv1alpha1.ChangeTransferPolicyLabel: utils.KubeSafeLabel(ctp.Name),
			promoterv1alpha1.EnvironmentLabel:          utils.KubeSafeLabel(ctp.Spec.ActiveBranch),
		}),
	})
	if err != nil {
		return fmt.Errorf("failed to list PullRequests for ChangeTransferPolicy %q status update: %w", ctp.Name, err)
	}
	if len(pr.Items) == 0 {
		// No PR resource found - keep existing status to preserve ExternallyMergedOrClosed and other metadata.
		// This allows the CTP to maintain a record of the last known PR state even after the PR resource
		// is deleted (e.g., after external merge/close). The status is only replaced when a new PR is created.
		logger.V(4).Info("No PR resource found, preserving existing PR status in CTP")
		return nil
	}

	if len(pr.Items) > 1 {
		return tooManyPRsError(pr)
	}

	if ctp.Status.PullRequest == nil {
		ctp.Status.PullRequest = &promoterv1alpha1.PullRequestCommonStatus{}
	}

	logger.V(4).Info("CTP copying PR status",
		"prName", pr.Items[0].Name,
		"prState", pr.Items[0].Status.State,
		"prID", pr.Items[0].Status.ID,
		"prDeletionTimestamp", pr.Items[0].DeletionTimestamp,
		"specState", pr.Items[0].Spec.State,
		"statusState", pr.Items[0].Status.State,
		"hasCTPFinalizer", controllerutil.ContainsFinalizer(&pr.Items[0], promoterv1alpha1.ChangeTransferPolicyPullRequestFinalizer))

	ctp.Status.PullRequest.ID = pr.Items[0].Status.ID
	ctp.Status.PullRequest.State = pr.Items[0].Status.State
	ctp.Status.PullRequest.PRCreationTime = pr.Items[0].Status.PRCreationTime
	ctp.Status.PullRequest.Url = pr.Items[0].Status.Url
	ctp.Status.PullRequest.ExternallyMergedOrClosed = pr.Items[0].Status.ExternallyMergedOrClosed

	// If PR is being deleted and has our finalizer, we need to ensure the CTP status is persisted.
	// The status will be persisted by the defer in Reconcile, and then on the next reconcile
	// (triggered by enqueue below) the finalizer will be removed by handlePRFinalizerRemoval.
	if !pr.Items[0].DeletionTimestamp.IsZero() && controllerutil.ContainsFinalizer(&pr.Items[0], promoterv1alpha1.ChangeTransferPolicyPullRequestFinalizer) {
		logger.V(4).Info("PR being deleted with CTP finalizer, CTP status will be persisted and finalizer removed on next reconcile")
		// Enqueue a reconcile to trigger finalizer removal after status is persisted
		if r.enqueueFunc != nil {
			r.enqueueFunc(ctp.Namespace, ctp.Name)
		}
	}

	return nil
}

// handlePRFinalizerRemoval checks if there's a PR being deleted with our finalizer where the CTP status
// already matches the PR status. If so, it removes the finalizer to allow the PR to be deleted.
func (r *ChangeTransferPolicyReconciler) handlePRFinalizerRemoval(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy) error {
	logger := log.FromContext(ctx)

	// Find any PR resources for this CTP
	pr := &promoterv1alpha1.PullRequestList{}
	err := r.List(ctx, pr, &client.ListOptions{
		Namespace: ctp.Namespace,
		LabelSelector: labels.SelectorFromSet(map[string]string{
			promoterv1alpha1.PromotionStrategyLabel:    utils.KubeSafeLabel(ctp.Labels[promoterv1alpha1.PromotionStrategyLabel]),
			promoterv1alpha1.ChangeTransferPolicyLabel: utils.KubeSafeLabel(ctp.Name),
			promoterv1alpha1.EnvironmentLabel:          utils.KubeSafeLabel(ctp.Spec.ActiveBranch),
		}),
	})
	if err != nil {
		return fmt.Errorf("failed to list PullRequests for finalizer check: %w", err)
	}

	if len(pr.Items) == 0 {
		// No PR found, nothing to do
		return nil
	}

	if len(pr.Items) > 1 {
		// Multiple PRs found, return the error immediately
		return tooManyPRsError(pr)
	}

	prItem := &pr.Items[0]

	// Check if PR is being deleted and has our finalizer
	if prItem.DeletionTimestamp.IsZero() || !controllerutil.ContainsFinalizer(prItem, promoterv1alpha1.ChangeTransferPolicyPullRequestFinalizer) {
		return nil
	}

	// Check if CTP status already matches PR status (meaning status was already copied)
	if ctp.Status.PullRequest == nil {
		// CTP has no PR status yet, cannot remove finalizer safely
		logger.V(4).Info("PR being deleted but CTP has no PR status, cannot remove finalizer yet")
		return nil
	}

	// Verify that the CTP status matches the PR status
	statusMatches := ctp.Status.PullRequest.ID == prItem.Status.ID &&
		ctp.Status.PullRequest.State == prItem.Status.State &&
		boolPtrEqual(ctp.Status.PullRequest.ExternallyMergedOrClosed, prItem.Status.ExternallyMergedOrClosed)

	if !statusMatches {
		logger.V(4).Info("PR being deleted but CTP status doesn't match PR status, cannot remove finalizer yet",
			"ctpPRID", ctp.Status.PullRequest.ID,
			"prID", prItem.Status.ID,
			"ctpPRState", ctp.Status.PullRequest.State,
			"prState", prItem.Status.State,
			"ctpExternallyMergedOrClosed", ctp.Status.PullRequest.ExternallyMergedOrClosed,
			"prExternallyMergedOrClosed", prItem.Status.ExternallyMergedOrClosed)
		return nil
	}

	// Status matches, safe to remove finalizer
	logger.Info("Removing CTP finalizer from PR - status already synced",
		"prName", prItem.Name,
		"prID", prItem.Status.ID,
		"prState", prItem.Status.State)

	controllerutil.RemoveFinalizer(prItem, promoterv1alpha1.ChangeTransferPolicyPullRequestFinalizer)
	if err := r.Update(ctx, prItem); err != nil {
		return fmt.Errorf("failed to remove CTP finalizer from PullRequest: %w", err)
	}

	logger.V(4).Info("PR finalizer removed")
	return nil
}

// tooManyPRsError constructs an error indicating that there are too many open pull requests for the CTP.
func tooManyPRsError(pr *promoterv1alpha1.PullRequestList) error {
	prNames := make([]string, 0, len(pr.Items))
	for _, prItem := range pr.Items {
		prNames = append(prNames, prItem.Name)
	}
	// Only show the first 3 PR names and then indicate how many more there are
	summary := strings.Join(prNames, ", ")
	if len(prNames) > 3 {
		summary = strings.Join(prNames[:3], ", ") + fmt.Sprintf(" and %d more", len(prNames)-3)
	}
	return fmt.Errorf("found more than one open PullRequest: %s", summary)
}

func (r *ChangeTransferPolicyReconciler) creatOrUpdatePullRequest(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy) (*promoterv1alpha1.PullRequest, error) {
	logger := log.FromContext(ctx)
	if ctp.Status.Proposed.Dry.Sha == ctp.Status.Active.Dry.Sha {
		// If the proposed dry sha is the same as the active dry sha, no need to create a pull request
		logger.V(4).Info("No promotion needed - active branch already has proposed changes",
			"activeDrySha", ctp.Status.Active.Dry.Sha,
			"proposedDrySha", ctp.Status.Proposed.Dry.Sha)
		return nil, nil
	}

	logger.V(4).Info("Proposed dry sha, does not match active", "proposedDrySha", ctp.Status.Proposed.Dry.Sha, "activeDrySha", ctp.Status.Active.Dry.Sha)
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{Namespace: ctp.Namespace, Name: ctp.Spec.RepositoryReference.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository %q: %w", ctp.Spec.RepositoryReference.Name, err)
	}

	var prName string
	switch {
	case gitRepo.Spec.GitHub != nil:
		prName = utils.GetPullRequestName(gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch)
	case gitRepo.Spec.GitLab != nil:
		prName = utils.GetPullRequestName(gitRepo.Spec.GitLab.Namespace, gitRepo.Spec.GitLab.Name, ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch)
	case gitRepo.Spec.Forgejo != nil:
		prName = utils.GetPullRequestName(gitRepo.Spec.Forgejo.Owner, gitRepo.Spec.Forgejo.Name, ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch)
	case gitRepo.Spec.Gitea != nil:
		prName = utils.GetPullRequestName(gitRepo.Spec.Gitea.Owner, gitRepo.Spec.Gitea.Name, ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch)
	case gitRepo.Spec.Fake != nil:
		prName = utils.GetPullRequestName(gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch)
	case gitRepo.Spec.BitbucketCloud != nil:
		prName = utils.GetPullRequestName(gitRepo.Spec.BitbucketCloud.Owner, gitRepo.Spec.BitbucketCloud.Name, ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch)
	case gitRepo.Spec.AzureDevOps != nil:
		prName = utils.GetPullRequestName(gitRepo.Spec.AzureDevOps.Project, gitRepo.Spec.AzureDevOps.Name, ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch)
	default:
		return nil, errors.New("unsupported git repository type")
	}

	prName = utils.KubeSafeUniqueName(ctx, prName)

	templatePullRequestTemplate, err := r.SettingsMgr.GetPullRequestControllersTemplate(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get pull request template from settings: %w", err)
	}

	title, description, err := TemplatePullRequest(templatePullRequestTemplate, map[string]any{"ChangeTransferPolicy": ctp})
	if err != nil {
		return nil, fmt.Errorf("failed to template pull request: %w", err)
	}

	pr := promoterv1alpha1.PullRequest{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ctp.Namespace,
			Name:      prName,
		},
	}
	res, err := controllerutil.CreateOrUpdate(ctx, r.Client, &pr, func() error {
		kind := reflect.TypeOf(promoterv1alpha1.ChangeTransferPolicy{}).Name()
		gvk := promoterv1alpha1.GroupVersion.WithKind(kind)
		controllerRef := metav1.NewControllerRef(ctp, gvk)
		blockOwnerDeletion := true
		controllerRef.BlockOwnerDeletion = &blockOwnerDeletion

		pr.OwnerReferences = []metav1.OwnerReference{*controllerRef}
		pr.Labels = map[string]string{
			promoterv1alpha1.PromotionStrategyLabel:    utils.KubeSafeLabel(ctp.Labels[promoterv1alpha1.PromotionStrategyLabel]),
			promoterv1alpha1.ChangeTransferPolicyLabel: utils.KubeSafeLabel(ctp.Name),
			promoterv1alpha1.EnvironmentLabel:          utils.KubeSafeLabel(ctp.Spec.ActiveBranch),
		}
		// Add CTP finalizer to ensure we can copy status before PR is deleted
		controllerutil.AddFinalizer(&pr, promoterv1alpha1.ChangeTransferPolicyPullRequestFinalizer)
		pr.Spec.RepositoryReference = ctp.Spec.RepositoryReference
		pr.Spec.Title = title
		pr.Spec.TargetBranch = ctp.Spec.ActiveBranch
		pr.Spec.SourceBranch = ctp.Spec.ProposedBranch
		pr.Spec.Description = description
		pr.Spec.Commit.Message = fmt.Sprintf("%s\n\n%s", title, description)
		pr.Spec.MergeSha = ctp.Status.Proposed.Hydrated.Sha
		if pr.CreationTimestamp.IsZero() {
			// New PR
			pr.Spec.State = promoterv1alpha1.PullRequestOpen
			return nil
		}

		// Update existing PR
		commitTrailers := trailers{}
		commitTrailers[constants.TrailerPullRequestID] = pr.Status.ID
		commitTrailers[constants.TrailerPullRequestSourceBranch] = pr.Spec.SourceBranch
		commitTrailers[constants.TrailerPullRequestTargetBranch] = pr.Spec.TargetBranch
		commitTrailers[constants.TrailerPullRequestCreationTime] = pr.Status.PRCreationTime.Format(time.RFC3339)
		commitTrailers[constants.TrailerPullRequestUrl] = pr.Status.Url

		for _, status := range ctp.Status.Active.CommitStatuses {
			commitTrailers[constants.TrailerCommitStatusActivePrefix+status.Key+"-phase"] = status.Phase
			commitTrailers[constants.TrailerCommitStatusActivePrefix+status.Key+"-url"] = status.Url
		}
		for _, status := range ctp.Status.Proposed.CommitStatuses {
			commitTrailers[constants.TrailerCommitStatusProposedPrefix+status.Key+"-phase"] = status.Phase
			commitTrailers[constants.TrailerCommitStatusProposedPrefix+status.Key+"-url"] = status.Url
		}
		commitTrailers[constants.TrailerShaHydratedActive] = ctp.Status.Active.Hydrated.Sha
		commitTrailers[constants.TrailerShaHydratedProposed] = ctp.Status.Proposed.Hydrated.Sha
		commitTrailers[constants.TrailerShaDryActive] = ctp.Status.Active.Dry.Sha
		commitTrailers[constants.TrailerShaDryProposed] = ctp.Status.Proposed.Dry.Sha

		pr.Spec.Commit.Message = fmt.Sprintf("%s\n\n%s\n\n%s", pr.Spec.Title, pr.Spec.Description, commitTrailers)

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create or update PR %q: %w", prName, err)
	}
	switch res {
	case controllerutil.OperationResultCreated:
		r.Recorder.Event(ctp, "Normal", constants.PullRequestCreatedReason, fmt.Sprintf(constants.PullRequestCreatedMessage, pr.Name))
		logger.V(4).Info("Created pull request", "pullRequest", pr)
	case controllerutil.OperationResultNone:
		logger.V(4).Info("Pull request already exists and is up to date", "pullRequest", pr)
	case controllerutil.OperationResultUpdated:
		logger.V(4).Info("Updated pull request", "pullRequest", pr)
	default:
		logger.V(4).Info("Unexpected operation result", "result", res)
	}

	return &pr, nil
}

// mergePullRequests tries to merge the pull request if all the checks have passed and the environment is set to auto merge.
func (r *ChangeTransferPolicyReconciler) mergePullRequests(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy) (*promoterv1alpha1.PullRequest, error) {
	logger := log.FromContext(ctx)

	for i, status := range ctp.Status.Proposed.CommitStatuses {
		if status.Phase != string(promoterv1alpha1.CommitPhaseSuccess) {
			logger.V(4).Info("Proposed commit status is not success", "key", ctp.Spec.ProposedCommitStatuses[i].Key, "sha", ctp.Status.Proposed.Hydrated.Sha, "phase", status.Phase)
			return nil, nil
		}
	}

	if !*ctp.Spec.AutoMerge {
		return nil, nil
	}

	prl := promoterv1alpha1.PullRequestList{}
	// Find the PRs that match the proposed commit and the environment. There should only be one.
	err := r.List(ctx, &prl, &client.ListOptions{
		Namespace: ctp.Namespace,
		LabelSelector: labels.SelectorFromSet(map[string]string{
			promoterv1alpha1.PromotionStrategyLabel:    utils.KubeSafeLabel(ctp.Labels[promoterv1alpha1.PromotionStrategyLabel]),
			promoterv1alpha1.ChangeTransferPolicyLabel: utils.KubeSafeLabel(ctp.Name),
			promoterv1alpha1.EnvironmentLabel:          utils.KubeSafeLabel(ctp.Spec.ActiveBranch),
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list PullRequests for ChangeTransferPolicy %s and Environment %s: %w", ctp.Name, ctp.Spec.ActiveBranch, err)
	}

	if len(prl.Items) > 1 {
		return nil, tooManyPRsError(&prl)
	}

	if len(prl.Items) != 1 {
		return nil, nil
	}

	// We found 1 pull request process it.
	pullRequest := prl.Items[0]
	if pullRequest.Status.State == promoterv1alpha1.PullRequestOpen {
		logger.Info("Commit status checks passed", "branch", ctp.Spec.ActiveBranch,
			"activeCommitStatuses", ctp.Status.Active.CommitStatuses,
			"proposedCommitStatuses", ctp.Status.Proposed.CommitStatuses,
			"activeDryCommitTime", ctp.Status.Active.Dry.CommitTime)
	}

	if pullRequest.Status.State != promoterv1alpha1.PullRequestOpen {
		// Nothing to do, the PR has to be open to be merged.
		return &pullRequest, nil
	}

	if pullRequest.Spec.State != promoterv1alpha1.PullRequestOpen {
		// This is for the case where the PR is set to merge in k8s but something else is blocking it, like an external commit status check.
		logger.Info("Pull request can not be merged, probably due to SCM", "pr", pullRequest.Name)

		return &pullRequest, nil
	}

	if pullRequest.Status.ID == "" {
		// We could rely on XValidation to catch the missing ID when setting the PR to merged, but this gives a
		// better error message.

		// If the PR has a ready condition with status false, get that reason/message for this error message.
		prReadyCondition := meta.FindStatusCondition(pullRequest.Status.Conditions, string(promoterConditions.Ready))
		if prReadyCondition != nil && prReadyCondition.Status == metav1.ConditionFalse {
			return &pullRequest, fmt.Errorf("cannot merge PullRequest without an ID: PullRequest not ready: %s: %s", prReadyCondition.Reason, prReadyCondition.Message)
		}

		return &pullRequest, fmt.Errorf("cannot merge PullRequest %q without an ID", pullRequest.Name)
	}

	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var pr promoterv1alpha1.PullRequest
		err = r.Get(ctx, client.ObjectKey{Namespace: pullRequest.Namespace, Name: pullRequest.Name}, &pr, &client.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get PR %q: %w", pullRequest.Name, err)
		}
		pr.Spec.State = promoterv1alpha1.PullRequestMerged
		return r.Update(ctx, &pr)
	})
	if err != nil {
		return &pullRequest, fmt.Errorf("failed to update PR %q: %w", pullRequest.Name, err)
	}
	r.Recorder.Event(ctp, "Normal", constants.PullRequestMergedReason, fmt.Sprintf(constants.PullRequestMergedMessage, pullRequest.Name))
	logger.Info("Merged pull request")
	return &pullRequest, nil
}

// gitMergeStrategyOurs tests if there is a conflict between the active and proposed branches. If there is, we
// perform a merge with ours as the strategy. This is to prevent conflicts in the pull request by assuming that
// the proposed branch is the source of truth.
func (r *ChangeTransferPolicyReconciler) gitMergeStrategyOurs(ctx context.Context, gitOperations *git.EnvironmentOperations, ctp *promoterv1alpha1.ChangeTransferPolicy) error {
	logger := log.FromContext(ctx)
	logger.Info("Testing for conflicts between branches", "proposed", ctp.Spec.ProposedBranch, "active", ctp.Spec.ActiveBranch)

	// Check if there's a conflict between branches
	hasConflict, err := gitOperations.HasConflict(ctx, ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch)
	if err != nil {
		return fmt.Errorf("failed to check for conflicts between branches %q and %q: %w", ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch, err)
	}

	if !hasConflict {
		logger.V(4).Info("No conflicts detected between branches", "proposed", ctp.Spec.ProposedBranch, "active", ctp.Spec.ActiveBranch)
		return nil // No conflict, nothing to do
	}

	// If we have a conflict, perform a merge with "ours" strategy
	logger.Info("Conflicts detected, performing merge with 'ours' strategy", "proposed", ctp.Spec.ProposedBranch, "active", ctp.Spec.ActiveBranch)

	err = gitOperations.MergeWithOursStrategy(ctx, ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch)
	if err != nil {
		return fmt.Errorf("failed to merge branches %q and %q with 'ours' strategy: %w", ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch, err)
	}

	r.Recorder.Event(ctp, "Normal", constants.ResolvedConflictReason, fmt.Sprintf(constants.ResolvedConflictMessage, ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch))

	return nil
}

// TemplatePullRequest renders the title and description of a pull request using the provided data map.
func TemplatePullRequest(prt promoterv1alpha1.PullRequestTemplate, data map[string]any) (string, string, error) {
	title, err := utils.RenderStringTemplate(prt.Title, data)
	if err != nil {
		return "", "", fmt.Errorf("failed to render pull request title template: %w", err)
	}

	description, err := utils.RenderStringTemplate(prt.Description, data)
	if err != nil {
		return "", "", fmt.Errorf("failed to render pull request description template: %w", err)
	}

	return title, description, nil
}

// boolPtrEqual compares two *bool pointers for equality.
// Returns true if both are nil, or if both are non-nil and point to equal values.
func boolPtrEqual(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}
