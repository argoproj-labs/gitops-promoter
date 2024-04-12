package v1alpha1

import v1 "k8s.io/api/core/v1"

type GitHub struct {
	Url string `json:"url,omitempty"`
}

type RepositoryRef struct {
	// +kubebuilder:validation:Required
	Owner string `json:"owner"`
	// +kubebuilder:validation:Required
	Name string `json:"name"`
	// +kubebuilder:validation:Required
	ProviderRef v1.LocalObjectReference `json:"providerRef"`
}
