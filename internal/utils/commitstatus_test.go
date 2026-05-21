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
		Entry("TimedCommitStatus", &promoterv1alpha1.TimedCommitStatus{TypeMeta: promoterv1alpha1.ResourceTypeMeta(promoterv1alpha1.TimedCommitStatusKind)}, "promoter.argoproj.io/timed-commit-status"),
		Entry("WebRequestCommitStatus", &promoterv1alpha1.WebRequestCommitStatus{TypeMeta: promoterv1alpha1.ResourceTypeMeta(promoterv1alpha1.WebRequestCommitStatusKind)}, "promoter.argoproj.io/web-request-commit-status"),
		Entry("ArgoCDCommitStatus", &promoterv1alpha1.ArgoCDCommitStatus{TypeMeta: promoterv1alpha1.ResourceTypeMeta(promoterv1alpha1.ArgoCDCommitStatusKind)}, "promoter.argoproj.io/argo-cd-commit-status"),
		Entry("GitCommitStatus", &promoterv1alpha1.GitCommitStatus{TypeMeta: promoterv1alpha1.ResourceTypeMeta(promoterv1alpha1.GitCommitStatusKind)}, "promoter.argoproj.io/git-commit-status"),
	)
})

var _ = Describe("CommitStatusStandardLabels", func() {
	It("returns parent gate, environment, and commit-status key labels", func() {
		parent := &promoterv1alpha1.TimedCommitStatus{
			TypeMeta: promoterv1alpha1.ResourceTypeMeta(promoterv1alpha1.TimedCommitStatusKind),
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
			want := utils.KubeSafeUniqueName(ctx, "my-gate-"+branch+"-"+partialKind)
			Expect(got).To(Equal(want))
		},
		Entry("TimedCommitStatus", &promoterv1alpha1.TimedCommitStatus{TypeMeta: promoterv1alpha1.ResourceTypeMeta(promoterv1alpha1.TimedCommitStatusKind)}, "environment/development", "timed"),
		Entry("WebRequestCommitStatus", &promoterv1alpha1.WebRequestCommitStatus{TypeMeta: promoterv1alpha1.ResourceTypeMeta(promoterv1alpha1.WebRequestCommitStatusKind)}, "environment/staging", "webrequest"),
		Entry("ArgoCDCommitStatus", &promoterv1alpha1.ArgoCDCommitStatus{TypeMeta: promoterv1alpha1.ResourceTypeMeta(promoterv1alpha1.ArgoCDCommitStatusKind)}, "environment/production", "argocd"),
		Entry("GitCommitStatus", &promoterv1alpha1.GitCommitStatus{TypeMeta: promoterv1alpha1.ResourceTypeMeta(promoterv1alpha1.GitCommitStatusKind)}, "environment/development", "git"),
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
			TypeMeta: promoterv1alpha1.ResourceTypeMeta(promoterv1alpha1.TimedCommitStatusKind),
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
					Kind:       promoterv1alpha1.TimedCommitStatusKind,
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
					Kind:       promoterv1alpha1.TimedCommitStatusKind,
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
