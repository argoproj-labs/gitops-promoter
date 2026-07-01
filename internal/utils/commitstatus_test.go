package utils_test

import (
	"context"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("CommitStatusGateLabelKeyForParent", func() {
	DescribeTable("derives parent gate label keys from parent Kind",
		func(parent client.Object, want string) {
			Expect(utils.CommitStatusGateLabelKeyForParent(parent)).To(Equal(want))
		},
		Entry("TimedCommitStatus", &promoterv1alpha1.TimedCommitStatus{}, "promoter.argoproj.io/timed-commit-status"),
		Entry("WebRequestCommitStatus", &promoterv1alpha1.WebRequestCommitStatus{}, "promoter.argoproj.io/web-request-commit-status"),
		Entry("ArgoCDCommitStatus", &promoterv1alpha1.ArgoCDCommitStatus{}, "promoter.argoproj.io/argo-cd-commit-status"),
		Entry("GitCommitStatus", &promoterv1alpha1.GitCommitStatus{}, "promoter.argoproj.io/git-commit-status"),
	)
})

var _ = Describe("CommitStatusStandardLabels", func() {
	It("returns parent gate, environment, and commit-status key labels", func() {
		parent := &promoterv1alpha1.TimedCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-timed",
			},
		}
		labels := utils.CommitStatusStandardLabels(parent, "environment/development", "timer")
		Expect(labels).To(Equal(map[string]string{
			"promoter.argoproj.io/timed-commit-status": utils.KubeSafeLabel("my-timed"),
			promoterv1alpha1.EnvironmentLabel:          utils.KubeSafeLabel("environment/development"),
			promoterv1alpha1.CommitStatusLabel:         "timer",
		}))
	})
})

var _ = Describe("CommitStatusResourceName", func() {
	DescribeTable("derives partial kind from parent gate type",
		func(parent client.Object, branch, partialKind string) {
			ctx := context.Background()
			parent.SetName("my-gate")
			got := utils.CommitStatusResourceName(ctx, parent, branch)
			want := utils.KubeSafeUniqueName("my-gate-" + branch + "-" + partialKind)
			Expect(got).To(Equal(want))
		},
		Entry("TimedCommitStatus", &promoterv1alpha1.TimedCommitStatus{}, "environment/development", "timed"),
		Entry("WebRequestCommitStatus", &promoterv1alpha1.WebRequestCommitStatus{}, "environment/staging", "web-request"),
		Entry("ArgoCDCommitStatus", &promoterv1alpha1.ArgoCDCommitStatus{}, "environment/production", "argo-cd"),
		Entry("GitCommitStatus", &promoterv1alpha1.GitCommitStatus{}, "environment/development", "git"),
	)
})

