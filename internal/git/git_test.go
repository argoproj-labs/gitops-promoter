package git_test

import (
	"context"
	"encoding/json"
	"fmt"
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

// cloneHeadAndStatus captures a clone's HEAD position and porcelain status, used to assert that an
// operation left the clone's worktree/index/HEAD untouched (the clone-state invariant). HEAD is
// captured as both its resolved SHA (empty when unborn — some test setups clone a repo with no
// default branch) and its symbolic ref, so a move is detected either way.
func cloneHeadAndStatus(clonePath string) (string, string) {
	GinkgoHelper()
	head, _ := runGitCmd(clonePath, "rev-parse", "--verify", "--quiet", "HEAD")
	symref, _ := runGitCmd(clonePath, "symbolic-ref", "-q", "HEAD")
	status, err := runGitCmd(clonePath, "status", "--porcelain")
	Expect(err).NotTo(HaveOccurred())
	return strings.TrimSpace(head) + "|" + strings.TrimSpace(symref), status
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
			g := git.NewEnvironmentOperations(repo, gap, "default/testrepo")
			Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())

			// Call GetBranchShas with a non-existent branch
			_, err = g.GetBranchShas(GinkgoT().Context(), "environments/qal-usw2-eks-next", "")
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
		g = git.NewEnvironmentOperations(repo, gap, "default/testrepo")
		Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())
		_, err := g.GetBranchShas(GinkgoT().Context(), "active", "")
		Expect(err).NotTo(HaveOccurred())
		_, err = g.GetBranchShas(GinkgoT().Context(), "proposed", "")
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
		_, err := runGitCmd(workDir, "checkout", "-b", "active", defaultBranch)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.Remove(filepath.Join(workDir, "hydrator.metadata"))).To(Succeed())
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "active: empty base")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "-u", "origin", "active")
		Expect(err).NotTo(HaveOccurred())

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

		_, err = runGitCmd(workDir, "checkout", "active")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "merge", "--no-ff", "-m", "merge first into active", "first")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "origin", "active")
		Expect(err).NotTo(HaveOccurred())

		gap := &fakeGitProvider{tempDirPath: tempRepoDir}
		g = git.NewEnvironmentOperations(repo, gap, "default/testrepo")
		Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())
		_, err = g.GetBranchShas(GinkgoT().Context(), "active", "")
		Expect(err).NotTo(HaveOccurred())
		_, err = g.GetBranchShas(GinkgoT().Context(), "second", "")
		Expect(err).NotTo(HaveOccurred())

		hasConflict, err := g.HasConflict(GinkgoT().Context(), "second", "active")
		Expect(err).NotTo(HaveOccurred())
		Expect(hasConflict).To(BeTrue())
	})
})

