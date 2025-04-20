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
	"strconv"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitlab"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"

	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

var gvk = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "Application",
}

type aggregate struct {
	application  *argocd.ArgoCDApplication
	commitStatus *promoterv1alpha1.CommitStatus
}

// ArgoCDCommitStatusReconciler reconciles a ArgoCDCommitStatus object
type ArgoCDCommitStatusReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	SettingsMgr *settings.Manager
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=argocdcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=argocdcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=argocdcommitstatuses/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ArgoCDCommitStatus object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/reconcile
func (r *ArgoCDCommitStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ArgoCDCommitStatus")
	var argoCDCommitStatus promoterv1alpha1.ArgoCDCommitStatus
	err := r.Get(ctx, req.NamespacedName, &argoCDCommitStatus, &client.GetOptions{})
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			logger.Info("ArgoCDCommitStatus not found")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "failed to get ArgoCDCommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get ArgoCDCommitStatus: %w", err)
	}

	ls, err := metav1.LabelSelectorAsSelector(argoCDCommitStatus.Spec.ApplicationSelector)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to parse label selector: %w", err)
	}
	// TODO: we should setup a field index and only list apps related to the currently reconciled app
	var ulArgoCDApps unstructured.UnstructuredList
	ulArgoCDApps.SetGroupVersionKind(gvk)
	err = r.List(ctx, &ulArgoCDApps, &client.ListOptions{
		LabelSelector: ls,
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list CommitStatus objects: %w", err)
	}

	gitAuthProvider, repositoryRef, err := r.getGitAuthProvider(ctx, argoCDCommitStatus)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get git auth provider: %w", err)
	}

	groupedArgoCDApps, err := r.groupArgoCDApplicationsWithPhase(&argoCDCommitStatus, ulArgoCDApps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get ArgoCDApplication: %w", err)
	}

	for targetBranch, appsInEnvironment := range groupedArgoCDApps {
		gitOperation, err := git.NewGitOperations(ctx, r.Client, gitAuthProvider, repositoryRef, &argoCDCommitStatus, targetBranch)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to initialize git client: %w", err)
		}

		resolvedSha, err := gitOperation.LsRemote(ctx, targetBranch)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to ls-remote sha for branch %q: %w", targetBranch, err)
		}

		mostRecentLastTransitionTime := r.getMostRecentLastTransitionTime(appsInEnvironment)

		resolvedPhase, desc := r.calculateAggregatedPhaseAndDescription(appsInEnvironment, resolvedSha, mostRecentLastTransitionTime)

		err = r.updateAggregatedCommitStatus(ctx, argoCDCommitStatus, targetBranch, resolvedSha, resolvedPhase, desc)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	err = r.Status().Update(ctx, &argoCDCommitStatus)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ArgoCDCommitStatus status: %w", err)
	}

	requeueDuration, err := r.SettingsMgr.GetArgoCDCommitStatusRequeueDuration(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get ArgoCDCommitStatus requeue duration: %w", err)
	}

	return ctrl.Result{RequeueAfter: requeueDuration}, nil // Timer for now :(
}

