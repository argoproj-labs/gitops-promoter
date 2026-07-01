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
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promotercache "github.com/argoproj-labs/gitops-promoter/internal/cache"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
)

//go:embed testdata/ControllerConfiguration.yaml
var testControllerConfigurationYAML string

var _ = Describe("ControllerConfiguration Controller", func() {
	Context("When unmarshalling the test data", func() {
		It("should unmarshal the ControllerConfiguration resource", func() {
			err := unmarshalYamlStrict(testControllerConfigurationYAML, &promoterv1alpha1.ControllerConfiguration{})
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
		controllerconfiguration := &promoterv1alpha1.ControllerConfiguration{}

		BeforeEach(func() {
			By("creating the custom resource for the Kind ControllerConfiguration")
			err := k8sClient.Get(ctx, typeNamespacedName, controllerconfiguration)
			if err != nil && errors.IsNotFound(err) {
				resource, loadErr := loadShippedControllerConfigurationForTests(resourceName)
				Expect(loadErr).NotTo(HaveOccurred())
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			// TODO(user): Cleanup logic after each test, like removing the resource instance.
			resource := &promoterv1alpha1.ControllerConfiguration{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the specific resource instance ControllerConfiguration")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})
		It("should successfully reconcile the resource", func() {
			By("Reconciling the created resource")
			controllerReconciler := &ControllerConfigurationReconciler{
				Client:              k8sClient,
				Scheme:              k8sClient.Scheme(),
				ControllerNamespace: "default",
				StartupInstanceID:   nil,
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When spec.instanceID drifts from startup", func() {
		const resourceName = settings.ControllerConfigurationName

		ctx := context.Background()
		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		BeforeEach(func() {
			cc := &promoterv1alpha1.ControllerConfiguration{}
			err := k8sClient.Get(ctx, typeNamespacedName, cc)
			if err != nil && errors.IsNotFound(err) {
				resource, loadErr := loadShippedControllerConfigurationForTests(resourceName)
				Expect(loadErr).NotTo(HaveOccurred())
				Expect(k8sClient.Create(ctx, resource)).To(Succeed())
			}
		})

		AfterEach(func() {
			cc := &promoterv1alpha1.ControllerConfiguration{}
			err := k8sClient.Get(ctx, typeNamespacedName, cc)
			if err != nil {
				return
			}
			base := cc.DeepCopy()
			cc.Spec.InstanceID = nil
			Expect(k8sClient.Patch(ctx, cc, client.MergeFrom(base))).To(Succeed())
		})

		It("does not shut down when startup and spec instanceID match", func() {
			var shutdowns atomic.Int32
			reconciler := &ControllerConfigurationReconciler{
				Client:              k8sClient,
				Scheme:              k8sClient.Scheme(),
				ControllerNamespace: "default",
				StartupInstanceID:   nil,
				Shutdown: func() {
					shutdowns.Add(1)
				},
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(shutdowns.Load()).To(Equal(int32(0)))
		})

		It("shuts down when spec.instanceID changes from default install", func() {
			var shutdowns atomic.Int32
			wave0 := "wave-0"
			reconciler := &ControllerConfigurationReconciler{
				Client:              k8sClient,
				Scheme:              k8sClient.Scheme(),
				ControllerNamespace: "default",
				StartupInstanceID:   nil,
				Shutdown: func() {
					shutdowns.Add(1)
				},
			}

			cc := &promoterv1alpha1.ControllerConfiguration{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, cc)).To(Succeed())
			base := cc.DeepCopy()
			cc.Spec.InstanceID = &wave0
			Expect(k8sClient.Patch(ctx, cc, client.MergeFrom(base))).To(Succeed())

			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: typeNamespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(shutdowns.Load()).To(Equal(int32(1)))
		})

		It("ignores other ControllerConfiguration resources", func() {
			var shutdowns atomic.Int32
			wave0 := "wave-0"
			reconciler := &ControllerConfigurationReconciler{
				Client:              k8sClient,
				Scheme:              k8sClient.Scheme(),
				ControllerNamespace: "default",
				StartupInstanceID:   nil,
				Shutdown: func() {
					shutdowns.Add(1)
				},
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: "other-config", Namespace: "default"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(shutdowns.Load()).To(Equal(int32(0)))

			reconciler.StartupInstanceID = &wave0
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: resourceName, Namespace: "other-namespace"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(shutdowns.Load()).To(Equal(int32(0)))
		})
	})
})

var _ = Describe("OptionsForInstanceID", func() {
	It("scopes all promotor CRDs to resources without instance-id when nil", func() {
		opts := promotercache.OptionsForInstanceID(nil)
		Expect(opts.ByObject).To(HaveLen(len(promotercache.PromotorCRDObjects())))
		for _, obj := range promotercache.PromotorCRDObjects() {
			byObj, ok := opts.ByObject[obj]
			Expect(ok).To(BeTrue(), "missing ByObject for %T", obj)
			Expect(byObj.Label.String()).To(Equal("!promoter.argoproj.io/instance-id"))
		}
	})

	It("scopes all promotor CRDs to matching instance-id when set", func() {
		instanceID := "wave-0"
		opts := promotercache.OptionsForInstanceID(&instanceID)
		Expect(opts.ByObject).To(HaveLen(len(promotercache.PromotorCRDObjects())))
		for _, obj := range promotercache.PromotorCRDObjects() {
			byObj, ok := opts.ByObject[obj]
			Expect(ok).To(BeTrue(), "missing ByObject for %T", obj)
			Expect(byObj.Label.String()).To(Equal("promoter.argoproj.io/instance-id=wave-0"))
		}
	})

	It("does not include ControllerConfiguration", func() {
		instanceID := "wave-0"
		opts := promotercache.OptionsForInstanceID(&instanceID)
		_, ok := opts.ByObject[&promoterv1alpha1.ControllerConfiguration{}]
		Expect(ok).To(BeFalse())
	})
})
