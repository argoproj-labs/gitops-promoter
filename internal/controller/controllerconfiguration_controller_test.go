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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
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
				resource := &promoterv1alpha1.ControllerConfiguration{
					ObjectMeta: metav1.ObjectMeta{
						Name:      resourceName,
						Namespace: "default",
					},
					Spec: promoterv1alpha1.ControllerConfigurationSpec{
						PromotionStrategy: promoterv1alpha1.PromotionStrategyConfiguration{
							WorkQueue: promoterv1alpha1.WorkQueue{
								RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
								MaxConcurrentReconciles: 10,
								RateLimiter: promoterv1alpha1.RateLimiter{
									MaxOf: []promoterv1alpha1.RateLimiterTypes{
										{
											Bucket: &promoterv1alpha1.Bucket{
												Qps:    100,
												Bucket: 1000,
											},
										},
										{
											ExponentialFailure: &promoterv1alpha1.ExponentialFailure{
												BaseDelay: metav1.Duration{Duration: time.Millisecond * 5},
												MaxDelay:  metav1.Duration{Duration: time.Minute * 1},
											},
										},
									},
								},
							},
						},
						ChangeTransferPolicy: promoterv1alpha1.ChangeTransferPolicyConfiguration{
							WorkQueue: promoterv1alpha1.WorkQueue{
								RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
								MaxConcurrentReconciles: 10,
								RateLimiter: promoterv1alpha1.RateLimiter{
									MaxOf: []promoterv1alpha1.RateLimiterTypes{
										{
											Bucket: &promoterv1alpha1.Bucket{
												Qps:    100,
												Bucket: 1000,
											},
										},
										{
											ExponentialFailure: &promoterv1alpha1.ExponentialFailure{
												BaseDelay: metav1.Duration{Duration: time.Millisecond * 5},
												MaxDelay:  metav1.Duration{Duration: time.Minute * 1},
											},
										},
									},
								},
							},
						},
						PullRequest: promoterv1alpha1.PullRequestConfiguration{
							Template: promoterv1alpha1.PullRequestTemplate{
								Title:       "Promote {{ trunc 7 .ChangeTransferPolicy.Status.Proposed.Dry.Sha }} to `{{ .ChangeTransferPolicy.Spec.ActiveBranch }}`",
								Description: "This PR is promoting the environment branch `{{ .ChangeTransferPolicy.Spec.ActiveBranch }}` which is currently on dry sha {{ .ChangeTransferPolicy.Status.Active.Dry.Sha }} to dry sha {{ .ChangeTransferPolicy.Status.Proposed.Dry.Sha }}.",
							},
							WorkQueue: promoterv1alpha1.WorkQueue{
								RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
								MaxConcurrentReconciles: 10,
								RateLimiter: promoterv1alpha1.RateLimiter{
									MaxOf: []promoterv1alpha1.RateLimiterTypes{
										{
											Bucket: &promoterv1alpha1.Bucket{
												Qps:    100,
												Bucket: 1000,
											},
										},
										{
											ExponentialFailure: &promoterv1alpha1.ExponentialFailure{
												BaseDelay: metav1.Duration{Duration: time.Millisecond * 5},
												MaxDelay:  metav1.Duration{Duration: time.Minute * 1},
											},
										},
									},
								},
							},
						},
						CommitStatus: promoterv1alpha1.CommitStatusConfiguration{
							WorkQueue: promoterv1alpha1.WorkQueue{
								RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
								MaxConcurrentReconciles: 10,
								RateLimiter: promoterv1alpha1.RateLimiter{
									MaxOf: []promoterv1alpha1.RateLimiterTypes{
										{
											Bucket: &promoterv1alpha1.Bucket{
												Qps:    100,
												Bucket: 1000,
											},
										},
										{
											ExponentialFailure: &promoterv1alpha1.ExponentialFailure{
												BaseDelay: metav1.Duration{Duration: time.Millisecond * 5},
												MaxDelay:  metav1.Duration{Duration: time.Minute * 1},
											},
										},
									},
								},
							},
						},
						ArgoCDCommitStatus: promoterv1alpha1.ArgoCDCommitStatusConfiguration{
							WorkQueue: promoterv1alpha1.WorkQueue{
								RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
								MaxConcurrentReconciles: 10,
								RateLimiter: promoterv1alpha1.RateLimiter{
									MaxOf: []promoterv1alpha1.RateLimiterTypes{
										{
											Bucket: &promoterv1alpha1.Bucket{
												Qps:    100,
												Bucket: 1000,
											},
										},
										{
											ExponentialFailure: &promoterv1alpha1.ExponentialFailure{
												BaseDelay: metav1.Duration{Duration: time.Millisecond * 5},
												MaxDelay:  metav1.Duration{Duration: time.Minute * 1},
											},
										},
									},
								},
							},
						},
						TimedCommitStatus: promoterv1alpha1.TimedCommitStatusConfiguration{
							WorkQueue: promoterv1alpha1.WorkQueue{
								RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
								MaxConcurrentReconciles: 10,
								RateLimiter: promoterv1alpha1.RateLimiter{
									MaxOf: []promoterv1alpha1.RateLimiterTypes{
										{
											Bucket: &promoterv1alpha1.Bucket{
												Qps:    100,
												Bucket: 1000,
											},
										},
										{
											ExponentialFailure: &promoterv1alpha1.ExponentialFailure{
												BaseDelay: metav1.Duration{Duration: time.Millisecond * 5},
												MaxDelay:  metav1.Duration{Duration: time.Minute * 1},
											},
										},
									},
								},
							},
						},
						GitCommitStatus: promoterv1alpha1.GitCommitStatusConfiguration{
							WorkQueue: promoterv1alpha1.WorkQueue{
								RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
								MaxConcurrentReconciles: 10,
								RateLimiter: promoterv1alpha1.RateLimiter{
									MaxOf: []promoterv1alpha1.RateLimiterTypes{
										{
											Bucket: &promoterv1alpha1.Bucket{
												Qps:    100,
												Bucket: 1000,
											},
										},
										{
											ExponentialFailure: &promoterv1alpha1.ExponentialFailure{
												BaseDelay: metav1.Duration{Duration: time.Millisecond * 5},
												MaxDelay:  metav1.Duration{Duration: time.Minute * 1},
											},
										},
									},
								},
							},
						},
						WebRequestCommitStatus: promoterv1alpha1.WebRequestCommitStatusConfiguration{
							WorkQueue: promoterv1alpha1.WorkQueue{
								RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
								MaxConcurrentReconciles: 10,
								RateLimiter: promoterv1alpha1.RateLimiter{
									MaxOf: []promoterv1alpha1.RateLimiterTypes{
										{
											Bucket: &promoterv1alpha1.Bucket{
												Qps:    100,
												Bucket: 1000,
											},
										},
										{
											ExponentialFailure: &promoterv1alpha1.ExponentialFailure{
												BaseDelay: metav1.Duration{Duration: time.Millisecond * 5},
												MaxDelay:  metav1.Duration{Duration: time.Minute * 1},
											},
										},
									},
								},
							},
						},
					},
				}
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
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})
})
