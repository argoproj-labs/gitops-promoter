package scmserver_test

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/test/scmserver"
)

const (
	eventuallyTimeout = 15 * time.Second
	pollInterval      = 100 * time.Millisecond
)

// nativeSuccess is the state each provider stores for a successful promoter commit status.
var nativeSuccess = map[scmserver.Provider]string{
	scmserver.GitHub:              "completed",
	scmserver.GitLab:              "success",
	scmserver.AzureDevOps:         "succeeded",
	scmserver.BitbucketCloud:      "SUCCESSFUL",
	scmserver.Gitea:               "success",
	scmserver.Forgejo:             "success",
	scmserver.BitbucketDataCenter: "SUCCESSFUL",
}

func (f *fixture) commitStatus(name string, phase v1alpha1.CommitStatusPhase) *v1alpha1.CommitStatus {
	return &v1alpha1.CommitStatus{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec: v1alpha1.CommitStatusSpec{
			RepositoryReference: v1alpha1.ObjectReference{Name: f.gitRepo.Name},
			Sha:                 f.headSHA,
			Name:                name,
			Description:         "checked by " + name,
			Phase:               phase,
			Url:                 "https://ci.example.com/" + name,
		},
	}
}

// createPR opens the fixture's pull request through the provider and waits until FindOpen reports it.
func (f *fixture) createPR(ctx context.Context, name string) v1alpha1.PullRequest {
	GinkgoHelper()
	prObj := f.pullRequest(name)
	id, err := f.prs.Create(ctx, prObj.Spec.Title, prObj.Spec.SourceBranch, prObj.Spec.TargetBranch, prObj.Spec.Description, prObj)
	Expect(err).NotTo(HaveOccurred())
	Expect(id).NotTo(BeEmpty())
	prObj.Status.ID = id
	Eventually(func(g Gomega) {
		res, err := f.prs.FindOpen(ctx, prObj)
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(res.Found).To(BeTrue())
		g.Expect(res.ID).To(Equal(id))
		g.Expect(res.CreationTime).NotTo(BeZero())
	}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())
	return prObj
}

var allProviderEntries = []TableEntry{
	Entry("GitHub", scmserver.GitHub),
	Entry("GitLab", scmserver.GitLab),
	Entry("Azure DevOps", scmserver.AzureDevOps),
	Entry("Bitbucket Cloud", scmserver.BitbucketCloud),
	Entry("Gitea", scmserver.Gitea),
	Entry("Forgejo", scmserver.Forgejo),
	Entry("Bitbucket Data Center", scmserver.BitbucketDataCenter),
}

