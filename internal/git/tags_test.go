package git_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
)

// dryRepo is a bare "remote" repository with a dry branch, plus a work tree that plays a person pushing
// to it. The remote serves filtered fetches and commits by SHA, as hosted SCMs do.
type dryRepo struct {
	repo      *v1alpha1.GitRepository
	remoteDir string
	workDir   string
	branch    string
}

func newDryRepo() *dryRepo {
	GinkgoHelper()
	remoteDir, err := os.MkdirTemp("", "git-dry-remote-*")
	Expect(err).NotTo(HaveOccurred())
	workDir, err := os.MkdirTemp("", "git-dry-work-*")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		Expect(os.RemoveAll(remoteDir)).To(Succeed())
		Expect(os.RemoveAll(workDir)).To(Succeed())
	})

	mustGit(remoteDir, "init", "--bare")
	mustGit(remoteDir, "config", "uploadpack.allowFilter", "true")
	mustGit(workDir, "clone", remoteDir, ".")
	mustGit(workDir, "config", "user.name", "Jane Doe")
	mustGit(workDir, "config", "user.email", "jane@example.com")
	mustGit(workDir, "config", "commit.gpgsign", "false")
	mustGit(workDir, "config", "tag.gpgsign", "false")

	d := &dryRepo{
		remoteDir: remoteDir,
		workDir:   workDir,
		repo: &v1alpha1.GitRepository{
			Spec: v1alpha1.GitRepositorySpec{
				GitHub:         &v1alpha1.GitHubRepo{Owner: "test-owner", Name: "dry"},
				ScmProviderRef: v1alpha1.ScmProviderObjectReference{Kind: "ScmProvider", Name: "testprovider"},
			},
			Name: "dry", Namespace: "default",
		},
	}
	d.commit("first")
	d.branch = strings.TrimSpace(mustGit(workDir, "rev-parse", "--abbrev-ref", "HEAD"))
	d.push()
	return d
}

// commit adds a commit to whatever is checked out in the work tree and returns its SHA.
func (d *dryRepo) commit(message string) string {
	GinkgoHelper()
	name := strings.ReplaceAll(message, " ", "-") + ".txt"
	Expect(os.WriteFile(filepath.Join(d.workDir, name), []byte(message), 0o644)).To(Succeed())
	mustGit(d.workDir, "add", "-A")
	mustGit(d.workDir, "commit", "-m", message)
	return d.head()
}

func (d *dryRepo) head() string {
	GinkgoHelper()
	return strings.TrimSpace(mustGit(d.workDir, "rev-parse", "HEAD"))
}

// push pushes the dry branch.
func (d *dryRepo) push() {
	GinkgoHelper()
	mustGit(d.workDir, "push", "origin", d.branch)
}

// pushTag points a tag at sha on the remote, as a person with push access would.
func (d *dryRepo) pushTag(tag, sha string) {
	GinkgoHelper()
	mustGit(d.workDir, "push", "--force", "origin", sha+":refs/tags/"+tag)
}

