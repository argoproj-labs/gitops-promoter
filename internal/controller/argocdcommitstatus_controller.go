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
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/apiextensions-apiserver/pkg/apis/apiextensions"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/multicluster-runtime/pkg/controller"

	"sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/cespare/xxhash/v2"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/forgejo"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitlab"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	mcbuilder "sigs.k8s.io/multicluster-runtime/pkg/builder"
	mchandler "sigs.k8s.io/multicluster-runtime/pkg/handler"
	mcmanager "sigs.k8s.io/multicluster-runtime/pkg/manager"
	mcreconcile "sigs.k8s.io/multicluster-runtime/pkg/reconcile"
	"sigs.k8s.io/multicluster-runtime/providers/kubeconfig"

	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// lastTransitionTimeThreshold is the threshold for the last transition time to consider an application healthy.
// This helps avoid premature acceptance of a transitive healthy state.
const lastTransitionTimeThreshold = 5 * time.Second

// var syncMap sync.Map
var (
	rwMutex sync.RWMutex
	revMap  = make(map[appRevisionKey]string)
)

type aggregate struct {
	application  *argocd.Application
	commitStatus *promoterv1alpha1.CommitStatus
}

type appRevisionKey struct {
	clusterName string
	namespace   string
	name        string
}

// ArgoCDCommitStatusReconciler reconciles a ArgoCDCommitStatus object
type ArgoCDCommitStatusReconciler struct {
	Manager            mcmanager.Manager
	Recorder           record.EventRecorder
	SettingsMgr        *settings.Manager
	KubeConfigProvider *kubeconfig.Provider
	localClient        client.Client
}

// URLTemplateData is the data passed to the URLTemplate in the ArgoCDCommitStatus.
type URLTemplateData struct {
	Environment        string
	ArgoCDCommitStatus promoterv1alpha1.ArgoCDCommitStatus
}

// ApplicationsInEnvironment is a list of applications in an environment.
type ApplicationsInEnvironment struct {
	ClusterName string
	argocd.ApplicationList
}

// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=argocdcommitstatuses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=argocdcommitstatuses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=promoter.argoproj.io,resources=argocdcommitstatuses/finalizers,verbs=update
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ArgoCDCommitStatus object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/reconcile
func (r *ArgoCDCommitStatusReconciler) Reconcile(ctx context.Context, req mcreconcile.Request) (result ctrl.Result, err error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ArgoCDCommitStatus", "cluster", req.ClusterName, "namespace", req.Namespace, "name", req.Name)
	startTime := time.Now()
	var argoCDCommitStatus promoterv1alpha1.ArgoCDCommitStatus

	defer utils.HandleReconciliationResult(ctx, startTime, &argoCDCommitStatus, r.localClient, r.Recorder, &err)

	err = r.localClient.Get(ctx, req.NamespacedName, &argoCDCommitStatus, &client.GetOptions{})
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			logger.Info("ArgoCDCommitStatus not found")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "failed to get ArgoCDCommitStatus")
		return ctrl.Result{}, fmt.Errorf("failed to get ArgoCDCommitStatus: %w", err)
	}

	// Remove any existing Ready condition. We want to start fresh.
	meta.RemoveStatusCondition(argoCDCommitStatus.GetConditions(), string(promoterConditions.Ready))

	ls, err := metav1.LabelSelectorAsSelector(argoCDCommitStatus.Spec.ApplicationSelector)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to parse label selector: %w", err)
	}
	// TODO: we should setup a field index and only list apps related to the currently reconciled app
	apps := []ApplicationsInEnvironment{}

	// list clusters so we can query argocd applications from all clusters
	clusters := r.KubeConfigProvider.ListClusters()
	clusters = append(clusters, "") // add the local cluster
	for _, clusterName := range clusters {
		logger.Info("Fetching Argo CD applications from cluster", "cluster", clusterName)
		cluster, err := r.Manager.GetCluster(ctx, clusterName)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to get cluster: %w", err)
		}
		clusterClient := cluster.GetClient()

		clusterArgoCDApps := argocd.ApplicationList{}
		err = clusterClient.List(ctx, &clusterArgoCDApps, &client.ListOptions{
			LabelSelector: ls,
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to list ArgoCDApplications: %w", err)
		}

		apps = append(apps, ApplicationsInEnvironment{
			ApplicationList: clusterArgoCDApps,
			ClusterName:     clusterName,
		})
	}

	appCount := 0
	for _, clusterApps := range apps {
		appCount += len(clusterApps.Items)
	}
	logger.V(4).Info("Found Applications", "appCount", appCount)

	promotionStrategy := promoterv1alpha1.PromotionStrategy{}
	err = r.localClient.Get(ctx, client.ObjectKey{Namespace: argoCDCommitStatus.Namespace, Name: argoCDCommitStatus.Spec.PromotionStrategyRef.Name}, &promotionStrategy)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy object: %w", err)
	}

	groupedArgoCDApps, err := r.groupArgoCDApplicationsWithPhase(&promotionStrategy, &argoCDCommitStatus, apps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get Application: %w", err)
	}

	resolvedShas, err := r.getHeadShasForBranches(ctx, argoCDCommitStatus, slices.Collect(maps.Keys(groupedArgoCDApps)))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get head shas for target branches: %w", err)
	}

	maxTimeUntilThreshold := time.Duration(0)
	commitStatuses := make([]*promoterv1alpha1.CommitStatus, len(groupedArgoCDApps))
	var i int
	for targetBranch, appsInEnvironment := range groupedArgoCDApps {
		resolvedSha, ok := resolvedShas[targetBranch]
		if !ok {
			return ctrl.Result{}, fmt.Errorf("failed to resolve target branch %q: %w", targetBranch, err)
		}
		resolvedPhase, desc := r.calculateAggregatedPhaseAndDescription(appsInEnvironment, resolvedSha)
		resolvedPhase, maxTimeUntilThreshold = getRequeueTimeAndPhase(appsInEnvironment, resolvedPhase, maxTimeUntilThreshold)

		cs, err := r.updateAggregatedCommitStatus(ctx, &promotionStrategy, argoCDCommitStatus, targetBranch, resolvedSha, resolvedPhase, desc)
		if err != nil {
			return ctrl.Result{}, err
		}
		commitStatuses[i] = cs
		i++
	}

	utils.InheritNotReadyConditionFromObjects(&argoCDCommitStatus, promoterConditions.CommitStatusesNotReady, commitStatuses...)

	err = r.localClient.Status().Update(ctx, &argoCDCommitStatus)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ArgoCDCommitStatus status: %w", err)
	}

	requeueDuration, err := settings.GetRequeueDuration[promoterv1alpha1.ArgoCDCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get ArgoCDCommitStatus requeue duration: %w", err)
	}

	if maxTimeUntilThreshold > 0 && maxTimeUntilThreshold < requeueDuration {
		logger.V(4).Info("Requeueing for last transition time", "requeueIn", maxTimeUntilThreshold)
		requeueDuration = maxTimeUntilThreshold
	}

	return ctrl.Result{RequeueAfter: requeueDuration}, nil // Timer for now :(
}

// getRequeueTimeAndPhase determines the phase and requeue time based on the most recent last transition time of the
// given applications. The current resolved phase and maxTimeUntilThreshold are taken as parameters to that they can be
// returned unmodified if nothing needs to be changed.
//
// If none of the apps has a last transition time, the given phase and maxTimeUntilThreshold are returned unmodified.
//
// If the most recent last transition time is older than the threshold, the given phase and maxTimeUntilThreshold are
// returned unmodified.
//
// If the most recent last transition time is within the threshold, the phase is set to Pending, and the requeue time is
// set to the time remaining until the threshold is met for all the given applications or the current
// maxTimeUntilThreshold, whichever is greater.
func getRequeueTimeAndPhase(appsInEnvironment []*aggregate, resolvedPhase promoterv1alpha1.CommitStatusPhase, maxTimeUntilThreshold time.Duration) (promoterv1alpha1.CommitStatusPhase, time.Duration) {
	mostRecentLastTransitionTime := getMostRecentLastTransitionTime(appsInEnvironment)

	if mostRecentLastTransitionTime == nil {
		// If we don't have any information about the most recent last transition time, don't modify the phase or max
		// time until threshold.
		return resolvedPhase, maxTimeUntilThreshold
	}

	// metav1.Time is marshaled to second-level precision. Any nanoseconds are lost. So we set the last
	// transition time to the latest possible time that it could have been before it was truncated. That time is
	// the rounded time plus a second minus a nanosecond. Anything more than that would have been truncated to
	// the next whole second.
	timeSinceLastTransition := time.Since(mostRecentLastTransitionTime.Add(time.Second - time.Nanosecond))
	if timeSinceLastTransition >= lastTransitionTimeThreshold {
		return resolvedPhase, maxTimeUntilThreshold
	}

	// Don't consider the aggregate status healthy until 5s after the most recent transition.
	// This helps avoid prematurely accepting a transitive healthy state.
	resolvedPhase = promoterv1alpha1.CommitPhasePending

	timeUntilThreshold := lastTransitionTimeThreshold - timeSinceLastTransition
	if timeUntilThreshold > maxTimeUntilThreshold {
		// We take the higher of the requeue times so that the next reconcile is after all transition times
		// meet the threshold.
		maxTimeUntilThreshold = timeUntilThreshold
	}

	return resolvedPhase, maxTimeUntilThreshold
}