// groupArgoCDApplicationsWithPhase returns a map. The key is a branch name. The value is a list of apps configured for that target branch, along with the commit status for that one app.
// As a side-effect, this function updates argoCDCommitStatus to represent the aggregate status
// of all matching apps.
func (r *ArgoCDCommitStatusReconciler) groupArgoCDApplicationsWithPhase(argoCDCommitStatus *promoterv1alpha1.ArgoCDCommitStatus, ulAppList unstructured.UnstructuredList) (map[string][]*aggregate, error) {
	aggregates := map[string][]*aggregate{}
	argoCDCommitStatus.Status.ApplicationsSelected = []promoterv1alpha1.ApplicationsSelected{}
	repo := ""

	for _, ulApp := range ulAppList.Items {
		var application argocd.ArgoCDApplication
		err := runtime.DefaultUnstructuredConverter.FromUnstructured(ulApp.Object, &application)
		if err != nil {
			return map[string][]*aggregate{}, fmt.Errorf("failed to cast unstructured object to typed object: %w", err)
		}

		if application.Spec.SourceHydrator == nil {
			return map[string][]*aggregate{}, fmt.Errorf("application %s/%s does not have a SourceHydrator configured", application.GetNamespace(), application.GetName())
		}

		// Check that all the applications are configured with the same repo
		if repo == "" {
			repo = application.Spec.SourceHydrator.DrySource.RepoURL
		} else if repo != application.Spec.SourceHydrator.DrySource.RepoURL {
			return map[string][]*aggregate{}, fmt.Errorf("all applications must have the same repo configured")
		}

		aggregateItem := &aggregate{
			application: &application,
		}

		phase := promoterv1alpha1.CommitPhasePending
		if application.Status.Health.Status == argocd.HealthStatusHealthy && application.Status.Sync.Status == argocd.SyncStatusCodeSynced {
			phase = promoterv1alpha1.CommitPhaseSuccess
		} else if application.Status.Health.Status == argocd.HealthStatusDegraded {
			phase = promoterv1alpha1.CommitPhaseFailure
		}

		// This is an in memory version of the desired CommitStatus for a single application, this will be used to figure out
		// the aggregated phase of all applications for a particular environment
		aggregateItem.commitStatus = &promoterv1alpha1.CommitStatus{
			Spec: promoterv1alpha1.CommitStatusSpec{
				Sha:   application.Status.Sync.Revision,
				Phase: phase,
			},
		}
		argoCDCommitStatus.Status.ApplicationsSelected = append(argoCDCommitStatus.Status.ApplicationsSelected, promoterv1alpha1.ApplicationsSelected{
			Namespace:          application.GetNamespace(),
			Name:               application.GetName(),
			Phase:              phase,
			Sha:                application.Status.Sync.Revision,
			LastTransitionTime: application.Status.Health.LastTransitionTime,
		})

		aggregates[application.Spec.SourceHydrator.SyncSource.TargetBranch] = append(aggregates[application.Spec.SourceHydrator.SyncSource.TargetBranch], aggregateItem)
	}

	return aggregates, nil
}

func (r *ArgoCDCommitStatusReconciler) calculateAggregatedPhaseAndDescription(appsInEnvironment []*aggregate, resolvedSha string, mostRecentLastTransitionTime *metav1.Time) (promoterv1alpha1.CommitStatusPhase, string) {
	var desc string
	resolvedPhase := promoterv1alpha1.CommitPhasePending
	pending := 0
	healthy := 0
	degraded := 0
	for _, s := range appsInEnvironment {
		if s.commitStatus.Spec.Sha != resolvedSha {
			pending++
		} else if s.commitStatus.Spec.Phase == promoterv1alpha1.CommitPhaseSuccess {
			healthy++
		} else if s.commitStatus.Spec.Phase == promoterv1alpha1.CommitPhaseFailure {
			degraded++
		} else {
			pending++
		}
	}

	// Resolve state
	if healthy == len(appsInEnvironment) {
		resolvedPhase = promoterv1alpha1.CommitPhaseSuccess
		desc = fmt.Sprintf("%d/%d apps are healthy", healthy, len(appsInEnvironment))
	} else if degraded == len(appsInEnvironment) {
		resolvedPhase = promoterv1alpha1.CommitPhaseFailure
		desc = fmt.Sprintf("%d/%d apps are degraded", degraded, len(appsInEnvironment))
	} else {
		desc = fmt.Sprintf("Waiting for apps to be healthy (%d healthy, %d degraded, %d pending)", healthy, degraded, pending)
	}

	// Don't consider the aggregate status healthy until 5s after the most recent transition.
	// This helps avoid prematurely accepting a transitive healthy state.
	if mostRecentLastTransitionTime != nil && time.Since(mostRecentLastTransitionTime.Time) < 5*time.Second {
		return promoterv1alpha1.CommitPhasePending, desc
	}

	return resolvedPhase, desc
}

