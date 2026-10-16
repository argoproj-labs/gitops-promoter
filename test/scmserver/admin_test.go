package scmserver_test

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"

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
