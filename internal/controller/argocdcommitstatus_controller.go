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
	"os/exec"
	"strconv"
	"strings"
	"time"

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
	application  *argocd.ArgoCDApplication
	commitStatus *promoterv1alpha1.CommitStatus
	changed      bool
}

type objKey struct {
	repo     string
	revision string
}

// ArgoCDCommitStatusReconciler reconciles a ArgoCDCommitStatus object
type ArgoCDCommitStatusReconciler struct {
	client.Client
	Scheme *runtime.Scheme
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

		logger.Error(err, "failed to get Argo CD application")
		return ctrl.Result{}, fmt.Errorf("failed to get Argo CD application: %w", err)
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
			repo:     strings.TrimRight(application.Spec.SourceHydrator.DrySource.RepoURL, ".git"),
			revision: application.Spec.SourceHydrator.SyncSource.TargetBranch,
		}

		state := promoterv1alpha1.CommitPhasePending
		if application.Status.Health.Status == argocd.HealthStatusHealthy && application.Status.Sync.Status == argocd.SyncStatusCodeSynced {
			state = promoterv1alpha1.CommitPhaseSuccess
		} else if application.Status.Health.Status == argocd.HealthStatusDegraded {
			state = promoterv1alpha1.CommitPhaseFailure
		}

		commitStatusName := application.Name + "/health"
		resourceName := strings.ReplaceAll(commitStatusName, "/", "-")

		// TODO: drop this commit status
		desiredCommitStatus := promoterv1alpha1.CommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      resourceName,
				Namespace: application.Namespace,
				Labels: map[string]string{
					promoterv1alpha1.CommitStatusLabel: "app-healthy",
				},
			},
			Spec: promoterv1alpha1.CommitStatusSpec{
				RepositoryReference: promoterv1alpha1.ObjectReference{
					Name: utils.KubeSafeUniqueName(ctx, key.repo),
				},
				Sha:         application.Status.Sync.Revision,
				Name:        commitStatusName,
				Description: fmt.Sprintf("App %s is %s", application.Name, state),
				Phase:       state,
				Url:         "https://example.com",
			},
		}

		currentCommitStatus := promoterv1alpha1.CommitStatus{}
		err = r.Client.Get(ctx, client.ObjectKey{Namespace: application.Namespace, Name: resourceName}, &currentCommitStatus)
		if err != nil {
			if client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, fmt.Errorf("failed to get CommitStatus object: %w", err)
			}
			// Create
			// err = r.Client.Create(ctx, &desiredCommitStatus)
			//if err != nil {
			//	return ctrl.Result{}, fmt.Errorf("failed to create CommitStatus object: %w", err)
			//}
			currentCommitStatus = desiredCommitStatus
		} else {
			// Update
			desiredCommitStatus.Spec.DeepCopyInto(&currentCommitStatus.Spec)
			// err = r.Client.Update(ctx, &currentCommitStatus)
			//if err != nil {
			//	return ctrl.Result{}, fmt.Errorf("failed to update CommitStatus object: %w", err)
			//}
		}

		aggregateItem.commitStatus = &currentCommitStatus
		aggregateItem.changed = true // Should check if there is a no-op

		aggregates[key] = append(aggregates[key], aggregateItem)
	}

	for key, aggregateItem := range aggregates {
		update := false
		for _, v := range aggregateItem {
			update = update || v.changed
			if update {
				break
			}
		}
		if !update {
			continue
		}

		var resolvedSha string
		var i int
		repo, revision := key.repo, key.revision
		for {
			i++
			resolveShaCmd := exec.Command("git", "ls-remote", repo, revision)
			out, err := resolveShaCmd.CombinedOutput()
			if err != nil {
				fmt.Println(string(out))
				time.Sleep(500 * time.Millisecond)
				if i <= 25 {
					continue
				} else {
					return ctrl.Result{}, fmt.Errorf("failed to resolve sha: %w", err)
				}
			}
			resolvedSha = strings.Split(string(out), "\t")[0]
			break
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

		err = r.updateAggregatedCommitStatus(ctx, argoCDCommitStatus, revision, repo, resolvedSha, resolvedState, desc)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ArgoCDCommitStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(&promoterv1alpha1.ArgoCDCommitStatus{}).
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

	desiredCommitStatus := promoterv1alpha1.CommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: argoCDCommitStatus.Namespace, // Applications could come from multiple namespaces have to put this somewhere and avoid collisions
			Labels: map[string]string{
				promoterv1alpha1.CommitStatusLabel: "healthy",
			},
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

func hash(data []byte) string {
	return strconv.FormatUint(xxhash.Sum64(data), 8)
}
