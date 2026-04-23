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

	It("produces 3 steps and propagates outputs / phase across them", func() {
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
		Expect(results).To(HaveLen(3))
		Expect(results[0].Label).To(Equal("before-response"))
		Expect(results[1].Label).To(Equal("with-response"))
		Expect(results[2].Label).To(Equal("after-response"))

		// --- Step 1: before-response ---
		step1 := results[0]
		Expect(step1.Evaluations).To(HaveLen(1))
		eval1 := step1.Evaluations[0]
		Expect(eval1.Branch).To(Equal("environment/dev"))
		Expect(eval1.ResponseInjected).To(BeFalse())
		Expect(eval1.RenderedRequest).To(BeNil())
		Expect(eval1.MockResponse).To(BeNil())
		Expect(eval1.TriggerEval.Evaluated).To(BeTrue())
		Expect(eval1.TriggerEval.Error).To(BeEmpty())
		Expect(eval1.TriggerEval.ShouldFire).To(BeTrue())
		// response.output only runs in with-response, so no ResponseOutput yet
		Expect(eval1.ResponseOutput).To(BeEmpty())
		// success.when with Response=nil falls back to Phase=="success", which is false initially -> pending
		Expect(eval1.Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)))
		// trigger.when.output runs every step, so TriggerOutput is populated
		Expect(eval1.TriggerOutput).To(HaveKeyWithValue("trackedBranch", "environment/dev"))
		Expect(step1.CommitStatuses).To(HaveLen(1))
		Expect(step1.CommitStatuses[0].Description).To(Equal("waiting for environment/dev"))

		// --- Step 2: with-response ---
		step2 := results[1]
		eval2 := step2.Evaluations[0]
		Expect(eval2.ResponseInjected).To(BeTrue())
		Expect(eval2.RenderedRequest).ToNot(BeNil())
		Expect(eval2.RenderedRequest.URL).To(Equal("https://approvals.example.com/api/check/environment/dev"))
		Expect(eval2.RenderedRequest.Headers).To(HaveKeyWithValue("X-Branch", "environment/dev"))
		Expect(eval2.MockResponse).ToNot(BeNil())
		Expect(eval2.MockResponse.StatusCode).To(Equal(200))
		// response.output populated ResponseOutput
		Expect(eval2.ResponseOutput).To(HaveKeyWithValue("approved", true))
		Expect(eval2.ResponseOutput).To(HaveKeyWithValue("sha", "deadbeef"))
		Expect(eval2.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
		// CommitStatus description reflects latest outputs
		Expect(step2.CommitStatuses[0].Description).To(Equal("approved (deadbeef)"))
		Expect(step2.CommitStatuses[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
		// The trigger expression currently returns false because Phase was "pending" at step entry,
		// but we override phase AFTER; ensure our simulator reported the NATURAL trigger result (which was true at step start).
		Expect(eval2.TriggerEval.ShouldFire).To(BeTrue())

		// --- Step 3: after-response (carry-forward) ---
		step3 := results[2]
		eval3 := step3.Evaluations[0]
		Expect(eval3.ResponseInjected).To(BeFalse())
		// With Response=nil the success expression falls back to Phase=="success"; prior Phase was success
		Expect(eval3.Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
		// ResponseOutput carried from step 2
		Expect(eval3.ResponseOutput).To(HaveKeyWithValue("approved", true))
		Expect(eval3.ResponseOutput).To(HaveKeyWithValue("sha", "deadbeef"))
		// At step 3 entry, Phase is "success" from step 2, so the trigger expression (Phase != "success") returns false
		Expect(eval3.TriggerEval.Evaluated).To(BeTrue())
		Expect(eval3.TriggerEval.ShouldFire).To(BeFalse())
		// CommitStatus still reflects approval
		Expect(step3.CommitStatuses[0].Description).To(Equal("approved (deadbeef)"))
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
		Expect(results).To(HaveLen(3))

		// Step 2 with-response: per-branch phases from the object-shaped success expression.
		step2 := results[1]
		Expect(step2.Evaluations).To(HaveLen(1))
		Expect(step2.Evaluations[0].Branch).To(BeEmpty(), "context=promotionstrategy uses a shared evaluation with empty branch")
		Expect(step2.Evaluations[0].PhasePerBranch).To(HaveKeyWithValue("environment/dev", string(promoterv1alpha1.CommitPhaseSuccess)))
		Expect(step2.Evaluations[0].PhasePerBranch).To(HaveKeyWithValue("environment/staging", string(promoterv1alpha1.CommitPhasePending)))
		// Per-env CommitStatuses render using resolved per-branch phase.
		Expect(step2.CommitStatuses).To(HaveLen(2))
		byBranch := map[string]RenderedCommitStatus{}
		for _, cs := range step2.CommitStatuses {
			byBranch[cs.Branch] = cs
		}
		Expect(byBranch["environment/dev"].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
		Expect(byBranch["environment/dev"].Description).To(Equal("environment/dev - success"))
		Expect(byBranch["environment/staging"].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)))
	})
})

