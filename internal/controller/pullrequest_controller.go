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
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	bitbucket_cloud "github.com/argoproj-labs/gitops-promoter/internal/scms/bitbucket_cloud"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/forgejo"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitea"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitlab"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// PullRequestReconciler reconciles a PullRequest object
type PullRequestReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=pullrequests/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *PullRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling PullRequest")
	startTime := time.Now()

	var pr promoterv1alpha1.PullRequest
	// This function will update the resource status at the end of the reconciliation. don't call .Status().Update manually.
	defer utils.HandleReconciliationResult(ctx, startTime, &pr, r.Client, r.Recorder, &err)

	if err := r.Get(ctx, req.NamespacedName, &pr); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("PullRequest not found", "namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PullRequest: %w", err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	meta.RemoveStatusCondition(pr.GetConditions(), string(promoterConditions.Ready))

	// Handle deletion early - if being deleted and status.ID is empty, we can skip provider setup
	if handled, err := r.handleEmptyIDDeletion(ctx, &pr); handled || err != nil {
		return ctrl.Result{}, err
	}

	provider, err := r.getPullRequestProvider(ctx, pr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get PullRequest provider: %w", err)
	}

	found, prID, prCreationTime, err := provider.FindOpen(ctx, pr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to check for open PR: %w", err)
	}

	if deleted, err := r.handleFinalizer(ctx, &pr, provider, found); err != nil || deleted {
		return ctrl.Result{}, err
	}

	// Clean up already closed/merged PRs
	if cleaned, err := r.cleanupTerminalStates(ctx, &pr); cleaned || err != nil {
		return ctrl.Result{}, err
	}

	// Sync state from provider
	externallyMergedOrClosed, err := r.syncStateFromProvider(ctx, &pr, provider, found, prID, prCreationTime)
	if err != nil {
		return ctrl.Result{}, err
	}
	// If ExternallyMergedOrClosed was set, requeue immediately to trigger cleanup on the next reconciliation.
	// The flow is:
	// 1. syncStateFromProvider updates pr.Status.ExternallyMergedOrClosed to true in memory
	// 2. We return here with RequeueAfter
	// 3. The deferred HandleReconciliationResult persists the status update to the cluster
	// 4. The next reconciliation sees the persisted ExternallyMergedOrClosed flag
	// 5. cleanupTerminalStates (which runs earlier in the loop) handles deletion
	if externallyMergedOrClosed {
		return ctrl.Result{RequeueAfter: 1 * time.Microsecond}, nil
	}

	// Handle state transitions
	cleanupRequired, err := r.handleStateTransitions(ctx, &pr, provider)
	if err != nil {
		return ctrl.Result{}, err
	}
	// If a state transition was performed (merge or close), requeue immediately to trigger
	// cleanup on the next reconciliation. The flow is:
	// 1. handleStateTransitions updates pr.Status.State to Merged/Closed in memory
	// 2. We return here with RequeueAfter
	// 3. The deferred HandleReconciliationResult persists the status update to the cluster
	// 4. The next reconciliation sees the persisted Merged/Closed state
	// 5. cleanupTerminalStates (which runs earlier in the loop) handles deletion
	// Previously, merge/close would delete inline, but this was problematic because the status
	// update would be lost. Now we ensure the status is persisted before deletion occurs.
	if cleanupRequired {
		return ctrl.Result{RequeueAfter: 1 * time.Microsecond}, err
	}

	logger.Info("no known state transitions needed", "specState", pr.Spec.State, "statusState", pr.Status.State)

	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.PullRequestConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get pull request requeue duration: %w", err)
	}

	return ctrl.Result{RequeueAfter: requeueDuration}, nil
}

