package ordercommitstatusgate

import (
	"context"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/restmapper"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testRESTMapper(gvk schema.GroupVersionKind, resource string) meta.RESTMapper {
	return restmapper.NewDiscoveryRESTMapper([]*restmapper.APIGroupResources{{
		Group: metav1.APIGroup{
			Name: gvk.Group,
			Versions: []metav1.GroupVersionForDiscovery{
				{GroupVersion: gvk.GroupVersion().String(), Version: gvk.Version},
			},
			PreferredVersion: metav1.GroupVersionForDiscovery{
				GroupVersion: gvk.GroupVersion().String(),
				Version:      gvk.Version,
			},
		},
		VersionedResources: map[string][]metav1.APIResource{
			gvk.Version: {{
				Name:       resource,
				Kind:       gvk.Kind,
				Namespaced: true,
			}},
		},
	}})
}

var _ = Describe("OrderCommitStatusRef helpers", func() {
	It("applies API defaults to group and kind", func() {
		ref := promoterv1alpha1.OrderCommitStatusRef{Name: "demo"}.WithDefaults()
		Expect(ref.Group).To(Equal(promoterv1alpha1.DefaultOrderCommitStatusGroup))
		Expect(ref.Kind).To(Equal(promoterv1alpha1.DefaultOrderCommitStatusKind))
		Expect(ref.GroupKind()).To(Equal(ref.WithDefaults().GroupKind()))
	})
})

var _ = Describe("Resolve", func() {
	It("resolves DependentsSuccessfulCommitStatus", func() {
		ps := &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
			Spec: promoterv1alpha1.PromotionStrategySpec{
				OrderCommitStatusRef: promoterv1alpha1.OrderCommitStatusRef{
					Group: promoterv1alpha1.DefaultOrderCommitStatusGroup,
					Kind:  promoterv1alpha1.DefaultOrderCommitStatusKind,
					Name:  "demo",
				},
			},
		}
		gate := &unstructured.Unstructured{}
		gate.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   promoterv1alpha1.DefaultOrderCommitStatusGroup,
			Version: "v1alpha1",
			Kind:    promoterv1alpha1.DefaultOrderCommitStatusKind,
		})
		gate.SetName("demo")
		gate.SetNamespace("default")
		Expect(unstructured.SetNestedField(gate.Object, promoterv1alpha1.DependentsSuccessfulCommitStatusKey, "spec", "key")).To(Succeed())
		Expect(unstructured.SetNestedField(gate.Object, "demo", "spec", "promotionStrategyRef", "name")).To(Succeed())

		c := fake.NewClientBuilder().WithObjects(gate).Build()
		key, err := Resolve(context.Background(), c, nil, ps)
		Expect(err).NotTo(HaveOccurred())
		Expect(key).To(Equal(promoterv1alpha1.DependentsSuccessfulCommitStatusKey))
	})

	It("resolves out-of-tree gate CRs at v1alpha1", func() {
		ps := &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
			Spec: promoterv1alpha1.PromotionStrategySpec{
				OrderCommitStatusRef: promoterv1alpha1.OrderCommitStatusRef{
					Group: "ordering.example.com",
					Kind:  "TeamOrderCommitStatus",
					Name:  "demo",
				},
			},
		}
		gate := &unstructured.Unstructured{}
		gate.SetGroupVersionKind(schema.GroupVersionKind{
			Group:   "ordering.example.com",
			Version: "v1alpha1",
			Kind:    "TeamOrderCommitStatus",
		})
		gate.SetName("demo")
		gate.SetNamespace("default")
		Expect(unstructured.SetNestedField(gate.Object, "team-order", "spec", "key")).To(Succeed())
		Expect(unstructured.SetNestedField(gate.Object, "demo", "spec", "promotionStrategyRef", "name")).To(Succeed())

		c := fake.NewClientBuilder().WithObjects(gate).Build()
		key, err := Resolve(context.Background(), c, nil, ps)
		Expect(err).NotTo(HaveOccurred())
		Expect(key).To(Equal("team-order"))
	})

	It("resolves the cluster-preferred API version from the REST mapper", func() {
		ps := &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: "default"},
			Spec: promoterv1alpha1.PromotionStrategySpec{
				OrderCommitStatusRef: promoterv1alpha1.OrderCommitStatusRef{
					Group: "ordering.example.com",
					Kind:  "TeamOrderCommitStatus",
					Name:  "demo",
				},
			},
		}
		gvk := schema.GroupVersionKind{
			Group:   "ordering.example.com",
			Version: "v1",
			Kind:    "TeamOrderCommitStatus",
		}
		gate := &unstructured.Unstructured{}
		gate.SetGroupVersionKind(gvk)
		gate.SetName("demo")
		gate.SetNamespace("default")
		Expect(unstructured.SetNestedField(gate.Object, "team-order", "spec", "key")).To(Succeed())
		Expect(unstructured.SetNestedField(gate.Object, "demo", "spec", "promotionStrategyRef", "name")).To(Succeed())

		c := fake.NewClientBuilder().WithObjects(gate).Build()
		mapper := testRESTMapper(gvk, "teamordercommitstatuses")

		key, err := Resolve(context.Background(), c, mapper, ps)
		Expect(err).NotTo(HaveOccurred())
		Expect(key).To(Equal("team-order"))
	})
})
