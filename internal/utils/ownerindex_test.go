package utils_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
)

// countingIndexer records IndexField calls.
type countingIndexer struct {
	calls []string
}

func (c *countingIndexer) IndexField(_ context.Context, _ client.Object, field string, _ client.IndexerFunc) error {
	c.calls = append(c.calls, field)
	return nil
}

var _ = Describe("ControllerOwnerUIDIndexValues", func() {
	ownerRef := func(uid string, controller bool) metav1.OwnerReference {
		return metav1.OwnerReference{
			APIVersion: promoterv1alpha1.GroupVersion.String(),
			Kind:       "TimedCommitStatus",
			Name:       "gate",
			UID:        k8stypes.UID("uid-" + uid),
			Controller: new(controller),
		}
	}

	DescribeTable("returns the controller owner's UID",
		func(refs []metav1.OwnerReference, want []string) {
			cs := &promoterv1alpha1.CommitStatus{Name: "cs", Namespace: "default", OwnerReferences: refs}
			Expect(utils.ControllerOwnerUIDIndexValues(cs)).To(Equal(want))
		},
		Entry("no owner references", nil, nil),
		Entry("only a non-controller owner", []metav1.OwnerReference{ownerRef("a", false)}, nil),
		Entry("a controller owner", []metav1.OwnerReference{ownerRef("a", true)}, []string{"uid-a"}),
		Entry("a controller owner among others",
			[]metav1.OwnerReference{ownerRef("a", false), ownerRef("b", true)}, []string{"uid-b"}),
	)
})

var _ = Describe("RegisterControllerOwnerUIDFieldIndex", func() {
	It("registers once per indexer and type", func(ctx SpecContext) {
		first := &countingIndexer{}
		second := &countingIndexer{}

		Expect(utils.RegisterControllerOwnerUIDFieldIndex(ctx, first, &promoterv1alpha1.CommitStatus{})).To(Succeed())
		Expect(utils.RegisterControllerOwnerUIDFieldIndex(ctx, first, &promoterv1alpha1.CommitStatus{})).To(Succeed())
		Expect(utils.RegisterControllerOwnerUIDFieldIndex(ctx, first, &promoterv1alpha1.ChangeTransferPolicy{})).To(Succeed())
		Expect(utils.RegisterControllerOwnerUIDFieldIndex(ctx, second, &promoterv1alpha1.CommitStatus{})).To(Succeed())

		Expect(first.calls).To(Equal([]string{utils.ControllerOwnerUIDField, utils.ControllerOwnerUIDField}))
		Expect(second.calls).To(Equal([]string{utils.ControllerOwnerUIDField}))
	})
})