// handleEmptyIDDeletion handles the case where a PullRequest is being deleted but never created a PR on the SCM.
// Returns (handled=true, nil) if deletion was handled, (false, nil) if not applicable, or (false, err) on error.
func (r *PullRequestReconciler) handleEmptyIDDeletion(ctx context.Context, pr *promoterv1alpha1.PullRequest) (bool, error) {
	if pr.DeletionTimestamp.IsZero() || pr.Status.ID != "" {
		return false, nil
	}

	if controllerutil.ContainsFinalizer(pr, promoterv1alpha1.PullRequestFinalizer) {
		controllerutil.RemoveFinalizer(pr, promoterv1alpha1.PullRequestFinalizer)
		if err := r.Update(ctx, pr); err != nil {
			return true, fmt.Errorf("failed to remove finalizer: %w", err)
		}
	}
	return true, nil
}

// cleanupTerminalStates deletes PullRequests that have reached terminal states (merged/closed) or were externally merged/closed.
// Returns (cleaned=true, nil) if cleaned up, (false, nil) if not applicable, or (false, err) on error.
func (r *PullRequestReconciler) cleanupTerminalStates(ctx context.Context, pr *promoterv1alpha1.PullRequest) (bool, error) {
	logger := log.FromContext(ctx)

	// Check if PR should be cleaned up: either externally merged/closed or in terminal state (merged/closed)
	// When ExternallyMergedOrClosed is true, State may be empty (we don't know if merged or closed),
	// Open (set before we detected the external action), or a terminal state.
	externallyMergedOrClosed := pr.Status.ExternallyMergedOrClosed != nil && *pr.Status.ExternallyMergedOrClosed
	isTerminalState := pr.Status.State == promoterv1alpha1.PullRequestMerged || pr.Status.State == promoterv1alpha1.PullRequestClosed

	if !externallyMergedOrClosed && !isTerminalState {
		return false, nil
	}

	if externallyMergedOrClosed {
		logger.Info("Cleaning up externally merged or closed pull request", "pullRequestID", pr.Status.ID)
	} else {
		logger.Info("Cleaning up closed and merged pull request", "pullRequestID", pr.Status.ID)
	}
	if err := r.Delete(ctx, pr); err != nil && !errors.IsNotFound(err) {
		logger.Error(err, "Failed to delete PullRequest")
		return false, fmt.Errorf("failed to delete PullRequest: %w", err)
	}
	return true, nil
}

// syncStateFromProvider syncs the PullRequest state from the SCM provider.
// Returns (externallyMergedOrClosed=true, nil) if ExternallyMergedOrClosed was set (requeue needed), (false, nil) if successful, or (false, err) on error.
func (r *PullRequestReconciler) syncStateFromProvider(ctx context.Context, pr *promoterv1alpha1.PullRequest, provider scms.PullRequestProvider, found bool, prID string, prCreationTime time.Time) (bool, error) {
	logger := log.FromContext(ctx)

	logger.Info("Checking for open PR on provider")

	// Calculate the state of the PR based on the provider, if found we have to be open
	if found {
		pr.Status.State = promoterv1alpha1.PullRequestOpen
		pr.Status.ID = prID
		pr.Status.PRCreationTime = metav1.NewTime(prCreationTime)
		url, err := provider.GetUrl(ctx, *pr)
		if err != nil {
			return false, fmt.Errorf("failed to get pull request URL: %w", err)
		}
		pr.Status.Url = url
		return false, nil
	}

	// If we don't find the PR, but we have an ID, check if it was merged/closed externally or by the controller.
	// If spec.state is "merged" or "closed", the controller initiated the action and we should NOT mark as external.
	// Only mark as external if spec.state is "open" (controller didn't initiate the closure/merge).
	if pr.Status.ID != "" {
		if pr.Spec.State == promoterv1alpha1.PullRequestOpen {
			// Controller still thinks PR should be open, but it's not found on provider = external action
			pr.Status.ExternallyMergedOrClosed = ptr.To(true)
			// Don't set State since we don't know if it was merged or closed externally.
			// The ExternallyMergedOrClosed flag is the source of truth that this PR
			// is no longer active and was handled outside of the controller's control.
			// An empty State with ExternallyMergedOrClosed=true means "closed/merged externally, but we don't know which".
			pr.Status.State = ""
			return true, nil
		}
		// If spec.state is "merged" or "closed", the controller is in the process of merging/closing.
		// The PR may not be found as "open" because it's already transitioned on the provider.
		// This is normal - let handleStateTransitions continue to process the spec.state.
		logger.V(4).Info("PR not found open, but controller initiated the action", "specState", pr.Spec.State)
	}

	return false, nil
}