// remoteRef returns what a ref holds on the remote, "" when it does not exist.
func (d *dryRepo) remoteRef(ref string) string {
	GinkgoHelper()
	out, err := runGitCmd(d.remoteDir, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// newOps returns a fresh clone of the remote for the given identity.
func (d *dryRepo) newOps(identity string) *git.EnvironmentOperations {
	GinkgoHelper()
	ops := git.NewEnvironmentOperations(d.repo, &fakeGitProvider{tempDirPath: d.remoteDir}, identity)
	Expect(ops.CloneRepo(context.Background())).To(Succeed())
	DeferCleanup(func() { _ = ops.RemoveClone() })
	return ops
}

var _ = Describe("Environment tags", func() {
	const (
		tagDev  = "environment/dev-tag"
		tagProd = "environment/prod-tag"
	)

	var (
		ctx   context.Context
		dry   *dryRepo
		first string
	)

	BeforeEach(func() {
		ctx = context.Background()
		dry = newDryRepo()
		first = dry.head()
	})

	Describe("SyncDryRefs", func() {
		It("returns the dry head, and a zero state for tags that do not exist", func() {
			ops := dry.newOps("default/gate")
			refs, err := ops.SyncDryRefs(ctx, dry.branch, []string{tagDev, tagProd})
			Expect(err).NotTo(HaveOccurred())
			Expect(refs.Head).To(Equal(first))
			Expect(refs.NotesTip).To(BeEmpty())
			Expect(refs.Tags).To(HaveLen(2))
			Expect(refs.Tags[tagDev]).To(Equal(git.TagState{}))
			Expect(refs.Tags[tagProd]).To(Equal(git.TagState{}))
		})

		It("follows the dry branch and reads tags where they are", func() {
			ops := dry.newOps("default/gate")
			dry.pushTag(tagDev, first)
			second := dry.commit("second")
			dry.push()

			refs, err := ops.SyncDryRefs(ctx, dry.branch, []string{tagDev, tagProd})
			Expect(err).NotTo(HaveOccurred())
			Expect(refs.Head).To(Equal(second))
			Expect(refs.Tags[tagDev]).To(Equal(git.TagState{Raw: first, Commit: first}))
			Expect(refs.Tags[tagProd]).To(Equal(git.TagState{}))
			Expect(ops.HasCommit(ctx, second)).To(BeTrue())
		})

		It("sees a tag that was moved backward, and one that was deleted", func() {
			ops := dry.newOps("default/gate")
			second := dry.commit("second")
			dry.push()
			dry.pushTag(tagDev, second)
			dry.pushTag(tagProd, second)
			refs, err := ops.SyncDryRefs(ctx, dry.branch, []string{tagDev, tagProd})
			Expect(err).NotTo(HaveOccurred())
			Expect(refs.Tags[tagDev].Commit).To(Equal(second))
			Expect(refs.Tags[tagProd].Commit).To(Equal(second))

			dry.pushTag(tagDev, first)
			mustGit(dry.workDir, "push", "origin", ":refs/tags/"+tagProd)

			// A new instance per reconcile, as the controllers do.
			ops = dry.newOps("default/gate")
			refs, err = ops.SyncDryRefs(ctx, dry.branch, []string{tagDev, tagProd})
			Expect(err).NotTo(HaveOccurred())
			Expect(refs.Tags[tagDev]).To(Equal(git.TagState{Raw: first, Commit: first}))
			Expect(refs.Tags[tagProd]).To(Equal(git.TagState{}))
			Expect(mustGit(ops.ClonePath(), "for-each-ref", "refs/tags/"+tagProd)).To(BeEmpty())
		})

		It("reads an annotated tag by the commit it points at, and keeps the tag object for the lease", func() {
			mustGit(dry.workDir, "tag", "-a", "-m", "release", tagDev, first)
			mustGit(dry.workDir, "push", "origin", "refs/tags/"+tagDev)
			tagObject := dry.remoteRef("refs/tags/" + tagDev)
			Expect(tagObject).NotTo(Equal(first))

			ops := dry.newOps("default/gate")
			refs, err := ops.SyncDryRefs(ctx, dry.branch, []string{tagDev})
			Expect(err).NotTo(HaveOccurred())
			Expect(refs.Tags[tagDev]).To(Equal(git.TagState{Raw: tagObject, Commit: first}))
		})

		It("reads a tag on a commit that is not on the dry branch", func() {
			mustGit(dry.workDir, "checkout", "-b", "hotfix")
			hotfix := dry.commit("hotfix")
			mustGit(dry.workDir, "checkout", dry.branch)
			dry.pushTag(tagDev, hotfix)

			ops := dry.newOps("default/gate")
			refs, err := ops.SyncDryRefs(ctx, dry.branch, []string{tagDev})
			Expect(err).NotTo(HaveOccurred())
			Expect(refs.Tags[tagDev].Commit).To(Equal(hotfix))
			Expect(ops.HasCommit(ctx, hotfix)).To(BeTrue())
			Expect(ops.ContainsCommit(ctx, hotfix, first)).To(BeTrue())
		})

		It("fetches commits without their trees", func() {
			ops := dry.newOps("default/gate")
			_, err := ops.SyncDryRefs(ctx, dry.branch, nil)
			Expect(err).NotTo(HaveOccurred())
			tree := strings.TrimSpace(mustGit(dry.workDir, "rev-parse", first+"^{tree}"))
			Expect(gitObjectPresent(ops.ClonePath(), first)).To(BeTrue())
			Expect(gitObjectPresent(ops.ClonePath(), tree)).To(BeFalse(), "the dry branch's trees should not be downloaded")
		})

		It("reports a dry branch that does not exist", func() {
			ops := dry.newOps("default/gate")
			_, err := ops.SyncDryRefs(ctx, "no-such-branch", []string{tagDev})
			var notFound *git.DryBranchNotFoundError
			Expect(errors.As(err, &notFound)).To(BeTrue(), "got %v", err)
			Expect(notFound.Branch).To(Equal("no-such-branch"))
		})

		It("refuses a tag that does not point at a commit", func() {
			tree := strings.TrimSpace(mustGit(dry.workDir, "rev-parse", first+"^{tree}"))
			mustGit(dry.workDir, "push", "origin", tree+":refs/tags/"+tagDev)

			ops := dry.newOps("default/gate")
			_, err := ops.SyncDryRefs(ctx, dry.branch, []string{tagDev})
			Expect(err).To(MatchError(ContainSubstring("does not point at a commit")))
		})

		It("refuses an annotated tag of something that is not a commit", func() {
			tree := strings.TrimSpace(mustGit(dry.workDir, "rev-parse", first+"^{tree}"))
			mustGit(dry.workDir, "tag", "-a", "-m", "a tree", tagDev, tree)
			mustGit(dry.workDir, "push", "origin", "refs/tags/"+tagDev)

			ops := dry.newOps("default/gate")
			_, err := ops.SyncDryRefs(ctx, dry.branch, []string{tagDev})
			Expect(err).To(MatchError(ContainSubstring("does not point at a commit")))
		})
	})

	Describe("MoveTag", func() {
		var (
			ops    *git.EnvironmentOperations
			second string
		)

		BeforeEach(func() {
			second = dry.commit("second")
			dry.push()
			ops = dry.newOps("default/gate")
			_, err := ops.SyncDryRefs(ctx, dry.branch, []string{tagDev})
			Expect(err).NotTo(HaveOccurred())
		})

		It("creates a tag that does not exist", func() {
			Expect(ops.MoveTag(ctx, tagDev, first, "")).To(Succeed())
			Expect(dry.remoteRef("refs/tags/" + tagDev)).To(Equal(first))
			Expect(strings.TrimSpace(mustGit(dry.remoteDir, "cat-file", "-t", "refs/tags/"+tagDev))).To(Equal("commit"), "the tag should be lightweight")
		})

		It("moves a tag forward and backward while the lease holds", func() {
			Expect(ops.MoveTag(ctx, tagDev, first, "")).To(Succeed())
			Expect(ops.MoveTag(ctx, tagDev, second, first)).To(Succeed())
			Expect(dry.remoteRef("refs/tags/" + tagDev)).To(Equal(second))
			Expect(ops.MoveTag(ctx, tagDev, first, second)).To(Succeed())
			Expect(dry.remoteRef("refs/tags/" + tagDev)).To(Equal(first))

			state, err := ops.ReadTag(ctx, tagDev)
			Expect(err).NotTo(HaveOccurred())
			Expect(state).To(Equal(git.TagState{Raw: first, Commit: first}))
		})

		It("does not overwrite a tag someone moved since it was read", func() {
			Expect(ops.MoveTag(ctx, tagDev, first, "")).To(Succeed())
			By("A person moving the tag behind the controller's back")
			dry.pushTag(tagDev, second)

			err := ops.MoveTag(ctx, tagDev, first, first)
			var pushErr *git.TagPushError
			Expect(errors.As(err, &pushErr)).To(BeTrue(), "got %v", err)
			Expect(pushErr.Tag).To(Equal(tagDev))
			Expect(pushErr.Reason).To(ContainSubstring("stale info"))
			Expect(dry.remoteRef("refs/tags/"+tagDev)).To(Equal(second), "the hand-moved tag must be left alone")

			By("Reading the tag again to learn where it is now")
			state, err := ops.ReadTag(ctx, tagDev)
			Expect(err).NotTo(HaveOccurred())
			Expect(state.Commit).To(Equal(second))
		})

		It("does not create a tag someone created since it was read", func() {
			dry.pushTag(tagDev, second)

			err := ops.MoveTag(ctx, tagDev, first, "")
			var pushErr *git.TagPushError
			Expect(errors.As(err, &pushErr)).To(BeTrue(), "got %v", err)
			Expect(dry.remoteRef("refs/tags/" + tagDev)).To(Equal(second))
		})

		It("does not recreate a tag someone deleted since it was read", func() {
			Expect(ops.MoveTag(ctx, tagDev, first, "")).To(Succeed())
			mustGit(dry.workDir, "push", "origin", ":refs/tags/"+tagDev)

			err := ops.MoveTag(ctx, tagDev, second, first)
			var pushErr *git.TagPushError
			Expect(errors.As(err, &pushErr)).To(BeTrue(), "got %v", err)
			Expect(dry.remoteRef("refs/tags/" + tagDev)).To(BeEmpty())
		})

		It("replaces an annotated tag when given the tag object as the lease", func() {
			mustGit(dry.workDir, "tag", "-a", "-m", "release", tagProd, first)
			mustGit(dry.workDir, "push", "origin", "refs/tags/"+tagProd)
			state, err := ops.ReadTag(ctx, tagProd)
			Expect(err).NotTo(HaveOccurred())
			Expect(state.Raw).NotTo(Equal(state.Commit))

			Expect(ops.MoveTag(ctx, tagProd, second, state.Raw)).To(Succeed())
			Expect(dry.remoteRef("refs/tags/" + tagProd)).To(Equal(second))
		})

		It("reports a push the remote refuses", func() {
			hook := filepath.Join(dry.remoteDir, "hooks", "pre-receive")
			Expect(os.WriteFile(hook, []byte("#!/bin/sh\necho 'tags are protected' >&2\nexit 1\n"), 0o755)).To(Succeed())

			err := ops.MoveTag(ctx, tagDev, first, "")
			var pushErr *git.TagPushError
			Expect(errors.As(err, &pushErr)).To(BeTrue(), "got %v", err)
			Expect(pushErr.Reason).To(ContainSubstring("remote rejected"))
			Expect(dry.remoteRef("refs/tags/" + tagDev)).To(BeEmpty())
		})

		It("tags a commit that is not on the dry branch", func() {
			mustGit(dry.workDir, "checkout", "-b", "hotfix", first)
			hotfix := dry.commit("hotfix")
			mustGit(dry.workDir, "push", "origin", "hotfix")
			mustGit(dry.workDir, "checkout", dry.branch)

			Expect(ops.HasCommit(ctx, hotfix)).To(BeFalse())
			Expect(ops.EnsureCommit(ctx, hotfix)).To(BeTrue())
			Expect(ops.MoveTag(ctx, tagDev, hotfix, "")).To(Succeed())
			Expect(dry.remoteRef("refs/tags/" + tagDev)).To(Equal(hotfix))
		})
	})

	Describe("EnsureCommit and ContainsCommit", func() {
		var (
			ops     *git.EnvironmentOperations
			second  string
			hotfix1 string
			hotfix2 string
		)

		BeforeEach(func() {
			second = dry.commit("second")
			dry.push()
			mustGit(dry.workDir, "checkout", "-b", "hotfix", first)
			hotfix1 = dry.commit("hotfix one")
			hotfix2 = dry.commit("hotfix two")
			mustGit(dry.workDir, "push", "origin", "hotfix")
			mustGit(dry.workDir, "checkout", dry.branch)

			ops = dry.newOps("default/gate")
			_, err := ops.SyncDryRefs(ctx, dry.branch, nil)
			Expect(err).NotTo(HaveOccurred())
		})

		It("finds a commit on the dry branch without fetching", func() {
			Expect(ops.EnsureCommit(ctx, first)).To(BeTrue())
			Expect(ops.EnsureCommit(ctx, second)).To(BeTrue())
		})

		It("fetches a commit that is off the dry branch, tip or not", func() {
			Expect(ops.HasCommit(ctx, hotfix1)).To(BeFalse())
			Expect(ops.EnsureCommit(ctx, hotfix1)).To(BeTrue())
			Expect(ops.HasCommit(ctx, hotfix1)).To(BeTrue())
			Expect(ops.HasCommit(ctx, hotfix2)).To(BeFalse(), "only the requested commit's history is fetched")
			Expect(ops.EnsureCommit(ctx, hotfix2)).To(BeTrue())
		})

		It("reports a commit the repository does not have, without an error", func() {
			Expect(ops.EnsureCommit(ctx, strings.Repeat("ab", 20))).To(BeFalse())
		})

		It("reports an object that is not a commit, and a malformed SHA, as missing", func() {
			tree := strings.TrimSpace(mustGit(dry.workDir, "rev-parse", first+"^{tree}"))
			Expect(ops.EnsureCommit(ctx, tree)).To(BeFalse())
			Expect(ops.EnsureCommit(ctx, "main")).To(BeFalse())
			Expect(ops.EnsureCommit(ctx, "")).To(BeFalse())
		})

		It("answers which commit contains which", func() {
			Expect(ops.EnsureCommit(ctx, hotfix2)).To(BeTrue())

			Expect(ops.ContainsCommit(ctx, second, first)).To(BeTrue())
			Expect(ops.ContainsCommit(ctx, first, second)).To(BeFalse())
			Expect(ops.ContainsCommit(ctx, second, second)).To(BeTrue())
			Expect(ops.ContainsCommit(ctx, hotfix2, hotfix1)).To(BeTrue())
			Expect(ops.ContainsCommit(ctx, hotfix2, first)).To(BeTrue())
			Expect(ops.ContainsCommit(ctx, hotfix2, second)).To(BeFalse(), "the hotfix branched before the second commit")
			Expect(ops.ContainsCommit(ctx, second, hotfix1)).To(BeFalse())
			Expect(ops.ContainsCommit(ctx, "", first)).To(BeFalse())
			Expect(ops.ContainsCommit(ctx, first, "")).To(BeFalse())
		})

		It("sees a hotfix as contained once it is merged into the dry branch", func() {
			Expect(ops.EnsureCommit(ctx, hotfix2)).To(BeTrue())
			Expect(ops.ContainsCommit(ctx, second, hotfix2)).To(BeFalse())

			mustGit(dry.workDir, "merge", "--no-ff", "-m", "merge hotfix", "hotfix")
			merge := dry.head()
			dry.push()

			ops = dry.newOps("default/gate")
			refs, err := ops.SyncDryRefs(ctx, dry.branch, nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(refs.Head).To(Equal(merge))
			Expect(ops.ContainsCommit(ctx, merge, hotfix2)).To(BeTrue())
			Expect(ops.ContainsCommit(ctx, merge, second)).To(BeTrue())
		})

		It("fails rather than downloading when asked about a commit that is not in the clone", func() {
			_, err := ops.ContainsCommit(ctx, hotfix2, first)
			Expect(err).To(HaveOccurred())
			Expect(ops.HasCommit(ctx, hotfix2)).To(BeFalse())
		})
	})

	Describe("RemoteCommitExists", func() {
		var gap *fakeGitProvider

		BeforeEach(func() {
			gap = &fakeGitProvider{tempDirPath: dry.remoteDir}
		})

		It("finds commits on and off the dry branch, tips or not", func() {
			second := dry.commit("second")
			dry.push()
			mustGit(dry.workDir, "checkout", "-b", "hotfix", first)
			hotfix1 := dry.commit("hotfix one")
			dry.commit("hotfix two")
			mustGit(dry.workDir, "push", "origin", "hotfix")

			for _, sha := range []string{first, second, hotfix1} {
				Expect(git.RemoteCommitExists(ctx, gap, dry.repo, sha, dry.branch)).To(BeTrue(), sha)
			}
		})

		It("reports a commit the repository does not have", func() {
			Expect(git.RemoteCommitExists(ctx, gap, dry.repo, strings.Repeat("cd", 20), dry.branch)).To(BeFalse())
		})

		It("reports an object that is not a commit, and a malformed SHA, as missing", func() {
			tree := strings.TrimSpace(mustGit(dry.workDir, "rev-parse", first+"^{tree}"))
			Expect(git.RemoteCommitExists(ctx, gap, dry.repo, tree, dry.branch)).To(BeFalse())
			Expect(git.RemoteCommitExists(ctx, gap, dry.repo, "4f2a9c", dry.branch)).To(BeFalse())
		})

		It("returns an error when the repository cannot be reached", func() {
			_, err := git.RemoteCommitExists(ctx, &fakeGitProvider{tempDirPath: filepath.Join(dry.remoteDir, "nowhere")}, dry.repo, first, dry.branch)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("LiveDrySha", func() {
		const active = "environment/dev"

		var gap *fakeGitProvider

		// hydrate commits hydrator.metadata with the given content at path on the active branch and pushes it.
		hydrate := func(path, content string) {
			GinkgoHelper()
			if _, err := runGitCmd(dry.workDir, "rev-parse", "--verify", "--quiet", "refs/heads/"+active); err != nil {
				mustGit(dry.workDir, "checkout", "--orphan", active)
				mustGit(dry.workDir, "rm", "-rf", "--ignore-unmatch", ".")
			} else {
				mustGit(dry.workDir, "checkout", active)
			}
			file := filepath.Join(dry.workDir, path)
			Expect(os.MkdirAll(filepath.Dir(file), 0o755)).To(Succeed())
			Expect(os.WriteFile(file, []byte(content), 0o644)).To(Succeed())
			mustGit(dry.workDir, "add", "-A")
			mustGit(dry.workDir, "commit", "-m", "hydrate")
			mustGit(dry.workDir, "push", "origin", active)
			mustGit(dry.workDir, "checkout", dry.branch)
		}

		BeforeEach(func() {
			gap = &fakeGitProvider{tempDirPath: dry.remoteDir}
		})

		It("returns the dry SHA the tip of the active branch was hydrated from", func() {
			hydrate("hydrator.metadata", `{"drySha":"`+first+`"}`)
			Expect(git.LiveDrySha(ctx, gap, dry.repo, active, "")).To(Equal(first))

			By("Following the branch to a later hydration")
			second := dry.commit("second")
			dry.push()
			hydrate("hydrator.metadata", `{"drySha":"`+second+`","subject":"second"}`)
			Expect(git.LiveDrySha(ctx, gap, dry.repo, active, "")).To(Equal(second))
		})

		It("reads the metadata under the active path", func() {
			second := dry.commit("second")
			dry.push()
			hydrate("hydrator.metadata", `{"drySha":"`+first+`"}`)
			hydrate("apps/one/hydrator.metadata", `{"drySha":"`+second+`"}`)
			Expect(git.LiveDrySha(ctx, gap, dry.repo, active, "apps/one")).To(Equal(second))
			Expect(git.LiveDrySha(ctx, gap, dry.repo, active, "")).To(Equal(first))
			Expect(git.LiveDrySha(ctx, gap, dry.repo, active, "apps/two")).To(BeEmpty())
		})

		It("reports nothing live for a branch that does not exist", func() {
			Expect(git.LiveDrySha(ctx, gap, dry.repo, active, "")).To(BeEmpty())
		})

		It("reports nothing live for a branch that was never hydrated", func() {
			hydrate("README.md", "nothing here yet")
			Expect(git.LiveDrySha(ctx, gap, dry.repo, active, "")).To(BeEmpty())
		})

		It("reports nothing live when the metadata names no commit", func() {
			hydrate("hydrator.metadata", `{"drySha":"n/a"}`)
			Expect(git.LiveDrySha(ctx, gap, dry.repo, active, "")).To(BeEmpty())
		})

		It("returns an error for metadata it cannot read, rather than guessing", func() {
			hydrate("hydrator.metadata", `not json`)
			_, err := git.LiveDrySha(ctx, gap, dry.repo, active, "")
			var malformed *git.MalformedHydratorMetadataError
			Expect(errors.As(err, &malformed)).To(BeTrue(), "got %v", err)
		})

		It("returns an error when the repository cannot be reached", func() {
			_, err := git.LiveDrySha(ctx, &fakeGitProvider{tempDirPath: filepath.Join(dry.remoteDir, "nowhere")}, dry.repo, active, "")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("MoveTag input", func() {
		It("only takes full object IDs, for the commit and for the lease", func() {
			ops := dry.newOps("default/gate")
			_, err := ops.SyncDryRefs(ctx, dry.branch, []string{tagDev})
			Expect(err).NotTo(HaveOccurred())

			Expect(ops.MoveTag(ctx, tagDev, "HEAD", "")).To(MatchError(ContainSubstring("not a full git object ID")))
			Expect(ops.MoveTag(ctx, tagDev, "--delete", "")).To(MatchError(ContainSubstring("not a full git object ID")))
			Expect(ops.MoveTag(ctx, tagDev, first, "refs/heads/"+dry.branch)).To(MatchError(ContainSubstring("not a full git object ID")))
			Expect(dry.remoteRef("refs/tags/"+tagDev)).To(BeEmpty(), "nothing was pushed")
		})
	})

	Describe("IsFullObjectID", func() {
		It("accepts complete lowercase SHA-1 and SHA-256 IDs only", func() {
			Expect(git.IsFullObjectID(first)).To(BeTrue())
			Expect(git.IsFullObjectID(strings.Repeat("ab", 32))).To(BeTrue())
			for _, other := range []string{"", "n/a", "4f2a9c", strings.ToUpper(first), first + "0", " " + first} {
				Expect(git.IsFullObjectID(other)).To(BeFalse(), "%q", other)
			}
		})
	})

	Describe("PorcelainRejection", func() {
		It("returns git's verdict for the rejected ref", func() {
			out := "To /tmp/remote\n!\tabc123:refs/tags/environment/dev-tag\t[rejected] (stale info)\nDone\n"
			Expect(git.PorcelainRejection(out, "refs/tags/environment/dev-tag")).To(Equal("[rejected] (stale info)"))
		})

		It("ignores other refs and accepted pushes", func() {
			out := "To /tmp/remote\n*\tabc123:refs/tags/environment/dev-tag\t[new tag]\n!\tabc123:refs/tags/other\t[remote rejected] (hook declined)\nDone\n"
			Expect(git.PorcelainRejection(out, "refs/tags/environment/dev-tag")).To(BeEmpty())
			Expect(git.PorcelainRejection("", "refs/tags/environment/dev-tag")).To(BeEmpty())
		})
	})
})
