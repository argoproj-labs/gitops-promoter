package scms_test

import (
	"context"
	"testing"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/azuredevops"
	bitbucketcloud "github.com/argoproj-labs/gitops-promoter/internal/scms/bitbucket_cloud"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/forgejo"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitea"
	"github.com/argoproj-labs/gitops-promoter/internal/scms/gitlab"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestScms(t *testing.T) {
	t.Parallel()

	RegisterFailHandler(Fail)
	c, _ := GinkgoConfiguration()
	RunSpecs(t, "Scms Suite", c)
}

// reviewerProvider is the subset of scms.PullRequestProvider under test here.
type reviewerProvider interface {
	AddReviewers(ctx context.Context, pullRequest v1alpha1.PullRequest, reviewers []v1alpha1.PullRequestReviewer) error
	RemoveReviewers(ctx context.Context, pullRequest v1alpha1.PullRequest, reviewers []v1alpha1.PullRequestReviewer) error
}

var _ = Describe("Reviewers on providers without reviewer support", func() {
	DescribeTable("errors only when reviewers are configured",
		func(provider reviewerProvider) {
			ctx := context.Background()
			reviewers := []v1alpha1.PullRequestReviewer{{User: "alice"}}

			Expect(provider.AddReviewers(ctx, v1alpha1.PullRequest{}, nil)).To(Succeed())
			Expect(provider.RemoveReviewers(ctx, v1alpha1.PullRequest{}, nil)).To(Succeed())
			Expect(provider.AddReviewers(ctx, v1alpha1.PullRequest{}, reviewers)).To(MatchError(ContainSubstring("not yet supported")))
			Expect(provider.RemoveReviewers(ctx, v1alpha1.PullRequest{}, reviewers)).To(MatchError(ContainSubstring("not yet supported")))
		},
		Entry("gitlab", &gitlab.PullRequest{}),
		Entry("gitea", &gitea.PullRequest{}),
		Entry("forgejo", &forgejo.PullRequest{}),
		Entry("azuredevops", &azuredevops.PullRequest{}),
		Entry("bitbucket cloud", &bitbucketcloud.PullRequest{}),
	)
})
