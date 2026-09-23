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
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/gitauth"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// CTPHEnqueueFunc is a function type that can be used to enqueue ChangeTransferPolicyHistory reconcile
// requests without modifying the ChangeTransferPolicyHistory object. This is used by other controllers
// (like ChangeTransferPolicy after writing a promotion-history git note) to trigger reconciliation
// without causing object conflicts.
type CTPHEnqueueFunc func(namespace, name string)

// ChangeTransferPolicyHistoryReconciler reconciles a ChangeTransferPolicyHistory object
type ChangeTransferPolicyHistoryReconciler struct {
	client.Client
	Recorder    events.EventRecorder
	Scheme      *runtime.Scheme
	SettingsMgr *settings.Manager

	// enqueueFunc is set during SetupWithManager and can be retrieved via GetEnqueueFunc.
	// It allows other controllers to enqueue ChangeTransferPolicyHistory reconcile requests.
	enqueueFunc CTPHEnqueueFunc
}

// GetEnqueueFunc returns a function that can be used to enqueue ChangeTransferPolicyHistory reconcile
// requests. This should be called after SetupWithManager has been called.
func (r *ChangeTransferPolicyHistoryReconciler) GetEnqueueFunc() CTPHEnqueueFunc {
	return r.enqueueFunc
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicyhistories,verbs=get;list;watch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicyhistories/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicyhistories/finalizers,verbs=update
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicies,verbs=get;list;watch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories,verbs=get;list;watch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmproviders,verbs=get;list;watch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=clusterscmproviders,verbs=get;list;watch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile rebuilds the promotion history for one environment's active branch from git (rev-list
// first-parent walk plus promotion-history git notes / commit message trailers) and stores it in
// the ChangeTransferPolicyHistory status.
func (r *ChangeTransferPolicyHistoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ChangeTransferPolicyHistory")
	startTime := time.Now()

	var ctph promoterv1alpha1.ChangeTransferPolicyHistory
	// This function applies the resource status via Server-Side Apply at the end of the reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &ctph, r.Client, r.Recorder, constants.ChangeTransferPolicyHistoryControllerFieldOwner, &result, &err, &previousReady)

	err = r.Get(ctx, req.NamespacedName, &ctph, &client.GetOptions{})
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			logger.Info("ChangeTransferPolicyHistory not found")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "failed to get ChangeTransferPolicyHistory")
		return ctrl.Result{}, fmt.Errorf("failed to get ChangeTransferPolicyHistory: %w", err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	previousReady = utils.RemoveReadyCondition(&ctph)

	if err := ensureControllerInstanceIDStable(ctx, r.SettingsMgr); err != nil {
		return ctrl.Result{}, err
	}

	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.ChangeTransferPolicyHistoryConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get global promotion configuration: %w", err)
	}

	// Cheap fast-path before any git/network work: when the sibling ChangeTransferPolicy's active tip
	// is already fully described by the newest history entry, the rev-list window and trailer content
	// on those commits are immutable in git and a rebuild would produce the same result.
	if activeSha, ok := r.siblingActiveSha(ctx, &ctph); ok && shouldSkipHistoryRecalculation(ctph.Status.History, activeSha) {
		logger.V(4).Info("skipping history recalculation, newest history entry describes the active tip")
		return ctrl.Result{RequeueAfter: requeueDuration}, nil
	}

	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, r.SettingsMgr.GetControllerNamespace(), ctph.Spec.RepositoryReference, &ctph)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get ScmProvider and secret for repo %q: %w", ctph.Spec.RepositoryReference.Name, err)
	}

	gitAuthProvider, err := gitauth.CreateGitOperationsProvider(ctx, r.Client, scmProvider, secret, client.ObjectKey{Namespace: ctph.Namespace, Name: ctph.Spec.RepositoryReference.Name})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create git auth provider for ScmProvider %q: %w", scmProvider.GetName(), err)
	}
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{Namespace: ctph.GetNamespace(), Name: ctph.Spec.RepositoryReference.Name})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get GitRepository: %w", err)
	}
	// Use this ChangeTransferPolicyHistory's own git identity. The git package is not concurrency-safe
	// within a single identity, and the ChangeTransferPolicy controller reconciles the same repo under
	// its own identity concurrently; per-identity clones are independent and safe.
	gitOperations := git.NewEnvironmentOperations(gitRepo, gitAuthProvider, ctph.Namespace+"/"+ctph.Name)

	err = gitOperations.CloneRepo(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to clone repo %q: %w", ctph.Spec.RepositoryReference.Name, err)
	}

	// Sync the active branch and git notes refs in one remote round trip: history entries are built from
	// the promotion-history notes ref when present, and GetBranchSha below reuses the synced branch SHA.
	err = gitOperations.SyncRefs(ctx, ctph.Spec.ActiveBranch)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to sync git refs: %w", err)
	}

	// Resolve the active branch tip. SyncRefs above already fetched it if it changed, so this normally
	// needs no remote calls. GetRevListFirstParent requires the branch commits to be present in the
	// local clone.
	lastSeenSha := ""
	if len(ctph.Status.History) > 0 {
		lastSeenSha = ctph.Status.History[0].Active.Hydrated.Sha
	}
	activeSha, err := gitOperations.GetBranchSha(ctx, ctph.Spec.ActiveBranch, lastSeenSha)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get SHA for active branch %q: %w", ctph.Spec.ActiveBranch, err)
	}

	// Recheck the skip guard against the fetched tip. This covers resources without a sibling
	// ChangeTransferPolicy (the fast-path above could not run) whose branch has not moved.
	if shouldSkipHistoryRecalculation(ctph.Status.History, activeSha) {
		logger.V(4).Info("skipping history recalculation, newest history entry describes the fetched active tip")
		return ctrl.Result{RequeueAfter: requeueDuration}, nil
	}

	history, err := calculateHistory(ctx, ctph.Spec.ActiveBranch, ctph.Spec.ActivePath, gitOperations)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to calculate history for active branch %q: %w", ctph.Spec.ActiveBranch, err)
	}
	ctph.Status.History = history

	return ctrl.Result{
		RequeueAfter: requeueDuration,
	}, nil
}

