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
	"fmt"
	"reflect"
	"slices"
	"time"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/gitauth"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/argoproj-labs/gitops-promoter/internal/validations"
)

const (
	// commitValidationHistoryLimit is how many validations status.history holds.
	commitValidationHistoryLimit = 20

	// commitValidationHistoryWalk bounds how many commits of the validation notes are read to fill
	// status.history. Every environment of every PromotionStrategy in the repository writes to the same
	// notes, so one environment's validations can be far apart.
	commitValidationHistoryWalk = 500
)

// CommitValidationReconciler reconciles a CommitValidation object
type CommitValidationReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	SettingsMgr *settings.Manager

	// EnqueueValidatedCommitStatus wakes the ordering gate once a validation is recorded, so that it is
	// applied right away rather than on the gate's next poll of the validation notes.
	EnqueueValidatedCommitStatus ValidatedCommitStatusEnqueueFunc
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitvalidations,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitvalidations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitvalidations/finalizers,verbs=update
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=validatedcommitstatuses,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=gitrepositories,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=scmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=clusterscmproviders,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

// Reconcile records the validation requested in spec.sha as a line in the validation note of that dry
// commit, and mirrors the environment's validations from the repository into status.history. It never
// moves a tag: the ValidatedCommitStatus controller applies the validation it writes.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime/pkg/reconcile
func (r *CommitValidationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := logf.FromContext(ctx)
	logger.Info("Reconciling CommitValidation")
	startTime := time.Now()

	var cv promoterv1alpha1.CommitValidation
	// This applies the resource status via Server-Side Apply at the end of reconciliation. Don't write status manually.
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &cv, r.Client, r.Recorder, constants.CommitValidationControllerFieldOwner, &result, &err, &previousReady)

	if err = r.Get(ctx, req.NamespacedName, &cv); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("CommitValidation not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get CommitValidation %q: %w", req.Name, err)
	}

	// Start fresh on the Ready condition each reconcile.
	previousReady = utils.RemoveReadyCondition(&cv)

	if err := ensureControllerInstanceIDStable(ctx, r.SettingsMgr); err != nil {
		return ctrl.Result{}, err
	}

	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{Namespace: cv.Namespace, Name: cv.Spec.PromotionStrategyRef.Name}
	if err = r.Get(ctx, psKey, &ps); err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", cv.Spec.PromotionStrategyRef.Name, err)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", cv.Spec.PromotionStrategyRef.Name, err)
	}

	// A branch that is not an environment is a mistake in this resource. It is not a reason to reject
	// the request: Rejected says one thing only, that the commit does not exist.
	activePath, isEnvironment := environmentActivePaths(&ps)[cv.Spec.Branch]
	if !isEnvironment {
		return ctrl.Result{}, fmt.Errorf("spec.branch %q is not an environment of PromotionStrategy %q", cv.Spec.Branch, ps.Name)
	}
	tagName := validations.TagName(cv.Spec.Branch, activePath)

	scmProvider, secret, gitRepo, err := utils.GetScmProviderSecretAndGitRepositoryFromRepositoryReference(ctx, r.Client, r.SettingsMgr, ps.Spec.RepositoryReference, &cv)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get ScmProvider and secret for repo %q: %w", ps.Spec.RepositoryReference.Name, err)
	}
	gap, err := gitauth.CreateGitOperationsProvider(ctx, r.Client, scmProvider, secret, client.ObjectKey{Namespace: cv.Namespace, Name: ps.Spec.RepositoryReference.Name})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create git auth provider for ScmProvider %q: %w", scmProvider.GetName(), err)
	}
	// The identity names the kind, so this clone is never shared with that of another resource of the
	// same name. The ValidatedCommitStatus controller works on the same repository under its own.
	ops := git.NewEnvironmentOperations(gitRepo, gap, reflect.TypeFor[promoterv1alpha1.CommitValidation]().Name()+"/"+cv.Namespace+"/"+cv.Name)
	if err := ops.CloneRepo(ctx); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to clone repo %q: %w", ps.Spec.RepositoryReference.Name, err)
	}

	requestErr := r.handleRequest(ctx, &cv, &ps, commitValidationTarget{gap: gap, repo: gitRepo, ops: ops, tagName: tagName})

	// History is refreshed whatever became of the request, so that the resource stays useful as a view
	// of the environment while a request cannot be handled.
	history, historyErr := r.history(ctx, ops, tagName)
	if historyErr == nil {
		cv.Status.History = history
	}
	if requestErr != nil {
		return ctrl.Result{}, requestErr
	}
	if historyErr != nil {
		return ctrl.Result{}, historyErr
	}

	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.CommitValidationConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration for CommitValidation %q: %w", cv.Name, err)
	}
	return ctrl.Result{RequeueAfter: requeueDuration}, nil
}

