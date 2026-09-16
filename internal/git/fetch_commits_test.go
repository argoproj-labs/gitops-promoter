package git_test

import (
	"fmt"
	"os"
	"strings"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("FetchCommitsFromOrigin", func() {
	var bareDir string
	var workDir string
	var mainBranch string
	var offBranchCommit string

	BeforeEach(func() {
		var err error
		bareDir, err = os.MkdirTemp("", "fetch-commits-bare-*")
		Expect(err).NotTo(HaveOccurred())
		workDir, err = os.MkdirTemp("", "fetch-commits-work-*")
		Expect(err).NotTo(HaveOccurred())

		_, err = runGitCmd(bareDir, "init", "--bare")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "clone", bareDir, ".")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "config", "user.name", "test")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "config", "user.email", "t@t.com")
		Expect(err).NotTo(HaveOccurred())

		for i := 0; i < 12; i++ {
			_, err = runGitCmd(workDir, "commit", "--allow-empty", "-m", fmt.Sprintf("main %d", i))
			Expect(err).NotTo(HaveOccurred())
		}
		mainBranch, err = runGitCmd(workDir, "rev-parse", "--abbrev-ref", "HEAD")
		Expect(err).NotTo(HaveOccurred())
		mainBranch = strings.TrimSpace(mainBranch)
		_, err = runGitCmd(workDir, "push", "-u", "origin", mainBranch)
		Expect(err).NotTo(HaveOccurred())

		_, err = runGitCmd(workDir, "checkout", "--orphan", "orphan-root")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "--allow-empty", "-m", "orphan-only")
		Expect(err).NotTo(HaveOccurred())
		offBranchCommit, err = runGitCmd(workDir, "rev-parse", "HEAD")
		Expect(err).NotTo(HaveOccurred())
		offBranchCommit = strings.TrimSpace(offBranchCommit)
		_, err = runGitCmd(workDir, "push", "-u", "origin", "orphan-root")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "checkout", mainBranch)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		_ = os.RemoveAll(bareDir)
		_ = os.RemoveAll(workDir)
	})

	It("fetches commits missing from the local clone", func() {
		repo := &v1alpha1.GitRepository{
			Name: "r", Namespace: "default",
			Spec: v1alpha1.GitRepositorySpec{
				GitHub: &v1alpha1.GitHubRepo{Owner: "o", Name: "r"},
			},
		}
		ctx := GinkgoT().Context()
		g := git.NewEnvironmentOperations(repo, &fakeGitProvider{tempDirPath: bareDir}, "fetch-commits-test")
		Expect(g.CloneRepo(ctx)).To(Succeed())
		Expect(g.FetchBranch(ctx, mainBranch)).To(Succeed())

		Expect(g.FetchCommitsFromOrigin(ctx, offBranchCommit)).To(Succeed())
		Expect(g.LoadCommits(ctx, offBranchCommit)).To(Succeed())
		meta, err := g.GetShaMetadataFromGit(ctx, offBranchCommit)
		Expect(err).NotTo(HaveOccurred())
		Expect(meta.Sha).To(Equal(offBranchCommit))
		Expect(meta.Subject).To(Equal("orphan-only"))
	})

	It("requires a clone before fetching", func() {
		repo := &v1alpha1.GitRepository{
			Name: "r", Namespace: "default",
			Spec: v1alpha1.GitRepositorySpec{
				GitHub: &v1alpha1.GitHubRepo{Owner: "o", Name: "r"},
			},
		}
		g := git.NewEnvironmentOperations(repo, &fakeGitProvider{tempDirPath: bareDir}, "fetch-commits-no-clone")
		err := g.FetchCommitsFromOrigin(GinkgoT().Context(), strings.Repeat("a", 40))
		Expect(err).To(HaveOccurred())
	})

	It("skips SHAs that are already local", func() {
		repo := &v1alpha1.GitRepository{
			Name: "r", Namespace: "default",
			Spec: v1alpha1.GitRepositorySpec{
				GitHub: &v1alpha1.GitHubRepo{Owner: "o", Name: "r"},
			},
		}
		ctx := GinkgoT().Context()
		g := git.NewEnvironmentOperations(repo, &fakeGitProvider{tempDirPath: bareDir}, "fetch-commits-skip")
		Expect(g.CloneRepo(ctx)).To(Succeed())
		Expect(g.FetchBranch(ctx, mainBranch)).To(Succeed())

		tip, err := g.GetRevListFirstParent(ctx, "origin/"+mainBranch, 1)
		Expect(err).NotTo(HaveOccurred())
		Expect(tip).ToNot(BeEmpty())
		Expect(g.FetchCommitsFromOrigin(ctx, tip[0])).To(Succeed())
	})
})