// siblingActiveSha returns the active hydrated SHA of the ChangeTransferPolicy that owns this
// ChangeTransferPolicyHistory. The second return value is false when there is no controller owner
// or the owning ChangeTransferPolicy cannot be read.
func (r *ChangeTransferPolicyHistoryReconciler) siblingActiveSha(ctx context.Context, ctph *promoterv1alpha1.ChangeTransferPolicyHistory) (string, bool) {
	owner := metav1.GetControllerOf(ctph)
	if owner == nil || owner.Kind != "ChangeTransferPolicy" {
		return "", false
	}

	var ctp promoterv1alpha1.ChangeTransferPolicy
	if err := r.Get(ctx, client.ObjectKey{Namespace: ctph.Namespace, Name: owner.Name}, &ctp); err != nil {
		log.FromContext(ctx).V(4).Info("failed to get owning ChangeTransferPolicy", "err", err)
		return "", false
	}
	return ctp.Status.Active.Hydrated.Sha, true
}

// SetupWithManager sets up the controller with the Manager.
func (r *ChangeTransferPolicyHistoryReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.ChangeTransferPolicyHistoryConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get ChangeTransferPolicyHistory rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.ChangeTransferPolicyHistoryConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get ChangeTransferPolicyHistory max concurrent reconciles: %w", err)
	}

	// Create a channel for external enqueue requests. This allows other controllers (the
	// ChangeTransferPolicy controller after writing a promotion-history note) to trigger
	// reconciliation without modifying the ChangeTransferPolicyHistory object.
	// We use a buffer of 1024 to match the default internal buffer size of source.Channel.
	// Sends will block if the buffer is full, providing natural backpressure to callers.
	externalEnqueueChan := make(chan event.GenericEvent, 1024)

	// Store the enqueue function so it can be retrieved by other controllers.
	// This is a blocking send - callers will wait if the channel buffer is full.
	r.enqueueFunc = func(namespace, name string) {
		ctph := &promoterv1alpha1.ChangeTransferPolicyHistory{}
		ctph.SetNamespace(namespace)
		ctph.SetName(name)

		select {
		case externalEnqueueChan <- event.GenericEvent{Object: ctph}:
			// Sent successfully
		default:
			// Channel is full, log a warning and block until space is available
			log.FromContext(ctx).Info("ChangeTransferPolicyHistory enqueue channel is full, blocking until space is available",
				"namespace", namespace, "name", name)
			externalEnqueueChan <- event.GenericEvent{Object: ctph}
		}
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.ChangeTransferPolicyHistory{},
			builder.WithPredicates(predicate.Or(
				predicate.GenerationChangedPredicate{},
				predicate.AnnotationChangedPredicate{},
			))).
		// Wake up when the sibling ChangeTransferPolicy observes a new active tip or a pull request
		// state change. The predicate is load-bearing: CTP status is SSA-applied on every reconcile
		// pass, and without it every CTP requeue tick would wake this controller.
		Watches(&promoterv1alpha1.ChangeTransferPolicy{},
			handler.EnqueueRequestsFromMapFunc(r.mapCTPToChangeTransferPolicyHistories),
			builder.WithPredicates(ctpUpdateEnqueuesChangeTransferPolicyHistoryPredicate())).
		// Watch for external enqueue requests from other controllers.
		WatchesRawSource(source.Channel(externalEnqueueChan, &handler.EnqueueRequestForObject{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

// mapCTPToChangeTransferPolicyHistories maps a ChangeTransferPolicy event to the history object
// this CTP owns. The name is derived from the CTP name, matching upsertChangeTransferPolicyHistory.
func (r *ChangeTransferPolicyHistoryReconciler) mapCTPToChangeTransferPolicyHistories(_ context.Context, obj client.Object) []reconcile.Request {
	return []reconcile.Request{{
		NamespacedName: client.ObjectKey{
			Namespace: obj.GetNamespace(),
			Name:      utils.GetChangeTransferPolicyHistoryName(obj.GetName()),
		},
	}}
}

// ctpUpdateEnqueuesChangeTransferPolicyHistoryPredicate returns a predicate that only lets a
// ChangeTransferPolicy update through when something history-relevant changed: the active hydrated
// tip moved, or the observed pull request (ID, state, or merged target SHA) changed. Everything else
// (periodic status re-applies, proposed-side changes, commit status churn) is filtered out.
func ctpUpdateEnqueuesChangeTransferPolicyHistoryPredicate() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldCTP, ok := e.ObjectOld.(*promoterv1alpha1.ChangeTransferPolicy)
			if !ok {
				return false
			}
			newCTP, ok := e.ObjectNew.(*promoterv1alpha1.ChangeTransferPolicy)
			if !ok {
				return false
			}

			if oldCTP.Status.Active.Hydrated.Sha != newCTP.Status.Active.Hydrated.Sha {
				return true
			}
			return !pullRequestCommonStatusesEqual(oldCTP.Status.PullRequest, newCTP.Status.PullRequest)
		},
	}
}

