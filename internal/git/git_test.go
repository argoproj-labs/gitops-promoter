package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
)

func TestGit(t *testing.T) {
	// Do not use t.Parallel() here: the top-level test is already run in parallel by
	// make test-parallel (ginkgo -p -procs=4). Using t.Parallel() can trigger a nil
	// RPC client in Ginkgo's parallel support when the suite exits.
	RegisterFailHandler(Fail)
	RunSpecs(t, "Git Suite")
}

// Helper function to run git commands
func runGitCmd(dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	return string(output), err
}

var _ = Describe("GetBranchShas", func() {
	var tempRepoDir string

	BeforeEach(func() {
		// Create a temporary directory for the test repository
		var err error
		tempRepoDir, err = os.MkdirTemp("", "git-test-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		if tempRepoDir != "" {
			Expect(os.RemoveAll(tempRepoDir)).To(Succeed())
		}
	})

	Context("When the branch does not exist on the remote", func() {
		It("should provide a clear error message from GetBranchShas", func() {
			By("Setting up a bare git repository")
			_, err := runGitCmd(tempRepoDir, "init", "--bare")
			Expect(err).NotTo(HaveOccurred())

			By("Creating an initial commit")
			workDir, err := os.MkdirTemp("", "git-work-*")
			Expect(err).NotTo(HaveOccurred())
			defer func() {
				Expect(os.RemoveAll(workDir)).To(Succeed())
			}()

			_, err = runGitCmd(workDir, "clone", tempRepoDir, ".")
			Expect(err).NotTo(HaveOccurred())

			_, err = runGitCmd(workDir, "config", "user.name", "Test User")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "config", "user.email", "test@example.com")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "config", "commit.gpgsign", "false")
			Expect(err).NotTo(HaveOccurred())

			err = os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), []byte(`{"drySha": "abc123"}`), 0o644)
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "add", "hydrator.metadata")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "commit", "-m", "Initial commit")
			Expect(err).NotTo(HaveOccurred())

			defaultBranch, err := runGitCmd(workDir, "rev-parse", "--abbrev-ref", "HEAD")
			Expect(err).NotTo(HaveOccurred())
			defaultBranch = strings.TrimSpace(defaultBranch)

			_, err = runGitCmd(workDir, "push", "origin", defaultBranch)
			Expect(err).NotTo(HaveOccurred())

			// Prepare EnvironmentOperations
			repo := &v1alpha1.GitRepository{
				Spec: v1alpha1.GitRepositorySpec{
					GitHub: &v1alpha1.GitHubRepo{
						Owner: "test-owner",
						Name:  "testrepo",
					},
					ScmProviderRef: v1alpha1.ScmProviderObjectReference{
						Kind: "ScmProvider",
						Name: "testprovider",
					},
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testrepo",
					Namespace: "default",
				},
			}
			gap := &fakeGitProvider{tempDirPath: tempRepoDir}
			g := git.NewEnvironmentOperations(repo, gap, defaultBranch)
			Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())

			// Call GetBranchShas with a non-existent branch
			_, err = g.GetBranchShas(GinkgoT().Context(), "environments/qal-usw2-eks-next")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to fetch branch"))

			// Having a missing branch is a common error, so we're ensuring the error message is clear.
			Expect(err.Error()).To(ContainSubstring("couldn't find remote ref"))
		})
	})
})

