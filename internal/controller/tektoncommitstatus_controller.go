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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"text/template"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	tekton "github.com/argoproj-labs/gitops-promoter/internal/types/tekton"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	sprig "github.com/go-task/slim-sprig/v3"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	commitStatusLabelKey = promoterv1alpha1.CommitStatusGateLabelPrefix + "tekton-commit-status"
	commitShaLabelKey    = promoterv1alpha1.CommitStatusGateLabelPrefix + "commit-sha"
	maxRunNameLength     = 63
)

// TektonCommitStatusReconciler reconciles a TektonCommitStatus object.
type TektonCommitStatusReconciler struct {
	client.Client
	RuntimeScheme   *runtime.Scheme
	Recorder        events.EventRecorder
	SettingsMgr     *settings.Manager
	EnqueueCTP      CTPEnqueueFunc
	expressionCache sync.Map
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=tektoncommitstatuses,verbs=get;list;watch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=tektoncommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=tektoncommitstatuses/finalizers,verbs=update
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=commitstatuses,verbs=get;list;watch;patch;create;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=promotionstrategies,verbs=get;list;watch
// +kubebuilder:rbac:groups=tekton.dev,resources=pipelineruns,verbs=get;list;watch;create;delete
// +kubebuilder:rbac:groups=tekton.dev,resources=taskruns,verbs=get;list;watch;create;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For each environment the following is done:
// 1. Fetches commit data from the promotion strategy
// 2. Creates a Tekton TaskRun or PipelineRun with settings in the TektonCommitStatus
// 3. Evaluates the runs results when its finished
// 4. Gates promotions based on the results by creating+updating CommitStatuses for each environment
func (r *TektonCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling TektonCommitStatus", "name", req.Name)
	startTime := time.Now()

	var tcs promoterv1alpha1.TektonCommitStatus
	var previousReady *metav1.Condition
	defer utils.HandleReconciliationResult(ctx, startTime, &tcs, r.Client, r.Recorder, constants.TektonCommitStatusControllerFieldOwner, &result, &err, &previousReady)

	err = r.Get(ctx, req.NamespacedName, &tcs, &client.GetOptions{})
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			logger.Info("TektonCommitStatus not found")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "failed to get TektonCommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get TektonCommitStatus: %w", err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	previousReady = utils.RemoveReadyCondition(&tcs)

	if err := ensureControllerInstanceIDStable(ctx, r.SettingsMgr); err != nil {
		return ctrl.Result{}, err
	}

	// Get the promotion strategy
	var ps promoterv1alpha1.PromotionStrategy
	psKey := client.ObjectKey{
		Namespace: tcs.Namespace,
		Name:      tcs.Spec.PromotionStrategyRef.Name,
	}
	err = r.Get(ctx, psKey, &ps)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			return ctrl.Result{}, fmt.Errorf("referenced PromotionStrategy %q not found: %w", tcs.Spec.PromotionStrategyRef.Name, err)
		}
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %q: %w", tcs.Spec.PromotionStrategyRef.Name, err)
	}

	transitionedEnvironments, commitStatuses, err := r.processEnvironments(ctx, &tcs, &ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to process environments: %w", err)
	}

	err = utils.CleanupOrphanedCommitStatuses(ctx, r.Client, r.Recorder, &tcs, commitStatuses)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to cleanup orphaned CommitStatus resources: %w", err)
	}

	// Inherit conditions from CommitStatus objects
	utils.InheritNotReadyConditionFromObjects(&tcs, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	// If any validations transitioned to success, enqueue the corresponding ChangeTransferPolicies
	utils.EnqueueChangeTransferPolicies(ctx, r.EnqueueCTP, &ps, transitionedEnvironments, "validation transition")

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager
//
//nolint:dupl // Gate controllers share the same SetupWithManager skeleton by design.
func (r *TektonCommitStatusReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.TektonCommitStatusConfiguration, ctrl.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get TektonCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.TektonCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get TektonCommitStatus max concurrent reconciles: %w", err)
	}

	err = ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.TektonCommitStatus{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Watches(&promoterv1alpha1.PromotionStrategy{}, CommitStatusGatePromotionStrategyWatchHandler[promoterv1alpha1.TektonCommitStatusList](r.Client)).
		Watches(&tekton.TaskRun{}, handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &promoterv1alpha1.TektonCommitStatus{}),
			builder.WithPredicates(predicate.Funcs{
				UpdateFunc: tektonRunWatchUpdateHandler[*tekton.TaskRun],
			})).
		Watches(&tekton.PipelineRun{}, handler.EnqueueRequestForOwner(mgr.GetScheme(), mgr.GetRESTMapper(), &promoterv1alpha1.TektonCommitStatus{}),
			builder.WithPredicates(predicate.Funcs{
				UpdateFunc: tektonRunWatchUpdateHandler[*tekton.PipelineRun],
			})).
		Named("tektoncommitstatus").
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
			RateLimiter:             rateLimiter,
		}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