func (r *ArgoCDCommitStatusReconciler) getMostRecentLastTransitionTime(aggregateItem []*aggregate) *metav1.Time {
	var mostRecentLastTransitionTime *metav1.Time
	for _, s := range aggregateItem {
		// Find the most recent last transition time
		if s.application.Status.Health.LastTransitionTime != nil &&
			(mostRecentLastTransitionTime == nil || s.application.Status.Health.LastTransitionTime.After(mostRecentLastTransitionTime.Time)) {
			mostRecentLastTransitionTime = s.application.Status.Health.LastTransitionTime
		}
	}
	return mostRecentLastTransitionTime
}

// func lookupArgoCDCommitStatusFromArgoCDApplication(c client.Client) func(ctx context.Context, argoCDApplication client.Object) []reconcile.Request {
//	return func(ctx context.Context, argoCDApplication client.Object) []reconcile.Request {
//		var un unstructured.Unstructured
//		un.SetGroupVersionKind(gvk)
//
//		err := c.Get(ctx, client.ObjectKey{Namespace: argoCDApplication.GetName(), Name: argoCDApplication.GetName()}, &un, &client.GetOptions{})
//		if err != nil {
//			return []reconcile.Request{}
//		}
//
//		var argoCDCommitStatus promoterv1alpha1.ArgoCDCommitStatusList
//		err = c.List(ctx, &argoCDCommitStatus, &client.ListOptions{
//			FieldSelector: fields.SelectorFromSet(map[string]string{
//				".spec.l": "",
//			}),
//		})
//		if err != nil {
//			return []reconcile.Request{}
//		}
//
//		return []reconcile.Request{}
//	}
//}

// SetupWithManager sets up the controller with the Manager.
func (r *ArgoCDCommitStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	var ul unstructured.Unstructured
	ul.SetGroupVersionKind(gvk)

	// This index gets used by the CommitStatus controller and the webhook server to find the ChangeTransferPolicy to trigger reconcile
	// if err := mgr.GetFieldIndexer().IndexField(context.Background(), &ul, ".status.applications", func(rawObj client.Object) []string {
	//	//nolint:forcetypeassert
	//	ctp := rawObj.(*promoterv1alpha1.ChangeTransferPolicy)
	//	return []string{ctp.Status.Proposed.Hydrated.Sha}
	// }); err != nil {
	//	return fmt.Errorf("failed to set field index for .status.proposed.hydrated.sha: %w", err)
	//}

	err := ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.ArgoCDCommitStatus{}).
		// Watches(&ul, handler.TypedEnqueueRequestsFromMapFunc(lookupArgoCDCommitStatusFromArgoCDApplication(r.Client))).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}
	return nil
}

func (r *ArgoCDCommitStatusReconciler) updateAggregatedCommitStatus(ctx context.Context, argoCDCommitStatus promoterv1alpha1.ArgoCDCommitStatus, targetBranch string, sha string, phase promoterv1alpha1.CommitStatusPhase, desc string) error {
	logger := log.FromContext(ctx)

	commitStatusName := targetBranch + "/health"
	resourceName := strings.ReplaceAll(commitStatusName, "/", "-") + "-" + hash([]byte(argoCDCommitStatus.Name))

	promotionStrategy := promoterv1alpha1.PromotionStrategy{}
	err := r.Get(ctx, client.ObjectKey{Namespace: argoCDCommitStatus.Namespace, Name: argoCDCommitStatus.Spec.PromotionStrategyRef.Name}, &promotionStrategy, &client.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get PromotionStrategy object: %w", err)
	}

	kind := reflect.TypeOf(promoterv1alpha1.ArgoCDCommitStatus{}).Name()
	gvk := promoterv1alpha1.GroupVersion.WithKind(kind)
	controllerRef := metav1.NewControllerRef(&argoCDCommitStatus, gvk)

	desiredCommitStatus := promoterv1alpha1.CommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: argoCDCommitStatus.Namespace, // Applications could come from multiple namespaces have to put this somewhere and avoid collisions
			Labels: map[string]string{
				promoterv1alpha1.CommitStatusLabel: "argocd-health",
			},
			OwnerReferences: []metav1.OwnerReference{*controllerRef},
		},
		Spec: promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: promotionStrategy.Spec.RepositoryReference,
			Sha:                 sha,
			Name:                commitStatusName,
			Description:         desc,
			Phase:               phase,
			// Url:                 "https://example.com",
		},
	}

	currentCommitStatus := promoterv1alpha1.CommitStatus{}
	err = r.Get(ctx, client.ObjectKey{Namespace: argoCDCommitStatus.Namespace, Name: resourceName}, &currentCommitStatus)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get CommitStatus object: %w", err)
		}
		// Create
		err = r.Create(ctx, &desiredCommitStatus)
		logger.Info("Created ArgoCDCommitStatus", "name", desiredCommitStatus.Name)
		if err != nil {
			return fmt.Errorf("failed to create CommitStatus object: %w", err)
		}
	} else {
		// Update
		currentCommitStatus.Spec = desiredCommitStatus.Spec
		err = r.Update(ctx, &currentCommitStatus)
		logger.Info("Updated ArgoCDCommitStatus", "name", desiredCommitStatus.Name, "sha", sha, "phase", phase, "desc", desc)
		if err != nil {
			return fmt.Errorf("failed to update CommitStatus object: %w", err)
		}
	}

	return nil
}