var _ = Describe("provider contract", func() {
	DescribeTable("pull request lifecycle through the real provider implementation",
		func(ctx SpecContext, p scmserver.Provider) {
			f := newFixture(ctx, p)

			By("creating the pull request and finding it as the open pull request for the branch pair")
			prObj := f.createPR(ctx, "lifecycle")
			info := f.serverPR(ctx, prObj.Status.ID)
			Expect(info.State).To(Equal("open"))
			Expect(info.Source).To(Equal(sourceBranch))
			Expect(info.Target).To(Equal(targetBranch))
			Expect(info.HeadSHA).To(Equal(f.headSHA))

			By("updating the title and description")
			Expect(f.prs.Update(ctx, "Promote development (updated)", "Updated description.", prObj)).To(Succeed())
			info = f.serverPR(ctx, prObj.Status.ID)
			Expect(info.Title).To(Equal("Promote development (updated)"))
			Expect(info.Body).To(Equal("Updated description."))

			By("building a pull request URL on the server's host")
			url, err := f.prs.GetUrl(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(url).To(HavePrefix("https://" + srv.Domain(p) + "/"))

			By("reporting a commit status on the head commit")
			status := f.commitStatus("promoter-previous-environment", v1alpha1.CommitPhasePending)
			_, err = f.statuses.Set(ctx, status)
			Expect(err).NotTo(HaveOccurred())
			status.Spec.Phase = v1alpha1.CommitPhaseSuccess
			_, err = f.statuses.Set(ctx, status)
			Expect(err).NotTo(HaveOccurred())
			statuses, err := admin.CommitStatuses(ctx, f.repo.ID, f.headSHA)
			Expect(err).NotTo(HaveOccurred())
			Expect(statuses).NotTo(BeEmpty())
			latest := statuses[len(statuses)-1]
			Expect(latest.Context).To(Equal("promoter-previous-environment"))
			Expect(latest.State).To(Equal(nativeSuccess[p]))

			By("merging with the expected head SHA, retrying while the provider computes mergeability as the controller does")
			var result scms.MergeResult
			Eventually(func(g Gomega) {
				var err error
				result, err = f.prs.Merge(ctx, prObj)
				g.Expect(err).NotTo(HaveOccurred())
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())

			By("observing the merge through Get, with the SHA that landed on the target branch")
			Eventually(func(g Gomega) {
				got, err := f.prs.Get(ctx, prObj)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(got.Found).To(BeTrue())
				g.Expect(got.State).To(Equal(v1alpha1.PullRequestMerged))
				g.Expect(got.MergedTargetSHA).To(Equal(f.branchSHA(ctx, targetBranch)))
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())
			landed := f.branchSHA(ctx, targetBranch)
			Expect(landed).NotTo(Equal(f.baseSHA))
			if result.CommitSHA != "" {
				Expect(result.CommitSHA).To(Equal(landed))
			}
			info = f.serverPR(ctx, prObj.Status.ID)
			Expect(info.State).To(Equal("merged"))
			Expect(info.MergeCommitSHA).To(Equal(landed))

			By("no longer finding an open pull request for the branch pair")
			res, err := f.prs.FindOpen(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Found).To(BeFalse())
		},
		allProviderEntries,
	)

	DescribeTable("closing a pull request",
		func(ctx SpecContext, p scmserver.Provider) {
			f := newFixture(ctx, p)
			prObj := f.createPR(ctx, "close")

			Expect(f.prs.Close(ctx, prObj)).To(Succeed())

			got, err := f.prs.Get(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Found).To(BeTrue())
			Expect(got.State).To(Equal(v1alpha1.PullRequestClosed))
			res, err := f.prs.FindOpen(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(res.Found).To(BeFalse())
			Expect(f.branchSHA(ctx, targetBranch)).To(Equal(f.baseSHA))
		},
		allProviderEntries,
	)

	DescribeTable("reporting a pull request that does not exist as not found",
		func(ctx SpecContext, p scmserver.Provider) {
			f := newFixture(ctx, p)
			prObj := f.pullRequest("missing")
			prObj.Status.ID = "9999"

			got, err := f.prs.Get(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.Found).To(BeFalse())
		},
		allProviderEntries,
	)

	DescribeTable("pull request labels",
		func(ctx SpecContext, p scmserver.Provider) {
			f := newFixture(ctx, p)
			prObj := f.createPR(ctx, "labels")

			Expect(f.prs.AddLabels(ctx, prObj, []string{"promoter", "env-development"})).To(Succeed())
			Expect(f.serverPR(ctx, prObj.Status.ID).Labels).To(ConsistOf("promoter", "env-development"))

			res, err := f.prs.FindOpen(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			if res.LabelsReported {
				Expect(res.SCMLabels).To(ConsistOf("promoter", "env-development"))
			}

			Expect(f.prs.RemoveLabels(ctx, prObj, []string{"promoter"})).To(Succeed())
			Expect(f.serverPR(ctx, prObj.Status.ID).Labels).To(ConsistOf("env-development"))

			By("ignoring the removal of a label the pull request does not have")
			Expect(f.prs.RemoveLabels(ctx, prObj, []string{"never-added"})).To(Succeed())
		},
		Entry("GitHub", scmserver.GitHub),
		Entry("GitLab", scmserver.GitLab),
		Entry("Azure DevOps", scmserver.AzureDevOps),
		Entry("Gitea", scmserver.Gitea),
		Entry("Forgejo", scmserver.Forgejo),
	)

	DescribeTable("rejecting labels on providers without pull request labels",
		func(ctx SpecContext, p scmserver.Provider) {
			f := newFixture(ctx, p)
			prObj := f.createPR(ctx, "labels")
			Expect(f.prs.AddLabels(ctx, prObj, []string{"promoter"})).To(MatchError(ContainSubstring("does not support")))
		},
		Entry("Bitbucket Cloud", scmserver.BitbucketCloud),
		Entry("Bitbucket Data Center", scmserver.BitbucketDataCenter),
	)

	DescribeTable("git over smart HTTP with the provider's credentials",
		func(ctx SpecContext, p scmserver.Provider) {
			f := newFixture(ctx, p)
			prObj := f.createPR(ctx, "git")
			dir := filepath.Join(GinkgoT().TempDir(), "clone")

			By("cloning with the URL, user, token and HTTP header the promoter uses")
			out, err := f.git(ctx, "", "clone", "--quiet", f.authenticatedCloneURL(ctx), dir)
			Expect(err).NotTo(HaveOccurred(), out)
			out, err = git(ctx, dir, "rev-parse", "origin/"+sourceBranch)
			Expect(err).NotTo(HaveOccurred(), out)
			Expect(out).To(Equal(f.headSHA))

			By("pushing a new commit to the pull request's source branch")
			out, err = git(ctx, dir, "checkout", "--quiet", sourceBranch)
			Expect(err).NotTo(HaveOccurred(), out)
			out, err = git(ctx, dir, "commit", "--quiet", "--allow-empty", "-m", "re-hydrate")
			Expect(err).NotTo(HaveOccurred(), out)
			out, err = f.git(ctx, dir, "push", "--quiet", "origin", sourceBranch)
			Expect(err).NotTo(HaveOccurred(), out)
			pushed, err := git(ctx, dir, "rev-parse", "HEAD")
			Expect(err).NotTo(HaveOccurred())
			Expect(f.branchSHA(ctx, sourceBranch)).To(Equal(pushed))

			By("refreshing the open pull request's head")
			Eventually(func(g Gomega) {
				g.Expect(f.serverPR(ctx, prObj.Status.ID).HeadSHA).To(Equal(pushed))
			}).WithContext(ctx).WithTimeout(eventuallyTimeout).WithPolling(pollInterval).Should(Succeed())

			By("rejecting the stale merge SHA")
			_, err = f.prs.Merge(ctx, prObj)
			if p == scmserver.BitbucketCloud {
				// Bitbucket Cloud has no SHA guard.
				return
			}
			Expect(err).To(HaveOccurred())
			Expect(f.serverPR(ctx, prObj.Status.ID).State).To(Equal("open"))
		},
		allProviderEntries,
	)
})