// commitValidationTarget is where a CommitValidation writes: the repository, and the tag of its environment.
type commitValidationTarget struct {
	gap     scms.GitOperationsProvider
	repo    *promoterv1alpha1.GitRepository
	ops     *git.EnvironmentOperations
	tagName string
}

// handleRequest records the validation asked for in spec.sha, unless it was handled already. It sets
// status.request and, when the request cannot be handled yet, the Ready condition. An error means the
// request should be tried again; spec.sha stays locked until then.
func (r *CommitValidationReconciler) handleRequest(ctx context.Context, cv *promoterv1alpha1.CommitValidation, ps *promoterv1alpha1.PromotionStrategy, target commitValidationTarget) error {
	if cv.Spec.Sha == "" {
		// Nothing is asked for. The resource only shows the environment's history.
		cv.Status.Request = nil
		return nil
	}
	if cv.Status.Request != nil && cv.Status.Request.Sha == cv.Spec.Sha {
		return nil
	}
	// The outcome of an earlier SHA says nothing about this one.
	cv.Status.Request = nil

	// The gate applies what is written here, and knows the dry branch. Writing a validation that
	// nothing will apply would report a request as recorded that has no effect.
	gate, err := validatedCommitStatusFor(ctx, r.Client, ps)
	if err != nil {
		return err
	}
	if gate == nil {
		meta.SetStatusCondition(cv.GetConditions(), metav1.Condition{
			Type:               string(promoterConditions.Ready),
			Status:             metav1.ConditionFalse,
			Reason:             string(promoterConditions.ValidatedCommitStatusNotFound),
			Message:            fmt.Sprintf("PromotionStrategy %q has no ValidatedCommitStatus to apply the validation; it is recorded once there is one", ps.Name),
			ObservedGeneration: cv.Generation,
		})
		return nil
	}

	exists, err := git.RemoteCommitExists(ctx, target.gap, target.repo, cv.Spec.Sha, gate.Spec.DryBranch)
	if err != nil {
		return fmt.Errorf("failed to check that commit %q exists: %w", cv.Spec.Sha, err)
	}
	now := metav1.Now()
	if !exists {
		cv.Status.Request = &promoterv1alpha1.CommitValidationRequestStatus{
			HandledAt: now,
			Sha:       cv.Spec.Sha,
			Phase:     promoterv1alpha1.CommitValidationRejected,
			Message:   fmt.Sprintf("commit %s does not exist in the repository", cv.Spec.Sha),
		}
		r.Recorder.Eventf(cv, nil, "Warning", constants.CommitValidationRejectedReason, "RecordingValidation",
			constants.CommitValidationRejectedMessage, cv.Spec.Sha, cv.Spec.Branch)
		return nil
	}

	// The id is what makes a retry safe: if the note was pushed and status could not be written, the
	// next reconcile finds the line already there and writes nothing.
	_, err = target.ops.AppendValidations(ctx, []git.ValidationEntry{{
		Sha: cv.Spec.Sha,
		Line: validations.Line{
			Target: target.tagName,
			Source: validations.SourceResource,
			By:     reflect.TypeFor[promoterv1alpha1.CommitValidation]().Name() + "/" + cv.Name,
			Reason: cv.Spec.Reason,
			At:     now.Time,
			ID:     fmt.Sprintf("%s/%d", cv.UID, cv.Generation),
		},
	}})
	if err != nil {
		return fmt.Errorf("failed to record the validation of %q: %w", cv.Spec.Sha, err)
	}
	cv.Status.Request = &promoterv1alpha1.CommitValidationRequestStatus{
		HandledAt: now,
		Sha:       cv.Spec.Sha,
		Phase:     promoterv1alpha1.CommitValidationRecorded,
	}
	r.Recorder.Eventf(cv, nil, "Normal", constants.CommitValidationRecordedReason, "RecordingValidation",
		constants.CommitValidationRecordedMessage, cv.Spec.Sha, cv.Spec.Branch)
	if r.EnqueueValidatedCommitStatus != nil {
		r.EnqueueValidatedCommitStatus(gate.Namespace, gate.Name)
	}
	return nil
}

