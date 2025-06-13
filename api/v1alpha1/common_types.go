package v1alpha1

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