// getHeadShasForBranches returns a map. The key is a branch name. The value is the resolved head sha for that branch.
func (r *ArgoCDCommitStatusReconciler) getHeadShasForBranches(ctx context.Context, argoCDCommitStatus promoterv1alpha1.ArgoCDCommitStatus, targetBranches []string) (map[string]string, error) {
	gitAuthProvider, repositoryRef, err := r.getGitAuthProvider(ctx, argoCDCommitStatus)
	if err != nil {
		return nil, fmt.Errorf("failed to get git auth provider: %w", err)
	}

	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, r.localClient, client.ObjectKey{Namespace: argoCDCommitStatus.GetNamespace(), Name: repositoryRef.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	headShasByTargetBranch, err := git.LsRemote(ctx, gitAuthProvider, gitRepo, targetBranches...)
	if err != nil {
		return nil, fmt.Errorf("failed to ls-remote sha for branch %q: %w", targetBranches, err)
	}

	return headShasByTargetBranch, nil
}

// groupArgoCDApplicationsWithPhase returns a map. The key is a branch name. The value is a list of apps configured for that target branch, along with the commit status for that one app.
// As a side-effect, this function updates argoCDCommitStatus to represent the aggregate status
// of all matching apps.
func (r *ArgoCDCommitStatusReconciler) groupArgoCDApplicationsWithPhase(promotionStrategy *promoterv1alpha1.PromotionStrategy, argoCDCommitStatus *promoterv1alpha1.ArgoCDCommitStatus, apps []ApplicationsInEnvironment) (map[string][]*aggregate, error) {
	aggregates := map[string][]*aggregate{}
	argoCDCommitStatus.Status.ApplicationsSelected = []promoterv1alpha1.ApplicationsSelected{}
	repo := ""

	for _, clusterApps := range apps {
		for _, application := range clusterApps.Items {
			if application.Spec.SourceHydrator == nil {
				return map[string][]*aggregate{}, fmt.Errorf("application %s/%s does not have a SourceHydrator configured", application.GetNamespace(), application.GetName())
			}

			// Check that all the applications are configured with the same repo
			if repo == "" {
				repo = application.Spec.SourceHydrator.DrySource.RepoURL
			} else if repo != application.Spec.SourceHydrator.DrySource.RepoURL {
				return map[string][]*aggregate{}, errors.New("all applications must have the same repo configured")
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
				Environment:        application.Spec.SourceHydrator.SyncSource.TargetBranch,
				ClusterName:        clusterApps.ClusterName,
			})

			aggregates[application.Spec.SourceHydrator.SyncSource.TargetBranch] = append(aggregates[application.Spec.SourceHydrator.SyncSource.TargetBranch], aggregateItem)
		}
	}

	sortApplicationsSelected(promotionStrategy, argoCDCommitStatus)

	return aggregates, nil
}

// sortApplicationsSelected sorts the applications by environment (matching PromotionStrategy order), then namespace,
// then name.
func sortApplicationsSelected(promotionStrategy *promoterv1alpha1.PromotionStrategy, argoCDCommitStatus *promoterv1alpha1.ArgoCDCommitStatus) {
	slices.SortFunc(argoCDCommitStatus.Status.ApplicationsSelected, func(a promoterv1alpha1.ApplicationsSelected, b promoterv1alpha1.ApplicationsSelected) int {
		if a.Environment != b.Environment {
			aIdx, _ := utils.GetEnvironmentByBranch(*promotionStrategy, a.Environment)
			bIdx, _ := utils.GetEnvironmentByBranch(*promotionStrategy, b.Environment)

			// These shouldn't be equal, but it's technically possible, for example if the environment isn't found in
			// the promotion strategy.
			if aIdx != bIdx {
				// If a comes before b, then b will be greater than a. aIdx - bIdx will be negative. So this matches the
				// SortFunc requirement that we return a negative number if a < b.
				return aIdx - bIdx
			}
		}
		if a.Namespace != b.Namespace {
			return strings.Compare(a.Namespace, b.Namespace)
		}
		return strings.Compare(a.Name, b.Name)
	})
}