// pullRequestCommonStatusesEqual reports whether the history-relevant fields of two pull request
// statuses are equal. Nil is treated as equal to nil only.
func pullRequestCommonStatusesEqual(a, b *promoterv1alpha1.PullRequestCommonStatus) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.ID == b.ID && a.State == b.State && a.MergedTargetSha == b.MergedTargetSha
}

// shouldSkipHistoryRecalculation reports whether Status.History already fully describes the current
// active hydrated tip. History is derived from the top of the active branch; when the newest entry
// carries a pull request ID and its active and merged-target SHAs match the current tip, the rev-list
// window and trailer content on those commits are immutable in git.
//
// calculateHistory is best effort per entry, so a reconcile may have left Status.History holding a
// half-populated newest entry (a per-sha metadata read failed). Requiring the newest entry to fully
// describe the current tip makes those failures self-healing on the next reconcile.
//
// A newest entry without a pull request ID is never trusted. The ChangeTransferPolicy controller
// writes the promotion-history git note during PR finalization; a rebuild that ran before the note
// was pushed holds a trailer-less entry (SHAs matching the tip, no PR ID). Skipping there would pin
// the stale entry until the active tip moves. Rebuilding whenever the PR ID is missing keeps that
// path self-healing: once the note is on the remote, the rebuild picks it up. Commits that genuinely
// carry no PR metadata (direct pushes to the active branch) simply keep the behavior of recalculating
// every reconcile.
//
// A Spec.ActivePath change without a new active tip is not detected here; history is refreshed on
// the next promotion that moves the active branch.
func shouldSkipHistoryRecalculation(history []promoterv1alpha1.History, activeSha string) bool {
	if activeSha == "" || len(history) == 0 {
		return false
	}
	newest := history[0]
	return newest.PullRequest != nil && newest.PullRequest.ID != "" &&
		newest.PullRequest.MergedTargetSha == activeSha &&
		newest.Active.Hydrated.Sha == activeSha
}

