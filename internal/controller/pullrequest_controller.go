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
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/labels"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	bitbucket_cloud "github.com/argoproj-labs/gitops-promoter/internal/scms/bitbucket_cloud"
	bitbucket_datacenter "github.com/argoproj-labs/gitops-promoter/internal/scms/bitbucket_datacenter"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/forgejo"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitea"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitlab"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// PREnqueueFunc enqueues PullRequest reconcile requests without modifying the object.
// ChangeTransferPolicy uses this to wake the PR controller after trailer-only updates
// or when promotion is complete but a PR CR still exists.
type PREnqueueFunc func(namespace, name string)

// PullRequestReconciler reconciles a PullRequest object
type PullRequestReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager

	// enqueueFunc is set during SetupWithManager and can be retrieved via GetEnqueueFunc.
	enqueueFunc PREnqueueFunc
}

// GetEnqueueFunc returns a function that enqueues PullRequest reconcile requests.
// This should be called after SetupWithManager has been called.
func (r *PullRequestReconciler) GetEnqueueFunc() PREnqueueFunc {
	return r.enqueueFunc
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests,verbs=get;list;watch;delete;update
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories,verbs=get;list;watch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmproviders,verbs=get;list;watch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=clusterscmproviders,verbs=get;list;watch

// Reconcile syncs PullRequest state with SCM state.
//
// Keeping the PullRequest and the SCM in sync is complicated. This function could get unreadable if we let it. So we
// have some key principles and constraints that keep the controller maintainable.
//
// Principles:
//  1. Reconciliations are cheap. Don't be afraid to let one "task" spread across multiple reconciles.
//  2. SCM calls are expensive. Only the most narrow possible conditions should trigger an SCM call.
//  3. Focus on getting the happy path right. Trying to cover every edge case perfectly will make the logic
//     unmaintainable. Instead, make sure the failure mode isn't catastrophic, and report errors clearly.
//     A) Trust the SCM. Trying to handle SCM bugs or misbehavior is a maddening path. If we find that some SCM is
//     particularly problematic in a particular way, we can strategically introduce logic to handle that specific
//     case, ideally in SCM provider logic rather than controller logic.
//  4. Don't do the same thing twice. The logic should be simple enough that, for example, checking the same
//     prerequisite in two different places is obviously unnecessary.
//  5. Don't do the same thing in two places. There should be exactly one code path that reaches a particular
//     action. Don't duplicate the action, change the CR state to drive the next reconcile to the needed action.
//
// Key CR states:
//  1. Terminating: the deletion timestamp is set
//  2. Terminal Status:
//     A) spec.state is 'closed' OR
//     B) (status.state is 'merged', 'closed', or 'merged-or-closed')
//  3. Finalized Status: CR is Terminating AND
//     A) status.id is empty OR
//     B) (status.state is 'closed' OR 'unknown') OR
//     C) (status.state is 'merged' AND status.mergedTargetSha is non-empty)
//  4. Released: the CR is Terminating, AND our finalizer is removed
//
// Note: status.state value 'merged-or-closed' exists so that, when a PR is missing from FindOpen, we can communicate
// to the deletion flow that we need to try to recover the specific state and (if applicable) merged sha.
// 'unknown' exists so that we can bail if for some reason the PR disappears, and we can't get mergedTargetSha or
// confirm 'closed'.
//
// Note: constraints below ignore label syncing. We're mostly trying to cover the core merge operations. The pseudo-code
// includes an explanation about the labels so we can detect any logic problems, but otherwise they're not really
// considered.
//
// Constraints:
//  1. Make at most one "read" SCM call and one "write" SCM call per reconcile. If you need an additional call, set the
//     PullRequest state so that the next reconcile makes the call.
//  2. Persist at most one PullRequest change per reconcile. You can delete the CR, remove the finalizer, or update the
//     status. Status updates are handled automatically by utils.HandleReconciliationResult after Reconcile returns:
//     so don't update the status in Reconcile, just edit the pr.Status and return. If you think you need to do two of
//     these things, instead persist your change and let the next reconcile handle the next step. For example, if a new
//     code path needs to close the PR, set a Terminal Status so that the next reconcile deletes the CR and then the
//     subsequent one closes the PR. Adding the promoter finalizer (metadata-only Update) may occur in the same
//     reconcile as a status write when the object is first adopted.
//  3. Have at most one code path per SCM call type. You can call FindOpen, Get, Merge, Create, Update, or Close, and exactly
//     one code path should reach each of those. If you think you need a second place making a certain call, instead
//     persist changes to the CR status that ensure the next step will be taken care of by the next reconcile.
//  4. Have at most one code path for PullRequest changes per reconcile. Since util.HandleReconciliationResult
//     automatically persists in-memory changes to pr.Status, this means that the deletion path and the finalizer
//     removal path must not contain any changes to pr.Status.
//  5. Do not wrap SCM calls or PullRequest changes in utility functions that are called from more than one path. That's
//     just multiple call paths disguised.
//  6. Do not RequeueAfter a short time to chain reconciles. Successful non-terminal reconciles
//     that need another pass return RequeueAfter using pullRequest.workQueue.requeueDuration.
//     Predicates handle immediate requeue only for ->Terminal, ->Terminating, or ->Finalized.
//     Predicates are evaluated on cache contents; limiting immediate requeue to those transitions
//     makes it more likely that follow-up reconciles see an up-to-date object.
//  7. Do not move backwards. If the status is Terminating, don't do anything to make it not Terminating. If the status
//     is Finalized, don't do anything to make it not Finalized. If the finalizer is Released, do not add it back.
//     If code encounters SCM state that seems like it ought to cause us to move backwards, return an error clearly
//     explaining the unexpected situation. Trust the users to file a bug. If it's a common problem, we can assess how
//     to cover the edge case, ideally in SCM provider code instead of controller code.
//
// General design:
//
//	There are two "lanes": Terminating and non-Terminating. Each has different SCM calls it may make.
//
//	Terminating: Get -> Close.
//	non-Terminating: FindOpen -> Create, Update, or Merge
//
//	All calls are optional, since various short-circuits may skip them. But in all cases, an SCM
//	write operation will be preceded by one SCM read operation within that "lane".
//
// Pseudocode:
//
//		Note that in all error cases that don't directly map to some explicitly handled state, it's implied that we'll just
//		return the error and follow standard retry behavior.
//
//		if the CR is Terminating:
//		  if Released (promoter finalizer absent):
//		    Return.
//
//		  if CR is Finalized:
//		    Release finalizer and return.
//
//		  Call Get.
//
//		  if the PR is not found:
//		    Set status.state to 'unknown' and return.
//		  if the PR is closed:
//		    Set status.state to 'closed' and return.
//		  if the PR is open:
//		    Call Close, set status.state to 'closed', and return.
//		  if the PR is merged:
//		    Set status.state to 'merged'.
//		    if mergedTargetSha is not available:
//		      Return an error.
//		    Return.
//
//		if the CR status is Terminal:
//		  Delete the CR and return.
//
//		if SCM sync should be skipped (work avoidance short-circuit):
//		  Return.
//
//	 Note: if status.id is not empty, and spec.state is 'merged', we could do an optimistic Merge attempt here.
//	 Any failure would just be ignored. This would save one FindOpen on the happy path (Promoter merges the PR).
//	 That's left for a future enhancement.
//
//		Call FindOpen.
//
//		if not found:
//		  if status.id is empty:
//		    Call the SCM to Create the PR, then set status.id to the new ID, status.state to 'open', and return.
//
//		  Emit PullRequestExternallyMergedOrClosed, set status.state to 'merged-or-closed', and return.
//
//		if found:
//		  Set status.id to the found ID, status.state to 'open', and refresh applied labels from FindOpen when the provider
//		  reports them.
//
//		  if spec.state is 'merged':
//		    Call the SCM to merge the PR.
//		    Set status.state to 'merged' and, if available, set status.mergedTargetSha.
//		    Return.
//
//		  if title or description has drifted:
//		    Call the SCM to Update.
//
//		  if labels state has drifted:
//		    Make API calls to update the labels and update the status.
//
//		Return.
//
//nolint:gocyclo // Intentional linear state machine; splitting helpers would obscure docstring order.
func (r *PullRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling PullRequest")
	startTime := time.Now()

	var pr promoterv1alpha1.PullRequest
	// This function applies the resource status via Server-Side Apply at the end of the reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &pr, r.Client, r.Recorder, constants.PullRequestControllerFieldOwner, &result, &err, &previousReady)

	if err := r.Get(ctx, req.NamespacedName, &pr); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("PullRequest not found", "namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PullRequest: %w", err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	previousReady = utils.RemoveReadyCondition(&pr)

	if err := ensureControllerInstanceIDStable(ctx, r.SettingsMgr); err != nil {
		return ctrl.Result{}, err
	}

	// Terminating means the deletion timestamp is set.
	//nolint:nestif // Get-then-maybe-Close lane is one cohesive finalizer path; extracting branches adds indirection.
	if pullRequestIsTerminating(&pr) {
		if !controllerutil.ContainsFinalizer(&pr, promoterv1alpha1.PullRequestFinalizer) {
			// Either we cleared the finalizer, or it was never set. Either way, we're done.
			logger.V(4).Info("PullRequest is terminating and no longer holds the promoter finalizer, nothing left to do")
			return ctrl.Result{}, nil
		}

		if pullRequestStatusIsFinalized(&pr) {
			logger.V(4).Info("PullRequest is finalized, releasing finalizer")
			// Status is Finalized; release the promoter finalizer now so deletion can finish once other finalizers clear.
			return ctrl.Result{}, r.releaseFinalizer(ctx, &pr)
		}

		provider, err := r.getPullRequestProvider(ctx, pr)
		if err != nil {
			return ctrl.Result{}, err
		}

		details, err := provider.Get(ctx, pr)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get pull request: %w", err)
		}
		if !details.Found {
			// Far-edge case. The SCM has no info for us, so we have to leave the status in an unknown state.
			pr.Status.State = promoterv1alpha1.PullRequestUnknown
			// Status is now Finalized; persist and the ->Finalized predicate will requeue to release the finalizer.
			return ctrl.Result{}, nil
		}

		switch details.State {
		case promoterv1alpha1.PullRequestClosed:
			pr.Status.State = promoterv1alpha1.PullRequestClosed
			// Status is now Finalized; persist and the ->Finalized predicate will requeue to release the finalizer.
			return ctrl.Result{}, nil
		case promoterv1alpha1.PullRequestOpen:
			// Still open on the SCM; closing discharges the finalizer's obligation.
			if err := r.closePullRequest(ctx, &pr, provider); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to close pull request: %w", err)
			}
			// closePullRequest set status.state to closed (Finalized); persist and the ->Finalized predicate will requeue to release the finalizer.
			return ctrl.Result{}, nil
		case promoterv1alpha1.PullRequestMerged:
			pr.Status.State = promoterv1alpha1.PullRequestMerged
			pr.Status.MergedTargetSha = details.MergedTargetSHA
			if pr.Status.MergedTargetSha == "" {
				// mergedTargetSha still missing; stay not-Finalized and standard retry will re-run Get on the next reconcile.
				return ctrl.Result{}, fmt.Errorf("merged pull request %q missing mergedTargetSha after Get", pr.Status.ID)
			}
			// Status is now Finalized; persist and the ->Finalized predicate will requeue to release the finalizer.
			return ctrl.Result{}, nil
		default:
			return ctrl.Result{}, fmt.Errorf("terminating Get returned unexpected pull request state %q for id %q", details.State, pr.Status.ID)
		}
	}

	// Make sure finalizer is set to ensure cleanup.
	if err := r.ensureFinalizer(ctx, &pr); err != nil {
		return ctrl.Result{}, err
	}

	if pullRequestStatusIsTerminal(&pr) {
		logger.Info("Deleting terminal PullRequest", "pullRequestID", pr.Status.ID, "statusState", pr.Status.State, "specState", pr.Spec.State)
		if err := r.Delete(ctx, &pr); err != nil && !k8serrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("failed to delete PullRequest: %w", err)
		}
		// Promotion finished (merged/closed/merged-or-closed): delete the CR so the terminating
		// lane can release our finalizer after any remaining SCM outcome is recorded.
		return ctrl.Result{}, nil
	}

	// This short-circuit avoids FindOpen (and other) SCM calls for a very narrow kind of reconcile:
	// where the PR is marked open, the resource isn't being deleted, the spec has changed, and the
	// _only_ changes to the spec do not require an Update to the SCM PR (title/description) or
	// label Add/Remove.
	//
	// This keeps the possibly-frequent updates to the mergeSha and commit message fields from causing
	// a bunch of unnecessary API calls.
	//
	// The intentional tradeoff is that, for this kind of update, we will _not_ check the SCM for drift,
	// i.e. an external process merging or closing the PR.
	//
	// In practice, this will only be problematic for the "externally closed" case, which will sit in
	// a drifted state until a different kind of reconcile bypasses this short circuit. If the PR was
	// externally merged, a webhook will cause a CTP reconcile, and the CTP will explicitly enqueue the
	// PR so that it will FindOpen and close/delete itself.
	//
	// TODO: in the future we could consider breaking the PullRequest resource up so that fields like
	// mergeSha and the commit message get their own lifecycle, reserving the PullRequest reconcile
	// loop for activity that actually requires SCM calls. We'd have to do some internal cache work
	// to ensure we don't call Merge with stale sha/message data.
	if shouldSkipSCMSync(&pr) {
		logger.V(1).Info("skipping SCM sync for non-SCM spec change on open pull request")
		return r.pullRequestRequeueResult(ctx)
	}

	provider, err := r.getPullRequestProvider(ctx, pr)
	if err != nil {
		return ctrl.Result{}, err
	}

	openResult, err := provider.FindOpen(ctx, pr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check for open PR: %w", err)
	}

	if !openResult.Found {
		if pr.Status.ID == "" {
			// Hasn't been created yet. Create it.
			if err := r.createPullRequest(ctx, &pr, provider, previousReady); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to create pull request: %w", err)
			}
			return r.pullRequestRequeueResult(ctx)
		}

		// Not open on the SCM but status.id is set; the terminating Get lane will resolve merged vs closed and recover mergedTargetSha.
		r.Recorder.Eventf(&pr, nil, "Warning", constants.PullRequestExternallyMergedOrClosedReason, "SyncingPullRequestState", constants.PullRequestExternallyMergedOrClosedMessage, pr.Name, pr.Status.ID)
		pr.Status.State = promoterv1alpha1.PullRequestMergedOrClosed
		// Status is now Terminal; persist and the ->Terminal predicate will requeue to delete the CR, which puts us in the Terminating lane.
		return r.pullRequestRequeueResult(ctx)
	}

	pr.Status.ID = openResult.ID
	pr.Status.State = promoterv1alpha1.PullRequestOpen
	pr.Status.PRCreationTime = metav1.NewTime(openResult.CreationTime)
	if openResult.LabelsReported {
		// Record which managed labels the SCM currently shows; reconcileLabels compares spec to this.
		pr.Status.AppliedLabels = labels.ObservedManaged(pr.Spec.Labels, pr.Status.AppliedLabels, openResult.SCMLabels)
	}
	url, err := provider.GetUrl(ctx, pr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get pull request URL: %w", err)
	}
	pr.Status.Url = url

	if pr.Spec.State == promoterv1alpha1.PullRequestMerged {
		if err := r.mergePullRequest(ctx, &pr, provider, previousReady); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to merge pull request: %w", err)
		}
		// mergePullRequest set status.state to merged (Terminal); persist and the ->Terminal predicate will requeue to delete the CR, which puts us in the Terminating lane.
		return r.pullRequestRequeueResult(ctx)
	}

	if !pullRequestSCMRelevantSpecSynced(&pr) {
		// pullRequestSCMRelevantSpecSynced tracks whether our spec was pushed, not whether the SCM
		// still reflects it; out-of-band title/description edits while pr.Spec is unchanged are accepted.
		if err := r.updatePullRequest(ctx, &pr, provider); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update pull request: %w", err)
		}
	}

	if !labels.SetsEqual(pr.Spec.Labels, pr.Status.AppliedLabels) {
		if err := r.reconcileLabels(ctx, &pr, provider); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.pullRequestRequeueResult(ctx)
}

