package utils

import (
	"encoding/json"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// applyStatusJSON builds the full status apply configuration for obj and returns the
// "status" member of its JSON encoding, decoded generically.
func applyStatusJSON(o *promoterv1alpha1.ChangeTransferPolicy) map[string]any {
	GinkgoHelper()
	cfg, err := statusApplyConfig(o, false)
	Expect(err).NotTo(HaveOccurred())
	data, err := json.Marshal(cfg)
	Expect(err).NotTo(HaveOccurred())
	var decoded map[string]any
	Expect(json.Unmarshal(data, &decoded)).To(Succeed())
	status, ok := decoded["status"].(map[string]any)
	Expect(ok).To(BeTrue(), "status should be an object: %s", string(data))
	return status
}

var _ = Describe("statusApplyConfig", func() {
	It("omits empty sub-objects so SSA unsets them instead of applying {}", func() {
		ctp := &promoterv1alpha1.ChangeTransferPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "ctp", Namespace: "default"},
		}
		// The failure mode: the proposed branch moved to a commit with no readable
		// hydrator.metadata, so Proposed.Dry is reset to its zero value while
		// Proposed.Hydrated is populated.
		ctp.Status.Proposed.Hydrated.Sha = "ee98e8f78c020572402312f711eb0f2f138df854"

		status := applyStatusJSON(ctp)

		proposed, ok := status["proposed"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(proposed).NotTo(HaveKey("dry"), "zero-value Dry must not be applied as {}")
		Expect(proposed).To(HaveKeyWithValue("hydrated", HaveKeyWithValue("sha", ctp.Status.Proposed.Hydrated.Sha)))
		// A zero metav1.Time marshals as null and must not survive as an explicit null.
		Expect(proposed["hydrated"]).NotTo(HaveKey("commitTime"))
		Expect(status).NotTo(HaveKey("active"), "fully-empty Active must be omitted")
	})

	It("keeps populated sub-objects intact", func() {
		ctp := &promoterv1alpha1.ChangeTransferPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "ctp", Namespace: "default"},
		}
		ctp.Status.Proposed.Dry.Sha = "c33e44496bd2afa0f683c43083582fefab282ece"
		ctp.Status.Proposed.Dry.CommitTime = metav1.Now()
		ctp.Status.Proposed.Hydrated.Sha = "b06e7e71b37a32e1e9955eef93c71722c1c3bce4"
		ctp.Status.Active.Hydrated.Sha = "b06e7e71b37a32e1e9955eef93c71722c1c3bce4"

		status := applyStatusJSON(ctp)

		Expect(status).To(HaveKeyWithValue("proposed", SatisfyAll(
			HaveKeyWithValue("dry", SatisfyAll(
				HaveKeyWithValue("sha", ctp.Status.Proposed.Dry.Sha),
				HaveKey("commitTime"),
			)),
			HaveKeyWithValue("hydrated", HaveKeyWithValue("sha", ctp.Status.Proposed.Hydrated.Sha)),
		)))
		Expect(status).To(HaveKeyWithValue("active", HaveKeyWithValue("hydrated", HaveKeyWithValue("sha", ctp.Status.Active.Hydrated.Sha))))
		Expect(status["active"]).NotTo(HaveKey("dry"))
	})

	It("leaves the conditions-only apply configuration unchanged", func() {
		ctp := &promoterv1alpha1.ChangeTransferPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "ctp", Namespace: "default"},
		}
		ctp.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionFalse, Reason: "ReconciliationError", Message: "boom"}}
		ctp.Status.Proposed.Hydrated.Sha = "ee98e8f78c020572402312f711eb0f2f138df854"

		cfg, err := statusApplyConfig(ctp, true)
		Expect(err).NotTo(HaveOccurred())
		data, err := json.Marshal(cfg)
		Expect(err).NotTo(HaveOccurred())
		var decoded map[string]any
		Expect(json.Unmarshal(data, &decoded)).To(Succeed())
		status, ok := decoded["status"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(status).To(HaveKey("conditions"))
		Expect(status).NotTo(HaveKey("proposed"))
	})
})