var _ = Describe("CleanupOrphanedCommitStatuses", func() {
	var (
		ctx      context.Context
		scheme   *runtime.Scheme
		owner    *promoterv1alpha1.TimedCommitStatus
		recorder events.EventRecorder
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(promoterv1alpha1.AddToScheme(scheme)).To(Succeed())

		owner = &promoterv1alpha1.TimedCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "my-timed",
				Namespace: "default",
				UID:       "owner-uid",
			},
		}
		// Buffer must fit Eventf calls in this test; the fake recorder drops events when full.
		recorder = events.NewFakeRecorder(100)
	})

	It("deletes owned CommitStatuses not in the valid set", func() {
		gateLabel := utils.CommitStatusGateLabelKeyForParent(owner)
		valid := &promoterv1alpha1.CommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "valid-cs",
				Namespace: "default",
				Labels: map[string]string{
					gateLabel: utils.KubeSafeLabel(owner.Name),
				},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: promoterv1alpha1.GroupVersion.String(),
					Kind:       "TimedCommitStatus",
					Name:       owner.Name,
					UID:        owner.UID,
					Controller: new(true),
				}},
			},
		}
		orphan := &promoterv1alpha1.CommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "orphan-cs",
				Namespace:       "default",
				Labels:          map[string]string{gateLabel: utils.KubeSafeLabel(owner.Name)},
				OwnerReferences: valid.OwnerReferences,
			},
		}
		otherOwner := &promoterv1alpha1.CommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "other-owner-cs",
				Namespace: "default",
				Labels: map[string]string{
					gateLabel: utils.KubeSafeLabel(owner.Name),
				},
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: promoterv1alpha1.GroupVersion.String(),
					Kind:       "TimedCommitStatus",
					Name:       "other",
					UID:        "other-uid",
					Controller: new(true),
				}},
			},
		}

		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(valid, orphan, otherOwner).Build()
		err := utils.CleanupOrphanedCommitStatuses(ctx, cl, recorder, owner, []*promoterv1alpha1.CommitStatus{valid})
		Expect(err).NotTo(HaveOccurred())

		var remaining promoterv1alpha1.CommitStatusList
		Expect(cl.List(ctx, &remaining, client.InNamespace("default"))).To(Succeed())
		names := make([]string, len(remaining.Items))
		for i := range remaining.Items {
			names[i] = remaining.Items[i].Name
		}
		Expect(names).To(ConsistOf("valid-cs", "other-owner-cs"))
	})
})

var _ = Describe("CopyInstanceIDLabel", func() {
	It("no-ops when parent has no instance-id label", func() {
		parent := &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: "ps", Namespace: "ns"},
		}
		child := &promoterv1alpha1.ChangeTransferPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ctp",
				Namespace: "ns",
				Labels:    map[string]string{"existing": "value"},
			},
		}
		utils.CopyInstanceIDLabel(parent, child)
		Expect(child.GetLabels()).To(Equal(map[string]string{"existing": "value"}))
	})

	It("copies instance-id from parent to child", func() {
		parent := &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ps",
				Namespace: "ns",
				Labels:    map[string]string{promoterv1alpha1.InstanceIDLabel: "wave-0"},
			},
		}
		child := &promoterv1alpha1.CommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: "cs", Namespace: "ns"},
		}
		utils.CopyInstanceIDLabel(parent, child)
		Expect(child.GetLabels()[promoterv1alpha1.InstanceIDLabel]).To(Equal("wave-0"))
	})

	It("overwrites stale instance-id on child", func() {
		parent := &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ps",
				Namespace: "ns",
				Labels:    map[string]string{promoterv1alpha1.InstanceIDLabel: "wave-1"},
			},
		}
		child := &promoterv1alpha1.CommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cs",
				Namespace: "ns",
				Labels:    map[string]string{promoterv1alpha1.InstanceIDLabel: "wave-0"},
			},
		}
		utils.CopyInstanceIDLabel(parent, child)
		Expect(child.GetLabels()[promoterv1alpha1.InstanceIDLabel]).To(Equal("wave-1"))
	})
})

var _ = Describe("CopyInstanceIDLabelToMap", func() {
	It("no-ops when parent has no instance-id label", func() {
		parent := &promoterv1alpha1.TimedCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: "tcs", Namespace: "ns"},
		}
		labels := utils.CopyInstanceIDLabelToMap(parent, map[string]string{"k": "v"})
		Expect(labels).To(Equal(map[string]string{"k": "v"}))
	})

	It("adds instance-id to the labels map", func() {
		parent := &promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "wrcs",
				Namespace: "ns",
				Labels:    map[string]string{promoterv1alpha1.InstanceIDLabel: "a"},
			},
		}
		labels := utils.CopyInstanceIDLabelToMap(parent, utils.CommitStatusStandardLabels(parent, "env/dev", "key"))
		Expect(labels[promoterv1alpha1.InstanceIDLabel]).To(Equal("a"))
		Expect(labels[promoterv1alpha1.CommitStatusLabel]).To(Equal("key"))
	})
})