// calculateHistory calculates the history by getting the first parents on the active branch and using the trailers to reconstruct the history.
// Failures preparing the rev-list window (rev-list itself or the object prefetches) are returned as errors; failures building an
// individual entry are logged and the entry is skipped, because history is stored in git and getting out of a bad state would require
// re-writing git history or pushing a number of commits greater than the max history limit.
func calculateHistory(ctx context.Context, activeBranch, activePath string, gitOperations *git.EnvironmentOperations) ([]promoterv1alpha1.History, error) {
	logger := log.FromContext(ctx)

	shaListActive, err := gitOperations.GetRevListFirstParent(ctx, "origin/"+activeBranch, promoterv1alpha1.MaxPromotionHistory)
	if err != nil {
		return nil, fmt.Errorf("failed to get rev-list commit history for active branch %q: %w", activeBranch, err)
	}
	logger.V(4).Info("Rev-list history for active branch", "shaList", shaListActive)

	// We know which active commits we'll need, so pre-load them.
	if err := gitOperations.LoadCommitAndMetadataBlobs(ctx, activePath, shaListActive...); err != nil {
		return nil, fmt.Errorf("failed to prefetch history commit objects: %w", err)
	}

	// Each active commit has a corresponding proposed commit. Get those shas so we can preload them.
	var proposedHistoryShas []string
	for _, sha := range shaListActive {
		trailers, err := gitOperations.GetTrailers(ctx, sha)
		if err != nil {
			logger.V(4).Info("failed to get trailers while prefetching proposed history commits", "sha", sha, "err", err)
			continue
		}
		if proposedSha := getFirstTrailerValue(trailers, constants.TrailerShaHydratedProposed); proposedSha != "" {
			proposedHistoryShas = append(proposedHistoryShas, proposedSha)
		}
	}
	if len(proposedHistoryShas) > 0 {
		if err := gitOperations.LoadCommits(ctx, proposedHistoryShas...); err != nil {
			return nil, fmt.Errorf("failed to prefetch proposed history commit objects: %w", err)
		}
	}

	history := make([]promoterv1alpha1.History, 0, len(shaListActive))
	for _, sha := range shaListActive {
		historyEntry, shouldInclude, err := buildHistoryEntry(ctx, sha, activePath, gitOperations)
		if err != nil {
			logger.V(4).Info("failed to build history entry", "sha", sha, "err", err)
			continue
		}

		if shouldInclude {
			history = append(history, historyEntry)
		}
	}

	return history, nil
}

// buildHistoryEntry creates a single history entry for the given SHA. The trailer data comes from the
// promotion-history git note when one exists (written at PR finalization, surviving SCM-side message
// rewrites). When no note is readable — commits predating the notes, or a note that was never written
// because finalization failed — it falls back to the commit message trailers, which the promoter still
// writes on every managed pull request and which survive any merge style that preserves the message.
func buildHistoryEntry(ctx context.Context, sha, activePath string, gitOperations *git.EnvironmentOperations) (promoterv1alpha1.History, bool, error) {
	logger := log.FromContext(ctx)

	activeTrailers, err := gitOperations.GetHistoryNote(ctx, sha)
	if err != nil {
		logger.V(4).Info("failed to get history note, falling back to commit message trailers", "sha", sha, "err", err)
	}
	if len(activeTrailers) == 0 {
		activeTrailers, err = gitOperations.GetTrailers(ctx, sha)
		if err != nil {
			return promoterv1alpha1.History{}, false, fmt.Errorf("failed to get trailers for SHA %q: %w", sha, err)
		}
	}

	historyEntry := promoterv1alpha1.History{
		Proposed:    promoterv1alpha1.CommitBranchStateHistoryProposed{},
		Active:      promoterv1alpha1.CommitBranchState{},
		PullRequest: &promoterv1alpha1.PullRequestCommonStatus{},
	}

	populateActiveMetadata(ctx, &historyEntry, sha, activePath, gitOperations)
	populateProposedMetadata(ctx, &historyEntry, activeTrailers, gitOperations)
	populatePullRequestMetadata(ctx, &historyEntry, activeTrailers)
	populateCommitStatuses(ctx, &historyEntry, activeTrailers)
	historyEntry.MergeCommitSnapshotMismatch = getFirstTrailerValue(activeTrailers, constants.TrailerMergeCommitSnapshotMismatch) == "true"
	// The note is written on the merged target sha and history walks first-parent commits of the active
	// branch, so the entry's own sha is that commit; no trailer records it.
	historyEntry.PullRequest.MergedTargetSha = sha

	return historyEntry, true, nil
}

