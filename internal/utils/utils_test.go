package utils_test

import (
	"context"
	"errors"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var _ = Describe("test rendering a template", func() {
	tests := map[string]struct {
		testdata []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase
		result   bool
	}{
		"all success": {
			testdata: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
				{Key: "test1", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
				{Key: "test1", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
				{Key: "test1", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
			},
			result: true,
		},
		"one pending": {
			testdata: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
				{Key: "test1", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
				{Key: "test1", Phase: string(promoterv1alpha1.CommitPhasePending)},
				{Key: "test1", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
			},
			result: false,
		},
		"one failure": {
			testdata: []promoterv1alpha1.ChangeRequestPolicyCommitStatusPhase{
				{Key: "test1", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
				{Key: "test1", Phase: string(promoterv1alpha1.CommitPhaseFailure)},
				{Key: "test1", Phase: string(promoterv1alpha1.CommitPhaseSuccess)},
			},
			result: false,
		},
	}

	for name, test := range tests {
		It(name, func() {
			result := utils.AreCommitStatusesPassing(test.testdata)
			Expect(result).To(Equal(test.result))
		})
	}
})

var _ = Describe("InheritNotReadyConditionFromObjects", func() {
	var (
		parent    *promoterv1alpha1.PromotionStrategy
		child1    *promoterv1alpha1.CommitStatus
		child2    *promoterv1alpha1.CommitStatus
		childObjs []*promoterv1alpha1.CommitStatus
	)

	BeforeEach(func() {
		parent = &promoterv1alpha1.PromotionStrategy{
			TypeMeta:   metav1.TypeMeta{Kind: "PromotionStrategy"},
			ObjectMeta: metav1.ObjectMeta{Name: "parent", Generation: 1},
		}
		child1 = &promoterv1alpha1.CommitStatus{
			TypeMeta:   metav1.TypeMeta{Kind: "CommitStatus"},
			ObjectMeta: metav1.ObjectMeta{Name: "child1", Generation: 1},
		}
		child2 = &promoterv1alpha1.CommitStatus{
			TypeMeta:   metav1.TypeMeta{Kind: "CommitStatus"},
			ObjectMeta: metav1.ObjectMeta{Name: "child2", Generation: 1},
		}
		childObjs = []*promoterv1alpha1.CommitStatus{child2, child1} // Intentionally out of order to test sorting
	})

	It("should not modify parent Ready condition if all children are Ready", func() {
		meta.SetStatusCondition(child1.GetConditions(), metav1.Condition{
			Type:               string(conditions.Ready),
			Status:             metav1.ConditionTrue,
			ObservedGeneration: 1,
		})
		meta.SetStatusCondition(child2.GetConditions(), metav1.Condition{
			Type:               string(conditions.Ready),
			Status:             metav1.ConditionTrue,
			ObservedGeneration: 1,
		})
		meta.SetStatusCondition(parent.GetConditions(), metav1.Condition{
			Type:   string(conditions.Ready),
			Status: metav1.ConditionFalse,
		})

		utils.InheritNotReadyConditionFromObjects(parent, conditions.CommitStatusesNotReady, childObjs...)

		Expect(meta.FindStatusCondition(*parent.GetConditions(), string(conditions.Ready)).Status).To(Equal(metav1.ConditionFalse))
	})

	It("should set parent Ready condition to False if any child is not Ready", func() {
		meta.SetStatusCondition(child1.GetConditions(), metav1.Condition{
			Type:               string(conditions.Ready),
			Status:             metav1.ConditionTrue,
			ObservedGeneration: 1,
		})
		meta.SetStatusCondition(child2.GetConditions(), metav1.Condition{
			Type:               string(conditions.Ready),
			Status:             metav1.ConditionFalse,
			Reason:             "NotReady",
			Message:            "Child is not ready",
			ObservedGeneration: 1,
		})

		utils.InheritNotReadyConditionFromObjects(parent, conditions.CommitStatusesNotReady, childObjs...)

		readyCondition := meta.FindStatusCondition(*parent.GetConditions(), string(conditions.Ready))
		Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
		Expect(readyCondition.Message).To(Equal(`CommitStatus "child2" is not Ready because "NotReady": Child is not ready`))
	})

	It("should set parent Ready condition to False if a child Ready condition is missing", func() {
		meta.SetStatusCondition(child1.GetConditions(), metav1.Condition{
			Type:               string(conditions.Ready),
			Status:             metav1.ConditionTrue,
			ObservedGeneration: 1,
		})
		// child2 has no Ready condition

		utils.InheritNotReadyConditionFromObjects(parent, conditions.CommitStatusesNotReady, childObjs...)

		readyCondition := meta.FindStatusCondition(*parent.GetConditions(), string(conditions.Ready))
		Expect(readyCondition.Status).To(Equal(metav1.ConditionUnknown))
		Expect(readyCondition.Message).To(Equal(`CommitStatus "child2" Ready condition is missing`))
	})

	It("should set parent Ready condition to False if a child Ready condition is outdated", func() {
		meta.SetStatusCondition(child1.GetConditions(), metav1.Condition{
			Type:               string(conditions.Ready),
			Status:             metav1.ConditionTrue,
			ObservedGeneration: 1,
		})
		meta.SetStatusCondition(child2.GetConditions(), metav1.Condition{
			Type:               string(conditions.Ready),
			Status:             metav1.ConditionTrue,
			ObservedGeneration: 0, // Simulate outdated condition
		})

		utils.InheritNotReadyConditionFromObjects(parent, conditions.CommitStatusesNotReady, childObjs...)

		readyCondition := meta.FindStatusCondition(*parent.GetConditions(), string(conditions.Ready))
		Expect(readyCondition.Status).To(Equal(metav1.ConditionUnknown))
		Expect(readyCondition.Message).To(Equal(`CommitStatus "child2" Ready condition is not up to date`))
	})

	It("should always take the first not ready condition, ordered alphabetically by child name", func() {
		meta.SetStatusCondition(child1.GetConditions(), metav1.Condition{
			Type:               string(conditions.Ready),
			Status:             metav1.ConditionFalse,
			Reason:             "NotReady1",
			Message:            "Child1 is not ready",
			ObservedGeneration: 1,
		})
		meta.SetStatusCondition(child2.GetConditions(), metav1.Condition{
			Type:               string(conditions.Ready),
			Status:             metav1.ConditionFalse,
			Reason:             "NotReady2",
			Message:            "Child2 is not ready",
			ObservedGeneration: 1,
		})

		utils.InheritNotReadyConditionFromObjects(parent, conditions.CommitStatusesNotReady, childObjs...)

		readyCondition := meta.FindStatusCondition(*parent.GetConditions(), string(conditions.Ready))
		Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
		Expect(readyCondition.Message).To(Equal(`CommitStatus "child1" is not Ready because "NotReady1": Child1 is not ready`))
	})
})

var _ = Describe("HandleReconciliationResult panic recovery", func() {
	var (
		ctx      context.Context
		obj      *promoterv1alpha1.PromotionStrategy
		recorder events.EventRecorder
		scheme   *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		obj = &promoterv1alpha1.PromotionStrategy{
			TypeMeta: metav1.TypeMeta{
				Kind:       "PromotionStrategy",
				APIVersion: "promoter.argoproj.io/v1alpha1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-strategy",
				Namespace:  "default",
				Generation: 1,
			},
		}
		scheme = runtime.NewScheme()
		_ = promoterv1alpha1.AddToScheme(scheme)
		recorder = events.NewFakeRecorder(10)
	})

	It("should recover from panic and convert it to an error", func() {
		var err error
		// We use fakeclient here since it's virtually impossible to trigger a panic otherwise. Don't spread
		// this use to other tests if at all avoidable.
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).Build()

		// This function will panic, and HandleReconciliationResult should recover from it
		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, &err)
			panic("test panic message")
		}()

		// The panic should have been caught and converted to an error
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("panic in reconciliation"))
		Expect(err.Error()).To(ContainSubstring("test panic message"))
	})

	It("should handle normal errors without panicking", func() {
		var err error
		// We use fakeclient here since it's virtually impossible to trigger a panic otherwise. Don't spread
		// this use to other tests if at all avoidable.
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).Build()

		// Create the object in the fake client so HandleReconciliationResult can update it
		Expect(fakeClient.Create(ctx, obj)).To(Succeed())

		// This function will return an error normally
		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, &err)
			err = errors.New("test error message")
		}()

		// The error should be preserved
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("test error message"))
	})

	It("should handle successful reconciliation without error", func() {
		var err error
		// We use fakeclient here since it's virtually impossible to trigger a panic otherwise. Don't spread
		// this use to other tests if at all avoidable.
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).Build()

		// Create the object in the fake client so HandleReconciliationResult can update it
		Expect(fakeClient.Create(ctx, obj)).To(Succeed())

		// This function will complete successfully
		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, &err)
			// No error or panic
		}()

		// No error should be set
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("HandleReconciliationResult fallback status update", func() {
	var (
		ctx      context.Context
		obj      *promoterv1alpha1.ArgoCDCommitStatus
		recorder events.EventRecorder
		scheme   *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		obj = &promoterv1alpha1.ArgoCDCommitStatus{
			TypeMeta: metav1.TypeMeta{
				Kind:       "ArgoCDCommitStatus",
				APIVersion: "promoter.argoproj.io/v1alpha1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-commit-status",
				Namespace:  "default",
				Generation: 1,
			},
		}
		scheme = runtime.NewScheme()
		_ = promoterv1alpha1.AddToScheme(scheme)
		recorder = events.NewFakeRecorder(10)
	})

	It("should use fallback when full status update fails", func() {
		var err error
		updateCallCount := 0

		// Create a fake client with an interceptor that fails the first status update
		// but allows the second (fallback) update to succeed
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(obj).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourceUpdate: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
					updateCallCount++
					if updateCallCount == 1 {
						// First update (full status) fails with a validation error
						return apierrors.NewInvalid(
							schema.GroupKind{Group: "promoter.argoproj.io", Kind: "ArgoCDCommitStatus"},
							obj.GetName(),
							nil,
						)
					}
					// Second update (fallback with only condition) succeeds
					return client.SubResource(subResourceName).Update(ctx, obj, opts...)
				},
			}).
			Build()

		// Create the object in the fake client
		Expect(fakeClient.Create(ctx, obj)).To(Succeed())

		// Simulate a successful reconciliation followed by a status update failure
		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, &err)
			// No reconciliation error - reconciliation succeeded
		}()

		// The error should indicate that the full status update failed but fallback succeeded
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to update full status"))
		Expect(err.Error()).To(ContainSubstring("updating only the Ready condition succeeded"))

		// Verify that we attempted two updates: full status, then fallback
		Expect(updateCallCount).To(Equal(2))

		// Verify the Ready condition was set by fetching a fresh copy
		updated := &promoterv1alpha1.ArgoCDCommitStatus{}
		Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(obj), updated)).To(Succeed())
		readyCondition := meta.FindStatusCondition(*updated.GetConditions(), string(conditions.Ready))
		Expect(readyCondition).ToNot(BeNil())
		Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
		Expect(readyCondition.Reason).To(Equal(string(conditions.ReconciliationError)))
		Expect(readyCondition.Message).To(ContainSubstring("Reconciliation succeeded but failed to update status"))
	})

	It("should report error when both full update and fallback fail", func() {
		var err error

		// Create a fake client that fails all status updates
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(obj).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourceUpdate: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
					// All updates fail
					return apierrors.NewInvalid(
						schema.GroupKind{Group: "promoter.argoproj.io", Kind: "ArgoCDCommitStatus"},
						obj.GetName(),
						nil,
					)
				},
			}).
			Build()

		// Create the object in the fake client
		Expect(fakeClient.Create(ctx, obj)).To(Succeed())

		// Simulate a successful reconciliation
		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, &err)
			// No reconciliation error
		}()

		// The error should indicate that both updates failed
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("updating only the Ready condition also failed"))
	})

	It("should include original reconciliation error in fallback condition", func() {
		var err error
		updateCallCount := 0

		// Create a fake client that fails the first update but allows the fallback
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(obj).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourceUpdate: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
					updateCallCount++
					if updateCallCount == 1 {
						return errors.New("simulated status update failure")
					}
					// Fallback succeeds
					return client.SubResource(subResourceName).Update(ctx, obj, opts...)
				},
			}).
			Build()

		// Create the object in the fake client
		Expect(fakeClient.Create(ctx, obj)).To(Succeed())

		// Simulate a reconciliation that returns an error
		reconcileErr := errors.New("reconciliation failed for test")
		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, &err)
			err = reconcileErr
		}()

		// The error should mention both the reconciliation error and that updating only the condition succeeded
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("reconciliation failed for test"))
		Expect(err.Error()).To(ContainSubstring("updating only the Ready condition succeeded"))

		// Verify the condition includes the original reconciliation error
		updated := &promoterv1alpha1.ArgoCDCommitStatus{}
		Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(obj), updated)).To(Succeed())
		readyCondition := meta.FindStatusCondition(*updated.GetConditions(), string(conditions.Ready))
		Expect(readyCondition).ToNot(BeNil())
		Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
		Expect(readyCondition.Message).To(ContainSubstring("Reconciliation failed"))
		Expect(readyCondition.Message).To(ContainSubstring("reconciliation failed for test"))
	})
})
