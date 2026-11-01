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
	"slices"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	bitbucket_cloud "github.com/argoproj-labs/gitops-promoter/internal/scms/bitbucket_cloud"
	bitbucket_datacenter "github.com/argoproj-labs/gitops-promoter/internal/scms/bitbucket_datacenter"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/forgejo"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitea"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitlab"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CommitStatusReconciler reconciles a CommitStatus object
type CommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager

	// EnqueueCTP is a function to enqueue CTP reconcile requests without modifying the CTP object.
	EnqueueCTP CTPEnqueueFunc
}

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses,verbs=get;list;watch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories,verbs=get;list;watch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmproviders,verbs=get;list;watch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=clusterscmproviders,verbs=get;list;watch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicies,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the CommitStatus object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.17.2/pkg/reconcile
func (r *CommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling CommitStatus", "name", req.Name)
	startTime := time.Now()

	var cs promoterv1alpha1.CommitStatus
	// This function applies the resource status via Server-Side Apply at the end of the reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &cs, r.Client, r.Recorder, constants.CommitStatusControllerFieldOwner, &result, &err, &previousReady)

	err = r.Get(ctx, req.NamespacedName, &cs, &client.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("CommitStatus not found")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "failed to get CommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get CommitStatus %q: %w", req.Name, err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	previousReady = utils.RemoveReadyCondition(&cs)

	if err := ensureControllerInstanceIDStable(ctx, r.SettingsMgr); err != nil {
		return ctrl.Result{}, err
	}

	// empty phase should be impossible due to schema validation
	if cs.Spec.Sha == "" || cs.Spec.Phase == "" {
		logger.Info("Skip setting commit status, missing sha or phase", "sha", cs.Spec.Sha, "phase", cs.Spec.Phase)
		return ctrl.Result{}, nil
	}

	// We need the old sha to trigger the reconcile of the change transfer policy
	oldSha := cs.Status.Sha

	if !promoterv1alpha1.PublishesToSCM(cs.Spec.PublishTo) {
		// No scm destination: do not post to the SCM. Record the state in status only, and clear any SCM id from an earlier post.
		cs.Status.Sha = cs.Spec.Sha
		cs.Status.Phase = cs.Spec.Phase
		cs.Status.Id = ""
	} else {
		commitStatusProvider, err := r.getCommitStatusProvider(ctx, cs)
		if err != nil || commitStatusProvider == nil {
			return ctrl.Result{}, fmt.Errorf("failed to get CommitStatus provider: %w", err)
		}

		_, err = commitStatusProvider.Set(ctx, &cs)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to set CommitStatus state for %q: %w", req.Name, err)
		}
	}

	err = r.triggerReconcileChangeTransferPolicy(ctx, cs, oldSha, cs.Spec.Sha)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to trigger reconcile of ChangeTransferPolicy via CommitStatus: %w", err)
	}

	r.Recorder.Eventf(&cs, nil, "Normal", constants.CommitStatusSetReason, "SettingCommitStatus", "Commit status %s set to %s for hash %s", cs.Name, cs.Spec.Phase, cs.Spec.Sha)

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.CommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