// tektonRunWatchUpdateHandler checks to see if a TektonRun resource has progressed
func tektonRunWatchUpdateHandler[T tekton.RunResource](event event.UpdateEvent) bool {
	oldTektonRun, ok := event.ObjectOld.(T)
	if !ok {
		return false
	}
	newTektonRun, ok := event.ObjectNew.(T)
	if !ok {
		return false
	}

	oldCondition := meta.FindStatusCondition(oldTektonRun.GetConditions(), tekton.TektonConditionType)
	newCondition := meta.FindStatusCondition(newTektonRun.GetConditions(), tekton.TektonConditionType)
	if newCondition == nil {
		return false
	}
	return oldCondition == nil || oldCondition.Status != newCondition.Status
}

func (r *TektonCommitStatusReconciler) processEnvironments(ctx context.Context, tcs *promoterv1alpha1.TektonCommitStatus, ps *promoterv1alpha1.PromotionStrategy) ([]string, []*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	// Save previous state so we know what transitioned
	previousStatus := tcs.Status.DeepCopy()
	if previousStatus == nil {
		previousStatus = &promoterv1alpha1.TektonCommitStatusStatus{}
	}

	// Create a map of previous state environments for easy lookup on running Tekton resources
	prevEnvStatuses := make(map[string]promoterv1alpha1.TektonCommitStatusEnvironmentStatus, len(previousStatus.Environments))
	for i := range previousStatus.Environments {
		prevEnvStatuses[previousStatus.Environments[i].Branch] = previousStatus.Environments[i]
	}

	// Build a map of environment statuses for efficient lookup
	envStatuses := make(map[string]promoterv1alpha1.EnvironmentStatus, len(ps.Status.Environments))
	for i := range ps.Status.Environments {
		envStatuses[ps.Status.Environments[i].Branch] = ps.Status.Environments[i]
	}

	// Parse the reportOn by checking the promotion strategy for where the key is
	reportOn, err := utils.DetermineReportOn(ps, tcs.Spec.Key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to determine ReportOn for TektonCommitStatus: %w", err)
	}

	// Reject when reportOn is active and target is proposed because this scenario does not make sense.
	if reportOn == constants.CommitRefActive && tcs.Spec.Target == constants.CommitRefProposed {
		return nil, nil, errors.New("commit status is set to be active but the target is proposed which is unsupported")
	}

	// Get environments where this TektonCommitStatus applies to
	applicableEnvs := utils.GetApplicableEnvironments(ps, tcs.Spec.Key, reportOn)

	// Setup tracking variables
	transitionedEnvs := make([]string, 0)
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, 0, len(applicableEnvs))
	newEnvs := make([]promoterv1alpha1.TektonCommitStatusEnvironmentStatus, 0, len(applicableEnvs))

	for _, env := range applicableEnvs {
		envStatus, found := envStatuses[env.Branch]
		if !found {
			return nil, nil, fmt.Errorf("environment %q not found in PromotionStrategy %s status", env.Branch, ps.Name)
		}

		// Figure out which sha should be used for the Tekton run
		shaToRun := envStatus.Proposed.Hydrated.Sha
		if tcs.Spec.Target == constants.CommitRefActive {
			shaToRun = envStatus.Active.Hydrated.Sha
		}

		// If there is no SHA to run then skip environment
		if shaToRun == "" {
			logger.Info("No hydrated commit SHA to use, skipping environment", "branch", env.Branch)
			continue
		}

		// Create or get the Tekton resource based on if it is found
		prevEnvStatus, found := prevEnvStatuses[env.Branch]
		needToCreate := false
		if !found || prevEnvStatus.TargetedSha != shaToRun {
			needToCreate = true
		}

		// TektonRun has been pruned if sha is the same, prune run is enabled, and the commit status is successful
		pruned := prevEnvStatus.TargetedSha == shaToRun && tcs.Spec.PruneRun && prevEnvStatus.Phase == promoterv1alpha1.CommitPhaseSuccess

		// Get the current status of the Tekton resource
		runName := ""
		var currentStatus tekton.RunStatus
		if needToCreate {
			// Create the run if it doesn't exist or SHA changed
			runName, currentStatus, err = r.createTektonRun(ctx, tcs, env.Branch, shaToRun)
			if k8s_errors.IsAlreadyExists(err) {
				runName = prevEnvStatus.TektonRunName
				currentStatus, err = r.getTektonRun(ctx, tcs, prevEnvStatus)
			}
			if err != nil {
				return nil, nil, err
			}
		} else if !pruned {
			// Get existing run status if not creating and not pruned
			runName = prevEnvStatus.TektonRunName
			currentStatus, err = r.getTektonRun(ctx, tcs, prevEnvStatus)
			if k8s_errors.IsNotFound(err) {
				// Run was deleted, recreate it
				runName, currentStatus, err = r.createTektonRun(ctx, tcs, env.Branch, shaToRun)
			}
			if err != nil {
				return nil, nil, err
			}
		}

		var newEnvStatus promoterv1alpha1.TektonCommitStatusEnvironmentStatus
		var description string
		if pruned {
			// If TektonRun has been pruned reuse the previous status as we do not have data to calculate and result was already successful
			newEnvStatus = prevEnvStatus
			description = fmt.Sprintf("Tekton run %s of kind %s succeeded and all result checks passed on environment %s", newEnvStatus.TektonRunName, tcs.Spec.TektonResource.Kind, env.Branch)
		} else {
			newEnvStatus, description, err = r.calculateNewEnvironmentStatus(tcs, currentStatus, env.Branch, shaToRun, runName)
			if err != nil {
				return nil, nil, err
			}
		}

		if prevEnvStatus.Phase != promoterv1alpha1.CommitPhaseSuccess && newEnvStatus.Phase == promoterv1alpha1.CommitPhaseSuccess {
			transitionedEnvs = append(transitionedEnvs, env.Branch)
			logger.Info("Validation transitioned to success", "branch", env.Branch, "sha", shaToRun)
			if tcs.Spec.PruneRun {
				// Only prune the Tekton resource on a successful transition to allow users to look at the resource to
				err = r.pruneTektonResource(ctx, tcs.Spec.TektonResource.Kind, tcs.Namespace, runName)
				if err != nil {
					logger.Error(err, "failed to prune Tekton resource, resource will still exist",
						"kind", tcs.Spec.TektonResource.Kind, "namespace", tcs.Namespace,
						"name", runName)
				}
			}
		}

		newEnvs = append(newEnvs, newEnvStatus)
		cs, err := utils.UpsertCommitStatus(ctx, r.Client, utils.UpsertCommitStatusParams{
			Parent:      tcs,
			RepoRefName: ps.Spec.RepositoryReference.Name,
			Branch:      env.Branch,
			Sha:         shaToRun,
			Key:         tcs.Spec.Key,
			Description: description,
			Phase:       newEnvStatus.Phase,
			FieldOwner:  constants.TektonCommitStatusControllerFieldOwner,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to upsert CommitStatus for environment %s: %w", env.Branch, err)
		}
		commitStatuses = append(commitStatuses, cs)

		// Emit only after the upsert succeeded so the event always describes persisted state
		emitCommitStatusPhaseChangedEvent(r.Recorder, tcs, tcs.Spec.Key, env.Branch, string(prevEnvStatus.Phase), string(newEnvStatus.Phase))

		logger.Info("Processed environment validation",
			"branch", env.Branch,
			"shaToRun", shaToRun,
			"target", tcs.Spec.Target,
			"reportOn", reportOn,
			"phase", newEnvStatus.Phase,
			"key", tcs.Spec.Key)
	}
	// Set new status when all environment processing is done to not lose anything if there is an error
	tcs.Status.Environments = newEnvs

	return transitionedEnvs, commitStatuses, nil
}