// handleStateTransitions handles transitions between PullRequest states.
// Returns (done=true, nil) if a terminal state was reached, (false, nil) otherwise, or (false, err) on error.
func (r *PullRequestReconciler) handleStateTransitions(ctx context.Context, pr *promoterv1alpha1.PullRequest, provider scms.PullRequestProvider) (bool, error) {
	logger := log.FromContext(ctx)

	logger.Info("Reconciling PullRequest state", "desired", pr.Spec.State, "current", pr.Status.State)

	if pr.Status.State == pr.Spec.State {
		logger.Info("Updating PullRequest")
		if err := r.updatePullRequest(ctx, *pr, provider); err != nil {
			return false, fmt.Errorf("failed to update pull request: %w", err) // Top-level wrap for update errors
		}
		return false, nil
	}

	switch pr.Spec.State {
	case promoterv1alpha1.PullRequestOpen:
		if pr.Status.ID == "" {
			// Because status id is empty, we need to create a new pull request
			logger.Info("Creating PullRequest")
			if err := r.createPullRequest(ctx, pr, provider); err != nil {
				return false, fmt.Errorf("failed to create pull request: %w", err) // Top-level wrap for create errors
			}
		}
	case promoterv1alpha1.PullRequestMerged:
		logger.Info("Merging PullRequest")
		if err := r.mergePullRequest(ctx, pr, provider); err != nil {
			return false, fmt.Errorf("failed to merge pull request: %w", err) // Top-level wrap for merge errors
		}
		return true, nil
	case promoterv1alpha1.PullRequestClosed:
		logger.Info("Closing PullRequest")
		if err := r.closePullRequest(ctx, pr, provider); err != nil {
			return false, fmt.Errorf("failed to close pull request: %w", err) // Top-level wrap for close errors
		}
		return true, nil
	default:
		return false, fmt.Errorf("unknown PullRequest state %q: this should not happen, please report a bug", pr.Spec.State)
	}

	return false, nil
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

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.PullRequest{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

func (r *PullRequestReconciler) getPullRequestProvider(ctx context.Context, pr promoterv1alpha1.PullRequest) (scms.PullRequestProvider, error) {
	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, r.SettingsMgr.GetControllerNamespace(), pr.Spec.RepositoryReference, &pr)
	if err != nil {
		return nil, fmt.Errorf("failed to get ScmProvider and secret: %w", err)
	}

	gitRepository, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{Namespace: pr.Namespace, Name: pr.Spec.RepositoryReference.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	switch {
	case scmProvider.GetSpec().GitHub != nil:
		return github.NewGithubPullRequestProvider(ctx, r.Client, scmProvider, *secret, gitRepository.Spec.GitHub.Owner) //nolint:wrapcheck
	case scmProvider.GetSpec().GitLab != nil:
		return gitlab.NewGitlabPullRequestProvider(r.Client, *secret, scmProvider.GetSpec().GitLab.Domain) //nolint:wrapcheck
	case scmProvider.GetSpec().BitbucketCloud != nil:
		return bitbucket_cloud.NewBitbucketCloudPullRequestProvider(r.Client, *secret) //nolint:wrapcheck
	case scmProvider.GetSpec().Forgejo != nil:
		return forgejo.NewForgejoPullRequestProvider(r.Client, *secret, scmProvider.GetSpec().Forgejo.Domain) //nolint:wrapcheck
	case scmProvider.GetSpec().Gitea != nil:
		return gitea.NewGiteaPullRequestProvider(r.Client, *secret, scmProvider.GetSpec().Gitea.Domain) //nolint:wrapcheck
	case scmProvider.GetSpec().AzureDevOps != nil:
		return azuredevops.NewAzdoPullRequestProvider(r.Client, *secret, scmProvider, scmProvider.GetSpec().AzureDevOps.Organization) //nolint:wrapcheck,contextcheck
	case scmProvider.GetSpec().Fake != nil:
		return fake.NewFakePullRequestProvider(r.Client), nil
	default:
		return nil, fmt.Errorf("unsupported SCM provider: %s", scmProvider.GetName())
	}
}

func (r *PullRequestReconciler) handleFinalizer(ctx context.Context, pr *promoterv1alpha1.PullRequest, provider scms.PullRequestProvider, found bool) (bool, error) {
	finalizer := promoterv1alpha1.PullRequestFinalizer

	if pr.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(pr, finalizer) {
			// Not being deleted and already has finalizer, nothing to do.
			return false, nil
		}

		// Finalizer is missing, add it.
		return false, retry.RetryOnConflict(retry.DefaultRetry, func() error { //nolint:wrapcheck
			if err := r.Get(ctx, client.ObjectKeyFromObject(pr), pr); err != nil {
				return err //nolint:wrapcheck
			}
			if controllerutil.AddFinalizer(pr, finalizer) {
				return r.Update(ctx, pr)
			}
			return nil
		})
	}

	// If we're here, the object is being deleted
	if !controllerutil.ContainsFinalizer(pr, finalizer) {
		// Finalizer already removed, nothing to do.
		return false, nil
	}

	// If status.ID is empty, it means the PullRequest never took control of any PR on the SCM.
	// In this case, we can just remove the finalizer without attempting to close the PR.
	if pr.Status.ID != "" && found {
		if err := r.closePullRequest(ctx, pr, provider); err != nil {
			return false, fmt.Errorf("failed to close pull request: %w", err) // Top-level wrap for close errors
		}
	}

	controllerutil.RemoveFinalizer(pr, finalizer)
	if err := r.Update(ctx, pr); err != nil {
		return true, fmt.Errorf("failed to remove finalizer: %w", err)
	}
	return true, nil
}

func (r *PullRequestReconciler) createPullRequest(ctx context.Context, pr *promoterv1alpha1.PullRequest, provider scms.PullRequestProvider) error {
	id, err := provider.Create(ctx, pr.Spec.Title, pr.Spec.SourceBranch, pr.Spec.TargetBranch, pr.Spec.Description, *pr)
	if err != nil {
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

	return nil
}

func (r *PullRequestReconciler) updatePullRequest(ctx context.Context, pr promoterv1alpha1.PullRequest, provider scms.PullRequestProvider) error {
	if err := provider.Update(ctx, pr.Spec.Title, pr.Spec.Description, pr); err != nil {
		return err //nolint:wrapcheck // Error wrapping handled at top level
	}
	r.Recorder.Eventf(&pr, nil, "Normal", constants.PullRequestUpdatedReason, "UpdatingPullRequest", "Pull Request %s updated", pr.Name)
	return nil
}

func (r *PullRequestReconciler) mergePullRequest(ctx context.Context, pr *promoterv1alpha1.PullRequest, provider scms.PullRequestProvider) error {
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

	if err := provider.Merge(ctx, *pr); err != nil {
		return err //nolint:wrapcheck // Error wrapping handled at top level
	}
	pr.Status.State = promoterv1alpha1.PullRequestMerged
	return nil
}

type trailers map[string]string

func (t trailers) String() string {
	keys := make([]string, 0, len(t))

	for k := range t {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var result strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&result, "%s: %s\n", k, t[k])
	}
	return result.String()
}

func (r *PullRequestReconciler) closePullRequest(ctx context.Context, pr *promoterv1alpha1.PullRequest, provider scms.PullRequestProvider) error {
	if pr.Status.State == promoterv1alpha1.PullRequestMerged {
		return nil
	}
	if err := provider.Close(ctx, *pr); err != nil {
		return err //nolint:wrapcheck // Error wrapping handled at top level
	}
	pr.Status.State = promoterv1alpha1.PullRequestClosed
	return nil
}
