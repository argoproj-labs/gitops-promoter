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
	"time"

	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitlab"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	v1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

type ChangeTransferPolicyReconcilerConfig struct {
	RequeueDuration time.Duration
}

// ChangeTransferPolicyReconciler reconciles a ChangeTransferPolicy object
type ChangeTransferPolicyReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	SettingsMgr *settings.Manager
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicies,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicies/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicies/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ChangeTransferPolicy object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *ChangeTransferPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ChangeTransferPolicy")
	startTime := time.Now()

	var ctp promoterv1alpha1.ChangeTransferPolicy
	err := r.Get(ctx, req.NamespacedName, &ctp, &client.GetOptions{})
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			logger.Info("ChangeTransferPolicy not found")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "failed to get ChangeTransferPolicy")
		return ctrl.Result{}, fmt.Errorf("failed to get ChangeTransferPolicy: %w", err)
	}

	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, ctp.Spec.RepositoryReference, &ctp)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get ScmProvider and secret for repo %q: %w", ctp.Spec.RepositoryReference.Name, err)
	}

	gitAuthProvider, err := r.getGitAuthProvider(ctx, scmProvider, secret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get git auth provider for ScmProvider %q: %w", scmProvider.Name, err)
	}
	gitOperations, err := git.NewGitOperations(ctx, r.Client, gitAuthProvider, ctp.Spec.RepositoryReference, &ctp, ctp.Spec.ActiveBranch)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to initialize git client: %w", err)
	}

	// TODO: could probably short circuit the clone and use an ls-remote to compare the sha's of the current ctp status,
	// this would help with slamming the git provider with clone requests on controller restarts.

	err = gitOperations.CloneRepo(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to clone repo %q: %w", ctp.Spec.RepositoryReference.Name, err)
	}

	err = r.calculateStatus(ctx, &ctp, gitOperations)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to calculate ChangeTransferPolicy status: %w", err)
	}

	err = r.mergeOrPullRequestPromote(ctx, gitOperations, &ctp)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set promotion state: %w", err)
	}

	err = r.mergePullRequests(ctx, &ctp)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to merge pull requests: %w", err)
	}

	err = r.Status().Update(ctx, &ctp)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update status: %w", err)
	}

	logger.Info("Reconciling ChangeTransferPolicy End", "duration", time.Since(startTime))

	requeueDuration, err := r.SettingsMgr.GetChangeTransferPolicyRequeueDuration(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get global promotion configuration: %w", err)
	}

	return ctrl.Result{
		Requeue:      true,
		RequeueAfter: requeueDuration,
	}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ChangeTransferPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// This index gets used by the CommitStatus controller and the webhook server to find the ChangeTransferPolicy to trigger reconcile
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &promoterv1alpha1.ChangeTransferPolicy{}, ".status.proposed.hydrated.sha", func(rawObj client.Object) []string {
		//nolint:forcetypeassert
		ctp := rawObj.(*promoterv1alpha1.ChangeTransferPolicy)
		return []string{ctp.Status.Proposed.Hydrated.Sha}
	}); err != nil {
		return fmt.Errorf("failed to set field index for .status.proposed.hydrated.sha: %w", err)
	}

	// This gets used by the CommitStatus controller to find the ChangeTransferPolicy to trigger reconcile
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &promoterv1alpha1.ChangeTransferPolicy{}, ".status.active.hydrated.sha", func(rawObj client.Object) []string {
		//nolint:forcetypeassert
		ctp := rawObj.(*promoterv1alpha1.ChangeTransferPolicy)
		return []string{ctp.Status.Active.Hydrated.Sha}
	}); err != nil {
		return fmt.Errorf("failed to set field index for .status.active.hydrated.sha: %w", err)
	}

	err := ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.ChangeTransferPolicy{}).
		Owns(&promoterv1alpha1.PullRequest{}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

func (r *ChangeTransferPolicyReconciler) getGitAuthProvider(ctx context.Context, scmProvider *promoterv1alpha1.ScmProvider, secret *v1.Secret) (scms.GitOperationsProvider, error) {
	logger := log.FromContext(ctx)
	switch {
	case scmProvider.Spec.Fake != nil:
		logger.V(4).Info("Creating fake git authentication provider")
		return fake.NewFakeGitAuthenticationProvider(scmProvider, secret), nil
	case scmProvider.Spec.GitHub != nil:
		logger.V(4).Info("Creating GitHub git authentication provider")
		return github.NewGithubGitAuthenticationProvider(scmProvider, secret), nil
	case scmProvider.Spec.GitLab != nil:
		logger.V(4).Info("Creating GitLab git authentication provider")
		provider, err := gitlab.NewGitlabGitAuthenticationProvider(scmProvider, secret)
		if err != nil {
			return nil, fmt.Errorf("failed to create GitLab Auth Provider: %w", err)
		}
		return provider, nil
	default:
		return nil, fmt.Errorf("no supported git authentication provider found")
	}
}

func (r *ChangeTransferPolicyReconciler) calculateStatus(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy, gitOperations *git.GitOperations) error {
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

	err = r.setCommitStatusState(ctx, &ctp.Status.Active, ctp.Spec.ActiveCommitStatuses, gitOperations, activeShas)
	if err != nil {
		var tooManyMatchingShaError *TooManyMatchingShaError
		if errors.As(err, &tooManyMatchingShaError) {
			r.Recorder.Event(ctp, "Warning", "TooManyMatchingSha", "There are to many matching SHAs for the active commit status")
		}
		return fmt.Errorf("failed to set active commit status state: %w", err)
	}

	err = r.setCommitStatusState(ctx, &ctp.Status.Proposed, ctp.Spec.ProposedCommitStatuses, gitOperations, proposedShas)
	if err != nil {
		var tooManyMatchingShaError *TooManyMatchingShaError
		if errors.As(err, &tooManyMatchingShaError) {
			r.Recorder.Event(ctp, "Warning", "TooManyMatchingSha", "There are to many matching SHAs for the proposed commit status")
		}
		return fmt.Errorf("failed to set proposed commit status state: %w", err)
	}

	return nil
}

type TooManyMatchingShaError struct{}

func (e *TooManyMatchingShaError) Error() string {
	return "there are to many matching SHAs for the commit status"
}

// setCommitStatusState sets the hydrated and dry SHAs and commit times for the target commit branch state and sets the
// commit statuses.
func (r *ChangeTransferPolicyReconciler) setCommitStatusState(ctx context.Context, targetCommitBranchState *promoterv1alpha1.CommitBranchState, commitStatuses []promoterv1alpha1.CommitStatusSelector, gitOperations *git.GitOperations, shas git.BranchShas) error {
	logger := log.FromContext(ctx)

	targetCommitBranchState.Hydrated.Sha = shas.Hydrated
	commitTime, err := gitOperations.GetShaTime(ctx, shas.Hydrated)
	if err != nil {
		return fmt.Errorf("failed to get commit time for hydrated SHA %q: %w", shas.Hydrated, err)
	}
	targetCommitBranchState.Hydrated.CommitTime = commitTime

	if shas.Dry != "" {
		targetCommitBranchState.Dry.Sha = shas.Dry
		commitTime, err = gitOperations.GetShaTime(ctx, shas.Dry)
		if err != nil {
			return fmt.Errorf("failed to get dry SHA %q on: %w", shas.Dry, err)
		}
		targetCommitBranchState.Dry.CommitTime = commitTime
	}

	commitStatusesState := []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{}
	var tooManyMatchingShas bool
	for _, status := range commitStatuses {
		var csList promoterv1alpha1.CommitStatusList
		// Find all the replicasets that match the commit status configured name and the sha of the hydrated commit
		err = r.List(ctx, &csList, &client.ListOptions{
			LabelSelector: labels.SelectorFromSet(map[string]string{
				promoterv1alpha1.CommitStatusLabel: utils.KubeSafeLabel(ctx, status.Key),
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
			})
			found = true
			phase = csList.Items[0].Status.Phase
		} else if len(csList.Items) > 1 {
			// TODO: decided how to bubble up errors
			commitStatusesState = append(commitStatusesState, promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
				Key:   status.Key,
				Phase: string(promoterv1alpha1.CommitPhasePending),
			})
			tooManyMatchingShas = true
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
			"toManyMatchingSha", tooManyMatchingShas,
			"foundCount", len(csList.Items))
	}
	targetCommitBranchState.CommitStatuses = commitStatusesState

	if tooManyMatchingShas {
		return &TooManyMatchingShaError{}
	}
	return nil
}

func (r *ChangeTransferPolicyReconciler) mergeOrPullRequestPromote(ctx context.Context, gitOperations *git.GitOperations, ctp *promoterv1alpha1.ChangeTransferPolicy) error {
	if ctp.Status.Proposed.Dry.Sha == ctp.Status.Active.Dry.Sha {
		// There's nothing to promote.
		return nil
	}

	prRequired, err := gitOperations.IsPullRequestRequired(ctx, ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch)
	if err != nil {
		return fmt.Errorf("failed to check whether a PR is required from branch %q to %q: %w", ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch, err)
	}

	if prRequired {
		err = r.creatOrUpdatePullRequest(ctx, ctp)
		if err != nil {
			return fmt.Errorf("failed to create/update PR: %w", err)
		}
	} else {
		err = gitOperations.PromoteEnvironmentWithMerge(ctx, ctp.Spec.ActiveBranch, ctp.Spec.ProposedBranch)
		if err != nil {
			return fmt.Errorf("failed to merge: %w", err)
		}
	}

	return nil
}

func (r *ChangeTransferPolicyReconciler) creatOrUpdatePullRequest(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy) error {
	logger := log.FromContext(ctx)
	if ctp.Status.Proposed.Dry.Sha == ctp.Status.Active.Dry.Sha {
		// If the proposed dry sha is different from the active dry sha, create a pull request
		return nil
	}

	logger.V(4).Info("Proposed dry sha, does not match active", "proposedDrySha", ctp.Status.Proposed.Dry.Sha, "activeDrySha", ctp.Status.Active.Dry.Sha)
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{Namespace: ctp.Namespace, Name: ctp.Spec.RepositoryReference.Name})
	if err != nil {
		return fmt.Errorf("failed to get GitRepository %q: %w", ctp.Spec.RepositoryReference.Name, err)
	}

	var prName string
	switch {
	case gitRepo.Spec.GitHub != nil:
		prName = utils.GetPullRequestName(ctx, gitRepo.Spec.GitHub.Owner, gitRepo.Spec.GitHub.Name, ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch)
	case gitRepo.Spec.GitLab != nil:
		prName = utils.GetPullRequestName(ctx, gitRepo.Spec.GitLab.Namespace, gitRepo.Spec.GitLab.Name, ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch)
	case gitRepo.Spec.Fake != nil:
		prName = utils.GetPullRequestName(ctx, gitRepo.Spec.Fake.Owner, gitRepo.Spec.Fake.Name, ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch)
	}

	prName = utils.KubeSafeUniqueName(ctx, prName)

	controllerConfiguration, err := r.SettingsMgr.GetControllerConfiguration(ctx)
	if err != nil {
		return fmt.Errorf("failed to get global promotion configuration: %w", err)
	}

	title, description, err := TemplatePullRequest(&controllerConfiguration.Spec.PullRequest, map[string]any{"ChangeTransferPolicy": ctp})
	if err != nil {
		return fmt.Errorf("failed to template pull request: %w", err)
	}

	var pr promoterv1alpha1.PullRequest
	err = r.Get(ctx, client.ObjectKey{
		Namespace: ctp.Namespace,
		Name:      prName,
	}, &pr)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			// TODO: move some of the below code into a utility function. It's a bit verbose for being nested this deeply.
			// The code below sets the ownership for the PullRequest Object
			kind := reflect.TypeOf(promoterv1alpha1.ChangeTransferPolicy{}).Name()
			gvk := promoterv1alpha1.GroupVersion.WithKind(kind)
			controllerRef := metav1.NewControllerRef(ctp, gvk)

			pr = promoterv1alpha1.PullRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:            prName,
					Namespace:       ctp.Namespace,
					OwnerReferences: []metav1.OwnerReference{*controllerRef},
					Labels: map[string]string{
						promoterv1alpha1.PromotionStrategyLabel:    utils.KubeSafeLabel(ctx, ctp.Labels[promoterv1alpha1.PromotionStrategyLabel]),
						promoterv1alpha1.ChangeTransferPolicyLabel: utils.KubeSafeLabel(ctx, ctp.Name),
						promoterv1alpha1.EnvironmentLabel:          utils.KubeSafeLabel(ctx, ctp.Spec.ActiveBranch),
					},
				},
				Spec: promoterv1alpha1.PullRequestSpec{
					RepositoryReference: ctp.Spec.RepositoryReference,
					Title:               title,
					TargetBranch:        ctp.Spec.ActiveBranch,
					SourceBranch:        ctp.Spec.ProposedBranch,
					Description:         description,
					State:               "open",
				},
			}
			err = r.Create(ctx, &pr)
			if err != nil {
				return fmt.Errorf("failed to create PR from branch %q to %q: %w", ctp.Spec.ProposedBranch, ctp.Spec.ActiveBranch, err)
			}
			r.Recorder.Event(ctp, "Normal", "PullRequestCreated", fmt.Sprintf("Pull Request %s created", pr.Name))
			logger.V(4).Info("Created pull request")
		} else {
			return fmt.Errorf("failed to get PR %q: %w", prName, err)
		}
	} else {
		// Pull Request already exists, update it.
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			prUpdated := promoterv1alpha1.PullRequest{}
			// TODO: consider skipping this Get on the first attempt, the object we already got might be up to date.
			err = r.Get(ctx, client.ObjectKey{Namespace: pr.Namespace, Name: pr.Name}, &prUpdated)
			if err != nil {
				return fmt.Errorf("failed to get PR %q: %w", pr.Name, err)
			}
			prUpdated.Spec.RepositoryReference = ctp.Spec.RepositoryReference
			prUpdated.Spec.Title = title
			prUpdated.Spec.TargetBranch = ctp.Spec.ActiveBranch
			prUpdated.Spec.SourceBranch = ctp.Spec.ProposedBranch
			prUpdated.Spec.Description = description
			return r.Update(ctx, &prUpdated)
		})
		if err != nil {
			return fmt.Errorf("failed to update PR %q: %w", pr.Name, err)
		}
		// r.Recorder.Event(ctp, "Normal", "PullRequestUpdated", fmt.Sprintf("Pull Request %s updated", pr.Name))
		logger.V(4).Info("Updated pull request resource")
	}

	return nil
}

