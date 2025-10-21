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

type fakeGitProvider struct {
	tempDirPath string
}

func (f *fakeGitProvider) GetGitHttpsRepoUrl(repo v1alpha1.GitRepository) string {
	// Return the local bare repo path for testing
	return f.tempDirPath
}
func (f *fakeGitProvider) GetUser(ctx context.Context) (string, error)  { return "user", nil }
func (f *fakeGitProvider) GetToken(ctx context.Context) (string, error) { return "token", nil }
