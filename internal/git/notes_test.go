package git_test

import (
	"os"
	"path/filepath"
	"strings"

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
			ObjectMeta: metav1.ObjectMeta{Name: "testrepo", Namespace: "default"},
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

	It("SetHistoryNote leaves the clone's worktree/index/HEAD untouched", func() {
		g := newEnvOps("default/invariant")
		Expect(g.FetchBranch(GinkgoT().Context(), defaultBranch)).To(Succeed())

		headBefore, statusBefore := cloneHeadAndStatus(g.ClonePath())
		Expect(g.SetHistoryNote(GinkgoT().Context(), shaOne, map[string][]string{"Pull-request-id": {"1"}})).To(Succeed())
		headAfter, statusAfter := cloneHeadAndStatus(g.ClonePath())
		Expect(headAfter).To(Equal(headBefore), "SetHistoryNote must not move HEAD")
		Expect(statusAfter).To(Equal(statusBefore), "SetHistoryNote must not dirty the worktree/index")
	})

	It("supports the parent/ancestor helpers used for merge-commit location", func() {
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

		isAncestor, err := g.IsAncestor(GinkgoT().Context(), featureSha, mergeSha)
		Expect(err).NotTo(HaveOccurred())
		Expect(isAncestor).To(BeTrue())

		isAncestor, err = g.IsAncestor(GinkgoT().Context(), mergeSha, featureSha)
		Expect(err).NotTo(HaveOccurred())
		Expect(isAncestor).To(BeFalse())

		Expect(g.CommitExists(GinkgoT().Context(), mergeSha)).To(BeTrue())
		Expect(g.CommitExists(GinkgoT().Context(), strings.Repeat("0", 40))).To(BeFalse())
	})
})