// parseParameterTemplates loops over the provided parameters in the Tekton resource reference and creates
// a list of where values that are templated get filled in with the information needed
func parseTektonParameterTemplates(resourceRef promoterv1alpha1.TektonResourceRef, branch, sha string) ([]tekton.Data, error) {
	params := []tekton.Data{}
	templateValues := map[string]string{
		"TargetedSha": sha,
		"Branch":      branch,
	}

	for _, param := range resourceRef.Params {
		tmpl, err := template.New(param.Name).Funcs(sprig.HermeticTxtFuncMap()).Parse(param.Value)
		if err != nil {
			return nil, fmt.Errorf("failed to create template: %w", err)
		}

		var writer bytes.Buffer
		if err := tmpl.Execute(&writer, templateValues); err != nil {
			return nil, fmt.Errorf("failed to execute template: %w", err)
		}

		var jsonBytes []byte
		switch param.Type {
		case tekton.TektonDataTypeString, "":
			jsonBytes, err = json.Marshal(writer.String())
			if err != nil {
				return nil, fmt.Errorf("failed to Marshal data: %w", err)
			}
		case tekton.TektonDataTypeArray, tekton.TektonDataTypeObject:
			jsonBytes = []byte(strings.TrimSpace(writer.String()))
			if !json.Valid(jsonBytes) {
				return nil, fmt.Errorf("invalid JSON for param %s of type %s", param.Name, param.Type)
			}
		default:
			return nil, fmt.Errorf("unknown data type %s on parameter %s", param.Type, param.Name)
		}

		params = append(params, tekton.Data{Name: param.Name, Value: &apiextensionsv1.JSON{Raw: jsonBytes}, Type: param.Type})
	}
	return params, nil
}

