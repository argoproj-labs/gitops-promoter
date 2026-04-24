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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	sigyaml "sigs.k8s.io/yaml"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/webrequest"
	"github.com/argoproj-labs/gitops-promoter/webrequestsimulator"
	"github.com/argoproj-labs/gitops-promoter/webrequestsimulator/simulatortypes"
)

//go:embed testdata/change_management_webrequests.yaml
var changeManagementWebrequestsYAML []byte

func unmarshalJSONMap(raw *apiextensionsv1.JSON) (map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	result := make(map[string]any)
	if err := json.Unmarshal(raw.Raw, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal JSON map: %w", err)
	}
	return result, nil
}

func marshalJSONMap(data map[string]any) (*apiextensionsv1.JSON, error) {
	if data == nil {
		return nil, nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON map: %w", err)
	}
	return &apiextensionsv1.JSON{Raw: raw}, nil
}

// pscHTTPMocks wraps a single mock as a one-element slice for promotionstrategy Simulate
// (only index 0 is used). Nil pointer yields nil slice.
func pscHTTPMocks(r *simulatortypes.HTTPResponse) []simulatortypes.HTTPResponse {
	if r == nil {
		return nil
	}
	x := *r
	return []simulatortypes.HTTPResponse{x}
}

// envHTTPMocksSame returns one HTTPResponses entry per branch with StatusCode 200 and the same Body.
func envHTTPMocksSame(branches []string, body any) []simulatortypes.HTTPResponse {
	out := make([]simulatortypes.HTTPResponse, 0, len(branches))
	for _, b := range branches {
		out = append(out, simulatortypes.HTTPResponse{
			Branch: b,
			Resp:   webrequest.HTTPResponse{StatusCode: 200, Body: body},
		})
	}
	return out
}

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
	m, err := unmarshalJSONMap(st.PromotionStrategyContext.TriggerOutput)
	Expect(err).ToNot(HaveOccurred())
	if m == nil {
		m = make(map[string]any)
	}
	mut(m)
	st.PromotionStrategyContext.TriggerOutput, err = marshalJSONMap(m)
	Expect(err).ToNot(HaveOccurred())
}

// decodeJSONMap unmarshals persisted *apiextensionsv1.JSON status fields for assertions.
func decodeJSONMap(j *apiextensionsv1.JSON) map[string]any {
	m, err := unmarshalJSONMap(j)
	Expect(err).ToNot(HaveOccurred())
	if m == nil {
		return make(map[string]any)
	}
	return m
}

// testWRCSKey is the CommitStatus key used by twoEnvPromotionStrategy and basicWRCS.
const testWRCSKey = "my-key"

// jsonMap marshals a map into an apiextensionsv1.JSON for status fixtures.
func jsonMap(m map[string]any) *apiextensionsv1.JSON {
	raw, err := json.Marshal(m)
	Expect(err).ToNot(HaveOccurred())
	return &apiextensionsv1.JSON{Raw: raw}
}