func (r *CommitStatusReconciler) getCommitStatusProvider(ctx context.Context, commitStatus promoterv1alpha1.CommitStatus) (scms.CommitStatusProvider, error) {
	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, r.SettingsMgr, commitStatus.Spec.RepositoryReference, &commitStatus)
	if err != nil {
		return nil, fmt.Errorf("failed to get ScmProvider and secret for repo %q: %w", commitStatus.Spec.RepositoryReference.Name, err)
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.Client, client.ObjectKey{Namespace: commitStatus.Namespace, Name: commitStatus.Spec.RepositoryReference.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository for repo %q: %w", commitStatus.Spec.RepositoryReference.Name, err)
	}

	switch {
	case scmProvider.GetSpec().GitHub != nil:
		var p *github.CommitStatus
		p, err = github.NewGithubCommitStatusProvider(ctx, r.Client, scmProvider, *secret, gitRepo.Spec.GitHub.Owner)
		if err != nil {
			return nil, fmt.Errorf("failed to get GitHub provider for domain %q with secret %q: %w", scmProvider.GetSpec().GitHub.Domain, secret.Name, err)
		}
		return p, nil
	case scmProvider.GetSpec().GitLab != nil:
		var p *gitlab.CommitStatus
		p, err = gitlab.NewGitlabCommitStatusProvider(r.Client, *secret, scmProvider.GetSpec().GitLab.Domain)
		if err != nil {
			return nil, fmt.Errorf("failed to get GitLab provider for domain %q with secret %q: %w", scmProvider.GetSpec().GitLab.Domain, secret.Name, err)
		}
		return p, nil
	case scmProvider.GetSpec().BitbucketCloud != nil:
		var p *bitbucket_cloud.CommitStatus
		p, err = bitbucket_cloud.NewBitbucketCloudCommitStatusProvider(r.Client, *secret, scmProvider.GetSpec().BitbucketCloud.Domain)
		if err != nil {
			return nil, fmt.Errorf("failed to get Bitbucket Cloud provider with secret %q: %w", secret.Name, err)
		}
		return p, nil
	case scmProvider.GetSpec().BitbucketDataCenter != nil:
		var p *bitbucket_datacenter.CommitStatus
		p, err = bitbucket_datacenter.NewBitbucketDataCenterCommitStatusProvider(r.Client, scmProvider, *secret)
		if err != nil {
			return nil, fmt.Errorf("failed to get Bitbucket DataCenter provider for domain %q with secret %q: %w", scmProvider.GetSpec().BitbucketDataCenter.Domain, secret.Name, err)
		}
		return p, nil
	case scmProvider.GetSpec().Forgejo != nil:
		var p *forgejo.CommitStatus
		p, err = forgejo.NewForgejoCommitStatusProvider(r.Client, scmProvider, *secret)
		if err != nil {
			return nil, fmt.Errorf("failed to get Forgejo provider for domain %q with secret %q: %w", scmProvider.GetSpec().Forgejo.Domain, secret.Name, err)
		}
		return p, nil
	case scmProvider.GetSpec().Gitea != nil:
		var p *gitea.CommitStatus
		p, err = gitea.NewGiteaCommitStatusProvider(r.Client, scmProvider, *secret)
		if err != nil {
			return nil, fmt.Errorf("failed to get Gitea provider for domain %q with secret %q: %w", scmProvider.GetSpec().Gitea.Domain, secret.Name, err)
		}
		return p, nil
	case scmProvider.GetSpec().AzureDevOps != nil:
		var p *azuredevops.CommitStatus
		p, err = azuredevops.NewAzureDevopsCommitStatusProvider(ctx, r.Client, r.SettingsMgr, scmProvider, *secret, scmProvider.GetSpec().AzureDevOps.Organization)
		if err != nil {
			return nil, fmt.Errorf("failed to get Azure DevOps provider for organization %q with secret %q: %w", scmProvider.GetSpec().AzureDevOps.Organization, secret.Name, err)
		}
		return p, nil
	case scmProvider.GetSpec().Fake != nil:
		//nolint:wrapcheck // error wrapping not needed for fake provider
		return fake.NewFakeCommitStatusProvider(*secret)
	default:
		return nil, nil
	}
}

// triggerReconcileChangeTransferPolicy enqueues every ChangeTransferPolicy in the CommitStatus's namespace
// whose active or proposed hydrated SHA is the CommitStatus's previous or current SHA, so the policy re-reads
// the gate's phase. Policies in other namespaces never read this CommitStatus (setCommitStatusState lists
// only its own namespace), and the merge below dedupes by name, so a cluster-wide list would both wake
// strangers and collapse same-named policies from different namespaces into one.
//
// An empty SHA is never looked up. The hydrated-SHA field indexes also contain policies whose status has not
// been computed yet (their indexed value is ""), so before this guard a CommitStatus's first reconcile, when
// status.sha is still empty, enqueued every such policy. On a fresh install, with hundreds of CommitStatuses
// starting at once, that fanned out into thousands of needless ChangeTransferPolicy reconciles, each with git
// network calls, and the resulting status writes re-triggered the PromotionStrategy and gate controllers.
func (r *CommitStatusReconciler) triggerReconcileChangeTransferPolicy(ctx context.Context, cs promoterv1alpha1.CommitStatus, oldSha, newSha string) error {
	logger := log.FromContext(ctx)

	var ctpList []promoterv1alpha1.ChangeTransferPolicy
	for _, sha := range commitStatusShasToLookup(oldSha, newSha) {
		for _, field := range []string{".status.active.hydrated.sha", ".status.proposed.hydrated.sha"} {
			var matches promoterv1alpha1.ChangeTransferPolicyList
			if err := r.List(ctx, &matches, client.InNamespace(cs.Namespace), client.MatchingFields{field: sha}); err != nil {
				return fmt.Errorf("failed to list ChangeTransferPolicy with %s %q: %w", field, sha, err)
			}
			ctpList = utils.UpsertChangeTransferPolicyList(ctpList, matches.Items)
		}
	}

	logger.Info("ChangeTransferPolicy list", "count", len(ctpList), "oldSha", oldSha, "newSha", newSha)
	for _, ctp := range ctpList {
		if r.EnqueueCTP != nil {
			r.EnqueueCTP(ctp.Namespace, ctp.Name)
		}
		logger.Info("Reconcile of ChangeTransferPolicy triggered", "ChangeTransferPolicy", ctp.Name,
			"sha", cs.Spec.Sha, "phase", cs.Spec.Phase, "proposedHydratedSha", ctp.Status.Proposed.Hydrated.Sha, "activeHydratedSha", ctp.Status.Active.Hydrated.Sha)
	}

	return nil
}

// commitStatusShasToLookup returns the distinct non-empty SHAs among a CommitStatus's previous and current SHA,
// in that order. See triggerReconcileChangeTransferPolicy for why an empty SHA must not be looked up.
func commitStatusShasToLookup(oldSha, newSha string) []string {
	shas := make([]string, 0, 2)
	for _, sha := range []string{oldSha, newSha} {
		if sha != "" && !slices.Contains(shas, sha) {
			shas = append(shas, sha)
		}
	}
	return shas
}
