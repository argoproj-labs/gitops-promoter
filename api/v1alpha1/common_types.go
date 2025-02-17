package v1alpha1

type GitHub struct {
	Domain string `json:"domain,omitempty"`
}

type Fake struct {
	Domain string `json:"domain,omitempty"`
}

type ObjectReference struct {
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

type OpenPullerRequestFilter struct {
	Paths []string `json:"paths,omitempty"`
}