func (r *ArgoCDCommitStatusReconciler) getPromotionStrategy(ctx context.Context, namespace string, promotionStrategyRef promoterv1alpha1.ObjectReference) (*promoterv1alpha1.PromotionStrategy, error) {
	promotionStrategy := promoterv1alpha1.PromotionStrategy{}
	err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: promotionStrategyRef.Name}, &promotionStrategy, &client.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get PromotionStrategy object: %w", err)
	}
	return &promotionStrategy, nil
}

func (r *ArgoCDCommitStatusReconciler) getGitAuthProvider(ctx context.Context, argoCDCommitStatus promoterv1alpha1.ArgoCDCommitStatus) (scms.GitOperationsProvider, promoterv1alpha1.ObjectReference, error) {
	logger := log.FromContext(ctx)

	ps, err := r.getPromotionStrategy(ctx, argoCDCommitStatus.GetNamespace(), argoCDCommitStatus.Spec.PromotionStrategyRef)
	if ps == nil {
		return nil, promoterv1alpha1.ObjectReference{}, fmt.Errorf("PromotionStrategy is nil for ArgoCDCommitStatus %s", argoCDCommitStatus.Name)
	}
	if err != nil {
		return nil, ps.Spec.RepositoryReference, fmt.Errorf("failed to get PromotionStrategy from ArgoCDCommitStatus %s: %w", argoCDCommitStatus.Name, err)
	}

	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, ps.Spec.RepositoryReference, ps)
	if err != nil {
		return nil, ps.Spec.RepositoryReference, fmt.Errorf("failed to get ScmProvider and secret for PromotionStrategy %q: %w", ps.Name, err)
	}

	switch {
	case scmProvider.Spec.Fake != nil:
		logger.V(4).Info("Creating fake git authentication provider")
		return fake.NewFakeGitAuthenticationProvider(scmProvider, secret), ps.Spec.RepositoryReference, nil
	case scmProvider.Spec.GitHub != nil:
		logger.V(4).Info("Creating GitHub git authentication provider")
		return github.NewGithubGitAuthenticationProvider(scmProvider, secret), ps.Spec.RepositoryReference, nil
	case scmProvider.Spec.GitLab != nil:
		logger.V(4).Info("Creating GitLab git authentication provider")
		gitlabClient, err := gitlab.NewGitlabGitAuthenticationProvider(scmProvider, secret)
		if err != nil {
			return nil, ps.Spec.RepositoryReference, fmt.Errorf("failed to create GitLab client: %w", err)
		}
		return gitlabClient, ps.Spec.RepositoryReference, nil
	default:
		return nil, ps.Spec.RepositoryReference, fmt.Errorf("no supported git authentication provider found")
	}
}

func hash(data []byte) string {
	return strconv.FormatUint(xxhash.Sum64(data), 8)
}
