/*
Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package apiserver implements the view aggregation layer: an extension
// apiserver that serves a read-only, server-computed PromotionStrategyDetails
// bundle backed by a controller-runtime cache (no etcd).
package apiserver

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	viewinstall "github.com/argoproj-labs/gitops-promoter/api/view/install"
)

var (
	// Scheme is the scheme used by the view extension apiserver. It contains
	// the view aggregation types (v1alpha1) plus the promoter
	// v1alpha1 types embedded in the bundle.
	Scheme = runtime.NewScheme()
	// Codecs provides serialization for the view apiserver.
	Codecs = serializer.NewCodecFactory(Scheme)
	// ParameterCodec handles query-parameter (e.g. ListOptions) decoding.
	ParameterCodec = runtime.NewParameterCodec(Scheme)
)

func init() {
	viewinstall.Install(Scheme)
	utilruntime.Must(promoterv1alpha1.AddToScheme(Scheme))

	// Register the unversioned types used by API machinery (Status, APIGroup, etc.).
	metav1.AddToGroupVersion(Scheme, schema.GroupVersion{Version: "v1"})
	unversioned := schema.GroupVersion{Group: "", Version: "v1"}
	Scheme.AddUnversionedTypes(unversioned,
		&metav1.Status{},
		&metav1.APIVersions{},
		&metav1.APIGroupList{},
		&metav1.APIGroup{},
		&metav1.APIResourceList{},
	)
}