// createSafeRunName creates a safe and unique name for a TaskRun or PipelineRun. The name is unique for each
// environment by using the commit sha in the name. A TektonRun can only have a name of 63 or less characters so
// the key is trimmed to fit.
func createUniqueSafeRunName(tcs *promoterv1alpha1.TektonCommitStatus, branch, sha string) string {
	identifier := fmt.Sprintf("%s-%s-%s", string(tcs.UID), branch, sha)
	hash := sha256.Sum256([]byte(identifier))
	shaSuffix := fmt.Sprintf("sha%s-%s", sha[:8], hex.EncodeToString(hash[:])[:8])
	maxLen := maxRunNameLength - len(shaSuffix)
	return utils.TruncateString(tcs.Spec.Key, maxLen) + shaSuffix
}

// buildTaskRun builds a TaskRun object with the information from the CommitStatus, the branch, and SHA.
// It also parses the parameters provided and evaluates the templates that may be in them.
func buildTaskRun(tcs *promoterv1alpha1.TektonCommitStatus, branch, sha string) (*tekton.TaskRun, error) {
	taskRun := &tekton.TaskRun{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: tcs.Namespace,
			Name:      createUniqueSafeRunName(tcs, branch, sha),
			Labels: map[string]string{
				commitStatusLabelKey:              utils.KubeSafeLabel(tcs.Name),
				promoterv1alpha1.EnvironmentLabel: utils.KubeSafeLabel(branch),
				commitShaLabelKey:                 utils.KubeSafeLabel(sha),
			},
		},
		Spec: tekton.TaskRunSpec{
			TaskRef: tekton.ObjectReference{
				Name: tcs.Spec.TektonResource.Name,
			},
			ServiceAccountName: tcs.Spec.TektonResource.ServiceAccount,
		},
	}
	params, err := parseTektonParameterTemplates(tcs.Spec.TektonResource, branch, sha)
	if err != nil {
		return nil, err
	}
	taskRun.Spec.Params = params
	return taskRun, nil
}

