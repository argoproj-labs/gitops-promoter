package utils

import (
	"strings"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// RepoKey returns a provider-agnostic repository identity key as lowercased
// "<owner>/<name>". Used by field indexes and the webhook receiver so GitHub-style
// webhook payloads can match Fake (and other) GitRepository specs in tests and
// production without a provider prefix.
func RepoKey(owner, name string) string {
	if owner == "" || name == "" {
		return ""
	}
	return strings.ToLower(owner + "/" + name)
}

// GitRepositoryRepoKey returns the repo identity key for a GitRepository from
// whichever provider block is set. Returns "" when no provider block is present
// or owner/name are empty.
func GitRepositoryRepoKey(repo *promoterv1alpha1.GitRepository) string {
	if repo == nil {
		return ""
	}
	switch {
	case repo.Spec.GitHub != nil:
		return RepoKey(repo.Spec.GitHub.Owner, repo.Spec.GitHub.Name)
	case repo.Spec.GitLab != nil:
		return RepoKey(repo.Spec.GitLab.Namespace, repo.Spec.GitLab.Name)
	case repo.Spec.Forgejo != nil:
		return RepoKey(repo.Spec.Forgejo.Owner, repo.Spec.Forgejo.Name)
	case repo.Spec.Gitea != nil:
		return RepoKey(repo.Spec.Gitea.Owner, repo.Spec.Gitea.Name)
	case repo.Spec.BitbucketCloud != nil:
		return RepoKey(repo.Spec.BitbucketCloud.Owner, repo.Spec.BitbucketCloud.Name)
	case repo.Spec.AzureDevOps != nil:
		return RepoKey(repo.Spec.AzureDevOps.Project, repo.Spec.AzureDevOps.Name)
	case repo.Spec.Fake != nil:
		return RepoKey(repo.Spec.Fake.Owner, repo.Spec.Fake.Name)
	default:
		return ""
	}
}
