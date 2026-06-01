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

package apiserver

import (
	"context"
	"encoding/json"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	dashboardapi "github.com/argoproj-labs/gitops-promoter/api/dashboard/dashboard"
	dashboardv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/dashboard/v1alpha1"
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// lastAppliedAnnotation is stripped from every object included in a bundle.
const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// buildBundle assembles the PromotionStrategyDetails for the named PromotionStrategy
// from the given reader. It returns a NotFound error (scoped to the dashboard
// resource) when the PromotionStrategy does not exist. Secrets are never read.
//
// Embedded objects are stored as runtime.RawExtension (opaque JSON), so the served
// OpenAPI schema does not reference the promoter/core type universe. The returned
// bundle has its ResourceVersion set to the provided value.
func buildBundle(ctx context.Context, reader client.Reader, namespace, name, resourceVersion string) (*dashboardapi.PromotionStrategyDetails, error) {
	ps := &promoterv1alpha1.PromotionStrategy{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, ps); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, apierrors.NewNotFound(dashboardv1alpha1.Resource("promotionstrategydetails"), name)
		}
		return nil, fmt.Errorf("failed to get PromotionStrategy %s/%s: %w", namespace, name, err)
	}
	sanitize(ps)

	// The PromotionStrategy's status is a per-environment aggregation of the
	// ChangeTransferPolicy statuses, which are already embedded in the bundle
	// (.changeTransferPolicies). Drop it to avoid duplicating that data; consumers
	// reconstruct per-environment state from the CTPs.
	ps.Status = promoterv1alpha1.PromotionStrategyStatus{}

	psRaw, err := marshalRaw(ps)
	if err != nil {
		return nil, err
	}

	bundle := &dashboardapi.PromotionStrategyDetails{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			UID:               ps.UID,
			ResourceVersion:   resourceVersion,
			CreationTimestamp: ps.CreationTimestamp,
			Labels:            ps.Labels,
		},
		PromotionStrategy: psRaw,
	}

	// CTPs, PullRequests, base CommitStatuses are labelled with the PS name.
	psLabel := client.MatchingLabels{promoterv1alpha1.PromotionStrategyLabel: name}

	ctpList := &promoterv1alpha1.ChangeTransferPolicyList{}
	if err := reader.List(ctx, ctpList, client.InNamespace(namespace), psLabel); err != nil {
		return nil, fmt.Errorf("failed to list ChangeTransferPolicies: %w", err)
	}
	if bundle.ChangeTransferPolicies, err = rawExtensionSlice(ctpList.Items); err != nil {
		return nil, err
	}

	prList := &promoterv1alpha1.PullRequestList{}
	if err := reader.List(ctx, prList, client.InNamespace(namespace), psLabel); err != nil {
		return nil, fmt.Errorf("failed to list PullRequests: %w", err)
	}
	if bundle.PullRequests, err = rawExtensionSlice(prList.Items); err != nil {
		return nil, err
	}

	csList := &promoterv1alpha1.CommitStatusList{}
	if err := reader.List(ctx, csList, client.InNamespace(namespace), psLabel); err != nil {
		return nil, fmt.Errorf("failed to list CommitStatuses: %w", err)
	}
	if bundle.CommitStatuses, err = rawExtensionSlice(csList.Items); err != nil {
		return nil, err
	}

	// Commit-status managers reference the PS by spec.promotionStrategyRef.name.
	argocdList := &promoterv1alpha1.ArgoCDCommitStatusList{}
	if err := reader.List(ctx, argocdList, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list ArgoCDCommitStatuses: %w", err)
	}
	argocd := filterByPromotionStrategy(argocdList.Items, name, func(i *promoterv1alpha1.ArgoCDCommitStatus) string {
		return i.Spec.PromotionStrategyRef.Name
	})
	if bundle.ArgoCDCommitStatuses, err = rawExtensionSlice(argocd); err != nil {
		return nil, err
	}

	gitCSList := &promoterv1alpha1.GitCommitStatusList{}
	if err := reader.List(ctx, gitCSList, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list GitCommitStatuses: %w", err)
	}
	gitCS := filterByPromotionStrategy(gitCSList.Items, name, func(i *promoterv1alpha1.GitCommitStatus) string {
		return i.Spec.PromotionStrategyRef.Name
	})
	if bundle.GitCommitStatuses, err = rawExtensionSlice(gitCS); err != nil {
		return nil, err
	}

	timedCSList := &promoterv1alpha1.TimedCommitStatusList{}
	if err := reader.List(ctx, timedCSList, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list TimedCommitStatuses: %w", err)
	}
	timedCS := filterByPromotionStrategy(timedCSList.Items, name, func(i *promoterv1alpha1.TimedCommitStatus) string {
		return i.Spec.PromotionStrategyRef.Name
	})
	if bundle.TimedCommitStatuses, err = rawExtensionSlice(timedCS); err != nil {
		return nil, err
	}

	webReqCSList := &promoterv1alpha1.WebRequestCommitStatusList{}
	if err := reader.List(ctx, webReqCSList, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list WebRequestCommitStatuses: %w", err)
	}
	webReqCS := filterByPromotionStrategy(webReqCSList.Items, name, func(i *promoterv1alpha1.WebRequestCommitStatus) string {
		return i.Spec.PromotionStrategyRef.Name
	})
	if bundle.WebRequestCommitStatuses, err = rawExtensionSlice(webReqCS); err != nil {
		return nil, err
	}

	// Git config: GitRepository -> ScmProvider / ClusterScmProvider.
	// The credentials Secret referenced by the provider is intentionally never read.
	if err := attachGitConfig(ctx, reader, namespace, ps, bundle); err != nil {
		return nil, err
	}

	return bundle, nil
}