var _ = Describe("ActivePath support", func() {
	var tempRepoDir string
	var workDir string
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

	It("reads branch and commit metadata from activePath hydrator.metadata", func() {
		err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("root"), 0o644)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.MkdirAll(filepath.Join(workDir, "apps", "app-one"), 0o755)).To(Succeed())
		err = os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), []byte(`{"drySha":"root-sha"}`), 0o644)
		Expect(err).NotTo(HaveOccurred())
		err = os.WriteFile(filepath.Join(workDir, "apps", "app-one", "hydrator.metadata"), []byte(`{"drySha":"app-sha"}`), 0o644)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "init")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "branch", "-M", "environment/development")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "-u", "origin", "environment/development")
		Expect(err).NotTo(HaveOccurred())

		gap := &fakeGitProvider{tempDirPath: tempRepoDir}
		g = git.NewEnvironmentOperations(repo, gap, "default/testrepo")
		Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())

		shas, err := g.GetBranchShas(GinkgoT().Context(), "environment/development", "apps/app-one")
		Expect(err).NotTo(HaveOccurred())
		Expect(shas.Dry).To(Equal("app-sha"))
		Expect(shas.Hydrated).NotTo(BeEmpty())

		commitSha, err := runGitCmd(workDir, "rev-parse", "environment/development")
		Expect(err).NotTo(HaveOccurred())
		commitSha = strings.TrimSpace(commitSha)

		metadata, err := g.GetShaMetadataFromFile(GinkgoT().Context(), commitSha, "apps/app-one")
		Expect(err).NotTo(HaveOccurred())
		Expect(metadata.Sha).To(Equal("app-sha"))
	})

	It("treats a missing activePath hydrator.metadata as empty even when the path exists in the worktree", func() {
		// Regression for the activePath convergence bug: GetBranchShas reads
		// <activePath>/hydrator.metadata from the active branch, which legitimately does
		// not exist until that app's first promotion. When the clone's working tree
		// already holds that path (e.g. left by a prior checkout of the proposed branch
		// during conflict resolution), a worktree-sensitive read like `git show origin/<active>:<path>`
		// fails instead of reporting a clean absence. GetBranchShas must determine presence from the
		// ref's tree alone (not the worktree) and treat a genuinely-absent path as "no metadata yet",
		// not a hard error, or the CTP can never compute status and never promotes.
		Expect(os.MkdirAll(filepath.Join(workDir, "apps", "app-one"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-one", "config.yaml"), []byte("version: active\n"), 0o644)).To(Succeed())
		// Active branch intentionally has NO apps/app-one/hydrator.metadata.
		_, err := runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "active without app-one metadata")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "branch", "-M", "active")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "-u", "origin", "active")
		Expect(err).NotTo(HaveOccurred())

		gap := &fakeGitProvider{tempDirPath: tempRepoDir}
		g = git.NewEnvironmentOperations(repo, gap, "default/testrepo")
		Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())

		// Place the path in the clone's working tree to prove the read is worktree-independent:
		// a worktree-only copy must not be mistaken for metadata on the ref.
		clonePath := g.ClonePath()
		Expect(os.MkdirAll(filepath.Join(clonePath, "apps", "app-one"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(clonePath, "apps", "app-one", "hydrator.metadata"), []byte(`{"drySha":"worktree-only"}`), 0o644)).To(Succeed())

		shas, err := g.GetBranchShas(GinkgoT().Context(), "active", "apps/app-one")
		Expect(err).NotTo(HaveOccurred(), "missing activePath metadata on the ref must not be a hard error")
		Expect(shas.Dry).To(BeEmpty(), "worktree-only metadata must not be mistaken for ref metadata")
		Expect(shas.Hydrated).NotTo(BeEmpty(), "the hydrated SHA still resolves from the ref")
	})

	It("path-scoped merge: proposed wins inside activePath, active wins outside on conflict", func() {
		base := map[string]string{
			"apps/app-one/config.yaml": "version: base\n",
			"apps/app-two/config.yaml": "version: base\n",
		}
		for filePath, content := range base {
			Expect(os.MkdirAll(filepath.Dir(filepath.Join(workDir, filePath)), 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(workDir, filePath), []byte(content), 0o644)).To(Succeed())
		}
		metadata := map[string]string{"drySha": "base"}
		m, err := json.Marshal(metadata)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-one", "hydrator.metadata"), m, 0o644)).To(Succeed())
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "base")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "branch", "-M", "active")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "-u", "origin", "active")
		Expect(err).NotTo(HaveOccurred())

		_, err = runGitCmd(workDir, "checkout", "-b", "proposed-app-one-next")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-one", "config.yaml"), []byte("version: proposed\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-two", "config.yaml"), []byte("version: proposed\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), []byte(`{"drySha":"proposed-root"}`), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-one", "hydrator.metadata"), []byte(`{"drySha":"proposed"}`), 0o644)).To(Succeed())
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "proposed")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "-u", "origin", "proposed-app-one-next")
		Expect(err).NotTo(HaveOccurred())

		_, err = runGitCmd(workDir, "checkout", "active")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), []byte(`{"drySha":"active-root"}`), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-one", "config.yaml"), []byte("version: active\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-two", "config.yaml"), []byte("version: active\n"), 0o644)).To(Succeed())
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "active")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "origin", "active")
		Expect(err).NotTo(HaveOccurred())

		proposedTip := strings.TrimSpace(mustGit(workDir, "rev-parse", "proposed-app-one-next"))
		activeTip := strings.TrimSpace(mustGit(workDir, "rev-parse", "active"))

		gap := &fakeGitProvider{tempDirPath: tempRepoDir}
		g = git.NewEnvironmentOperations(repo, gap, "default/testrepo")
		Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())
		_, err = g.GetBranchShas(GinkgoT().Context(), "proposed-app-one-next", "apps/app-one")
		Expect(err).NotTo(HaveOccurred())

		// The path-scoped merge must not touch the clone's worktree/index/HEAD.
		headBefore, statusBefore := cloneHeadAndStatus(g.ClonePath())

		err = g.MergeWithOursStrategyForPath(GinkgoT().Context(), "proposed-app-one-next", "active", "apps/app-one")
		Expect(err).NotTo(HaveOccurred())

		headAfter, statusAfter := cloneHeadAndStatus(g.ClonePath())
		Expect(headAfter).To(Equal(headBefore), "merge must not move HEAD")
		Expect(statusAfter).To(Equal(statusBefore), "merge must not dirty the worktree/index")

		_, err = runGitCmd(workDir, "fetch", "origin")
		Expect(err).NotTo(HaveOccurred())

		// The pushed commit must record proposed and active as its two parents (ours-style topology).
		parents := strings.Fields(mustGit(workDir, "rev-list", "--parents", "-n", "1", "origin/proposed-app-one-next"))
		Expect(parents[1:]).To(ConsistOf(proposedTip, activeTip), "merge commit parents must be [proposed, active]")

		_, err = runGitCmd(workDir, "checkout", "-B", "proposed-app-one-next", "origin/proposed-app-one-next")
		Expect(err).NotTo(HaveOccurred())

		appOneContent, err := os.ReadFile(filepath.Join(workDir, "apps", "app-one", "config.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(appOneContent))).To(Equal("version: proposed"),
			"inside activePath, proposed wins")

		appTwoContent, err := os.ReadFile(filepath.Join(workDir, "apps", "app-two", "config.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(appTwoContent))).To(Equal("version: active"),
			"outside activePath, active wins on conflict")

		rootMetadata, err := os.ReadFile(filepath.Join(workDir, "hydrator.metadata"))
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(rootMetadata))).To(ContainSubstring("active-root"),
			"root hydrator.metadata is no longer special-cased: outside activePath, active wins on conflict")

		hasConflict, err := g.HasConflict(GinkgoT().Context(), "proposed-app-one-next", "active")
		Expect(err).NotTo(HaveOccurred())
		Expect(hasConflict).To(BeFalse(), "resolved proposed branch should merge cleanly into active for SCM")
	})

	It("removes files deleted in the proposed branch from activePath during conflict resolution", func() {
		base := map[string]string{
			"apps/app-one/config.yaml": "version: base\n",
			"apps/app-one/extra.yaml":  "extra: base\n",
			"apps/app-two/config.yaml": "version: base\n",
		}
		for filePath, content := range base {
			Expect(os.MkdirAll(filepath.Dir(filepath.Join(workDir, filePath)), 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(workDir, filePath), []byte(content), 0o644)).To(Succeed())
		}
		metadata := map[string]string{"drySha": "base"}
		m, err := json.Marshal(metadata)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-one", "hydrator.metadata"), m, 0o644)).To(Succeed())
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "base")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "branch", "-M", "active")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "-u", "origin", "active")
		Expect(err).NotTo(HaveOccurred())

		// Proposed branch: extra.yaml is deleted from apps/app-one
		_, err = runGitCmd(workDir, "checkout", "-b", "proposed-app-one-next")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-one", "config.yaml"), []byte("version: proposed\n"), 0o644)).To(Succeed())
		Expect(os.Remove(filepath.Join(workDir, "apps", "app-one", "extra.yaml"))).To(Succeed())
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "proposed")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "-u", "origin", "proposed-app-one-next")
		Expect(err).NotTo(HaveOccurred())

		// Advance active branch so a conflict exists
		_, err = runGitCmd(workDir, "checkout", "active")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-one", "config.yaml"), []byte("version: active\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-two", "config.yaml"), []byte("version: active\n"), 0o644)).To(Succeed())
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "active advance")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "origin", "active")
		Expect(err).NotTo(HaveOccurred())

		gap := &fakeGitProvider{tempDirPath: tempRepoDir}
		g = git.NewEnvironmentOperations(repo, gap, "default/testrepo")
		Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())
		_, err = g.GetBranchShas(GinkgoT().Context(), "proposed-app-one-next", "apps/app-one")
		Expect(err).NotTo(HaveOccurred())

		err = g.MergeWithOursStrategyForPath(GinkgoT().Context(), "proposed-app-one-next", "active", "apps/app-one")
		Expect(err).NotTo(HaveOccurred())

		_, err = runGitCmd(workDir, "fetch", "origin")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "checkout", "-B", "proposed-app-one-next", "origin/proposed-app-one-next")
		Expect(err).NotTo(HaveOccurred())

		// extra.yaml was deleted in proposed branch — must not be present after merge
		_, err = os.Stat(filepath.Join(workDir, "apps", "app-one", "extra.yaml"))
		Expect(os.IsNotExist(err)).To(BeTrue(), "extra.yaml deleted in proposed branch should not exist after merge")

		// config.yaml should carry proposed content
		appOneContent, err := os.ReadFile(filepath.Join(workDir, "apps", "app-one", "config.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(appOneContent))).To(Equal("version: proposed"))

		// app-two is outside activePath — must keep active content
		appTwoContent, err := os.ReadFile(filepath.Join(workDir, "apps", "app-two", "config.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(appTwoContent))).To(Equal("version: active"))
	})

	// Regression for the conflict-resolution wedge: if a prior merge was interrupted between
	// `merge --no-commit` and `commit`, the clone is left with MERGE_HEAD set and an unmerged
	// index. A worktree-based implementation then fails every subsequent reconcile at its opening
	// `checkout -B` ("you need to resolve your current index first"), permanently stalling
	// conflict resolution for that policy until the process restarts. The merge functions must
	// instead compute the result from the object DB and succeed regardless of the clone's state.
	It("MergeWithOursStrategyForPath succeeds even when the clone is left in a wedged mid-merge state", func() {
		base := map[string]string{
			"apps/app-one/config.yaml": "version: base\n",
			"apps/app-two/config.yaml": "version: base\n",
		}
		for filePath, content := range base {
			Expect(os.MkdirAll(filepath.Dir(filepath.Join(workDir, filePath)), 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(workDir, filePath), []byte(content), 0o644)).To(Succeed())
		}
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-one", "hydrator.metadata"), []byte(`{"drySha":"base"}`), 0o644)).To(Succeed())
		_, err := runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "base")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "branch", "-M", "active")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "-u", "origin", "active")
		Expect(err).NotTo(HaveOccurred())

		_, err = runGitCmd(workDir, "checkout", "-b", "proposed-app-one-next")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-one", "config.yaml"), []byte("version: proposed\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-two", "config.yaml"), []byte("version: proposed\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-one", "hydrator.metadata"), []byte(`{"drySha":"proposed"}`), 0o644)).To(Succeed())
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "proposed")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "-u", "origin", "proposed-app-one-next")
		Expect(err).NotTo(HaveOccurred())

		// Advance active so it conflicts with proposed.
		_, err = runGitCmd(workDir, "checkout", "active")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-one", "config.yaml"), []byte("version: active\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "apps", "app-two", "config.yaml"), []byte("version: active\n"), 0o644)).To(Succeed())
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "active advance")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "origin", "active")
		Expect(err).NotTo(HaveOccurred())

		gap := &fakeGitProvider{tempDirPath: tempRepoDir}
		g = git.NewEnvironmentOperations(repo, gap, "default/testrepo")
		Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())
		_, err = g.GetBranchShas(GinkgoT().Context(), "proposed-app-one-next", "apps/app-one")
		Expect(err).NotTo(HaveOccurred())

		// Inject the wedge: start a path-scoped merge in the clone and abandon it before commit,
		// exactly as an interrupted/failed prior reconcile would leave it.
		clonePath := g.ClonePath()
		Expect(clonePath).NotTo(BeEmpty())
		_, err = runGitCmd(clonePath, "checkout", "-B", "proposed-app-one-next", "origin/proposed-app-one-next")
		Expect(err).NotTo(HaveOccurred())
		// This merge conflicts; the non-zero exit is expected and intentionally ignored.
		_, _ = runGitCmd(clonePath, "merge", "--no-commit", "--no-ff", "origin/active")
		_, statErr := os.Stat(filepath.Join(clonePath, ".git", "MERGE_HEAD"))
		Expect(statErr).NotTo(HaveOccurred(), "precondition: clone must be left mid-merge with MERGE_HEAD set")
		headBefore, statusBefore := cloneHeadAndStatus(clonePath)

		// The merge must still succeed despite the wedged clone state, and must leave that state
		// untouched (it operates entirely on the object DB).
		err = g.MergeWithOursStrategyForPath(GinkgoT().Context(), "proposed-app-one-next", "active", "apps/app-one")
		Expect(err).NotTo(HaveOccurred(), "path-scoped merge must succeed from a pre-wedged clone")

		headAfter, statusAfter := cloneHeadAndStatus(clonePath)
		Expect(headAfter).To(Equal(headBefore), "merge must not move HEAD even from a wedged clone")
		Expect(statusAfter).To(Equal(statusBefore), "merge must not touch the wedged worktree/index")

		// Verify the remote advanced with the correct resolved tree.
		_, err = runGitCmd(workDir, "fetch", "origin")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "checkout", "-B", "proposed-app-one-next", "origin/proposed-app-one-next")
		Expect(err).NotTo(HaveOccurred())
		appOneContent, err := os.ReadFile(filepath.Join(workDir, "apps", "app-one", "config.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(appOneContent))).To(Equal("version: proposed"), "app-one keeps proposed content")
		appTwoContent, err := os.ReadFile(filepath.Join(workDir, "apps", "app-two", "config.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(appTwoContent))).To(Equal("version: active"), "app-two uses active content")
	})

	It("MergeWithOursStrategy succeeds even when the clone is left in a wedged mid-merge state", func() {
		Expect(os.WriteFile(filepath.Join(workDir, "config.yaml"), []byte("version: base\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(workDir, "hydrator.metadata"), []byte(`{"drySha":"base"}`), 0o644)).To(Succeed())
		_, err := runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "base")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "branch", "-M", "active")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "-u", "origin", "active")
		Expect(err).NotTo(HaveOccurred())

		_, err = runGitCmd(workDir, "checkout", "-b", "proposed")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(workDir, "config.yaml"), []byte("version: proposed\n"), 0o644)).To(Succeed())
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "proposed")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "-u", "origin", "proposed")
		Expect(err).NotTo(HaveOccurred())

		// Advance active so it conflicts with proposed.
		_, err = runGitCmd(workDir, "checkout", "active")
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(workDir, "config.yaml"), []byte("version: active\n"), 0o644)).To(Succeed())
		_, err = runGitCmd(workDir, "add", "-A")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "active advance")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "origin", "active")
		Expect(err).NotTo(HaveOccurred())

		gap := &fakeGitProvider{tempDirPath: tempRepoDir}
		g = git.NewEnvironmentOperations(repo, gap, "default/testrepo")
		Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())
		_, err = g.GetBranchShas(GinkgoT().Context(), "proposed", "")
		Expect(err).NotTo(HaveOccurred())

		// Inject the wedge in the clone.
		clonePath := g.ClonePath()
		Expect(clonePath).NotTo(BeEmpty())
		_, err = runGitCmd(clonePath, "checkout", "-B", "proposed", "origin/proposed")
		Expect(err).NotTo(HaveOccurred())
		_, _ = runGitCmd(clonePath, "merge", "--no-commit", "--no-ff", "origin/active")
		_, statErr := os.Stat(filepath.Join(clonePath, ".git", "MERGE_HEAD"))
		Expect(statErr).NotTo(HaveOccurred(), "precondition: clone must be left mid-merge with MERGE_HEAD set")
		headBefore, statusBefore := cloneHeadAndStatus(clonePath)

		err = g.MergeWithOursStrategy(GinkgoT().Context(), "proposed", "active")
		Expect(err).NotTo(HaveOccurred(), "ours-strategy merge must succeed from a pre-wedged clone")

		headAfter, statusAfter := cloneHeadAndStatus(clonePath)
		Expect(headAfter).To(Equal(headBefore), "merge must not move HEAD even from a wedged clone")
		Expect(statusAfter).To(Equal(statusBefore), "merge must not touch the wedged worktree/index")

		// The ours strategy keeps proposed's tree; the remote tip must reflect proposed content.
		_, err = runGitCmd(workDir, "fetch", "origin")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "checkout", "-B", "proposed", "origin/proposed")
		Expect(err).NotTo(HaveOccurred())
		content, err := os.ReadFile(filepath.Join(workDir, "config.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(string(content))).To(Equal("version: proposed"), "ours strategy keeps proposed content")
	})
})

