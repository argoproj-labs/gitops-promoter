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

package utils_test

import (
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GetApplicableEnvironments", func() {
	var ps *promoterv1alpha1.PromotionStrategy

	BeforeEach(func() {
		ps = &promoterv1alpha1.PromotionStrategy{
			Spec: promoterv1alpha1.PromotionStrategySpec{
				Environments: []promoterv1alpha1.Environment{
					{Branch: "environment/dev"},
					{Branch: "environment/staging"},
					{
						Branch: "environment/prod",
						ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{
							{Key: "prod-only-gate"},
						},
						ActiveCommitStatuses: []promoterv1alpha1.CommitStatusSelector{
							{Key: "prod-active-gate"},
						},
					},
				},
			},
		}
	})

	type testCase struct {
		key          string
		reportOn     string
		globalSpec   func(*promoterv1alpha1.PromotionStrategySpec)
		wantBranches []string
	}

	DescribeTable("returns the correct environments",
		func(tc testCase) {
			if tc.globalSpec != nil {
				tc.globalSpec(&ps.Spec)
			}
			envs := utils.GetApplicableEnvironments(ps, tc.key, tc.reportOn)
			branches := make([]string, len(envs))
			for i, e := range envs {
				branches[i] = e.Branch
			}
			Expect(branches).To(Equal(tc.wantBranches))
		},
		Entry("global proposed key matches all environments",
			testCase{
				key:      "global-gate",
				reportOn: constants.CommitRefProposed,
				globalSpec: func(spec *promoterv1alpha1.PromotionStrategySpec) {
					spec.ProposedCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
						{Key: "global-gate"},
					}
				},
				wantBranches: []string{"environment/dev", "environment/staging", "environment/prod"},
			},
		),
		Entry("global active key matches all environments",
			testCase{
				key:      "global-active-gate",
				reportOn: constants.CommitRefActive,
				globalSpec: func(spec *promoterv1alpha1.PromotionStrategySpec) {
					spec.ActiveCommitStatuses = []promoterv1alpha1.CommitStatusSelector{
						{Key: "global-active-gate"},
					}
				},
				wantBranches: []string{"environment/dev", "environment/staging", "environment/prod"},
			},
		),
		Entry("per-env proposed key matches only that environment",
			testCase{
				key:          "prod-only-gate",
				reportOn:     constants.CommitRefProposed,
				wantBranches: []string{"environment/prod"},
			},
		),
		Entry("per-env active key matches only that environment",
			testCase{
				key:          "prod-active-gate",
				reportOn:     constants.CommitRefActive,
				wantBranches: []string{"environment/prod"},
			},
		),
		Entry("proposed key does not match active selectors",
			testCase{
				key:          "prod-active-gate",
				reportOn:     constants.CommitRefProposed,
				wantBranches: []string{},
			},
		),
		Entry("active key does not match proposed selectors",
			testCase{
				key:          "prod-only-gate",
				reportOn:     constants.CommitRefActive,
				wantBranches: []string{},
			},
		),
		Entry("unknown key returns no environments",
			testCase{
				key:          "unknown-gate",
				reportOn:     constants.CommitRefProposed,
				wantBranches: []string{},
			},
		),
		Entry("empty reportOn defaults to proposed selectors",
			testCase{
				key:          "prod-only-gate",
				reportOn:     "",
				wantBranches: []string{"environment/prod"},
			},
		),
	)
})