func (r *ArgoCDCommitStatusReconciler) calculateAggregatedPhaseAndDescription(appsInEnvironment []*aggregate, resolvedSha string) (promoterv1alpha1.CommitStatusPhase, string) {
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

	return resolvedPhase, desc
}

func getMostRecentLastTransitionTime(aggregateItem []*aggregate) *metav1.Time {
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

func lookupArgoCDCommitStatusFromArgoCDApplication(mgr mcmanager.Manager) mchandler.TypedEventHandlerFunc[client.Object, mcreconcile.Request] {
	return func(clusterName string, cl cluster.Cluster) handler.TypedEventHandler[client.Object, mcreconcile.Request] {
		return handler.TypedEnqueueRequestsFromMapFunc(func(ctx context.Context, argoCDApplication client.Object) []mcreconcile.Request {
			logger := log.FromContext(ctx)

			application := &argocd.Application{}

			// fetch the ArgoCDApplication from the cluster
			if err := cl.GetClient().Get(ctx, client.ObjectKeyFromObject(argoCDApplication), application, &client.GetOptions{}); err != nil {
				logger.Error(err, "failed to get ArgoCDApplication")
				return nil
			}

			// if clusterName is empty, then cluster == local cluster
			appKey := appRevisionKey{
				clusterName: clusterName,
				namespace:   application.GetNamespace(),
				name:        application.GetName(),
			}

			rwMutex.RLock()
			appRef := revMap[appKey]
			rwMutex.RUnlock()

			if appRef == application.Status.Sync.Revision && (application.Status.Health.LastTransitionTime == nil || time.Since(application.Status.Health.LastTransitionTime.Time) >= 10*time.Second) {
				// No change in-app revision, and the last transition time is more than 10 seconds ago, let's not add this to the queue
				return nil
			}

			rwMutex.Lock()
			revMap[appKey] = application.Status.Sync.Revision
			rwMutex.Unlock()

			// lookup the ArgoCDCommitStatus objects in the local cluster
			var argoCDCommitStatusList promoterv1alpha1.ArgoCDCommitStatusList
			if err := mgr.GetLocalManager().GetClient().List(ctx, &argoCDCommitStatusList, &client.ListOptions{}); err != nil {
				logger.Error(err, "failed to list ArgoCDCommitStatus objects")
				return nil
			}

			// TODO: is there some way to do this without a loop? Can we use a field indexer? The one issue with field indexers is that
			// they can not be used with lists (aka label selectors) so how else can we lookup.
			for _, argoCDCommitStatus := range argoCDCommitStatusList.Items {
				selector, err := metav1.LabelSelectorAsSelector(argoCDCommitStatus.Spec.ApplicationSelector)
				if err != nil {
					logger.Error(err, "failed to parse label selector")
				}
				if err == nil && selector.Matches(labels.Set(application.GetLabels())) {
					logger.Info("ArgoCD application caused ArgoCDCommitStatus to reconcile",
						"app-namespace", argoCDApplication.GetNamespace(), "application", argoCDApplication.GetName(),
						"argocdcommitstatus", argoCDCommitStatus.Namespace+"/"+argoCDCommitStatus.Name)

					return []mcreconcile.Request{{
						Request: reconcile.Request{
							NamespacedName: client.ObjectKeyFromObject(&argoCDCommitStatus),
						},
						ClusterName: clusterName,
					}}
				}
			}

			logger.V(6).Info("No ArgoCDCommitStatus found for ArgoCD application",
				"app-namespace", argoCDApplication.GetNamespace(), "application", argoCDApplication.GetName())
			return nil
		})
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *ArgoCDCommitStatusReconciler) SetupWithManager(ctx context.Context, mcMgr mcmanager.Manager) error {
	// Set the local client for interacting with manager cluster
	r.localClient = mcMgr.GetLocalManager().GetClient()

	// Use Direct methods to read configuration from the API server without cache during setup.
	// The cache is not started during SetupWithManager, so we must use the non-cached API reader.
	rateLimiter, err := settings.GetRateLimiterDirect[promoterv1alpha1.ArgoCDCommitStatusConfiguration, mcreconcile.Request](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get ArgoCDCommitStatus rate limiter: %w", err)
	}

	maxConcurrentReconciles, err := settings.GetMaxConcurrentReconcilesDirect[promoterv1alpha1.ArgoCDCommitStatusConfiguration](ctx, r.SettingsMgr)
	if err != nil {
		return fmt.Errorf("failed to get ArgoCDCommitStatus max concurrent reconciles: %w", err)
	}

	// Get the controller configuration to check if local Applications should be watched
	controllerConfig, err := r.SettingsMgr.GetControllerConfigurationDirect(ctx)
	if err != nil {
		return fmt.Errorf("failed to get controller configuration: %w", err)
	}

	// Determine if we should watch local Applications (default: true)
	enableLocalApplications := true
	if controllerConfig.Spec.ArgoCDCommitStatus.EnableLocalArgoCDApplications != nil {
		enableLocalApplications = *controllerConfig.Spec.ArgoCDCommitStatus.EnableLocalArgoCDApplications
	}

	// If local applications are enabled, verify the Application CRD exists in the local cluster.
	if enableLocalApplications {
		// If local applications are enabled, verify the Application CRD exists in the local cluster.
		if enableLocalApplications {
			// Use the manager's client to check for the CRD directly
			crd := &apiextensions.CustomResourceDefinition{}
			err := r.localClient.Get(ctx, client.ObjectKey{Name: "applications.argoproj.io"}, crd)
			if err != nil {
				if k8s_errors.IsNotFound(err) {
					// Return a clear error so tests and operator setup can fail-fast when CRD is missing
					return fmt.Errorf("application CRD (argoproj.io/v1alpha1 Application) is not installed in the local cluster")
				}
				return fmt.Errorf("failed to check for Application CRD: %w", err)
			}
		}
	}

	builder := mcbuilder.ControllerManagedBy(mcMgr).
		For(&promoterv1alpha1.ArgoCDCommitStatus{},
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(false),
			mcbuilder.WithPredicates(predicate.GenerationChangedPredicate{}),
		).
		WithOptions(controller.Options{MaxConcurrentReconciles: maxConcurrentReconciles, RateLimiter: rateLimiter})

	// Only watch local Applications if enabled
	if enableLocalApplications {
		builder = builder.Watches(&argocd.Application{}, lookupArgoCDCommitStatusFromArgoCDApplication(mcMgr),
			mcbuilder.WithEngageWithLocalCluster(true),
			mcbuilder.WithEngageWithProviderClusters(true),
		)
	} else {
		// Watch Applications only in remote clusters
		builder = builder.Watches(&argocd.Application{}, lookupArgoCDCommitStatusFromArgoCDApplication(mcMgr),
			mcbuilder.WithEngageWithLocalCluster(false),
			mcbuilder.WithEngageWithProviderClusters(true),
		)
	}

	err = builder.Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

// updateAggregatedCommitStatus creates or updates a CommitStatus object for the given target branch and sha.
// If err is nil, the returned CommitStatus is guaranteed to be non-nil.
func (r *ArgoCDCommitStatusReconciler) updateAggregatedCommitStatus(ctx context.Context, promotionStrategy *promoterv1alpha1.PromotionStrategy, argoCDCommitStatus promoterv1alpha1.ArgoCDCommitStatus, targetBranch string, sha string, phase promoterv1alpha1.CommitStatusPhase, desc string) (*promoterv1alpha1.CommitStatus, error) {
	logger := log.FromContext(ctx)

	commitStatusName := targetBranch + "/health"
	resourceName := strings.ReplaceAll(commitStatusName, "/", "-") + "-" + hash([]byte(argoCDCommitStatus.Name))

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
		},
	}

	// Render URL from template if it exists, but don't block commit status update if it fails
	if argoCDCommitStatus.Spec.URL.Template != "" {
		data := URLTemplateData{
			Environment:        targetBranch,
			ArgoCDCommitStatus: argoCDCommitStatus,
		}

		renderedURL, err := utils.RenderStringTemplate(argoCDCommitStatus.Spec.URL.Template, data, argoCDCommitStatus.Spec.URL.Options...)
		if err != nil {
			return nil, fmt.Errorf("failed to render URL template: %w", err)
		}

		// Parse the URL to check that it's valid
		parsedURL, err := url.Parse(renderedURL)
		if err != nil {
			return nil, fmt.Errorf("failed to parse URL: %w", err)
		}

		// Check that the URL scheme is http or https
		if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
			return nil, fmt.Errorf("URL scheme is not http or https: %s", parsedURL.Scheme)
		}

		// Set the URL in the CommitStatus
		logger.Info("Rendered URL template", "url", renderedURL, "environment", targetBranch, "commitStatus", desiredCommitStatus.Name, "namespace", desiredCommitStatus.Namespace)
		desiredCommitStatus.Spec.Url = renderedURL
	}

	currentCommitStatus := promoterv1alpha1.CommitStatus{}
	err := r.localClient.Get(ctx, client.ObjectKey{Namespace: argoCDCommitStatus.Namespace, Name: resourceName}, &currentCommitStatus)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return nil, fmt.Errorf("failed to get CommitStatus object: %w", err)
		}
		// Create
		err = r.localClient.Create(ctx, &desiredCommitStatus)
		logger.Info("Created CommitStatus", "name", desiredCommitStatus.Name, "targetBranch", targetBranch, "sha", sha, "phase", phase, "description", desc)
		if err != nil {
			return nil, fmt.Errorf("failed to create CommitStatus object: %w", err)
		}
	}

	if currentCommitStatus.Spec == desiredCommitStatus.Spec {
		logger.V(4).Info("CommitStatus is already in sync", "targetBranch", targetBranch, "sha", sha, "phase", phase, "description", desc)
		return &currentCommitStatus, nil
	}

	currentCommitStatus.Spec = desiredCommitStatus.Spec
	err = r.localClient.Update(ctx, &currentCommitStatus)
	logger.Info("Updated CommitStatus", "name", desiredCommitStatus.Name, "targetBranch", targetBranch, "sha", sha, "phase", phase, "description", desc)
	if err != nil {
		return nil, fmt.Errorf("failed to update CommitStatus object: %w", err)
	}

	return &currentCommitStatus, nil
}