// twoEnvPromotionStrategy builds a minimal PromotionStrategy that gates branches
// "dev" and "prod" on testWRCSKey via per-env ProposedCommitStatuses.
func twoEnvPromotionStrategy() *promoterv1alpha1.PromotionStrategy {
	return &promoterv1alpha1.PromotionStrategy{
		ObjectMeta: metav1.ObjectMeta{Name: "ps", Namespace: "default"},
		Spec: promoterv1alpha1.PromotionStrategySpec{
			RepositoryReference: promoterv1alpha1.ObjectReference{Name: "repo"},
			Environments: []promoterv1alpha1.Environment{
				{Branch: "dev", ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{{Key: testWRCSKey}}},
				{Branch: "prod", ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{{Key: testWRCSKey}}},
			},
		},
		Status: promoterv1alpha1.PromotionStrategyStatus{
			Environments: []promoterv1alpha1.EnvironmentStatus{
				{
					Branch: "dev",
					Proposed: promoterv1alpha1.CommitBranchState{
						Hydrated: promoterv1alpha1.CommitShaState{Sha: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
					},
					Active: promoterv1alpha1.CommitBranchState{
						Hydrated: promoterv1alpha1.CommitShaState{Sha: "1111111111111111111111111111111111111111"},
					},
				},
				{
					Branch: "prod",
					Proposed: promoterv1alpha1.CommitBranchState{
						Hydrated: promoterv1alpha1.CommitShaState{Sha: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
					},
					Active: promoterv1alpha1.CommitBranchState{
						Hydrated: promoterv1alpha1.CommitShaState{Sha: "2222222222222222222222222222222222222222"},
					},
				},
			},
		},
	}
}

// basicWRCS builds a minimal WebRequestCommitStatus for the environments context
// with the supplied mode and success expression.
func basicWRCS(mode promoterv1alpha1.ModeSpec, successExpr string) *promoterv1alpha1.WebRequestCommitStatus {
	return &promoterv1alpha1.WebRequestCommitStatus{
		ObjectMeta: metav1.ObjectMeta{Name: "wrcs", Namespace: "default"},
		Spec: promoterv1alpha1.WebRequestCommitStatusSpec{
			PromotionStrategyRef: promoterv1alpha1.ObjectReference{Name: "ps"},
			Key:                  testWRCSKey,
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

		r, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: wrcs,
			PromotionStrategy:      newPS(),
			HTTPResponses:          envHTTPMocksSame([]string{"dev", "prod"}, nil),
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

	// Polling always evaluates as "should fire"; simulator requires HTTPResponses when a request runs.
	// Omitting HTTPResponses exercises the public error wrap from webrequestsimulator.Simulate.
	It("Simulate returns a wrapped error when polling would fire but HTTPResponses is empty", func() {
		wrcs := newWRCS(
			promoterv1alpha1.ModeSpec{Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}}},
			"true",
		)
		_, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: wrcs,
			PromotionStrategy:      newPS(),
			// HTTPResponses deliberately omitted to force the "required" error path.
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
		r, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: wrcs,
			PromotionStrategy:      newPS(),
			HTTPResponses:          []simulatortypes.HTTPResponse{{Resp: webrequest.HTTPResponse{StatusCode: 200}}},
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
		r, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: wrcs,
			PromotionStrategy:      newPS(),
			NamespaceMetadata:      simulatortypes.NamespaceMetadata{Labels: map[string]string{"team": "payments"}},
			HTTPResponses:          envHTTPMocksSame([]string{"dev", "prod"}, nil),
		})
		Expect(err).ToNot(HaveOccurred())
		for _, req := range r.RenderedRequests {
			Expect(req.URL).To(HavePrefix("https://example.com/payments/"))
		}
	})
})

