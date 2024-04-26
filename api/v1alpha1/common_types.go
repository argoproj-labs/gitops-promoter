package v1alpha1

type GitHub struct {
	Domain string `json:"domain,omitempty"`
}

type Fake struct {
	Domain string `json:"domain,omitempty"`
}

type Repository struct {
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
