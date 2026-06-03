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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("cacheTransform", func() {
	It("strips managedFields and the last-applied annotation", func() {
		ps := &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-ps",
				Namespace: "test-ns",
				ManagedFields: []metav1.ManagedFieldsEntry{
					{Manager: "controller", Operation: metav1.ManagedFieldsOperationUpdate, APIVersion: "promoter.argoproj.io/v1alpha1", FieldsType: "FieldsV1"},
				},
				Annotations: map[string]string{
					lastAppliedAnnotation: "{\"should\":\"be stripped\"}",
					"keep-me":             "yes",
				},
			},
		}

		out, err := cacheTransform()(ps)
		Expect(err).NotTo(HaveOccurred())

		transformed := out.(*promoterv1alpha1.PromotionStrategy)
		Expect(transformed.ManagedFields).To(BeNil())
		Expect(transformed.Annotations).NotTo(HaveKey(lastAppliedAnnotation))
		Expect(transformed.Annotations).To(HaveKeyWithValue("keep-me", "yes"))
	})
})
