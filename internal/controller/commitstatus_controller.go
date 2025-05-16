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

	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/util/retry"

	"k8s.io/client-go/tools/record"

	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"

	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitlab"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// CommitStatusReconciler reconciles a CommitStatus object
type CommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	SettingsMgr *settings.Manager
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
func (r *CommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling CommitStatus", "name", req.Name)
	startTime := time.Now()

	var cs promoterv1alpha1.CommitStatus
	err := r.Get(ctx, req.NamespacedName, &cs, &client.GetOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("CommitStatus not found")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "failed to get CommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get CommitStatus %q: %w", req.Name, err)
	}

	// We use observed generation pattern here to avoid provider API calls.
	if cs.Status.ObservedGeneration == cs.Generation {
		logger.Info("No need to reconcile")
		return ctrl.Result{}, nil
	}

	// empty phase should be impossible due to schema validation
	if cs.Spec.Sha == "" || cs.Spec.Phase == "" {
		logger.Info("Skip setting commit status, missing sha or phase", "sha", cs.Spec.Sha, "phase", cs.Spec.Phase)
		return ctrl.Result{}, nil
	}

	commitStatusProvider, err := r.getCommitStatusProvider(ctx, cs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get CommitStatus provider: %w", err)
	}

	// We need the old sha to trigger the reconcile of the change transfer policy
	oldSha := cs.Status.Sha

	// We use retry on conflict to avoid conflicts when updating the status because so many other controllers will be
	// creating and updating commit status and the API is very simple we try to avoid conflicts to update the status as
	// soon as possible.
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		newCs := promoterv1alpha1.CommitStatus{}
		// TODO: consider skipping Get on the initial attempt. The object we already got might be up to date.
		err = r.Get(ctx, req.NamespacedName, &newCs, &client.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get CommitStatus %q: %w", req.Name, err)
		}

		// TODO: consider pulling this outside the retry loop and instead using the reconcile requeue to handle SCM errors.
		_, err = commitStatusProvider.Set(ctx, &newCs)
		if err != nil {
			return fmt.Errorf("failed to set CommitStatus state for %q: %w", req.Name, err)
		}

		newCs.Status.ObservedGeneration = newCs.Generation
		err = r.Status().Update(ctx, &newCs)
		if err != nil {
			if errors.IsConflict(err) {
				logger.Info("Conflict while updating CommitStatus status. Retrying")
			}
			// Don't wrap this error, it'll be wrapped one level up.
			//nolint: wrapcheck
			return err
		}

		err = r.triggerReconcileChangeTransferPolicy(ctx, newCs, oldSha, cs.Spec.Sha)
		if err != nil {
			return fmt.Errorf("failed to trigger reconcile of ChangeTransferPolicy via CommitStatus: %w", err)
		}

		return nil
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update CommitStatus %q, %w", req.Name, err)
	}
	r.Recorder.Eventf(&cs, "Normal", "CommitStatusSet", "Commit status %s set to %s for hash %s", cs.Name, cs.Spec.Phase, cs.Spec.Sha)

	logger.Info("Reconciling CommitStatus End", "duration", time.Since(startTime))
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CommitStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
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

	switch {
	case scmProvider.GetSpec().GitHub != nil:
		var p *github.CommitStatus
		p, err = github.NewGithubCommitStatusProvider(r.Client, scmProvider, *secret)
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
	// TODO: parallelize this loop since it contains network calls.
	for _, ctp := range ctpList {
		if ctp.Annotations == nil {
			ctp.Annotations = map[string]string{}
		}
		err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
			ctpUpdated := promoterv1alpha1.ChangeTransferPolicy{}
			err = r.Get(ctx, client.ObjectKey{Namespace: ctp.Namespace, Name: ctp.Name}, &ctpUpdated)
			if ctpUpdated.Annotations == nil {
				ctpUpdated.Annotations = map[string]string{}
			}
			ctpUpdated.Annotations[promoterv1alpha1.ReconcileAtAnnotation] = time.Now().Format(time.RFC3339Nano)
			if err != nil {
				return fmt.Errorf("failed to get ChangeTransferPolicy %q: %w", ctp.Name, err)
			}
			return r.Update(ctx, &ctpUpdated)
		})
		if err != nil {
			return fmt.Errorf("failed to update ChangeTransferPolicy %q: %w", ctp.Name, err)
		}
		logger.Info("Reconcile of ChangeTransferPolicy triggered", "ChangeTransferPolicy", ctp.Name,
			"phase", cs.Spec.Phase, "proposedHydratedSha", ctp.Status.Proposed.Hydrated.Sha, "activeHydratedSha", ctp.Status.Active.Hydrated.Sha)
	}

	return nil
}
