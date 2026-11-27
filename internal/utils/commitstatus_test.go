package utils_test

import (
	"context"
	"encoding/json"

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
	It("omits a nil pointer and encodes an empty list", func() {
		nilList, err := json.Marshal(promoterv1alpha1.CommitStatusSpec{})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(nilList)).NotTo(ContainSubstring(`"publishTo"`))

		empty := []promoterv1alpha1.CommitStatusPublishTo{}
		encoded, err := json.Marshal(promoterv1alpha1.CommitStatusSpec{PublishTo: &empty})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(encoded)).To(ContainSubstring(`"publishTo":[]`))

		gate, err := json.Marshal(promoterv1alpha1.WebRequestCommitStatusSpec{PublishTo: &empty})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(gate)).To(ContainSubstring(`"publishTo":[]`))
	})
})

var _ = Describe("generated publishTo JSON", func() {
	It("encodes an explicit empty list and omits an unset field", func() {
		raw, err := json.Marshal(acv1alpha1.CommitStatusSpec().WithPublishTo([]promoterv1alpha1.CommitStatusPublishTo{}))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).To(ContainSubstring(`"publishTo":[]`))

		omitted, err := json.Marshal(acv1alpha1.CommitStatusSpec())
		Expect(err).NotTo(HaveOccurred())
		Expect(string(omitted)).NotTo(ContainSubstring(`"publishTo"`))
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

		// Controlled by owner but missing the gate label: still matched by the owner index, but the
		// label selector keeps it, as before the index was used.
		unlabeled := &promoterv1alpha1.CommitStatus{
			Name:            "unlabeled-cs",
			Namespace:       "default",
			OwnerReferences: valid.OwnerReferences,
		}

		cl := fake.NewClientBuilder().WithScheme(scheme).
			WithIndex(&promoterv1alpha1.CommitStatus{}, utils.ControllerOwnerUIDField, utils.ControllerOwnerUIDIndexValues).
			WithObjects(valid, orphan, otherOwner, unlabeled).Build()
		err := utils.CleanupOrphanedCommitStatuses(ctx, cl, recorder, owner, []*promoterv1alpha1.CommitStatus{valid})
		Expect(err).NotTo(HaveOccurred())

		var remaining promoterv1alpha1.CommitStatusList
		Expect(cl.List(ctx, &remaining, client.InNamespace("default"))).To(Succeed())
		names := make([]string, len(remaining.Items))
		for i := range remaining.Items {
			names[i] = remaining.Items[i].Name
		}
		Expect(names).To(ConsistOf("valid-cs", "other-owner-cs", "unlabeled-cs"))
	})
})

var _ = Describe("UpsertCommitStatus", func() {
	var (
		ctx    context.Context
		cl     client.Client
		parent *promoterv1alpha1.TimedCommitStatus
		params utils.UpsertCommitStatusParams
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme := runtime.NewScheme()
		Expect(promoterv1alpha1.AddToScheme(scheme)).To(Succeed())
		cl = fake.NewClientBuilder().WithScheme(scheme).Build()

		parent = &promoterv1alpha1.TimedCommitStatus{
			Name:      "my-timed",
			Namespace: "default",
			UID:       "owner-uid",
		}
		params = utils.UpsertCommitStatusParams{
			Parent:      parent,
			RepoRefName: "repo",
			Branch:      "env/dev",
			Sha:         "abc123",
			Key:         "timer",
			Description: "desc",
			Phase:       promoterv1alpha1.CommitPhasePending,
			FieldOwner:  "test",
		}
	})

	get := func() *promoterv1alpha1.CommitStatus {
		cs := &promoterv1alpha1.CommitStatus{}
		name := utils.CommitStatusResourceName(ctx, parent, params.Branch)
		Expect(cl.Get(ctx, client.ObjectKey{Namespace: "default", Name: name}, cs)).To(Succeed())
		return cs
	}

	It("defaults spec.name to key/branch and leaves url unset", func() {
		_, err := utils.UpsertCommitStatus(ctx, cl, params)
		Expect(err).NotTo(HaveOccurred())

		cs := get()
		Expect(cs.Spec.Name).To(Equal("timer/env/dev"))
		Expect(cs.Spec.Url).To(BeEmpty())
		Expect(cs.Spec.Sha).To(Equal("abc123"))
		Expect(cs.Spec.Description).To(Equal("desc"))
		Expect(metav1.IsControlledBy(cs, parent)).To(BeTrue())
	})

	It("applies the Name override and Url when set", func() {
		params.Name = "timer"
		params.Url = "https://example.com/details"
		_, err := utils.UpsertCommitStatus(ctx, cl, params)
		Expect(err).NotTo(HaveOccurred())

		cs := get()
		Expect(cs.Spec.Name).To(Equal("timer"))
		Expect(cs.Spec.Url).To(Equal("https://example.com/details"))
	})
})

var _ = Describe("RenderGateURL", func() {
	data := struct{ Environment string }{Environment: "env/dev"}

	DescribeTable("renders and validates a gate's URL template",
		func(cfg promoterv1alpha1.URLConfig, want string, wantErr string) {
			got, err := utils.RenderGateURL(cfg, data)
			if wantErr != "" {
				Expect(err).To(MatchError(ContainSubstring(wantErr)))
				return
			}
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(want))
		},
		Entry("returns empty when no template is configured",
			promoterv1alpha1.URLConfig{}, "", ""),
		Entry("renders an https URL",
			promoterv1alpha1.URLConfig{Template: "https://example.com/{{ .Environment }}"}, "https://example.com/env/dev", ""),
		Entry("rejects a non-http scheme",
			promoterv1alpha1.URLConfig{Template: "javascript:alert(1)"}, "", "URL scheme is not http or https"),
		Entry("rejects a relative URL",
			promoterv1alpha1.URLConfig{Template: "/{{ .Environment }}"}, "", "URL scheme is not http or https"),
		Entry("surfaces a template error",
			promoterv1alpha1.URLConfig{Template: "https://example.com/{{ .Missing }}"}, "", "failed to render URL template"),
	)
})
