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
	"reflect"
	"time"

	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	acmetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/gitauth"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// RevertActiveCommitReconciler reconciles a RevertActiveCommit object.
type RevertActiveCommitReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager

	// EnqueueCTP wakes the ChangeTransferPolicy controller after a restore so it observes the new
	// active tip and status.blockedDrySha without waiting for its requeue interval.
	EnqueueCTP CTPEnqueueFunc
	// EnqueueCTPH wakes the history controller so the restore shows up in promotion history.
	EnqueueCTPH CTPHEnqueueFunc
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=revertactivecommits,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=revertactivecommits/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=clusterscmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile resolves spec.promotionStrategyRef and spec.branch to that environment's
// ChangeTransferPolicy, makes the policy the owner of this RevertActiveCommit, and restores the policy's
// active branch to spec.sha exactly once. A successful status (status.restoredFrom == spec.sha) is
// not repeated, so a later promotion is not overwritten when this resource is reconciled again. A
// spec.sha that carries Promoter-restored-from is refused. status.blockedDrySha is the dry SHA that
// was on the active branch; the ChangeTransferPolicy does not open a pull request that would put it
// back. A pull request for a different proposed dry SHA may open, but nothing is auto-merged while
// this RevertActiveCommit exists. Deleting it lifts both holds, but does not by itself propose the
// reverted dry SHA again: see ChangeTransferPolicyReconciler.skipPullRequestAfterRevert.
func (r *RevertActiveCommitReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling RevertActiveCommit")
	startTime := time.Now()

	var rc promoterv1alpha1.RevertActiveCommit
	// This function applies the resource status via Server-Side Apply at the end of the reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &rc, r.Client, r.Recorder, constants.RevertActiveCommitControllerFieldOwner, &result, &err, &previousReady)

	err = r.Get(ctx, req.NamespacedName, &rc)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			logger.Info("RevertActiveCommit not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get RevertActiveCommit: %w", err)
	}

	previousReady = utils.RemoveReadyCondition(&rc)

	if !rc.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	if err := ensureControllerInstanceIDStable(ctx, r.SettingsMgr); err != nil {
		return ctrl.Result{}, err
	}

	ctp, err := r.resolveChangeTransferPolicy(ctx, &rc)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Owner reference first, before any git work: it ties the gate to the policy for garbage collection
	// and for the ChangeTransferPolicy controller's watch, and must exist even when the restore fails.
	if err := r.applyOwnerReference(ctx, &rc, ctp); err != nil {
		return ctrl.Result{}, err
	}

	if rc.Status.RestoredFrom == rc.Spec.Sha {
		logger.V(4).Info("restore already applied", "sha", rc.Spec.Sha, "activeSha", rc.Status.ActiveSha)
		return r.requeueResult(ctx)
	}

	scmProvider, secret, gitRepo, err := utils.GetScmProviderSecretAndGitRepositoryFromRepositoryReference(ctx, r.Client, r.SettingsMgr.GetControllerNamespace(), ctp.Spec.RepositoryReference, ctp)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get ScmProvider and secret for repo %q: %w", ctp.Spec.RepositoryReference.Name, err)
	}

	gitAuthProvider, err := gitauth.CreateGitOperationsProvider(ctx, r.Client, scmProvider, secret, client.ObjectKey{Namespace: ctp.Namespace, Name: ctp.Spec.RepositoryReference.Name})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create git auth provider for ScmProvider %q: %w", scmProvider.GetName(), err)
	}
	// Distinct from the ChangeTransferPolicy controller's identity (namespace/name). The suffix keeps
	// the restore clone independent of the policy's long-lived clone of the same repo. The restore
	// runs once, so the clone is removed when this reconcile ends rather than kept for the life of
	// the process.
	gitOperations := git.NewEnvironmentOperations(gitRepo, gitAuthProvider, ctp.Namespace+"/"+ctp.Name+"-revert")
	defer func() {
		if rmErr := gitOperations.RemoveClone(); rmErr != nil {
			logger.Error(rmErr, "failed to remove RevertActiveCommit clone")
		}
	}()
	if err := gitOperations.CloneRepo(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to clone repo %q: %w", ctp.Spec.RepositoryReference.Name, err)
	}

	restored, err := gitOperations.RestoreActiveBranch(ctx, ctp.Spec.ActiveBranch, ctp.Spec.ActivePath, rc.Spec.Sha)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to restore %q to %q: %w", ctp.Spec.ActiveBranch, rc.Spec.Sha, err)
	}

	rc.Status.ActiveSha = restored.ActiveSha
	rc.Status.BlockedDrySha = restored.BlockedDrySha
	rc.Status.RestoredFrom = rc.Spec.Sha

	if r.EnqueueCTP != nil {
		r.EnqueueCTP(ctp.Namespace, ctp.Name)
	}
	if r.EnqueueCTPH != nil {
		r.EnqueueCTPH(ctp.Namespace, utils.GetChangeTransferPolicyHistoryName(ctp.Name))
	}

	if restored.Unchanged {
		r.Recorder.Eventf(&rc, nil, "Normal", "AlreadyRestored", "Restoring", "%s already matches %s at %s; nothing was written", ctp.Spec.ActiveBranch, rc.Spec.Sha, restored.ActiveSha)
	} else {
		r.Recorder.Eventf(&rc, nil, "Normal", "Restored", "Restoring", "Restored %s to %s as %s", ctp.Spec.ActiveBranch, rc.Spec.Sha, restored.ActiveSha)
	}
	return r.requeueResult(ctx)
}

