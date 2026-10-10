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
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
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

// RestoreActiveCommitReconciler reconciles a RestoreActiveCommit object.
type RestoreActiveCommitReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=restoreactivecommits,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=restoreactivecommits/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=changetransferpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=clusterscmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile resolves spec.promotionStrategyRef and spec.branch to that environment's
// ChangeTransferPolicy, makes the policy the owner of this RestoreActiveCommit, and restores the policy's
// active branch to spec.sha exactly once. A successful status (status.restoredFrom == spec.sha) is
// not repeated, so a later promotion is not overwritten when this resource is reconciled again. A
// spec.sha that carries Promoter-restored-from is refused. A spec.sha the active tip already
// restores is also refused once that tip's note carries Promoter-restore-unblocked-at; the note is
// not rewritten. status.blockedDrySha is the dry SHA that was on the active branch; the
// ChangeTransferPolicy does not open a pull request that would put it
// back while the restore commit's note lacks Promoter-restore-unblocked-at. A pull request for a different
// proposed dry SHA may open, but nothing is auto-merged until that trailer is written. Setting
// spec.blockEnvironment to false (it defaults to true) deletes this resource. The finalizer then
// stamps Promoter-restore-unblocked-at before the object can disappear. Deleting it while the field
// is still true does not stamp the trailer, so deleting the PromotionStrategy cannot release the
// hold. It does not by itself propose the blocked dry SHA again: see
// ChangeTransferPolicyReconciler.skipPullRequestAfterRevert.
func (r *RestoreActiveCommitReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling RestoreActiveCommit")
	startTime := time.Now()

	var rc promoterv1alpha1.RestoreActiveCommit
	// This function applies the resource status via Server-Side Apply at the end of the reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &rc, r.Client, r.Recorder, constants.RestoreActiveCommitControllerFieldOwner, &result, &err, &previousReady)

	err = r.Get(ctx, req.NamespacedName, &rc)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			logger.Info("RestoreActiveCommit not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get RestoreActiveCommit: %w", err)
	}

	if deleted, err := r.handleFinalizer(ctx, &rc); err != nil || deleted {
		return ctrl.Result{}, err
	}

	previousReady = utils.RemoveReadyCondition(&rc)

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
		if rc.Spec.BlocksEnvironment() {
			logger.V(4).Info("restore already applied", "sha", rc.Spec.Sha, "activeSha", rc.Status.ActiveSha)
			return r.requeueResult(ctx)
		}
		return r.unblockAndSelfDelete(ctx, &rc)
	}

	scmProvider, secret, gitRepo, err := utils.GetScmProviderSecretAndGitRepositoryFromRepositoryReference(ctx, r.Client, r.SettingsMgr, ctp.Spec.RepositoryReference, ctp)
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
	gitOperations := git.NewEnvironmentOperations(gitRepo, gitAuthProvider, rc.Namespace+"/"+rc.Name+"-revert")
	defer func() {
		if rmErr := gitOperations.RemoveClone(); rmErr != nil {
			logger.Error(rmErr, "failed to remove RestoreActiveCommit clone")
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

	if restored.Unchanged {
		r.Recorder.Eventf(&rc, nil, "Normal", "AlreadyRestored", "Restoring", "%s already matches %s at %s; nothing was written", ctp.Spec.ActiveBranch, rc.Spec.Sha, restored.ActiveSha)
	} else {
		r.Recorder.Eventf(&rc, nil, "Normal", "Restored", "Restoring", "Restored %s to %s as %s", ctp.Spec.ActiveBranch, rc.Spec.Sha, restored.ActiveSha)
	}
	// blockEnvironment false deletes on the next pass, after this status is stored, so the
	// finalizer observes activeSha and stamps the note before the object disappears.
	return r.requeueResult(ctx)
}

// unblockAndSelfDelete stamps Promoter-restore-unblocked-at then deletes the RestoreActiveCommit.
// Stamping first keeps a ChangeTransferPolicy reconcile in this window from adopting a second
// object against a still-blocked note. The finalizer stamps again, idempotently, and will not
// release until that write has succeeded.
func (r *RestoreActiveCommitReconciler) unblockAndSelfDelete(ctx context.Context, rc *promoterv1alpha1.RestoreActiveCommit) (ctrl.Result, error) {
	if err := r.unblockRestoreOnDelete(ctx, rc); err != nil {
		return ctrl.Result{}, err
	}
	if err := r.Delete(ctx, rc); err != nil && !k8s_errors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("failed to delete RestoreActiveCommit after blockEnvironment was set false: %w", err)
	}
	return ctrl.Result{}, nil
}