var _ = Describe("SimulateWebRequestTemplates — after-state-change (step 4)", func() {
	ctx := context.Background()

	const key = "sim-fingerprint-gate"

	// makeFingerprintWRCS builds a WebRequestCommitStatus that uses when.variables + when.output
	// to persist lastFingerprint, and a success.when expression that carries forward success only
	// while Variables.fingerprint == TriggerOutput.lastFingerprint. This mirrors the
	// change-management example's structure at the minimum fidelity needed to exercise step 4.
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

	It("carries forward success in 3 steps when fingerprint is unchanged", func() {
		wrcs := makeFingerprintWRCS()
		ps := makeFingerprintPS("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

		results, err := SimulateWebRequestTemplates(ctx, wrcs, ps, SimulationNamespaceMetadata{}, SimulationMockResponse{
			StatusCode: 200,
		}, "", SimulateWebRequestOptions{})
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(3), "without --promotion-strategy-updated or --web-request-updated the simulator returns 3 steps")
		// Step 3 (after-response) carries forward success because fingerprint is unchanged.
		Expect(results[2].Label).To(Equal("after-response"))
		Expect(results[2].Evaluations[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
	})

	It("flips phase back to pending in step 4 when the updated PromotionStrategy advances DrySha", func() {
		wrcs := makeFingerprintWRCS()
		psOld := makeFingerprintPS("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
		psNew := makeFingerprintPS("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

		results, err := SimulateWebRequestTemplates(ctx, wrcs, psOld, SimulationNamespaceMetadata{}, SimulationMockResponse{
			StatusCode: 200,
		}, "", SimulateWebRequestOptions{PromotionStrategyUpdated: psNew})
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(4), "with --promotion-strategy-updated the simulator returns 4 steps")
		Expect(results[3].Label).To(Equal(SimStepAfterStateChange))

		// Step 3 established success with fp_old.
		Expect(results[2].Evaluations[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
		// Step 3's persisted TriggerOutput.lastFingerprint is fp_old.
		step3TO := results[2].Evaluations[0].TriggerOutput
		Expect(step3TO).To(HaveKey("lastFingerprint"))

		// Step 4 recomputes fingerprint from psNew → fp_new != fp_old → carry-forward fails → pending.
		Expect(results[3].Evaluations[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)),
			"fingerprint mismatch between step 3 TriggerOutput.lastFingerprint and step 4 Variables.fingerprint must flip carry-forward to pending")
	})

	It("uses the updated WebRequestCommitStatus's templates in step 4 when --web-request-updated is provided", func() {
		wrcs := makeFingerprintWRCS()
		wrcs.Spec.DescriptionTemplate = "original: {{ .Phase }}"
		wrcsUpdated := makeFingerprintWRCS()
		wrcsUpdated.Spec.DescriptionTemplate = "updated: {{ .Phase }}"
		ps := makeFingerprintPS("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")

		results, err := SimulateWebRequestTemplates(ctx, wrcs, ps, SimulationNamespaceMetadata{}, SimulationMockResponse{
			StatusCode: 200,
		}, "", SimulateWebRequestOptions{WebRequestCommitStatusUpdated: wrcsUpdated})
		Expect(err).ToNot(HaveOccurred())
		Expect(results).To(HaveLen(4))

		// Steps 1-3 use the original descriptionTemplate.
		Expect(results[0].CommitStatuses[0].Description).To(HavePrefix("original:"))
		Expect(results[1].CommitStatuses[0].Description).To(HavePrefix("original:"))
		Expect(results[2].CommitStatuses[0].Description).To(HavePrefix("original:"))
		// Step 4 swaps in the updated descriptionTemplate while carrying forward prior outputs.
		Expect(results[3].CommitStatuses[0].Description).To(HavePrefix("updated:"))
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
		Expect(results).To(HaveLen(3))
		// Step 1 should see Phase=success and TriggerOutput.lastFingerprint from the seeded status,
		// so the carry-forward success.when branch returns success — not pending.
		Expect(results[0].Evaluations[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)),
			"seeded status should make step 1 behave like a warm reconcile (success carries forward)")
	})

	It("ignores step-1 seed when wrcs.Status is empty (preserves cold-start behavior)", func() {
		wrcs := makeSeedWRCS()
		ps := makeSimPromotionStrategy(key)
		ps.Status.Environments[0].Proposed.Note = &promoterv1alpha1.HydratorMetadata{
			DrySha: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		}
		// Status intentionally empty.

		results, err := SimulateWebRequestTemplates(ctx, wrcs, ps, SimulationNamespaceMetadata{}, SimulationMockResponse{}, "", SimulateWebRequestOptions{})
		Expect(err).ToNot(HaveOccurred())
		// Step 1 with no seed and no response: Phase is "" → carry-forward branch returns pending.
		Expect(results[0].Evaluations[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)))
	})

	It("re-seeds step 4 from wrcsUpdated.Status.PromotionStrategyContext when populated", func() {
		wrcs := makeSeedWRCS()
		ps := makeSimPromotionStrategy(key)
		ps.Status.Environments[0].Proposed.Note = &promoterv1alpha1.HydratorMetadata{
			DrySha: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
		}
		// No step-1 seed; cold start. Step 2 injects Response=200 → success.
		// Step 3 carries forward (Phase=success, TriggerOutput has been refreshed by when.output
		// if configured; here the trigger has no .Output so TriggerOutput stays empty).
		// Step 4 re-seeds from updated status, overriding carry-forward.
		wrcsUpdated := wrcs.DeepCopy()
		// Seed step 4's prior state: say the controller wrote back a stale fingerprint so the
		// carry-forward success.when fails at step 4 (Variables.fingerprint != TriggerOutput.lastFingerprint).
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
		Expect(results).To(HaveLen(4))
		// Step 2: HTTP path returned 200 → success.
		Expect(results[1].Evaluations[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhaseSuccess)))
		// Step 4: re-seeded TriggerOutput.lastFingerprint no longer matches current fingerprint,
		// so carry-forward success.when returns pending. This proves the re-seed ran and overrode
		// whatever step 3's carry-forward produced.
		Expect(results[3].Evaluations[0].Phase).To(Equal(string(promoterv1alpha1.CommitPhasePending)),
			"re-seeded stale fingerprint in step 4 must make carry-forward success.when fail")
	})
})

// Silence unused-import warnings when editing. apiextensionsv1 is imported in case future tests
// construct *apiextensionsv1.JSON fixtures; keep the explicit var to avoid goimports removing it.
var _ = apiextensionsv1.JSON{}