func (r *ArgoCDCommitStatusReconciler) getPromotionStrategy(ctx context.Context, namespace string, promotionStrategyRef promoterv1alpha1.ObjectReference) (*promoterv1alpha1.PromotionStrategy, error) {
	promotionStrategy := promoterv1alpha1.PromotionStrategy{}
	err := r.localClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: promotionStrategyRef.Name}, &promotionStrategy)
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

	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.localClient, r.SettingsMgr.GetControllerNamespace(), ps.Spec.RepositoryReference, ps)
	if err != nil {
		return nil, ps.Spec.RepositoryReference, fmt.Errorf("failed to get ScmProvider and secret for PromotionStrategy %q: %w", ps.Name, err)
	}

	switch {
	case scmProvider.GetSpec().Fake != nil:
		logger.V(4).Info("Creating fake git authentication provider")
		return fake.NewFakeGitAuthenticationProvider(scmProvider, secret), ps.Spec.RepositoryReference, nil
	case scmProvider.GetSpec().GitHub != nil:
		logger.V(4).Info("Creating GitHub git authentication provider")
		provider, err := github.NewGithubGitAuthenticationProvider(ctx, r.localClient, scmProvider, secret, client.ObjectKey{Namespace: argoCDCommitStatus.Namespace, Name: ps.Spec.RepositoryReference.Name})
		if err != nil {
			return nil, ps.Spec.RepositoryReference, fmt.Errorf("failed to create GitHub client: %w", err)
		}
		return provider, ps.Spec.RepositoryReference, nil
	case scmProvider.GetSpec().GitLab != nil:
		logger.V(4).Info("Creating GitLab git authentication provider")
		gitlabClient, err := gitlab.NewGitlabGitAuthenticationProvider(scmProvider, secret)
		if err != nil {
			return nil, ps.Spec.RepositoryReference, fmt.Errorf("failed to create GitLab client: %w", err)
		}
		return gitlabClient, ps.Spec.RepositoryReference, nil
	case scmProvider.GetSpec().Forgejo != nil:
		logger.V(4).Info("Creating Forgejo git authentication provider")
		return forgejo.NewForgejoGitAuthenticationProvider(scmProvider, secret), ps.Spec.RepositoryReference, nil
	default:
		return nil, ps.Spec.RepositoryReference, errors.New("no supported git authentication provider found")
	}
}

func hash(data []byte) string {
	return strconv.FormatUint(xxhash.Sum64(data), 8)
}
