package resourcetemplate

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/exprcache"
)

var _ = Describe("EvaluatePhaseExpression", func() {
	var cache *exprcache.Cache

	BeforeEach(func() {
		cache = exprcache.New(16)
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
		res, err := EvaluatePhaseExpression(cache, "true", jobEnv())
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))

		res, err = EvaluatePhaseExpression(cache, "false", jobEnv())
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
	})

	It("parses phase objects with description and url", func() {
		expr := `any(Resource.status?.conditions ?? [], {.type == 'Complete' && .status == 'True'})
			? {phase: 'success', url: 'https://example.com/run/1'}
			: any(Resource.status?.conditions ?? [], {.type == 'Failed' && .status == 'True'})
				? {phase: 'failure', description: 'Smoke test is failing'}
				: false`

		res, err := EvaluatePhaseExpression(cache, expr, jobEnv())
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Phase).To(Equal(promoterv1alpha1.CommitPhasePending))

		res, err = EvaluatePhaseExpression(cache, expr, jobEnv(map[string]any{"type": "Complete", "status": "True"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(PhaseResult{Phase: promoterv1alpha1.CommitPhaseSuccess, URL: "https://example.com/run/1"}))

		res, err = EvaluatePhaseExpression(cache, expr, jobEnv(map[string]any{"type": "Failed", "status": "True"}))
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(PhaseResult{Phase: promoterv1alpha1.CommitPhaseFailure, Description: "Smoke test is failing"}))
	})

	It("treats an omitted phase as pending", func() {
		res, err := EvaluatePhaseExpression(cache, `{description: 'still going'}`, jobEnv())
		Expect(err).NotTo(HaveOccurred())
		Expect(res).To(Equal(PhaseResult{Phase: promoterv1alpha1.CommitPhasePending, Description: "still going"}))
	})

	It("rejects invalid phases and return types", func() {
		_, err := EvaluatePhaseExpression(cache, `{phase: 'done'}`, jobEnv())
		Expect(err).To(MatchError(ContainSubstring("invalid phase")))

		_, err = EvaluatePhaseExpression(cache, `"success"`, jobEnv())
		Expect(err).To(MatchError(ContainSubstring("must return a boolean or an object")))

		_, err = EvaluatePhaseExpression(cache, `{phase: 1}`, jobEnv())
		Expect(err).To(MatchError(ContainSubstring(`key "phase" must be a string`)))
	})

	It("surfaces compile and runtime errors", func() {
		_, err := EvaluatePhaseExpression(cache, `Resource.status.conditions ==`, jobEnv())
		Expect(err).To(MatchError(ContainSubstring("failed to compile")))

		// Member access through a nil base is a runtime error in expr; users must guard with ?. and ??.
		_, err = EvaluatePhaseExpression(cache, `Resource.status.missing.deeper == 1`, jobEnv())
		Expect(err).To(MatchError(ContainSubstring("phase expression failed")))
	})

	It("reuses compiled programs", func() {
		_, err := EvaluatePhaseExpression(cache, "true", jobEnv())
		Expect(err).NotTo(HaveOccurred())
		_, hit := cache.Get(exprcache.Key{Prefix: phaseExpressionCachePrefix, Expression: "true"})
		Expect(hit).To(BeTrue())
	})
})
