package utils_test

import (
	"context"
	"encoding/json"
	"reflect"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	acv1alpha1 "github.com/argoproj-labs/gitops-promoter/applyconfiguration/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/settings"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

var _ = Describe("publishTo JSON", func() {
	It("omits a nil list and encodes an empty list", func() {
		nilList, err := json.Marshal(promoterv1alpha1.CommitStatusSpec{})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(nilList)).NotTo(ContainSubstring(`"publishTo"`))

		empty, err := json.Marshal(promoterv1alpha1.CommitStatusSpec{
			PublishTo: []promoterv1alpha1.CommitStatusPublishTo{},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(empty)).To(ContainSubstring(`"publishTo":[]`))

		gate, err := json.Marshal(promoterv1alpha1.WebRequestCommitStatusSpec{
			PublishTo: []promoterv1alpha1.CommitStatusPublishTo{},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(gate)).To(ContainSubstring(`"publishTo":[]`))
	})
})

var _ = Describe("generated publishTo JSON", func() {
	DescribeTable("apply configurations use omitempty and drop an empty list",
		func(obj any) {
			field, ok := reflect.TypeOf(obj).Elem().FieldByName("PublishTo")
			Expect(ok).To(BeTrue())
			Expect(field.Tag.Get("json")).To(Equal("publishTo,omitempty"))

			fieldValue := reflect.ValueOf(obj).Elem().FieldByName("PublishTo")
			fieldValue.Set(reflect.MakeSlice(fieldValue.Type(), 0, 0))
			raw, err := json.Marshal(obj)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(raw)).NotTo(ContainSubstring(`"publishTo"`))
		},
		Entry("CommitStatus", acv1alpha1.CommitStatusSpec()),
		Entry("ArgoCDCommitStatus", acv1alpha1.ArgoCDCommitStatusSpec()),
		Entry("DependentsSuccessfulCommitStatus", acv1alpha1.DependentsSuccessfulCommitStatusSpec()),
		Entry("GitCommitStatus", acv1alpha1.GitCommitStatusSpec()),
		Entry("ScheduledCommitStatus", acv1alpha1.ScheduledCommitStatusSpec()),
		Entry("TimedCommitStatus", acv1alpha1.TimedCommitStatusSpec()),
		Entry("WebRequestCommitStatus", acv1alpha1.WebRequestCommitStatusSpec()),
	)
})

var _ = Describe("CommitStatusApplyPatch", func() {
	It("writes an empty publishTo as an empty array", func() {
		patch, err := utils.CommitStatusApplyPatch(
			acv1alpha1.CommitStatus("name", "default").
				WithSpec(acv1alpha1.CommitStatusSpec().WithPublishTo()),
			nil,
		)
		Expect(err).NotTo(HaveOccurred())
		data, err := patch.Data(nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring(`"publishTo":[]`))
	})
})

var _ = Describe("CommitStatusGateLabelKeyForParent", func() {
	DescribeTable("derives parent gate label keys from parent Kind",
		func(parent client.Object, want string) {
			Expect(utils.CommitStatusGateLabelKeyForParent(parent)).To(Equal(want))
		},
		Entry("TimedCommitStatus", &promoterv1alpha1.TimedCommitStatus{}, "promoter.argoproj.io/timed-commit-status"),
		Entry("WebRequestCommitStatus", &promoterv1alpha1.WebRequestCommitStatus{}, "promoter.argoproj.io/web-request-commit-status"),
		Entry("ArgoCDCommitStatus", &promoterv1alpha1.ArgoCDCommitStatus{}, "promoter.argoproj.io/argo-cd-commit-status"),
		Entry("GitCommitStatus", &promoterv1alpha1.GitCommitStatus{}, "promoter.argoproj.io/git-commit-status"),
		Entry("ScheduledCommitStatus", &promoterv1alpha1.ScheduledCommitStatus{}, "promoter.argoproj.io/scheduled-commit-status"),
	)
})

var _ = Describe("CommitStatusStandardLabels", func() {
	BeforeEach(func() {
		settings.ResetControllerInstanceIDForTest()
		settings.SetControllerInstanceIDForTest(nil)
	})

	AfterEach(func() {
		settings.ResetControllerInstanceIDForTest()
	})

	It("returns parent gate, environment, and commit-status key labels", func() {
		parent := &promoterv1alpha1.TimedCommitStatus{
			Name: "my-timed",
		}
		labels := utils.CommitStatusStandardLabels(parent, "environment/development", "timer")
		Expect(labels).To(Equal(map[string]string{
			"promoter.argoproj.io/timed-commit-status": utils.KubeSafeLabel("my-timed"),
			promoterv1alpha1.EnvironmentLabel:          utils.KubeSafeLabel("environment/development"),
			promoterv1alpha1.CommitStatusLabel:         "timer",
		}))
	})

	It("stamps instance-id from settings.ControllerInstanceID when configured", func() {
		settings.SetControllerInstanceIDForTest(new("wave-0"))
		parent := &promoterv1alpha1.WebRequestCommitStatus{
			Name:      "wrcs",
			Namespace: "ns",
		}
		labels := utils.CommitStatusStandardLabels(parent, "env/dev", "key")
		Expect(labels[promoterv1alpha1.InstanceIDLabel]).To(Equal("wave-0"))
		Expect(labels[promoterv1alpha1.CommitStatusLabel]).To(Equal("key"))
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
		Entry("ScheduledCommitStatus", &promoterv1alpha1.ScheduledCommitStatus{}, "environment/staging", "scheduled"),
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
			Name:      "my-timed",
			Namespace: "default",
			UID:       "owner-uid",
		}
		// Buffer must fit Eventf calls in this test; the fake recorder drops events when full.
		recorder = events.NewFakeRecorder(100)
	})

	It("deletes owned CommitStatuses not in the valid set", func() {
		gateLabel := utils.CommitStatusGateLabelKeyForParent(owner)
		valid := &promoterv1alpha1.CommitStatus{
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
		}
		orphan := &promoterv1alpha1.CommitStatus{
			Name:            "orphan-cs",
			Namespace:       "default",
			Labels:          map[string]string{gateLabel: utils.KubeSafeLabel(owner.Name)},
			OwnerReferences: valid.OwnerReferences,
		}
		otherOwner := &promoterv1alpha1.CommitStatus{
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