// pullRequestIsTerminating reports whether the PullRequest resource is being deleted.
// It's a trivial helper, but having "Terminating" as a reusable term is easier than "deleting" (ambiguous)
// or "deletion timestamp non-zero," which is cumbersome.
func pullRequestIsTerminating(pr *promoterv1alpha1.PullRequest) bool {
	return !pr.DeletionTimestamp.IsZero()
}

// pullRequestStatusIsTerminal reports whether the live PullRequest should initiate deletion.
func pullRequestStatusIsTerminal(pr *promoterv1alpha1.PullRequest) bool {
	if pr.Spec.State == promoterv1alpha1.PullRequestClosed {
		return true
	}
	switch pr.Status.State {
	case promoterv1alpha1.PullRequestMerged, promoterv1alpha1.PullRequestClosed, promoterv1alpha1.PullRequestMergedOrClosed:
		return true
	default:
		return false
	}
}

// pullRequestStatusIsFinalized reports whether a terminating PullRequest has recorded enough SCM
// outcome for the promoter finalizer to be released. Only meaningful when terminating.
//
// For merged pull requests, mergedTargetSha must be non-empty before release: async SCM providers
// may omit it from the merge response and the terminating Get lane must populate it so the owning
// ChangeTransferPolicy can write the promotion history note.
func pullRequestStatusIsFinalized(pr *promoterv1alpha1.PullRequest) bool {
	if pr.Status.ID == "" {
		return true
	}
	switch pr.Status.State {
	case promoterv1alpha1.PullRequestClosed, promoterv1alpha1.PullRequestUnknown:
		return true
	case promoterv1alpha1.PullRequestMerged:
		return pr.Status.MergedTargetSha != ""
	default:
		return false
	}
}