var _ = Describe("statusApplyConfig for PromotionStrategy", func() {
	It("keeps required environment sub-objects while dropping optional empty ones", func() {
		ps := &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: "ps", Namespace: "default"},
		}
		// status.environments[].active and .proposed are required in the CRD (no
		// omitempty), so an empty environment must still apply them as {}. The
		// optional dry/hydrated objects inside them must be omitted when empty.
		ps.Status.Environments = []promoterv1alpha1.EnvironmentStatus{
			{Branch: "environment/development"},
			{Branch: "environment/staging", Proposed: promoterv1alpha1.CommitBranchState{
				Hydrated: promoterv1alpha1.CommitShaState{Sha: "ee98e8f78c020572402312f711eb0f2f138df854"},
			}},
		}

		cfg, err := statusApplyConfig(ps, false)
		Expect(err).NotTo(HaveOccurred())
		data, err := json.Marshal(cfg)
		Expect(err).NotTo(HaveOccurred())
		var decoded map[string]any
		Expect(json.Unmarshal(data, &decoded)).To(Succeed())
		envs, ok := decoded["status"].(map[string]any)["environments"].([]any)
		Expect(ok).To(BeTrue())
		Expect(envs).To(HaveLen(2))

		Expect(envs[0]).To(SatisfyAll(
			HaveKeyWithValue("branch", "environment/development"),
			HaveKeyWithValue("active", BeEmpty()),
			HaveKeyWithValue("proposed", BeEmpty()),
		))
		Expect(envs[1]).To(SatisfyAll(
			HaveKeyWithValue("active", BeEmpty()),
			HaveKeyWithValue("proposed", SatisfyAll(
				HaveKeyWithValue("hydrated", HaveKeyWithValue("sha", "ee98e8f78c020572402312f711eb0f2f138df854")),
				Not(HaveKey("dry")),
			)),
		))
	})
})

type pruneLeaf struct {
	Time metav1.Time `json:"time,omitempty"`
	Sha  string      `json:"sha,omitempty"`
}

type pruneEmbedded struct {
	Inline pruneLeaf `json:"inline,omitempty"`
}

type pruneRoot struct {
	pruneEmbedded `json:",inline"`
	Optional      pruneLeaf            `json:"optional,omitempty"`
	Required      pruneLeaf            `json:"required"`
	Renamed       pruneLeaf            `json:"renamedField,omitempty"`
	Skipped       pruneLeaf            `json:"-"`
	unexported    pruneLeaf            //nolint:unused // exercises the unexported-field branch
	OptionalPtr   *pruneLeaf           `json:"optionalPtr,omitempty"`
	ByKey         map[string]pruneLeaf `json:"byKey,omitempty"`
	Items         []pruneLeaf          `json:"items,omitempty"`
}

var _ = Describe("pruneEmptyOptionalObjects", func() {
	prune := func(v any) map[string]any {
		GinkgoHelper()
		data, err := json.Marshal(v)
		Expect(err).NotTo(HaveOccurred())
		var generic any
		Expect(json.Unmarshal(data, &generic)).To(Succeed())
		out, ok := pruneEmptyOptionalObjects(reflect.ValueOf(v), generic).(map[string]any)
		Expect(ok).To(BeTrue())
		return out
	}

	It("drops empty optional objects and nulls but keeps required ones", func() {
		out := prune(&pruneRoot{
			OptionalPtr: &pruneLeaf{},
			Items:       []pruneLeaf{{}, {Sha: "a"}},
			ByKey:       map[string]pruneLeaf{"k": {}},
		})
		Expect(out).NotTo(HaveKey("optional"))
		Expect(out).NotTo(HaveKey("optionalPtr"))
		Expect(out).NotTo(HaveKey("renamedField"))
		Expect(out).NotTo(HaveKey("inline"), "inlined embedded struct members are pruned in place")
		Expect(out).To(HaveKeyWithValue("required", BeEmpty()), "required object stays as {} with its null time removed")
		// List elements and map entries are traversed but never removed.
		Expect(out).To(HaveKeyWithValue("items", ConsistOf(BeEmpty(), HaveKeyWithValue("sha", "a"))))
		Expect(out).To(HaveKeyWithValue("byKey", HaveKeyWithValue("k", BeEmpty())))
	})

	It("keeps populated optional objects", func() {
		out := prune(&pruneRoot{
			pruneEmbedded: pruneEmbedded{Inline: pruneLeaf{Sha: "i"}},
			Optional:      pruneLeaf{Time: metav1.Now()},
			Required:      pruneLeaf{Sha: "r"},
		})
		Expect(out).To(HaveKeyWithValue("inline", HaveKeyWithValue("sha", "i")))
		Expect(out).To(HaveKeyWithValue("optional", HaveKey("time")))
		Expect(out).To(HaveKeyWithValue("required", HaveKeyWithValue("sha", "r")))
	})

	It("leaves non-object encodings untouched", func() {
		Expect(pruneEmptyOptionalObjects(reflect.ValueOf(metav1.Time{}), nil)).To(BeNil())
		Expect(pruneEmptyOptionalObjects(reflect.ValueOf((*pruneRoot)(nil)), map[string]any{"x": 1})).To(HaveKeyWithValue("x", 1))
		Expect(pruneEmptyOptionalObjects(reflect.ValueOf("s"), "s")).To(Equal("s"))
	})
})
