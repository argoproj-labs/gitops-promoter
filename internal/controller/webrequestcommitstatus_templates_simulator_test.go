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

package controller

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
)

// makeSimPromotionStrategy builds a minimal PromotionStrategy suitable for the templates simulator
// tests: one environment ("environment/dev") with the SHA-bearing Status entry the simulator uses
// when resolving current SHAs. The key argument matches the WebRequestCommitStatus key so the
// environment is classified as applicable.
func makeSimPromotionStrategy(key string) *promoterv1alpha1.PromotionStrategy {
	return &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{Name: "sim-ps", Namespace: "default"},
		Spec: promoterv1alpha1.PromotionStrategySpec{
			RepositoryReference:    promoterv1alpha1.ObjectReference{Name: "sim-repo"},
			ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{{Key: key}},
			Environments: []promoterv1alpha1.Environment{
				{Branch: "environment/dev"},
			},
		},
		Status: promoterv1alpha1.PromotionStrategyStatus{
			Environments: []promoterv1alpha1.EnvironmentStatus{
				{
					Branch: "environment/dev",
					Proposed: promoterv1alpha1.CommitBranchState{
						Hydrated: promoterv1alpha1.CommitShaState{Sha: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"},
					},
				},
			},
		},
	}
}

var _ = Describe("SimulateWebRequestTemplates — environments context", func() {
	ctx := context.Background()

	const key = "sim-external-approval"

	makeWRCS := func() *promoterv1alpha1.WebRequestCommitStatus {
		return &promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: "sim-wrcs", Namespace: "default"},
			Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "sim-ps"},
				Key:                  key,
				DescriptionTemplate:  `{{ if .ResponseOutput.approved }}approved ({{ .ResponseOutput.sha }}){{ else }}waiting for {{ .Branch }}{{ end }}`,
				UrlTemplate:          `https://approvals.example.com/{{ .Branch }}`,
				ReportOn:             "proposed",
				HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
					Method:      "GET",
					URLTemplate: `https://approvals.example.com/api/check/{{ .Branch }}`,
					HeaderTemplates: map[string]string{
						"X-Branch": `{{ .Branch }}`,
					},
					Timeout: metav1.Duration{},
				},
				Success: promoterv1alpha1.SuccessSpec{
					When: promoterv1alpha1.WhenWithOutputSpec{
						Expression: `Response != nil ? (Response.StatusCode == 200 && ResponseOutput.approved == true) : Phase == "success"`,
					},
				},
				Mode: promoterv1alpha1.ModeSpec{
					Context: promoterv1alpha1.ContextEnvironments,
					Trigger: &promoterv1alpha1.TriggerModeSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Phase != "success"`,
							Output: &promoterv1alpha1.OutputSpec{
								Expression: `{ "trackedBranch": Branch }`,
							},
						},
						Response: &promoterv1alpha1.ResponseOutputSpec{
							Output: promoterv1alpha1.OutputSpec{
								Expression: `{ "approved": Response.Body.approved, "sha": Response.Body.sha }`,
							},
						},
					},
				},
			},
		}
	}

	It("produces 2 steps and propagates outputs / phase across them", func() {
		wrcs := makeWRCS()
		ps := makeSimPromotionStrategy(key)
		mock := SimulationMockResponse{
			StatusCode: 200,
			Body: map[string]any{
				"approved": true,
				"sha":      "deadbeef",
			},
			Headers: map[string][]string{"Content-Type": {"application/json"}},
		}

		results, err := SimulateWebRequestTemplates(ctx, wrcs, ps, SimulationNamespaceMetadata{
			Labels: map[string]string{"env": "dev"},
		}, mock, "", SimulateWebRequestOptions{})

		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(2))
		Expect(results[0].Label).To(Equal("reconcile"))
		Expect(results[1].Label).To(Equal("next-reconcile"))

		// --- Step 1: reconcile (trigger fires -> mock injected) ---
		step1 := results[0]
		Expect(step1.Evaluations).To(HaveLen(1))
		eval1 := step1.Evaluations[0]
		Expect(eval1.Branch).To(Equal("environment/dev"))
		Expect(eval1.TriggerEval.Evaluated).To(BeTrue())
		Expect(eval1.TriggerEval.Error).To(BeEmpty())
		Expect(eval1.TriggerEval.ShouldFire).To(BeTrue(),
			"Phase is pending on first reconcile, so Phase != \"success\" -> trigger fires")
		Expect(eval1.ResponseInjected).To(BeTrue(),
			"trigger fires -> mock response is injected")
		Expect(eval1.RenderedRequest).ToNot(BeNil())
		Expect(eval1.RenderedRequest.URL).To(Equal("https://approvals.example.com/api/check/environment/dev"))
		Expect(eval1.RenderedRequest.Headers).To(HaveKeyWithValue("X-Branch", "environment/dev"))
		Expect(eval1.MockResponse).ToNot(BeNil())
		Expect(eval1.MockResponse.StatusCode).To(Equal(200))
		Expect(eval1.ResponseOutput).To(HaveKeyWithValue("approved", true))
		Expect(eval1.ResponseOutput).To(HaveKeyWithValue("sha", "deadbeef"))
		Expect(eval1.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
		Expect(eval1.TriggerOutput).To(HaveKeyWithValue("trackedBranch", "environment/dev"))
		Expect(step1.CommitStatuses).To(HaveLen(1))
		Expect(step1.CommitStatuses[0].Description).To(Equal("approved (deadbeef)"))
		Expect(step1.CommitStatuses[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

		// --- Step 2: next-reconcile (carry-forward; injection only if trigger fires) ---
		step2 := results[1]
		eval2 := step2.Evaluations[0]
		Expect(eval2.ResponseInjected).To(BeFalse(), "trigger did not fire, so no mock injection")
		Expect(eval2.RenderedRequest).To(BeNil())
		Expect(eval2.MockResponse).To(BeNil())
		Expect(eval2.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
			"Response=nil path uses Phase==\"success\" check; prior Phase was success -> still success")
		Expect(eval2.ResponseOutput).To(HaveKeyWithValue("approved", true), "ResponseOutput carried from reconcile")
		Expect(eval2.ResponseOutput).To(HaveKeyWithValue("sha", "deadbeef"))
		Expect(eval2.TriggerEval.Evaluated).To(BeTrue())
		Expect(eval2.TriggerEval.ShouldFire).To(BeFalse(),
			"Phase is success at step entry -> trigger expression Phase != \"success\" is false")
		Expect(step2.CommitStatuses[0].Description).To(Equal("approved (deadbeef)"))
	})

	It("does not inject at 'reconcile' when the trigger evaluates to false", func() {
		wrcs := makeWRCS()
		wrcs.Spec.Mode.Trigger.When.Expression = `false`
		ps := makeSimPromotionStrategy(key)
		mock := SimulationMockResponse{
			StatusCode: 200,
			Body:       map[string]any{"approved": true, "sha": "abc"},
		}

		results, err := SimulateWebRequestTemplates(ctx, wrcs, ps, SimulationNamespaceMetadata{}, mock, "", SimulateWebRequestOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(2))
		Expect(results[0].Evaluations[0].TriggerEval.ShouldFire).To(BeFalse())
		Expect(results[0].Evaluations[0].ResponseInjected).To(BeFalse(),
			"trigger did not fire, so the mock response must not be injected")
		Expect(results[0].Evaluations[0].RenderedRequest).To(BeNil())
		Expect(results[0].Evaluations[0].MockResponse).To(BeNil())
		Expect(results[1].Evaluations[0].ResponseInjected).To(BeFalse())
	})

	It("errors when the WebRequestCommitStatus key has no applicable environment", func() {
		wrcs := makeWRCS()
		wrcs.Spec.Key = "unknown-key"
		ps := makeSimPromotionStrategy(key)

		_, err := SimulateWebRequestTemplates(ctx, wrcs, ps, SimulationNamespaceMetadata{}, SimulationMockResponse{StatusCode: 200}, "", SimulateWebRequestOptions{})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("no applicable environments"))
	})

	It("filters to a single branch when --branch is provided", func() {
		wrcs := makeWRCS()
		ps := makeSimPromotionStrategy(key)
		ps.Spec.Environments = append(ps.Spec.Environments, promoterv1alpha1.Environment{Branch: "environment/staging"})
		ps.Status.Environments = append(ps.Status.Environments, promoterv1alpha1.EnvironmentStatus{
			Branch: "environment/staging",
			Proposed: promoterv1alpha1.CommitBranchState{
				Hydrated: promoterv1alpha1.CommitShaState{Sha: "cafebabecafebabecafebabecafebabecafebabe"},
			},
		})

		results, err := SimulateWebRequestTemplates(ctx, wrcs, ps, SimulationNamespaceMetadata{}, SimulationMockResponse{
			StatusCode: 200,
			Body:       map[string]any{"approved": true, "sha": "cafebabe"},
		}, "environment/staging", SimulateWebRequestOptions{})
		Expect(err).ToNot(HaveOccurred())
		for _, step := range results {
			Expect(step.Evaluations).To(HaveLen(1))
			Expect(step.Evaluations[0].Branch).To(Equal("environment/staging"))
		}
	})
})

var _ = Describe("SimulateWebRequestTemplates — promotionstrategy context", func() {
	ctx := context.Background()

	const key = "sim-shared-gate"

	makeWRCS := func() *promoterv1alpha1.WebRequestCommitStatus {
		return &promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: "sim-wrcs-ps", Namespace: "default"},
			Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "sim-ps"},
				Key:                  key,
				DescriptionTemplate:  `{{ .Branch }} - {{ .Phase }}`,
				UrlTemplate:          `https://dashboard.example.com/{{ .Branch }}`,
				ReportOn:             "proposed",
				HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
					Method:      "GET",
					URLTemplate: `https://deployments.example.com/apps/{{ .PromotionStrategy.Spec.RepositoryReference.Name }}/status`,
				},
				Success: promoterv1alpha1.SuccessSpec{
					When: promoterv1alpha1.WhenWithOutputSpec{
						// Object-shaped result: per-branch phases
						Expression: `Response != nil ? { "defaultPhase": "pending", "environments": [ { "branch": "environment/dev", "phase": "success" }, { "branch": "environment/staging", "phase": "pending" } ] } : { "defaultPhase": Phase == "success" ? "success" : "pending" }`,
					},
				},
				Mode: promoterv1alpha1.ModeSpec{
					Context: promoterv1alpha1.ContextPromotionStrategy,
					Polling: &promoterv1alpha1.PollingModeSpec{
						Interval: metav1.Duration{},
					},
				},
			},
		}
	}

	It("produces a shared evaluation with PhasePerBranch and per-env CommitStatuses", func() {
		wrcs := makeWRCS()
		ps := makeSimPromotionStrategy(key)
		ps.Spec.Environments = append(ps.Spec.Environments, promoterv1alpha1.Environment{Branch: "environment/staging"})
		ps.Status.Environments = append(ps.Status.Environments, promoterv1alpha1.EnvironmentStatus{
			Branch: "environment/staging",
			Proposed: promoterv1alpha1.CommitBranchState{
				Hydrated: promoterv1alpha1.CommitShaState{Sha: "cafebabecafebabecafebabecafebabecafebabe"},
			},
		})

		results, err := SimulateWebRequestTemplates(ctx, wrcs, ps, SimulationNamespaceMetadata{}, SimulationMockResponse{
			StatusCode: 200,
			Body:       map[string]any{"ok": true},
		}, "", SimulateWebRequestOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(2))
		Expect(results[0].Label).To(Equal("reconcile"))
		Expect(results[1].Label).To(Equal("next-reconcile"))

		// Step 1 "reconcile" in polling mode always fires (no trigger gate) → mock injected →
		// per-branch phases come from the object-shaped success expression's HTTP branch.
		step1 := results[0]
		Expect(step1.Evaluations).To(HaveLen(1))
		Expect(step1.Evaluations[0].ResponseInjected).To(BeTrue(),
			"polling mode injects on every simulated reconcile (no trigger gate)")
		Expect(step1.Evaluations[0].Branch).To(BeEmpty(), "context=promotionstrategy uses a shared evaluation with empty branch")
		Expect(step1.Evaluations[0].PhasePerBranch).To(HaveKeyWithValue("environment/dev", string(promoterv1alpha1.CommitPhaseSuccess)))
		Expect(step1.Evaluations[0].PhasePerBranch).To(HaveKeyWithValue("environment/staging", string(promoterv1alpha1.CommitPhasePending)))
		Expect(step1.CommitStatuses).To(HaveLen(2))
		byBranch := map[string]RenderedCommitStatus{}
		for _, cs := range step1.CommitStatuses {
			byBranch[cs.Branch] = cs
		}
		Expect(byBranch["environment/dev"].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
		Expect(byBranch["environment/dev"].Description).To(Equal("environment/dev - success"))
		Expect(byBranch["environment/staging"].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)))

		// next-reconcile is a full reconcile: polling has no trigger gate, so the mock is injected again.
		step2 := results[1]
		Expect(step2.Evaluations).To(HaveLen(1))
		Expect(step2.Evaluations[0].ResponseInjected).To(BeTrue(),
			"polling mode injects on every step, matching the controller")
	})
})

