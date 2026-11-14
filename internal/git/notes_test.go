package git_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
)

var _ = Describe("Promotion history notes", func() {
	var tempRepoDir string
	var workDir string
	var defaultBranch string
	var repo *v1alpha1.GitRepository
	var shaOne, shaTwo string

	commit := func(fileName, content, message string) string {
		Expect(os.WriteFile(filepath.Join(workDir, fileName), []byte(content), 0o644)).To(Succeed())
		_, err := runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", message)
		Expect(err).NotTo(HaveOccurred())
		return strings.TrimSpace(mustGit(workDir, "rev-parse", "HEAD"))
	}

	newEnvOps := func(identity string) *git.EnvironmentOperations {
		gap := &fakeGitProvider{tempDirPath: tempRepoDir}
		g := git.NewEnvironmentOperations(repo, gap, identity)
		Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())
		return g
	}

	BeforeEach(func() {
		var err error
		tempRepoDir, err = os.MkdirTemp("", "git-test-*")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(tempRepoDir, "init", "--bare")
		Expect(err).NotTo(HaveOccurred())

		workDir, err = os.MkdirTemp("", "git-work-*")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "clone", tempRepoDir, ".")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "config", "user.name", "Test User")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "config", "user.email", "test@example.com")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "config", "commit.gpgsign", "false")
		Expect(err).NotTo(HaveOccurred())

		shaOne = commit("one.txt", "one", "first commit")
		shaTwo = commit("two.txt", "two", "second commit")

		defaultBranch = strings.TrimSpace(mustGit(workDir, "rev-parse", "--abbrev-ref", "HEAD"))
		_, err = runGitCmd(workDir, "push", "-u", "origin", defaultBranch)
		Expect(err).NotTo(HaveOccurred())

		repo = &v1alpha1.GitRepository{
			Spec: v1alpha1.GitRepositorySpec{
				GitHub: &v1alpha1.GitHubRepo{Owner: "test-owner", Name: "testrepo"},
				ScmProviderRef: v1alpha1.ScmProviderObjectReference{
					Kind: "ScmProvider",
					Name: "testprovider",
				},
			},
			Name: "testrepo", Namespace: "default",
		}
	})

	AfterEach(func() {
		if tempRepoDir != "" {
			Expect(os.RemoveAll(tempRepoDir)).To(Succeed())
		}
		if workDir != "" {
			Expect(os.RemoveAll(workDir)).To(Succeed())
		}
	})

	It("roundtrips a history note through SetHistoryNote and GetHistoryNote", func() {
		g := newEnvOps("default/roundtrip")
		Expect(g.FetchBranch(GinkgoT().Context(), defaultBranch)).To(Succeed())

		payload := map[string][]string{
			"Pull-request-id":  {"42"},
			"Pull-request-url": {"https://example.com/pr/42"},
			"Signed-off-by":    {"A <a@example.com>", "B <b@example.com>"},
		}
		Expect(g.SetHistoryNote(GinkgoT().Context(), shaOne, payload)).To(Succeed())

		// Read through a fresh clone to prove the note made it to the remote.
		g2 := newEnvOps("default/roundtrip-reader")
		Expect(g2.FetchBranch(GinkgoT().Context(), defaultBranch)).To(Succeed())
		Expect(g2.FetchNotes(GinkgoT().Context())).To(Succeed())
		got, err := g2.GetHistoryNote(GinkgoT().Context(), shaOne)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(payload))
	})

	It("overwrites an existing note so retries are idempotent", func() {
		g := newEnvOps("default/overwrite")
		Expect(g.FetchBranch(GinkgoT().Context(), defaultBranch)).To(Succeed())

		Expect(g.SetHistoryNote(GinkgoT().Context(), shaOne, map[string][]string{"Pull-request-id": {"1"}})).To(Succeed())
		Expect(g.SetHistoryNote(GinkgoT().Context(), shaOne, map[string][]string{"Pull-request-id": {"2"}})).To(Succeed())

		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())
		got, err := g.GetHistoryNote(GinkgoT().Context(), shaOne)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(map[string][]string{"Pull-request-id": {"2"}}))
	})

	It("writes from a clone with a stale notes ref without losing the other clone's note", func() {
		gA := newEnvOps("default/writer-a")
		Expect(gA.FetchBranch(GinkgoT().Context(), defaultBranch)).To(Succeed())
		gB := newEnvOps("default/writer-b")
		Expect(gB.FetchBranch(GinkgoT().Context(), defaultBranch)).To(Succeed())

		// A writes first; B's local notes ref (if any) is now stale relative to the remote.
		Expect(gA.SetHistoryNote(GinkgoT().Context(), shaOne, map[string][]string{"Pull-request-id": {"a"}})).To(Succeed())
		Expect(gB.SetHistoryNote(GinkgoT().Context(), shaTwo, map[string][]string{"Pull-request-id": {"b"}})).To(Succeed())

		// Both notes must exist on the remote ref.
		reader := newEnvOps("default/race-reader")
		Expect(reader.FetchBranch(GinkgoT().Context(), defaultBranch)).To(Succeed())
		Expect(reader.FetchNotes(GinkgoT().Context())).To(Succeed())
		noteOne, err := reader.GetHistoryNote(GinkgoT().Context(), shaOne)
		Expect(err).NotTo(HaveOccurred())
		Expect(noteOne).To(Equal(map[string][]string{"Pull-request-id": {"a"}}))
		noteTwo, err := reader.GetHistoryNote(GinkgoT().Context(), shaTwo)
		Expect(err).NotTo(HaveOccurred())
		Expect(noteTwo).To(Equal(map[string][]string{"Pull-request-id": {"b"}}))
	})

	It("FetchNotes tolerates missing notes refs", func() {
		By("with neither notes ref on the remote")
		g := newEnvOps("default/fetch-missing")
		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())

		By("with only the hydrator notes ref on the remote")
		_, err := runGitCmd(workDir, "notes", "--ref="+git.HydratorNotesRef, "add", "-m", `{"drySha":"abc"}`, shaOne)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "origin", git.HydratorNotesRef)
		Expect(err).NotTo(HaveOccurred())
		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())

		By("with both notes refs on the remote")
		_, err = runGitCmd(workDir, "notes", "--ref="+git.PromoterHistoryNotesRef, "add", "-m", `{"Pull-request-id":["1"]}`, shaOne)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "origin", git.PromoterHistoryNotesRef)
		Expect(err).NotTo(HaveOccurred())
		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())

		got, err := g.GetHistoryNote(GinkgoT().Context(), shaOne)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(map[string][]string{"Pull-request-id": {"1"}}))
	})

	It("GetHistoryNote returns nil for missing notes and malformed JSON", func() {
		g := newEnvOps("default/get-nil")
		Expect(g.FetchBranch(GinkgoT().Context(), defaultBranch)).To(Succeed())
		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())

		By("missing note")
		got, err := g.GetHistoryNote(GinkgoT().Context(), shaOne)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNil())

		By("malformed JSON note")
		_, err = runGitCmd(workDir, "notes", "--ref="+git.PromoterHistoryNotesRef, "add", "-m", "not json at all", shaOne)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "origin", git.PromoterHistoryNotesRef)
		Expect(err).NotTo(HaveOccurred())
		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())

		got, err = g.GetHistoryNote(GinkgoT().Context(), shaOne)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNil())
	})

	It("PromotionHistoryNotesMatchSnapshot compares the clone to the snapshot and leaves the snapshot in place", func() {
		ctx := GinkgoT().Context()
		g := newEnvOps("default/notes-match")
		Expect(g.FetchBranch(ctx, defaultBranch)).To(Succeed())

		By("no snapshot has been taken")
		match, err := g.PromotionHistoryNotesMatchSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(match).To(BeFalse())

		By("neither side has a promotion-history notes ref")
		Expect(g.SnapshotRemoteRefs(ctx, defaultBranch)).To(Succeed())
		match, err = g.PromotionHistoryNotesMatchSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(match).To(BeTrue())

		By("the remote gained a note the clone has not fetched")
		_, err = runGitCmd(workDir, "notes", "--ref="+git.PromoterHistoryNotesRef, "add", "-m", `{"Pull-request-id":["1"]}`, shaOne)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "origin", git.PromoterHistoryNotesRef)
		Expect(err).NotTo(HaveOccurred())
		Expect(g.SnapshotRemoteRefs(ctx, defaultBranch)).To(Succeed())
		match, err = g.PromotionHistoryNotesMatchSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(match).To(BeFalse())

		By("fetching the note makes the clone match, and the snapshot is still there for FetchNotes")
		Expect(g.FetchNotes(ctx)).To(Succeed())
		Expect(g.SnapshotRemoteRefs(ctx, defaultBranch)).To(Succeed())
		match, err = g.PromotionHistoryNotesMatchSnapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(match).To(BeTrue())
		_, err = runGitCmd(g.ClonePath(), "remote", "set-url", "origin", filepath.Join(tempRepoDir, "does-not-exist"))
		Expect(err).NotTo(HaveOccurred())
		Expect(g.FetchNotes(ctx)).To(Succeed())
	})

	It("FetchNotes only fetches notes refs that moved on the remote", func() {
		ctx := GinkgoT().Context()
		g := newEnvOps("default/fetch-skip")

		_, err := runGitCmd(workDir, "notes", "--ref="+git.HydratorNotesRef, "add", "-m", `{"drySha":"abc"}`, shaOne)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "origin", git.HydratorNotesRef)
		Expect(err).NotTo(HaveOccurred())
		Expect(g.FetchNotes(ctx)).To(Succeed())

		By("breaking the clone's origin so any real fetch fails, while ls-remote still reaches the remote through gap")
		_, err = runGitCmd(g.ClonePath(), "remote", "set-url", "origin", filepath.Join(tempRepoDir, "does-not-exist"))
		Expect(err).NotTo(HaveOccurred())

		By("nothing moved: no fetch is attempted, from a fresh probe or from a snapshot")
		Expect(g.FetchNotes(ctx)).To(Succeed())
		Expect(g.SnapshotRemoteRefs(ctx, defaultBranch)).To(Succeed())
		Expect(g.FetchNotes(ctx)).To(Succeed())

		By("a notes ref that moved on the remote is fetched")
		_, err = runGitCmd(workDir, "notes", "--ref="+git.HydratorNotesRef, "add", "-m", `{"drySha":"def"}`, shaTwo)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "origin", git.HydratorNotesRef)
		Expect(err).NotTo(HaveOccurred())
		Expect(g.SnapshotRemoteRefs(ctx, defaultBranch)).To(Succeed())
		Expect(g.FetchNotes(ctx)).To(MatchError(ContainSubstring("failed to fetch git notes")))
	})

	It("LoadHistoryNotes serves GetHistoryNote from one batch", func() {
		ctx := GinkgoT().Context()
		_, err := runGitCmd(workDir, "notes", "--ref="+git.PromoterHistoryNotesRef, "add", "-m", `{"Pull-request-id":["1"]}`, shaOne)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "origin", git.PromoterHistoryNotesRef)
		Expect(err).NotTo(HaveOccurred())

		g := newEnvOps("default/load-notes")
		Expect(g.FetchBranch(ctx, defaultBranch)).To(Succeed())
		Expect(g.FetchNotes(ctx)).To(Succeed())
		Expect(g.LoadHistoryNotes(ctx, shaOne, strings.ToUpper(shaTwo), shaOne)).To(Succeed())

		By("deleting the local notes ref so only the cache can answer")
		_, err = runGitCmd(g.ClonePath(), "update-ref", "-d", git.PromoterHistoryNotesRef)
		Expect(err).NotTo(HaveOccurred())
		got, err := g.GetHistoryNote(ctx, shaOne)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(Equal(map[string][]string{"Pull-request-id": {"1"}}))
		got, err = g.GetHistoryNote(ctx, shaTwo)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNil())
	})

	It("LoadHistoryNotes tolerates a commit missing from the clone", func() {
		ctx := GinkgoT().Context()
		g := newEnvOps("default/load-notes-missing")
		Expect(g.FetchBranch(ctx, defaultBranch)).To(Succeed())
		Expect(g.FetchNotes(ctx)).To(Succeed())
		Expect(g.LoadHistoryNotes(ctx, shaOne, strings.Repeat("a", 40))).To(Succeed())
		got, err := g.GetHistoryNote(ctx, shaOne)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNil())
	})

	It("SetHistoryNote writes the note", func() {
		g := newEnvOps("default/invariant")
		Expect(g.FetchBranch(GinkgoT().Context(), defaultBranch)).To(Succeed())
		Expect(g.SetHistoryNote(GinkgoT().Context(), shaOne, map[string][]string{"Pull-request-id": {"1"}})).To(Succeed())
		got, err := g.GetHistoryNote(GinkgoT().Context(), shaOne)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(HaveKeyWithValue("Pull-request-id", []string{"1"}))
	})

	It("FetchNotes drops a note whose push failed before the remote had a notes ref", func() {
		ctx := GinkgoT().Context()
		g := newEnvOps("default/failed-first-push")
		Expect(g.FetchBranch(ctx, defaultBranch)).To(Succeed())

		hook := filepath.Join(tempRepoDir, "hooks", "pre-receive")
		Expect(os.WriteFile(hook, []byte("#!/bin/sh\nexit 1\n"), 0o755)).To(Succeed())
		Expect(g.SetHistoryNote(ctx, shaOne, map[string][]string{"Pull-request-id": {"1"}})).NotTo(Succeed())
		Expect(os.Remove(hook)).To(Succeed())

		Expect(g.FetchNotes(ctx)).To(Succeed())
		got, err := g.GetHistoryNote(ctx, shaOne)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNil(), "the note never reached the remote, so it must not read as written")

		By("taking a snapshot first, as the reconcilers do")
		Expect(g.SetHistoryNote(ctx, shaTwo, map[string][]string{"Pull-request-id": {"2"}})).To(Succeed())
		_, err = runGitCmd(tempRepoDir, "update-ref", "-d", git.PromoterHistoryNotesRef)
		Expect(err).NotTo(HaveOccurred())
		Expect(g.SnapshotRemoteRefs(ctx, defaultBranch)).To(Succeed())
		Expect(g.FetchNotes(ctx)).To(Succeed())
		got, err = g.GetHistoryNote(ctx, shaTwo)
		Expect(err).NotTo(HaveOccurred())
		Expect(got).To(BeNil())
	})

	It("supports GetCommitParents and CommitExists for merge commits", func() {
		// Build: base -> feature branch -> merge commit.
		_, err := runGitCmd(workDir, "checkout", "-b", "feature")
		Expect(err).NotTo(HaveOccurred())
		featureSha := commit("feature.txt", "feature", "feature commit")
		_, err = runGitCmd(workDir, "checkout", defaultBranch)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "merge", "--no-ff", "-m", "merge feature", "feature")
		Expect(err).NotTo(HaveOccurred())
		mergeSha := strings.TrimSpace(mustGit(workDir, "rev-parse", "HEAD"))
		_, err = runGitCmd(workDir, "push", "origin", defaultBranch, "feature")
		Expect(err).NotTo(HaveOccurred())

		g := newEnvOps("default/helpers")
		Expect(g.FetchBranch(GinkgoT().Context(), defaultBranch)).To(Succeed())
		Expect(g.FetchBranch(GinkgoT().Context(), "feature")).To(Succeed())

		parents, err := g.GetCommitParents(GinkgoT().Context(), mergeSha)
		Expect(err).NotTo(HaveOccurred())
		Expect(parents).To(HaveLen(2))
		Expect(parents[0]).To(Equal(shaTwo))
		Expect(parents[1]).To(Equal(featureSha))

		Expect(g.CommitExists(GinkgoT().Context(), mergeSha)).To(BeTrue())
		Expect(g.CommitExists(GinkgoT().Context(), strings.Repeat("0", 40))).To(BeFalse())
	})
})

