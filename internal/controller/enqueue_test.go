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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

var _ = Describe("EnqueueCommitStatusGatesForPromotionStrategy", func() {
	const (
		ns       = "test-ns"
		psName   = "my-ps"
		otherPS  = "other-ps"
		otherNS  = "other-ns"
		gateName = "my-gate"
	)

	var (
		ctx context.Context
		ps  *promoterv1alpha1.PromotionStrategy
	)

	BeforeEach(func() {
		ctx = context.Background()
		ps = &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: psName, Namespace: ns},
		}
	})

	newClient := func(objs ...client.Object) client.Client {
		b := fake.NewClientBuilder().WithScheme(utils.GetScheme())
		for _, obj := range GateCommitStatusKinds() {
			b = b.WithIndex(obj, PromotionStrategyRefField, PromotionStrategyRefIndexValues)
		}
		return b.WithObjects(objs...).Build()
	}

	requestNames := func(reqs []reconcile.Request) []string {
		names := make([]string, len(reqs))
		for i, req := range reqs {
			names[i] = req.String()
		}
		return names
	}

	DescribeTable("returns reconcile requests for matching gates",
		func(gate client.Object, list client.ObjectList) {
			c := newClient(gate)
			reqs := EnqueueCommitStatusGatesForPromotionStrategy(ctx, c, ps, list, "TestGate")
			Expect(requestNames(reqs)).To(ConsistOf(ns + "/" + gateName))
		},
		Entry("GitCommitStatus",
			&promoterv1alpha1.GitCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: gateName, Namespace: ns},
				Spec:       promoterv1alpha1.GitCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName}},
			},
			&promoterv1alpha1.GitCommitStatusList{},
		),
		Entry("TimedCommitStatus",
			&promoterv1alpha1.TimedCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: gateName, Namespace: ns},
				Spec:       promoterv1alpha1.TimedCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName}},
			},
			&promoterv1alpha1.TimedCommitStatusList{},
		),
		Entry("WebRequestCommitStatus",
			&promoterv1alpha1.WebRequestCommitStatus{
				ObjectMeta: metav1.ObjectMeta{Name: gateName, Namespace: ns},
				Spec:       promoterv1alpha1.WebRequestCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName}},
			},
			&promoterv1alpha1.WebRequestCommitStatusList{},
		),
	)

	It("returns nil when no gates match the PromotionStrategy ref", func() {
		gate := &promoterv1alpha1.GitCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: gateName, Namespace: ns},
			Spec:       promoterv1alpha1.GitCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: otherPS}},
		}
		c := newClient(gate)
		reqs := EnqueueCommitStatusGatesForPromotionStrategy(ctx, c, ps, &promoterv1alpha1.GitCommitStatusList{}, "GitCommitStatus")
		Expect(reqs).To(BeEmpty())
	})

	It("scopes results to the PromotionStrategy namespace", func() {
		localGate := &promoterv1alpha1.GitCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: "local", Namespace: ns},
			Spec:       promoterv1alpha1.GitCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName}},
		}
		otherGate := &promoterv1alpha1.GitCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: "remote", Namespace: otherNS},
			Spec:       promoterv1alpha1.GitCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName}},
		}
		c := newClient(localGate, otherGate)
		reqs := EnqueueCommitStatusGatesForPromotionStrategy(ctx, c, ps, &promoterv1alpha1.GitCommitStatusList{}, "GitCommitStatus")
		Expect(requestNames(reqs)).To(Equal([]string{ns + "/local"}))
	})

	It("returns reconcile requests for multiple matching gates", func() {
		gateA := &promoterv1alpha1.GitCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: "gate-a", Namespace: ns},
			Spec:       promoterv1alpha1.GitCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName}},
		}
		gateB := &promoterv1alpha1.GitCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: "gate-b", Namespace: ns},
			Spec:       promoterv1alpha1.GitCommitStatusSpec{PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: psName}},
		}
		c := newClient(gateA, gateB)
		reqs := EnqueueCommitStatusGatesForPromotionStrategy(ctx, c, ps, &promoterv1alpha1.GitCommitStatusList{}, "GitCommitStatus")
		Expect(requestNames(reqs)).To(ConsistOf(ns+"/gate-a", ns+"/gate-b"))
	})
})
