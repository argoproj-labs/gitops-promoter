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

package events

import (
	"maps"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RefForObject builds an ObjectRef and a copy of the object's labels from a live Kubernetes
// object. gvk supplies the apiVersion/kind, which the controller-runtime cache does not always
// populate on read. It is a convenience for reconcilers emitting events.
//
// Labels are copied so the resulting Event remains a self-contained value (mutating the live
// object's labels afterwards does not affect the buffered event).
func RefForObject(obj client.Object, gvk schema.GroupVersionKind) (ObjectRef, map[string]string) {
	ref := ObjectRef{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Namespace:  obj.GetNamespace(),
		Name:       obj.GetName(),
		UID:        string(obj.GetUID()),
	}

	var labels map[string]string
	if src := obj.GetLabels(); len(src) > 0 {
		labels = make(map[string]string, len(src))
		maps.Copy(labels, src)
	}
	return ref, labels
}