var _ = Describe("LsRemote", func() {
	var tempRepoDir string
	var workDir string

	BeforeEach(func() {
		// Create a temporary directory for the test repository
		var err error
		tempRepoDir, err = os.MkdirTemp("", "git-test-*")
		Expect(err).NotTo(HaveOccurred())

		By("Setting up a bare git repository")
		_, err = runGitCmd(tempRepoDir, "init", "--bare")
		Expect(err).NotTo(HaveOccurred())

		By("Creating a working directory with initial commit")
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

		err = os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Test Repo"), 0o644)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "add", "README.md")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "Initial commit")
		Expect(err).NotTo(HaveOccurred())

		defaultBranch, err := runGitCmd(workDir, "rev-parse", "--abbrev-ref", "HEAD")
		Expect(err).NotTo(HaveOccurred())
		defaultBranch = strings.TrimSpace(defaultBranch)

		_, err = runGitCmd(workDir, "push", "origin", defaultBranch)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		if tempRepoDir != "" {
			Expect(os.RemoveAll(tempRepoDir)).To(Succeed())
		}
		if workDir != "" {
			Expect(os.RemoveAll(workDir)).To(Succeed())
		}
	})

	Context("When some branches are missing", func() {
		It("should provide a clear error message indicating which branches don't exist", func() {
			By("Creating only development and staging branches")
			_, err := runGitCmd(workDir, "checkout", "-b", "environment/development")
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(workDir, "dev.txt"), []byte("dev"), 0o644)
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "add", "dev.txt")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "commit", "-m", "Dev commit")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "push", "origin", "environment/development")
			Expect(err).NotTo(HaveOccurred())

			_, err = runGitCmd(workDir, "checkout", "-b", "environment/staging")
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(workDir, "staging.txt"), []byte("staging"), 0o644)
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "add", "staging.txt")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "commit", "-m", "Staging commit")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "push", "origin", "environment/staging")
			Expect(err).NotTo(HaveOccurred())

			By("Calling LsRemote with development, staging, and prod branches (prod doesn't exist)")
			repo := &v1alpha1.GitRepository{
				Spec: v1alpha1.GitRepositorySpec{
					GitHub: &v1alpha1.GitHubRepo{
						Owner: "test-owner",
						Name:  "testrepo",
					},
					ScmProviderRef: v1alpha1.ScmProviderObjectReference{
						Kind: "ScmProvider",
						Name: "testprovider",
					},
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testrepo",
					Namespace: "default",
				},
			}
			gap := &fakeGitProvider{tempDirPath: tempRepoDir}

			_, err = git.LsRemote(
				context.Background(),
				gap,
				repo,
				"environment/development",
				"environment/prod",
				"environment/staging",
			)
			Expect(err).To(HaveOccurred())

			By("Verifying the error message is helpful")
			Expect(err.Error()).To(ContainSubstring("missing branches: [environment/prod]"))
			Expect(err.Error()).To(ContainSubstring("(these branches may not exist yet"))
			Expect(err.Error()).To(ContainSubstring("check your PromotionStrategy"))
		})
	})

	Context("When multiple branches are missing", func() {
		It("should list all missing branches in the error message", func() {
			By("Creating only the development branch")
			_, err := runGitCmd(workDir, "checkout", "-b", "environment/development")
			Expect(err).NotTo(HaveOccurred())
			err = os.WriteFile(filepath.Join(workDir, "dev.txt"), []byte("dev"), 0o644)
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "add", "dev.txt")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "commit", "-m", "Dev commit")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "push", "origin", "environment/development")
			Expect(err).NotTo(HaveOccurred())

			By("Calling LsRemote with development, staging, and prod branches")
			repo := &v1alpha1.GitRepository{
				Spec: v1alpha1.GitRepositorySpec{
					GitHub: &v1alpha1.GitHubRepo{
						Owner: "test-owner",
						Name:  "testrepo",
					},
					ScmProviderRef: v1alpha1.ScmProviderObjectReference{
						Kind: "ScmProvider",
						Name: "testprovider",
					},
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testrepo",
					Namespace: "default",
				},
			}
			gap := &fakeGitProvider{tempDirPath: tempRepoDir}

			_, err = git.LsRemote(
				context.Background(),
				gap,
				repo,
				"environment/development",
				"environment/prod",
				"environment/staging",
			)
			Expect(err).To(HaveOccurred())

			By("Verifying all missing branches are listed")
			Expect(err.Error()).To(ContainSubstring("missing branches:"))
			Expect(err.Error()).To(ContainSubstring("environment/prod"))
			Expect(err.Error()).To(ContainSubstring("environment/staging"))
		})
	})
})

