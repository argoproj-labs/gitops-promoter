package v1alpha1

type GitHub struct {
	Url string `json:"url,omitempty"`
}

type Fake struct {
}

type RepositoryRef struct {
	// +kubebuilder:validation:Required
	Owner string `json:"owner"`
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// +kubebuilder:validation:Required
	ScmProviderRef NamespacedObjectReference `json:"scmProviderRef"`
}

type NamespacedObjectReference struct {
	// +kubebuilder:validation:Required
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}
