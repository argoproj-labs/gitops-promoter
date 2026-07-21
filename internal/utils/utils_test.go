package utils_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
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
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = BeforeEach(func() {
	settings.ResetControllerInstanceIDForTest()
	settings.SetControllerInstanceIDForTest(nil)
})

var _ = AfterEach(func() {
	settings.ResetControllerInstanceIDForTest()
})

var (
	_ utils.PromoterResource = &promoterv1alpha1.PromotionStrategy{}
	_ utils.PromoterResource = &promoterv1alpha1.ChangeTransferPolicy{}
	_ utils.PromoterResource = &promoterv1alpha1.CommitStatus{}
	_ utils.PromoterResource = &promoterv1alpha1.PullRequest{}
	_ utils.PromoterResource = &promoterv1alpha1.ScmProvider{}
	_ utils.PromoterResource = &promoterv1alpha1.ClusterScmProvider{}
	_ utils.PromoterResource = &promoterv1alpha1.GitRepository{}
	_ utils.PromoterResource = &promoterv1alpha1.TimedCommitStatus{}
	_ utils.PromoterResource = &promoterv1alpha1.ScheduledCommitStatus{}
	_ utils.PromoterResource = &promoterv1alpha1.GitCommitStatus{}
	_ utils.PromoterResource = &promoterv1alpha1.WebRequestCommitStatus{}
	_ utils.PromoterResource = &promoterv1alpha1.ArgoCDCommitStatus{}
	_ utils.PromoterResource = &promoterv1alpha1.ControllerConfiguration{}
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
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, nil, &err, nil)
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
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, nil, &err, nil)
			err = errors.New("test error message")
		}()

		// The error should be preserved
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("test error message"))
	})

	It("should stamp status.instanceID from settings.ControllerInstanceID", func() {
		var err error
		const instanceID = "wave-0"
		settings.SetControllerInstanceIDForTest(ptr.To(instanceID))
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).Build()
		Expect(fakeClient.Create(ctx, obj)).To(Succeed())

		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, nil, &err, nil)
		}()

		Expect(err).NotTo(HaveOccurred())
		updated := &promoterv1alpha1.PromotionStrategy{}
		Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(obj), updated)).To(Succeed())
		Expect(updated.Status.InstanceID).NotTo(BeNil())
		Expect(*updated.Status.InstanceID).To(Equal(instanceID))
	})

	It("should handle successful reconciliation without error", func() {
		var err error
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).Build()

		// Create the object in the fake client so the SSA status apply can target it
		Expect(fakeClient.Create(ctx, obj)).To(Succeed())

		// This function will complete successfully
		func() {
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, nil, &err, nil)
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
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, &result, &err, nil)
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
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, &result, &err, nil)
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
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, &result, &err, nil)
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
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, nil, &err, nil)
			// No reconciliation error; the status apply returns NotFound.
		}()

		// NotFound on status apply is treated as "object deleted concurrently", not an error.
		Expect(err).ToNot(HaveOccurred())
	})
})