// pullRequestStatusTransitionPredicate enqueues when status transitions require another reconcile
// without a generation bump.
func pullRequestStatusTransitionPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectOld == nil || e.ObjectNew == nil {
				return false
			}
			oldPR, okOld := e.ObjectOld.(*promoterv1alpha1.PullRequest)
			newPR, okNew := e.ObjectNew.(*promoterv1alpha1.PullRequest)
			if !okOld || !okNew {
				return false
			}
			if !pullRequestIsTerminating(oldPR) && pullRequestIsTerminating(newPR) {
				return true
			}
			if !pullRequestStatusIsTerminal(oldPR) && pullRequestStatusIsTerminal(newPR) {
				return true
			}
			if pullRequestIsTerminating(newPR) &&
				!pullRequestStatusIsFinalized(oldPR) && pullRequestStatusIsFinalized(newPR) {
				return true
			}
			return false
		},
	}
}

// pullRequestImmediatelySyncedSpecDigest fingerprints title and description, the fields
// pushed to the SCM via provider.Update while the pull request is open.
func pullRequestImmediatelySyncedSpecDigest(pr *promoterv1alpha1.PullRequest) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%s", pr.Spec.Title, pr.Spec.Description))
	return hex.EncodeToString(sum[:])
}

// pullRequestSCMRelevantSpecSynced reports whether the SCM already has the current title/description.
// SCMSyncedSpecDigest is set only after a successful Create/Update, so a failed push leaves a stale
// digest and the next reconcile still retries.
func pullRequestSCMRelevantSpecSynced(pr *promoterv1alpha1.PullRequest) bool {
	return pr.Status.SCMSyncedSpecDigest != "" &&
		pr.Status.SCMSyncedSpecDigest == pullRequestImmediatelySyncedSpecDigest(pr)
}

