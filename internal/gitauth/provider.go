package gitauth

import (
	"context"
	"errors"
	"fmt"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
	bitbucket_cloud "github.com/argoproj-labs/gitops-promoter/internal/scms/bitbucket_cloud"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/fake"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/forgejo"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitea"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/github"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitlab"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CreateGitOperationsProvider creates the appropriate git operations provider based on the SCM provider type.
// This is a shared utility function used by multiple controllers to avoid code duplication.
//
// Parameters:
//   - ctx: Context for logging and cancellation
//   - k8sClient: Kubernetes client for fetching resources (needed by GitHub provider)
//   - scmProvider: The SCM provider configuration (GitHub, GitLab, Forgejo, Bitbucket Cloud, or Fake)
//   - secret: The secret containing authentication credentials
//   - repoObjectKey: The namespace/name reference to the GitRepository resource
//
// Returns the GitOperationsProvider interface implementation and any error encountered.
func CreateGitOperationsProvider(
	ctx context.Context,
	k8sClient client.Client,
	scmProvider v1alpha1.GenericScmProvider,
	secret *corev1.Secret,
	repoObjectKey client.ObjectKey,
) (scms.GitOperationsProvider, error) {
	logger := log.FromContext(ctx)

	switch {
	case scmProvider.GetSpec().Fake != nil:
		logger.V(4).Info("Creating fake git authentication provider")
		return fake.NewFakeGitAuthenticationProvider(scmProvider, secret), nil

	case scmProvider.GetSpec().GitHub != nil:
		logger.V(4).Info("Creating GitHub git authentication provider")
		provider, err := github.NewGithubGitAuthenticationProvider(ctx, k8sClient, scmProvider, secret, repoObjectKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create GitHub Auth Provider: %w", err)
		}
		return provider, nil

	case scmProvider.GetSpec().GitLab != nil:
		logger.V(4).Info("Creating GitLab git authentication provider")
		provider, err := gitlab.NewGitlabGitAuthenticationProvider(scmProvider, secret)
		if err != nil {
			return nil, fmt.Errorf("failed to create GitLab Auth Provider: %w", err)
		}
		return provider, nil

	case scmProvider.GetSpec().Forgejo != nil:
		logger.V(4).Info("Creating Forgejo git authentication provider")
		return forgejo.NewForgejoGitAuthenticationProvider(scmProvider, secret), nil

	case scmProvider.GetSpec().Gitea != nil:
		logger.V(4).Info("Creating Gitea git authentication provider")
		return gitea.NewGiteaGitAuthenticationProvider(scmProvider, secret), nil

	case scmProvider.GetSpec().BitbucketCloud != nil:
		logger.V(4).Info("Creating Bitbucket Cloud git authentication provider")
		provider, err := bitbucket_cloud.NewBitbucketCloudGitAuthenticationProvider(scmProvider, secret)
		if err != nil {
			return nil, fmt.Errorf("failed to create Bitbucket Cloud Auth Provider: %w", err)
		}
		return provider, nil

	case scmProvider.GetSpec().AzureDevOps != nil:
		logger.V(4).Info("Creating Azure DevOps git authentication provider")
		return azuredevops.NewAzdoGitAuthenticationProvider(scmProvider, secret), nil
	default:
		return nil, errors.New("no supported git authentication provider found")
	}
}