var _ = Describe("HandleReconciliationResult event emission", func() {
	var (
		ctx        context.Context
		obj        *promoterv1alpha1.PromotionStrategy
		recorder   *events.FakeRecorder
		scheme     *runtime.Scheme
		fakeClient client.Client
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
		fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(obj).Build()
		Expect(fakeClient.Create(ctx, obj)).To(Succeed())
	})

	drainEvents := func() []string {
		var drained []string
		for {
			select {
			case e := <-recorder.Events:
				drained = append(drained, e)
			default:
				return drained
			}
		}
	}

	Context("When the status apply succeeds", func() {
		It("emits an event when there is no previous Ready condition", func() {
			var err error
			func() {
				defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, nil, &err, nil)
			}()

			emitted := drainEvents()
			Expect(emitted).To(HaveLen(1))
			Expect(emitted[0]).To(ContainSubstring(string(conditions.ReconciliationSuccess)))
		})

		It("does not emit an event when the Ready condition did not transition", func() {
			var err error
			previousReady := &metav1.Condition{
				Type:    string(conditions.Ready),
				Status:  metav1.ConditionTrue,
				Reason:  string(conditions.ReconciliationSuccess),
				Message: "Reconciliation successful",
			}
			func() {
				defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, nil, &err, &previousReady)
			}()

			Expect(drainEvents()).To(BeEmpty())
		})

		It("emits a Warning event when the Ready condition transitions from True to False", func() {
			var err error
			previousReady := &metav1.Condition{
				Type:    string(conditions.Ready),
				Status:  metav1.ConditionTrue,
				Reason:  string(conditions.ReconciliationSuccess),
				Message: "Reconciliation successful",
			}
			func() {
				defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, nil, &err, &previousReady)
				err = errors.New("something broke")
			}()

			emitted := drainEvents()
			Expect(emitted).To(HaveLen(1))
			Expect(emitted[0]).To(ContainSubstring("Warning"))
			Expect(emitted[0]).To(ContainSubstring(string(conditions.ReconciliationError)))
		})

		It("does not emit an event when reconciliation keeps failing with the same reason", func() {
			var err error
			// Message intentionally differs from what this reconcile will produce: message-only
			// changes are not transitions, because failure messages often vary per attempt.
			previousReady := &metav1.Condition{
				Type:    string(conditions.Ready),
				Status:  metav1.ConditionFalse,
				Reason:  string(conditions.ReconciliationError),
				Message: "Reconciliation failed: previous attempt",
			}
			func() {
				defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, nil, &err, &previousReady)
				err = errors.New("something broke again")
			}()

			Expect(drainEvents()).To(BeEmpty())
		})

		It("captures previousReady late, after the defer is registered", func() {
			var err error
			var previousReady *metav1.Condition
			func() {
				// The defer is registered while previousReady is still nil, mirroring how
				// controllers register the defer before calling RemoveReadyCondition.
				defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, testFieldOwner, nil, &err, &previousReady)
				previousReady = &metav1.Condition{
					Type:    string(conditions.Ready),
					Status:  metav1.ConditionTrue,
					Reason:  string(conditions.ReconciliationSuccess),
					Message: "Reconciliation successful",
				}
			}()

			Expect(drainEvents()).To(BeEmpty())
		})
	})

	Context("When the status apply fails", func() {
		It("emits the fallback Ready condition, not the in-memory success, when only the conditions-only apply succeeds", func() {
			var err error
			// Fail the first (full) status apply and let the second (conditions-only fallback)
			// succeed. The persisted condition is then Ready=False/ReconciliationError even
			// though the reconcile itself succeeded in memory.
			patchCalls := 0
			failFirstPatchClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(obj).
				WithInterceptorFuncs(interceptor.Funcs{
					SubResourcePatch: func(ctx context.Context, c client.Client, subResourceName string, o client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
						patchCalls++
						if patchCalls == 1 {
							return apierrors.NewInternalError(errors.New("simulated full status apply failure"))
						}
						return c.Status().Patch(ctx, o, patch, opts...)
					},
				}).
				Build()
			// obj was already created in the shared fakeClient by BeforeEach; clear the
			// ResourceVersion so it can be created in this spec-local client too.
			obj.ResourceVersion = ""
			Expect(failFirstPatchClient.Create(ctx, obj)).To(Succeed())

			previousReady := &metav1.Condition{
				Type:    string(conditions.Ready),
				Status:  metav1.ConditionTrue,
				Reason:  string(conditions.ReconciliationSuccess),
				Message: "Reconciliation successful",
			}
			func() {
				defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, failFirstPatchClient, recorder, testFieldOwner, nil, &err, &previousReady)
				// No reconcile error; the full status apply fails, the fallback succeeds.
			}()

			Expect(err).To(HaveOccurred())
			emitted := drainEvents()
			Expect(emitted).To(HaveLen(1))
			Expect(emitted[0]).To(ContainSubstring("Warning"))
			Expect(emitted[0]).To(ContainSubstring(string(conditions.ReconciliationError)))
			Expect(emitted[0]).NotTo(ContainSubstring(string(conditions.ReconciliationSuccess)))
		})

		It("does not emit an event when nothing was persisted", func() {
			var err error
			// Both the full apply and the conditions-only fallback fail: the object still has
			// its old Ready condition, so no event should announce a state that never landed.
			alwaysFailClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(obj).
				WithInterceptorFuncs(interceptor.Funcs{
					SubResourcePatch: func(_ context.Context, _ client.Client, _ string, _ client.Object, _ client.Patch, _ ...client.SubResourcePatchOption) error {
						return apierrors.NewInternalError(errors.New("simulated status apply failure"))
					},
				}).
				Build()
			// obj was already created in the shared fakeClient by BeforeEach; clear the
			// ResourceVersion so it can be created in this spec-local client too.
			obj.ResourceVersion = ""
			Expect(alwaysFailClient.Create(ctx, obj)).To(Succeed())

			func() {
				defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, alwaysFailClient, recorder, testFieldOwner, nil, &err, nil)
			}()

			Expect(err).To(HaveOccurred())
			Expect(drainEvents()).To(BeEmpty())
		})
	})
})