// requeueResult schedules the next reconcile from ControllerConfiguration. The git restore itself
// still runs once: a later pass returns before cloning when status.restoredFrom already matches spec.sha.
func (r *RevertActiveCommitReconciler) requeueResult(ctx context.Context) (ctrl.Result, error) {
	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.RevertActiveCommitConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration for RevertActiveCommit: %w", err)
	}
	return ctrl.Result{RequeueAfter: requeueDuration}, nil
}

// resolveChangeTransferPolicy loads the PromotionStrategy and the ChangeTransferPolicy the strategy
// controller created for spec.branch.
func (r *RevertActiveCommitReconciler) resolveChangeTransferPolicy(ctx context.Context, rc *promoterv1alpha1.RevertActiveCommit) (*promoterv1alpha1.ChangeTransferPolicy, error) {
	ps := &promoterv1alpha1.PromotionStrategy{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: rc.Namespace, Name: rc.Spec.PromotionStrategyRef.Name}, ps); err != nil {
		return nil, fmt.Errorf("failed to get PromotionStrategy %q: %w", rc.Spec.PromotionStrategyRef.Name, err)
	}
	if !strategyHasBranch(ps, rc.Spec.Branch) {
		return nil, fmt.Errorf("branch %q is not an environment on PromotionStrategy %q", rc.Spec.Branch, ps.Name)
	}
	ctpName := utils.ChangeTransferPolicyNameForEnvironment(ps.Name, rc.Spec.Branch)
	ctp := &promoterv1alpha1.ChangeTransferPolicy{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: rc.Namespace, Name: ctpName}, ctp); err != nil {
		return nil, fmt.Errorf("failed to get ChangeTransferPolicy %q for PromotionStrategy %q branch %q: %w", ctpName, ps.Name, rc.Spec.Branch, err)
	}
	return ctp, nil
}

func strategyHasBranch(ps *promoterv1alpha1.PromotionStrategy, branch string) bool {
	for i := range ps.Spec.Environments {
		if ps.Spec.Environments[i].Branch == branch {
			return true
		}
	}
	return false
}

// applyOwnerReference makes the ChangeTransferPolicy the controller owner of the RevertActiveCommit via
// Server-Side Apply. Only metadata.ownerReferences is declared, so the user's spec stays with its
// own field manager. Deleting the policy garbage-collects its RevertActiveCommits.
func (r *RevertActiveCommitReconciler) applyOwnerReference(ctx context.Context, rc *promoterv1alpha1.RevertActiveCommit, ctp *promoterv1alpha1.ChangeTransferPolicy) error {
	for i := range rc.OwnerReferences {
		if rc.OwnerReferences[i].UID == ctp.UID {
			return nil
		}
	}

	kind := reflect.TypeFor[promoterv1alpha1.ChangeTransferPolicy]().Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)
	apply := acv1alpha1.RevertActiveCommit(rc.Name, rc.Namespace).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(ctp.Name).
			WithUID(ctp.UID).
			WithController(true).
			WithBlockOwnerDeletion(true))

	// Patch a bare object so the response does not overwrite the in-memory status this reconcile is building.
	target := &promoterv1alpha1.RevertActiveCommit{}
	target.Name = rc.Name
	target.Namespace = rc.Namespace
	if err := r.Patch(ctx, target, utils.ApplyPatch{ApplyConfig: apply}, client.FieldOwner(constants.RevertActiveCommitControllerFieldOwner), client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to set owner reference on RevertActiveCommit %q: %w", rc.Name, err)
	}
	rc.OwnerReferences = target.OwnerReferences
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RevertActiveCommitReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.RevertActiveCommitConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get RevertActiveCommit rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.RevertActiveCommitConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get RevertActiveCommit max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.RevertActiveCommit{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}
