package v1alpha1

import v1 "k8s.io/api/core/v1"

type GitHub struct {
	Url string `json:"url,omitempty"`
}

type RepositoryRef struct {
	Owner       string                  `json:"owner,omitempty"`
	Name        string                  `json:"name,omitempty"`
	ProviderRef v1.LocalObjectReference `json:"providerRef,omitempty"`
}