// handleFinalizer keeps RestoreActiveCommitFinalizer on the resource until deletion. When
// spec.blockEnvironment is false the finalizer stamps Promoter-restore-unblocked-at before it
// releases, so a self-delete cannot drop the object ahead of the note. A delete while the field
// is still true releases without writing. The first bool is true when Reconcile should not run
// the restore path (the resource is terminating).
func (r *RestoreActiveCommitReconciler) handleFinalizer(ctx context.Context, rc *promoterv1alpha1.RestoreActiveCommit) (bool, error) {
	finalizer := promoterv1alpha1.RestoreActiveCommitFinalizer

	if rc.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(rc, finalizer) {
			return false, nil
		}
		return false, retry.RetryOnConflict(retry.DefaultRetry, func() error { //nolint:wrapcheck // RetryOnConflict returns wrapped error
			if err := r.Get(ctx, client.ObjectKeyFromObject(rc), rc); err != nil {
				return err //nolint:wrapcheck // error will be wrapped by caller
			}
			if controllerutil.AddFinalizer(rc, finalizer) {
				return r.Update(ctx, rc) //nolint:wrapcheck // RetryOnConflict returns wrapped error
			}
			return nil
		})
	}

	if !controllerutil.ContainsFinalizer(rc, finalizer) {
		return true, nil
	}

	if !rc.Spec.BlocksEnvironment() {
		if err := r.unblockRestoreOnDelete(ctx, rc); err != nil {
			return false, err
		}
	}

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error { //nolint:wrapcheck // RetryOnConflict returns wrapped error
		if err := r.Get(ctx, client.ObjectKeyFromObject(rc), rc); err != nil {
			return err //nolint:wrapcheck // error will be wrapped by caller
		}
		if controllerutil.RemoveFinalizer(rc, finalizer) {
			return r.Update(ctx, rc) //nolint:wrapcheck // error will be wrapped by caller
		}
		return nil
	}); err != nil {
		return true, fmt.Errorf("failed to remove finalizer: %w", err)
	}
	return true, nil
}

// unblockRestoreOnDelete stamps Promoter-restore-unblocked-at on the restore commit when a
// successful restore was recorded and spec.blockEnvironment is false. When the ChangeTransferPolicy
// is already gone the git write is skipped so the finalizer can still release.
func (r *RestoreActiveCommitReconciler) unblockRestoreOnDelete(ctx context.Context, rc *promoterv1alpha1.RestoreActiveCommit) error {
	logger := log.FromContext(ctx)
	if rc.Status.RestoredFrom != rc.Spec.Sha || rc.Status.ActiveSha == "" {
		logger.Info("RestoreActiveCommit deleted before a restore was recorded; nothing to unblock")
		return nil
	}

	ctp, err := r.changeTransferPolicyForUnblock(ctx, rc)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			logger.Info("ChangeTransferPolicy or PromotionStrategy gone; skipping Promoter-restore-unblocked-at", "error", err)
			return nil
		}
		return fmt.Errorf("failed to resolve ChangeTransferPolicy for unblock: %w", err)
	}

	scmProvider, secret, gitRepo, err := utils.GetScmProviderSecretAndGitRepositoryFromRepositoryReference(ctx, r.Client, r.SettingsMgr, ctp.Spec.RepositoryReference, ctp)
	if err != nil {
		return fmt.Errorf("failed to get ScmProvider and secret for repo %q: %w", ctp.Spec.RepositoryReference.Name, err)
	}
	gitAuthProvider, err := gitauth.CreateGitOperationsProvider(ctx, r.Client, scmProvider, secret, client.ObjectKey{Namespace: ctp.Namespace, Name: ctp.Spec.RepositoryReference.Name})
	if err != nil {
		return fmt.Errorf("failed to create git auth provider for ScmProvider %q: %w", scmProvider.GetName(), err)
	}
	gitOperations := git.NewEnvironmentOperations(gitRepo, gitAuthProvider, rc.Namespace+"/"+rc.Name+"-revert")
	defer func() {
		if rmErr := gitOperations.RemoveClone(); rmErr != nil {
			logger.Error(rmErr, "failed to remove RestoreActiveCommit clone during unblock")
		}
	}()
	if err := gitOperations.CloneRepo(ctx); err != nil {
		return fmt.Errorf("failed to clone repo %q for unblock: %w", ctp.Spec.RepositoryReference.Name, err)
	}
	if err := gitOperations.FetchNotes(ctx); err != nil {
		return fmt.Errorf("failed to fetch git notes for unblock: %w", err)
	}

	wrote, err := gitOperations.UnblockRestore(ctx, rc.Status.ActiveSha, time.Now())
	if err != nil {
		return fmt.Errorf("failed to stamp Promoter-restore-unblocked-at on %q: %w", rc.Status.ActiveSha, err)
	}
	if wrote {
		r.Recorder.Eventf(rc, nil, "Normal", "RestoreUnblocked", "Unblocking", "Stamped Promoter-restore-unblocked-at on %s so promotion may resume", rc.Status.ActiveSha)
	}
	return nil
}

