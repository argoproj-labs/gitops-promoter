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

	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	v1 "k8s.io/api/core/v1"

	"github.com/argoproj-labs/gitops-promoter/internal/types/argocd"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/cespare/xxhash/v2"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var gvk = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "Application",
}

type Aggregate struct {
	application         *argocd.ArgoCDApplication
	commitStatus        *promoterv1alpha1.CommitStatus
	selectedApplication *promoterv1alpha1.SelectedApplications
}

type objKey struct {
	repo         string
	targetBranch string
}

// ArgoCDCommitStatusReconciler reconciles a ArgoCDCommitStatus object
type ArgoCDCommitStatusReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	PathLookup utils.PathLookup
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

	aggregates := map[objKey][]*Aggregate{}

	ls, err := metav1.LabelSelectorAsSelector(argoCDCommitStatus.Spec.ApplicationSelector)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to parse label selector: %w", err)
	}
	// TODO: we should setup a field index and only list apps related to the currently reconciled app
	var ul unstructured.UnstructuredList
	ul.SetGroupVersionKind(gvk)
	err = r.Client.List(ctx, &ul, &client.ListOptions{
		LabelSelector: ls,
	})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list CommitStatus objects: %w", err)
	}

	ps, err := r.getPromotionStrategy(ctx, argoCDCommitStatus.GetNamespace(), argoCDCommitStatus.Spec.PromotionStrategyRef)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get PromotionStrategy %s: %w", argoCDCommitStatus.Spec.PromotionStrategyRef, err)
	}

	scmProvider, secret, err := utils.GetScmProviderAndSecretFromRepositoryReference(ctx, r.Client, ps.Spec.RepositoryReference, ps)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get ScmProvider and secret for ArgoCDCommitStatus %q: %w", argoCDCommitStatus.Name, err)
	}

	gitAuthProvider, err := r.getGitAuthProvider(ctx, scmProvider, secret)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get git auth provider for ScmProvider %q: %w", scmProvider.Name, err)
	}

	argoCDCommitStatus.Status.ApplicationsSelected = []promoterv1alpha1.SelectedApplications{}
	for _, obj := range ul.Items {
		var application argocd.ArgoCDApplication
		err = runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &application)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to cast unstructured object to typed object: %w", err)
		}

		if application.Spec.SourceHydrator == nil {
			continue
		}

		aggregateItem := &Aggregate{
			application: &application,
		}

		key := objKey{
			repo:         strings.TrimRight(application.Spec.SourceHydrator.DrySource.RepoURL, ".git"),
			targetBranch: application.Spec.SourceHydrator.SyncSource.TargetBranch,
		}

		state := promoterv1alpha1.CommitPhasePending
		if application.Status.Health.Status == argocd.HealthStatusHealthy && application.Status.Sync.Status == argocd.SyncStatusCodeSynced {
			state = promoterv1alpha1.CommitPhaseSuccess
		} else if application.Status.Health.Status == argocd.HealthStatusDegraded {
			state = promoterv1alpha1.CommitPhaseFailure
		}

		// This is an in memory version of the desired CommitStatus for a single application, this will be used to figure out
		// the aggregated state of all applications for a particular environment
		aggregateItem.commitStatus = &promoterv1alpha1.CommitStatus{
			Spec: promoterv1alpha1.CommitStatusSpec{
				Sha:   application.Status.Sync.Revision,
				Phase: state,
			},
		}
		aggregateItem.selectedApplication = &promoterv1alpha1.SelectedApplications{}
		aggregateItem.selectedApplication.Sate = state
		aggregateItem.selectedApplication.Sha = application.Status.Sync.Revision
		aggregateItem.selectedApplication.Name = application.GetName()
		aggregateItem.selectedApplication.Namespace = application.GetNamespace()

		argoCDCommitStatus.Status.ApplicationsSelected = append(argoCDCommitStatus.Status.ApplicationsSelected, promoterv1alpha1.SelectedApplications{
			Namespace: application.GetNamespace(),
			Name:      application.GetName(),
			Sate:      state,
			Sha:       application.Status.Sync.Revision,
		})

		aggregates[key] = append(aggregates[key], aggregateItem)
	}

	for key, aggregateItem := range aggregates {
		var resolvedSha string
		repo, targetBranch := key.repo, key.targetBranch

		gitOperation, err := git.NewGitOperations(ctx, r.Client, gitAuthProvider, r.PathLookup, ps.Spec.RepositoryReference, &argoCDCommitStatus, targetBranch)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to initialize git client: %w", err)
		}

		resolvedSha, err = gitOperation.LsRemote(ctx, targetBranch)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to ls-remote sha: %w", err)
		}

		var desc string
		resolvedState := promoterv1alpha1.CommitPhasePending
		pending := 0
		healthy := 0
		degraded := 0
		for _, s := range aggregateItem {
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
		if healthy == len(aggregateItem) {
			resolvedState = promoterv1alpha1.CommitPhaseSuccess
			desc = fmt.Sprintf("%d/%d apps are healthy", healthy, len(aggregateItem))
		} else if degraded == len(aggregateItem) {
			resolvedState = promoterv1alpha1.CommitPhaseFailure
			desc = fmt.Sprintf("%d/%d apps are degraded", healthy, len(aggregateItem))
		} else {
			desc = fmt.Sprintf("Waiting for apps to be healthy (%d/%d healthy, %d/%d degraded, %d/%d pending)", healthy, len(aggregateItem), degraded, len(aggregateItem), pending, len(aggregateItem))
		}

		err = r.updateAggregatedCommitStatus(ctx, argoCDCommitStatus, targetBranch, repo, resolvedSha, resolvedState, desc)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	err = r.Status().Update(ctx, &argoCDCommitStatus)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update ArgoCDCommitStatus status: %w", err)
	}

	return ctrl.Result{RequeueAfter: 1 * time.Minute}, nil // Timer for now :(
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

