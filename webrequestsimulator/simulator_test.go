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

package webrequestsimulator_test

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigyaml "sigs.k8s.io/yaml"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/webrequestsimulator"
)

//go:embed testdata/change_management_webrequests.yaml
var changeManagementWebrequestsYAML []byte

// changeMgmtNoteDrySha is the shared proposed.note.drySha across all branches in the
// change-management fixture PromotionStrategy (must match expr + template expectations).
const changeMgmtNoteDrySha = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// changeMgmtAltNoteDrySha breaks allNoteDryShasMatch when used on a single branch.
const changeMgmtAltNoteDrySha = "cccccccccccccccccccccccccccccccccccccccc"

// changeMgmtBaselineFingerprint is the join(map(specEnvsList, ...), "|") for changeManagementArgoconDemoPS().
const changeMgmtBaselineFingerprint = "environment/dev:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa|" +
	"environment/staging:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa|" +
	"environments/production:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa|" +
	"environments/production-eu:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func loadChangeManagementWRCSByName(name string) *promoterv1alpha1.WebRequestCommitStatus {
	docs := bytes.Split(changeManagementWebrequestsYAML, []byte("\n---\n"))
	for _, doc := range docs {
		doc = bytes.TrimSpace(doc)
		if len(doc) == 0 {
			continue
		}
		jsonDoc, err := sigyaml.YAMLToJSON(doc)
		Expect(err).ToNot(HaveOccurred())
		var w promoterv1alpha1.WebRequestCommitStatus
		Expect(json.Unmarshal(jsonDoc, &w)).To(Succeed())
		if w.Name == name {
			return &w
		}
	}
	Fail("WebRequestCommitStatus not found in bundle: " + name)
	return nil
}

// changeManagementArgoconDemoPS matches the branch layout and gates expected by the
// change-management-open / change-management-approval expr fixtures (testdata YAML).
func changeManagementArgoconDemoPS() *promoterv1alpha1.PromotionStrategy {
	const proposedHydrated = "dddddddddddddddddddddddddddddddddddddddd"
	open := promoterv1alpha1.PullRequestOpen

	env := func(branch string, keys ...string) promoterv1alpha1.Environment {
		e := promoterv1alpha1.Environment{Branch: branch}
		for _, k := range keys {
			e.ProposedCommitStatuses = append(e.ProposedCommitStatuses, promoterv1alpha1.CommitStatusSelector{Key: k})
		}
		return e
	}
	envStatus := func(branch string, pr *promoterv1alpha1.PullRequestCommonStatus) promoterv1alpha1.EnvironmentStatus {
		return promoterv1alpha1.EnvironmentStatus{
			Branch: branch,
			Proposed: promoterv1alpha1.CommitBranchState{
				Hydrated: promoterv1alpha1.CommitShaState{Sha: proposedHydrated},
				Note:     &promoterv1alpha1.HydratorMetadata{DrySha: changeMgmtNoteDrySha},
			},
			Active: promoterv1alpha1.CommitBranchState{
				Hydrated: promoterv1alpha1.CommitShaState{Sha: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
			},
			PullRequest: pr,
		}
	}

	return &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{Name: "argocon-demo", Namespace: "default"},
		Spec: promoterv1alpha1.PromotionStrategySpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{Name: "my-app-repo"},
			Environments: []promoterv1alpha1.Environment{
				env("environment/dev"),
				env("environment/staging"),
				env("environments/production", "change-management-open", "change-management-approval"),
				env("environments/production-eu", "change-management-open", "change-management-approval"),
			},
		},
		Status: promoterv1alpha1.PromotionStrategyStatus{
			Environments: []promoterv1alpha1.EnvironmentStatus{
				envStatus("environment/dev", nil),
				envStatus("environment/staging", nil),
				envStatus("environments/production", &promoterv1alpha1.PullRequestCommonStatus{
					State: open,
					Url:   "https://example.com/pr/production",
				}),
				envStatus("environments/production-eu", &promoterv1alpha1.PullRequestCommonStatus{
					State: open,
					Url:   "https://example.com/pr/production-eu",
				}),
			},
		},
	}
}

// changeManagementPSKeyedBranchesNoOpenPR clears open PRs on production branches so hasOpenPR is false.
func changeManagementPSKeyedBranchesNoOpenPR() *promoterv1alpha1.PromotionStrategy {
	ps := changeManagementArgoconDemoPS()
	closed := promoterv1alpha1.PullRequestClosed
	ps.Status.Environments[2].PullRequest = &promoterv1alpha1.PullRequestCommonStatus{State: closed, Url: ""}
	ps.Status.Environments[3].PullRequest = &promoterv1alpha1.PullRequestCommonStatus{State: closed, Url: ""}
	return ps
}