// changeTransferPolicyForUnblock loads the ChangeTransferPolicy named for this RestoreActiveCommit's
// strategy and branch without requiring the branch to still be listed on the PromotionStrategy.
func (r *RestoreActiveCommitReconciler) changeTransferPolicyForUnblock(ctx context.Context, rc *promoterv1alpha1.RestoreActiveCommit) (*promoterv1alpha1.ChangeTransferPolicy, error) {
	if rc.Spec.PromotionStrategyRef.Name == "" || rc.Spec.Branch == "" {
		return nil, fmt.Errorf("RestoreActiveCommit %q has empty promotionStrategyRef or branch", rc.Name)
	}
	ctpName := utils.ChangeTransferPolicyNameForEnvironment(rc.Spec.PromotionStrategyRef.Name, rc.Spec.Branch)
	ctp := &promoterv1alpha1.ChangeTransferPolicy{}
	if err := r.Get(ctx, client.ObjectKey{Namespace: rc.Namespace, Name: ctpName}, ctp); err != nil {
		return nil, fmt.Errorf("failed to get ChangeTransferPolicy %q: %w", ctpName, err)
	}
	return ctp, nil
}

// requeueResult schedules the next reconcile from ControllerConfiguration. The git restore itself
// still runs once: a later pass returns before cloning when status.restoredFrom already matches spec.sha.
func (r *RestoreActiveCommitReconciler) requeueResult(ctx context.Context) (ctrl.Result, error) {
	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.RestoreActiveCommitConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration for RestoreActiveCommit: %w", err)
	}
	return ctrl.Result{RequeueAfter: requeueDuration}, nil
}

// resolveChangeTransferPolicy loads the PromotionStrategy and the ChangeTransferPolicy the strategy
// controller created for spec.branch.
func (r *RestoreActiveCommitReconciler) resolveChangeTransferPolicy(ctx context.Context, rc *promoterv1alpha1.RestoreActiveCommit) (*promoterv1alpha1.ChangeTransferPolicy, error) {
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

// applyOwnerReference makes the ChangeTransferPolicy the controller owner of the RestoreActiveCommit via
// Server-Side Apply. Only metadata.ownerReferences is declared, so the user's spec stays with its
// own field manager. Deleting the policy garbage-collects its RestoreActiveCommits.
func (r *RestoreActiveCommitReconciler) applyOwnerReference(ctx context.Context, rc *promoterv1alpha1.RestoreActiveCommit, ctp *promoterv1alpha1.ChangeTransferPolicy) error {
	for i := range rc.OwnerReferences {
		if rc.OwnerReferences[i].UID == ctp.UID {
			return nil
		}
	}

	kind := reflect.TypeFor[promoterv1alpha1.ChangeTransferPolicy]().Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)
	apply := acv1alpha1.RestoreActiveCommit(rc.Name, rc.Namespace).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(ctp.Name).
			WithUID(ctp.UID).
			WithController(true).
			WithBlockOwnerDeletion(true))

	// Patch a bare object so the response does not overwrite the in-memory status this reconcile is building.
	target := &promoterv1alpha1.RestoreActiveCommit{}
	target.Name = rc.Name
	target.Namespace = rc.Namespace
	if err := r.Patch(ctx, target, utils.ApplyPatch{ApplyConfig: apply}, client.FieldOwner(constants.RestoreActiveCommitControllerFieldOwner), client.ForceOwnership); err != nil {
		return fmt.Errorf("failed to set owner reference on RestoreActiveCommit %q: %w", rc.Name, err)
	}
	rc.OwnerReferences = target.OwnerReferences
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *RestoreActiveCommitReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.RestoreActiveCommitConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get RestoreActiveCommit rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.RestoreActiveCommitConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get RestoreActiveCommit max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.RestoreActiveCommit{}, builder.WithPredicates(predicate.Or(
			predicate.GenerationChangedPredicate{},
			// deletionTimestamp is metadata, so generation does not change when a delete is requested.
			// Without this, a terminating RestoreActiveCommit would never run the finalizer that
			// stamps Promoter-restore-unblocked-at when spec.blockEnvironment is false.
			predicate.Funcs{
				UpdateFunc: func(e event.UpdateEvent) bool {
					if e.ObjectOld == nil || e.ObjectNew == nil {
						return false
					}
					return e.ObjectOld.GetDeletionTimestamp().IsZero() && !e.ObjectNew.GetDeletionTimestamp().IsZero()
				},
			},
		))).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}