var _ = Describe("Simulate engine", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	Describe("environments context", func() {
		It("fires for polling mode and returns success with a rendered request per env", func() {
			wrcs := basicWRCS(
				promoterv1alpha1.ModeSpec{Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}}},
				"Response.StatusCode == 200",
			)
			ps := twoEnvPromotionStrategy()

			result, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponses:          envHTTPMocksSame([]string{"dev", "prod"}, map[string]any{"ok": true}),
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
			wrcs := basicWRCS(
				promoterv1alpha1.ModeSpec{Trigger: &promoterv1alpha1.TriggerModeSpec{
					RequeueDuration: metav1.Duration{Duration: 0},
					When:            promoterv1alpha1.WhenWithOutputSpec{Expression: "false"},
				}},
				"true",
			)
			wrcs.Status.Environments = []promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus{
				{
					Branch:        "dev",
					ReportedSha:   "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
					Phase:         promoterv1alpha1.CommitPhasePending,
					TriggerOutput: jsonMap(map[string]any{"note": "prev-dev"}),
				},
				{
					Branch:        "prod",
					ReportedSha:   "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
					Phase:         promoterv1alpha1.CommitPhaseSuccess,
					TriggerOutput: jsonMap(map[string]any{"note": "prev-prod"}),
				},
			}
			ps := twoEnvPromotionStrategy()

			result, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
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
			Expect(byBranch["prod"].Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			Expect(byBranch["dev"].Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
		})

		It("round-trips: previous Status.Environments outputs surface to expressions", func() {
			wrcs := basicWRCS(
				promoterv1alpha1.ModeSpec{Trigger: &promoterv1alpha1.TriggerModeSpec{
					RequeueDuration: metav1.Duration{Duration: 0},
					When: promoterv1alpha1.WhenWithOutputSpec{
						Expression: "true",
						Output:     &promoterv1alpha1.OutputSpec{Expression: `{ counter: (TriggerOutput.counter ?? 0) + 1 }`},
					},
				}},
				"Response.StatusCode == 200",
			)
			ps := twoEnvPromotionStrategy()

			first, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponses:          envHTTPMocksSame([]string{"dev", "prod"}, nil),
			})
			Expect(err).ToNot(HaveOccurred())
			wrcs.Status = first.Status
			second, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponses:          envHTTPMocksSame([]string{"dev", "prod"}, nil),
			})
			Expect(err).ToNot(HaveOccurred())
			for _, e := range second.Status.Environments {
				Expect(e.TriggerOutput).ToNot(BeNil())
				m := map[string]any{}
				Expect(json.Unmarshal(e.TriggerOutput.Raw, &m)).To(Succeed())
				Expect(m["counter"]).To(BeNumerically("==", 2))
			}
		})

		It("surfaces an error when the trigger fires but HTTPResponses is empty", func() {
			wrcs := basicWRCS(
				promoterv1alpha1.ModeSpec{Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}}},
				"true",
			)
			ps := twoEnvPromotionStrategy()

			_, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponses:          nil,
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("HTTPResponse is required"))
		})

		It("surfaces a compile error for invalid expressions", func() {
			wrcs := basicWRCS(
				promoterv1alpha1.ModeSpec{Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}}},
				"this is not expr",
			)
			ps := twoEnvPromotionStrategy()

			_, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponses:          envHTTPMocksSame([]string{"dev", "prod"}, nil),
			})
			Expect(err).To(HaveOccurred())
		})

		It("uses per-branch HTTP mocks in environments context", func() {
			wrcs := basicWRCS(
				promoterv1alpha1.ModeSpec{Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}}},
				`Response.StatusCode == 200`,
			)
			ps := twoEnvPromotionStrategy()
			result, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponses: []simulatortypes.HTTPResponse{
					{Branch: "dev", Resp: webrequest.HTTPResponse{StatusCode: 200}},
					{Branch: "prod", Resp: webrequest.HTTPResponse{StatusCode: 201}},
				},
			})
			Expect(err).ToNot(HaveOccurred())
			byBranch := map[string]promoterv1alpha1.WebRequestCommitStatusEnvironmentStatus{}
			for _, e := range result.Status.Environments {
				byBranch[e.Branch] = e
			}
			Expect(byBranch["dev"].Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			Expect(byBranch["prod"].Phase).To(Equal(promoterv1alpha1.CommitPhasePending))
			Expect(byBranch["prod"].LastResponseStatusCode).ToNot(BeNil())
			Expect(*byBranch["prod"].LastResponseStatusCode).To(Equal(201))
		})
	})

	Describe("promotionstrategy context", func() {
		It("issues one shared request and resolves per-branch phases from an object return", func() {
			successExpr := `{ defaultPhase: "pending", environments: ` +
				`[{ branch: "dev", phase: "success" }, { branch: "prod", phase: "failure" }] }`
			wrcs := basicWRCS(
				promoterv1alpha1.ModeSpec{
					Context: promoterv1alpha1.ContextPromotionStrategy,
					Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}},
				},
				successExpr,
			)
			ps := twoEnvPromotionStrategy()

			result, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponses:          []simulatortypes.HTTPResponse{{Resp: webrequest.HTTPResponse{StatusCode: 200}}},
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

			csByBranch := map[string]*promoterv1alpha1.CommitStatus{}
			for _, cs := range result.CommitStatuses {
				csByBranch[cs.Labels[promoterv1alpha1.EnvironmentLabel]] = cs
			}
			Expect(csByBranch["dev"].Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			Expect(csByBranch["prod"].Spec.Phase).To(Equal(promoterv1alpha1.CommitPhaseFailure))
		})

		It("uses only HTTPResponses[0] in promotionstrategy context (ignores later entries)", func() {
			successExpr := `{ defaultPhase: "pending", environments: ` +
				`[{ branch: "dev", phase: "success" }, { branch: "prod", phase: "success" }] }`
			wrcs := basicWRCS(
				promoterv1alpha1.ModeSpec{
					Context: promoterv1alpha1.ContextPromotionStrategy,
					Polling: &promoterv1alpha1.PollingModeSpec{Interval: metav1.Duration{Duration: 0}},
				},
				successExpr,
			)
			ps := twoEnvPromotionStrategy()
			result, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponses: []simulatortypes.HTTPResponse{
					{Resp: webrequest.HTTPResponse{StatusCode: 200}},
					{Resp: webrequest.HTTPResponse{StatusCode: 503, Body: map[string]any{"would": "fail"}}},
				},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RenderedRequests).To(HaveLen(1))
			Expect(result.Status.PromotionStrategyContext).ToNot(BeNil())
			for _, p := range result.Status.PromotionStrategyContext.PhasePerBranch {
				Expect(p.Phase).To(Equal(promoterv1alpha1.CommitPhaseSuccess))
			}
		})

		It("surfaces previous promotionstrategy-context outputs to expressions", func() {
			wrcs := basicWRCS(
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
			ps := twoEnvPromotionStrategy()

			result, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponses:          []simulatortypes.HTTPResponse{{Resp: webrequest.HTTPResponse{StatusCode: 200}}},
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(result.RenderedRequests).To(HaveLen(1))
		})

		It("renders URL/body/headers/description with Sprig functions and latest outputs", func() {
			wrcs := basicWRCS(
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

			ps := twoEnvPromotionStrategy()

			result, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
				WebRequestCommitStatus: wrcs,
				PromotionStrategy:      ps,
				HTTPResponses:          []simulatortypes.HTTPResponse{{Resp: webrequest.HTTPResponse{StatusCode: 200}}},
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
			_, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
				PromotionStrategy: twoEnvPromotionStrategy(),
			})
			Expect(err).To(MatchError(ContainSubstring("WebRequestCommitStatus is required")))
		})
		It("rejects nil PromotionStrategy", func() {
			_, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
				WebRequestCommitStatus: basicWRCS(
					promoterv1alpha1.ModeSpec{Polling: &promoterv1alpha1.PollingModeSpec{}},
					"true",
				),
			})
			Expect(err).To(MatchError(ContainSubstring("PromotionStrategy is required")))
		})
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
	// trigger/response output maps are asserted. Second reconcile: same PS fingerprint, nil HTTPResponses —
	// trigger is false (no isNewFingerprint/needsRetry); success.when no-HTTP branch keeps success.
	It("change-management-open (YAML): happy-path POST, trigger/response output, carry-forward", func() {
		w := loadChangeManagementWRCSByName("change-management-open")
		ps := changeManagementArgoconDemoPS()

		r1, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponses: pscHTTPMocks(&simulatortypes.HTTPResponse{
				Resp: webrequest.HTTPResponse{
					StatusCode: 202,
					Body:       openOKBody(),
				},
			}),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r1.RenderedRequests).To(HaveLen(1))
		Expect(r1.RenderedRequests[0].Method).To(Equal("POST"))
		Expect(r1.RenderedRequests[0].URL).To(ContainSubstring("/v1/change-management-service/change"))
		Expect(r1.RenderedRequests[0].Body).To(ContainSubstring(changeMgmtNoteDrySha))
		Expect(r1.RenderedRequests[0].Body).To(ContainSubstring("argocon-demo"))

		expectAllBranches(promoterv1alpha1.CommitPhaseSuccess, r1.Status.PromotionStrategyContext)

		trig := decodeJSONMap(r1.Status.PromotionStrategyContext.TriggerOutput)
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

		resp := decodeJSONMap(r1.Status.PromotionStrategyContext.ResponseOutput)
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
		r2, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: w2,
			PromotionStrategy:      ps,
			HTTPResponses:          nil,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r2.RenderedRequests).To(BeEmpty())
		trig2 := decodeJSONMap(r2.Status.PromotionStrategyContext.TriggerOutput)
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
			r, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
				WebRequestCommitStatus: w,
				PromotionStrategy:      ps,
				HTTPResponses: pscHTTPMocks(&simulatortypes.HTTPResponse{
					Resp: webrequest.HTTPResponse{
						StatusCode: 202,
						Body:       openOKBody(),
					},
				}),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(r.RenderedRequests).To(BeEmpty())
			trig := decodeJSONMap(r.Status.PromotionStrategyContext.TriggerOutput)
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
		r, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponses: pscHTTPMocks(&simulatortypes.HTTPResponse{
				Resp: webrequest.HTTPResponse{
					StatusCode: 202,
					Body:       openOKBody(),
				},
			}),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r.RenderedRequests).To(HaveLen(1))
		trig := decodeJSONMap(r.Status.PromotionStrategyContext.TriggerOutput)
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

		r1, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponses: pscHTTPMocks(&simulatortypes.HTTPResponse{
				Resp: webrequest.HTTPResponse{
					StatusCode: 503,
					Body:       map[string]any{"error": "unavailable"},
				},
			}),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r1.RenderedRequests).To(HaveLen(1))
		expectAllBranches(promoterv1alpha1.CommitPhasePending, r1.Status.PromotionStrategyContext)
		t1 := decodeJSONMap(r1.Status.PromotionStrategyContext.TriggerOutput)
		Expect(t1["shouldTrigger"]).To(BeTrue())
		Expect(t1["isNewFingerprint"]).To(BeTrue())
		Expect(t1["fingerprint"]).To(Equal(changeMgmtBaselineFingerprint))

		w2 := w.DeepCopy()
		w2.Status = *r1.Status.DeepCopy()
		r2, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: w2,
			PromotionStrategy:      ps,
			HTTPResponses: pscHTTPMocks(&simulatortypes.HTTPResponse{
				Resp: webrequest.HTTPResponse{
					StatusCode: 202,
					Body:       openOKBody(),
				},
			}),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r2.RenderedRequests).To(HaveLen(1))
		t2 := decodeJSONMap(r2.Status.PromotionStrategyContext.TriggerOutput)
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
		r1, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponses: pscHTTPMocks(&simulatortypes.HTTPResponse{
				Resp: webrequest.HTTPResponse{StatusCode: 400, Body: map[string]any{"error": "bad"}},
			}),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r1.RenderedRequests).To(HaveLen(1))
		expectAllBranches(promoterv1alpha1.CommitPhasePending, r1.Status.PromotionStrategyContext)
		t1 := decodeJSONMap(r1.Status.PromotionStrategyContext.TriggerOutput)
		Expect(t1["shouldTrigger"]).To(BeTrue())
		Expect(t1["needsRetry"]).To(BeFalse())

		w2 := w.DeepCopy()
		w2.Status = *r1.Status.DeepCopy()
		r2, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: w2,
			PromotionStrategy:      ps,
			HTTPResponses:          nil,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r2.RenderedRequests).To(BeEmpty())
		t2 := decodeJSONMap(r2.Status.PromotionStrategyContext.TriggerOutput)
		Expect(t2["shouldTrigger"]).To(BeFalse())
		Expect(t2["needsRetry"]).To(BeFalse())
	})

	// Same two-step pattern as 503: 429 is explicitly retryable, then 202 completes the change.
	It("change-management-open (fixture YAML): 429 then 202 exercises isRetryable for status code 429", func() {
		w := loadChangeManagementWRCSByName("change-management-open")
		ps := changeManagementArgoconDemoPS()
		r1, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponses: pscHTTPMocks(&simulatortypes.HTTPResponse{
				Resp: webrequest.HTTPResponse{
					StatusCode: 429,
					Body:       map[string]any{"error": "rate limited"},
				},
			}),
		})
		Expect(err).ToNot(HaveOccurred())
		t1 := decodeJSONMap(r1.Status.PromotionStrategyContext.TriggerOutput)
		Expect(t1["shouldTrigger"]).To(BeTrue())

		w2 := w.DeepCopy()
		w2.Status = *r1.Status.DeepCopy()
		r2, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: w2,
			PromotionStrategy:      ps,
			HTTPResponses: pscHTTPMocks(&simulatortypes.HTTPResponse{
				Resp: webrequest.HTTPResponse{StatusCode: 202, Body: openOKBody()},
			}),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r2.RenderedRequests).To(HaveLen(1))
		expectAllBranches(promoterv1alpha1.CommitPhaseSuccess, r2.Status.PromotionStrategyContext)
	})

	// Trigger still true (happy PS); each HTTP response fails success.when’s Response != nil branch
	// (status not 202, or id missing/empty). Phases stay pending after one POST.
	DescribeTable("change-management-open (fixture YAML): success.when HTTP ternary fails unless 202 + non-empty id",
		func(resp *simulatortypes.HTTPResponse) {
			w := loadChangeManagementWRCSByName("change-management-open")
			ps := changeManagementArgoconDemoPS()
			r, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
				WebRequestCommitStatus: w,
				PromotionStrategy:      ps,
				HTTPResponses:          pscHTTPMocks(resp),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(r.RenderedRequests).To(HaveLen(1))
			expectAllBranches(promoterv1alpha1.CommitPhasePending, r.Status.PromotionStrategyContext)
		},
		Entry("HTTP branch: status 200 with id still fails (requires 202)",
			&simulatortypes.HTTPResponse{Resp: webrequest.HTTPResponse{StatusCode: 200, Body: map[string]any{"id": "x"}}},
		),
		Entry("HTTP branch: 202 with Body.id null fails id check",
			&simulatortypes.HTTPResponse{
				Resp: webrequest.HTTPResponse{
					StatusCode: 202,
					Body:       map[string]any{"id": nil, "message": "m"},
				},
			},
		),
		Entry("HTTP branch: 202 with Body.id empty string fails",
			&simulatortypes.HTTPResponse{
				Resp: webrequest.HTTPResponse{
					StatusCode: 202,
					Body:       map[string]any{"id": "", "message": "m"},
				},
			},
		),
		Entry("HTTP branch: 202 with no id key — id nil in body map, changeId empty in response.output",
			&simulatortypes.HTTPResponse{Resp: webrequest.HTTPResponse{StatusCode: 202, Body: map[string]any{"message": "m"}}},
		),
	)

	// response.output expression uses string(id) / string(message) with nil guards — assert persisted map.
	It("change-management-open (fixture YAML): response.output maps nil Body.id and nil message to empty strings", func() {
		w := loadChangeManagementWRCSByName("change-management-open")
		ps := changeManagementArgoconDemoPS()
		r, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponses: pscHTTPMocks(&simulatortypes.HTTPResponse{
				Resp: webrequest.HTTPResponse{
					StatusCode: 202,
					Body:       map[string]any{"message": nil},
				},
			}),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r.RenderedRequests).To(HaveLen(1))
		out := decodeJSONMap(r.Status.PromotionStrategyContext.ResponseOutput)
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

		r1, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponses: pscHTTPMocks(&simulatortypes.HTTPResponse{
				Resp: webrequest.HTTPResponse{
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
			}),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r1.RenderedRequests).To(HaveLen(1))
		Expect(r1.RenderedRequests[0].Method).To(Equal("GET"))
		Expect(r1.RenderedRequests[0].URL).To(ContainSubstring("changes/search"))
		Expect(r1.RenderedRequests[0].URL).To(ContainSubstring("commit_id=" + changeMgmtNoteDrySha))
		Expect(r1.RenderedRequests[0].URL).To(ContainSubstring("asset_id=1372489579564901493"))

		expectAllBranches(promoterv1alpha1.CommitPhaseSuccess, r1.Status.PromotionStrategyContext)

		trig := decodeJSONMap(r1.Status.PromotionStrategyContext.TriggerOutput)
		Expect(trig["shouldTrigger"]).To(BeTrue())
		Expect(trig["hasOpenPR"]).To(BeTrue())
		Expect(trig["allNoteDryShasMatch"]).To(BeTrue())
		Expect(trig["preGateNoOpenPR"]).To(BeTrue())
		// isFirstRun / shouldTriggerByTime live only in when.variables, not in when.output JSON.
		Expect(trig["isNewFingerprint"]).To(BeTrue())
		Expect(trig["fingerprint"]).To(Equal(changeMgmtBaselineFingerprint))

		resp := decodeJSONMap(r1.Status.PromotionStrategyContext.ResponseOutput)
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
		r2, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: w2,
			PromotionStrategy:      ps,
			HTTPResponses:          nil,
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r2.RenderedRequests).To(BeEmpty())
		trig2 := decodeJSONMap(r2.Status.PromotionStrategyContext.TriggerOutput)
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
			r, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
				WebRequestCommitStatus: w,
				PromotionStrategy:      ps,
				HTTPResponses: pscHTTPMocks(&simulatortypes.HTTPResponse{
					Resp: webrequest.HTTPResponse{
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
				}),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(r.RenderedRequests).To(BeEmpty())
			trig := decodeJSONMap(r.Status.PromotionStrategyContext.TriggerOutput)
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

		r1, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponses: pscHTTPMocks(&simulatortypes.HTTPResponse{
				Resp: webrequest.HTTPResponse{StatusCode: 200, Body: okBody},
			}),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r1.RenderedRequests).To(HaveLen(1))

		w2 := w.DeepCopy()
		w2.Status = *r1.Status.DeepCopy()
		patchPromotionStrategyTriggerOutput(&w2.Status, func(m map[string]any) {
			m["lastRequestTime"] = time.Now().UTC().Add(-125 * time.Minute).Format(time.RFC3339Nano)
		})

		r2, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: w2,
			PromotionStrategy:      ps,
			HTTPResponses: pscHTTPMocks(&simulatortypes.HTTPResponse{
				Resp: webrequest.HTTPResponse{StatusCode: 200, Body: okBody},
			}),
		})
		Expect(err).ToNot(HaveOccurred())
		Expect(r2.RenderedRequests).To(HaveLen(1))
		t2 := decodeJSONMap(r2.Status.PromotionStrategyContext.TriggerOutput)
		Expect(t2["shouldTrigger"]).To(BeTrue())
		Expect(t2["isNewFingerprint"]).To(BeFalse())
	})

	// Trigger fires (happy PS); each response fails success.when’s HTTP branch (wrong status, empty list, or dates).
	// response.output still runs; approvedCount is 0 for the filter expression.
	DescribeTable("change-management-approval (fixture YAML): success.when needs 200, records, in-window times",
		func(http *simulatortypes.HTTPResponse) {
			w := loadChangeManagementWRCSByName("change-management-approval")
			ps := changeManagementArgoconDemoPS()
			r, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
				WebRequestCommitStatus: w,
				PromotionStrategy:      ps,
				HTTPResponses:          pscHTTPMocks(http),
			})
			Expect(err).ToNot(HaveOccurred())
			Expect(r.RenderedRequests).To(HaveLen(1))
			expectAllBranches(promoterv1alpha1.CommitPhasePending, r.Status.PromotionStrategyContext)
			out := decodeJSONMap(r.Status.PromotionStrategyContext.ResponseOutput)
			Expect(out["approvedCount"]).To(BeNumerically("==", 0))
		},
		Entry("HTTP branch: non-200 status", &simulatortypes.HTTPResponse{
			Resp: webrequest.HTTPResponse{
				StatusCode: 201,
				Body:       map[string]any{"change_records": []any{}},
			},
		}),
		Entry("HTTP branch: 200 with empty change_records", &simulatortypes.HTTPResponse{
			Resp: webrequest.HTTPResponse{
				StatusCode: 200,
				Body:       map[string]any{"change_records": []any{}},
			},
		}),
		Entry("HTTP branch: 200 but no record brackets now()",
			&simulatortypes.HTTPResponse{
				Resp: webrequest.HTTPResponse{
					StatusCode: 200,
					Body: map[string]any{"change_records": []any{
						map[string]any{"change_request": map[string]any{
							"start_time": time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339),
							"end_time":   time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339),
						}},
					}},
				},
			},
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
		r, err := webrequestsimulator.Simulate(ctx, simulatortypes.Input{
			WebRequestCommitStatus: w,
			PromotionStrategy:      ps,
			HTTPResponses: pscHTTPMocks(&simulatortypes.HTTPResponse{
				Resp: webrequest.HTTPResponse{
					StatusCode: 200,
					Body: map[string]any{
						"change_records": []any{
							map[string]any{"change_request": map[string]any{"start_time": inStart, "end_time": inEnd}},
							map[string]any{"change_request": map[string]any{"start_time": outStart, "end_time": outEnd}},
						},
					},
				},
			}),
		})
		Expect(err).ToNot(HaveOccurred())
		out := decodeJSONMap(r.Status.PromotionStrategyContext.ResponseOutput)
		Expect(out["totalRecordCount"]).To(BeNumerically("==", 2))
		Expect(out["approvedCount"]).To(BeNumerically("==", 1))
	})
})
