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

	"github.com/google/uuid"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	viewv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/view/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/controller"
)

// detailsUIDNamespace is a fixed UUID namespace used to derive deterministic UIDs
// for the virtual PromotionStrategyDetails resource. It is an arbitrary, stable
// constant; its only purpose is to seed the UUIDv5 derivation below.
var detailsUIDNamespace = uuid.MustParse("6f0c3b9e-1c2d-4f7a-9b5e-2a1d3c4e5f60")

// detailsUID derives the UID for a PromotionStrategyDetails object from its backing
// PromotionStrategy's UID. The details resource is a distinct virtual object, not
// the PromotionStrategy itself, so it must carry its own UID rather than reusing
// ps.UID (which would make the two indistinguishable to clients and caches). The
// UID is derived deterministically so repeated reads of the same PromotionStrategy
// yield a stable identity for watch/cache consumers.
func detailsUID(psUID types.UID) types.UID {
	return types.UID(uuid.NewSHA1(detailsUIDNamespace, []byte(psUID)).String())
}

// buildBundle assembles the PromotionStrategyDetails for the named PromotionStrategy
// from the given reader. It returns a NotFound error (scoped to the dashboard
// resource) when the PromotionStrategy does not exist. Secrets are never read.
//
// The returned bundle has its ResourceVersion set to the provided value.
func buildBundle(ctx context.Context, reader client.Reader, namespace, name, resourceVersion string) (*viewv1alpha1.PromotionStrategyDetails, error) {
	ps := &promoterv1alpha1.PromotionStrategy{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, ps); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, apierrors.NewNotFound(viewv1alpha1.Resource("promotionstrategydetails"), name)
		}
		return nil, fmt.Errorf("failed to get PromotionStrategy %s/%s: %w", namespace, name, err)
	}
	// The PromotionStrategy's status is a per-environment aggregation of the
	// ChangeTransferPolicy statuses, which are already embedded in the bundle
	// (.changeTransferPolicies). Drop it to avoid duplicating that data; consumers
	// reconstruct per-environment state from the CTPs.
	ps.Status = promoterv1alpha1.PromotionStrategyStatus{}

	bundle := &viewv1alpha1.PromotionStrategyDetails{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			UID:               detailsUID(ps.UID),
			ResourceVersion:   resourceVersion,
			CreationTimestamp: ps.CreationTimestamp,
			Labels:            ps.Labels,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: promoterv1alpha1.SchemeGroupVersion.String(),
				Kind:       "PromotionStrategy",
				Name:       ps.Name,
				UID:        ps.UID,
			}},
		},
		PromotionStrategy: *ps,
	}

	// CTPs, PullRequests, base CommitStatuses are labelled with the PS name.
	psLabel := client.MatchingLabels{promoterv1alpha1.PromotionStrategyLabel: name}

	ctpList := &promoterv1alpha1.ChangeTransferPolicyList{}
	if err := reader.List(ctx, ctpList, client.InNamespace(namespace), psLabel); err != nil {
		return nil, fmt.Errorf("failed to list ChangeTransferPolicies: %w", err)
	}
	bundle.ChangeTransferPolicies = nilIfEmpty(ctpList.Items)

	prList := &promoterv1alpha1.PullRequestList{}
	if err := reader.List(ctx, prList, client.InNamespace(namespace), psLabel); err != nil {
		return nil, fmt.Errorf("failed to list PullRequests: %w", err)
	}
	bundle.PullRequests = nilIfEmpty(prList.Items)

	csList := &promoterv1alpha1.CommitStatusList{}
	if err := reader.List(ctx, csList, client.InNamespace(namespace), psLabel); err != nil {
		return nil, fmt.Errorf("failed to list CommitStatuses: %w", err)
	}
	bundle.CommitStatuses = nilIfEmpty(csList.Items)

	// Commit-status managers reference the PS by spec.promotionStrategyRef.name.
	argocdList := &promoterv1alpha1.ArgoCDCommitStatusList{}
	if err := reader.List(ctx, argocdList, client.InNamespace(namespace), client.MatchingFields{controller.PromotionStrategyRefField: name}); err != nil {
		return nil, fmt.Errorf("failed to list ArgoCDCommitStatuses: %w", err)
	}
	bundle.ArgoCDCommitStatuses = nilIfEmpty(argocdList.Items)

	gitCSList := &promoterv1alpha1.GitCommitStatusList{}
	if err := reader.List(ctx, gitCSList, client.InNamespace(namespace), client.MatchingFields{controller.PromotionStrategyRefField: name}); err != nil {
		return nil, fmt.Errorf("failed to list GitCommitStatuses: %w", err)
	}
	bundle.GitCommitStatuses = nilIfEmpty(gitCSList.Items)

	timedCSList := &promoterv1alpha1.TimedCommitStatusList{}
	if err := reader.List(ctx, timedCSList, client.InNamespace(namespace), client.MatchingFields{controller.PromotionStrategyRefField: name}); err != nil {
		return nil, fmt.Errorf("failed to list TimedCommitStatuses: %w", err)
	}
	bundle.TimedCommitStatuses = nilIfEmpty(timedCSList.Items)

	webReqCSList := &promoterv1alpha1.WebRequestCommitStatusList{}
	if err := reader.List(ctx, webReqCSList, client.InNamespace(namespace), client.MatchingFields{controller.PromotionStrategyRefField: name}); err != nil {
		return nil, fmt.Errorf("failed to list WebRequestCommitStatuses: %w", err)
	}
	bundle.WebRequestCommitStatuses = nilIfEmpty(webReqCSList.Items)

	dagCSList := &promoterv1alpha1.DAGCommitStatusList{}
	if err := reader.List(ctx, dagCSList, client.InNamespace(namespace), client.MatchingFields{controller.PromotionStrategyRefField: name}); err != nil {
		return nil, fmt.Errorf("failed to list DAGCommitStatuses: %w", err)
	}
	bundle.DAGCommitStatuses = nilIfEmpty(dagCSList.Items)

	prevEnvCSList := &promoterv1alpha1.PreviousEnvironmentCommitStatusList{}
	if err := reader.List(ctx, prevEnvCSList, client.InNamespace(namespace), client.MatchingFields{controller.PromotionStrategyRefField: name}); err != nil {
		return nil, fmt.Errorf("failed to list PreviousEnvironmentCommitStatuses: %w", err)
	}
	bundle.PreviousEnvironmentCommitStatuses = nilIfEmpty(prevEnvCSList.Items)

	scheduledCSList := &promoterv1alpha1.ScheduledCommitStatusList{}
	if err := reader.List(ctx, scheduledCSList, client.InNamespace(namespace), client.MatchingFields{controller.PromotionStrategyRefField: name}); err != nil {
		return nil, fmt.Errorf("failed to list ScheduledCommitStatuses: %w", err)
	}
	bundle.ScheduledCommitStatuses = nilIfEmpty(scheduledCSList.Items)

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
func attachGitConfig(ctx context.Context, reader client.Reader, namespace string, ps *promoterv1alpha1.PromotionStrategy, bundle *viewv1alpha1.PromotionStrategyDetails) error {
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

	bundle.GitRepository = gitRepo
	return attachScmProvider(ctx, reader, namespace, gitRepo, bundle)
}

// attachScmProvider resolves the GitRepository's ScmProviderRef and attaches the
// referenced (Cluster)ScmProvider to the bundle. Secrets are never resolved.
func attachScmProvider(ctx context.Context, reader client.Reader, namespace string, gitRepo *promoterv1alpha1.GitRepository, bundle *viewv1alpha1.PromotionStrategyDetails) error {
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
		bundle.ClusterScmProvider = provider
	default: // "ScmProvider" (also the kubebuilder default)
		provider := &promoterv1alpha1.ScmProvider{}
		if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, provider); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("failed to get ScmProvider %s/%s: %w", namespace, ref.Name, err)
		}
		bundle.ScmProvider = provider
	}
	return nil
}

// nilIfEmpty returns nil for an empty slice so bundle JSON omits empty arrays.
func nilIfEmpty[T any](items []T) []T {
	if len(items) == 0 {
		return nil
	}
	return items
}