var _ = Describe("SimulateWebRequestTemplates — after-state-change step", func() {
	ctx := context.Background()

	const key = "sim-fingerprint-gate"

	// makeFingerprintWRCS builds a WebRequestCommitStatus that uses when.variables + when.output
	// to persist lastFingerprint, and a success.when expression that carries forward success only
	// while Variables.fingerprint == TriggerOutput.lastFingerprint. This mirrors the
	// change-management example's structure at the minimum fidelity needed to exercise the
	// after-state-change step.
	makeFingerprintWRCS := func() *promoterv1alpha1.WebRequestCommitStatus {
		return &promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: "sim-fp-wrcs", Namespace: "default"},
			Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "sim-ps"},
				Key:                  key,
				DescriptionTemplate:  `{{ .Phase }}`,
				ReportOn:             "proposed",
				HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
					Method:      "POST",
					URLTemplate: "https://example.com/api/record",
				},
				Success: promoterv1alpha1.SuccessSpec{
					When: promoterv1alpha1.WhenWithOutputSpec{
						Variables: &promoterv1alpha1.OutputSpec{
							Expression: `{ "fingerprint": join(map(PromotionStrategy.Status.Environments, { #.Proposed.Note.DrySha }), "|") }`,
						},
						Expression: `Response != nil ? Response.StatusCode == 200 : (Phase == "success" && Variables.fingerprint == (TriggerOutput["lastFingerprint"] ?? ""))`,
					},
				},
				Mode: promoterv1alpha1.ModeSpec{
					Context: promoterv1alpha1.ContextPromotionStrategy,
					Trigger: &promoterv1alpha1.TriggerModeSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Variables: &promoterv1alpha1.OutputSpec{
								Expression: `{ "fingerprint": join(map(PromotionStrategy.Status.Environments, { #.Proposed.Note.DrySha }), "|") }`,
							},
							Expression: `Variables.fingerprint != (TriggerOutput.lastFingerprint ?? "")`,
							Output: &promoterv1alpha1.OutputSpec{
								Expression: `{ "lastFingerprint": Variables.fingerprint }`,
							},
						},
					},
				},
			},
		}
	}

	makeFingerprintPS := func(drySha string) *promoterv1alpha1.PromotionStrategy {
		ps := makeSimPromotionStrategy(key)
		ps.Status.Environments[0].Proposed.Note = &promoterv1alpha1.HydratorMetadata{DrySha: drySha}
		return ps
	}

	It("carries forward success in 2 steps when fingerprint is unchanged", func() {
		wrcs := makeFingerprintWRCS()
		ps := makeFingerprintPS("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

		results, err := SimulateWebRequestTemplates(ctx, wrcs, ps, SimulationNamespaceMetadata{}, SimulationMockResponse{
			StatusCode: 200,
		}, "", SimulateWebRequestOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(2), "without --promotion-strategy-updated or --web-request-updated the simulator returns 2 steps")
		Expect(results[1].Label).To(Equal("next-reconcile"))
		// Step 2 (next-reconcile) carries forward success because fingerprint is unchanged.
		Expect(results[1].Evaluations[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
	})

	It("re-fires trigger and re-injects in the after-state-change step when the updated PromotionStrategy advances DrySha", func() {
		wrcs := makeFingerprintWRCS()
		psOld := makeFingerprintPS("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		psNew := makeFingerprintPS("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

		results, err := SimulateWebRequestTemplates(ctx, wrcs, psOld, SimulationNamespaceMetadata{}, SimulationMockResponse{
			StatusCode: 200,
		}, "", SimulateWebRequestOptions{PromotionStrategyUpdated: psNew})
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(3), "with --promotion-strategy-updated the simulator returns 3 steps")
		Expect(results[2].Label).To(Equal(SimStepAfterStateChange))

		// Step 1 (reconcile): fp_old → trigger fires → 200 → success; when.output persists lastFingerprint=fp_old.
		Expect(results[0].Evaluations[0].TriggerEval.ShouldFire).To(BeTrue())
		Expect(results[0].Evaluations[0].ResponseInjected).To(BeTrue())
		Expect(results[0].Evaluations[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

		// Step 2 (next-reconcile): still fp_old → trigger DOES NOT fire → no injection → carry-forward success.
		Expect(results[1].Evaluations[0].TriggerEval.ShouldFire).To(BeFalse())
		Expect(results[1].Evaluations[0].ResponseInjected).To(BeFalse())
		Expect(results[1].Evaluations[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))

		// After-state-change (psNew): fp_new != persisted lastFingerprint → trigger re-fires →
		// mock 200 is injected again → HTTP branch returns success. This is controller-faithful:
		// a state change that advances the fingerprint drives a fresh reconcile with a fresh HTTP
		// call, not a silent carry-forward invalidation.
		Expect(results[2].Evaluations[0].TriggerEval.ShouldFire).To(BeTrue(),
			"fp_new != persisted lastFingerprint must make the trigger re-fire")
		Expect(results[2].Evaluations[0].ResponseInjected).To(BeTrue(),
			"trigger re-fired, so the mock response must be injected on after-state-change")
		Expect(results[2].Evaluations[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
	})

	It("returns an error when MockResponseUpdated is set without PromotionStrategyUpdated or WebRequestCommitStatusUpdated", func() {
		wrcs := makeFingerprintWRCS()
		ps := makeFingerprintPS("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		upd := SimulationMockResponse{StatusCode: 500}
		_, err := SimulateWebRequestTemplates(ctx, wrcs, ps, SimulationNamespaceMetadata{}, SimulationMockResponse{StatusCode: 200}, "", SimulateWebRequestOptions{
			MockResponseUpdated: &upd,
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("MockResponseUpdated"))
	})

	It("uses MockResponseUpdated for after-state-change when set", func() {
		wrcs := makeFingerprintWRCS()
		psOld := makeFingerprintPS("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		psNew := makeFingerprintPS("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
		updated := SimulationMockResponse{StatusCode: 418}

		results, err := SimulateWebRequestTemplates(ctx, wrcs, psOld, SimulationNamespaceMetadata{}, SimulationMockResponse{
			StatusCode: 200,
		}, "", SimulateWebRequestOptions{
			PromotionStrategyUpdated: psNew,
			MockResponseUpdated:      &updated,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(results[0].Evaluations[0].MockResponse).ToNot(BeNil())
		Expect(results[0].Evaluations[0].MockResponse.StatusCode).To(Equal(200))
		Expect(results[2].Evaluations[0].MockResponse).ToNot(BeNil())
		Expect(results[2].Evaluations[0].MockResponse.StatusCode).To(Equal(418))
		Expect(results[2].Evaluations[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)),
			"success.when HTTP branch requires 200; updated mock is 418")
	})

	It("uses the updated WebRequestCommitStatus's templates in the after-state-change step when --web-request-updated is provided", func() {
		wrcs := makeFingerprintWRCS()
		wrcs.Spec.DescriptionTemplate = "original: {{ .Phase }}"
		wrcsUpdated := makeFingerprintWRCS()
		wrcsUpdated.Spec.DescriptionTemplate = "updated: {{ .Phase }}"
		ps := makeFingerprintPS("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

		results, err := SimulateWebRequestTemplates(ctx, wrcs, ps, SimulationNamespaceMetadata{}, SimulationMockResponse{
			StatusCode: 200,
		}, "", SimulateWebRequestOptions{WebRequestCommitStatusUpdated: wrcsUpdated})
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(3))

		// Steps 1-2 use the original descriptionTemplate.
		Expect(results[0].CommitStatuses[0].Description).To(HavePrefix("original:"))
		Expect(results[1].CommitStatuses[0].Description).To(HavePrefix("original:"))
		// The after-state-change step swaps in the updated descriptionTemplate while carrying
		// forward prior outputs.
		Expect(results[2].CommitStatuses[0].Description).To(HavePrefix("updated:"))
	})
})

var _ = Describe("SimulateWebRequestTemplates — seed from WRCS status", func() {
	ctx := context.Background()

	const key = "sim-seeded-fingerprint"

	// makeSeedWRCS builds a minimal WRCS whose success.when carry-forward branch checks the
	// fingerprint against TriggerOutput.lastFingerprint — exactly like the change-management
	// example. It is used below to prove that seeding TriggerOutput values via wrcs.status
	// changes the outcome of the carry-forward branch without any HTTP request.
	makeSeedWRCS := func() *promoterv1alpha1.WebRequestCommitStatus {
		return &promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: "sim-seed-wrcs", Namespace: "default"},
			Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "sim-ps"},
				Key:                  key,
				DescriptionTemplate:  `{{ .Phase }}`,
				ReportOn:             "proposed",
				HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
					Method:      "POST",
					URLTemplate: "https://example.com",
				},
				Success: promoterv1alpha1.SuccessSpec{
					When: promoterv1alpha1.WhenWithOutputSpec{
						Variables: &promoterv1alpha1.OutputSpec{
							Expression: `{ "fingerprint": join(map(PromotionStrategy.Status.Environments, { #.Proposed.Note.DrySha }), "|") }`,
						},
						// HTTP path: success. Carry-forward: success iff Phase == "success" && fingerprint matches.
						Expression: `Response != nil ? Response.StatusCode == 200 : (Phase == "success" && Variables.fingerprint == (TriggerOutput["lastFingerprint"] ?? ""))`,
					},
				},
				Mode: promoterv1alpha1.ModeSpec{
					Context: promoterv1alpha1.ContextPromotionStrategy,
					Trigger: &promoterv1alpha1.TriggerModeSpec{
						When: promoterv1alpha1.WhenWithOutputSpec{
							Expression: `Phase != "success"`,
						},
					},
				},
			},
		}
	}

	mustJSON := func(data map[string]any) *apiextensionsv1.JSON {
		raw, err := json.Marshal(data)
		Expect(err).ToNot(HaveOccurred())
		return &apiextensionsv1.JSON{Raw: raw}
	}

	It("seeds step 1 from wrcs.Status.PromotionStrategyContext when populated", func() {
		wrcs := makeSeedWRCS()
		ps := makeSimPromotionStrategy(key)
		ps.Status.Environments[0].Proposed.Note = &promoterv1alpha1.HydratorMetadata{
			DrySha: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		}
		// Seed: pretend the last reconcile already succeeded for the same fingerprint.
		wrcs.Status.PromotionStrategyContext = &promoterv1alpha1.WebRequestCommitStatusPromotionStrategyContextStatus{
			PhasePerBranch: []promoterv1alpha1.WebRequestCommitStatusPhasePerBranchItem{
				{Branch: "environment/dev", Phase: promoterv1alpha1.CommitPhaseSuccess},
			},
			TriggerOutput: mustJSON(map[string]any{
				"lastFingerprint": "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			}),
		}

		results, err := SimulateWebRequestTemplates(ctx, wrcs, ps, SimulationNamespaceMetadata{}, SimulationMockResponse{}, "", SimulateWebRequestOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(2))
		// Step 1 ("reconcile") should see Phase=success and TriggerOutput.lastFingerprint from the
		// seeded status. Because Phase is already success at step entry, the trigger expression
		// (Phase != "success") is false → no injection → Response=nil → success.when carry-forward
		// branch returns success (Phase == "success" && fingerprint matches).
		Expect(results[0].Evaluations[0].TriggerEval.ShouldFire).To(BeFalse())
		Expect(results[0].Evaluations[0].ResponseInjected).To(BeFalse())
		Expect(results[0].Evaluations[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
			"seeded status should make reconcile behave like a warm reconcile (success carries forward)")
	})

	It("ignores step-1 seed when wrcs.Status is empty (preserves cold-start behavior)", func() {
		wrcs := makeSeedWRCS()
		ps := makeSimPromotionStrategy(key)
		ps.Status.Environments[0].Proposed.Note = &promoterv1alpha1.HydratorMetadata{
			DrySha: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		}
		// Status intentionally empty.

		// Use an empty mock response: cold-start trigger fires (Phase != "success"), mock injects
		// with StatusCode=0, so success.when's HTTP branch (Response.StatusCode == 200) returns
		// false → pending.
		results, err := SimulateWebRequestTemplates(ctx, wrcs, ps, SimulationNamespaceMetadata{}, SimulationMockResponse{}, "", SimulateWebRequestOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(results[0].Evaluations[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)))
	})

	It("re-seeds the after-state-change step from wrcsUpdated.Status.PromotionStrategyContext when populated", func() {
		wrcs := makeSeedWRCS()
		ps := makeSimPromotionStrategy(key)
		ps.Status.Environments[0].Proposed.Note = &promoterv1alpha1.HydratorMetadata{
			DrySha: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		}
		// No step-1 seed; cold start. Step 1 (reconcile) fires because Phase != "success", so the
		// mock Response=200 is injected → success. Step 2 (next-reconcile) carries forward. The
		// after-state-change step re-seeds from updated status, overriding that carry-forward.
		wrcsUpdated := wrcs.DeepCopy()
		// Seed after-state-change prior state: the controller wrote back a stale fingerprint so
		// the carry-forward success.when fails (Variables.fingerprint != TriggerOutput.lastFingerprint).
		wrcsUpdated.Status.PromotionStrategyContext = &promoterv1alpha1.WebRequestCommitStatusPromotionStrategyContextStatus{
			PhasePerBranch: []promoterv1alpha1.WebRequestCommitStatusPhasePerBranchItem{
				{Branch: "environment/dev", Phase: promoterv1alpha1.CommitPhaseSuccess},
			},
			TriggerOutput: mustJSON(map[string]any{
				"lastFingerprint": "some-older-fingerprint-that-no-longer-matches",
			}),
		}

		results, err := SimulateWebRequestTemplates(ctx, wrcs, ps, SimulationNamespaceMetadata{}, SimulationMockResponse{StatusCode: 200}, "",
			SimulateWebRequestOptions{WebRequestCommitStatusUpdated: wrcsUpdated})
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(3))
		// Step 1 (reconcile): HTTP path returned 200 → success.
		Expect(results[0].Evaluations[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
		// After-state-change: re-seeded TriggerOutput.lastFingerprint no longer matches current
		// fingerprint, so carry-forward success.when returns pending. This proves the re-seed ran
		// and overrode whatever next-reconcile's carry-forward produced.
		Expect(results[2].Evaluations[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)),
			"re-seeded stale fingerprint in the after-state-change step must make carry-forward success.when fail")
	})
})

// Silence unused-import warnings when editing. apiextensionsv1 is imported in case future tests
// construct *apiextensionsv1.JSON fixtures; keep the explicit var to avoid goimports removing it.
var _ = apiextensionsv1.JSON{}
