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
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	viewv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/view/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/controller"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
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
//  4. Add the resource plural to config/apiserver/base/rbac.yaml (promoter-apiserver)
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
				"scheme discovery should find %s. Add Spec.PromotionStrategyRef and register the type "+
					"with SchemeBuilder in api/v1alpha1",
				want)
		}
	})

	It("exposes every discovered gate kind on PromotionStrategyDetails", func() {
		viewFields := promotionStrategyDetailsGateSliceFields()
		for _, gate := range controller.GateCommitStatusKinds() {
			elemType := reflect.TypeOf(gate).Elem()
			_, ok := viewFields[elemType]
			Expect(ok).To(BeTrue(),
				"PromotionStrategyDetails is missing a []%s field for gate %T. Add the field in "+
					"api/view/v1alpha1/types.go, then run make generate-apiserver and make generate-ui-types",
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
				"childKinds() does not watch gate %T. Append controller.GateCommitStatusKinds() in "+
					"BundleProvider.childKinds (internal/apiserver/index.go)",
				gate)
		}
	})

	It("maps every discovered gate kind back to its PromotionStrategy", func() {
		provider := newProviderWithReader(newFakeReader(mappingSeed()...))
		psKey := types.NamespacedName{Namespace: testNamespace, Name: testPSName}

		for _, gate := range controller.GateCommitStatusKinds() {
			gate.SetName("gate-" + reflect.TypeOf(gate).Elem().Name())
			gate.SetNamespace(testNamespace)
			setPromotionStrategyRefName(gate, testPSName)

			Expect(controller.PromotionStrategyRefIndexValues(gate)).To(Equal([]string{testPSName}),
				"PromotionStrategyRefIndexValues returned nothing for %T. Spec.PromotionStrategyRef must be present; "+
					"fieldindex discovery is structural — check the Spec field name",
				gate)
			Expect(provider.mapObjectToPromotionStrategies(context.Background(), gate)).To(Equal([]types.NamespacedName{psKey}),
				"mapObjectToPromotionStrategies did not map %T to its PromotionStrategy. "+
					"Gate kinds are handled via controller.PromotionStrategyRefName in the default branch of "+
					"mapObjectToPromotionStrategies (internal/apiserver/index.go)",
				gate)
		}
	})

	It("includes every discovered gate kind when building a bundle", func() {
		gates := controller.GateCommitStatusKinds()
		objs := make([]client.Object, 0, 1+len(gates))
		objs = append(objs, &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: testPSName, Namespace: testNamespace, UID: testPSUID},
			Spec: promoterv1alpha1.PromotionStrategySpec{
				Environments: []promoterv1alpha1.Environment{{Branch: "environment/dev"}},
			},
		})
		for _, gate := range gates {
			gate.SetName("gate-" + reflect.TypeOf(gate).Elem().Name())
			gate.SetNamespace(testNamespace)
			setPromotionStrategyRefName(gate, testPSName)
			objs = append(objs, gate)
		}

		bundle, err := buildBundle(context.Background(), newFakeReader(objs...), testNamespace, testPSName, "1")
		Expect(err).NotTo(HaveOccurred())

		viewFields := promotionStrategyDetailsGateSliceFields()
		bundleVal := reflect.ValueOf(bundle).Elem()
		for _, proto := range gates {
			elemType := reflect.TypeOf(proto).Elem()
			fieldName := viewFields[elemType]
			Expect(fieldName).NotTo(BeEmpty(),
				"PromotionStrategyDetails has no []%s field for %T. Add it in api/view/v1alpha1/types.go",
				elemType.Name(), proto)
			slice := bundleVal.FieldByName(fieldName)
			Expect(slice.IsValid()).To(BeTrue(),
				"PromotionStrategyDetails.%s is not a settable field on the bundle value", fieldName)
			Expect(slice.Len()).To(Equal(1),
				"buildBundle did not include %s (field %s). List the kind with MatchingFields and assign "+
					"bundle.%s in internal/apiserver/builder.go",
				elemType.Name(), fieldName, fieldName)
		}
	})

	It("grants the apiserver ClusterRole read access to every discovered gate kind", func() {
		scheme := utils.GetScheme()
		resources := apiserverClusterRoleResources()
		for _, proto := range controller.GateCommitStatusKinds() {
			gvk, err := apiutil.GVKForObject(proto, scheme)
			Expect(err).NotTo(HaveOccurred())
			plural := gateResourcePlural(gvk)
			Expect(resources).To(ContainElement(plural),
				"%s: add %q under promoter-apiserver ClusterRole resources in config/apiserver/base/rbac.yaml, "+
					"then run make build-installer so dist/ install manifests stay in sync",
				gvk.Kind, plural)
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

// gateResourcePlural returns the CRD plural for a gate kind (…Status → …statuses).
func gateResourcePlural(gvk schema.GroupVersionKind) string {
	return strings.ToLower(gvk.Kind) + "es"
}

// apiserverClusterRoleResources returns the promoter.argoproj.io resources listed
// on the promoter-apiserver ClusterRole in config/apiserver/base/rbac.yaml.
//
// TODO: automatically generate this RBAC instead of relying on the user to update it.
func apiserverClusterRoleResources() []string {
	_, thisFile, _, ok := goruntime.Caller(0)
	Expect(ok).To(BeTrue())
	path := filepath.Join(filepath.Dir(thisFile), "..", "..", "config", "apiserver", "base", "rbac.yaml")

	raw, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred(), "read %s", path)

	decoder := yaml.NewYAMLOrJSONDecoder(strings.NewReader(string(raw)), 4096)
	for {
		var role rbacv1.ClusterRole
		err := decoder.Decode(&role)
		if errors.Is(err, io.EOF) {
			break
		}
		Expect(err).NotTo(HaveOccurred(), "decode %s", path)
		if role.Name != "promoter-apiserver" {
			continue
		}
		var resources []string
		for _, rule := range role.Rules {
			for _, group := range rule.APIGroups {
				if group != "promoter.argoproj.io" {
					continue
				}
				resources = append(resources, rule.Resources...)
			}
		}
		return resources
	}
	Fail("ClusterRole promoter-apiserver not found in " + path)
	return nil
}