// history returns the environment's validations as the repository records them, newest first.
func (r *CommitValidationReconciler) history(ctx context.Context, ops *git.EnvironmentOperations, tagName string) ([]promoterv1alpha1.CommitValidationHistoryEntry, error) {
	if _, err := ops.SyncValidationNotes(ctx); err != nil {
		return nil, fmt.Errorf("failed to fetch validation notes: %w", err)
	}
	recorded, _, err := ops.ListValidations(ctx, "", commitValidationHistoryWalk)
	if err != nil {
		return nil, fmt.Errorf("failed to list validations: %w", err)
	}

	history := make([]promoterv1alpha1.CommitValidationHistoryEntry, 0, commitValidationHistoryLimit)
	for _, validation := range slices.Backward(recorded) {
		if validation.Line.Target != tagName {
			continue
		}
		history = append(history, promoterv1alpha1.CommitValidationHistoryEntry{
			ValidatedAt: metav1.NewTime(validation.At()),
			Sha:         validation.Sha,
			Source:      promoterv1alpha1.CommitValidationSource(validation.Line.Source),
			ValidatedBy: validation.By(),
			Reason:      validation.Line.Reason,
		})
		if len(history) == commitValidationHistoryLimit {
			break
		}
	}
	return history, nil
}

// commitValidationsForGate maps a ValidatedCommitStatus to the CommitValidations of the same
// PromotionStrategy, whose history it has just added to, or whose request it can now apply.
func (r *CommitValidationReconciler) commitValidationsForGate(ctx context.Context, obj client.Object) []reconcile.Request {
	gate, ok := obj.(*promoterv1alpha1.ValidatedCommitStatus)
	if !ok {
		return nil
	}
	var commitValidations promoterv1alpha1.CommitValidationList
	if err := r.List(ctx, &commitValidations, client.InNamespace(gate.Namespace), client.MatchingFields{PromotionStrategyRefField: gate.Spec.PromotionStrategyRef.Name}); err != nil {
		logf.FromContext(ctx).Error(err, "failed to list CommitValidations for ValidatedCommitStatus watch")
		return nil
	}
	requests := make([]reconcile.Request, 0, len(commitValidations.Items))
	for i := range commitValidations.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&commitValidations.Items[i])})
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *CommitValidationReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// CommitValidation is not a gate, so the index the gates share is not registered for it.
	if err := mgr.GetFieldIndexer().IndexField(ctx, &promoterv1alpha1.CommitValidation{}, PromotionStrategyRefField, func(obj client.Object) []string {
		//nolint:forcetypeassert // type is guaranteed by the IndexField API
		return []string{obj.(*promoterv1alpha1.CommitValidation).Spec.PromotionStrategyRef.Name}
	}); err != nil {
		return fmt.Errorf("failed to set field index for CommitValidation %s: %w", PromotionStrategyRefField, err)
	}

	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.CommitValidationConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get CommitValidation rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.CommitValidationConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get CommitValidation max concurrent reconciles: %w", err)
	}

	// The gate's status changes on every reconcile of a busy pipeline. Only a new gate, a gate that is
	// gone, and a gate that has read further into the validation notes are of interest here.
	gateChanged := predicate.Funcs{
		CreateFunc: func(event.CreateEvent) bool { return true },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldGate, okOld := e.ObjectOld.(*promoterv1alpha1.ValidatedCommitStatus)
			newGate, okNew := e.ObjectNew.(*promoterv1alpha1.ValidatedCommitStatus)
			return okOld && okNew && oldGate.Status.ValidationsCursor != newGate.Status.ValidationsCursor
		},
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.CommitValidation{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.ValidatedCommitStatus{}, handler.EnqueueRequestsFromMapFunc(r.commitValidationsForGate), builder.WithPredicates(gateChanged)).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Named("commitvalidation").
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}