func (r *ArgoCDCommitStatusReconciler) updateAggregatedCommitStatus(ctx context.Context, argoCDCommitStatus promoterv1alpha1.ArgoCDCommitStatus, revision string, repo string, sha string, state promoterv1alpha1.CommitStatusPhase, desc string) error {
	commitStatusName := revision + "/health"
	resourceName := strings.ReplaceAll(commitStatusName, "/", "-") + "-" + hash([]byte(repo))

	promotionStrategy := promoterv1alpha1.PromotionStrategy{}
	err := r.Client.Get(ctx, client.ObjectKey{Namespace: argoCDCommitStatus.Namespace, Name: argoCDCommitStatus.Spec.PromotionStrategyRef.Name}, &promotionStrategy, &client.GetOptions{})
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
				promoterv1alpha1.CommitStatusLabel: "healthy",
			},
			OwnerReferences: []metav1.OwnerReference{*controllerRef},
		},
		Spec: promoterv1alpha1.CommitStatusSpec{
			RepositoryReference: promotionStrategy.Spec.RepositoryReference,
			Sha:                 sha,
			Name:                commitStatusName,
			Description:         desc,
			Phase:               state,
			Url:                 "https://example.com",
		},
	}

	currentCommitStatus := promoterv1alpha1.CommitStatus{}
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: argoCDCommitStatus.Namespace, Name: resourceName}, &currentCommitStatus)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get CommitStatus object: %w", err)
		}
		// Create
		err = r.Client.Create(ctx, &desiredCommitStatus)
		if err != nil {
			return fmt.Errorf("failed to create CommitStatus object: %w", err)
		}
		currentCommitStatus = desiredCommitStatus
	} else {
		// Update
		currentCommitStatus.Spec = desiredCommitStatus.Spec
		err = r.Client.Update(ctx, &currentCommitStatus)
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

func (r *ArgoCDCommitStatusReconciler) getGitAuthProvider(ctx context.Context, scmProvider *promoterv1alpha1.ScmProvider, secret *v1.Secret) (scms.GitOperationsProvider, error) {
	logger := log.FromContext(ctx)
	switch {
	case scmProvider.Spec.Fake != nil:
		logger.V(4).Info("Creating fake git authentication provider")
		return fake.NewFakeGitAuthenticationProvider(scmProvider, secret), nil
	case scmProvider.Spec.GitHub != nil:
		logger.V(4).Info("Creating GitHub git authentication provider")
		return github.NewGithubGitAuthenticationProvider(scmProvider, secret), nil
	default:
		return nil, fmt.Errorf("no supported git authentication provider found")
	}
}

func hash(data []byte) string {
	return strconv.FormatUint(xxhash.Sum64(data), 8)
}
