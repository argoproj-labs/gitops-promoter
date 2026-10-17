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

var _ = Describe("ExpressionEvaluator", func() {
	var (
		ctx context.Context
		e   *ExpressionEvaluator
	)

	BeforeEach(func() {
		ctx = context.Background()
		e = NewExpressionEvaluator()
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

	Describe("evaluateTriggerWhenBranch", func() {
		It("can read NamespaceMetadata labels from TemplateData", func() {
			trigger := &promoterv1alpha1.TriggerModeSpec{
				When: promoterv1alpha1.WhenWithOutputSpec{
					Expression: `NamespaceMetadata.Labels["team"] == "payments"`,
				},
			}
			td := TemplateData{
				NamespaceMetadata: NamespaceMetadata{Labels: map[string]string{"team": "payments"}},
			}
			shouldFire, _, _, err := e.evaluateTriggerWhenBranch(ctx, trigger, td)
			Expect(err).ToNot(HaveOccurred())
			Expect(shouldFire).To(BeTrue())
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

	Describe("evaluateValidationExpressionForEnvironments", func() {
		It("returns CommitPhaseSuccess for a boolean expression that evaluates to true", func() {
			phase, err := e.evaluateValidationExpressionForEnvironments(ctx, "Response.StatusCode == 200", map[string]any{
				"Response": map[string]any{"StatusCode": 200},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
		})

		It("returns CommitPhasePending when the boolean expression is false", func() {
			phase, err := e.evaluateValidationExpressionForEnvironments(ctx, "Response.StatusCode == 200", map[string]any{
				"Response": map[string]any{"StatusCode": 500},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(phase).To(Equal(promoterv1alpha1.CommitPhasePending))
		})

		It("returns the phase from a { phase } object", func() {
			expression := `Response.StatusCode >= 500 ? {"phase": "failure"} : {"phase": "success"}`

			phase, err := e.evaluateValidationExpressionForEnvironments(ctx, expression, map[string]any{
				"Response": map[string]any{"StatusCode": 503},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(phase).To(Equal(promoterv1alpha1.CommitPhaseFailure))

			phase, err = e.evaluateValidationExpressionForEnvironments(ctx, expression, map[string]any{
				"Response": map[string]any{"StatusCode": 200},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
		})

		It("returns CommitPhasePending for an explicit pending phase", func() {
			phase, err := e.evaluateValidationExpressionForEnvironments(ctx, `{"phase": "pending"}`, map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			Expect(phase).To(Equal(promoterv1alpha1.CommitPhasePending))
		})

		It("defaults phase to pending when the object omits it", func() {
			phase, err := e.evaluateValidationExpressionForEnvironments(ctx, `{"other": "value"}`, map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			Expect(phase).To(Equal(promoterv1alpha1.CommitPhasePending))
		})

		It("errors on an unrecognized phase", func() {
			_, err := e.evaluateValidationExpressionForEnvironments(ctx, `{"phase": "bogus"}`, map[string]any{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unrecognized phase"))
		})

		It("errors when the object uses promotionstrategy-only keys", func() {
			for _, expression := range []string{
				`{"defaultPhase": "success"}`,
				`{"environments": [{"branch": "env/dev", "phase": "success"}]}`,
			} {
				_, err := e.evaluateValidationExpressionForEnvironments(ctx, expression, map[string]any{})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("promotionstrategy"))
			}
		})

		It("errors when the expression returns a non-bool, non-object value", func() {
			_, err := e.evaluateValidationExpressionForEnvironments(ctx, `"success"`, map[string]any{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("must return bool or object { phase }"))
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

	Describe("EvaluateHTTPRequestVariables", func() {
		It("returns nil when spec is nil", func() {
			result, err := e.EvaluateHTTPRequestVariables(ctx, nil, TemplateData{})
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(BeNil())
		})

		It("returns nil when expression is blank", func() {
			spec := &promoterv1alpha1.HTTPRequestVariablesSpec{Expression: "   "}
			result, err := e.EvaluateHTTPRequestVariables(ctx, spec, TemplateData{})
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(BeNil())
		})

		It("evaluates a simple expression and returns the map", func() {
			spec := &promoterv1alpha1.HTTPRequestVariablesSpec{Expression: `{"pathSuffix": "/api/v1"}`}
			result, err := e.EvaluateHTTPRequestVariables(ctx, spec, TemplateData{Branch: "env/dev"})
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(HaveKeyWithValue("pathSuffix", "/api/v1"))
		})

		It("exposes Branch from TemplateData", func() {
			spec := &promoterv1alpha1.HTTPRequestVariablesSpec{Expression: `{"b": Branch}`}
			result, err := e.EvaluateHTTPRequestVariables(ctx, spec, TemplateData{Branch: "env/prod"})
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(HaveKeyWithValue("b", "env/prod"))
		})

		It("exposes TriggerVariables from TemplateData", func() {
			spec := &promoterv1alpha1.HTTPRequestVariablesSpec{
				Expression: `{"mode": TriggerVariables["inCloseMode"]}`,
			}
			td := TemplateData{TriggerVariables: map[string]any{"inCloseMode": true}}
			result, err := e.EvaluateHTTPRequestVariables(ctx, spec, td)
			Expect(err).ToNot(HaveOccurred())
			Expect(result).To(HaveKeyWithValue("mode", true))
		})

		It("makes urlValuesEncode available and encodes query parameters", func() {
			spec := &promoterv1alpha1.HTTPRequestVariablesSpec{
				Expression: `{"qs": urlValuesEncode({"limit": "25", "q": "hello world"})}`,
			}
			result, err := e.EvaluateHTTPRequestVariables(ctx, spec, TemplateData{})
			Expect(err).ToNot(HaveOccurred())
			// url.Values.Encode sorts keys alphabetically.
			Expect(result).To(HaveKeyWithValue("qs", "limit=25&q=hello+world"))
		})

		It("errors when the expression returns a non-map", func() {
			spec := &promoterv1alpha1.HTTPRequestVariablesSpec{Expression: `"not a map"`}
			_, err := e.EvaluateHTTPRequestVariables(ctx, spec, TemplateData{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("must return a map/object"))
		})

		It("errors when the expression does not compile", func() {
			spec := &promoterv1alpha1.HTTPRequestVariablesSpec{Expression: "this is not expr syntax !!!"}
			_, err := e.EvaluateHTTPRequestVariables(ctx, spec, TemplateData{})
			Expect(err).To(HaveOccurred())
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

		It("evicts least-recently-used entries when over capacity", func() {
			lim := newExpressionEvaluatorWithCompileCacheMax(2)
			_, err := lim.evaluateTriggerExpression(ctx, "true", map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			_, err = lim.evaluateTriggerExpression(ctx, "false", map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			_, err = lim.evaluateTriggerExpression(ctx, "1==1", map[string]any{})
			Expect(err).ToNot(HaveOccurred())
			// "true" was never referenced after the first two; adding a third evicts the LRU ("true").
			_, hit := lim.cache.get(expressionCacheKey{Prefix: "trigger", Expression: "true"})
			Expect(hit).To(BeFalse())
			_, hit = lim.cache.get(expressionCacheKey{Prefix: "trigger", Expression: "false"})
			Expect(hit).To(BeTrue())
		})
	})
})

var _ = Describe("Pure helpers", func() {
	Describe("urlValuesEncode", func() {
		It("returns an empty string for an empty map", func() {
			Expect(urlValuesEncode(map[string]any{})).To(Equal(""))
		})

		It("percent-encodes special characters in values", func() {
			result := urlValuesEncode(map[string]any{"q": "hello world"})
			Expect(result).To(Equal("q=hello+world"))
		})

		It("converts numeric values to strings", func() {
			result := urlValuesEncode(map[string]any{"limit": 25})
			Expect(result).To(Equal("limit=25"))
		})

		It("sorts keys alphabetically (url.Values.Encode behaviour)", func() {
			result := urlValuesEncode(map[string]any{"z": "last", "a": "first"})
			Expect(result).To(Equal("a=first&z=last"))
		})

		It("encodes a realistic RHACS-style filter", func() {
			result := urlValuesEncode(map[string]any{
				"query": "Namespace:petclinic-dev+Violation State:ACTIVE",
			})
			Expect(result).To(Equal("query=Namespace%3Apetclinic-dev%2BViolation+State%3AACTIVE"))
		})
	})

	Describe("TemplateData.triggerExprData", func() {
		It("exposes expected keys", func() {
			td := TemplateData{
				Branch: "env/dev",
				Phase:  "pending",
				NamespaceMetadata: NamespaceMetadata{
					Labels:      map[string]string{"team": "payments"},
					Annotations: map[string]string{"cost-center": "cc-42"},
				},
			}
			m := td.triggerExprData()
			Expect(m).To(HaveKeyWithValue("Branch", "env/dev"))
			Expect(m).To(HaveKeyWithValue("Phase", "pending"))
			Expect(m).To(HaveKey("PromotionStrategy"))
			Expect(m).To(HaveKey("WebRequestCommitStatus"))
			Expect(m).To(HaveKey("TriggerOutput"))
			Expect(m).To(HaveKey("ResponseOutput"))
			Expect(m).To(HaveKey("SuccessOutput"))
			Expect(m).To(HaveKey("NamespaceMetadata"))
			ns, ok := m["NamespaceMetadata"].(NamespaceMetadata)
			Expect(ok).To(BeTrue())
			Expect(ns.Labels).To(HaveKeyWithValue("team", "payments"))
			Expect(ns.Annotations).To(HaveKeyWithValue("cost-center", "cc-42"))
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
