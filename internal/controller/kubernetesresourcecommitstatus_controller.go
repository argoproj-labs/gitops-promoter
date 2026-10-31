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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	acmetav1 "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/exprcache"
	"github.com/argoproj-labs/gitops-promoter/internal/resourcetemplate"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// KubernetesResourceCommitStatusReconciler reconciles KubernetesResourceCommitStatus resources: for every
// applicable PromotionStrategy environment it instantiates the real resource from spec.template,
// evaluates spec.phase.expression against the live resource, and upserts the child CommitStatus.
type KubernetesResourceCommitStatusReconciler struct {
	client.Client
	Recorder    events.EventRecorder
	Scheme      *runtime.Scheme
	SettingsMgr *settings.Manager
	EnqueueCTP  CTPEnqueueFunc

	// configured is the startup snapshot of ControllerConfiguration.spec.kubernetesResourceCommitStatus.resources,
	// read in SetupWithManager. Gates referencing a kind outside this set fail reconcile.
	configured map[schema.GroupVersionKind]struct{}
	exprCache  *exprcache.Cache
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=kubernetesresourcecommitstatuses,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=kubernetesresourcecommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=kubernetesresourcecommitstatuses/finalizers,verbs=update
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses,verbs=get;list;watch;patch;create;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch

// Reconcile fetches the KubernetesResourceCommitStatus and its PromotionStrategy, parses the template, processes each
// applicable environment (create or apply the real resource, evaluate the phase expression, upsert the
// CommitStatus), prunes resources beyond the history limit, cleans up orphaned CommitStatuses, and enqueues
// ChangeTransferPolicies when an environment transitions to success.
func (r *KubernetesResourceCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling KubernetesResourceCommitStatus")
	startTime := time.Now()

	var krcs promoterv1alpha1.KubernetesResourceCommitStatus
	var previousReady *metav1.Condition
	// This function applies the resource status via Server-Side Apply at the end of the reconciliation. Don't write status manually.
	defer func() {
		utils.HandleReconciliationResult(ctx, startTime, &krcs, r.Client, r.Recorder, constants.KubernetesResourceCommitStatusControllerFieldOwner, &result, &err, &previousReady)
	}()

	if err = r.Get(ctx, req.NamespacedName, &krcs); err != nil {
		if k8serrors.IsNotFound(err) {
			logger.Info("KubernetesResourceCommitStatus not found")
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get KubernetesResourceCommitStatus %q: %w", req.Name, err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	previousReady = utils.RemoveReadyCondition(&krcs)

	if err := ensureControllerInstanceIDStable(ctx, r.SettingsMgr); err != nil {
		return ctrl.Result{}, err
	}

	var ps promoterv1alpha1.PromotionStrategy
	if err = r.Get(ctx, client.ObjectKey{Namespace: krcs.Namespace, Name: krcs.Spec.PromotionStrategyRef.Name}, &ps); err != nil {
		if k8serrors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", krcs.Spec.PromotionStrategyRef.Name, err)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", krcs.Spec.PromotionStrategyRef.Name, err)
	}

	template, sourceGVK, err := r.parseTemplate(&krcs)
	if err != nil {
		return ctrl.Result{}, err
	}

	commitStatuses, transitioned, err := r.processEnvironments(ctx, &krcs, &ps, template, sourceGVK)
	if err != nil {
		return ctrl.Result{}, err
	}

	if err := r.pruneResources(ctx, &krcs, &ps, sourceGVK); err != nil {
		return ctrl.Result{}, err
	}

	if err := utils.CleanupOrphanedCommitStatuses(ctx, r.Client, r.Recorder, &krcs, commitStatuses); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to cleanup orphaned CommitStatus resources: %w", err)
	}

	utils.InheritNotReadyConditionFromObjects(&krcs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	utils.EnqueueChangeTransferPolicies(ctx, r.EnqueueCTP, &ps, transitioned, "resource phase transition")

	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.KubernetesResourceCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get requeue duration: %w", err)
	}
	return ctrl.Result{RequeueAfter: requeueDuration}, nil
}

// parseTemplate decodes spec.template and checks that its kind is one the controller was configured to create.
func (r *KubernetesResourceCommitStatusReconciler) parseTemplate(krcs *promoterv1alpha1.KubernetesResourceCommitStatus) (map[string]any, schema.GroupVersionKind, error) {
	template, sourceGVK, err := resourcetemplate.ParseTemplate(krcs.Spec.Template)
	if err != nil {
		return nil, schema.GroupVersionKind{}, fmt.Errorf("invalid spec.template: %w", err)
	}
	if _, ok := r.configured[sourceGVK]; !ok {
		return nil, schema.GroupVersionKind{}, fmt.Errorf("resource kind %s is not listed in ControllerConfiguration.spec.kubernetesResourceCommitStatus.resources", sourceGVK)
	}
	return template, sourceGVK, nil
}

// processEnvironments reconciles every applicable environment and rebuilds krcs.Status.Environments. When an
// environment fails, the previous status entries of the environments not yet reached are kept.
func (r *KubernetesResourceCommitStatusReconciler) processEnvironments(
	ctx context.Context,
	krcs *promoterv1alpha1.KubernetesResourceCommitStatus,
	ps *promoterv1alpha1.PromotionStrategy,
	template map[string]any,
	sourceGVK schema.GroupVersionKind,
) ([]*promoterv1alpha1.CommitStatus, []string, error) {
	snapshot := krcs.DeepCopy()
	previous := make(map[string]promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus, len(krcs.Status.Environments))
	for _, e := range krcs.Status.Environments {
		previous[e.Branch] = e
	}
	statusByBranch := make(map[string]*promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for i := range ps.Status.Environments {
		statusByBranch[ps.Status.Environments[i].Branch] = &ps.Status.Environments[i]
	}

	applicable := utils.GetApplicableEnvironments(ps, krcs.Spec.Key, krcs.Spec.ReportOn)
	newStatus := make([]promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus, 0, len(applicable))
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(applicable))
	var transitioned []string

	for i, env := range applicable {
		envStatus := statusByBranch[env.Branch]
		sha := ""
		if envStatus != nil {
			sha = reportedSha(envStatus, krcs.Spec.ReportOn)
		}
		prev, hasPrev := previous[env.Branch]
		if sha == "" {
			// The PromotionStrategy has not hydrated this environment yet; keep the last report.
			if hasPrev {
				newStatus = append(newStatus, prev)
			}
			continue
		}

		var prevPtr *promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus
		if hasPrev {
			prevPtr = &prev
		}
		entry, cs, err := r.reconcileEnvironment(ctx, environmentInput{
			gate: krcs, snapshot: snapshot, ps: ps, template: template, sourceGVK: sourceGVK,
			branch: env.Branch, envStatus: envStatus, sha: sha, previous: prevPtr,
		})
		if err != nil {
			// Preserve the previous reports of the environments we did not reach.
			for _, rest := range applicable[i:] {
				if p, ok := previous[rest.Branch]; ok {
					newStatus = append(newStatus, p)
				}
			}
			krcs.Status.Environments = newStatus
			return nil, nil, fmt.Errorf("environment %s: %w", env.Branch, err)
		}
		newStatus = append(newStatus, entry)
		commitStatuses = append(commitStatuses, cs)

		emitCommitStatusPhaseChangedEvent(r.Recorder, krcs, krcs.Spec.Key, env.Branch, string(prev.Phase), string(entry.Phase))
		if prev.Phase != promoterv1alpha1.CommitPhaseSuccess && entry.Phase == promoterv1alpha1.CommitPhaseSuccess {
			transitioned = append(transitioned, env.Branch)
		}
	}
	krcs.Status.Environments = newStatus
	return commitStatuses, transitioned, nil
}

func reportedSha(envStatus *promoterv1alpha1.EnvironmentStatus, reportOn string) string {
	if reportOn == constants.CommitRefActive {
		return envStatus.Active.Hydrated.Sha
	}
	return envStatus.Proposed.Hydrated.Sha
}

// environmentInput bundles the per-environment reconcile inputs.
type environmentInput struct {
	gate      *promoterv1alpha1.KubernetesResourceCommitStatus
	snapshot  *promoterv1alpha1.KubernetesResourceCommitStatus
	ps        *promoterv1alpha1.PromotionStrategy
	template  map[string]any
	envStatus *promoterv1alpha1.EnvironmentStatus
	previous  *promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus
	sourceGVK schema.GroupVersionKind
	branch    string
	sha       string
}

func (in environmentInput) mode() promoterv1alpha1.KubernetesResourceLifecycleMode {
	if in.gate.Spec.Lifecycle.Mode == promoterv1alpha1.KubernetesResourceLifecycleInPlace {
		return promoterv1alpha1.KubernetesResourceLifecycleInPlace
	}
	return promoterv1alpha1.KubernetesResourceLifecyclePerSha
}

func (in environmentInput) resourceName() string {
	if in.mode() == promoterv1alpha1.KubernetesResourceLifecycleInPlace {
		return resourcetemplate.InPlaceResourceName(in.gate.Name, in.branch)
	}
	return resourcetemplate.PerShaResourceName(in.gate.Name, in.branch, in.sha)
}

// isTerminalForSha reports whether the previous report already reached a terminal phase for the current SHA.
func (in environmentInput) isTerminalForSha() bool {
	if in.previous == nil || in.previous.ReportedSha != in.sha {
		return false
	}
	return in.previous.Phase == promoterv1alpha1.CommitPhaseSuccess || in.previous.Phase == promoterv1alpha1.CommitPhaseFailure
}

// reconcileEnvironment ensures the real resource exists for the environment (or re-applies it in InPlace mode),
// evaluates the phase expression against the live object, and upserts the child CommitStatus.
func (r *KubernetesResourceCommitStatusReconciler) reconcileEnvironment(ctx context.Context, in environmentInput) (promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus, *promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx).WithValues("branch", in.branch, "sha", in.sha)
	name := in.resourceName()
	live := &unstructured.Unstructured{}
	live.SetGroupVersionKind(in.sourceGVK)
	err := r.Get(ctx, client.ObjectKey{Namespace: in.gate.Namespace, Name: name}, live)
	exists := err == nil
	if err != nil && !k8serrors.IsNotFound(err) {
		return promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus{}, nil, fmt.Errorf("failed to get %s %q: %w", in.sourceGVK.Kind, name, err)
	}

	// Terminal-phase rule: once a SHA reached success or failure, a resource that disappeared afterwards
	// (for example a Job removed by ttlSecondsAfterFinished) must not be recreated, or the gate would loop.
	if !exists && in.mode() == promoterv1alpha1.KubernetesResourceLifecyclePerSha && in.isTerminalForSha() {
		logger.Info("resource is gone but the SHA already reached a terminal phase; keeping the previous report", "resource", name, "phase", in.previous.Phase)
		entry := *in.previous.DeepCopy()
		cs, err := r.upsertCommitStatus(ctx, in, entry.GateEnvironmentCommitStatus)
		if err != nil {
			return promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus{}, nil, err
		}
		return entry, cs, nil
	}

	templateData := resourcetemplate.TemplateData{
		Branch:                         in.branch,
		Sha:                            in.sha,
		ShortSha:                       resourcetemplate.ShortSha(in.sha),
		ReportOn:                       in.gate.Spec.ReportOn,
		Environment:                    in.envStatus,
		PromotionStrategy:              in.ps,
		KubernetesResourceCommitStatus: in.snapshot,
	}

	switch {
	case in.mode() == promoterv1alpha1.KubernetesResourceLifecycleInPlace:
		// Always re-apply so template changes and SHA changes propagate. Keep the original created-at
		// annotation: a fresh timestamp would modify the object on every reconcile.
		createdAt := time.Now()
		if exists {
			createdAt = resourcetemplate.ResourceCreationTime(live)
		}
		desired, err := r.buildObject(in, name, templateData, createdAt)
		if err != nil {
			return promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus{}, nil, err
		}
		// Server-Side Apply of the rendered template. The unstructured object was built from the template, not
		// from a typed API object, so zero values are explicit and ApplyConfigurationFromUnstructured is safe.
		if err := r.Apply(ctx, client.ApplyConfigurationFromUnstructured(desired), client.FieldOwner(constants.KubernetesResourceCommitStatusControllerFieldOwner), client.ForceOwnership); err != nil {
			return promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus{}, nil, fmt.Errorf("failed to apply %s %q: %w", in.sourceGVK.Kind, name, err)
		}
		if !exists {
			r.Recorder.Eventf(in.gate, nil, "Normal", constants.KubernetesResourceCreatedReason, "CreatingResource",
				constants.KubernetesResourceCreatedMessage, in.sourceGVK.Kind, name, in.branch, in.sha)
		}
		live = desired
	case !exists:
		desired, err := r.buildObject(in, name, templateData, time.Now())
		if err != nil {
			return promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus{}, nil, err
		}
		if err := r.Create(ctx, desired); err != nil && !k8serrors.IsAlreadyExists(err) {
			return promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus{}, nil, fmt.Errorf("failed to create %s %q: %w", in.sourceGVK.Kind, name, err)
		}
		logger.Info("created resource from template", "kind", in.sourceGVK.Kind, "resource", name)
		r.Recorder.Eventf(in.gate, nil, "Normal", constants.KubernetesResourceCreatedReason, "CreatingResource",
			constants.KubernetesResourceCreatedMessage, in.sourceGVK.Kind, name, in.branch, in.sha)
		live = desired
	default:
		// PerSha and the resource already exists: it is immutable from our side, evaluate it as is.
	}

	result, err := r.evaluatePhase(in, live, templateData)
	if err != nil {
		return promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus{}, nil, err
	}

	report, err := r.buildReport(in, name, live, result, templateData)
	if err != nil {
		return promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus{}, nil, err
	}
	cs, err := r.upsertCommitStatus(ctx, in, report)
	if err != nil {
		return promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus{}, nil, err
	}
	now := metav1.Now()
	return promoterv1alpha1.KubernetesResourceCommitStatusEnvironmentStatus{
		Branch:                      in.branch,
		GateEnvironmentCommitStatus: report,
		Resource: &promoterv1alpha1.KubernetesResourceReference{
			APIVersion: in.sourceGVK.GroupVersion().String(),
			Kind:       in.sourceGVK.Kind,
			Name:       name,
		},
		LastEvaluationTime: &now,
	}, cs, nil
}

