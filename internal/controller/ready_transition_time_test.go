package controller

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	promoterConditions "github.com/argoproj-labs/gitops-promoter/internal/types/conditions"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// A reconcile that changes nothing must not change the object: same resourceVersion, so no watch event for
// owners and watchers. Before the fix, HandleReconciliationResult re-stamped the Ready condition's
// lastTransitionTime on every reconcile, so every status write was a real write.
var _ = Describe("HandleReconciliationResult against the API server", func() {
	// A ControllerConfiguration outside the install namespace is ignored by its controller
	// (controllerConfigurationInstallNamespacePredicate), so the only status writes are the ones made here.
	const namespace = "ready-transition-time"
	const fieldOwner = "ready-transition-time-test"

	It("leaves resourceVersion alone when an unchanged reconcile repeats", func() {
		ctx := context.Background()
		Expect(client.IgnoreAlreadyExists(k8sClient.Create(ctx, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: namespace},
		}))).To(Succeed())
		cc, err := loadShippedControllerConfigurationForTests("ready-transition-time")
		Expect(err).NotTo(HaveOccurred())
		cc.Namespace = namespace
		Expect(k8sClient.Create(ctx, cc)).To(Succeed())
		key := client.ObjectKeyFromObject(cc)
		recorder := events.NewFakeRecorder(20)

		// reconcile mimics a controller: read, clear the Ready condition, optionally touch the status, and let
		// HandleReconciliationResult persist the outcome. It returns what the API server stored.
		reconcile := func(during func(obj *promoterv1alpha1.ControllerConfiguration), reconcileErr error) *promoterv1alpha1.ControllerConfiguration {
			obj := &promoterv1alpha1.ControllerConfiguration{}
			Expect(k8sClient.Get(ctx, key, obj)).To(Succeed())
			previousReady := utils.RemoveReadyCondition(obj)
			func() {
				err := reconcileErr
				defer utils.HandleReconciliationResult(ctx, time.Now(), obj, k8sClient, recorder, fieldOwner, nil, &err, &previousReady)
				if during != nil {
					during(obj)
				}
			}()
			persisted := &promoterv1alpha1.ControllerConfiguration{}
			Expect(k8sClient.Get(ctx, key, persisted)).To(Succeed())
			return persisted
		}
		readyOf := func(obj *promoterv1alpha1.ControllerConfiguration) *metav1.Condition {
			ready := meta.FindStatusCondition(obj.Status.Conditions, string(promoterConditions.Ready))
			Expect(ready).NotTo(BeNil())
			return ready
		}

		By("the first reconcile records Ready=True")
		first := reconcile(nil, nil)
		Expect(readyOf(first).Status).To(Equal(metav1.ConditionTrue))

		// lastTransitionTime is serialized to the second; let the clock move so a re-stamp would be visible.
		time.Sleep(1100 * time.Millisecond)

		By("an unchanged reconcile is a no-op on the server")
		second := reconcile(nil, nil)
		Expect(second.ResourceVersion).To(Equal(first.ResourceVersion))
		Expect(readyOf(second).LastTransitionTime).To(Equal(readyOf(first).LastTransitionTime))

		By("a Ready condition set during the reconcile with a fresh time, as InheritNotReadyConditionFromObjects does, is still a no-op")
		third := reconcile(func(obj *promoterv1alpha1.ControllerConfiguration) {
			meta.SetStatusCondition(&obj.Status.Conditions, metav1.Condition{
				Type:               string(promoterConditions.Ready),
				Status:             metav1.ConditionTrue,
				Reason:             string(promoterConditions.ReconciliationSuccess),
				Message:            "Reconciliation successful",
				ObservedGeneration: obj.Generation,
				LastTransitionTime: metav1.Now(),
			})
		}, nil)
		Expect(third.ResourceVersion).To(Equal(first.ResourceVersion))

		By("a transition to Ready=False moves the time and writes the object")
		fourth := reconcile(nil, errors.New("boom"))
		Expect(fourth.ResourceVersion).NotTo(Equal(first.ResourceVersion))
		Expect(readyOf(fourth).Status).To(Equal(metav1.ConditionFalse))
		Expect(readyOf(fourth).LastTransitionTime.Time).To(BeTemporally(">", readyOf(first).LastTransitionTime.Time))

		By("a repeated failure is a no-op too")
		fifth := reconcile(nil, errors.New("boom"))
		Expect(fifth.ResourceVersion).To(Equal(fourth.ResourceVersion))
		Expect(readyOf(fifth).LastTransitionTime).To(Equal(readyOf(fourth).LastTransitionTime))

		Expect(k8sClient.Delete(ctx, cc)).To(Succeed())
	})
})