func decodeTrailerDescription(ctx context.Context, encoded string) string {
	if encoded == "" {
		return ""
	}
	var description string
	if err := json.Unmarshal([]byte(encoded), &description); err != nil {
		log.FromContext(ctx).Error(err, "failed to decode commit status description trailer", "encoded", encoded)
		return ""
	}
	return description
}

// populateActiveMetadata populates the active metadata for a history entry
func populateActiveMetadata(ctx context.Context, h *promoterv1alpha1.History, sha, activePath string, gitOperations *git.EnvironmentOperations) {
	logger := log.FromContext(ctx)
	activeHydrated, err := gitOperations.GetShaMetadataFromGit(ctx, sha)
	if err != nil {
		logger.V(4).Info("failed to get active historic metadata from git", "sha", sha, "error", err)
	}
	h.Active.Hydrated = activeHydrated
	h.Active.Hydrated.Body = removeKnownTrailers(h.Active.Hydrated.Body)

	activeDry, err := gitOperations.GetShaMetadataFromFile(ctx, sha, activePath)
	if err != nil {
		logger.V(4).Info("failed to get active historic metadata from file", "sha", sha, "error", err)
	}
	h.Active.Dry = activeDry
}

// populateProposedMetadata populates the proposed metadata for a history entry
func populateProposedMetadata(ctx context.Context, h *promoterv1alpha1.History, activeTrailers map[string][]string, gitOperations *git.EnvironmentOperations) {
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
func populatePullRequestMetadata(ctx context.Context, h *promoterv1alpha1.History, activeTrailers map[string][]string) {
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
func populateCommitStatuses(ctx context.Context, h *promoterv1alpha1.History, activeTrailers map[string][]string) {
	activeKeys, proposedKeys := getCommitStatusKeysFromTrailers(ctx, activeTrailers)

	h.Active.CommitStatuses = make([]promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase, 0, len(activeKeys))
	for _, key := range activeKeys {
		url := getFirstTrailerValue(activeTrailers, constants.TrailerCommitStatusActivePrefix+key+"-url")
		if url != "" && !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			log.FromContext(ctx).Error(errors.New("invalid URL"), "active commit status URL does not start with http:// or https://", "url", url, "key", key)
			url = ""
		}
		h.Active.CommitStatuses = append(h.Active.CommitStatuses, promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
			Key:         key,
			Phase:       getFirstTrailerValue(activeTrailers, constants.TrailerCommitStatusActivePrefix+key+"-phase"),
			Url:         url,
			Description: decodeTrailerDescription(ctx, getFirstTrailerValue(activeTrailers, constants.TrailerCommitStatusActivePrefix+key+"-description")),
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
			Key:         key,
			Phase:       getFirstTrailerValue(activeTrailers, constants.TrailerCommitStatusProposedPrefix+key+"-phase"),
			Url:         url,
			Description: decodeTrailerDescription(ctx, getFirstTrailerValue(activeTrailers, constants.TrailerCommitStatusProposedPrefix+key+"-description")),
		})
	}
}

// getCommitStatusKeysFromTrailers extracts the commit status keys from the trailers in the given context.
func getCommitStatusKeysFromTrailers(ctx context.Context, trailers map[string][]string) (activeKeys []string, proposedKeys []string) {
	logger := log.FromContext(ctx)

	// This function extracts commit status keys from trailers with the given prefix.
	// It looks for keys that start with the prefix, trims the prefix, splits by "-", and joins all but the last part to form the commit status key.
	// This is under the assumption that the last part is always "-phase", "-url", or "-description" and that it does not go over multiple "-" aka the ending can not be
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
