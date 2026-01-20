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
	"time"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	bitbucket_cloud "github.com/argoproj-labs/gitops-promoter/internal/scms/bitbucket_cloud"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/forgejo"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitea"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitlab"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/fields"
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

//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses/finalizers,verbs=update

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
	// This function will update the resource status at the end of the reconciliation. don't call .Status().Update manually.
	defer utils.HandleReconciliationResult(ctx, startTime, &cs, r.Client, r.Recorder, &err)

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
	meta.RemoveStatusCondition(cs.GetConditions(), string(promoterConditions.Ready))

	// empty phase should be impossible due to schema validation
	if cs.Spec.Sha == "" || cs.Spec.Phase == "" {
		logger.Info("Skip setting commit status, missing sha or phase", "sha", cs.Spec.Sha, "phase", cs.Spec.Phase)
		return ctrl.Result{}, nil
	}

	commitStatusProvider, err := r.getCommitStatusProvider(ctx, cs)
	if err != nil || commitStatusProvider == nil {
		return ctrl.Result{}, fmt.Errorf("failed to get CommitStatus provider: %w", err)
	}

	// We need the old sha to trigger the reconcile of the change transfer policy
	oldSha := cs.Status.Sha

	_, err = commitStatusProvider.Set(ctx, &cs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to set CommitStatus state for %q: %w", req.Name, err)
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
	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, r.SettingsMgr.GetControllerNamespace(), commitStatus.Spec.RepositoryReference, &commitStatus)
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
		p, err = bitbucket_cloud.NewBitbucketCloudCommitStatusProvider(r.Client, *secret)
		if err != nil {
			return nil, fmt.Errorf("failed to get Bitbucket Cloud provider with secret %q: %w", secret.Name, err)
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
		p, err = azuredevops.NewAzureDevopsCommitStatusProvider(ctx, r.Client, scmProvider, *secret, scmProvider.GetSpec().AzureDevOps.Organization)
		if err != nil {
			return nil, fmt.Errorf("failed to get Azure DevOps provider for organization %q with secret %q: %w", scmProvider.GetSpec().AzureDevOps.Organization, secret.Name, err)
		}
		return p, nil
	case scmProvider.GetSpec().Fake != nil:
		//nolint: wrapcheck
		return fake.NewFakeCommitStatusProvider(*secret)
	default:
		return nil, nil
	}
}

func (r *CommitStatusReconciler) triggerReconcileChangeTransferPolicy(ctx context.Context, cs promoterv1alpha1.CommitStatus, oldSha, newSha string) error {
	logger := log.FromContext(ctx)
	// Get list of CTPs that have the oldSha
	ctpListActiveOldSha := &promoterv1alpha1.ChangeTransferPolicyList{}
	err := r.List(ctx, ctpListActiveOldSha, &client.ListOptions{
		FieldSelector: fields.SelectorFromSet(map[string]string{
			".status.active.hydrated.sha": oldSha,
		}),
	})
	if err != nil {
		return fmt.Errorf("failed to list ChangeTransferPolicy with old sha %q: %w", oldSha, err)
	}

	// Get list of CTPs that have the newSha
	ctpListActiveNewSha := &promoterv1alpha1.ChangeTransferPolicyList{}
	err = r.List(ctx, ctpListActiveNewSha, &client.ListOptions{
		FieldSelector: fields.SelectorFromSet(map[string]string{
			".status.active.hydrated.sha": newSha,
		}),
	})
	if err != nil {
		return fmt.Errorf("failed to list ChangeTransferPolicy with new sha %q: %w", newSha, err)
	}

	ctpListProposedOldSha := &promoterv1alpha1.ChangeTransferPolicyList{}
	err = r.List(ctx, ctpListProposedOldSha, &client.ListOptions{
		FieldSelector: fields.SelectorFromSet(map[string]string{
			".status.proposed.hydrated.sha": oldSha,
		}),
	})
	if err != nil {
		return fmt.Errorf("failed to list ChangeTransferPolicy with old sha %q: %w", oldSha, err)
	}

	// Get list of CTPs that have the newSha
	ctpListProposedNewSha := &promoterv1alpha1.ChangeTransferPolicyList{}
	err = r.List(ctx, ctpListProposedNewSha, &client.ListOptions{
		FieldSelector: fields.SelectorFromSet(map[string]string{
			".status.proposed.hydrated.sha": newSha,
		}),
	})
	if err != nil {
		return fmt.Errorf("failed to list ChangeTransferPolicy with new sha %q: %w", newSha, err)
	}

	ctpList := utils.UpsertChangeTransferPolicyList(ctpListActiveOldSha.Items, ctpListActiveNewSha.Items, ctpListProposedOldSha.Items, ctpListProposedNewSha.Items)

	logger.Info("ChangeTransferPolicy list", "count", len(ctpList), "oldSha", oldSha, "newSha", newSha)
	for _, ctp := range ctpList {
		// Use the enqueue function to trigger reconciliation
		if r.EnqueueCTP != nil {
			r.EnqueueCTP(ctp.Namespace, ctp.Name)
		}
		logger.Info("Reconcile of ChangeTransferPolicy triggered", "ChangeTransferPolicy", ctp.Name,
			"sha", cs.Spec.Sha, "phase", cs.Spec.Phase, "proposedHydratedSha", ctp.Status.Proposed.Hydrated.Sha, "activeHydratedSha", ctp.Status.Active.Hydrated.Sha)
	}

	return nil
}
