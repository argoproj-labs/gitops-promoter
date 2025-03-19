package v1alpha1

type GitHub struct {
	Domain string `json:"domain,omitempty"`
}

type GitLab struct {
	Domain string `json:"domain,omitempty"`
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

type FakeRepo struct {
	// +kubebuilder:validation:Required
	Owner string `json:"owner"`
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}