// changeManagementPSMisalignedNoteDrySha sets a different note dry SHA on production-eu.
func changeManagementPSMisalignedNoteDrySha() *promoterv1alpha1.PromotionStrategy {
	ps := changeManagementArgoconDemoPS()
	ps.Status.Environments[3].Proposed.Note.DrySha = changeMgmtAltNoteDrySha
	return ps
}

// changeManagementPSPreGateOpenPROnLowerEnv puts an open promotion PR on staging (lowerSpecs before first keyed env).
func changeManagementPSPreGateOpenPROnLowerEnv() *promoterv1alpha1.PromotionStrategy {
	ps := changeManagementArgoconDemoPS()
	open := promoterv1alpha1.PullRequestOpen
	ps.Status.Environments[1].PullRequest = &promoterv1alpha1.PullRequestCommonStatus{
		State: open,
		Url:   "https://example.com/staging-pr",
	}
	return ps
}

// changeManagementPSOneBranchMissingNoteDrySha removes proposed.note on production so
// allSpecBranchesHaveNoteDrySha fails.
func changeManagementPSOneBranchMissingNoteDrySha() *promoterv1alpha1.PromotionStrategy {
	ps := changeManagementArgoconDemoPS()
	ps.Status.Environments[2].Proposed.Note = nil
	return ps
}

// changeManagementPSStrategyGlobalKeys adds strategy-level proposedCommitStatuses so globalHasKey is true
// (firstGatedIdx becomes 0, lowerSpecs empty, envHasKey true for every branch in hasOpenPR).
func changeManagementPSStrategyGlobalKeys() *promoterv1alpha1.PromotionStrategy {
	ps := changeManagementArgoconDemoPS()
	ps.Spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
		{Key: "change-management-open"},
		{Key: "change-management-approval"},
	}
	return ps
}

func patchPromotionStrategyTriggerOutput(st *promoterv1alpha1.WebRequestCommitStatusStatus, mut func(map[string]any)) {
	Expect(st).ToNot(BeNil())
	Expect(st.PromotionStrategyContext).ToNot(BeNil())
	Expect(st.PromotionStrategyContext.TriggerOutput).ToNot(BeNil())
	m := decodeJSONMap(st.PromotionStrategyContext.TriggerOutput.Raw)
	mut(m)
	raw, err := json.Marshal(m)
	Expect(err).ToNot(HaveOccurred())
	st.PromotionStrategyContext.TriggerOutput = &apiextensionsv1.JSON{Raw: raw}
}

func decodeJSONMap(raw []byte) map[string]any {
	out := make(map[string]any)
	if len(raw) == 0 {
		return out
	}
	Expect(json.Unmarshal(raw, &out)).To(Succeed())
	return out
}

