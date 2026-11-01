package resourcetemplate

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/runtime/schema"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("EvaluatePhaseExpression", func() {
	var compiler *Compiler

	BeforeEach(func() {
		compiler = newTestCompiler()
	})

	jobEnv := func(conditions ...map[string]any) map[string]any {
		conds := make([]any, 0, len(conditions))
		for _, c := range conditions {
			conds = append(conds, c)
		}
		status := map[string]any{}
		if len(conds) > 0 {
			status["conditions"] = conds
		}
		return map[string]any{
			"Resource": map[string]any{"status": status},
			"Branch":   "environment/development",
		}
	}

	It("maps booleans to success and pending", func() {
		res, err := EvaluatePhaseExpression(compiler, jobGVK, "true", jobEnv())
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))

		res, err = EvaluatePhaseExpression(compiler, jobGVK, "false", jobEnv())
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
	})

	It("parses phase objects with description and url", func() {
		expr := `Resource.status.?conditions.orValue([]).exists(c, c.type == 'Complete' && c.status == 'True')
			? {'phase': 'success', 'url': 'https://example.com/run/1'}
			: Resource.status.?conditions.orValue([]).exists(c, c.type == 'Failed' && c.status == 'True')
				? {'phase': 'failure', 'description': 'Smoke test is failing'}
				: {'phase': 'pending'}`

		res, err := EvaluatePhaseExpression(compiler, jobGVK, expr, jobEnv())
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))

		res, err = EvaluatePhaseExpression(compiler, jobGVK, expr, jobEnv(map[string]any{"type": "Complete", "status": "True"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(PhaseResult{Phase: promoterv1alpha1.CommitPhaseSuccess, URL: "https://example.com/run/1"}))

		res, err = EvaluatePhaseExpression(compiler, jobGVK, expr, jobEnv(map[string]any{"type": "Failed", "status": "True"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(PhaseResult{Phase: promoterv1alpha1.CommitPhaseFailure, Description: "Smoke test is failing"}))
	})

	It("treats an omitted phase as pending", func() {
		res, err := EvaluatePhaseExpression(compiler, jobGVK, `{'description': 'still going'}`, jobEnv())
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(PhaseResult{Phase: promoterv1alpha1.CommitPhasePending, Description: "still going"}))
	})

	It("rejects invalid phases and return types", func() {
		_, err := EvaluatePhaseExpression(compiler, jobGVK, `{'phase': 'done'}`, jobEnv())
		Expect(err).To(MatchError(ContainSubstring("invalid phase")))

		_, err = EvaluatePhaseExpression(compiler, jobGVK, `"success"`, jobEnv())
		Expect(err).To(MatchError(ContainSubstring("must return a boolean or an object")), "a string output type is rejected at compile time")

		_, err = EvaluatePhaseExpression(compiler, jobGVK, `{'phase': 1}`, jobEnv())
		Expect(err).To(MatchError(ContainSubstring(`key "phase" must be a string`)))
	})

	It("surfaces compile and runtime errors", func() {
		_, err := EvaluatePhaseExpression(compiler, jobGVK, `Resource.status.conditions ==`, jobEnv())
		Expect(err).To(MatchError(ContainSubstring("phase expression:")))

		By("rejecting unknown fields of a typed Resource at compile time")
		_, err = EvaluatePhaseExpression(compiler, jobGVK, `Resource.status.missing.deeper == 1`, jobEnv())
		Expect(err).To(MatchError(ContainSubstring("missing")))
		Expect(err).NotTo(MatchError(ContainSubstring("phase expression failed")))

		By("failing at runtime on an absent field of an untyped Resource")
		untyped := schema.GroupVersionKind{Version: "v1", Kind: "ConfigMap"}
		_, err = EvaluatePhaseExpression(compiler, untyped, `Resource.status.missing.deeper == 1`, jobEnv())
		Expect(err).To(MatchError(ContainSubstring("phase expression failed")))
	})

	It("reuses compiled programs", func() {
		first, err := compiler.Compile(PhaseVariables, jobGVK, "true")
		Expect(err).NotTo(HaveOccurred())
		second, err := compiler.Compile(PhaseVariables, jobGVK, "true")
		Expect(err).NotTo(HaveOccurred())
		Expect(second).To(BeIdenticalTo(first))
	})
})