// buildPipelineRun builds a PipelineRun object with the information from the CommitStatus, the branch, and SHA.
// It also parses the parameters provided and evaluates the templates that may be in them.
func buildPipelineRun(tcs *promoterv1alpha1.TektonCommitStatus, branch, sha string) (*tekton.PipelineRun, error) {
	pipelineRun := &tekton.PipelineRun{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: tcs.Namespace,
			Name:      createUniqueSafeRunName(tcs, branch, sha),
			Labels: map[string]string{
				commitStatusLabelKey:              utils.KubeSafeLabel(tcs.Name),
				promoterv1alpha1.EnvironmentLabel: utils.KubeSafeLabel(branch),
				commitShaLabelKey:                 utils.KubeSafeLabel(sha),
			},
		},
		Spec: tekton.PipelineRunSpec{
			PipelineRef: tekton.ObjectReference{
				Name: tcs.Spec.TektonResource.Name,
			},
			TaskRunTemplate: tekton.PipelineTaskRunTemplate{
				ServiceAccountName: tcs.Spec.TektonResource.ServiceAccount,
			},
		},
	}

	params, err := parseTektonParameterTemplates(tcs.Spec.TektonResource, branch, sha)
	if err != nil {
		return nil, err
	}
	pipelineRun.Spec.Params = params
	return pipelineRun, nil
}

// createTektonResource uses the reconciler's client to append an owner reference to the object and then create it.
func (r *TektonCommitStatusReconciler) createTektonResource(ctx context.Context, tcs *promoterv1alpha1.TektonCommitStatus, obj client.Object) error {
	if err := controllerutil.SetControllerReference(tcs, obj, r.RuntimeScheme); err != nil {
		return fmt.Errorf("failed to set owner reference on object: %w", err)
	}
	if err := r.Create(ctx, obj, &client.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create TektonRun: %w", err)
	}
	return nil
}

// getTektonResource uses the reconciler's client to get a Tekton Resource by namespace and name.
func (r *TektonCommitStatusReconciler) getTektonResource(ctx context.Context, ns, name string, obj client.Object) error {
	if err := r.Get(ctx, client.ObjectKey{Namespace: ns, Name: name}, obj, &client.GetOptions{}); err != nil {
		return fmt.Errorf("failed to get TektonRun: %w", err)
	}
	return nil
}

// deleteTektonResource uses the reconciler's client to delete a Tekton Resource by namespace and name.
func (r *TektonCommitStatusReconciler) deleteTektonResource(ctx context.Context, obj client.Object) error {
	if err := r.Delete(ctx, obj, &client.DeleteOptions{}); err != nil {
		return fmt.Errorf("failed to delete TektonRun: %w", err)
	}
	return nil
}

// parseResultValue tries to unmarshal the value field of the Tekton results data to parse whether it is one of tekton's three result types being
// a string, array, or object. If the unmarshal fails then it is a string so just return it as is.
func parseResultValue(result tekton.Data) any {
	if result.Value == nil {
		return nil
	}
	var value any
	if err := json.Unmarshal(result.Value.Raw, &value); err == nil {
		return value
	}
	return result.Value
}

