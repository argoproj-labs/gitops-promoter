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

package apiserver

import (
	"k8s.io/apimachinery/pkg/api/meta"
	toolscache "k8s.io/client-go/tools/cache"
	ctrlcache "sigs.k8s.io/controller-runtime/pkg/cache"
)

// lastAppliedAnnotation is stripped from cached objects so bundles stay small and
// free of noisy server-side-apply metadata.
const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// cacheTransform drops managedFields and the last-applied-configuration annotation
// when objects are committed to the read cache.
func cacheTransform() toolscache.TransformFunc {
	stripManaged := ctrlcache.TransformStripManagedFields()
	return func(in any) (any, error) {
		out, err := stripManaged(in)
		if err != nil {
			return out, err
		}
		obj, err := meta.Accessor(out)
		if err != nil {
			return out, nil
		}
		annotations := obj.GetAnnotations()
		if annotations == nil {
			return out, nil
		}
		if _, ok := annotations[lastAppliedAnnotation]; !ok {
			return out, nil
		}
		delete(annotations, lastAppliedAnnotation)
		obj.SetAnnotations(annotations)
		return out, nil
	}
}
