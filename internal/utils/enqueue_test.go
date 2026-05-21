package utils_test

import (
	"context"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("EnqueueCommitStatusGatesForPromotionStrategy", func() {
	var (
		ctx    context.Context
		scheme = utils.GetScheme()
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	Context("with GitCommitStatus resources", func() {
		It("enqueues only gates that reference the given PromotionStrategy", func() {
			ps := &promoterv1alpha1.PromotionStrategy{
				ObjectMeta: metav1.ObjectMeta{Name: "my-strategy", Namespace: "default"},
			}

			matchingGCS := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: "gcs-matching", Namespace: "default"},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "my-strategy"},
					Key:                  "ci",
				},
			}
			otherGCS := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: "gcs-other", Namespace: "default"},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "other-strategy"},
					Key:                  "ci",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(matchingGCS, otherGCS).
				Build()

			requests := utils.EnqueueCommitStatusGatesForPromotionStrategy(
				ctx,
				fakeClient,
				ps,
				&promoterv1alpha1.GitCommitStatusList{},
				func(gcs *promoterv1alpha1.GitCommitStatus) string {
					return gcs.Spec.PromotionStrategyRef.Name
				},
			)

			Expect(requests).To(ConsistOf([]reconcile.Request{
				{NamespacedName: types.NamespacedName{Name: "gcs-matching", Namespace: "default"}},
			}))
		})

		It("returns nil when no gates reference the PromotionStrategy", func() {
			ps := &promoterv1alpha1.PromotionStrategy{
				ObjectMeta: metav1.ObjectMeta{Name: "my-strategy", Namespace: "default"},
			}

			unrelatedGCS := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: "gcs-unrelated", Namespace: "default"},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "other-strategy"},
					Key:                  "ci",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(unrelatedGCS).
				Build()

			requests := utils.EnqueueCommitStatusGatesForPromotionStrategy(
				ctx,
				fakeClient,
				ps,
				&promoterv1alpha1.GitCommitStatusList{},
				func(gcs *promoterv1alpha1.GitCommitStatus) string {
					return gcs.Spec.PromotionStrategyRef.Name
				},
			)

			Expect(requests).To(BeNil())
		})

		It("does not enqueue gates from a different namespace", func() {
			ps := &promoterv1alpha1.PromotionStrategy{
				ObjectMeta: metav1.ObjectMeta{Name: "my-strategy", Namespace: "default"},
			}

			sameNsGCS := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: "gcs-same-ns", Namespace: "default"},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "my-strategy"},
					Key:                  "ci",
				},
			}
			otherNsGCS := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: "gcs-other-ns", Namespace: "other"},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "my-strategy"},
					Key:                  "ci",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(sameNsGCS, otherNsGCS).
				Build()

			requests := utils.EnqueueCommitStatusGatesForPromotionStrategy(
				ctx,
				fakeClient,
				ps,
				&promoterv1alpha1.GitCommitStatusList{},
				func(gcs *promoterv1alpha1.GitCommitStatus) string {
					return gcs.Spec.PromotionStrategyRef.Name
				},
			)

			Expect(requests).To(ConsistOf([]reconcile.Request{
				{NamespacedName: types.NamespacedName{Name: "gcs-same-ns", Namespace: "default"}},
			}))
		})

		It("enqueues multiple matching gates", func() {
			ps := &promoterv1alpha1.PromotionStrategy{
				ObjectMeta: metav1.ObjectMeta{Name: "my-strategy", Namespace: "default"},
			}

			gcs1 := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: "gcs-1", Namespace: "default"},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "my-strategy"},
					Key:                  "ci",
				},
			}
			gcs2 := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: "gcs-2", Namespace: "default"},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "my-strategy"},
					Key:                  "lint",
				},
			}
			gcs3 := &promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: "gcs-3", Namespace: "default"},
				Spec: promoterv1alpha1.GitCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "other-strategy"},
					Key:                  "ci",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(gcs1, gcs2, gcs3).
				Build()

			requests := utils.EnqueueCommitStatusGatesForPromotionStrategy(
				ctx,
				fakeClient,
				ps,
				&promoterv1alpha1.GitCommitStatusList{},
				func(gcs *promoterv1alpha1.GitCommitStatus) string {
					return gcs.Spec.PromotionStrategyRef.Name
				},
			)

			Expect(requests).To(ConsistOf(
				reconcile.Request{NamespacedName: types.NamespacedName{Name: "gcs-1", Namespace: "default"}},
				reconcile.Request{NamespacedName: types.NamespacedName{Name: "gcs-2", Namespace: "default"}},
			))
		})
	})

	Context("with TimedCommitStatus resources", func() {
		It("enqueues only gates that reference the given PromotionStrategy", func() {
			ps := &promoterv1alpha1.PromotionStrategy{
				ObjectMeta: metav1.ObjectMeta{Name: "my-strategy", Namespace: "default"},
			}

			matchingTCS := &promoterv1alpha1.TimedCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: "tcs-matching", Namespace: "default"},
				Spec: promoterv1alpha1.TimedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "my-strategy"},
				},
			}
			otherTCS := &promoterv1alpha1.TimedCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: "tcs-other", Namespace: "default"},
				Spec: promoterv1alpha1.TimedCommitStatusSpec{
					PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "other-strategy"},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(matchingTCS, otherTCS).
				Build()

			requests := utils.EnqueueCommitStatusGatesForPromotionStrategy(
				ctx,
				fakeClient,
				ps,
				&promoterv1alpha1.TimedCommitStatusList{},
				func(tcs *promoterv1alpha1.TimedCommitStatus) string {
					return tcs.Spec.PromotionStrategyRef.Name
				},
			)

			Expect(requests).To(ConsistOf([]reconcile.Request{
				{NamespacedName: types.NamespacedName{Name: "tcs-matching", Namespace: "default"}},
			}))
		})
	})
})