var _ = Describe("Git subprocess proxy env", func() {
	It("forwards proxy and TLS env vars via runCmd", func() {
		want := map[string]string{
			"HTTPS_PROXY":    "http://proxy.test:8443",
			"HTTP_PROXY":     "http://proxy.test:8080",
			"NO_PROXY":       "localhost,127.0.0.1",
			"GIT_SSL_CAINFO": "/tmp/test-ca.pem",
			"SSL_CERT_FILE":  "/tmp/test-cert.pem",
		}
		for key, val := range want {
			GinkgoT().Setenv(key, val)
		}

		workDir := GinkgoT().TempDir()
		envDump := filepath.Join(workDir, "proxy-env-dump")
		gitBin := filepath.Join(workDir, "git")
		script := fmt.Sprintf(`#!/bin/sh
ENV_DUMP=%q
if [ "$1" = "ls-remote" ] && [ "$2" = "--heads" ]; then
  : > "$ENV_DUMP"
  for key in HTTPS_PROXY HTTP_PROXY NO_PROXY GIT_SSL_CAINFO SSL_CERT_FILE; do
    eval "val=\$$key"
    echo "$key=$val" >> "$ENV_DUMP"
  done
  echo "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef	refs/heads/main"
  exit 0
fi
echo "unexpected git args: $*" >&2
exit 1
`, envDump)
		Expect(os.WriteFile(gitBin, []byte(script), 0o755)).To(Succeed())
		GinkgoT().Setenv("PATH", workDir)

		repo := &v1alpha1.GitRepository{
			Spec: v1alpha1.GitRepositorySpec{
				GitHub: &v1alpha1.GitHubRepo{Owner: "test-owner", Name: "testrepo"},
				ScmProviderRef: v1alpha1.ScmProviderObjectReference{
					Kind: "ScmProvider",
					Name: "testprovider",
				},
			},
			ObjectMeta: metav1.ObjectMeta{Name: "testrepo", Namespace: "default"},
		}
		gap := &fakeGitProvider{tempDirPath: filepath.Join(workDir, "ignored-repo")}

		shas, err := git.LsRemote(context.Background(), gap, repo, "main")
		Expect(err).NotTo(HaveOccurred())
		Expect(shas).To(HaveKeyWithValue("main", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"))

		dump, err := os.ReadFile(envDump)
		Expect(err).NotTo(HaveOccurred())
		got := map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(string(dump)), "\n") {
			key, val, ok := strings.Cut(line, "=")
			Expect(ok).To(BeTrue(), "unexpected dump line %q", line)
			got[key] = val
		}
		for key, val := range want {
			Expect(got[key]).To(Equal(val), "proxy env var %s", key)
		}
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
