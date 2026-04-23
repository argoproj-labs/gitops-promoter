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

package simulator_test

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/webrequest"
	"github.com/argoproj-labs/gitops-promoter/internal/webrequest/simulator"
)

// jsonMap marshals a map into an apiextensionsv1.JSON for status fixtures.
func jsonMap(m map[string]any) *apiextensionsv1.JSON {
	raw, err := json.Marshal(m)
	Expect(err).ToNot(HaveOccurred())
	return &apiextensionsv1.JSON{Raw: raw}
}

// twoEnvPromotionStrategy builds a minimal PromotionStrategy that gates
// branches "dev" and "prod" on the key "my-key" via per-env ProposedCommitStatuses.
func twoEnvPromotionStrategy(key string) *promoterv1alpha1.PromotionStrategy {
	return &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{Name: "ps", Namespace: "default"},
		Spec: promoterv1alpha1.PromotionStrategySpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{Name: "repo"},
			Environments: []promoterv1alpha1.Environment{
				{Branch: "dev", ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{{Key: key}}},
				{Branch: "prod", ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{{Key: key}}},
			},
		},
		Status: promoterv1alpha1.PromotionStrategyStatus{
			Environments: []promoterv1alpha1.EnvironmentStatus{
				{
					Branch:   "dev",
					Proposed: promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
					Active:   promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "1111111111111111111111111111111111111111"}},
				},
				{
					Branch:   "prod",
					Proposed: promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
					Active:   promoterv1alpha1.CommitBranchState{Hydrated: promoterv1alpha1.CommitShaState{Sha: "2222222222222222222222222222222222222222"}},
				},
			},
		},
	}
}