var _ = Describe("RemoveReadyCondition", func() {
	Context("When removing the Ready condition", func() {
		It("returns nil when no Ready condition exists", func() {
			obj := &promoterv1alpha1.PromotionStrategy{}
			Expect(utils.RemoveReadyCondition(obj)).To(BeNil())
		})

		It("removes the Ready condition and returns a copy of it", func() {
			obj := &promoterv1alpha1.PromotionStrategy{}
			meta.SetStatusCondition(obj.GetConditions(), metav1.Condition{
				Type:               string(conditions.Ready),
				Status:             metav1.ConditionTrue,
				Reason:             string(conditions.ReconciliationSuccess),
				Message:            "Reconciliation successful",
				ObservedGeneration: 3,
			})

			prev := utils.RemoveReadyCondition(obj)

			Expect(prev).ToNot(BeNil())
			Expect(prev.Status).To(Equal(metav1.ConditionTrue))
			Expect(prev.Reason).To(Equal(string(conditions.ReconciliationSuccess)))
			Expect(prev.ObservedGeneration).To(Equal(int64(3)))
			Expect(meta.FindStatusCondition(*obj.GetConditions(), string(conditions.Ready))).To(BeNil())
		})
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
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, constants.ArgoCDCommitStatusControllerFieldOwner, &result, &err, nil)
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
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, constants.ArgoCDCommitStatusControllerFieldOwner, &result, &err, nil)
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
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, constants.ArgoCDCommitStatusControllerFieldOwner, nil, &err, nil)
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
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj2, fakeClient, recorder, constants.ArgoCDCommitStatusControllerFieldOwner, &result, &err, nil)
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
			defer utils.HandleReconciliationResult(ctx, metav1.Now().Time, obj, fakeClient, recorder, constants.ArgoCDCommitStatusControllerFieldOwner, &result, &err, nil)
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

var _ = Describe("EnqueueChangeTransferPolicies", func() {
	var (
		ctx      context.Context
		ps       *promoterv1alpha1.PromotionStrategy
		enqueued []string // collects "namespace/name" pairs passed to enqueueCTP
	)

	BeforeEach(func() {
		ctx = context.Background()
		ps = &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-strategy",
				Namespace: "my-namespace",
			},
		}
		enqueued = nil
	})

	It("enqueues the expected CTP names for each transitioned branch", func() {
		branches := []string{"main", "staging"}
		utils.EnqueueChangeTransferPolicies(ctx, func(namespace, name string) {
			enqueued = append(enqueued, namespace+"/"+name)
		}, ps, branches, "validation transition")

		expected := []string{
			"my-namespace/" + utils.KubeSafeUniqueName(utils.GetChangeTransferPolicyName("my-strategy", "main")),
			"my-namespace/" + utils.KubeSafeUniqueName(utils.GetChangeTransferPolicyName("my-strategy", "staging")),
		}
		Expect(enqueued).To(Equal(expected))
	})

	It("does nothing when transitionedBranches is empty", func() {
		utils.EnqueueChangeTransferPolicies(ctx, func(namespace, name string) {
			enqueued = append(enqueued, namespace+"/"+name)
		}, ps, nil, "validation transition")

		Expect(enqueued).To(BeEmpty())
	})

	It("does not panic when enqueueCTP is nil", func() {
		Expect(func() {
			utils.EnqueueChangeTransferPolicies(ctx, nil, ps, []string{"main"}, "validation transition")
		}).NotTo(Panic())
	})

	It("uses the CTP name derived from the promotion strategy name and branch", func() {
		var capturedName string
		utils.EnqueueChangeTransferPolicies(ctx, func(namespace, name string) {
			capturedName = name
		}, ps, []string{"main"}, "validation transition")

		expected := utils.KubeSafeUniqueName(utils.GetChangeTransferPolicyName("my-strategy", "main"))
		Expect(capturedName).To(Equal(expected))
	})
})

var _ = Describe("API error helpers", func() {
	It("extracts the innermost NotFound StatusDetails from a wrapped client error", func() {
		inner := apierrors.NewNotFound(
			schema.GroupResource{Group: "promoter.argoproj.io", Resource: "scmproviders"},
			"my-scm",
		)
		err := fmt.Errorf("failed to get ScmProvider and secret: %w", fmt.Errorf("failed to get ScmProvider: %w", inner))

		details, isNotFound := utils.NotFoundInErrorChain(err)
		Expect(isNotFound).To(BeTrue())
		Expect(details).To(Equal(&metav1.StatusDetails{
			Group: "promoter.argoproj.io",
			Kind:  "scmproviders",
			Name:  "my-scm",
		}))
	})

	It("reports NotFound without resource details", func() {
		err := fmt.Errorf("wrap: %w", &apierrors.StatusError{
			ErrStatus: metav1.Status{
				Status:  metav1.StatusFailure,
				Code:    404,
				Reason:  metav1.StatusReasonNotFound,
				Message: "not found",
			},
		})

		details, isNotFound := utils.NotFoundInErrorChain(err)
		Expect(isNotFound).To(BeTrue())
		Expect(details).To(BeNil())
	})

	It("returns false for non-NotFound errors", func() {
		err := errors.New("connection refused")
		details, isNotFound := utils.NotFoundInErrorChain(err)
		Expect(isNotFound).To(BeFalse())
		Expect(details).To(BeNil())
	})
})
