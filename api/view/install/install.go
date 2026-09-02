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

// Package install registers the view aggregation API (the single v1alpha1
// version) into a scheme.
package install

import (
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	viewv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/view/v1alpha1"
)

// Install registers the view API group (v1alpha1) into the given scheme.
// The resource has a single version and no etcd backing, so there is no internal
// version or conversion to register.
func Install(scheme *runtime.Scheme) {
	utilruntime.Must(viewv1alpha1.AddToScheme(scheme))
	utilruntime.Must(scheme.SetVersionPriority(viewv1alpha1.SchemeGroupVersion))
}