// createTektonRun creates either a TaskRun or PipelineRun based on what is defined in the TektonCommitStatus and returns its name and status
func (r *TektonCommitStatusReconciler) createTektonRun(ctx context.Context, tcs *promoterv1alpha1.TektonCommitStatus, branch, sha string) (string, tekton.RunStatus, error) {
	var obj client.Object
	var status tekton.RunStatus
	var runName string
	switch tcs.Spec.TektonResource.Kind {
	case tekton.TektonKindTask:
		taskRun, err := buildTaskRun(tcs, branch, sha)
		if err != nil {
			return "", tekton.RunStatus{}, err
		}
		obj = taskRun
		status = taskRun.Status
		runName = taskRun.Name
	case tekton.TektonKindPipeline:
		pipelineRun, err := buildPipelineRun(tcs, branch, sha)
		if err != nil {
			return "", tekton.RunStatus{}, err
		}
		obj = pipelineRun
		status = pipelineRun.Status
		runName = pipelineRun.Name
	default:
		return "", tekton.RunStatus{}, fmt.Errorf("unknown Tekton resource kind: %s", tcs.Spec.TektonResource.Kind)
	}
	err := r.createTektonResource(ctx, tcs, obj)
	if err != nil {
		return "", tekton.RunStatus{}, err
	}
	return runName, status, nil
}

// getTektonRun gets either a TaskRun or a PipelineRun based on what is defined in the TektonCommitStatus and returns its status
func (r *TektonCommitStatusReconciler) getTektonRun(ctx context.Context, tcs *promoterv1alpha1.TektonCommitStatus, prevEnvStatus promoterv1alpha1.TektonCommitStatusEnvironmentStatus) (tekton.RunStatus, error) {
	var status tekton.RunStatus
	switch tcs.Spec.TektonResource.Kind {
	case tekton.TektonKindTask:
		taskRun := &tekton.TaskRun{}
		if err := r.getTektonResource(ctx, tcs.Namespace, prevEnvStatus.TektonRunName, taskRun); err != nil {
			return tekton.RunStatus{}, err
		}
		status = taskRun.Status
	case tekton.TektonKindPipeline:
		pipelineRun := &tekton.PipelineRun{}
		if err := r.getTektonResource(ctx, tcs.Namespace, prevEnvStatus.TektonRunName, pipelineRun); err != nil {
			return tekton.RunStatus{}, err
		}
		status = pipelineRun.Status
	default:
		return tekton.RunStatus{}, fmt.Errorf("unknown Tekton resource kind: %s", tcs.Spec.TektonResource.Kind)
	}
	return status, nil
}

// EvaluateExpression compiles (or loads from cache) and runs an expression that parses a single Tekton expression and returns if it was expected.
func (r *TektonCommitStatusReconciler) EvaluateExpression(expression string, resultValue any, isCombinedExpression bool) (bool, error) {
	program, err := r.getCompiledExpression(expression)
	if err != nil {
		return false, err
	}

	env := map[string]any{}
	if resultsMap, ok := resultValue.(map[string]any); ok && isCombinedExpression {
		env["Results"] = resultsMap
	} else {
		env["Result"] = resultValue
	}

	output, err := expr.Run(program, env)
	if err != nil {
		return false, fmt.Errorf("failed to evaluate expression: %w", err)
	}

	result, ok := output.(bool)
	if !ok {
		return false, fmt.Errorf("expression output was not a boolean, got %T", output)
	}
	return result, nil
}

// getCompiledExpression tries to load the program from the cache and if it does not exist compiles it
func (r *TektonCommitStatusReconciler) getCompiledExpression(expression string) (*vm.Program, error) {
	if cached, ok := r.expressionCache.Load(expression); ok {
		program, ok := cached.(*vm.Program)
		if !ok {
			return nil, fmt.Errorf("cached value for %s is not a program", expression)
		}
		return program, nil
	}

	program, err := expr.Compile(expression, expr.AsBool(), expr.AllowUndefinedVariables())
	if err != nil {
		return nil, fmt.Errorf("failed to compile expression: %w", err)
	}

	r.expressionCache.Store(expression, program)
	return program, nil
}