var _ = Describe("GetShaMetadataFromFile", func() {
	var tempRepoDir string
	var workDir string
	var defaultBranch string
	ctx := context.Background()

	BeforeEach(func() {
		var err error
		tempRepoDir, err = os.MkdirTemp("", "git-metadata-test-bare-*")
		Expect(err).NotTo(HaveOccurred())

		_, err = runGitCmd(tempRepoDir, "init", "--bare")
		Expect(err).NotTo(HaveOccurred())

		workDir, err = os.MkdirTemp("", "git-metadata-test-work-*")
		Expect(err).NotTo(HaveOccurred())

		_, err = runGitCmd(workDir, "clone", tempRepoDir, ".")
		Expect(err).NotTo(HaveOccurred())

		_, err = runGitCmd(workDir, "config", "user.name", "Test User")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "config", "user.email", "test@example.com")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "config", "commit.gpgsign", "false")
		Expect(err).NotTo(HaveOccurred())

		err = os.WriteFile(filepath.Join(workDir, "README.md"), []byte("# Test"), 0o644)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "add", "README.md")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "Initial commit")
		Expect(err).NotTo(HaveOccurred())

		defaultBranch, err = runGitCmd(workDir, "rev-parse", "--abbrev-ref", "HEAD")
		Expect(err).NotTo(HaveOccurred())
		defaultBranch = strings.TrimSpace(defaultBranch)
		_, err = runGitCmd(workDir, "push", "-u", "origin", defaultBranch)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		if tempRepoDir != "" {
			Expect(os.RemoveAll(tempRepoDir)).To(Succeed())
		}
		if workDir != "" {
			Expect(os.RemoveAll(workDir)).To(Succeed())
		}
	})

	Context("when hydrator.metadata contains references that fail API validation", func() {
		It("clears only the invalid fields (Sha, RepoURL) and keeps the reference so status update does not fail", func() {
			// Bad data that would fail CommitMetadata CRD validation: invalid Sha (not 40/64 hex), invalid RepoURL (not http(s)).
			badMetadata := []byte(`{
	"drySha": "c4c862564afe56abf8cc8ac683eee3dc8bf96108",
	"references": [
		{
			"commit": {
				"sha": "not-a-valid-sha",
				"repoURL": "ftp://invalid.example.com/repo",
				"subject": "Bad ref"
			}
		}
	]}`)
			err := os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), badMetadata, 0o644)
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "add", "hydrator.metadata")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "commit", "-m", "Add bad hydrator metadata")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "push", "origin", defaultBranch)
			Expect(err).NotTo(HaveOccurred())

			headSha, err := runGitCmd(workDir, "rev-parse", "HEAD")
			Expect(err).NotTo(HaveOccurred())
			headSha = strings.TrimSpace(headSha)

			repo := &v1alpha1.GitRepository{
				Spec: v1alpha1.GitRepositorySpec{
					GitHub: &v1alpha1.GitHubRepo{
						Owner: "test-owner",
						Name:  "testrepo",
					},
					ScmProviderRef: v1alpha1.ScmProviderObjectReference{
						Kind: "ScmProvider",
						Name: "testprovider",
					},
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testrepo",
					Namespace: "default",
				},
			}
			gap := &fakeGitProvider{tempDirPath: tempRepoDir}
			g := git.NewEnvironmentOperations(repo, gap, defaultBranch)
			Expect(g.CloneRepo(ctx)).To(Succeed())

			state, err := g.GetShaMetadataFromFile(ctx, headSha)
			Expect(err).NotTo(HaveOccurred())

			// Invalid fields are cleared; the reference item is kept so status patch succeeds.
			Expect(state.References).To(HaveLen(1))
			Expect(state.References[0].Commit).ToNot(BeNil())
			Expect(state.References[0].Commit.Sha).To(BeEmpty())
			Expect(state.References[0].Commit.RepoURL).To(BeEmpty())
			Expect(state.References[0].Commit.Subject).To(Equal("Bad ref"))
		})
	})

	Context("when hydrator.metadata contains valid references", func() {
		It("preserves valid references in the returned CommitShaState", func() {
			validMetadata := []byte(`{
	"drySha": "c4c862564afe56abf8cc8ac683eee3dc8bf96108",
	"references": [
		{
			"commit": {
				"sha": "c4c862564afe56abf8cc8ac683eee3dc8bf96108",
				"repoURL": "https://github.com/upstream/repo",
				"subject": "Valid ref"
			}
		}
	]}`)
			err := os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), validMetadata, 0o644)
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "add", "hydrator.metadata")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "commit", "-m", "Add valid hydrator metadata")
			Expect(err).NotTo(HaveOccurred())
			_, err = runGitCmd(workDir, "push", "origin", defaultBranch)
			Expect(err).NotTo(HaveOccurred())

			headSha, err := runGitCmd(workDir, "rev-parse", "HEAD")
			Expect(err).NotTo(HaveOccurred())
			headSha = strings.TrimSpace(headSha)

			repo := &v1alpha1.GitRepository{
				Spec: v1alpha1.GitRepositorySpec{
					GitHub: &v1alpha1.GitHubRepo{
						Owner: "test-owner",
						Name:  "testrepo",
					},
					ScmProviderRef: v1alpha1.ScmProviderObjectReference{
						Kind: "ScmProvider",
						Name: "testprovider",
					},
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "testrepo",
					Namespace: "default",
				},
			}
			gap := &fakeGitProvider{tempDirPath: tempRepoDir}
			g := git.NewEnvironmentOperations(repo, gap, defaultBranch)
			Expect(g.CloneRepo(ctx)).To(Succeed())

			state, err := g.GetShaMetadataFromFile(ctx, headSha)
			Expect(err).NotTo(HaveOccurred())

			Expect(state.References).To(HaveLen(1))
			Expect(state.References[0].Commit).NotTo(BeNil())
			Expect(state.References[0].Commit.Sha).To(Equal("c4c862564afe56abf8cc8ac683eee3dc8bf96108"))
			Expect(state.References[0].Commit.RepoURL).To(Equal("https://github.com/upstream/repo"))
		})
	})
})

