package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type GitHub struct {
	// Domain is the GitHub domain, such as "github.mycompany.com". If using the default GitHub domain, leave this field
	// empty.
	// +kubebuilder:validation:XValidation:rule=`self != "github.com"`, message="Instead of setting the domain to github.com, leave the field blank"
	Domain string `json:"domain,omitempty"`
	// AppID is the GitHub App ID.
	// +kubebuilder:validation:Required
	AppID int64 `json:"appID"`
	// InstallationID is the GitHub App Installation ID.
	// +kubebuilder:validation:Required
	InstallationID int64 `json:"installationID"`
}

type GitLab struct {
	Domain string `json:"domain,omitempty"`
}

type Forgejo struct {
	// Domain is the Forgejo domain, such as "codeberg.org" or "forgejo.mycompany.com".
	// There is no default domain since Forgejo is not a service like Gitlab or Github.
	// +kubebuilder:validation:Required
	Domain string `json:"domain"`
}

type Fake struct {
	Domain string `json:"domain,omitempty"`
}

type ObjectReference struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

type GitHubRepo struct {
	// +kubebuilder:validation:Required
	Owner string `json:"owner"`
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

type GitLabRepo struct {
	// User, group or group with subgroup (e.g. group/subgroup).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=^[a-zA-Z0-9_\-\/.]+$
	Namespace string `json:"namespace"`
	// Project slug of the repository.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=^[a-zA-Z0-9_\-\/.]+$
	Name string `json:"name"`
	// +kubebuilder:validation:Required
	ProjectID int `json:"projectId"`
}

type ForgejoRepo struct {
	// +kubebuilder:validation:Required
	Owner string `json:"owner"`
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

type FakeRepo struct {
	// +kubebuilder:validation:Required
	Owner string `json:"owner"`
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// HydratorMetadata contains metadata about the commit that is used to hydrate a branch. It is used to store
type HydratorMetadata struct {
	// RepoURL is the URL of the repository where the commit is located.
	RepoURL string `json:"repoURL,omitempty"`
	// DrySha is the SHA of the commit that was used as the dry source for hydration.
	DrySha string `json:"drySha,omitempty"`
	// Commands are the commands that were run to hydrate the branch.
	Commands []string `json:"commands,omitempty"`
	// Author is the author of the dry commit that was used to hydrate the branch.
	Author string `json:"author,omitempty"`
	// Date is the date of the dry commit that was used to hydrate the branch.
	Date metav1.Time `json:"date,omitempty"`
	// Subject is the subject line of the dry commit that was used to hydrate the branch.
	Subject string `json:"subject,omitempty"`
	// Body is the body of the dry commit that was used to hydrate the branch without the subject.
	Body string `json:"body,omitempty"`
	// References are the references to other commits, that went into the hydration of the branch.
	References []RevisionReference `json:"references,omitempty"`
}

// CommitMetadata contains metadata about a commit that is related in some way to another commit.
type CommitMetadata struct {
	// Author is the author of the commit.
	Author string `json:"author,omitempty"`
	// Date is the date of the commit, formatted as by `git show -s --format=%aI`.
	Date metav1.Time `json:"date,omitempty"`
	// Subject is the subject line of the commit message, i.e. `git show --format=%s`.
	Subject string `json:"message,omitempty"`
	// Body is the body of the commit message, excluding the subject line, i.e. `git show --format=%b`.
	Body string `json:"body,omitempty"`
	// Sha is the commit hash.
	Sha string `json:"sha,omitempty"`
	// RepoURL is the URL of the repository where the commit is located.
	RepoURL string `json:"repoURL,omitempty"`
}

// RevisionReference contains a reference to a some information that is related in some way to another commit. For now,
// it supports only references to a commit. In the future, it may support other types of references.
type RevisionReference struct {
	// Commit contains metadata about the commit that is related in some way to another commit.
	Commit *CommitMetadata `json:"commit,omitempty"`
}