// evaluateTektonRunResults iterates over results in the status from a Tekton Run resource and evaluates the expressions provided.
// If no expressions are provided then take a look for the succeeded condition and see if it has the value of true.
// The function returns the individual results, the general expression result, the combined results in that order
func (r *TektonCommitStatusReconciler) evaluateTektonRunResults(tcs *promoterv1alpha1.TektonCommitStatus, runStatus tekton.RunStatus) ([]promoterv1alpha1.IndividualExpressionResult, bool, bool, error) {
	// If we have no expressions check conditions to make sure
	// Even though this function is only ever called in a succeeded pipeline, check still is valuable
	if tcs.UseRunStatusEvaluation() {
		condition := meta.FindStatusCondition(runStatus.Conditions, tekton.TektonConditionType)
		return []promoterv1alpha1.IndividualExpressionResult{}, true, condition != nil && condition.Status == metav1.ConditionTrue, nil
	}

	// Build maps to be able to look up by key to filter out results that may be on the run but have no expression checking them
	resultExpressions := make(map[string]string, len(tcs.Spec.ExpectedResults))
	for _, expected := range tcs.Spec.ExpectedResults {
		resultExpressions[expected.Key] = expected.Expression
	}

	allResults := map[string]any{}
	individualOk := true
	individualResults := []promoterv1alpha1.IndividualExpressionResult{}
	for _, result := range runStatus.Results {
		// Parse result and store in all results map for later use in general expression
		allResults[result.Name] = parseResultValue(result)

		// If no expression is found skip
		expression, ok := resultExpressions[result.Name]
		if !ok {
			continue
		}

		evaluatedResult, err := r.EvaluateExpression(expression, allResults[result.Name], false)
		if err != nil {
			return nil, false, false, err
		}

		// If failed set individualOk to false to allow to continue parsing
		if !evaluatedResult {
			individualOk = false
		}

		individualResults = append(individualResults, promoterv1alpha1.IndividualExpressionResult{
			Key:              result.Name,
			ExpressionResult: evaluatedResult,
		})
	}

	// If the expected results and calculated individual results do not match in length then a result was missing so return a failure no matter what
	if len(tcs.Spec.ExpectedResults) != len(individualResults) {
		return individualResults, false, false, nil
	}

	generalOk := true
	if tcs.Spec.Expression != "" {
		var err error
		generalOk, err = r.EvaluateExpression(tcs.Spec.Expression, allResults, true)
		if err != nil {
			return nil, false, false, err
		}
	}

	return individualResults, generalOk, individualOk && generalOk, nil
}

// calculateNewEnvironmentStatus calculates the new environment status for the current environment and determines the phase of the commit status. Returns the new environment
// and also the description to use for the child commit status.
func (r *TektonCommitStatusReconciler) calculateNewEnvironmentStatus(tcs *promoterv1alpha1.TektonCommitStatus, currentStatus tekton.RunStatus, branch, sha, runName string) (promoterv1alpha1.TektonCommitStatusEnvironmentStatus, string, error) {
	// Parse the current status of the Tekton resource run
	var newEnvStatus promoterv1alpha1.TektonCommitStatusEnvironmentStatus

	// Check the conditions of the Tekton Resource which will tell you what the current state is
	var description string
	var err error
	if len(currentStatus.Conditions) > 0 {
		// Loop through conditions and look for one in which the type is Succeeded.
		// See below docs for information.
		// https://github.com/tektoncd/pipeline/blob/main/docs/api-spec.md#taskrunstatus
		// https://github.com/tektoncd/pipeline/blob/main/docs/api-spec.md#pipelinerunstatus
		condition := meta.FindStatusCondition(currentStatus.Conditions, tekton.TektonConditionType)
		if condition != nil {
			newEnvStatus, description, err = r.determineEnvironmentPhases(tcs, currentStatus, condition.Status, runName, branch)
		} else {
			// If for some reason the Succeeded condition is not listed just set to running and pending
			newEnvStatus.TektonRunPhase = tekton.TektonRunPhaseRunning
			newEnvStatus.Phase = promoterv1alpha1.CommitPhasePending
		}
	} else {
		// If there are no conditions then the resource is new so just set running and pending
		newEnvStatus.TektonRunPhase = tekton.TektonRunPhaseRunning
		newEnvStatus.Phase = promoterv1alpha1.CommitPhasePending
	}
	if err != nil {
		return promoterv1alpha1.TektonCommitStatusEnvironmentStatus{}, "", fmt.Errorf("failed to determine environment phases: %w", err)
	}

	newEnvStatus.Branch = branch
	newEnvStatus.TargetedSha = sha
	newEnvStatus.TektonRunName = runName

	return newEnvStatus, description, nil
}

