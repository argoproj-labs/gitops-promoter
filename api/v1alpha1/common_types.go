package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GitHub is a GitHub SCM provider configuration. It is used to configure the GitHub settings.
type GitHub struct {
	// Domain is the GitHub domain, such as "github.mycompany.com". If using the default GitHub domain, leave this field
	// empty.
	// +kubebuilder:validation:XValidation:rule=`self != "github.com"`, message="Instead of setting the domain to github.com, leave the field blank"
	Domain string `json:"domain,omitempty"`
	// AppID is the GitHub App ID.
	// +kubebuilder:validation:Required
	AppID int64 `json:"appID"`
	// InstallationID is the GitHub App Installation ID. If you want to use this ScmProvider for multiple
	// GitHub orgs, do not specify this field. The installation ID will be inferred from the repo owner
	// when needed.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	InstallationID int64 `json:"installationID,omitempty"`
}

// GitLab is a GitLab SCM provider configuration. It is used to configure the GitLab settings.
type GitLab struct {
	// Domain is the GitLab domain, such as "gitlab.mycompany.com". If using the default GitLab domain, leave this field
	// empty.
	Domain string `json:"domain,omitempty"`
}

// Forgejo is a Forgejo SCM provider configuration. It is used to configure the Forgejo settings.
type Forgejo struct {
	// Domain is the Forgejo domain, such as "codeberg.org" or "forgejo.mycompany.com".
	// There is no default domain since Forgejo is not a service like Gitlab or Github.
	// +kubebuilder:validation:Required
	Domain string `json:"domain"`
}

// Fake is a placeholder for a fake SCM provider, used for testing purposes.
type Fake struct {
	// Domain is the domain of the fake SCM provider. This is used for testing purposes.
	Domain string `json:"domain,omitempty"`
}

// ObjectReference is a reference to an object by name. It is used to refer to objects in the same namespace.
type ObjectReference struct {
	// Name is the name of the object to refer to.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// GitHubRepo is a repository in GitHub, identified by its owner and name.
type GitHubRepo struct {
	// These validation rules are based on unofficial documentation and may need to be relaxed in the future.
	// https://github.com/dead-claudia/github-limits

	// Owner is the owner of the repository, which can be a user or an organization.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=39
	// +kubebuilder:validation:Pattern=^[a-zA-Z0-9][a-zA-Z0-9\-]*$
	Owner string `json:"owner"`
	// Name is the name of the repository.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:Pattern=^[a-zA-Z0-9_\-\.]+$
	Name string `json:"name"`
}

// GitLabRepo is a repository in GitLab, identified by its namespace, name, and project ID.
type GitLabRepo struct {
	// Namespace is the user, group or group with subgroup (e.g. group/subgroup).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=^[a-zA-Z0-9_\-\/.]+$
	Namespace string `json:"namespace"`
	// Name is the project slug of the repository.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=^[a-zA-Z0-9_\-\/.]+$
	Name string `json:"name"`
	// ProjectID is the ID of the project in GitLab.
	// +kubebuilder:validation:Required
	ProjectID int `json:"projectId"`
}

// ForgejoRepo is a repository in Forgejo, identified by its owner and name.
type ForgejoRepo struct {
	// Owner is the owner of the repository.
	// +kubebuilder:validation:Required
	Owner string `json:"owner"`
	// Name is the name of the repository.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// FakeRepo is a placeholder for a repository in the fake SCM provider, used for testing purposes.
type FakeRepo struct {
	// Owner is the owner of the repository.
	// +kubebuilder:validation:Required
	Owner string `json:"owner"`
	// Name is the name of the repository.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// CommitMetadata contains metadata about a commit that is related in some way to another commit.
type CommitMetadata struct {
	// Author is the author of the commit.
	Author string `json:"author,omitempty"`
	// Date is the date of the commit, formatted as by `git show -s --format=%aI`.
	Date *metav1.Time `json:"date,omitempty"`
	// Subject is the subject line of the commit message, i.e. `git show --format=%s`.
	Subject string `json:"subject,omitempty"`
	// Body is the body of the commit message, excluding the subject line, i.e. `git show --format=%b`.
	Body string `json:"body,omitempty"`
	// Sha is the commit hash.
	Sha string `json:"sha,omitempty"`
	// RepoURL is the URL of the repository where the commit is located.
	// +kubebuilder:validation:XValidation:rule="self == '' || isURL(self)",message="must be a valid URL"
	// +kubebuilder:validation:Pattern="^(https?://.*)?$"
	RepoURL string `json:"repoURL,omitempty"`
}

// RevisionReference contains a reference to a some information that is related in some way to another commit. For now,
// it supports only references to a commit. In the future, it may support other types of references.
type RevisionReference struct {
	// Commit contains metadata about the commit that is related in some way to another commit.
	Commit *CommitMetadata `json:"commit,omitempty"`
}