// shouldSkipSCMSync reports whether reconcile can refresh status without contacting the SCM.
// Reconcile must call this only on non-terminating pull requests.
// CTP trailer and mergeSha updates bump metadata.generation while title/description and labels
// stay the same. If either the title/description digest or labels need syncing, fall through to
// FindOpen and the normal SCM write path.
func shouldSkipSCMSync(pr *promoterv1alpha1.PullRequest) bool {
	if pr.Spec.State == promoterv1alpha1.PullRequestMerged {
		return false
	}
	if pr.Spec.State != promoterv1alpha1.PullRequestOpen {
		return false
	}
	if pr.Status.ID == "" {
		return false
	}
	if pr.Generation <= pr.Status.ObservedGeneration {
		return false
	}
	if !pullRequestSCMRelevantSpecSynced(pr) {
		return false
	}
	if !labels.SetsEqual(pr.Spec.Labels, pr.Status.AppliedLabels) {
		return false
	}
	return true
}

func (r *PullRequestReconciler) pullRequestRequeueResult(ctx context.Context) (ctrl.Result, error) {
	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.PullRequestConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get pull request requeue duration: %w", err)
	}
	return ctrl.Result{RequeueAfter: requeueDuration}, nil
}

// pullRequestDeletionFinalizerLengthChangedPredicate matches Update events where the object is
// terminating (deletionTimestamp set) and the finalizer count changed. This is important because
// the CTP controller sets a finalizer, and we need to reconcile when it's removed to ensure
// quick cleanup of the PR.
func pullRequestDeletionFinalizerLengthChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectOld == nil || e.ObjectNew == nil {
				return false
			}
			if e.ObjectNew.GetDeletionTimestamp().IsZero() {
				return false
			}
			return len(e.ObjectOld.GetFinalizers()) != len(e.ObjectNew.GetFinalizers())
		},
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *PullRequestReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.PullRequestConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get pull request rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.PullRequestConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get pull request max concurrent reconciles: %w", err)
	}

	externalEnqueueChan := make(chan event.GenericEvent, 1024)
	r.enqueueFunc = func(namespace, name string) {
		pr := &promoterv1alpha1.PullRequest{}
		pr.SetNamespace(namespace)
		pr.SetName(name)

		select {
		case externalEnqueueChan <- event.GenericEvent{Object: pr}:
		default:
			log.FromContext(ctx).Info("PullRequest enqueue channel is full, blocking until space is available",
				"namespace", namespace, "name", name)
			externalEnqueueChan <- event.GenericEvent{Object: pr}
		}
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.PullRequest{}, builder.WithPredicates(predicate.Or(
			predicate.GenerationChangedPredicate{},
			pullRequestDeletionFinalizerLengthChangedPredicate(),
			pullRequestStatusTransitionPredicate(),
		))).
		WatchesRawSource(source.Channel(externalEnqueueChan, &handler.EnqueueRequestForObject{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

func (r *PullRequestReconciler) getPullRequestProvider(ctx context.Context, pr promoterv1alpha1.PullRequest) (scms.PullRequestProvider, error) {
	scmProvider, secret, gitRepository, err := utils.GetScmProviderSecretAndGitRepositoryFromRepositoryReference(
		ctx, r.Client, r.SettingsMgr.GetControllerNamespace(), pr.Spec.RepositoryReference, &pr,
	)
	if err != nil {
		if !pr.DeletionTimestamp.IsZero() && k8serrors.IsNotFound(err) {
			return nil, pullRequestDeletionBlockedByMissingDependency(err)
		}
		return nil, fmt.Errorf("failed to get PullRequest provider: %w", err)
	}

	switch {
	case scmProvider.GetSpec().GitHub != nil:
		return github.NewGithubPullRequestProvider(ctx, r.Client, scmProvider, *secret, gitRepository.Spec.GitHub.Owner) //nolint:wrapcheck // provider factory returns descriptive errors
	case scmProvider.GetSpec().GitLab != nil:
		return gitlab.NewGitlabPullRequestProvider(r.Client, *secret, scmProvider.GetSpec().GitLab.Domain) //nolint:wrapcheck // provider factory returns descriptive errors
	case scmProvider.GetSpec().BitbucketCloud != nil:
		return bitbucket_cloud.NewBitbucketCloudPullRequestProvider(r.Client, *secret) //nolint:wrapcheck // provider factory returns descriptive errors
	case scmProvider.GetSpec().BitbucketDataCenter != nil:
		return bitbucket_datacenter.NewBitbucketDataCenterPullRequestProvider(r.Client, scmProvider, *secret) //nolint:wrapcheck // provider factory returns descriptive errors
	case scmProvider.GetSpec().Forgejo != nil:
		return forgejo.NewForgejoPullRequestProvider(r.Client, *secret, scmProvider.GetSpec().Forgejo.Domain) //nolint:wrapcheck // provider factory returns descriptive errors
	case scmProvider.GetSpec().Gitea != nil:
		return gitea.NewGiteaPullRequestProvider(r.Client, *secret, scmProvider.GetSpec().Gitea.Domain) //nolint:wrapcheck // provider factory returns descriptive errors
	case scmProvider.GetSpec().AzureDevOps != nil:
		return azuredevops.NewAzdoPullRequestProvider(r.Client, *secret, scmProvider, scmProvider.GetSpec().AzureDevOps.Organization) //nolint:wrapcheck,contextcheck // provider factory returns descriptive errors
	case scmProvider.GetSpec().Fake != nil:
		return fake.NewFakePullRequestProvider(r.Client), nil
	default:
		return nil, fmt.Errorf("unsupported SCM provider: %s", scmProvider.GetName())
	}
}

// ensureFinalizer adds the PullRequest finalizer to a PullRequest that is not being deleted, so that
// every deletion is funneled through the terminating Get/Close lane above.
func (r *PullRequestReconciler) ensureFinalizer(ctx context.Context, pr *promoterv1alpha1.PullRequest) error {
	finalizer := promoterv1alpha1.PullRequestFinalizer

	if controllerutil.ContainsFinalizer(pr, finalizer) {
		return nil
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error { //nolint:wrapcheck // RetryOnConflict returns wrapped error
		if err := r.Get(ctx, client.ObjectKeyFromObject(pr), pr); err != nil {
			return err //nolint:wrapcheck // error will be wrapped by caller
		}
		if controllerutil.AddFinalizer(pr, finalizer) {
			return r.Update(ctx, pr) //nolint:wrapcheck // RetryOnConflict returns wrapped error
		}
		return nil
	})
}

// releaseFinalizer removes the PullRequest finalizer, allowing the resource to be removed once no
// other finalizer retains it.
func (r *PullRequestReconciler) releaseFinalizer(ctx context.Context, pr *promoterv1alpha1.PullRequest) error {
	if !controllerutil.RemoveFinalizer(pr, promoterv1alpha1.PullRequestFinalizer) {
		return nil
	}
	if err := r.Update(ctx, pr); err != nil {
		return fmt.Errorf("failed to remove finalizer: %w", err)
	}
	return nil
}

func pullRequestWasHealthy(previousReady *metav1.Condition) bool {
	return previousReady == nil || previousReady.Status == metav1.ConditionTrue
}

// createPullRequest creates the SCM pull request. previousReady gates health-responsive failure
// events so backoff retries do not spam Warning events after the first failure.
func (r *PullRequestReconciler) createPullRequest(ctx context.Context, pr *promoterv1alpha1.PullRequest, provider scms.PullRequestProvider, previousReady *metav1.Condition) error {
	log.FromContext(ctx).Info("Creating PullRequest")
	id, err := provider.Create(ctx, pr.Spec.Title, pr.Spec.SourceBranch, pr.Spec.TargetBranch, pr.Spec.Description, *pr)
	if err != nil {
		if pullRequestWasHealthy(previousReady) {
			r.Recorder.Eventf(pr, nil, "Warning", constants.PullRequestCreateFailedReason, "CreatingPullRequest", constants.PullRequestCreateFailedMessage, pr.Name, err)
		}
		return err //nolint:wrapcheck // Error wrapping handled at top level
	}
	pr.Status.State = promoterv1alpha1.PullRequestOpen
	pr.Status.PRCreationTime = metav1.Now()
	pr.Status.ID = id

	url, err := provider.GetUrl(ctx, *pr)
	if err != nil {
		return fmt.Errorf("failed to get pull request URL: %w", err)
	}
	pr.Status.Url = url
	pr.Status.SCMSyncedSpecDigest = pullRequestImmediatelySyncedSpecDigest(pr)

	return nil
}

func (r *PullRequestReconciler) updatePullRequest(ctx context.Context, pr *promoterv1alpha1.PullRequest, provider scms.PullRequestProvider) error {
	log.FromContext(ctx).Info("Updating PullRequest")
	if err := provider.Update(ctx, pr.Spec.Title, pr.Spec.Description, *pr); err != nil {
		return err //nolint:wrapcheck // Error wrapping handled at top level
	}
	pr.Status.SCMSyncedSpecDigest = pullRequestImmediatelySyncedSpecDigest(pr)
	r.Recorder.Eventf(pr, nil, "Normal", constants.PullRequestUpdatedReason, "UpdatingPullRequest", "Pull Request %s updated", pr.Name)
	return nil
}

// mergePullRequest merges the SCM pull request. previousReady gates health-responsive failure
// events so backoff retries do not spam Warning events after the first failure.
func (r *PullRequestReconciler) mergePullRequest(ctx context.Context, pr *promoterv1alpha1.PullRequest, provider scms.PullRequestProvider, previousReady *metav1.Condition) error {
	log.FromContext(ctx).Info("Merging PullRequest")
	mergedTime := metav1.Now()

	updatedMessage, err := git.AddTrailerToCommitMessage(
		ctx,
		pr.Spec.Commit.Message,
		constants.TrailerPullRequestMergeTime,
		mergedTime.Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("failed to add trailer to commit message: %w", err)
	}

	// Update the commit message with the new trailers
	pr.Spec.Commit.Message = updatedMessage

	result, err := provider.Merge(ctx, *pr)
	if err != nil {
		if pullRequestWasHealthy(previousReady) {
			r.Recorder.Eventf(pr, nil, "Warning", constants.PullRequestMergeFailedReason, "MergingPullRequest", constants.PullRequestMergeFailedMessage, pr.Name, err)
		}
		return err //nolint:wrapcheck // Error wrapping handled at top level
	}
	pr.Status.State = promoterv1alpha1.PullRequestMerged
	// Providers that report the resulting target-branch commit in the merge response let us record it
	// now; the rest leave it empty and the terminating Get lane recovers it after deletion.
	pr.Status.MergedTargetSha = result.CommitSHA
	return nil
}

// closePullRequest closes the SCM pull request and records status.state=closed.
// The caller must invoke this only when Get reports the pull request is open during termination.
func (r *PullRequestReconciler) closePullRequest(ctx context.Context, pr *promoterv1alpha1.PullRequest, provider scms.PullRequestProvider) error {
	log.FromContext(ctx).Info("Closing PullRequest")
	if err := provider.Close(ctx, *pr); err != nil {
		return err //nolint:wrapcheck // Error wrapping handled at top level
	}
	pr.Status.State = promoterv1alpha1.PullRequestClosed
	r.Recorder.Eventf(pr, nil, "Normal", constants.PullRequestClosedReason, "ClosingPullRequest", constants.PullRequestClosedMessage, pr.Name)
	return nil
}

type trailers map[string]string

func (t trailers) String() string {
	keys := make([]string, 0, len(t))

	for k := range t {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var result strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&result, "%s: %s\n", k, t[k])
	}
	return result.String()
}

// pullRequestDeletionBlockedByMissingDependency builds an operator-facing error when a terminating
// PullRequest cannot resolve its SCM provider because a dependency is missing.
func pullRequestDeletionBlockedByMissingDependency(err error) error {
	var details *metav1.StatusDetails
	if statusErr, ok := errors.AsType[*k8serrors.StatusError](err); ok {
		d := statusErr.Status().Details
		if d != nil && d.Kind != "" && d.Name != "" {
			details = d
		}
	}

	if details == nil {
		return fmt.Errorf(
			"this PullRequest cannot close its SCM pull request because a required dependency was not found - "+
				"either restore the missing resource or remove the %s finalizer from this PullRequest and manually close the SCM pull request if it is not already closed",
			promoterv1alpha1.PullRequestFinalizer,
		)
	}

	typeRef := details.Kind
	if details.Group != "" {
		typeRef = details.Group + "/" + details.Kind
	}
	return fmt.Errorf(
		"this PullRequest cannot close its SCM pull request because %s %q is missing - "+
			"either restore that resource or remove the %s finalizer from this PullRequest and manually close the SCM pull request if it is not already closed",
		typeRef,
		details.Name,
		promoterv1alpha1.PullRequestFinalizer,
	)
}

// reconcileLabels syncs spec.labels to the SCM provider and updates status.appliedLabels.
// The caller must ensure status.id is set and spec.labels differs from status.appliedLabels.
// TODO: add a validating admission webhook to reject spec.labels when the repository's SCM
// provider does not support pull request labels (e.g. Bitbucket Cloud), so misconfiguration
// is caught at apply time instead of surfacing as a reconcile error loop.
func (r *PullRequestReconciler) reconcileLabels(ctx context.Context, pr *promoterv1alpha1.PullRequest, provider scms.PullRequestProvider) error {
	log.FromContext(ctx).Info("Reconciling PullRequest labels")
	toAdd, toRemove := labels.Diff(pr.Spec.Labels, pr.Status.AppliedLabels)
	if len(toRemove) > 0 {
		if err := provider.RemoveLabels(ctx, *pr, toRemove); err != nil {
			return fmt.Errorf("failed to remove pull request labels: %w", err)
		}
	}
	if len(toAdd) > 0 {
		if err := provider.AddLabels(ctx, *pr, toAdd); err != nil {
			return fmt.Errorf("failed to add pull request labels: %w", err)
		}
	}

	pr.Status.AppliedLabels = slices.Clone(pr.Spec.Labels)
	return nil
}
