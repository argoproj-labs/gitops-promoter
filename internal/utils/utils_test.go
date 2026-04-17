package utils_test

import (
	"context"
	"errors"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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

const testFieldOwner = constants.PromotionStrategyControllerFieldOwner

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
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, nil, &err)
			panic("test panic message")
		}()

		// The panic should have been caught and converted to an error
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("panic in reconciliation"))
		Expect(err.Error()).To(ContainSubstring("test panic message"))
	})

	It("should handle normal errors without panicking", func() {
		var err error
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).Build()

		// Create the object in the fake client so the SSA status apply can target it
		Expect(fakeClient.Create(ctx, obj)).To(Succeed())

		// This function will return an error normally
		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, nil, &err)
			err = errors.New("test error message")
		}()

		// The error should be preserved
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("test error message"))
	})

	It("should handle successful reconciliation without error", func() {
		var err error
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).Build()

		// Create the object in the fake client so the SSA status apply can target it
		Expect(fakeClient.Create(ctx, obj)).To(Succeed())

		// This function will complete successfully
		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, nil, &err)
			// No error or panic
		}()

		// No error should be set
		Expect(err).ToNot(HaveOccurred())

		// The helper must stamp status.observedGeneration so consumers can detect stale
		// status writes; SSA with ForceOwnership has no optimistic-concurrency guard.
		updated := &promoterv1alpha1.PromotionStrategy{}
		Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(obj), updated)).To(Succeed())
		Expect(updated.Status.ObservedGeneration).To(Equal(obj.Generation))
	})

	It("should clear result when panic occurs with a non-nil result", func() {
		var err error
		result := reconcile.Result{Requeue: true, RequeueAfter: 5 * time.Second}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).Build()

		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, &result, &err)
			panic("test panic message")
		}()

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("panic in reconciliation"))
		// result must be zeroed so the caller doesn't return both a requeue and an error
		Expect(result).To(Equal(reconcile.Result{}))
	})

	It("should clear result when status apply fails", func() {
		var err error
		result := reconcile.Result{Requeue: true, RequeueAfter: 5 * time.Second}
		// Intercept all status patches to force them to fail. Mirrors an apiserver rejecting
		// the SSA patch (e.g. schema validation, RBAC, or similar terminal failure).
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(obj).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(_ context.Context, _ client.Client, _ string, _ client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
					return apierrors.NewInternalError(errors.New("simulated status apply failure"))
				},
			}).
			Build()
		Expect(fakeClient.Create(ctx, obj)).To(Succeed())

		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, &result, &err)
			// No error or panic — HandleReconciliationResult will try (and fail) to apply status.
		}()

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to apply"))
		// result must be zeroed so the caller doesn't return both a requeue and an error
		Expect(result).To(Equal(reconcile.Result{}))
	})

	It("should preserve result when reconciliation succeeds and status apply succeeds", func() {
		var err error
		result := reconcile.Result{RequeueAfter: 5 * time.Second}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).Build()
		Expect(fakeClient.Create(ctx, obj)).To(Succeed())

		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, &result, &err)
			// No error or panic
		}()

		Expect(err).ToNot(HaveOccurred())
		// result should be untouched — HandleReconciliationResult only clears it on error
		Expect(result).To(Equal(reconcile.Result{RequeueAfter: 5 * time.Second}))
	})

	It("should skip fallback when object has been deleted (NotFound)", func() {
		var err error
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(obj).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(_ context.Context, _ client.Client, _ string, _ client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
					return apierrors.NewNotFound(
						schema.GroupResource{Group: "promoter.argoproj.io", Resource: "promotionstrategies"},
						obj.GetName(),
					)
				},
			}).
			Build()

		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, nil, &err)
			// No reconciliation error; the status apply returns NotFound.
		}()

		// NotFound on status apply is treated as "object deleted concurrently", not an error.
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("HandleReconciliationResult fallback status apply", func() {
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

	It("should use fallback when full status apply fails", func() {
		var err error
		patchCallCount := 0

		// First SSA patch (full status) fails with a validation error, the second
		// (conditions-only fallback) is allowed through.
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(obj).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, patchObj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					patchCallCount++
					if patchCallCount == 1 {
						return apierrors.NewInvalid(
							schema.GroupKind{Group: "promoter.argoproj.io", Kind: "ArgoCDCommitStatus"},
							patchObj.GetName(),
							nil,
						)
					}
					return c.SubResource(subResourceName).Patch(ctx, patchObj, patch, opts...)
				},
			}).
			Build()

		Expect(fakeClient.Create(ctx, obj)).To(Succeed())

		// Simulate a successful reconciliation followed by a status apply failure
		result := reconcile.Result{}
		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, constants.ArgoCDCommitStatusControllerFieldOwner, &result, &err)
			// No reconciliation error - reconciliation succeeded
		}()

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("failed to apply full status"))
		Expect(err.Error()).To(ContainSubstring("applying only the Ready condition succeeded"))

		// Two SSA attempts: full status, then conditions-only fallback
		Expect(patchCallCount).To(Equal(2))

		// The conditions-only fallback should have landed the Ready=False condition on the object.
		updated := &promoterv1alpha1.ArgoCDCommitStatus{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Name: obj.Name, Namespace: obj.Namespace}, updated)).To(Succeed())
		readyCondition := meta.FindStatusCondition(*updated.GetConditions(), string(conditions.Ready))
		Expect(readyCondition).ToNot(BeNil())
		Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
		Expect(readyCondition.Reason).To(Equal(string(conditions.ReconciliationError)))
		Expect(readyCondition.Message).To(ContainSubstring("Reconciliation succeeded but failed to apply status"))

		// The Ready condition's own ObservedGeneration records the generation the
		// controller attempted to reconcile, even when the full status apply failed.
		Expect(readyCondition.ObservedGeneration).To(Equal(obj.Generation))

		// The top-level status.observedGeneration is deliberately NOT advanced by the
		// conditions-only fallback. It stays pinned to the last successful full apply so
		// consumers can detect that the stored status body is stale. Here there has been
		// no prior successful apply, so it should remain zero.
		Expect(updated.Status.ObservedGeneration).To(BeZero())
	})

	It("should report error when both full apply and fallback fail", func() {
		var err error

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(obj).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(_ context.Context, _ client.Client, _ string, patchObj client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
					// All apply attempts fail
					return apierrors.NewInvalid(
						schema.GroupKind{Group: "promoter.argoproj.io", Kind: "ArgoCDCommitStatus"},
						patchObj.GetName(),
						nil,
					)
				},
			}).
			Build()

		Expect(fakeClient.Create(ctx, obj)).To(Succeed())

		result := reconcile.Result{}
		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, constants.ArgoCDCommitStatusControllerFieldOwner, &result, &err)
			// No reconciliation error
		}()

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("applying only the Ready condition also failed"))
	})

	It("should preserve other status fields when the conditions-only fallback runs", func() {
		var err error
		patchCallCount := 0

		// First full apply succeeds (populating status.applicationsSelected). Second
		// full apply fails; the conditions-only fallback runs. The fields owned by the
		// main FieldOwner on the first apply must survive the fallback because the
		// fallback uses a distinct FieldOwner and doesn't declare them.
		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(obj).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, patchObj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					patchCallCount++
					// Second call is the second reconcile's full apply — fail it so the
					// fallback runs. All other calls pass through.
					if patchCallCount == 2 {
						return apierrors.NewInvalid(
							schema.GroupKind{Group: "promoter.argoproj.io", Kind: "ArgoCDCommitStatus"},
							patchObj.GetName(),
							nil,
						)
					}
					return c.SubResource(subResourceName).Patch(ctx, patchObj, patch, opts...)
				},
			}).
			Build()

		Expect(fakeClient.Create(ctx, obj)).To(Succeed())

		// First reconcile: populate a non-conditions status field via the in-memory
		// object and run the successful full apply through the helper.
		obj.Status.ApplicationsSelected = []promoterv1alpha1.ApplicationsSelected{{
			Namespace: "argocd",
			Name:      "my-app",
			Phase:     promoterv1alpha1.CommitPhaseSuccess,
		}}
		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, constants.ArgoCDCommitStatusControllerFieldOwner, nil, &err)
		}()
		Expect(err).ToNot(HaveOccurred())

		// Confirm the first apply landed the field on the stored object.
		afterFirst := &promoterv1alpha1.ArgoCDCommitStatus{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Name: obj.Name, Namespace: obj.Namespace}, afterFirst)).To(Succeed())
		Expect(afterFirst.Status.ApplicationsSelected).To(HaveLen(1))
		storedObservedGeneration := afterFirst.Status.ObservedGeneration

		// Second reconcile: refetch (to reset managedFields state) and bump the
		// generation so the new full apply writes a different set of fields. The
		// interceptor will reject this full apply, forcing the conditions-only fallback.
		obj2 := &promoterv1alpha1.ArgoCDCommitStatus{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Name: obj.Name, Namespace: obj.Namespace}, obj2)).To(Succeed())
		obj2.Generation = 2
		result := reconcile.Result{}
		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj2, fakeClient, recorder, constants.ArgoCDCommitStatusControllerFieldOwner, &result, &err)
		}()
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("applying only the Ready condition succeeded"))

		// The conditions-only fallback must NOT have wiped status.applicationsSelected.
		// The main FieldOwner still owns that field and the fallback owner only declared
		// status.conditions, so the field persists untouched.
		afterFallback := &promoterv1alpha1.ArgoCDCommitStatus{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Name: obj.Name, Namespace: obj.Namespace}, afterFallback)).To(Succeed())
		Expect(afterFallback.Status.ApplicationsSelected).To(HaveLen(1), "fallback must not wipe other status fields")
		Expect(afterFallback.Status.ApplicationsSelected[0].Name).To(Equal("my-app"))

		// Top-level observedGeneration must stay pinned to the last successful apply.
		Expect(afterFallback.Status.ObservedGeneration).To(Equal(storedObservedGeneration))

		// The Ready condition must reflect the latest attempted generation.
		readyCondition := meta.FindStatusCondition(*afterFallback.GetConditions(), string(conditions.Ready))
		Expect(readyCondition).ToNot(BeNil())
		Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
		Expect(readyCondition.ObservedGeneration).To(Equal(int64(2)))
	})

	It("should include original reconciliation error in fallback condition", func() {
		var err error
		patchCallCount := 0

		fakeClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithStatusSubresource(obj).
			WithInterceptorFuncs(interceptor.Funcs{
				SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, patchObj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
					patchCallCount++
					if patchCallCount == 1 {
						return errors.New("simulated status apply failure")
					}
					return c.SubResource(subResourceName).Patch(ctx, patchObj, patch, opts...)
				},
			}).
			Build()

		Expect(fakeClient.Create(ctx, obj)).To(Succeed())

		reconcileErr := errors.New("reconciliation failed for test")
		result := reconcile.Result{}
		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, constants.ArgoCDCommitStatusControllerFieldOwner, &result, &err)
			err = reconcileErr
		}()

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("reconciliation failed for test"))
		Expect(err.Error()).To(ContainSubstring("applying only the Ready condition succeeded"))

		// The fallback should have written the Ready condition including the reconcile error.
		updated := &promoterv1alpha1.ArgoCDCommitStatus{}
		Expect(fakeClient.Get(ctx, types.NamespacedName{Name: obj.Name, Namespace: obj.Namespace}, updated)).To(Succeed())
		readyCondition := meta.FindStatusCondition(*updated.GetConditions(), string(conditions.Ready))
		Expect(readyCondition).ToNot(BeNil())
		Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
		Expect(readyCondition.Message).To(ContainSubstring("Reconciliation failed"))
		Expect(readyCondition.Message).To(ContainSubstring("reconciliation failed for test"))
	})
})
