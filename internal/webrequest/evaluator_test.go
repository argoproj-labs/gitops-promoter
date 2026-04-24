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

package webrequest

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

var _ = Describe("Evaluator", func() {
	var (
		ctx context.Context
		e   *Evaluator
	)

	BeforeEach(func() {
		ctx = context.Background()
		e = NewEvaluator()
	})

	Describe("evaluateTriggerExpression", func() {
		It("returns true for an expression evaluating to true", func() {
			tr, err := e.evaluateTriggerExpression(ctx, "true", map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			Expect(tr.Trigger).To(BeTrue())
		})

		It("returns false for an expression evaluating to false", func() {
			tr, err := e.evaluateTriggerExpression(ctx, "1 == 2", map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			Expect(tr.Trigger).To(BeFalse())
		})

		It("returns an error when the expression does not compile", func() {
			_, err := e.evaluateTriggerExpression(ctx, "this is not expr", map[string]any{})
			Expect(err).To(HaveOccurred())
		})

		It("reads variables from the env map", func() {
			tr, err := e.evaluateTriggerExpression(ctx, "Phase == 'success'", map[string]any{"Phase": "success"})
			Expect(err).ToNot(HaveOccurred())
			Expect(tr.Trigger).To(BeTrue())
		})
	})

	Describe("evaluateTriggerDataExpression", func() {
		It("returns the map produced by the expression", func() {
			result, err := e.evaluateTriggerDataExpression(ctx, `{"foo": "bar"}`, map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(HaveKeyWithValue("foo", "bar"))
		})

		It("errors when the expression returns a non-map", func() {
			_, err := e.evaluateTriggerDataExpression(ctx, `"not a map"`, map[string]any{})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("evaluateValidationExpression", func() {
		It("returns true for a boolean expression that evaluates to true", func() {
			passed, err := e.evaluateValidationExpression(ctx, "Response.StatusCode == 200", map[string]any{
				"Response": map[string]any{"StatusCode": 200},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(passed).To(BeTrue())
		})

		It("returns false when the boolean expression is false", func() {
			passed, err := e.evaluateValidationExpression(ctx, "Response.StatusCode == 200", map[string]any{
				"Response": map[string]any{"StatusCode": 500},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(passed).To(BeFalse())
		})
	})

	Describe("evaluateValidationExpressionForPromotionStrategy", func() {
		It("returns CommitPhaseSuccess when expression returns true", func() {
			phase, phaseByBranch, err := e.evaluateValidationExpressionForPromotionStrategy(ctx, "true", map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			Expect(phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			Expect(phaseByBranch).To(BeNil())
		})

		It("returns CommitPhasePending when expression returns false", func() {
			phase, phaseByBranch, err := e.evaluateValidationExpressionForPromotionStrategy(ctx, "false", map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			Expect(phase).To(Equal(promoterv1alpha1.CommitPhasePending))
			Expect(phaseByBranch).To(BeNil())
		})

		It("parses a { defaultPhase, environments } object into a per-branch map", func() {
			expression := `{
				"defaultPhase": "pending",
				"environments": [
					{"branch": "env/dev", "phase": "success"},
					{"branch": "env/prod", "phase": "failure"}
				]
			}`
			phase, phaseByBranch, err := e.evaluateValidationExpressionForPromotionStrategy(ctx, expression, map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			Expect(phase).To(Equal(promoterv1alpha1.CommitPhasePending))
			Expect(phaseByBranch).To(HaveKeyWithValue("env/dev", promoterv1alpha1.CommitPhaseSuccess))
			Expect(phaseByBranch).To(HaveKeyWithValue("env/prod", promoterv1alpha1.CommitPhaseFailure))
		})

		It("defaults defaultPhase to pending when omitted", func() {
			phase, _, err := e.evaluateValidationExpressionForPromotionStrategy(ctx, `{"environments": []}`, map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			Expect(phase).To(Equal(promoterv1alpha1.CommitPhasePending))
		})

		It("errors when the expression returns a non-bool, non-object value", func() {
			_, _, err := e.evaluateValidationExpressionForPromotionStrategy(ctx, `"bogus"`, map[string]any{})
			Expect(err).To(HaveOccurred())
		})

		It("errors on duplicate branch entries", func() {
			expression := `{
				"environments": [
					{"branch": "env/dev", "phase": "success"},
					{"branch": "env/dev", "phase": "failure"}
				]
			}`
			_, _, err := e.evaluateValidationExpressionForPromotionStrategy(ctx, expression, map[string]any{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("duplicate branch"))
		})
	})

	Describe("evaluateResponseDataExpression", func() {
		It("extracts values from a JSON body", func() {
			resp := HTTPResponse{
				StatusCode: 200,
				Body:       map[string]any{"url": "https://example.com/run/42"},
				Headers:    map[string][]string{},
			}
			result, err := e.evaluateResponseDataExpression(ctx, `{"buildUrl": Response.Body.url}`, resp)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(HaveKeyWithValue("buildUrl", "https://example.com/run/42"))
		})
	})

	Describe("evaluateSuccessDataExpression", func() {
		It("returns the map produced by the expression", func() {
			result, err := e.evaluateSuccessDataExpression(ctx, `{"phase": Phase}`, map[string]any{"Phase": "success"})
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(HaveKeyWithValue("phase", "success"))
		})
	})

	Describe("enrichWhenExprEnv", func() {
		It("returns base unchanged when variables is nil", func() {
			base := map[string]any{"Phase": "pending"}
			out, err := e.enrichWhenExprEnv(ctx, promoterv1alpha1.WhenWithOutputSpec{}, base)
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(Equal(base))
		})

		It("binds the variables expression result to Variables in the returned env", func() {
			spec := promoterv1alpha1.WhenWithOutputSpec{
				Variables: &promoterv1alpha1.OutputSpec{Expression: `{"threshold": 3}`},
			}
			out, err := e.enrichWhenExprEnv(ctx, spec, map[string]any{"Phase": "pending"})
			Expect(err).ToNot(HaveOccurred())
			Expect(out).To(HaveKeyWithValue("Phase", "pending"))
			Expect(out).To(HaveKey("Variables"))
			vars, ok := out["Variables"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(vars).To(HaveKeyWithValue("threshold", 3))
		})
	})

	Describe("compile cache", func() {
		It("only compiles the same (prefix, expression) once", func() {
			// Both calls on the same expression should hit the cache on the second invocation.
			_, err := e.evaluateTriggerExpression(ctx, "Phase == 'pending'", map[string]any{"Phase": "pending"})
			Expect(err).ToNot(HaveOccurred())
			tr, err := e.evaluateTriggerExpression(ctx, "Phase == 'pending'", map[string]any{"Phase": "pending"})
			Expect(err).ToNot(HaveOccurred())
			Expect(tr.Trigger).To(BeTrue())
		})

		It("separates entries by prefix (trigger vs triggerdata) even with the same expression", func() {
			// Trigger expression needs AsBool(); triggerdata does not. If they shared a cache
			// entry, running the second with the first's program (or vice versa) would behave oddly.
			_, err := e.evaluateTriggerExpression(ctx, "true", map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			result, err := e.evaluateTriggerDataExpression(ctx, `{"ok": true}`, map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(HaveKeyWithValue("ok", true))
		})
	})
})

var _ = Describe("Pure helpers", func() {
	Describe("TemplateData.triggerExprData", func() {
		It("exposes expected keys", func() {
			td := TemplateData{Branch: "env/dev", Phase: "pending"}
			m := td.triggerExprData()
			Expect(m).To(HaveKeyWithValue("Branch", "env/dev"))
			Expect(m).To(HaveKeyWithValue("Phase", "pending"))
			Expect(m).To(HaveKey("PromotionStrategy"))
			Expect(m).To(HaveKey("WebRequestCommitStatus"))
			Expect(m).To(HaveKey("TriggerOutput"))
			Expect(m).To(HaveKey("ResponseOutput"))
			Expect(m).To(HaveKey("SuccessOutput"))
		})
	})

	Describe("successWhenExprData", func() {
		It("sets Response to nil when response is nil", func() {
			td := TemplateData{Branch: "env/dev"}
			m := successWhenExprData(td, nil)
			Expect(m).To(HaveKeyWithValue("Response", BeNil()))
		})

		It("populates Response when a response is provided", func() {
			td := TemplateData{Branch: "env/dev"}
			resp := &HTTPResponse{StatusCode: 201, Body: "ok", Headers: map[string][]string{"X": {"y"}}}
			m := successWhenExprData(td, resp)
			Expect(m).To(HaveKey("Response"))
			r, ok := m["Response"].(map[string]any)
			Expect(ok).To(BeTrue())
			Expect(r).To(HaveKeyWithValue("StatusCode", 201))
			Expect(r).To(HaveKeyWithValue("Body", "ok"))
		})
	})

	Describe("AggregatePhase", func() {
		It("returns pending for an empty map", func() {
			Expect(aggregatePhase(nil)).To(Equal("pending"))
		})

		It("returns failure when any branch is failure", func() {
			Expect(aggregatePhase(map[string]promoterv1alpha1.CommitStatusPhase{
				"a": promoterv1alpha1.CommitPhaseSuccess,
				"b": promoterv1alpha1.CommitPhaseFailure,
			})).To(Equal("failure"))
		})

		It("returns success only when every branch succeeded", func() {
			Expect(aggregatePhase(map[string]promoterv1alpha1.CommitStatusPhase{
				"a": promoterv1alpha1.CommitPhaseSuccess,
				"b": promoterv1alpha1.CommitPhaseSuccess,
			})).To(Equal("success"))
		})

		It("returns pending when one branch is pending and none failed", func() {
			Expect(aggregatePhase(map[string]promoterv1alpha1.CommitStatusPhase{
				"a": promoterv1alpha1.CommitPhaseSuccess,
				"b": promoterv1alpha1.CommitPhasePending,
			})).To(Equal("pending"))
		})
	})

	Describe("ResolvePhaseForBranch", func() {
		It("returns the branch-specific phase when present", func() {
			m := map[string]promoterv1alpha1.CommitStatusPhase{"a": promoterv1alpha1.CommitPhaseFailure}
			Expect(resolvePhaseForBranch("a", promoterv1alpha1.CommitPhaseSuccess, m)).To(Equal(promoterv1alpha1.CommitPhaseFailure))
		})

		It("falls back to the default when the branch is missing", func() {
			m := map[string]promoterv1alpha1.CommitStatusPhase{"a": promoterv1alpha1.CommitPhaseFailure}
			Expect(resolvePhaseForBranch("b", promoterv1alpha1.CommitPhaseSuccess, m)).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
		})

		It("falls back to the default when the map is nil", func() {
			Expect(resolvePhaseForBranch("a", promoterv1alpha1.CommitPhasePending, nil)).To(Equal(promoterv1alpha1.CommitPhasePending))
		})
	})

	Describe("GetPhasesByBranch", func() {
		It("builds an entry for every environment, filling in the default", func() {
			envs := []promoterv1alpha1.Environment{{Branch: "a"}, {Branch: "b"}, {Branch: "c"}}
			overrides := map[string]promoterv1alpha1.CommitStatusPhase{"b": promoterv1alpha1.CommitPhaseFailure}
			got := getPhasesByBranch(envs, promoterv1alpha1.CommitPhaseSuccess, overrides)
			Expect(got).To(HaveLen(3))
			Expect(got).To(HaveKeyWithValue("a", promoterv1alpha1.CommitPhaseSuccess))
			Expect(got).To(HaveKeyWithValue("b", promoterv1alpha1.CommitPhaseFailure))
			Expect(got).To(HaveKeyWithValue("c", promoterv1alpha1.CommitPhaseSuccess))
		})
	})
})
