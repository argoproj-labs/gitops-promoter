package git_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sosedoff/gitkit"

	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/validations"
)

// These specs run the tag and validation helpers against a plain smart-HTTP git server: no partial
// clone filters, and no commits by SHA unless a ref points at them. It is the server the controller
// tests use, and self-hosted servers can be configured the same way.
var _ = Describe("A git server that does not serve unadvertised commits", func() {
	const tagProd = "environment/prod-tag"

	var (
		ctx     context.Context
		dry     *dryRepo
		gap     *fakeGitProvider
		first   string
		second  string
		third   string
		hotfix1 string
		hotfix2 string
	)

	BeforeEach(func() {
		ctx = context.Background()

		storage, err := os.MkdirTemp("", "git-http-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(storage)).To(Succeed()) })
		service := gitkit.New(gitkit.Config{Dir: storage, AutoCreate: true})
		Expect(service.Setup()).To(Succeed())
		server := httptest.NewServer(service)
		DeferCleanup(server.Close)
		url := server.URL + "/owner/dry"

		// Build the history locally, then publish it to the HTTP server.
		dry = newDryRepo()
		first = dry.head()
		second = dry.commit("second")
		third = dry.commit("third")
		mustGit(dry.workDir, "checkout", "-b", "hotfix", first)
		hotfix1 = dry.commit("hotfix one")
		hotfix2 = dry.commit("hotfix two")
		mustGit(dry.workDir, "checkout", dry.branch)
		mustGit(dry.workDir, "remote", "set-url", "origin", url)
		mustGit(dry.workDir, "push", "origin", dry.branch, "hotfix")

		gap = &fakeGitProvider{tempDirPath: url}
	})

	newOps := func(identity string) *git.EnvironmentOperations {
		GinkgoHelper()
		ops := git.NewEnvironmentOperations(dry.repo, gap, identity)
		Expect(ops.CloneRepo(ctx)).To(Succeed())
		DeferCleanup(func() { _ = ops.RemoveClone() })
		return ops
	}

	It("creates and moves tags with a lease, and refuses a stale one", func() {
		ops := newOps("default/gate")
		refs, err := ops.SyncDryRefs(ctx, dry.branch, []string{tagProd})
		Expect(err).NotTo(HaveOccurred())
		Expect(refs.Head).To(Equal(third))
		Expect(refs.Tags[tagProd]).To(Equal(git.TagState{}))

		Expect(ops.MoveTag(ctx, tagProd, first, "")).To(Succeed())
		Expect(ops.MoveTag(ctx, tagProd, second, first)).To(Succeed())

		By("A person moving the tag behind the controller's back")
		dry.pushTag(tagProd, third)
		err = ops.MoveTag(ctx, tagProd, first, second)
		var pushErr *git.TagPushError
		Expect(errors.As(err, &pushErr)).To(BeTrue(), "got %v", err)

		state, err := ops.ReadTag(ctx, tagProd)
		Expect(err).NotTo(HaveOccurred())
		Expect(state.Commit).To(Equal(third))
	})

	It("fetches a commit by SHA only when a ref points at it", func() {
		ops := newOps("default/gate")
		_, err := ops.SyncDryRefs(ctx, dry.branch, nil)
		Expect(err).NotTo(HaveOccurred())

		Expect(ops.EnsureCommit(ctx, second)).To(BeTrue(), "on the dry branch, so already in the clone")
		Expect(ops.EnsureCommit(ctx, hotfix1)).To(BeFalse(), "not a branch tip, and this server will not serve it")
		Expect(ops.EnsureCommit(ctx, hotfix2)).To(BeTrue(), "the tip of the hotfix branch")
		Expect(ops.EnsureCommit(ctx, hotfix1)).To(BeTrue(), "now in the clone as part of the tip's history")
		Expect(ops.EnsureCommit(ctx, strings.Repeat("ab", 20))).To(BeFalse())
	})

	It("answers whether a commit exists by looking on the fallback branch", func() {
		Expect(git.RemoteCommitExists(ctx, gap, dry.repo, third, dry.branch)).To(BeTrue(), "the branch tip")
		Expect(git.RemoteCommitExists(ctx, gap, dry.repo, second, dry.branch)).To(BeTrue(), "found on the fallback branch")
		Expect(git.RemoteCommitExists(ctx, gap, dry.repo, hotfix2, dry.branch)).To(BeTrue(), "the hotfix branch tip")
		Expect(git.RemoteCommitExists(ctx, gap, dry.repo, strings.Repeat("ab", 20), dry.branch)).To(BeFalse())

		By("Not finding a commit that is neither a tip nor on the fallback branch")
		Expect(git.RemoteCommitExists(ctx, gap, dry.repo, hotfix1, dry.branch)).To(BeFalse())

		By("Failing, rather than guessing, when there is no branch to look on")
		_, err := git.RemoteCommitExists(ctx, gap, dry.repo, second, "")
		Expect(err).To(MatchError(ContainSubstring("does not serve commit")))
	})

	It("records and lists validations", func() {
		ops := newOps("default/gate")
		cursor, err := ops.AppendValidations(ctx, []git.ValidationEntry{
			{Sha: second, Line: validations.Line{Target: tagProd, Source: validations.SourceAutomatic}},
		})
		Expect(err).NotTo(HaveOccurred())

		By("A person validating by hand")
		mustGit(dry.workDir, "fetch", "origin", "+"+git.PromoterValidationsNotesRef+":"+git.PromoterValidationsNotesRef)
		mustGit(dry.workDir, "notes", "--ref="+validationsNotesName, "append", "-m", `{"target":"`+tagProd+`"}`, third)
		mustGit(dry.workDir, "push", "origin", git.PromoterValidationsNotesRef)

		reader := newOps("default/reader")
		refs, err := reader.SyncDryRefs(ctx, dry.branch, []string{tagProd})
		Expect(err).NotTo(HaveOccurred())
		Expect(refs.NotesTip).NotTo(Equal(cursor))
		events, ok, err := reader.ListValidations(ctx, cursor, 0)
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(summarize(events)).To(Equal([]string{tagProd + " Git " + third}))
	})
})