// Smoke tests for the public webrequestsimulator.Simulate API using minimal inline WRCS + PromotionStrategy
// (not the change-management YAML fixtures).
var _ = Describe("webrequestsimulator.Simulate", func() {
	var ctx context.Context

	BeforeEach(func() { ctx = context.Background() })

	// newPS builds a minimal PromotionStrategy with two branches gated on key "k".
	newPS := func() *promoterv1alpha1.PromotionStrategy {
		return &promoterv1alpha1.PromotionStrategy{
			ObjectMeta: metav1.ObjectMeta{Name: "ps", Namespace: "default"},
			Spec: promoterv1alpha1.PromotionStrategySpec{
				RepositoryReference: promoterv1alpha1.ObjectReference{Name: "repo"},
				Environments: []promoterv1alpha1.Environment{
					{Branch: "dev", ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{{Key: "k"}}},
					{Branch: "prod", ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{{Key: "k"}}},
				},
			},
			Status: promoterv1alpha1.PromotionStrategyStatus{
				Environments: []promoterv1alpha1.EnvironmentStatus{
					{
						Branch: "dev",
						Proposed: promoterv1alpha1.CommitBranchState{
							Hydrated: promoterv1alpha1.CommitShaState{Sha: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
						},
					},
					{
						Branch: "prod",
						Proposed: promoterv1alpha1.CommitBranchState{
							Hydrated: promoterv1alpha1.CommitShaState{Sha: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
						},
					},
				},
			},
		}
	}

	// newWRCS builds a minimal WebRequestCommitStatus with polling mode, key "k"
	// (matching newPS), and the supplied success expression.
	newWRCS := func(
		mode promoterv1alpha1.ModeSpec,
		successExpr string,
	) *promoterv1alpha1.WebRequestCommitStatus {
		return &promoterv1alpha1.WebRequestCommitStatus{
			ObjectMeta: metav1.ObjectMeta{Name: "wrcs", Namespace: "default"},
			Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
				PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "ps"},
				Key:                  "k",
				ReportOn:             "proposed",
				HTTPRequest: promoterv1alpha1.HTTPRequestSpec{
					URLTemplate: "https://example.com/{{ .Branch }}",
					Method:      "GET",
				},
				Success: promoterv1alpha1.SuccessSpec{When: promoterv1alpha1.WhenWithOutputSpec{Expression: successExpr}},
				Mode:    mode,
			},
		}
	}

	// Uses mode.context=environments with polling interval 0 so every branch fires immediately.
	// Asserts per-environment Status rows, two rendered GETs (one per branch), and success.when
	// from HTTP 200 — mirrors controller behavior for the default request scope.
	It("Simulate: environments context — GET per branch, Environments status, no PS context", func() {
		wrcs := newWRCS(
			promoterv1alpha1.ModeSpec{Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}}},
			"Response.StatusCode == 200",
		)

		r, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: wrcs,
			PromotionStrategy:      newPS(),
			HTTPResponse:           &webrequestsimulator.HTTPResponse{StatusCode: 200},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r.Status.Environments).To(HaveLen(2))
		Expect(r.Status.PromotionStrategyContext).To(BeNil())
		Expect(r.RenderedRequests).To(HaveLen(2))
		Expect(r.CommitStatuses).To(HaveLen(2))
		for _, e := range r.Status.Environments {
			Expect(e.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
		}
	})

	// Polling always evaluates as "should fire"; simulator requires HTTPResponse when a request runs.
	// Omitting HTTPResponse exercises the public error wrap from webrequestsimulator.Simulate.
	It("Simulate returns a wrapped error when polling would fire but HTTPResponse is nil", func() {
		wrcs := newWRCS(
			promoterv1alpha1.ModeSpec{Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}}},
			"true",
		)
		_, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: wrcs,
			PromotionStrategy:      newPS(),
			// HTTPResponse deliberately nil to force the "required" error path.
		})
		Expect(err).To(MatchError(ContainSubstring("HTTPResponse is required")))
	})

	// mode.context=promotionstrategy collapses to one shared HTTP call; RenderedRequests[0].Branch is "".
	// Status is written to PromotionStrategyContext, not per-environment Status slice.
	It("Simulate: promotionstrategy context — one GET, empty Branch, PS context status", func() {
		wrcs := newWRCS(
			promoterv1alpha1.ModeSpec{
				Context: promoterv1alpha1.ContextPromotionStrategy,
				Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}},
			},
			"true",
		)
		r, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: wrcs,
			PromotionStrategy:      newPS(),
			HTTPResponse:           &webrequestsimulator.HTTPResponse{StatusCode: 200},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r.RenderedRequests).To(HaveLen(1))
		Expect(r.RenderedRequests[0].Branch).To(Equal(""))
		Expect(r.Status.PromotionStrategyContext).ToNot(BeNil())
		Expect(r.CommitStatuses).To(HaveLen(2))
	})

	// Input.NamespaceMetadata is forwarded as template data; URL template reads .NamespaceMetadata.Labels.
	It("Simulate forwards NamespaceMetadata into Go template rendering for URLTemplate", func() {
		wrcs := newWRCS(
			promoterv1alpha1.ModeSpec{Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}}},
			"true",
		)
		wrcs.Spec.HTTPRequest.URLTemplate = "https://example.com/{{ index .NamespaceMetadata.Labels \"team\" }}/{{ .Branch }}"
		r, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: wrcs,
			PromotionStrategy:      newPS(),
			NamespaceMetadata:      webrequestsimulator.NamespaceMetadata{Labels: map[string]string{"team": "payments"}},
			HTTPResponse:           &webrequestsimulator.HTTPResponse{StatusCode: 200},
		})
		Expect(err).ToNot(HaveOccurred())
		for _, req := range r.RenderedRequests {
			Expect(req.URL).To(HavePrefix("https://example.com/payments/"))
		}
	})
})