var _ = Describe("sanitizeReferences", func() {
	ctx := context.Background()

	// sha40 and sha64 are valid test SHAs (40 and 64 lowercase hex chars).
	const sha40 = "c4c862564bcd56bb78cc8bc683eee3dc8bf96108"
	const sha64 = "c4c862564bcd56bb78cc8ac683eee3dc8bf96108c4c862564bcd56bb78cc8bc6"

	DescribeTable("Sha validation",
		func(sha string, expectCleared bool) {
			refs := []v1alpha1.RevisionReference{
				{
					Commit: &v1alpha1.CommitMetadata{
						Sha:     sha,
						RepoURL: "https://github.com/example/repo",
					},
				},
			}
			result := git.SanitizeReferences(ctx, refs)
			Expect(result).To(HaveLen(1))
			Expect(result[0].Commit).NotTo(BeNil())
			if expectCleared {
				Expect(result[0].Commit.Sha).To(BeEmpty(), "expected Sha to be cleared for value %q", sha)
			} else {
				Expect(result[0].Commit.Sha).To(Equal(sha), "expected Sha to be preserved for value %q", sha)
			}
		},
		Entry("valid SHA-1 (40 hex chars) is preserved", sha40, false),
		Entry("valid SHA-256 (64 hex chars) is preserved", sha64, false),
		Entry("invalid: too short is cleared", "abc123", true),
		Entry("invalid: uppercase hex is cleared", "C4C862564BCD56BB78CC8BC683EEE3DC8BF96108", true),
		Entry("invalid: non-hex chars is cleared", "not-a-valid-sha-at-all-xxxxxxxxxxxxxxxxxxxx", true),
		Entry("invalid: 39 hex chars is cleared", "c4c862564bcd56bb78cc8bc683eee3dc8bf9610", true),
		Entry("invalid: 41 hex chars is cleared", "c4c862564bcd56bb78cc8bc683eee3dc8bf961080", true),
		Entry("empty Sha is preserved as-is (no change)", "", false),
	)

	DescribeTable("RepoURL validation",
		func(repoURL string, expectCleared bool) {
			refs := []v1alpha1.RevisionReference{
				{
					Commit: &v1alpha1.CommitMetadata{
						Sha:     sha40,
						RepoURL: repoURL,
					},
				},
			}
			result := git.SanitizeReferences(ctx, refs)
			Expect(result).To(HaveLen(1))
			Expect(result[0].Commit).NotTo(BeNil())
			if expectCleared {
				Expect(result[0].Commit.RepoURL).To(BeEmpty(), "expected RepoURL to be cleared for value %q", repoURL)
			} else {
				Expect(result[0].Commit.RepoURL).To(Equal(repoURL), "expected RepoURL to be preserved for value %q", repoURL)
			}
		},
		Entry("valid https URL is preserved", "https://github.com/example/repo", false),
		Entry("valid http URL is preserved", "http://github.example.com/example/repo", false),
		Entry("empty RepoURL is preserved as-is (no change)", "", false),
		Entry("ftp URL is cleared", "ftp://invalid.example.com/repo", true),
		Entry("ssh URL is cleared", "ssh://git@github.com/example/repo", true),
		Entry("git protocol URL is cleared", "git://github.com/example/repo", true),
		Entry("relative path is cleared", "example/repo", true),
		Entry("bare hostname without scheme is cleared", "github.com/example/repo", true),
		Entry("https URL with no path is preserved", "https://", false),
	)

	Context("when refs slice is nil", func() {
		It("returns nil without panicking", func() {
			result := git.SanitizeReferences(ctx, nil)
			Expect(result).To(BeNil())
		})
	})

	Context("when refs slice is empty", func() {
		It("returns an empty slice", func() {
			result := git.SanitizeReferences(ctx, []v1alpha1.RevisionReference{})
			Expect(result).To(BeEmpty())
		})
	})

	Context("when a reference has a nil Commit", func() {
		It("skips it without panicking and returns it unchanged", func() {
			refs := []v1alpha1.RevisionReference{
				{Commit: nil},
			}
			result := git.SanitizeReferences(ctx, refs)
			Expect(result).To(HaveLen(1))
			Expect(result[0].Commit).To(BeNil())
		})
	})

	Context("when a reference has both invalid Sha and invalid RepoURL", func() {
		It("clears both fields but keeps the reference item with other fields intact", func() {
			refs := []v1alpha1.RevisionReference{
				{
					Commit: &v1alpha1.CommitMetadata{
						Sha:     "not-a-valid-sha",
						RepoURL: "ftp://bad.example.com",
						Subject: "Keep me",
						Author:  "Someone <someone@example.com>",
						Body:    "Keep body too",
						Date:    func() *metav1.Time { t := metav1.Now(); return &t }(),
					},
				},
			}
			result := git.SanitizeReferences(ctx, refs)
			Expect(result).To(HaveLen(1))
			c := result[0].Commit
			Expect(c).NotTo(BeNil())
			Expect(c.Sha).To(BeEmpty())
			Expect(c.RepoURL).To(BeEmpty())
			Expect(c.Subject).To(Equal("Keep me"))
			Expect(c.Author).To(Equal("Someone <someone@example.com>"))
			Expect(c.Body).To(Equal("Keep body too"))
			Expect(c.Date).NotTo(BeNil())
		})
	})

	Context("when multiple references are present with mixed validity", func() {
		It("sanitizes only invalid entries and preserves valid ones", func() {
			refs := []v1alpha1.RevisionReference{
				{
					Commit: &v1alpha1.CommitMetadata{
						Sha:     sha40,
						RepoURL: "https://github.com/valid/repo",
						Subject: "Valid entry",
					},
				},
				{
					Commit: &v1alpha1.CommitMetadata{
						Sha:     "bad-sha",
						RepoURL: "ftp://bad.example.com",
						Subject: "Invalid entry",
					},
				},
				{Commit: nil},
			}
			result := git.SanitizeReferences(ctx, refs)
			Expect(result).To(HaveLen(3))

			// First entry: valid - both fields preserved.
			Expect(result[0].Commit.Sha).To(Equal(sha40))
			Expect(result[0].Commit.RepoURL).To(Equal("https://github.com/valid/repo"))
			Expect(result[0].Commit.Subject).To(Equal("Valid entry"))

			// Second entry: both fields cleared, subject kept.
			Expect(result[1].Commit.Sha).To(BeEmpty())
			Expect(result[1].Commit.RepoURL).To(BeEmpty())
			Expect(result[1].Commit.Subject).To(Equal("Invalid entry"))

			// Third entry: nil Commit unchanged.
			Expect(result[2].Commit).To(BeNil())
		})
	})

	Context("when Sha is longer than 64 characters", func() {
		It("clears the overlong Sha without panicking (truncation for logging only)", func() {
			overlongSha := sha64 + "extra"
			refs := []v1alpha1.RevisionReference{
				{
					Commit: &v1alpha1.CommitMetadata{
						Sha:     overlongSha,
						RepoURL: "https://github.com/example/repo",
					},
				},
			}
			result := git.SanitizeReferences(ctx, refs)
			Expect(result[0].Commit.Sha).To(BeEmpty())
		})
	})

	Context("when RepoURL is longer than 100 characters", func() {
		It("clears the overlong invalid RepoURL without panicking (truncation for logging only)", func() {
			longInvalidURL := "ftp://" + string(make([]byte, 200))
			refs := []v1alpha1.RevisionReference{
				{
					Commit: &v1alpha1.CommitMetadata{
						Sha:     sha40,
						RepoURL: longInvalidURL,
					},
				},
			}
			result := git.SanitizeReferences(ctx, refs)
			Expect(result[0].Commit.RepoURL).To(BeEmpty())
		})
	})
})

type fakeGitProvider struct {
	tempDirPath string
}

func (f *fakeGitProvider) GetGitHttpsRepoUrl(repo v1alpha1.GitRepository) string {
	// Return the local bare repo path for testing
	return f.tempDirPath
}
func (f *fakeGitProvider) GetUser(ctx context.Context) (string, error)  { return "user", nil }
func (f *fakeGitProvider) GetToken(ctx context.Context) (string, error) { return "token", nil }
