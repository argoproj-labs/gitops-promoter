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

package apiserver

import (
	"context"
	"fmt"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	viewv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/view/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/controller"
)

// GateCommitStatusKinds is discovered from the promoter scheme (any type with
// Spec.PromotionStrategyRef). These specs assert the view aggregate still wires
// each discovered gate into PromotionStrategyDetails / buildBundle / watches /
// PS mapping — the parts that cannot be inferred automatically.
//
// When you add a new gate CRD with Spec.PromotionStrategyRef:
//  1. Register it with SchemeBuilder (discovery picks it up automatically)
//  2. Add a []T field on PromotionStrategyDetails
//  3. List it in buildBundle
var _ = Describe("Gate commit-status managers stay in sync with the view aggregate", func() {
	It("discovers at least the known PromotionStrategyRef gate kinds", func() {
		got := map[string]struct{}{}
		for _, gate := range controller.GateCommitStatusKinds() {
			got[reflect.TypeOf(gate).Elem().Name()] = struct{}{}
		}
		for _, want := range []string{
			"ArgoCDCommitStatus",
			"GitCommitStatus",
			"TimedCommitStatus",
			"WebRequestCommitStatus",
			"ScheduledCommitStatus",
		} {
			Expect(got).To(HaveKey(want),
				"scheme discovery should find %s (does the type have Spec.PromotionStrategyRef and SchemeBuilder.Register?)", want)
		}
	})

	It("exposes every discovered gate kind on PromotionStrategyDetails", func() {
		viewFields := promotionStrategyDetailsGateSliceFields()
		for _, gate := range controller.GateCommitStatusKinds() {
			elemType := reflect.TypeOf(gate).Elem()
			_, ok := viewFields[elemType]
			Expect(ok).To(BeTrue(),
				"PromotionStrategyDetails is missing a []%s field for gate %T; add it to api/view/v1alpha1/types.go and regenerate the view OpenAPI",
				elemType.Name(), gate)
		}
	})

	It("watches every discovered gate kind as a child kind", func() {
		watched := map[reflect.Type]struct{}{}
		for _, kind := range newProviderWithReader(newFakeReader()).childKinds() {
			watched[reflect.TypeOf(kind)] = struct{}{}
		}
		for _, gate := range controller.GateCommitStatusKinds() {
			Expect(watched).To(HaveKey(reflect.TypeOf(gate)),
				"childKinds() is missing gate %T; include controller.GateCommitStatusKinds() in BundleProvider.childKinds", gate)
		}
	})

	It("maps every discovered gate kind back to its PromotionStrategy", func() {
		provider := newProviderWithReader(newFakeReader(mappingSeed()...))
		psKey := types.NamespacedName{Namespace: testNamespace, Name: testPSName}

		for _, proto := range controller.GateCommitStatusKinds() {
			gate := proto.DeepCopyObject().(client.Object)
			gate.SetName(fmt.Sprintf("gate-%s", reflect.TypeOf(gate).Elem().Name()))
			gate.SetNamespace(testNamespace)
			setPromotionStrategyRefName(gate, testPSName)

			Expect(controller.PromotionStrategyRefIndexValues(gate)).To(Equal([]string{testPSName}),
				"PromotionStrategyRefIndexValues must handle %T", gate)
			Expect(provider.mapObjectToPromotionStrategies(context.Background(), gate)).To(Equal([]types.NamespacedName{psKey}),
				"mapObjectToPromotionStrategies must handle %T", gate)
		}
	})

	It("includes every discovered gate kind when building a bundle", func() {
		objs := []client.Object{
			&promoterv1alpha1.PromotionStrategy{
				ObjectMeta: metav1.ObjectMeta{Name: testPSName, Namespace: testNamespace, UID: testPSUID},
				Spec: promoterv1alpha1.PromotionStrategySpec{
					Environments: []promoterv1alpha1.Environment{{Branch: "environment/dev"}},
				},
			},
		}
		for _, proto := range controller.GateCommitStatusKinds() {
			gate := proto.DeepCopyObject().(client.Object)
			gate.SetName(fmt.Sprintf("gate-%s", reflect.TypeOf(gate).Elem().Name()))
			gate.SetNamespace(testNamespace)
			setPromotionStrategyRefName(gate, testPSName)
			objs = append(objs, gate)
		}

		bundle, err := buildBundle(context.Background(), newFakeReader(objs...), testNamespace, testPSName, "1")
		Expect(err).NotTo(HaveOccurred())

		viewFields := promotionStrategyDetailsGateSliceFields()
		bundleVal := reflect.ValueOf(bundle).Elem()
		for _, proto := range controller.GateCommitStatusKinds() {
			elemType := reflect.TypeOf(proto).Elem()
			fieldName := viewFields[elemType]
			Expect(fieldName).NotTo(BeEmpty())
			slice := bundleVal.FieldByName(fieldName)
			Expect(slice.IsValid()).To(BeTrue())
			Expect(slice.Len()).To(Equal(1),
				"buildBundle did not include %s (field %s); list the gate kind in internal/apiserver/builder.go",
				elemType.Name(), fieldName)
		}
	})
})

// promotionStrategyDetailsGateSliceFields maps gate element types (e.g.
// promoterv1alpha1.TimedCommitStatus) to the PromotionStrategyDetails field name
// that holds []T for that gate.
func promotionStrategyDetailsGateSliceFields() map[reflect.Type]string {
	detailsType := reflect.TypeOf(viewv1alpha1.PromotionStrategyDetails{})
	out := make(map[reflect.Type]string)
	for i := 0; i < detailsType.NumField(); i++ {
		field := detailsType.Field(i)
		if field.Type.Kind() != reflect.Slice {
			continue
		}
		out[field.Type.Elem()] = field.Name
	}
	return out
}

// setPromotionStrategyRefName sets spec.promotionStrategyRef.name on a gate object.
func setPromotionStrategyRefName(obj client.Object, name string) {
	spec := reflect.ValueOf(obj).Elem().FieldByName("Spec")
	Expect(spec.IsValid()).To(BeTrue(), "%T has no Spec field", obj)
	ref := spec.FieldByName("PromotionStrategyRef")
	Expect(ref.IsValid()).To(BeTrue(), "%T.Spec has no PromotionStrategyRef field", obj)
	nameField := ref.FieldByName("Name")
	Expect(nameField.CanSet()).To(BeTrue(), "%T.Spec.PromotionStrategyRef.Name is not settable", obj)
	nameField.SetString(name)
}
