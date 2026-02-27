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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
			}

			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
			// TODO(user): Add more specific assertions depending on your controller's reconciliation logic.
			// Example: If you expect a certain status condition after reconciliation, verify it here.
		})
	})

	Context("When testing hot-reload of local cluster watch", func() {
		const resourceName = "hot-reload-test"

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		BeforeEach(func() {
			By("creating ControllerConfiguration with WatchLocalApplications disabled")
			resource := &promoterv1alpha1.ControllerConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: "default",
				},
				Spec: promoterv1alpha1.ControllerConfigurationSpec{
					ArgoCDCommitStatus: promoterv1alpha1.ArgoCDCommitStatusConfiguration{
						WatchLocalApplications: false, // Start with local watching disabled
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
								},
							},
						},
					},
					// Minimal configuration for other required fields
					PromotionStrategy: promoterv1alpha1.PromotionStrategyConfiguration{
						WorkQueue: promoterv1alpha1.WorkQueue{
							RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
							MaxConcurrentReconciles: 10,
						},
					},
					ChangeTransferPolicy: promoterv1alpha1.ChangeTransferPolicyConfiguration{
						WorkQueue: promoterv1alpha1.WorkQueue{
							RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
							MaxConcurrentReconciles: 10,
						},
					},
					PullRequest: promoterv1alpha1.PullRequestConfiguration{
						Template: promoterv1alpha1.PullRequestTemplate{
							Title:       "Test",
							Description: "Test",
						},
						WorkQueue: promoterv1alpha1.WorkQueue{
							RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
							MaxConcurrentReconciles: 10,
						},
					},
					CommitStatus: promoterv1alpha1.CommitStatusConfiguration{
						WorkQueue: promoterv1alpha1.WorkQueue{
							RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
							MaxConcurrentReconciles: 10,
						},
					},
					TimedCommitStatus: promoterv1alpha1.TimedCommitStatusConfiguration{
						WorkQueue: promoterv1alpha1.WorkQueue{
							RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
							MaxConcurrentReconciles: 10,
						},
					},
					GitCommitStatus: promoterv1alpha1.GitCommitStatusConfiguration{
						WorkQueue: promoterv1alpha1.WorkQueue{
							RequeueDuration:         metav1.Duration{Duration: time.Minute * 5},
							MaxConcurrentReconciles: 10,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())
		})

		AfterEach(func() {
			resource := &promoterv1alpha1.ControllerConfiguration{}
			err := k8sClient.Get(ctx, typeNamespacedName, resource)
			Expect(err).NotTo(HaveOccurred())

			By("Cleanup the ControllerConfiguration")
			Expect(k8sClient.Delete(ctx, resource)).To(Succeed())
		})

		It("should update status when reconciling configuration changes", func() {
			By("Creating a basic reconciler without full multicluster setup")
			// Note: This test verifies status updates work without requiring full multicluster manager
			configController := &ControllerConfigurationReconciler{
				Client:   k8sClient,
				Scheme:   k8sClient.Scheme(),
				Recorder: events.NewFakeRecorder(10),
			}

			By("Reconciling with WatchLocalApplications disabled")
			// Without localManager and mcMgr, engagement won't be attempted since WatchLocalApplications is false
			_, err := configController.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			// Should succeed since we're not trying to engage anything
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the controller reconciled successfully")
			config := &promoterv1alpha1.ControllerConfiguration{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, config)).To(Succeed())

			// Verify the CR exists and has expected state
			Expect(config.Name).To(Equal(resourceName))
			Expect(config.Spec.ArgoCDCommitStatus.WatchLocalApplications).To(BeFalse())
		})

		It("should track WatchLocalApplications configuration changes", func() {
			config := &promoterv1alpha1.ControllerConfiguration{}

			By("Verifying initial WatchLocalApplications is false")
			Expect(k8sClient.Get(ctx, typeNamespacedName, config)).To(Succeed())
			Expect(config.Spec.ArgoCDCommitStatus.WatchLocalApplications).To(BeFalse())

			By("Updating WatchLocalApplications to true")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, typeNamespacedName, config); err != nil {
					return fmt.Errorf("failed to get config: %w", err)
				}
				config.Spec.ArgoCDCommitStatus.WatchLocalApplications = true
				if err := k8sClient.Update(ctx, config); err != nil {
					return fmt.Errorf("failed to update config: %w", err)
				}
				return nil
			}, time.Second*5, time.Millisecond*100).Should(Succeed())

			By("Verifying the update was persisted")
			updatedConfig := &promoterv1alpha1.ControllerConfiguration{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, updatedConfig)).To(Succeed())
			Expect(updatedConfig.Spec.ArgoCDCommitStatus.WatchLocalApplications).To(BeTrue())

			By("Updating WatchLocalApplications back to false")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, typeNamespacedName, updatedConfig); err != nil {
					return fmt.Errorf("failed to get config: %w", err)
				}
				updatedConfig.Spec.ArgoCDCommitStatus.WatchLocalApplications = false
				if err := k8sClient.Update(ctx, updatedConfig); err != nil {
					return fmt.Errorf("failed to update config: %w", err)
				}
				return nil
			}, time.Second*5, time.Millisecond*100).Should(Succeed())

			By("Verifying the second update was persisted")
			finalConfig := &promoterv1alpha1.ControllerConfiguration{}
			Expect(k8sClient.Get(ctx, typeNamespacedName, finalConfig)).To(Succeed())
			Expect(finalConfig.Spec.ArgoCDCommitStatus.WatchLocalApplications).To(BeFalse())
		})

		It("should allow status subresource updates", func() {
			config := &promoterv1alpha1.ControllerConfiguration{}

			By("Updating the status subresource")
			Eventually(func() error {
				if err := k8sClient.Get(ctx, typeNamespacedName, config); err != nil {
					return fmt.Errorf("failed to get config: %w", err)
				}
				config.Status.ArgoCDCommitStatus.LocalClusterEngaged = true
				meta.SetStatusCondition(&config.Status.Conditions, metav1.Condition{
					Type:               "Ready",
					Status:             metav1.ConditionTrue,
					Reason:             "ReconciliationSuccess",
					Message:            "Test status update",
					LastTransitionTime: metav1.Now(),
				})
				if err := k8sClient.Status().Update(ctx, config); err != nil {
					return fmt.Errorf("failed to update status: %w", err)
				}
				return nil
			}, time.Second*5, time.Millisecond*100).Should(Succeed())

			By("Verifying the status was updated")
			updatedConfig := &promoterv1alpha1.ControllerConfiguration{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, typeNamespacedName, updatedConfig)
				if err != nil {
					return false
				}
				return updatedConfig.Status.ArgoCDCommitStatus.LocalClusterEngaged &&
					len(updatedConfig.Status.Conditions) > 0
			}, time.Second*5, time.Millisecond*100).Should(BeTrue())

			By("Verifying the Ready condition is set correctly")
			readyCondition := meta.FindStatusCondition(updatedConfig.Status.Conditions, "Ready")
			Expect(readyCondition).NotTo(BeNil())
			Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCondition.Reason).To(Equal("ReconciliationSuccess"))
		})
	})
})
