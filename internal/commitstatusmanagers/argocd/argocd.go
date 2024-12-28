package argocd

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/cespare/xxhash/v2"
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

type ArgoCDCommitStatusManagerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

type Aggregate struct {
	application  *ArgoCDApplication
	commitStatus *v1alpha1.CommitStatus
	changed      bool
}

type objKey struct {
	repo     string
	revision string
}

func (r *ArgoCDCommitStatusManagerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling ArgoCD Application")

	var unstructuredApplication unstructured.Unstructured
	unstructuredApplication.SetGroupVersionKind(gvk)

	err := r.Get(ctx, req.NamespacedName, &unstructuredApplication, &client.GetOptions{})
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			logger.Info("Argo CD application not found")
			return ctrl.Result{}, nil
		}

		logger.Error(err, "failed to get Argo CD application")
		return ctrl.Result{}, fmt.Errorf("failed to get Argo CD application: %w", err)
	}

	// Cast the unstructured object to the typed object
	var application ArgoCDApplication
	err = runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredApplication.Object, &application)
	if err != nil {
		logger.Error(err, "failed to cast unstructured object to typed object")
		return ctrl.Result{}, fmt.Errorf("failed to cast unstructured object to typed object: %w", err)
	}

	aggregates := map[objKey][]*Aggregate{}

	// TODO: we should setup a field index and only list apps related to the currently reconciled app
	var ul unstructured.UnstructuredList
	ul.SetGroupVersionKind(gvk)
	err = r.Client.List(ctx, &ul, &client.ListOptions{})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list CommitStatus objects: %w", err)
	}

	for _, obj := range ul.Items {
		var application ArgoCDApplication
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

		state := v1alpha1.CommitPhasePending
		if application.Status.Health.Status == HealthStatusHealthy && application.Status.Sync.Status == SyncStatusCodeSynced {
			state = v1alpha1.CommitPhaseSuccess
		} else if application.Status.Health.Status == HealthStatusDegraded {
			state = v1alpha1.CommitPhaseFailure
		}

		commitStatusName := application.Name + "/health"
		resourceName := strings.ReplaceAll(commitStatusName, "/", "-")

		desiredCommitStatus := v1alpha1.CommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      resourceName,
				Namespace: "argocd",
				Labels: map[string]string{
					v1alpha1.CommitStatusLabel: "app-healthy",
				},
			},
			Spec: v1alpha1.CommitStatusSpec{
				RepositoryReference: v1alpha1.ObjectReference{
					Name: "argocon-demo",
				},
				Sha:         application.Status.Sync.Revision,
				Name:        commitStatusName,
				Description: fmt.Sprintf("App %s is %s", application.Name, state),
				Phase:       state,
				Url:         "https://example.com",
			},
		}

		currentCommitStatus := v1alpha1.CommitStatus{}
		err = r.Client.Get(ctx, client.ObjectKey{Namespace: "argocd", Name: resourceName}, &currentCommitStatus)
		if err != nil {
			if client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, fmt.Errorf("failed to get CommitStatus object: %w", err)
			}
			// Create
			err = r.Client.Create(ctx, &desiredCommitStatus)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to create CommitStatus object: %w", err)
			}
			currentCommitStatus = desiredCommitStatus
		} else {
			// Update
			currentCommitStatus.Spec = desiredCommitStatus.Spec
			err = r.Client.Update(ctx, &currentCommitStatus)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to update CommitStatus object: %w", err)
			}
		}

		aggregateItem.commitStatus = &currentCommitStatus
		aggregateItem.changed = true // Should check if there is a no-op

		key := objKey{
			repo:     strings.TrimRight(application.Spec.SourceHydrator.DrySource.RepoURL, ".git"),
			revision: application.Spec.SourceHydrator.SyncSource.TargetBranch,
		}
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
		resolvedState := v1alpha1.CommitPhasePending
		pending := 0
		healthy := 0
		degraded := 0
		for _, s := range aggregateItem {
			if s.commitStatus.Spec.Sha != resolvedSha {
				pending++
			} else if s.commitStatus.Spec.Phase == v1alpha1.CommitPhaseSuccess {
				healthy++
			} else if s.commitStatus.Spec.Phase == v1alpha1.CommitPhaseFailure {
				degraded++
			} else {
				pending++
			}
		}

		// Resolve state
		if healthy == len(aggregateItem) {
			resolvedState = v1alpha1.CommitPhaseSuccess
			desc = fmt.Sprintf("%d/%d apps are healthy", healthy, len(aggregateItem))
		} else if degraded == len(aggregateItem) {
			resolvedState = v1alpha1.CommitPhaseFailure
			desc = fmt.Sprintf("%d/%d apps are degraded", healthy, len(aggregateItem))
		} else {
			desc = fmt.Sprintf("Waiting for apps to be healthy (%d/%d healthy, %d/%d degraded, %d/%d pending)", healthy, len(aggregateItem), degraded, len(aggregateItem), pending, len(aggregateItem))
		}

		err = updateAggregatedStatus(ctx, r.Client, revision, repo, resolvedSha, resolvedState, desc)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *ArgoCDCommitStatusManagerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	err := ctrl.NewControllerManagedBy(mgr).
		For(runtimeObjFromGVK(gvk)).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create controller: %w", err)
	}

	return nil
}

func updateAggregatedStatus(ctx context.Context, kubeClient client.Client, revision string, repo string, sha string, state v1alpha1.CommitStatusPhase, desc string) error {
	commitStatusName := revision + "/health"
	resourceName := strings.ReplaceAll(commitStatusName, "/", "-") + "-" + hash([]byte(repo))

	desiredCommitStatus := v1alpha1.CommitStatus{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: "argocd",
			Labels: map[string]string{
				v1alpha1.CommitStatusLabel: "healthy",
			},
		},
		Spec: v1alpha1.CommitStatusSpec{
			RepositoryReference: v1alpha1.ObjectReference{
				Name: "argocon-demo",
			},
			Sha:         sha,
			Name:        commitStatusName,
			Description: desc,
			Phase:       state,
			Url:         "https://example.com",
		},
	}

	currentCommitStatus := v1alpha1.CommitStatus{}
	err := kubeClient.Get(ctx, client.ObjectKey{Namespace: "argocd", Name: resourceName}, &currentCommitStatus)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return fmt.Errorf("failed to get CommitStatus object: %w", err)
		}
		// Create
		err = kubeClient.Create(ctx, &desiredCommitStatus)
		if err != nil {
			return fmt.Errorf("failed to create CommitStatus object: %w", err)
		}
		currentCommitStatus = desiredCommitStatus
	} else {
		// Update
		currentCommitStatus.Spec = desiredCommitStatus.Spec
		err = kubeClient.Update(ctx, &currentCommitStatus)
		if err != nil {
			return fmt.Errorf("failed to update CommitStatus object: %w", err)
		}
	}

	return nil
}

func hash(data []byte) string {
	return strconv.FormatUint(xxhash.Sum64(data), 8)
}

func runtimeObjFromGVK(r schema.GroupVersionKind) client.Object {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(r)
	return obj
}