// mergePullRequests tries to merge the pull request if all the checks have passed and the environment is set to auto merge.
func (r *ChangeTransferPolicyReconciler) mergePullRequests(ctx context.Context, ctp *promoterv1alpha1.ChangeTransferPolicy) error {
	logger := log.FromContext(ctx)

	for i, status := range ctp.Status.Proposed.CommitStatuses {
		if status.Phase != string(promoterv1alpha1.CommitPhaseSuccess) {
			logger.V(4).Info("Proposed commit status is not success", "key", ctp.Spec.ProposedCommitStatuses[i].Key, "sha", ctp.Status.Proposed.Hydrated.Sha, "phase", status.Phase)
			return nil
		}
	}

	if *ctp.Spec.AutoMerge {
		prl := promoterv1alpha1.PullRequestList{}
		// Find the PRs that match the proposed commit and the environment. There should only be one.
		err := r.List(ctx, &prl, &client.ListOptions{
			LabelSelector: labels.SelectorFromSet(map[string]string{
				promoterv1alpha1.PromotionStrategyLabel:    utils.KubeSafeLabel(ctx, ctp.Labels[promoterv1alpha1.PromotionStrategyLabel]),
				promoterv1alpha1.ChangeTransferPolicyLabel: utils.KubeSafeLabel(ctx, ctp.Name),
				promoterv1alpha1.EnvironmentLabel:          utils.KubeSafeLabel(ctx, ctp.Spec.ActiveBranch),
			}),
		})
		if err != nil {
			return fmt.Errorf("failed to list PullRequests for ChangeTransferPolicy %s and Environment %s: %w", ctp.Name, ctp.Spec.ActiveBranch, err)
		}

		if len(prl.Items) > 1 {
			return fmt.Errorf("more than one PullRequest found for ChangeTransferPolicy %s and Environment %s", ctp.Name, ctp.Spec.ActiveBranch)
		}

		if len(prl.Items) == 1 {
			// We found 1 pull request process it.
			pullRequest := prl.Items[0]
			if pullRequest.Status.State == promoterv1alpha1.PullRequestOpen {
				logger.Info("Commit status checks passed", "branch", ctp.Spec.ActiveBranch,
					"activeCommitStatuses", ctp.Status.Active.CommitStatuses,
					"proposedCommitStatuses", ctp.Status.Proposed.CommitStatuses,
					"activeDryCommitTime", ctp.Status.Active.Dry.CommitTime)
			}

			if pullRequest.Spec.State == promoterv1alpha1.PullRequestOpen && pullRequest.Status.State == promoterv1alpha1.PullRequestOpen {
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
					return fmt.Errorf("failed to update PR %q: %w", pullRequest.Name, err)
				}
				r.Recorder.Event(ctp, "Normal", "PullRequestMerged", fmt.Sprintf("Pull Request %s merged", pullRequest.Name))
				logger.Info("Merged pull request")
			} else if pullRequest.Status.State == promoterv1alpha1.PullRequestOpen {
				// This is for the case where the PR is set to merge in k8s but something else is blocking it, like an external commit status check.
				logger.Info("Pull request can not be merged, probably due to SCM", "pr", pullRequest.Name)
			}
		}
	}

	return nil
}

func TemplatePullRequest(prc *promoterv1alpha1.PullRequestConfiguration, data map[string]any) (string, string, error) {
	title, err := utils.RenderStringTemplate(prc.Template.Title, data)
	if err != nil {
		return "", "", fmt.Errorf("failed to render pull request title template: %w", err)
	}

	description, err := utils.RenderStringTemplate(prc.Template.Description, data)
	if err != nil {
		return "", "", fmt.Errorf("failed to render pull request description template: %w", err)
	}

	return title, description, nil
}
