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

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
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
					Name:      resourceName,
					Namespace: "default",
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

	Context("When secretRef is changed to a different Secret", func() {
		It("should remove the finalizer from the previously referenced Secret", func() {
			ctx := context.Background()
			name := "clusterscmprovider-secretref-change-" + utils.KubeSafeUniqueName(randomString(15))

			// The test manager runs with "default" as the controller namespace.
			oldSecret := &v1.Secret{Name: name + "-old", Namespace: "default"}
			newSecret := &v1.Secret{Name: name + "-new", Namespace: "default"}
			clusterScmProvider := &promoterv1alpha1.ClusterScmProvider{
				Name: name,
				Spec: promoterv1alpha1.ScmProviderSpec{
					SecretRef: &v1.LocalObjectReference{Name: oldSecret.Name},
					Fake:      &promoterv1alpha1.Fake{},
				},
			}
			Expect(k8sClient.Create(ctx, oldSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, newSecret)).To(Succeed())
			Expect(k8sClient.Create(ctx, clusterScmProvider)).To(Succeed())

			DeferCleanup(func() {
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, clusterScmProvider))).To(Succeed())
				Eventually(func(g Gomega) {
					err := k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterScmProvider), clusterScmProvider)
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
				}, constants.EventuallyTimeout).Should(Succeed())
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, oldSecret))).To(Succeed())
				Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, newSecret))).To(Succeed())
			})

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(oldSecret), oldSecret)).To(Succeed())
				g.Expect(oldSecret.Finalizers).To(ContainElement(promoterv1alpha1.ClusterScmProviderSecretFinalizer))
			}, constants.EventuallyTimeout).Should(Succeed())

			By("Pointing the ClusterScmProvider at the new Secret")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(clusterScmProvider), clusterScmProvider)).To(Succeed())
				clusterScmProvider.Spec.SecretRef = &v1.LocalObjectReference{Name: newSecret.Name}
				g.Expect(k8sClient.Update(ctx, clusterScmProvider)).To(Succeed())
			}, constants.EventuallyTimeout).Should(Succeed())

			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(newSecret), newSecret)).To(Succeed())
				g.Expect(newSecret.Finalizers).To(ContainElement(promoterv1alpha1.ClusterScmProviderSecretFinalizer))
				g.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(oldSecret), oldSecret)).To(Succeed())
				g.Expect(oldSecret.Finalizers).ToNot(ContainElement(promoterv1alpha1.ClusterScmProviderSecretFinalizer))
			}, constants.EventuallyTimeout).Should(Succeed())
		})
	})
})
