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

package controller

import (
	"context"
	_ "embed"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
)

//go:embed testdata/ClusterScmProvider.yaml
var testClusterScmProviderYAML string

var _ = Describe("ClusterScmProvider Controller", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the ClusterScmProvider resource", func() {
			err := unmarshalYamlStrict(testClusterScmProviderYAML, &promoterv1alpha1.ClusterScmProvider{})
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("When reconciling a resource", func() {
		const resourceName = "test-resource"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default", // TODO(user):Modify as needed
		}
		clusterscmprovider := &promoterv1alpha1.ClusterScmProvider{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ClusterScmProvider")
			err := k8sClient.Get(ctx, typeNamespacedName, clusterscmprovider)
			if err != nil && errors.IsNotFound(err) {
				resource := &promoterv1alpha1.ClusterScmProvider{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: promoterv1alpha1.ScmProviderSpec{
						Fake: &promoterv1alpha1.Fake{},
					},
					// TODO(user): Specify other spec details if needed.
				}
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &promoterv1alpha1.ClusterScmProvider{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ClusterScmProvider")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Waiting for the controller to reconcile the resource")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, typeNamespacedName, clusterscmprovider)
				g.Expect(err).NotTo(HaveOccurred())
				// Verify that the controller has added the finalizer
				g.Expect(clusterscmprovider.Finalizers).To(ContainElement(promoterv1alpha1.ClusterScmProviderFinalizer))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})
