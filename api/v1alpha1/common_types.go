package v1alpha1

type GitHub struct {
	Domain string `json:"domain,omitempty"`
}

type Fake struct {
	Domain string `json:"domain,omitempty"`
}

type NamespacedObjectReference struct {
	// +kubebuilder:validation:Required
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}