var _ = Describe("Qualification notes", func() {
	var (
		ctx     context.Context
		bareDir string
		g       *git.EnvironmentOperations
		shas    map[string]string
	)

	// otherClone returns a second clone of the same repository, as another controller replica or another
	// gate sharing the repository would have.
	otherClone := func(identity string) *git.EnvironmentOperations {
		GinkgoHelper()
		repo := &v1alpha1.GitRepository{
			Name: "testrepo", Namespace: "default",
			Spec: v1alpha1.GitRepositorySpec{
				GitHub:         &v1alpha1.GitHubRepo{Owner: "test-owner", Name: "testrepo"},
				ScmProviderRef: v1alpha1.ScmProviderObjectReference{Kind: "ScmProvider", Name: "testprovider"},
			},
		}
		other := git.NewEnvironmentOperations(repo, &fakeGitProvider{tempDirPath: bareDir}, identity)
		Expect(other.CloneRepo(ctx)).To(Succeed())
		Expect(other.FetchBranch(ctx, "main")).To(Succeed())
		return other
	}
	entry := func(drySha string) git.QualifiedNoteEntry {
		return git.QualifiedNoteEntry{
			DrySha:      drySha,
			QualifiedAt: metav1.NewTime(time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)),
			CommitStatuses: []git.QualifiedNoteCommitStatus{
				{Key: "argocd-health", Phase: "success"},
			},
		}
	}

	BeforeEach(func() {
		ctx = context.Background()
		bareDir, _, g, shas = newTagTestRepo()
	})

	It("reports no note for a commit that has none", func() {
		Expect(g.FetchQualifiedNotes(ctx)).To(Succeed())
		note, err := g.GetQualifiedNote(ctx, shas["c1"])
		Expect(err).NotTo(HaveOccurred())
		Expect(note).To(BeNil())
	})

	It("writes an entry that another clone can read", func() {
		Expect(g.SetQualifiedNoteEntry(ctx, shas["c2"], "environment/dev", entry("dry-1"))).To(Succeed())

		other := otherClone("default/testrepo-reader")
		Expect(other.FetchQualifiedNotes(ctx)).To(Succeed())
		note, err := other.GetQualifiedNote(ctx, shas["c2"])
		Expect(err).NotTo(HaveOccurred())
		Expect(note).NotTo(BeNil())
		Expect(note.Environments).To(HaveKey("environment/dev"))
		got := note.Environments["environment/dev"]
		Expect(got.DrySha).To(Equal("dry-1"))
		Expect(got.QualifiedAt.Time).To(BeTemporally("==", entry("").QualifiedAt.Time))
		Expect(got.CommitStatuses).To(Equal(entry("").CommitStatuses))
	})

	It("keeps other environments' entries on a shared commit", func() {
		// Environments that share an active branch (activePath) share its commits.
		other := otherClone("default/testrepo-other-gate")
		Expect(g.SetQualifiedNoteEntry(ctx, shas["c2"], "environments:apps/one", entry("dry-1"))).To(Succeed())
		Expect(other.SetQualifiedNoteEntry(ctx, shas["c2"], "environments:apps/two", entry("dry-2"))).To(Succeed())

		Expect(g.FetchQualifiedNotes(ctx)).To(Succeed())
		note, err := g.GetQualifiedNote(ctx, shas["c2"])
		Expect(err).NotTo(HaveOccurred())
		Expect(note.Environments).To(HaveLen(2))
		Expect(note.Environments["environments:apps/one"].DrySha).To(Equal("dry-1"))
		Expect(note.Environments["environments:apps/two"].DrySha).To(Equal("dry-2"))
	})

	It("replaces an environment's own entry", func() {
		Expect(g.SetQualifiedNoteEntry(ctx, shas["c2"], "environment/dev", entry("dry-1"))).To(Succeed())
		Expect(g.SetQualifiedNoteEntry(ctx, shas["c2"], "environment/dev", entry("dry-2"))).To(Succeed())
		note, err := g.GetQualifiedNote(ctx, shas["c2"])
		Expect(err).NotTo(HaveOccurred())
		Expect(note.Environments).To(HaveLen(1))
		Expect(note.Environments["environment/dev"].DrySha).To(Equal("dry-2"))
	})

	It("loses no entry when several clones write at the same time", func() {
		keys := []string{"environments:apps/one", "environments:apps/two", "environments:apps/three"}
		clones := make([]*git.EnvironmentOperations, len(keys))
		for i := range keys {
			clones[i] = otherClone("default/testrepo-writer-" + keys[i])
		}
		errs := make([]error, len(keys))
		var wg sync.WaitGroup
		for i, key := range keys {
			wg.Go(func() {
				errs[i] = clones[i].SetQualifiedNoteEntry(ctx, shas["c2"], key, entry("dry-"+key))
			})
		}
		wg.Wait()
		for _, err := range errs {
			Expect(err).NotTo(HaveOccurred())
		}

		Expect(g.FetchQualifiedNotes(ctx)).To(Succeed())
		note, err := g.GetQualifiedNote(ctx, shas["c2"])
		Expect(err).NotTo(HaveOccurred())
		Expect(note.Environments).To(HaveLen(len(keys)))
	})

	It("ignores a note that is not valid JSON", func() {
		Expect(g.SetQualifiedNoteEntry(ctx, shas["c1"], "environment/dev", entry("dry-1"))).To(Succeed())
		_, err := runGitCmd(bareDir, "-c", "user.name=t", "-c", "user.email=t@example.com",
			"notes", "--ref="+git.PromoterQualifiedNotesRef, "add", "-f", "-m", "not json", shas["c1"])
		Expect(err).NotTo(HaveOccurred())
		Expect(g.FetchQualifiedNotes(ctx)).To(Succeed())
		note, err := g.GetQualifiedNote(ctx, shas["c1"])
		Expect(err).NotTo(HaveOccurred())
		Expect(note).To(BeNil())
	})

	Describe("FindNewestQualifiedNoteEntry", func() {
		It("finds nothing when no commit on the branch has an entry for the environment", func() {
			Expect(g.SetQualifiedNoteEntry(ctx, shas["c2"], "environment/other", entry("dry-1"))).To(Succeed())
			found, sha, err := g.FindNewestQualifiedNoteEntry(ctx, shas["c3"], "environment/dev", 50)
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeNil())
			Expect(sha).To(BeEmpty())
		})

		It("finds the entry on the newest commit that has one", func() {
			Expect(g.SetQualifiedNoteEntry(ctx, shas["c1"], "environment/dev", entry("dry-1"))).To(Succeed())
			Expect(g.SetQualifiedNoteEntry(ctx, shas["c2"], "environment/dev", entry("dry-2"))).To(Succeed())
			found, sha, err := g.FindNewestQualifiedNoteEntry(ctx, shas["c3"], "environment/dev", 50)
			Expect(err).NotTo(HaveOccurred())
			Expect(found).NotTo(BeNil())
			Expect(found.DrySha).To(Equal("dry-2"))
			Expect(sha).To(Equal(shas["c2"]))
		})

		It("does not look further back than the limit", func() {
			Expect(g.SetQualifiedNoteEntry(ctx, shas["c1"], "environment/dev", entry("dry-1"))).To(Succeed())
			found, _, err := g.FindNewestQualifiedNoteEntry(ctx, shas["c3"], "environment/dev", 2)
			Expect(err).NotTo(HaveOccurred())
			Expect(found).To(BeNil())
		})
	})
})