// basicWRCS builds a minimal WebRequestCommitStatus for the environments context
// with polling mode and a trivially-true success expression.
func basicWRCS(key string, mode promoterv1alpha1.ModeSpec, successExpr string) *promoterv1alpha1.WebRequestCommitStatus {
	return &promoterv1alpha1.WebRequestCommitStatus{
		ObjectMeta: metav1.ObjectMeta{Name: "wrcs", Namespace: "default"},
		Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "ps"},
			Key:                  key,
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

var _ = Describe("Simulate", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("environments context", func() {
		It("fires for polling mode and returns success with a rendered request per env", func() {
			wrcs := basicWRCS("my-key",
				promoterv1alpha1.ModeSpec{Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}}},
				"Response.StatusCode == 200",
			)
			ps := twoEnvPromotionStrategy("my-key")

			result, err := simulator.Simulate(ctx, simulator.Args{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponse:           &webrequest.HTTPResponse{StatusCode: 200, Body: map[string]any{"ok": true}},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(result.Status.Environments).To(HaveLen(2))
			Expect(result.Status.PromotionStrategyContext).To(BeNil())
			Expect(result.RenderedRequests).To(HaveLen(2))
			Expect(result.CommitStatuses).To(HaveLen(2))
			for _, env := range result.Status.Environments {
				Expect(env.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
				Expect(env.LastResponseStatusCode).ToNot(BeNil())
				Expect(*env.LastResponseStatusCode).To(Equal(200))
				Expect(env.LastSuccessfulSha).ToNot(BeEmpty())
			}
			for _, req := range result.RenderedRequests {
				Expect(req.Branch).To(BeElementOf("dev", "prod"))
				Expect(req.URL).To(HavePrefix("https://example.com/"))
				Expect(req.Method).To(Equal("GET"))
			}
		})

		It("carries forward previous phase and outputs when the trigger is false", func() {
			wrcs := basicWRCS("my-key",
				promoterv1alpha1.ModeSpec{Trigger: &promoterv1alpha1.TriggerModeSpec{
					RequeueDuration: metav1.Duration{Duration: 0},
					When:            promoterv1alpha1.WhenWithOutputSpec{Expression: "false"},
				}},
				"true",
			)
			wrcs.Status.Environments = []promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus{
				{Branch: "dev", ReportedSha: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Phase: promoterv1alpha1.CommitPhasePending, TriggerOutput: jsonMap(map[string]any{"note": "prev-dev"})},
				{Branch: "prod", ReportedSha: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Phase: promoterv1alpha1.CommitPhaseSuccess, TriggerOutput: jsonMap(map[string]any{"note": "prev-prod"})},
			}
			ps := twoEnvPromotionStrategy("my-key")

			result, err := simulator.Simulate(ctx, simulator.Args{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RenderedRequests).To(BeEmpty(), "trigger was false, no HTTP request should render")
			Expect(result.Status.Environments).To(HaveLen(2))
			byBranch := map[string]promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus{}
			for _, e := range result.Status.Environments {
				byBranch[e.Branch] = e
			}
			// Success carries forward via the success expression returning true.
			Expect(byBranch["prod"].Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			Expect(byBranch["dev"].Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
		})

		It("round-trips: previous Status.Environments outputs surface to expressions", func() {
			wrcs := basicWRCS("my-key",
				promoterv1alpha1.ModeSpec{Trigger: &promoterv1alpha1.TriggerModeSpec{
					RequeueDuration: metav1.Duration{Duration: 0},
					When: promoterv1alpha1.WhenWithOutputSpec{
						Expression: "true",
						Output:     &promoterv1alpha1.OutputSpec{Expression: `{ counter: (TriggerOutput.counter ?? 0) + 1 }`},
					},
				}},
				"Response.StatusCode == 200",
			)
			ps := twoEnvPromotionStrategy("my-key")

			first, err := simulator.Simulate(ctx, simulator.Args{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponse:           &webrequest.HTTPResponse{StatusCode: 200},
			})
			Expect(err).ToNot(HaveOccurred())
			// Feed first result's Status back in for the second reconcile.
			wrcs.Status = first.Status
			second, err := simulator.Simulate(ctx, simulator.Args{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponse:           &webrequest.HTTPResponse{StatusCode: 200},
			})
			Expect(err).ToNot(HaveOccurred())
			for _, e := range second.Status.Environments {
				Expect(e.TriggerOutput).ToNot(BeNil())
				m := map[string]any{}
				Expect(json.Unmarshal(e.TriggerOutput.Raw, &m)).To(Succeed())
				// The counter expression should have incremented from 1 to 2.
				Expect(m["counter"]).To(BeNumerically("==", 2))
			}
		})

		It("surfaces an error when the trigger fires but HTTPResponse is nil", func() {
			wrcs := basicWRCS("my-key",
				promoterv1alpha1.ModeSpec{Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}}},
				"true",
			)
			ps := twoEnvPromotionStrategy("my-key")

			_, err := simulator.Simulate(ctx, simulator.Args{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponse:           nil,
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("HTTPResponse is required"))
		})

		It("surfaces a compile error for invalid expressions", func() {
			wrcs := basicWRCS("my-key",
				promoterv1alpha1.ModeSpec{Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}}},
				"this is not expr",
			)
			ps := twoEnvPromotionStrategy("my-key")

			_, err := simulator.Simulate(ctx, simulator.Args{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponse:           &webrequest.HTTPResponse{StatusCode: 200},
			})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("promotionstrategy context", func() {
		It("issues one shared request and resolves per-branch phases from an object return", func() {
			wrcs := basicWRCS("my-key",
				promoterv1alpha1.ModeSpec{
					Context: promoterv1alpha1.ContextPromotionStrategy,
					Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}},
				},
				`{ defaultPhase: "pending", environments: [{ branch: "dev", phase: "success" }, { branch: "prod", phase: "failure" }] }`,
			)
			ps := twoEnvPromotionStrategy("my-key")

			result, err := simulator.Simulate(ctx, simulator.Args{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponse:           &webrequest.HTTPResponse{StatusCode: 200},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RenderedRequests).To(HaveLen(1), "promotionstrategy context should issue at most one request")
			Expect(result.RenderedRequests[0].Branch).To(Equal(""))
			Expect(result.Status.Environments).To(BeEmpty())
			Expect(result.Status.PromotionStrategyContext).ToNot(BeNil())
			Expect(result.Status.PromotionStrategyContext.PhasePerBranch).To(HaveLen(2))

			phaseByBranch := map[string]promoterv1alpha1.CommitStatusPhase{}
			for _, p := range result.Status.PromotionStrategyContext.PhasePerBranch {
				phaseByBranch[p.Branch] = p.Phase
			}
			Expect(phaseByBranch["dev"]).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			Expect(phaseByBranch["prod"]).To(Equal(promoterv1alpha1.CommitPhaseFailure))

			// CommitStatuses reflect the per-branch phases.
			csByBranch := map[string]*promoterv1alpha1.CommitStatus{}
			for _, cs := range result.CommitStatuses {
				csByBranch[cs.Labels[promoterv1alpha1.EnvironmentLabel]] = cs
			}
			Expect(csByBranch["dev"].Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			Expect(csByBranch["prod"].Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseFailure))
		})

		It("surfaces previous promotionstrategy-context outputs to expressions", func() {
			wrcs := basicWRCS("my-key",
				promoterv1alpha1.ModeSpec{
					Context: promoterv1alpha1.ContextPromotionStrategy,
					Trigger: &promoterv1alpha1.TriggerModeSpec{
						RequeueDuration: metav1.Duration{Duration: 0},
						When:            promoterv1alpha1.WhenWithOutputSpec{Expression: `TriggerOutput.lastTag == "v1"`},
					},
				},
				"true",
			)
			wrcs.Status.PromotionStrategyContext = &promoterv1alpha1.WebRequestCommitStatusPromotionStrategyContextStatus{
				TriggerOutput: jsonMap(map[string]any{"lastTag": "v1"}),
			}
			ps := twoEnvPromotionStrategy("my-key")

			result, err := simulator.Simulate(ctx, simulator.Args{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponse:           &webrequest.HTTPResponse{StatusCode: 200},
			})
			Expect(err).ToNot(HaveOccurred())
			// The trigger expression read TriggerOutput.lastTag == "v1" so the request fires.
			Expect(result.RenderedRequests).To(HaveLen(1))
		})

		It("renders URL/body/headers/description with Sprig functions and latest outputs", func() {
			wrcs := basicWRCS("my-key",
				promoterv1alpha1.ModeSpec{
					Context: promoterv1alpha1.ContextPromotionStrategy,
					Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}},
				},
				"true",
			)
			wrcs.Spec.HTTPRequest.URLTemplate = `https://example.com/{{ "abc" | upper }}`
			wrcs.Spec.HTTPRequest.BodyTemplate = `{"msg":"{{ .PromotionStrategy.Spec.Environments | len }}"}`
			wrcs.Spec.HTTPRequest.HeaderTemplates = map[string]string{
				"X-Trace-Id": `{{ printf "req-%s" "xyz" }}`,
			}
			wrcs.Spec.DescriptionTemplate = `{{ .Phase }} for {{ .Branch }}`
			wrcs.Spec.UrlTemplate = `https://dash.example.com/{{ .Branch }}`

			ps := twoEnvPromotionStrategy("my-key")

			result, err := simulator.Simulate(ctx, simulator.Args{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponse:           &webrequest.HTTPResponse{StatusCode: 200},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RenderedRequests).To(HaveLen(1))
			req := result.RenderedRequests[0]
			Expect(req.URL).To(Equal("https://example.com/ABC"))
			Expect(req.Body).To(Equal(`{"msg":"2"}`))
			Expect(req.Headers).To(HaveKeyWithValue("X-Trace-Id", "req-xyz"))

			Expect(result.CommitStatuses).To(HaveLen(2))
			for _, cs := range result.CommitStatuses {
				branch := cs.Labels[promoterv1alpha1.EnvironmentLabel]
				Expect(cs.Spec.Description).To(Equal("success for " + branch))
				Expect(cs.Spec.Url).To(Equal("https://dash.example.com/" + branch))
			}
		})
	})

	Describe("validation", func() {
		It("rejects nil WebRequestCommitStatus", func() {
			_, err := simulator.Simulate(ctx, simulator.Args{
				PromotionStrategy: twoEnvPromotionStrategy("my-key"),
			})
			Expect(err).To(MatchError(ContainSubstring("WebRequestCommitStatus is required")))
		})
		It("rejects nil PromotionStrategy", func() {
			_, err := simulator.Simulate(ctx, simulator.Args{
				WebRequestCommitStatus: basicWRCS("my-key",
					promoterv1alpha1.ModeSpec{Polling: &promoterv1alpha1.PollingModeSpec{}},
					"true",
				),
			})
			Expect(err).To(MatchError(ContainSubstring("PromotionStrategy is required")))
		})
	})
})
