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
	t.Parallel()
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
			g := git.NewEnvironmentOperations(repo, gap, defaultBranch, "default/testrepo")
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

var _ = Describe("HasConflict", func() {
	var tempRepoDir string
	var workDir string
	var defaultBranch string
	var repo *v1alpha1.GitRepository
	var g *git.EnvironmentOperations

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

		Expect(os.MkdirAll(filepath.Join(workDir, "apps", "app-one"), 0o755)).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(workDir, "apps", "app-two"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-one", "config.yaml"), []byte("version: base\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-two", "config.yaml"), []byte("version: base\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), []byte(`{"drySha":"base"}`), 0o644)).To(Succeed())
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "base")
		Expect(err).NotTo(HaveOccurred())

		out, err := runGitCmd(workDir, "rev-parse", "--abbrev-ref", "HEAD")
		Expect(err).NotTo(HaveOccurred())
		defaultBranch = strings.TrimSpace(out)

		_, err = runGitCmd(workDir, "push", "-u", "origin", defaultBranch)
		Expect(err).NotTo(HaveOccurred())

		repo = &v1alpha1.GitRepository{
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
	})

	AfterEach(func() {
		if tempRepoDir != "" {
			Expect(os.RemoveAll(tempRepoDir)).To(Succeed())
		}
		if workDir != "" {
			Expect(os.RemoveAll(workDir)).To(Succeed())
		}
	})

	createBranch := func(branch string, files map[string]string, commitMessage string) {
		_, err := runGitCmd(workDir, "checkout", "-b", branch, defaultBranch)
		Expect(err).NotTo(HaveOccurred())
		for relPath, content := range files {
			full := filepath.Join(workDir, relPath)
			Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(Succeed())
			Expect(os.WriteFile(full, []byte(content), 0o644)).To(Succeed())
		}
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", commitMessage)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "-u", "origin", branch)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "checkout", defaultBranch)
		Expect(err).NotTo(HaveOccurred())
	}

	prepareEnvOps := func() {
		gap := &fakeGitProvider{tempDirPath: tempRepoDir}
		g = git.NewEnvironmentOperations(repo, gap, "active", "default/testrepo")
		Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())
		_, err := g.GetBranchShas(GinkgoT().Context(), "active")
		Expect(err).NotTo(HaveOccurred())
		_, err = g.GetBranchShas(GinkgoT().Context(), "proposed")
		Expect(err).NotTo(HaveOccurred())
	}

	It("returns false when active and proposed touch disjoint files", func() {
		createBranch("active", map[string]string{
			"apps/app-one/config.yaml": "version: active\n",
		}, "active app-one")
		createBranch("proposed", map[string]string{
			"apps/app-two/config.yaml": "version: proposed\n",
		}, "proposed app-two")

		prepareEnvOps()

		hasConflict, err := g.HasConflict(GinkgoT().Context(), "proposed", "active")
		Expect(err).NotTo(HaveOccurred())
		Expect(hasConflict).To(BeFalse())
	})

	It("returns true when active and proposed both modify the same file", func() {
		createBranch("active", map[string]string{
			"apps/app-one/config.yaml": "version: active\n",
		}, "active app-one")
		createBranch("proposed", map[string]string{
			"apps/app-one/config.yaml": "version: proposed\n",
		}, "proposed app-one")

		prepareEnvOps()

		hasConflict, err := g.HasConflict(GinkgoT().Context(), "proposed", "active")
		Expect(err).NotTo(HaveOccurred())
		Expect(hasConflict).To(BeTrue())
	})

	It("returns true when disjoint app paths share modifications to root hydrator.metadata", func() {
		createBranch("active", map[string]string{
			"apps/app-one/config.yaml": "version: active\n",
			"hydrator.metadata":        `{"drySha":"dry-one"}`,
		}, "active app-one + root metadata")
		createBranch("proposed", map[string]string{
			"apps/app-two/config.yaml": "version: proposed\n",
			"hydrator.metadata":        `{"drySha":"dry-two"}`,
		}, "proposed app-two + root metadata")

		prepareEnvOps()

		hasConflict, err := g.HasConflict(GinkgoT().Context(), "proposed", "active")
		Expect(err).NotTo(HaveOccurred())
		Expect(hasConflict).To(BeTrue())
	})

	It("returns true when a sibling branch still has different root hydrator.metadata after another sibling has merged into active", func() {
		// Build an active branch whose merge base has no hydrator.metadata at root, so each
		// sibling branch ADDS the file independently (the real shared-active-branch case).
		_, err := runGitCmd(workDir, "checkout", "-b", "active", defaultBranch)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Remove(filepath.Join(workDir, "hydrator.metadata"))).To(Succeed())
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "active: empty base")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "-u", "origin", "active")
		Expect(err).NotTo(HaveOccurred())

		// Two sibling branches off active, each ADDING root hydrator.metadata with different values.
		_, err = runGitCmd(workDir, "checkout", "-b", "first", "active")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), []byte(`{"drySha":"first-dry"}`), 0o644)).To(Succeed())
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "first adds root metadata")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "-u", "origin", "first")
		Expect(err).NotTo(HaveOccurred())

		_, err = runGitCmd(workDir, "checkout", "-b", "second", "active")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), []byte(`{"drySha":"second-dry"}`), 0o644)).To(Succeed())
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "second adds root metadata")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "-u", "origin", "second")
		Expect(err).NotTo(HaveOccurred())

		// Merge first into active, simulating the first PR getting merged.
		_, err = runGitCmd(workDir, "checkout", "active")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "merge", "--no-ff", "-m", "merge first into active", "first")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "origin", "active")
		Expect(err).NotTo(HaveOccurred())

		gap := &fakeGitProvider{tempDirPath: tempRepoDir}
		g = git.NewEnvironmentOperations(repo, gap, "active", "default/testrepo")
		Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())
		_, err = g.GetBranchShas(GinkgoT().Context(), "active")
		Expect(err).NotTo(HaveOccurred())
		_, err = g.GetBranchShas(GinkgoT().Context(), "second")
		Expect(err).NotTo(HaveOccurred())

		hasConflict, err := g.HasConflict(GinkgoT().Context(), "second", "active")
		Expect(err).NotTo(HaveOccurred())
		Expect(hasConflict).To(BeTrue())
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
