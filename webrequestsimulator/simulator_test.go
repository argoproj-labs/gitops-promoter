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
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/webrequestsimulator"
)

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

	// newWRCS builds a minimal WebRequestCommitStatus with polling mode and the
	// supplied success expression.
	newWRCS := func(
		key string,
		mode promoterv1alpha1.ModeSpec,
		successExpr string,
	) *promoterv1alpha1.WebRequestCommitStatus {
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

	It("returns Status that matches what the controller would write (environments context)", func() {
		wrcs := newWRCS("k",
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

	It("propagates errors from the internal simulator", func() {
		wrcs := newWRCS("k",
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

	It("produces a single shared request with Branch=\"\" in promotionstrategy context", func() {
		wrcs := newWRCS("k",
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

	It("passes NamespaceMetadata through to templates", func() {
		wrcs := newWRCS("k",
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