// End-to-end expr + template coverage for real change-management manifests (testdata YAML) and a
// matching four-branch PromotionStrategy. Each case calls webrequestsimulator.Simulate once or twice
// and asserts rendered HTTP, status JSON (trigger/response), and CommitStatus phases.
var _ = Describe("change management WebRequestCommitStatus fixtures (full expr)", func() {
	var ctx context.Context
	BeforeEach(func() { ctx = context.Background() })

	// openOKBody is a minimal valid POST body for change-management-open success.when (202 + non-empty id).
	openOKBody := func() map[string]any {
		return map[string]any{
			"id":      "9f515fd4-0354-40d7-9c71-a83856372bc3",
			"message": "accepted",
			"change_request": map[string]any{
				"short_description": "cr",
				"start_time":        "2020-01-01T00:00:00Z",
				"end_time":          "2030-01-01T00:00:00Z",
			},
		}
	}

	// expectAllBranches asserts every entry in PromotionStrategyContext.phasePerBranch (applicable envs only).
	expectAllBranches := func(
		phase promoterv1alpha1.CommitStatusPhase,
		st *promoterv1alpha1.WebRequestCommitStatusPromotionStrategyContextStatus,
	) {
		Expect(st).ToNot(BeNil())
		for _, p := range st.PhasePerBranch {
			Expect(p.Phase).To(Equal(phase))
		}
	}

	// First reconcile: gates pass (isNewFingerprint), POST returns 202 + id → success.when HTTP branch;
	// trigger/response output maps are asserted. Second reconcile: same PS fingerprint, nil HTTPResponse —
	// trigger is false (no isNewFingerprint/needsRetry); success.when no-HTTP branch keeps success.
	It("change-management-open (YAML): happy-path POST, trigger/response output, carry-forward", func() {
		w := loadChangeManagementWRCSByName("change-management-open")
		ps := changeManagementArgoconDemoPS()

		r1, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponse: &webrequestsimulator.HTTPResponse{
				StatusCode: 202,
				Body:       openOKBody(),
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r1.RenderedRequests).To(HaveLen(1))
		Expect(r1.RenderedRequests[0].Method).To(Equal("POST"))
		Expect(r1.RenderedRequests[0].URL).To(ContainSubstring("/v1/change-management-service/change"))
		Expect(r1.RenderedRequests[0].Body).To(ContainSubstring(changeMgmtNoteDrySha))
		Expect(r1.RenderedRequests[0].Body).To(ContainSubstring("argocon-demo"))

		expectAllBranches(promoterv1alpha1.CommitPhaseSuccess, r1.Status.PromotionStrategyContext)

		trig := decodeJSONMap(r1.Status.PromotionStrategyContext.TriggerOutput.Raw)
		Expect(trig["shouldTrigger"]).To(BeTrue())
		Expect(trig["hasOpenPR"]).To(BeTrue())
		Expect(trig["allNoteDryShasMatch"]).To(BeTrue())
		Expect(trig["preGateNoOpenPR"]).To(BeTrue())
		Expect(trig["isNewFingerprint"]).To(BeTrue())
		Expect(trig["needsRetry"]).To(BeFalse())
		Expect(trig["fingerprint"]).To(Equal(changeMgmtBaselineFingerprint))
		Expect(trig["canonicalNoteDrySha"]).To(Equal(changeMgmtNoteDrySha))
		branches, ok := trig["strategyBranches"].([]any)
		Expect(ok).To(BeTrue())
		Expect(branches).To(HaveLen(4))
		Expect(trig["lastStatusCode"]).To(BeNumerically("==", 0))

		resp := decodeJSONMap(r1.Status.PromotionStrategyContext.ResponseOutput.Raw)
		Expect(resp["statusCode"]).To(BeNumerically("==", 202))
		Expect(resp["changeId"]).To(Equal("9f515fd4-0354-40d7-9c71-a83856372bc3"))
		Expect(resp["message"]).To(Equal("accepted"))
		cr, ok := resp["changeRequest"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(cr["short_description"]).To(Equal("cr"))

		for _, cs := range r1.CommitStatuses {
			Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			Expect(cs.Spec.Description).To(ContainSubstring("success"))
		}

		w2 := w.DeepCopy()
		w2.Status = r1.Status
		r2, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: w2,
			PromotionStrategy:      ps,
			HTTPResponse:           nil,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r2.RenderedRequests).To(BeEmpty())
		trig2 := decodeJSONMap(r2.Status.PromotionStrategyContext.TriggerOutput.Raw)
		Expect(trig2["shouldTrigger"]).To(BeFalse())
		Expect(trig2["isNewFingerprint"]).To(BeFalse())
		Expect(trig2["needsRetry"]).To(BeFalse())
		expectAllBranches(promoterv1alpha1.CommitPhaseSuccess, r2.Status.PromotionStrategyContext)
	})

	// Each row uses a PS variant that breaks exactly one of Variables.hasOpenPR | allNoteDryShasMatch | preGateNoOpenPR.
	// Trigger still evaluates when.output (shouldTrigger false); no POST; phases stay pending.
	DescribeTable("change-management-open (fixture YAML): POST not sent when one trigger gate is false "+
		"(assert trigger output booleans)",
		func(ps *promoterv1alpha1.PromotionStrategy, want map[string]any) {
			w := loadChangeManagementWRCSByName("change-management-open")
			r, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
				WebRequestCommitStatus: w,
				PromotionStrategy:      ps,
				HTTPResponse: &webrequestsimulator.HTTPResponse{
					StatusCode: 202,
					Body:       openOKBody(),
				},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(r.RenderedRequests).To(BeEmpty())
			trig := decodeJSONMap(r.Status.PromotionStrategyContext.TriggerOutput.Raw)
			Expect(trig["shouldTrigger"]).To(BeFalse())
			for k, v := range want {
				Expect(trig[k]).To(Equal(v), "unexpected trigger output field %q", k)
			}
			expectAllBranches(promoterv1alpha1.CommitPhasePending, r.Status.PromotionStrategyContext)
		},
		Entry("gate: hasOpenPR — no open PR on branches that list the WRCS key",
			changeManagementPSKeyedBranchesNoOpenPR(),
			map[string]any{"hasOpenPR": false, "allNoteDryShasMatch": true, "preGateNoOpenPR": true},
		),
		Entry("gate: allNoteDryShasMatch — one branch has a different proposed.note.drySha",
			changeManagementPSMisalignedNoteDrySha(),
			map[string]any{"hasOpenPR": true, "allNoteDryShasMatch": false, "preGateNoOpenPR": true},
		),
		Entry("gate: preGateNoOpenPR — open promotion PR on a spec-ordered env before the first keyed env",
			changeManagementPSPreGateOpenPROnLowerEnv(),
			map[string]any{"hasOpenPR": true, "allNoteDryShasMatch": true, "preGateNoOpenPR": false},
		),
		Entry("gate: allNoteDryShasMatch — a spec branch has no proposed.note / drySha",
			changeManagementPSOneBranchMissingNoteDrySha(),
			map[string]any{"hasOpenPR": true, "allNoteDryShasMatch": false, "preGateNoOpenPR": true},
		),
	)

	// Strategy-level proposedCommitStatuses makes globalHasKey true → firstGatedIdx 0 → lowerSpecs empty
	// → preGateNoOpenPR vacuously true; POST still fires with baseline PR state on prod branches.
	It("change-management-open (YAML): strategy-level keys, firstGatedIdx=0, POST succeeds", func() {
		w := loadChangeManagementWRCSByName("change-management-open")
		ps := changeManagementPSStrategyGlobalKeys()
		r, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponse: &webrequestsimulator.HTTPResponse{
				StatusCode: 202,
				Body:       openOKBody(),
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r.RenderedRequests).To(HaveLen(1))
		trig := decodeJSONMap(r.Status.PromotionStrategyContext.TriggerOutput.Raw)
		Expect(trig["shouldTrigger"]).To(BeTrue())
		Expect(trig["preGateNoOpenPR"]).To(BeTrue())
		expectAllBranches(promoterv1alpha1.CommitPhaseSuccess, r.Status.PromotionStrategyContext)
	})

	// Reconcile 1: POST returns 503 → pending; persisted fingerprint matches next reconcile.
	// Reconcile 2: isNewFingerprint false, needsRetry true (same fp, pending, ResponseOutput.statusCode retryable);
	// POST returns 202 → success. Asserts needsRetry stays true in persisted trigger output (computed pre-HTTP).
	It("change-management-open (YAML): needsRetry — 503 then 202 on same fingerprint", func() {
		w := loadChangeManagementWRCSByName("change-management-open")
		ps := changeManagementArgoconDemoPS()

		r1, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponse: &webrequestsimulator.HTTPResponse{
				StatusCode: 503,
				Body:       map[string]any{"error": "unavailable"},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r1.RenderedRequests).To(HaveLen(1))
		expectAllBranches(promoterv1alpha1.CommitPhasePending, r1.Status.PromotionStrategyContext)
		t1 := decodeJSONMap(r1.Status.PromotionStrategyContext.TriggerOutput.Raw)
		Expect(t1["shouldTrigger"]).To(BeTrue())
		Expect(t1["isNewFingerprint"]).To(BeTrue())
		Expect(t1["fingerprint"]).To(Equal(changeMgmtBaselineFingerprint))

		w2 := w.DeepCopy()
		w2.Status = *r1.Status.DeepCopy()
		r2, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: w2,
			PromotionStrategy:      ps,
			HTTPResponse: &webrequestsimulator.HTTPResponse{
				StatusCode: 202,
				Body:       openOKBody(),
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r2.RenderedRequests).To(HaveLen(1))
		t2 := decodeJSONMap(r2.Status.PromotionStrategyContext.TriggerOutput.Raw)
		Expect(t2["shouldTrigger"]).To(BeTrue())
		Expect(t2["isNewFingerprint"]).To(BeFalse())
		// needsRetry is computed in when.variables using Phase + ResponseOutput from *before* this
		// reconcile's HTTP, so it stays true on the retry row even after the POST succeeds.
		Expect(t2["needsRetry"]).To(BeTrue())
		expectAllBranches(promoterv1alpha1.CommitPhaseSuccess, r2.Status.PromotionStrategyContext)
	})

	// 400 is not retryable (expr: 429 or >=500). Second reconcile: no POST (shouldTrigger false), needsRetry false.
	It("change-management-open (YAML): 400 not retryable; second reconcile skips HTTP", func() {
		w := loadChangeManagementWRCSByName("change-management-open")
		ps := changeManagementArgoconDemoPS()
		r1, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponse:           &webrequestsimulator.HTTPResponse{StatusCode: 400, Body: map[string]any{"error": "bad"}},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r1.RenderedRequests).To(HaveLen(1))
		expectAllBranches(promoterv1alpha1.CommitPhasePending, r1.Status.PromotionStrategyContext)
		t1 := decodeJSONMap(r1.Status.PromotionStrategyContext.TriggerOutput.Raw)
		Expect(t1["shouldTrigger"]).To(BeTrue())
		Expect(t1["needsRetry"]).To(BeFalse())

		w2 := w.DeepCopy()
		w2.Status = *r1.Status.DeepCopy()
		r2, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: w2,
			PromotionStrategy:      ps,
			HTTPResponse:           nil,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r2.RenderedRequests).To(BeEmpty())
		t2 := decodeJSONMap(r2.Status.PromotionStrategyContext.TriggerOutput.Raw)
		Expect(t2["shouldTrigger"]).To(BeFalse())
		Expect(t2["needsRetry"]).To(BeFalse())
	})

	// Same two-step pattern as 503: 429 is explicitly retryable, then 202 completes the change.
	It("change-management-open (fixture YAML): 429 then 202 exercises isRetryable for status code 429", func() {
		w := loadChangeManagementWRCSByName("change-management-open")
		ps := changeManagementArgoconDemoPS()
		r1, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponse: &webrequestsimulator.HTTPResponse{
				StatusCode: 429,
				Body:       map[string]any{"error": "rate limited"},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		t1 := decodeJSONMap(r1.Status.PromotionStrategyContext.TriggerOutput.Raw)
		Expect(t1["shouldTrigger"]).To(BeTrue())

		w2 := w.DeepCopy()
		w2.Status = *r1.Status.DeepCopy()
		r2, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: w2,
			PromotionStrategy:      ps,
			HTTPResponse:           &webrequestsimulator.HTTPResponse{StatusCode: 202, Body: openOKBody()},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r2.RenderedRequests).To(HaveLen(1))
		expectAllBranches(promoterv1alpha1.CommitPhaseSuccess, r2.Status.PromotionStrategyContext)
	})

	// Trigger still true (happy PS); each HTTP response fails success.when’s Response != nil branch
	// (status not 202, or id missing/empty). Phases stay pending after one POST.
	DescribeTable("change-management-open (fixture YAML): success.when HTTP ternary fails unless 202 + non-empty id",
		func(resp *webrequestsimulator.HTTPResponse) {
			w := loadChangeManagementWRCSByName("change-management-open")
			ps := changeManagementArgoconDemoPS()
			r, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
				WebRequestCommitStatus: w,
				PromotionStrategy:      ps,
				HTTPResponse:           resp,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(r.RenderedRequests).To(HaveLen(1))
			expectAllBranches(promoterv1alpha1.CommitPhasePending, r.Status.PromotionStrategyContext)
		},
		Entry("HTTP branch: status 200 with id still fails (requires 202)",
			&webrequestsimulator.HTTPResponse{StatusCode: 200, Body: map[string]any{"id": "x"}},
		),
		Entry("HTTP branch: 202 with Body.id null fails id check",
			&webrequestsimulator.HTTPResponse{StatusCode: 202, Body: map[string]any{"id": nil, "message": "m"}},
		),
		Entry("HTTP branch: 202 with Body.id empty string fails",
			&webrequestsimulator.HTTPResponse{StatusCode: 202, Body: map[string]any{"id": "", "message": "m"}},
		),
		Entry("HTTP branch: 202 with no id key — id nil in body map, changeId empty in response.output",
			&webrequestsimulator.HTTPResponse{StatusCode: 202, Body: map[string]any{"message": "m"}},
		),
	)

	// response.output expression uses string(id) / string(message) with nil guards — assert persisted map.
	It("change-management-open (fixture YAML): response.output maps nil Body.id and nil message to empty strings", func() {
		w := loadChangeManagementWRCSByName("change-management-open")
		ps := changeManagementArgoconDemoPS()
		r, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponse: &webrequestsimulator.HTTPResponse{
				StatusCode: 202,
				Body:       map[string]any{"message": nil},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r.RenderedRequests).To(HaveLen(1))
		out := decodeJSONMap(r.Status.PromotionStrategyContext.ResponseOutput.Raw)
		Expect(out["changeId"]).To(Equal(""))
		Expect(out["message"]).To(Equal(""))
	})

	// GET returns in-window change_records → success.when HTTP branch; response.output approvedCount/totalRecordCount.
	// Second reconcile nil HTTP: trigger false (isFirstRun false, isNewFingerprint false, shouldTriggerByTime false).
	It("change-management-approval (YAML): happy GET, outputs, carry-forward without HTTP", func() {
		w := loadChangeManagementWRCSByName("change-management-approval")
		ps := changeManagementArgoconDemoPS()

		start := time.Now().UTC().Add(-30 * time.Minute).Format(time.RFC3339)
		end := time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)

		r1, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponse: &webrequestsimulator.HTTPResponse{
				StatusCode: 200,
				Body: map[string]any{
					"change_records": []any{
						map[string]any{
							"change_request": map[string]any{
								"start_time": start,
								"end_time":   end,
							},
						},
					},
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r1.RenderedRequests).To(HaveLen(1))
		Expect(r1.RenderedRequests[0].Method).To(Equal("GET"))
		Expect(r1.RenderedRequests[0].URL).To(ContainSubstring("changes/search"))
		Expect(r1.RenderedRequests[0].URL).To(ContainSubstring("commit_id=" + changeMgmtNoteDrySha))
		Expect(r1.RenderedRequests[0].URL).To(ContainSubstring("asset_id=1372489579564901493"))

		expectAllBranches(promoterv1alpha1.CommitPhaseSuccess, r1.Status.PromotionStrategyContext)

		trig := decodeJSONMap(r1.Status.PromotionStrategyContext.TriggerOutput.Raw)
		Expect(trig["shouldTrigger"]).To(BeTrue())
		Expect(trig["hasOpenPR"]).To(BeTrue())
		Expect(trig["allNoteDryShasMatch"]).To(BeTrue())
		Expect(trig["preGateNoOpenPR"]).To(BeTrue())
		// isFirstRun / shouldTriggerByTime live only in when.variables, not in when.output JSON.
		Expect(trig["isNewFingerprint"]).To(BeTrue())
		Expect(trig["fingerprint"]).To(Equal(changeMgmtBaselineFingerprint))

		resp := decodeJSONMap(r1.Status.PromotionStrategyContext.ResponseOutput.Raw)
		Expect(resp["statusCode"]).To(BeNumerically("==", 200))
		Expect(resp["approvedCount"]).To(BeNumerically("==", 1))
		Expect(resp["totalRecordCount"]).To(BeNumerically("==", 1))

		for _, cs := range r1.CommitStatuses {
			Expect(cs.Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			Expect(cs.Spec.Description).To(ContainSubstring("success"))
			Expect(cs.Spec.Description).To(ContainSubstring("/"))
		}

		w2 := w.DeepCopy()
		w2.Status = *r1.Status.DeepCopy()
		r2, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: w2,
			PromotionStrategy:      ps,
			HTTPResponse:           nil,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r2.RenderedRequests).To(BeEmpty())
		trig2 := decodeJSONMap(r2.Status.PromotionStrategyContext.TriggerOutput.Raw)
		Expect(trig2["shouldTrigger"]).To(BeFalse())
		expectAllBranches(promoterv1alpha1.CommitPhaseSuccess, r2.Status.PromotionStrategyContext)
	})

	// Same gate variables as open; approval trigger adds isFirstRun || isNewFingerprint || shouldTriggerByTime.
	// First run: isFirstRun true would allow GET — we break a gate so shouldTrigger is false and no GET is sent.
	DescribeTable("change-management-approval (fixture YAML): GET not sent when a trigger gate is false",
		func(ps *promoterv1alpha1.PromotionStrategy, want map[string]any) {
			w := loadChangeManagementWRCSByName("change-management-approval")
			start := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
			end := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339)
			r, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
				WebRequestCommitStatus: w,
				PromotionStrategy:      ps,
				HTTPResponse: &webrequestsimulator.HTTPResponse{
					StatusCode: 200,
					Body: map[string]any{
						"change_records": []any{
							map[string]any{
								"change_request": map[string]any{
									"start_time": start,
									"end_time":   end,
								},
							},
						},
					},
				},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(r.RenderedRequests).To(BeEmpty())
			trig := decodeJSONMap(r.Status.PromotionStrategyContext.TriggerOutput.Raw)
			Expect(trig["shouldTrigger"]).To(BeFalse())
			for k, v := range want {
				Expect(trig[k]).To(Equal(v), "field %q", k)
			}
		},
		Entry("gate: hasOpenPR false",
			changeManagementPSKeyedBranchesNoOpenPR(),
			map[string]any{"hasOpenPR": false, "preGateNoOpenPR": true},
		),
		Entry("gate: preGateNoOpenPR false",
			changeManagementPSPreGateOpenPROnLowerEnv(),
			map[string]any{"hasOpenPR": true, "preGateNoOpenPR": false},
		),
		Entry("gate: allNoteDryShasMatch false",
			changeManagementPSMisalignedNoteDrySha(),
			map[string]any{"hasOpenPR": true, "allNoteDryShasMatch": false},
		),
	)

	// After first GET, patch TriggerOutput.lastRequestTime to >1m ago while fingerprint unchanged;
	// shouldTriggerByTime becomes true so a second GET runs (isNewFingerprint still false).
	It("change-management-approval (YAML): stale lastRequestTime forces second GET", func() {
		w := loadChangeManagementWRCSByName("change-management-approval")
		ps := changeManagementArgoconDemoPS()
		start := time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)
		end := time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)
		okBody := map[string]any{
			"change_records": []any{
				map[string]any{
					"change_request": map[string]any{
						"start_time": start,
						"end_time":   end,
					},
				},
			},
		}

		r1, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponse:           &webrequestsimulator.HTTPResponse{StatusCode: 200, Body: okBody},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r1.RenderedRequests).To(HaveLen(1))

		w2 := w.DeepCopy()
		w2.Status = *r1.Status.DeepCopy()
		patchPromotionStrategyTriggerOutput(&w2.Status, func(m map[string]any) {
			m["lastRequestTime"] = time.Now().UTC().Add(-125 * time.Minute).Format(time.RFC3339Nano)
		})

		r2, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: w2,
			PromotionStrategy:      ps,
			HTTPResponse:           &webrequestsimulator.HTTPResponse{StatusCode: 200, Body: okBody},
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r2.RenderedRequests).To(HaveLen(1))
		t2 := decodeJSONMap(r2.Status.PromotionStrategyContext.TriggerOutput.Raw)
		Expect(t2["shouldTrigger"]).To(BeTrue())
		Expect(t2["isNewFingerprint"]).To(BeFalse())
	})

	// Trigger fires (happy PS); each response fails success.when’s HTTP branch (wrong status, empty list, or dates).
	// response.output still runs; approvedCount is 0 for the filter expression.
	DescribeTable("change-management-approval (fixture YAML): success.when needs 200, records, in-window times",
		func(http *webrequestsimulator.HTTPResponse) {
			w := loadChangeManagementWRCSByName("change-management-approval")
			ps := changeManagementArgoconDemoPS()
			r, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
				WebRequestCommitStatus: w,
				PromotionStrategy:      ps,
				HTTPResponse:           http,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(r.RenderedRequests).To(HaveLen(1))
			expectAllBranches(promoterv1alpha1.CommitPhasePending, r.Status.PromotionStrategyContext)
			out := decodeJSONMap(r.Status.PromotionStrategyContext.ResponseOutput.Raw)
			Expect(out["approvedCount"]).To(BeNumerically("==", 0))
		},
		Entry("HTTP branch: non-200 status", &webrequestsimulator.HTTPResponse{
			StatusCode: 201,
			Body:       map[string]any{"change_records": []any{}},
		}),
		Entry("HTTP branch: 200 with empty change_records", &webrequestsimulator.HTTPResponse{
			StatusCode: 200,
			Body:       map[string]any{"change_records": []any{}},
		}),
		Entry("HTTP branch: 200 but no record brackets now()",
			&webrequestsimulator.HTTPResponse{StatusCode: 200, Body: map[string]any{"change_records": []any{
				map[string]any{"change_request": map[string]any{
					"start_time": time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339),
					"end_time":   time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339),
				}},
			}}},
		),
	)

	// response.output filters with the same date predicate as success.when; totalRecordCount is raw list length.
	It("change-management-approval (YAML): approvedCount in-window; totalRecordCount all records", func() {
		w := loadChangeManagementWRCSByName("change-management-approval")
		ps := changeManagementArgoconDemoPS()
		inStart := time.Now().UTC().Add(-5 * time.Minute).Format(time.RFC3339)
		inEnd := time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)
		outStart := time.Now().UTC().Add(-72 * time.Hour).Format(time.RFC3339)
		outEnd := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
		r, err := webrequestsimulator.Simulate(ctx, webrequestsimulator.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponse: &webrequestsimulator.HTTPResponse{
				StatusCode: 200,
				Body: map[string]any{
					"change_records": []any{
						map[string]any{"change_request": map[string]any{"start_time": inStart, "end_time": inEnd}},
						map[string]any{"change_request": map[string]any{"start_time": outStart, "end_time": outEnd}},
					},
				},
			},
		})
		Expect(err).ToNot(HaveOccurred())
		out := decodeJSONMap(r.Status.PromotionStrategyContext.ResponseOutput.Raw)
		Expect(out["totalRecordCount"]).To(BeNumerically("==", 2))
		Expect(out["approvedCount"]).To(BeNumerically("==", 1))
	})
})