func (r *KubernetesResourceCommitStatusReconciler) buildObject(in environmentInput, name string, data resourcetemplate.TemplateData, createdAt time.Time) (*unstructured.Unstructured, error) {
	gvk := promoterv1alpha1.GroupVersion.WithKind(reflect.TypeFor[promoterv1alpha1.KubernetesResourceCommitStatus]().Name())
	obj, err := resourcetemplate.BuildObject(resourcetemplate.BuildParams{
		Template:    in.template,
		Cache:       r.exprCache,
		SourceGVK:   in.sourceGVK,
		Name:        name,
		Namespace:   in.gate.Namespace,
		Labels:      resourcetemplate.ResourceLabels(in.gate, in.branch, in.sha),
		Annotations: resourcetemplate.ResourceAnnotations(createdAt),
		Owner: metav1.OwnerReference{
			APIVersion:         gvk.GroupVersion().String(),
			Kind:               gvk.Kind,
			Name:               in.gate.Name,
			UID:                in.gate.UID,
			Controller:         new(true),
			BlockOwnerDeletion: new(true),
		},
		Data: data,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to render template: %w", err)
	}
	return obj, nil
}

func (r *KubernetesResourceCommitStatusReconciler) evaluatePhase(in environmentInput, live *unstructured.Unstructured, data resourcetemplate.TemplateData) (resourcetemplate.PhaseResult, error) {
	env := data.Env()
	env["Resource"] = live.Object
	result, err := resourcetemplate.EvaluatePhaseExpression(r.exprCache, in.gate.Spec.Phase.Expression, env)
	if err != nil {
		return resourcetemplate.PhaseResult{}, fmt.Errorf("failed to evaluate phase for %s %q: %w", in.sourceGVK.Kind, live.GetName(), err)
	}
	return result, nil
}

// buildReport renders the description and URL for the CommitStatus from spec.description / spec.url, the
// expression result, or the built-in defaults.
func (r *KubernetesResourceCommitStatusReconciler) buildReport(in environmentInput, name string, live *unstructured.Unstructured, result resourcetemplate.PhaseResult, data resourcetemplate.TemplateData) (promoterv1alpha1.GateEnvironmentCommitStatus, error) {
	data.Phase = result.Phase
	data.ResourceName = name
	data.ResourceKind = in.sourceGVK.Kind
	data.Resource = live.Object
	data.ExpressionDescription = result.Description
	data.ExpressionURL = result.URL
	interpolator := resourcetemplate.NewInterpolator(r.exprCache, resourcetemplate.ReportExpressionCachePrefix, data.ReportEnv())

	description := result.Description
	if in.gate.Spec.Description != "" {
		rendered, err := interpolator.String(in.gate.Spec.Description)
		if err != nil {
			return promoterv1alpha1.GateEnvironmentCommitStatus{}, fmt.Errorf("failed to render spec.description: %w", err)
		}
		description = rendered
	}
	if description == "" {
		description = defaultDescription(in.sourceGVK.Kind, name, result.Phase)
	}

	url := result.URL
	if in.gate.Spec.Url != "" {
		rendered, err := interpolator.String(in.gate.Spec.Url)
		if err != nil {
			return promoterv1alpha1.GateEnvironmentCommitStatus{}, fmt.Errorf("failed to render spec.url: %w", err)
		}
		url = rendered
	}

	return promoterv1alpha1.GateEnvironmentCommitStatus{
		Phase:       result.Phase,
		Description: description,
		Url:         url,
		ReportedSha: in.sha,
	}, nil
}

func defaultDescription(kind, name string, phase promoterv1alpha1.CommitStatusPhase) string {
	switch phase {
	case promoterv1alpha1.CommitPhaseSuccess:
		return fmt.Sprintf("%s %s reports success", kind, name)
	case promoterv1alpha1.CommitPhaseFailure:
		return fmt.Sprintf("%s %s reports failure", kind, name)
	default:
		return fmt.Sprintf("Waiting for %s %s", kind, name)
	}
}

// upsertCommitStatus creates or updates the CommitStatus that reports this gate's result for the environment.
func (r *KubernetesResourceCommitStatusReconciler) upsertCommitStatus(ctx context.Context, in environmentInput, report promoterv1alpha1.GateEnvironmentCommitStatus) (*promoterv1alpha1.CommitStatus, error) {
	gvk := promoterv1alpha1.GroupVersion.WithKind(reflect.TypeFor[promoterv1alpha1.KubernetesResourceCommitStatus]().Name())
	commitStatusName := utils.CommitStatusResourceName(ctx, in.gate, in.branch)

	spec := acv1alpha1.CommitStatusSpec().
		WithRepositoryReference(acv1alpha1.ObjectReference().WithName(in.ps.Spec.RepositoryReference.Name)).
		WithName(in.gate.Spec.Key + "/" + in.branch).
		WithDescription(report.Description).
		WithPhase(report.Phase).
		WithSha(report.ReportedSha)
	if in.gate.Spec.PublishTo != nil {
		spec = spec.WithPublishTo(*in.gate.Spec.PublishTo)
	}
	if report.Url != "" {
		spec = spec.WithUrl(report.Url)
	}

	apply := acv1alpha1.CommitStatus(commitStatusName, in.gate.Namespace).
		WithLabels(utils.CommitStatusStandardLabels(in.gate, in.branch, in.gate.Spec.Key)).
		WithOwnerReferences(acmetav1.OwnerReference().
			WithAPIVersion(gvk.GroupVersion().String()).
			WithKind(gvk.Kind).
			WithName(in.gate.Name).
			WithUID(in.gate.UID).
			WithController(true).
			WithBlockOwnerDeletion(true)).
		WithSpec(spec)

	cs := &promoterv1alpha1.CommitStatus{}
	cs.Name = commitStatusName
	cs.Namespace = in.gate.Namespace
	if err := r.Patch(ctx, cs, utils.ApplyPatch{ApplyConfig: apply}, client.FieldOwner(constants.KubernetesResourceCommitStatusControllerFieldOwner), client.ForceOwnership); err != nil {
		return nil, fmt.Errorf("failed to apply CommitStatus: %w", err)
	}
	return cs, nil
}

// pruneResources deletes created resources whose environment is no longer gated and, in PerSha mode, older
// per-SHA resources beyond spec.lifecycle.historyLimit. The resource named in each status entry is always kept.
func (r *KubernetesResourceCommitStatusReconciler) pruneResources(ctx context.Context, krcs *promoterv1alpha1.KubernetesResourceCommitStatus, ps *promoterv1alpha1.PromotionStrategy, sourceGVK schema.GroupVersionKind) error {
	logger := log.FromContext(ctx)
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(sourceGVK.GroupVersion().WithKind(sourceGVK.Kind + "List"))
	if err := r.List(ctx, list, client.InNamespace(krcs.Namespace), client.MatchingLabels{
		utils.CommitStatusGateLabelKeyForParent(krcs): utils.KubeSafeLabel(krcs.Name),
	}); err != nil {
		return fmt.Errorf("failed to list %s resources for pruning: %w", sourceGVK.Kind, err)
	}

	applicable := map[string]bool{}
	for _, env := range utils.GetApplicableEnvironments(ps, krcs.Spec.Key, krcs.Spec.ReportOn) {
		applicable[utils.KubeSafeLabel(env.Branch)] = true
	}
	current := map[string]bool{}
	for _, e := range krcs.Status.Environments {
		if e.Resource != nil {
			current[e.Resource.Name] = true
		}
	}
	historyLimit := 1
	if krcs.Spec.Lifecycle.HistoryLimit != nil {
		historyLimit = int(*krcs.Spec.Lifecycle.HistoryLimit)
	}
	perSha := krcs.Spec.Lifecycle.Mode != promoterv1alpha1.KubernetesResourceLifecycleInPlace

	byEnv := map[string][]unstructured.Unstructured{}
	for _, item := range list.Items {
		if !metav1.IsControlledBy(&item, krcs) {
			continue
		}
		envLabel := item.GetLabels()[promoterv1alpha1.EnvironmentLabel]
		if !applicable[envLabel] {
			if err := r.deleteResource(ctx, krcs, &item, "environment is no longer gated"); err != nil {
				return err
			}
			continue
		}
		byEnv[envLabel] = append(byEnv[envLabel], item)
	}
	if !perSha {
		return nil
	}

	for env, items := range byEnv {
		// Newest first; the current resource always sorts first so it is never pruned. The created-at
		// annotation breaks ties that metadata.creationTimestamp (second granularity) cannot.
		slices.SortStableFunc(items, func(a, b unstructured.Unstructured) int {
			ca, cb := current[a.GetName()], current[b.GetName()]
			switch {
			case ca && !cb:
				return -1
			case cb && !ca:
				return 1
			default:
				return resourcetemplate.ResourceCreationTime(&b).Compare(resourcetemplate.ResourceCreationTime(&a))
			}
		})
		for i := historyLimit; i < len(items); i++ {
			if current[items[i].GetName()] {
				continue
			}
			logger.Info("pruning resource beyond history limit", "environment", env, "resource", items[i].GetName(), "historyLimit", historyLimit)
			if err := r.deleteResource(ctx, krcs, &items[i], fmt.Sprintf("exceeds historyLimit %d", historyLimit)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *KubernetesResourceCommitStatusReconciler) deleteResource(ctx context.Context, krcs *promoterv1alpha1.KubernetesResourceCommitStatus, obj *unstructured.Unstructured, reason string) error {
	if err := r.Delete(ctx, obj, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil && !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete %s %q: %w", obj.GetKind(), obj.GetName(), err)
	}
	r.Recorder.Eventf(krcs, nil, "Normal", constants.KubernetesResourcePrunedReason, "PruningResource",
		constants.KubernetesResourcePrunedMessage, obj.GetKind(), obj.GetName(), reason)
	return nil
}

// SetupWithManager registers the controller: it watches KubernetesResourceCommitStatus, PromotionStrategy, and
// every configured kind (enqueuing the owning gate). Configured kinds that are not served by the API server
// are skipped with an error log.
func (r *KubernetesResourceCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	logger := log.FromContext(ctx).WithName("kubernetesresourcecommitstatus-setup")
	r.exprCache = exprcache.New(exprcache.DefaultMaxEntries)

	resources, err := r.SettingsMgr.GetKubernetesResourceCommitStatusResourcesDirect(ctx)
	if err != nil {
		return fmt.Errorf("failed to get KubernetesResourceCommitStatus resources: %w", err)
	}
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.KubernetesResourceCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get KubernetesResourceCommitStatus rate limiter: %w", err)
	}
	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.KubernetesResourceCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get KubernetesResourceCommitStatus max concurrent reconciles: %w", err)
	}

	b := ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.KubernetesResourceCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.PromotionStrategy{}, CommitStatusGatePromotionStrategyWatchHandler[promoterv1alpha1.KubernetesResourceCommitStatusList](r.Client))

	r.configured = make(map[schema.GroupVersionKind]struct{}, len(resources))
	mapper := mgr.GetRESTMapper()
	for _, res := range resources {
		gvk := resourcetemplate.GVKOf(res)
		mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			logger.Error(err, "configured resource kind is not served by the API server; gates referencing it will fail", "gvk", gvk.String())
			continue
		}
		if mapping.Scope.Name() != meta.RESTScopeNameNamespace {
			logger.Error(nil, "configured resource kind is cluster-scoped and cannot be used as a template; skipping", "gvk", gvk.String())
			continue
		}
		r.configured[gvk] = struct{}{}

		realKind := &unstructured.Unstructured{}
		realKind.SetGroupVersionKind(gvk)
		b = b.Watches(realKind, handler.EnqueueRequestForOwner(mgr.GetScheme(), mapper, &promoterv1alpha1.KubernetesResourceCommitStatus{}, handler.OnlyControllerOwner()))
	}

	if err := b.WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter}).
		Named("kubernetesresourcecommitstatus").
		Complete(r); err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}