// attachGitConfig resolves the PromotionStrategy's GitRepository and its
// (Cluster)ScmProvider and attaches them to the bundle. Missing resources are not
// an error (the bundle simply omits them). Secrets are never read.
func attachGitConfig(ctx context.Context, reader client.Reader, namespace string, ps *promoterv1alpha1.PromotionStrategy, bundle *dashboardapi.PromotionStrategyDetails) error {
	repoName := ps.Spec.RepositoryReference.Name
	if repoName == "" {
		return nil
	}

	gitRepo := &promoterv1alpha1.GitRepository{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: repoName}, gitRepo); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get GitRepository %s/%s: %w", namespace, repoName, err)
	}

	sanitize(gitRepo)
	repoRaw, err := marshalRawPtr(gitRepo)
	if err != nil {
		return err
	}
	bundle.GitRepository = repoRaw

	return attachScmProvider(ctx, reader, namespace, gitRepo, bundle)
}

// attachScmProvider resolves the GitRepository's ScmProviderRef and attaches the
// referenced (Cluster)ScmProvider to the bundle. Secrets are never resolved.
func attachScmProvider(ctx context.Context, reader client.Reader, namespace string, gitRepo *promoterv1alpha1.GitRepository, bundle *dashboardapi.PromotionStrategyDetails) error {
	ref := gitRepo.Spec.ScmProviderRef
	switch ref.Kind {
	case "ClusterScmProvider":
		provider := &promoterv1alpha1.ClusterScmProvider{}
		if err := reader.Get(ctx, client.ObjectKey{Name: ref.Name}, provider); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to get ClusterScmProvider %s: %w", ref.Name, err)
		}
		sanitize(provider)
		raw, err := marshalRawPtr(provider)
		if err != nil {
			return err
		}
		bundle.ClusterScmProvider = raw
	default: // "ScmProvider" (also the kubebuilder default)
		provider := &promoterv1alpha1.ScmProvider{}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, provider); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to get ScmProvider %s/%s: %w", namespace, ref.Name, err)
		}
		sanitize(provider)
		raw, err := marshalRawPtr(provider)
		if err != nil {
			return err
		}
		bundle.ScmProvider = raw
	}
	return nil
}

// sanitize strips managedFields and the last-applied-configuration annotation so
// bundles stay small and free of noisy server-side-apply metadata.
func sanitize(obj client.Object) {
	obj.SetManagedFields(nil)
	annotations := obj.GetAnnotations()
	if annotations != nil {
		delete(annotations, lastAppliedAnnotation)
		obj.SetAnnotations(annotations)
	}
}

// marshalRaw JSON-encodes an object into a runtime.RawExtension.
func marshalRaw(obj any) (runtime.RawExtension, error) {
	data, err := json.Marshal(obj)
	if err != nil {
		return runtime.RawExtension{}, fmt.Errorf("failed to marshal %T for bundle: %w", obj, err)
	}
	return runtime.RawExtension{Raw: data}, nil
}

// marshalRawPtr is marshalRaw returning a pointer (for optional bundle fields).
func marshalRawPtr(obj any) (*runtime.RawExtension, error) {
	raw, err := marshalRaw(obj)
	if err != nil {
		return nil, err
	}
	return &raw, nil
}

// rawExtensionSlice sanitizes each element of a slice in place and returns them as
// RawExtensions (nil when empty).
func rawExtensionSlice[T any, PT interface {
	client.Object
	*T
}](items []T) ([]runtime.RawExtension, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]runtime.RawExtension, 0, len(items))
	for i := range items {
		sanitize(PT(&items[i]))
		raw, err := marshalRaw(PT(&items[i]))
		if err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, nil
}

// filterByPromotionStrategy returns the items whose referenced PromotionStrategy
// name matches psName.
func filterByPromotionStrategy[T any](items []T, psName string, refName func(*T) string) []T {
	var out []T
	for i := range items {
		if refName(&items[i]) == psName {
			out = append(out, items[i])
		}
	}
	return out
}
