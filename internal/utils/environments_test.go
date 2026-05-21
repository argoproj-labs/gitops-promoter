package utils_test

import (
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GetApplicableEnvironments", func() {
	buildPromotionStrategy := func() *promoterv1alpha1.PromotionStrategy {
		return &promoterv1alpha1.PromotionStrategy{
			Spec: promoterv1alpha1.PromotionStrategySpec{
				ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{
					{Key: "global-proposed"},
				},
				ActiveCommitStatuses: []promoterv1alpha1.CommitStatusSelector{
					{Key: "global-active"},
				},
				Environments: []promoterv1alpha1.Environment{
					{
						Branch: "dev",
						ProposedCommitStatuses: []promoterv1alpha1.CommitStatusSelector{
							{Key: "dev-proposed"},
						},
						ActiveCommitStatuses: []promoterv1alpha1.CommitStatusSelector{
							{Key: "dev-active"},
						},
					},
					{
						Branch: "stage",
					},
					{
						Branch: "prod",
					},
				},
			},
		}
	}

	tests := map[string]struct {
		key          string
		reportOn     string
		expectedEnvs []string
	}{
		"matches all environments for a globally configured proposed key": {
			key:          "global-proposed",
			reportOn:     constants.CommitRefProposed,
			expectedEnvs: []string{"dev", "stage", "prod"},
		},
		"matches only one environment for an environment-scoped proposed key": {
			key:          "dev-proposed",
			reportOn:     constants.CommitRefProposed,
			expectedEnvs: []string{"dev"},
		},
		"defaults to proposed selectors when reportOn is empty": {
			key:          "dev-proposed",
			expectedEnvs: []string{"dev"},
		},
		"matches all environments for a globally configured active key": {
			key:          "global-active",
			reportOn:     constants.CommitRefActive,
			expectedEnvs: []string{"dev", "stage", "prod"},
		},
		"matches only one environment for an environment-scoped active key": {
			key:          "dev-active",
			reportOn:     constants.CommitRefActive,
			expectedEnvs: []string{"dev"},
		},
		"active reportOn ignores proposed selectors": {
			key:          "dev-proposed",
			reportOn:     constants.CommitRefActive,
			expectedEnvs: []string{},
		},
	}

	for name, test := range tests {
		It(name, func() {
			ps := buildPromotionStrategy()

			applicable := utils.GetApplicableEnvironments(ps, test.key, test.reportOn)

			branches := make([]string, 0, len(applicable))
			for _, env := range applicable {
				branches = append(branches, env.Branch)
			}
			Expect(branches).To(Equal(test.expectedEnvs))
		})
	}
})
