package git_test

import (
	"os"
	"path/filepath"
	"strings"

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

	It("restores the active tree, copies the history note, and leaves proposed in place", func() {
		v1 := commitFile("version.txt", "v1\n", "version v1")
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

		Expect(g.FetchNotes(GinkgoT().Context())).To(Succeed())
		got, err := g.GetHistoryNote(GinkgoT().Context(), restored.ActiveSha)
		Expect(err).NotTo(HaveOccurred())
		Expect(got[constants.TrailerRestoredFrom]).To(Equal([]string{v1}))
		Expect(got[constants.TrailerPullRequestID]).To(Equal([]string{"9"}))
		Expect(got[constants.TrailerPullRequestMergeTime]).NotTo(Equal([]string{"2020-01-01T00:00:00Z"}))

		again, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", v1)
		Expect(err).NotTo(HaveOccurred())
		Expect(again).To(Equal(restored))
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

	It("fetches a target commit that is not in the clone yet", func() {
		v1 := commitFile("version.txt", "v1\n", "version v1")
		mustGit(workDir, "branch", "-M", "environment/development")
		mustGit(workDir, "push", "-u", "origin", "environment/development")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/environment/development-next")

		g := newOps()

		// Pushed after the clone, to a ref the restore does not fetch.
		mustGit(workDir, "checkout", "-b", "elsewhere")
		target := commitFile("version.txt", "elsewhere\n", "elsewhere")
		mustGit(workDir, "push", "origin", "HEAD:refs/heads/elsewhere")
		mustGit(workDir, "checkout", "environment/development")

		restored, err := g.RestoreActiveBranch(GinkgoT().Context(), "environment/development", "", target)
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "rev-parse", restored.ActiveSha+"^"))).To(Equal(v1))
		Expect(strings.TrimSpace(mustGit(tempRepoDir, "show", restored.ActiveSha+":version.txt"))).To(Equal("elsewhere"))
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
})
