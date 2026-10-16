package scmserver_test

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/test/scmserver"
)

var _ = Describe("admin API", func() {
	It("reports how to reach every provider and serves the CA", func(ctx SpecContext) {
		info, err := admin.Info(ctx)
		Expect(err).NotTo(HaveOccurred())
		for _, p := range scmserver.AllProviders {
			Expect(info.Providers).To(HaveKeyWithValue(p, HaveField("Domain", srv.Domain(p))))
		}
		ca, err := admin.CAPEM(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(ca).To(Equal(srv.CAPEM()))
	})

	It("seeds repositories with commits, git notes and branches", func(ctx SpecContext) {
		repo, err := admin.CreateRepo(ctx, scmserver.CreateRepoRequest{Provider: scmserver.Gitea, Owner: "admin-org", Name: "seeded"})
		Expect(err).NotTo(HaveOccurred())
		Expect(repo.Branches).To(BeEmpty())

		first, err := admin.Commit(ctx, repo.ID, scmserver.CommitRequest{
			Branch: "main", Files: map[string]string{"hydrator.metadata": `{"drySha": "n/a"}`},
			Notes: map[string]string{"hydrator.metadata": `{"drySha": "n/a"}`},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(first.Before).To(Equal("0000000000000000000000000000000000000000"))

		orphan, err := admin.Commit(ctx, repo.ID, scmserver.CommitRequest{Branch: "environment/development-next", Orphan: true})
		Expect(err).NotTo(HaveOccurred())
		_, err = admin.SetBranch(ctx, repo.ID, "environment/development", scmserver.BranchRequest{SHA: orphan.SHA})
		Expect(err).NotTo(HaveOccurred())

		By("refusing to rewind a branch without force")
		_, err = admin.SetBranch(ctx, repo.ID, "environment/development", scmserver.BranchRequest{SHA: first.SHA})
		Expect(err).To(MatchError(ContainSubstring("does not descend")))
		_, err = admin.SetBranch(ctx, repo.ID, "environment/development", scmserver.BranchRequest{SHA: first.SHA, Force: true})
		Expect(err).NotTo(HaveOccurred())

		info, err := admin.Repo(ctx, repo.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Branches).To(Equal(map[string]string{
			"main": first.SHA, "environment/development-next": orphan.SHA, "environment/development": first.SHA,
		}))

		By("exposing the note to git clients")
		dir := filepath.Join(GinkgoT().TempDir(), "clone")
		cloneURL := fmt.Sprintf("https://oauth2:%s@%s/admin-org/seeded.git", testToken, srv.Domain(scmserver.Gitea))
		out, err := git(ctx, "", "clone", "--quiet", cloneURL, dir)
		Expect(err).NotTo(HaveOccurred(), out)
		out, err = git(ctx, dir, "fetch", "--quiet", "origin", "refs/notes/*:refs/notes/*")
		Expect(err).NotTo(HaveOccurred(), out)
		out, err = git(ctx, dir, "notes", "--ref", "hydrator.metadata", "show", first.SHA)
		Expect(err).NotTo(HaveOccurred(), out)
		Expect(out).To(Equal(`{"drySha": "n/a"}`))
	})

	DescribeTable("rejects repository names that are not safe path segments",
		func(ctx SpecContext, req scmserver.CreateRepoRequest) {
			_, err := admin.CreateRepo(ctx, req)
			Expect(err).To(MatchError(ContainSubstring("400 Bad Request")))
		},
		Entry("a traversing owner", scmserver.CreateRepoRequest{Provider: scmserver.GitHub, Owner: "../escape", Name: "repo"}),
		Entry("a traversing name", scmserver.CreateRepoRequest{Provider: scmserver.Gitea, Owner: "acme", Name: ".."}),
		Entry("a nested name", scmserver.CreateRepoRequest{Provider: scmserver.GitHub, Owner: "acme", Name: "a/b"}),
		Entry("a nested owner outside GitLab", scmserver.CreateRepoRequest{Provider: scmserver.Gitea, Owner: "acme/team", Name: "repo"}),
		Entry("a traversing Azure DevOps project", scmserver.CreateRepoRequest{Provider: scmserver.AzureDevOps, Owner: "org", Project: "../p", Name: "repo"}),
	)

	It("accepts GitLab subgroups", func(ctx SpecContext) {
		repo, err := admin.CreateRepo(ctx, scmserver.CreateRepoRequest{Provider: scmserver.GitLab, Owner: "group/subgroup", Name: "deploy"})
		Expect(err).NotTo(HaveOccurred())
		Expect(repo.FullName).To(Equal("group/subgroup/deploy"))
	})

	It("changes pull requests out of band", func(ctx SpecContext) {
		f := newFixture(ctx, scmserver.GitHub)
		prObj := f.createPR(ctx, "out-of-band")
		number, err := strconv.Atoi(prObj.Status.ID)
		Expect(err).NotTo(HaveOccurred())

		info, err := admin.PullRequestAction(ctx, f.repo.ID, number, scmserver.PullRequestAction{Action: "set-labels", Labels: []string{"a", "b"}})
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Labels).To(Equal([]string{"a", "b"}))

		_, err = admin.PullRequestAction(ctx, f.repo.ID, number, scmserver.PullRequestAction{Action: "hide"})
		Expect(err).NotTo(HaveOccurred())
		res, err := f.prs.FindOpen(ctx, prObj)
		Expect(err).NotTo(HaveOccurred())
		Expect(res.Found).To(BeFalse())
		_, err = admin.PullRequestAction(ctx, f.repo.ID, number, scmserver.PullRequestAction{Action: "show"})
		Expect(err).NotTo(HaveOccurred())

		By("merging as a person would in the provider's UI")
		info, err = admin.PullRequestAction(ctx, f.repo.ID, number, scmserver.PullRequestAction{Action: "merge"})
		Expect(err).NotTo(HaveOccurred())
		Expect(info.State).To(Equal("merged"))
		got, err := f.prs.Get(ctx, prObj)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.State).To(Equal(v1alpha1.PullRequestMerged))
		Expect(got.MergedTargetSHA).To(Equal(info.MergeCommitSHA))

		By("deleting it so the provider reports it as not found")
		_, err = admin.PullRequestAction(ctx, f.repo.ID, number, scmserver.PullRequestAction{Action: "delete"})
		Expect(err).NotTo(HaveOccurred())
		got, err = f.prs.Get(ctx, prObj)
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Found).To(BeFalse())

		prs, err := admin.PullRequests(ctx, f.repo.ID, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(prs).To(BeEmpty())
	})

	It("lands one merge commit when the same pull request is merged twice at once", func(ctx SpecContext) {
		f := newFixture(ctx, scmserver.GitHub)
		prObj := f.createPR(ctx, "merged-twice")
		number, err := strconv.Atoi(prObj.Status.ID)
		Expect(err).NotTo(HaveOccurred())

		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range errs {
			wg.Add(1)
			go func() {
				defer GinkgoRecover()
				defer wg.Done()
				_, errs[i] = admin.PullRequestAction(ctx, f.repo.ID, number, scmserver.PullRequestAction{Action: "merge"})
			}()
		}
		wg.Wait()
		Expect(errs).To(ContainElement(BeNil()))
		Expect(errs).To(ContainElement(MatchError(ContainSubstring("not open"))))

		info := f.serverPR(ctx, prObj.Status.ID)
		Expect(f.branchSHA(ctx, targetBranch)).To(Equal(info.MergeCommitSHA))
		dir := filepath.Join(GinkgoT().TempDir(), "clone")
		out, err := git(ctx, "", "clone", "--quiet", f.authenticatedCloneURL(ctx), dir)
		Expect(err).NotTo(HaveOccurred(), out)
		out, err = git(ctx, dir, "rev-list", "--parents", "-n", "1", info.MergeCommitSHA)
		Expect(err).NotTo(HaveOccurred(), out)
		Expect(strings.Fields(out)[1:]).To(Equal([]string{f.baseSHA, f.headSHA}))
	})

	DescribeTable("merges pull requests out of band with a merge or a squash commit",
		func(ctx SpecContext, p scmserver.Provider, squash bool) {
			f := newFixture(ctx, p)
			prObj := f.createPR(ctx, "manual-merge")
			number, err := strconv.Atoi(prObj.Status.ID)
			Expect(err).NotTo(HaveOccurred())

			info, err := admin.PullRequestAction(ctx, f.repo.ID, number, scmserver.PullRequestAction{Action: "merge", Squash: squash})
			Expect(err).NotTo(HaveOccurred())
			Expect(info.State).To(Equal("merged"))
			Expect(info.Squashed).To(Equal(squash))
			Expect(f.branchSHA(ctx, targetBranch)).To(Equal(info.MergeCommitSHA))

			By("landing a commit with the pull request's tree and the expected parents")
			dir := filepath.Join(GinkgoT().TempDir(), "clone")
			out, err := git(ctx, "", "clone", "--quiet", f.authenticatedCloneURL(ctx), dir)
			Expect(err).NotTo(HaveOccurred(), out)
			out, err = git(ctx, dir, "rev-list", "--parents", "-n", "1", info.MergeCommitSHA)
			Expect(err).NotTo(HaveOccurred(), out)
			parents := strings.Fields(out)[1:]
			if squash {
				Expect(parents).To(Equal([]string{f.baseSHA}))
			} else {
				Expect(parents).To(Equal([]string{f.baseSHA, f.headSHA}))
			}
			mergedTree, err := git(ctx, dir, "rev-parse", info.MergeCommitSHA+"^{tree}")
			Expect(err).NotTo(HaveOccurred(), mergedTree)
			headTree, err := git(ctx, dir, "rev-parse", f.headSHA+"^{tree}")
			Expect(err).NotTo(HaveOccurred(), headTree)
			Expect(mergedTree).To(Equal(headTree))

			By("reporting the landed commit through the provider's API")
			got, err := f.prs.Get(ctx, prObj)
			Expect(err).NotTo(HaveOccurred())
			Expect(got.State).To(Equal(v1alpha1.PullRequestMerged))
			Expect(got.MergedTargetSHA).To(Equal(info.MergeCommitSHA))
		},
		func() []TableEntry {
			entries := make([]TableEntry, 0, 2*len(scmserver.AllProviders))
			for _, p := range scmserver.AllProviders {
				entries = append(entries,
					Entry(string(p)+" merge commit", p, false),
					Entry(string(p)+" squash commit", p, true))
			}
			return entries
		}(),
	)

	It("manages fault rules and records their hits", func(ctx SpecContext) {
		f := newFixture(ctx, scmserver.GitLab)
		rule, err := admin.AddFault(ctx, scmserver.FaultRule{
			Provider: scmserver.GitLab, Method: http.MethodGet, Path: "/merge_requests$", Status: http.StatusNotFound, Times: 1,
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(ctx SpecContext) { Expect(admin.ClearFaults(ctx)).To(Succeed()) })

		_, err = f.prs.FindOpen(ctx, f.pullRequest("fault"))
		Expect(err).To(HaveOccurred())
		rules, err := admin.Faults(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(rules).NotTo(ContainElement(HaveField("ID", rule.ID)), "a rule with Times expires after its last hit")

		_, err = admin.AddFault(ctx, scmserver.FaultRule{Provider: scmserver.GitLab, Path: "(", Status: http.StatusTeapot})
		Expect(err).To(MatchError(ContainSubstring("invalid path regexp")))
	})
})
