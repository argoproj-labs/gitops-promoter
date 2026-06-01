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
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
// The returned bundle has its ResourceVersion set to the provided value.
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

	bundle := &dashboardapi.PromotionStrategyDetails{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			UID:               ps.UID,
			ResourceVersion:   resourceVersion,
			CreationTimestamp: ps.CreationTimestamp,
			Labels:            ps.Labels,
		},
		PromotionStrategy: *ps,
	}

	// CTPs, PullRequests, base CommitStatuses are labelled with the PS name.
	psLabel := client.MatchingLabels{promoterv1alpha1.PromotionStrategyLabel: name}

	ctpList := &promoterv1alpha1.ChangeTransferPolicyList{}
	if err := reader.List(ctx, ctpList, client.InNamespace(namespace), psLabel); err != nil {
		return nil, fmt.Errorf("failed to list ChangeTransferPolicies: %w", err)
	}
	bundle.ChangeTransferPolicies = sanitizeSlice(ctpList.Items)

	prList := &promoterv1alpha1.PullRequestList{}
	if err := reader.List(ctx, prList, client.InNamespace(namespace), psLabel); err != nil {
		return nil, fmt.Errorf("failed to list PullRequests: %w", err)
	}
	bundle.PullRequests = sanitizeSlice(prList.Items)

	csList := &promoterv1alpha1.CommitStatusList{}
	if err := reader.List(ctx, csList, client.InNamespace(namespace), psLabel); err != nil {
		return nil, fmt.Errorf("failed to list CommitStatuses: %w", err)
	}
	bundle.CommitStatuses = sanitizeSlice(csList.Items)

	// Commit-status managers reference the PS by spec.promotionStrategyRef.name.
	argocdList := &promoterv1alpha1.ArgoCDCommitStatusList{}
	if err := reader.List(ctx, argocdList, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list ArgoCDCommitStatuses: %w", err)
	}
	for i := range argocdList.Items {
		if argocdList.Items[i].Spec.PromotionStrategyRef.Name == name {
			item := argocdList.Items[i]
			sanitize(&item)
			bundle.ArgoCDCommitStatuses = append(bundle.ArgoCDCommitStatuses, item)
		}
	}

	gitCSList := &promoterv1alpha1.GitCommitStatusList{}
	if err := reader.List(ctx, gitCSList, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list GitCommitStatuses: %w", err)
	}
	for i := range gitCSList.Items {
		if gitCSList.Items[i].Spec.PromotionStrategyRef.Name == name {
			item := gitCSList.Items[i]
			sanitize(&item)
			bundle.GitCommitStatuses = append(bundle.GitCommitStatuses, item)
		}
	}

	timedCSList := &promoterv1alpha1.TimedCommitStatusList{}
	if err := reader.List(ctx, timedCSList, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list TimedCommitStatuses: %w", err)
	}
	for i := range timedCSList.Items {
		if timedCSList.Items[i].Spec.PromotionStrategyRef.Name == name {
			item := timedCSList.Items[i]
			sanitize(&item)
			bundle.TimedCommitStatuses = append(bundle.TimedCommitStatuses, item)
		}
	}

	webReqCSList := &promoterv1alpha1.WebRequestCommitStatusList{}
	if err := reader.List(ctx, webReqCSList, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list WebRequestCommitStatuses: %w", err)
	}
	for i := range webReqCSList.Items {
		if webReqCSList.Items[i].Spec.PromotionStrategyRef.Name == name {
			item := webReqCSList.Items[i]
			sanitize(&item)
			bundle.WebRequestCommitStatuses = append(bundle.WebRequestCommitStatuses, item)
		}
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
	bundle.GitRepository = gitRepo
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
		bundle.ClusterScmProvider = provider
	default: // "ScmProvider" (also the kubebuilder default)
		provider := &promoterv1alpha1.ScmProvider{}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, provider); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to get ScmProvider %s/%s: %w", namespace, ref.Name, err)
		}
		sanitize(provider)
		bundle.ScmProvider = provider
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

// sanitizeSlice sanitizes each element of a slice in place and returns it (nil when empty).
func sanitizeSlice[T any, PT interface {
	client.Object
	*T
}](items []T) []T {
	if len(items) == 0 {
		return nil
	}
	for i := range items {
		sanitize(PT(&items[i]))
	}
	return items
}