// determineEnvironmentPhases determines what the phase of the CommitStatus and TektonRun are for an environment based on the condition status.
// It also evaluates the results that are found on a TektonRun
func (r *TektonCommitStatusReconciler) determineEnvironmentPhases(tcs *promoterv1alpha1.TektonCommitStatus, currentStatus tekton.RunStatus, conditionStatus metav1.ConditionStatus, runName, branch string) (promoterv1alpha1.TektonCommitStatusEnvironmentStatus, string, error) {
	newEnvStatus := promoterv1alpha1.TektonCommitStatusEnvironmentStatus{}
	var description string
	switch conditionStatus {
	case metav1.ConditionTrue:
		// Tekton run has completed so parse the results
		newEnvStatus.TektonRunPhase = tekton.TektonRunPhaseSucceeded
		individualResults, generalOk, combinedOk, err := r.evaluateTektonRunResults(tcs, currentStatus)
		if err != nil {
			return promoterv1alpha1.TektonCommitStatusEnvironmentStatus{}, "", fmt.Errorf("failed to evaluate results: %w", err)
		}
		newEnvStatus.IndividualExpressionResults = individualResults
		newEnvStatus.ExpressionResult = generalOk
		if combinedOk {
			newEnvStatus.Phase = promoterv1alpha1.CommitPhaseSuccess
			description = fmt.Sprintf("Tekton run %s of kind %s succeeded and all result checks passed on environment %s", runName, tcs.Spec.TektonResource.Kind, branch)
		} else {
			newEnvStatus.Phase = promoterv1alpha1.CommitPhaseFailure
			description = fmt.Sprintf("Tekton run %s of kind %s succeeded but a result check failed on environment %s", runName, tcs.Spec.TektonResource.Kind, branch)
		}
	case metav1.ConditionFalse:
		// Tekton run failed
		newEnvStatus.TektonRunPhase = tekton.TektonRunPhaseFailed
		newEnvStatus.Phase = promoterv1alpha1.CommitPhaseFailure
		description = fmt.Sprintf("Tekton run %s of kind %s failed on environment %s", runName, tcs.Spec.TektonResource.Kind, branch)
	case metav1.ConditionUnknown:
		// Tekton run is still running
		newEnvStatus.TektonRunPhase = tekton.TektonRunPhaseRunning
		newEnvStatus.Phase = promoterv1alpha1.CommitPhasePending
		description = fmt.Sprintf("Waiting for Tekton run %s of kind %s to finish running on environment %s", runName, tcs.Spec.TektonResource.Kind, branch)
	default:
		return promoterv1alpha1.TektonCommitStatusEnvironmentStatus{}, "", fmt.Errorf("condition status was an unknown type of %v", conditionStatus)
	}
	return newEnvStatus, description, nil
}

// pruneTektonResource prunes a Tekton Resource
func (r *TektonCommitStatusReconciler) pruneTektonResource(ctx context.Context, kind, ns, name string) error {
	var obj client.Object
	switch kind {
	case tekton.TektonKindTask:
		obj = &tekton.TaskRun{ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
		}}
	case tekton.TektonKindPipeline:
		obj = &tekton.PipelineRun{ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
		}}
	default:
		return fmt.Errorf("resource kind is an unknown kind of %v", kind)
	}
	err := r.deleteTektonResource(ctx, obj)
	if err != nil && !k8s_errors.IsNotFound(err) {
		return err
	}
	return nil
}
