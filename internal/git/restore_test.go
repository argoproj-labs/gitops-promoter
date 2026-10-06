package git_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
)

var _ = Describe("RestoreActiveBranch", func() {
	var tempRepoDir string
	var workDir string
	var repo *v1alpha1.GitRepository

	commitFile := func(name, content, message string) string {
		GinkgoHelper()
		Expect(os.WriteFile(filepath.Join(workDir, name), []byte(content), 0o644)).To(Succeed())
		mustGit(workDir, "add", name)
		mustGit(workDir, "commit", "-m", message)
		return strings.TrimSpace(mustGit(workDir, "rev-parse", "HEAD"))
	}

	newOps := func() *git.EnvironmentOperations {
		GinkgoHelper()
		g := git.NewEnvironmentOperations(repo, &fakeGitProvider{tempDirPath: tempRepoDir}, "default/restore-"+strings.TrimSpace(mustGit(workDir, "rev-parse", "HEAD")))
		Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())
		return g
	}

	BeforeEach(func() {
		var err error
		tempRepoDir, err = os.MkdirTemp("", "git-restore-bare-*")
		Expect(err).NotTo(HaveOccurred())
		mustGit(tempRepoDir, "init", "--bare")

		workDir, err = os.MkdirTemp("", "git-restore-work-*")
		Expect(err).NotTo(HaveOccurred())
		mustGit(workDir, "clone", tempRepoDir, ".")
		mustGit(workDir, "config", "user.name", "Test User")
		mustGit(workDir, "config", "user.email", "test@example.com")
		mustGit(workDir, "config", "commit.gpgsign", "false")

		repo = &v1alpha1.GitRepository{
			Name: "testrepo", Namespace: "default",
			Spec: v1alpha1.GitRepositorySpec{
				Fake:           &v1alpha1.FakeRepo{Owner: "test-owner", Name: "testrepo"},
				ScmProviderRef: v1alpha1.ScmProviderObjectReference{Kind: "ScmProvider", Name: "testprovider"},
			},
		}
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tempRepoDir)).To(Succeed())
		Expect(os.RemoveAll(workDir)).To(Succeed())
	})

	It("restores the active tree, copies the trailers and history note, and leaves proposed in place", func() {
		v1 := commitFile("version.txt", "v1\n", "version v1\n\nPull-request-id: 9\nPull-request-url: https://example.com/pr/9\nSigned-off-by: A <a@example.com>\nSigned-off-by: B <b@example.com>\n")
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development-next")

		note := `{"Pull-request-id":["9"],"Pull-request-merge-time":["2020-01-01T00:00:00Z"]}`
		mustGit(workDir, "notes", "--ref="+git.PromoterHistoryNotesRef, "add", "-m", note, v1)
		mustGit(workDir, "push", "origin", git.PromoterHistoryNotesRef)

		activeDry := "5555555555555555555555555555555555555555"
		Expect(os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), []byte(`{"drySha":"`+activeDry+`"}`), 0o644)).To(Succeed())
		mustGit(workDir, "add", "hydrator.metadata")
		v2 := commitFile("version.txt", "v2\n", "version v2")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development")

		mustGit(workDir, "checkout", "-B", "environment/development-next", "origin/environment/development-next")
		Expect(os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), []byte(`{"drySha":"4444444444444444444444444444444444444444"}`), 0o644)).To(Succeed())
		mustGit(workDir, "add", "hydrator.metadata")
		extra := commitFile("extra.txt", "keep\n", "proposed only")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development-next")

		g := newOps()
		head, status := cloneHeadAndStatus(g.ClonePath())
		restored, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", v1)
		Expect(err).NotTo(HaveOccurred())
		afterHead, afterStatus := cloneHeadAndStatus(g.ClonePath())
		Expect(afterHead).To(Equal(head))
		Expect(afterStatus).To(Equal(status))

		Expect(restored.ActiveSha).NotTo(Equal(v1))
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", restored.ActiveSha+"^"))).To(Equal(v2))
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", restored.ActiveSha+"^{tree}"))).To(Equal(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", v1+"^{tree}"))))
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", "refs/heads/environment/development"))).To(Equal(restored.ActiveSha))

		Expect(restored.BlockedDrySha).To(Equal(activeDry))
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", "refs/heads/environment/development-next"))).To(Equal(extra))

		// The restore commit message carries the target's trailers plus the restore marker, the same way
		// the note is copied from the target with only that key added.
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "log", "-1", "--format=%s", restored.ActiveSha))).To(Equal("Restore environment/development to " + v1[:7]))
		messageTrailers, err := g.GetTrailers(GinkgoT().Context(), restored.ActiveSha)
		Expect(err).NotTo(HaveOccurred())
		Expect(messageTrailers).To(Equal(map[string][]string{
			constants.TrailerPullRequestID:  {"9"},
			constants.TrailerPullRequestUrl: {"https://example.com/pr/9"},
			"Signed-off-by":                 {"A <a@example.com>", "B <b@example.com>"},
			constants.TrailerRestoredFrom:   {v1},
		}))
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "log", "-1", "--format=%B", restored.ActiveSha))).To(HaveSuffix(constants.TrailerRestoredFrom+": "+v1), "the restore marker is the last trailer")

		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())
		got, err := g.GetHistoryNote(GinkgoT().Context(), restored.ActiveSha)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[constants.TrailerRestoredFrom]).To(Equal([]string{v1}))
		Expect(got[constants.TrailerPullRequestID]).To(Equal([]string{"9"}))
		Expect(got[constants.TrailerPullRequestMergeTime]).To(Equal([]string{"2020-01-01T00:00:00Z"}), "the original promotion's merge time is kept")

		restoreMeta, err := g.GetShaMetadataFromGit(GinkgoT().Context(), restored.ActiveSha)
		Expect(err).NotTo(HaveOccurred())
		Expect(restoreMeta.CommitTime.Time).To(BeTemporally("~", time.Now(), time.Minute), "the restore commit's own time is the restore time")

		again, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", v1)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(restored))
	})

	It("copies the target's commit trailers into the note when the target has no history note", func() {
		v1 := commitFile("version.txt", "v1\n", "version v1\n\nPull-request-id: 9\nPull-request-url: https://example.com/pr/9\n")
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")

		commitFile("version.txt", "v2\n", "version v2")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development")

		g := newOps()
		restored, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", v1)
		Expect(err).NotTo(HaveOccurred())

		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())
		got, err := g.GetHistoryNote(GinkgoT().Context(), restored.ActiveSha)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(map[string][]string{
			constants.TrailerPullRequestID:  {"9"},
			constants.TrailerPullRequestUrl: {"https://example.com/pr/9"},
			constants.TrailerRestoredFrom:   {v1},
		}))

		// The marker is added to a copy, so the target's own trailers are unchanged.
		targetTrailers, err := g.GetTrailers(GinkgoT().Context(), v1)
		Expect(err).NotTo(HaveOccurred())
		Expect(targetTrailers).NotTo(HaveKey(constants.TrailerRestoredFrom))
	})

	It("replaces only activePath and keeps the rest of the active tree", func() {
		Expect(os.MkdirAll(filepath.Join(workDir, "apps", "demo"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "demo", "config.yaml"), []byte("old\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "other.txt"), []byte("drop\n"), 0o644)).To(Succeed())
		mustGit(workDir, "add", "-A")
		mustGit(workDir, "commit", "-m", "old")
		target := strings.TrimSpace(mustGit(workDir, "rev-parse", "HEAD"))
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development-next")

		Expect(os.WriteFile(filepath.Join(workDir, "apps", "demo", "config.yaml"), []byte("new\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "other.txt"), []byte("keep\n"), 0o644)).To(Succeed())
		mustGit(workDir, "add", "-A")
		mustGit(workDir, "commit", "-m", "new")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development")

		g := newOps()
		restored, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "apps/demo", target)
		Expect(err).NotTo(HaveOccurred())

		Expect(strings.TrimSpace(mustGit(tempRepoDir, "show", restored.ActiveSha+":apps/demo/config.yaml"))).To(Equal("old"))
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "show", restored.ActiveSha+":other.txt"))).To(Equal("keep"))
	})

	It("refuses a commit that carries Promoter-restored-from", func() {
		v1 := commitFile("version.txt", "v1\n", "version v1")
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")

		v2 := commitFile("version.txt", "v2\n", "version v2")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development")

		g := newOps()
		restored, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", v1)
		Expect(err).NotTo(HaveOccurred())

		mustGit(workDir, "fetch", "origin", "environment/development")
		mustGit(workDir, "checkout", "-B", "environment/development", "origin/environment/development")
		v3 := commitFile("version.txt", "v3\n", "version v3")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development")

		_, err = g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", restored.ActiveSha)
		Expect(err).To(MatchError(ContainSubstring(constants.TrailerRestoredFrom)))
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", "refs/heads/environment/development"))).To(Equal(v3))

		// The commit the restore moved off does not carry the trailer, so it stays eligible.
		movedOff, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", v2)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", movedOff.ActiveSha+"^{tree}"))).To(Equal(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", v2+"^{tree}"))))
	})

	It("restores the commit a restore just moved off while that restore is still the tip", func() {
		v1 := commitFile("version.txt", "v1\n", "version v1")
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")

		v2 := commitFile("version.txt", "v2\n", "version v2")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development")

		g := newOps()
		restored, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", v1)
		Expect(err).NotTo(HaveOccurred())

		undone, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", v2)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", undone.ActiveSha+"^"))).To(Equal(restored.ActiveSha))
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", undone.ActiveSha+"^{tree}"))).To(Equal(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", v2+"^{tree}"))))
	})

	It("refuses a target that was never on the active branch", func() {
		v1 := commitFile("version.txt", "v1\n", "version v1")
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development-next")

		// A commit that exists on the remote, but only on another branch.
		mustGit(workDir, "checkout", "-b", "elsewhere")
		target := commitFile("version.txt", "elsewhere\n", "elsewhere")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/elsewhere")
		mustGit(workDir, "checkout", "environment/development")

		g := newOps()
		_, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", target)
		Expect(err).To(MatchError(ContainSubstring("is not in the history of active branch")))
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", "refs/heads/environment/development"))).To(Equal(v1))
	})

	It("writes nothing when the target is the active tip", func() {
		v1 := commitFile("version.txt", "v1\n", "version v1")
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development-next")

		g := newOps()
		restored, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", v1)
		Expect(err).NotTo(HaveOccurred())
		Expect(restored).To(Equal(git.RestoreResult{ActiveSha: v1, Unchanged: true}))
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", "refs/heads/environment/development"))).To(Equal(v1))
	})

	It("writes nothing and blocks nothing when the active tree already matches an older target", func() {
		Expect(os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), []byte(`{"drySha":"5555555555555555555555555555555555555555"}`), 0o644)).To(Succeed())
		mustGit(workDir, "add", "hydrator.metadata")
		v1 := commitFile("version.txt", "v1\n", "version v1")
		mustGit(workDir, "branch", "-M", "environment/development")
		// Same content, new commit: the branch runs v1's version already.
		mustGit(workDir, "commit", "--allow-empty", "-m", "no-op promotion")
		tip := strings.TrimSpace(mustGit(workDir, "rev-parse", "HEAD"))
		mustGit(workDir, "push", "-u", "origin", "environment/development")

		g := newOps()
		restored, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", v1)
		Expect(err).NotTo(HaveOccurred())
		Expect(restored).To(Equal(git.RestoreResult{ActiveSha: tip, Unchanged: true}))
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", "refs/heads/environment/development"))).To(Equal(tip))

		again, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", v1)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(restored))
	})

	It("removes the clone and forgets it", func() {
		commitFile("version.txt", "v1\n", "version v1")
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")

		g := newOps()
		path := g.ClonePath()
		Expect(path).NotTo(BeEmpty())

		Expect(g.RemoveClone()).To(Succeed())
		Expect(g.ClonePath()).To(BeEmpty())
		_, err := os.Stat(path)
		Expect(os.IsNotExist(err)).To(BeTrue())

		// A second call has nothing to remove.
		Expect(g.RemoveClone()).To(Succeed())
	})

	It("returns an error when the target is not a commit", func() {
		sha := commitFile("version.txt", "v1\n", "version v1")
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development-next")
		tree := strings.TrimSpace(mustGit(workDir, "rev-parse", sha+"^{tree}"))

		g := newOps()
		_, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", tree)
		Expect(err).To(MatchError(ContainSubstring("is not a commit")))
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", "refs/heads/environment/development"))).To(Equal(sha))
	})

	It("returns an error when the target commit does not exist", func() {
		sha := commitFile("version.txt", "v1\n", "version v1")
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development-next")

		g := newOps()
		_, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", "0123456789abcdef0123456789abcdef01234567")
		Expect(err).To(HaveOccurred())
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", "refs/heads/environment/development"))).To(Equal(sha))
	})

	It("UnblockRestore stamps Promoter-restore-unblocked-at and is idempotent", func() {
		v1 := commitFile("version.txt", "v1\n", "version v1\n\nPull-request-id: 9\n")
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")

		Expect(os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), []byte(`{"drySha":"5555555555555555555555555555555555555555"}`), 0o644)).To(Succeed())
		mustGit(workDir, "add", "hydrator.metadata")
		commitFile("version.txt", "v2\n", "version v2")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development")

		g := newOps()
		restored, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", v1)
		Expect(err).NotTo(HaveOccurred())

		at := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
		wrote, err := g.UnblockRestore(GinkgoT().Context(), restored.ActiveSha, at)
		Expect(err).NotTo(HaveOccurred())
		Expect(wrote).To(BeTrue())

		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())
		got, err := g.GetHistoryNote(GinkgoT().Context(), restored.ActiveSha)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[constants.TrailerRestoreUnblockedAt]).To(Equal([]string{"2024-06-01T12:00:00Z"}))
		Expect(got[constants.TrailerRestoredFrom]).To(Equal([]string{v1}))

		again, err := g.UnblockRestore(GinkgoT().Context(), restored.ActiveSha, at.Add(time.Hour))
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(BeTrue())
		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())
		got, err = g.GetHistoryNote(GinkgoT().Context(), restored.ActiveSha)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[constants.TrailerRestoreUnblockedAt]).To(Equal([]string{"2024-06-01T12:00:00Z"}), "idempotent: first timestamp kept")
	})

	It("UnblockRestore builds a note from commit trailers when the restore has no history note", func() {
		v1 := commitFile("version.txt", "v1\n", "version v1")
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")
		commitFile("version.txt", "v2\n", "version v2")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development")

		g := newOps()
		restored, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", v1)
		Expect(err).NotTo(HaveOccurred())

		// Drop the note the restore wrote so UnblockRestore falls back to commit trailers.
		mustGit(workDir, "fetch", "origin", "+"+git.PromoterHistoryNotesRef+":"+git.PromoterHistoryNotesRef)
		mustGit(workDir, "notes", "--ref="+git.PromoterHistoryNotesRef, "remove", "--ignore-missing", restored.ActiveSha)
		mustGit(workDir, "push", "origin", git.PromoterHistoryNotesRef)
		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())

		wrote, err := g.UnblockRestore(GinkgoT().Context(), restored.ActiveSha, time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC))
		Expect(err).NotTo(HaveOccurred())
		Expect(wrote).To(BeTrue())
		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())
		got, err := g.GetHistoryNote(GinkgoT().Context(), restored.ActiveSha)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[constants.TrailerRestoredFrom]).To(Equal([]string{v1}))
		Expect(got[constants.TrailerRestoreUnblockedAt]).To(Equal([]string{"2024-07-01T00:00:00Z"}))
	})

	It("UnblockRestore is a no-op for a non-restore commit", func() {
		v1 := commitFile("version.txt", "v1\n", "version v1")
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")

		g := newOps()
		wrote, err := g.UnblockRestore(GinkgoT().Context(), v1, time.Now())
		Expect(err).NotTo(HaveOccurred())
		Expect(wrote).To(BeFalse())
	})

	It("RestoreBlockState reports blocked dry SHA and unblock state", func() {
		v1 := commitFile("version.txt", "v1\n", "version v1")
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")

		activeDry := "5555555555555555555555555555555555555555"
		Expect(os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), []byte(`{"drySha":"`+activeDry+`"}`), 0o644)).To(Succeed())
		mustGit(workDir, "add", "hydrator.metadata")
		commitFile("version.txt", "v2\n", "version v2")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development")

		g := newOps()
		restored, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", v1)
		Expect(err).NotTo(HaveOccurred())

		gate, err := g.RestoreBlockState(GinkgoT().Context(), restored.ActiveSha, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(gate).To(Equal(git.RestoreBlock{IsRestore: true, RestoredFrom: v1, BlockedDrySha: activeDry}))

		_, err = g.UnblockRestore(GinkgoT().Context(), restored.ActiveSha, time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC))
		Expect(err).NotTo(HaveOccurred())
		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())

		gate, err = g.RestoreBlockState(GinkgoT().Context(), restored.ActiveSha, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(gate).To(Equal(git.RestoreBlock{IsRestore: true, RestoredFrom: v1, Unblocked: true, BlockedDrySha: activeDry}))

		ordinary, err := g.RestoreBlockState(GinkgoT().Context(), v1, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(ordinary).To(Equal(git.RestoreBlock{}))
	})

	It("refuses a restore of a tip that already carries Promoter-restore-unblocked-at", func() {
		v1 := commitFile("version.txt", "v1\n", "version v1")
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")

		Expect(os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), []byte(`{"drySha":"5555555555555555555555555555555555555555"}`), 0o644)).To(Succeed())
		mustGit(workDir, "add", "hydrator.metadata")
		commitFile("version.txt", "v2\n", "version v2")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development")

		g := newOps()
		restored, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", v1)
		Expect(err).NotTo(HaveOccurred())
		_, err = g.UnblockRestore(GinkgoT().Context(), restored.ActiveSha, time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC))
		Expect(err).NotTo(HaveOccurred())

		// The tip is already this restore and the previous RestoreActiveCommit was released.
		// A new restore of the same target is refused, and the unblock trailer stays on the note.
		_, err = g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", v1)
		Expect(err).To(MatchError(ContainSubstring(constants.TrailerRestoreUnblockedAt)))
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", "refs/heads/environment/development"))).To(Equal(restored.ActiveSha))

		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())
		got, err := g.GetHistoryNote(GinkgoT().Context(), restored.ActiveSha)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[constants.TrailerRestoreUnblockedAt]).To(Equal([]string{"2024-09-01T00:00:00Z"}))
		Expect(got[constants.TrailerRestoredFrom]).To(Equal([]string{v1}))

		gate, err := g.RestoreBlockState(GinkgoT().Context(), restored.ActiveSha, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(gate.IsRestore).To(BeTrue())
		Expect(gate.Unblocked).To(BeTrue())
	})

	It("blocks and unblocks when restore and unblock are done only with git commands", func() {
		v1 := commitFile("version.txt", "v1\n", "version v1")
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")

		activeDry := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		Expect(os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), []byte(`{"drySha":"`+activeDry+`"}`), 0o644)).To(Succeed())
		mustGit(workDir, "add", "hydrator.metadata")
		commitFile("version.txt", "v2\n", "version v2")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development")

		mustGit(workDir, "fetch", "origin", "environment/development")
		activeTip := strings.TrimSpace(mustGit(workDir, "rev-parse", "origin/environment/development"))
		tree := strings.TrimSpace(mustGit(workDir, "rev-parse", v1+"^{tree}"))
		message := "Restore environment/development to " + v1[:7] + "\n\n" + constants.TrailerRestoredFrom + ": " + v1 + "\n"
		restoreSha := strings.TrimSpace(mustGit(workDir, "commit-tree", tree, "-p", activeTip, "-m", message))

		notePayload, err := json.Marshal(map[string][]string{constants.TrailerRestoredFrom: {v1}})
		Expect(err).NotTo(HaveOccurred())
		mustGit(workDir, "notes", "--ref="+git.PromoterHistoryNotesRef, "add", "-f", "-m", string(notePayload), restoreSha)
		mustGit(workDir, "push", "origin", git.PromoterHistoryNotesRef+":"+git.PromoterHistoryNotesRef)
		mustGit(workDir, "push", "--force-with-lease=refs/heads/environment/development:"+activeTip, "origin", restoreSha+":refs/heads/environment/development")

		g := newOps()
		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())
		gate, err := g.RestoreBlockState(GinkgoT().Context(), restoreSha, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(gate).To(Equal(git.RestoreBlock{IsRestore: true, RestoredFrom: v1, BlockedDrySha: activeDry}))

		unblockPayload, err := json.Marshal(map[string][]string{
			constants.TrailerRestoredFrom:       {v1},
			constants.TrailerRestoreUnblockedAt: {"2024-10-02T18:00:00Z"},
		})
		Expect(err).NotTo(HaveOccurred())
		mustGit(workDir, "fetch", "origin", "+"+git.PromoterHistoryNotesRef+":"+git.PromoterHistoryNotesRef)
		mustGit(workDir, "notes", "--ref="+git.PromoterHistoryNotesRef, "add", "-f", "-m", string(unblockPayload), restoreSha)
		mustGit(workDir, "push", "origin", git.PromoterHistoryNotesRef+":"+git.PromoterHistoryNotesRef)

		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())
		gate, err = g.RestoreBlockState(GinkgoT().Context(), restoreSha, "")
		Expect(err).NotTo(HaveOccurred())
		Expect(gate).To(Equal(git.RestoreBlock{IsRestore: true, RestoredFrom: v1, Unblocked: true, BlockedDrySha: activeDry}))
	})
})
