package utils

import (
	"strings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// SCM provider identifiers used to scope RepoKey so identically named repositories
// on different SCM providers never collide. Values match the provider strings the
// webhook receiver detects from request headers (see webhookreceiver.DetectProvider).
const (
	ProviderGitHub         = "github"
	ProviderGitLab         = "gitlab"
	ProviderForgejo        = "forgejo"
	ProviderGitea          = "gitea"
	ProviderBitbucketCloud = "bitbucketCloud"
	ProviderAzureDevOps    = "azureDevOps"

	// repoKeySeparator cannot appear in an owner/namespace/name segment from any
	// supported provider, unlike '/', which GitLab namespaces use for subgroups
	// (e.g. "group/sub"). Using it between provider, owner, and name (instead of '/')
	// prevents both cross-provider and GitLab-subgroup key collisions: without it,
	// namespace="group/sub" name="proj" and namespace="group" name="sub/proj" would
	// both join to the same "group/sub/proj" string.
	repoKeySeparator = "\x00"
)

// RepoKey returns a repository identity key scoped by provider, as lowercased
// "<provider>\x00<owner>\x00<name>". Used by field indexes and the webhook receiver
// so a webhook payload can only match GitRepository specs of the same provider, and
// so GitLab subgroup namespaces can't collide with a differently-split owner/name.
func RepoKey(provider, owner, name string) string {
	if provider == "" || owner == "" || name == "" {
		return ""
	}
	return strings.ToLower(provider + repoKeySeparator + owner + repoKeySeparator + name)
}

// GitRepositoryRepoKey returns the repo identity key for a GitRepository from
// whichever provider block is set. Returns "" when no provider block is present
// or owner/name are empty.
//
// Fake is keyed as ProviderGitHub because the webhook receiver never detects a
// "fake" provider from request headers: fake-provider tests send GitHub-shaped
// webhook payloads and rely on GitHub's provider bucket to match Fake GitRepository
// specs by owner/name (see webhookreceiver.DetectProvider, which has no fake case).
func GitRepositoryRepoKey(repo *promoterv1alpha1.GitRepository) string {
	if repo == nil {
		return ""
	}
	switch {
	case repo.Spec.GitHub != nil:
		return RepoKey(ProviderGitHub, repo.Spec.GitHub.Owner, repo.Spec.GitHub.Name)
	case repo.Spec.GitLab != nil:
		return RepoKey(ProviderGitLab, repo.Spec.GitLab.Namespace, repo.Spec.GitLab.Name)
	case repo.Spec.Forgejo != nil:
		return RepoKey(ProviderForgejo, repo.Spec.Forgejo.Owner, repo.Spec.Forgejo.Name)
	case repo.Spec.Gitea != nil:
		return RepoKey(ProviderGitea, repo.Spec.Gitea.Owner, repo.Spec.Gitea.Name)
	case repo.Spec.BitbucketCloud != nil:
		return RepoKey(ProviderBitbucketCloud, repo.Spec.BitbucketCloud.Owner, repo.Spec.BitbucketCloud.Name)
	case repo.Spec.AzureDevOps != nil:
		return RepoKey(ProviderAzureDevOps, repo.Spec.AzureDevOps.Project, repo.Spec.AzureDevOps.Name)
	case repo.Spec.Fake != nil:
		return RepoKey(ProviderGitHub, repo.Spec.Fake.Owner, repo.Spec.Fake.Name)
	default:
		return ""
	}
}
