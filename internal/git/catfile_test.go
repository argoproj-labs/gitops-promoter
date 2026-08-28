package git_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
)

// logRecord assembles one `git log -z --pretty=format:` record as git would emit it. Fields and
// records share the same NUL separator, so records are joined with it too.
func logRecord(sha, author, commitTime, subject, body, message string) string {
	return strings.Join([]string{sha, author, commitTime, subject, body, message}, "\x00")
}

var _ = Describe("parseCommitLogOutput", func() {
	It("maps each record to its SHA and parses the committer time offset", func() {
		stdout := strings.Join([]string{
			logRecord("abc123", "Alice Example", "2023-11-14T22:33:20-04:00", "feat: do something important",
				"Body paragraph one.\n\nSigned-off-by: Alice Example <alice@example.com>\n",
				"feat: do something important\n\nBody paragraph one.\n\nSigned-off-by: Alice Example <alice@example.com>\n"),
			logRecord("def456", "Bob Example", "2023-11-14T22:33:20+00:00", "subject only", "", "subject only\n"),
		}, "\x00")

		results, err := git.ParseCommitLogOutput(stdout)
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(2))

		first := results["abc123"]
		Expect(first.State.Sha).To(Equal("abc123"))
		Expect(first.State.Author).To(Equal("Alice Example"))
		Expect(first.State.Subject).To(Equal("feat: do something important"))
		Expect(first.State.Body).To(ContainSubstring("Body paragraph one."))
		Expect(first.State.Body).To(ContainSubstring("Signed-off-by: Alice Example"))
		Expect(first.Message).To(HavePrefix("feat: do something important"))

		expected := time.Unix(1700015600, 0).In(time.FixedZone("-0400", -4*60*60))
		Expect(first.State.CommitTime.Time.Equal(expected)).To(BeTrue())

		Expect(results["def456"].State.Subject).To(Equal("subject only"))
		Expect(results["def456"].State.Body).To(BeEmpty())
	})

	It("returns no records for empty output", func() {
		results, err := git.ParseCommitLogOutput("")
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(BeEmpty())
	})

	It("preserves the empty fields of a commit with no message", func() {
		stdout := strings.Join([]string{
			logRecord("abc123", "Alice", "2023-11-14T22:33:20+00:00", "", "", ""),
			logRecord("def456", "Bob", "2023-11-14T22:33:20+00:00", "subject", "", "subject\n"),
		}, "\x00")

		results, err := git.ParseCommitLogOutput(stdout)
		Expect(err).NotTo(HaveOccurred())
		Expect(results).To(HaveLen(2))
		Expect(results["abc123"].State.Subject).To(BeEmpty())
		Expect(results["abc123"].Message).To(BeEmpty())
		Expect(results["def456"].State.Subject).To(Equal("subject"))
	})

	It("keeps a message that contains the field separator's printable predecessors", func() {
		results, err := git.ParseCommitLogOutput(
			logRecord("abc123", "Alice", "2023-11-14T22:33:20+00:00", "s", "before\x1fafter\n", "s\n\nbefore\x1fafter\n"))
		Expect(err).NotTo(HaveOccurred())
		Expect(results["abc123"].State.Body).To(Equal("before\x1fafter"))
	})

	It("rejects output that does not divide into whole records", func() {
		_, err := git.ParseCommitLogOutput("abc123\x00Alice")
		Expect(err).To(MatchError(ContainSubstring("expected a multiple of 6 fields")))
	})

	It("rejects an unparsable committer time", func() {
		_, err := git.ParseCommitLogOutput(logRecord("abc123", "Alice", "not-a-time", "s", "", "s\n"))
		Expect(err).To(MatchError(ContainSubstring("parse committer time")))
	})
})

var _ = Describe("parseCatFileBatch", func() {
	It("parses mixed hits and missing objects in request order", func() {
		stdout := "abc blob 6\nfirst\n\n" +
			"sha:path/blob missing\n" +
			"def blob 5\nhello\n"

		results, err := git.ParseCatFileBatch(strings.NewReader(stdout), []string{"abc", "sha:path/blob", "def"})
		Expect(err).NotTo(HaveOccurred())
		Expect(results["abc"].Missing).To(BeFalse())
		Expect(string(results["abc"].Data)).To(Equal("first\n"))
		Expect(results["sha:path/blob"].Missing).To(BeTrue())
		Expect(results["def"].Missing).To(BeFalse())
		Expect(string(results["def"].Data)).To(Equal("hello"))
	})

	It("handles zero-length blob objects", func() {
		results, err := git.ParseCatFileBatch(strings.NewReader("empty blob 0\n\n"), []string{"empty"})
		Expect(err).NotTo(HaveOccurred())
		Expect(results["empty"].Data).To(BeEmpty())
	})

	It("rejects output truncated mid-object", func() {
		_, err := git.ParseCatFileBatch(strings.NewReader("abc blob 10\nshort\n"), []string{"abc"})
		Expect(err).To(MatchError(ContainSubstring("truncated cat-file object")))
	})

	It("rejects output that ends before every request is answered", func() {
		_, err := git.ParseCatFileBatch(strings.NewReader("abc blob 5\nhello\n"), []string{"abc", "def"})
		Expect(err).To(MatchError(ContainSubstring("after 1 of 2 objects")))
	})
})

var _ = Describe("GetShaMetadataFromGit", func() {
	var tempRepoDir, workDir string

	BeforeEach(func() {
		var err error
		tempRepoDir, err = os.MkdirTemp("", "git-catfile-test-*")
		Expect(err).NotTo(HaveOccurred())
		workDir, err = os.MkdirTemp("", "git-catfile-work-*")
		Expect(err).NotTo(HaveOccurred())

		_, err = runGitCmd(tempRepoDir, "init", "--bare")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "clone", tempRepoDir, ".")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "config", "user.name", "Test User")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "config", "user.email", "test@example.com")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "config", "commit.gpgsign", "false")
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

	// expectMatchesGitShow commits commitMsg and asserts GetShaMetadataFromGit reports exactly what
	// `git show` does for the same commit.
	expectMatchesGitShow := func(commitMsg string) {
		err := os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("x"), 0o644)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "add", "file.txt")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", commitMsg)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "origin", "HEAD")
		Expect(err).NotTo(HaveOccurred())

		sha, err := runGitCmd(workDir, "rev-parse", "HEAD")
		Expect(err).NotTo(HaveOccurred())
		sha = strings.TrimSpace(sha)

		showAuthor, err := runGitCmd(workDir, "show", "-s", "--format=%an", sha)
		Expect(err).NotTo(HaveOccurred())
		showSubject, err := runGitCmd(workDir, "show", "-s", "--format=%s", sha)
		Expect(err).NotTo(HaveOccurred())
		showBody, err := runGitCmd(workDir, "show", "-s", "--format=%b", sha)
		Expect(err).NotTo(HaveOccurred())
		showTime, err := runGitCmd(workDir, "show", "-s", "--format=%cI", sha)
		Expect(err).NotTo(HaveOccurred())

		repo := newTestGitRepository()
		gap := &fakeGitProvider{tempDirPath: tempRepoDir}
		g := git.NewEnvironmentOperations(repo, gap, "default/catfile")
		Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())

		meta, err := g.GetShaMetadataFromGit(GinkgoT().Context(), sha)
		Expect(err).NotTo(HaveOccurred())
		Expect(meta.Author).To(Equal(strings.TrimSpace(showAuthor)))
		Expect(meta.Subject).To(Equal(strings.TrimSpace(showSubject)))
		Expect(meta.Body).To(Equal(strings.TrimSpace(showBody)))

		expectedTime, err := time.Parse(time.RFC3339, strings.TrimSpace(showTime))
		Expect(err).NotTo(HaveOccurred())
		Expect(meta.CommitTime.Time.Equal(expectedTime)).To(BeTrue())
	}

	It("matches git show field formatting for author, subject, body, and committer time", func() {
		expectMatchesGitShow("subject line\n\nbody paragraph\n\nSigned-off-by: Test User <test@example.com>")
	})

	It("folds a multi-line subject the same way git show does", func() {
		expectMatchesGitShow("first line of subject\nsecond line of subject\n\nbody line")
	})

	It("reports an empty body for a subject-only commit", func() {
		expectMatchesGitShow("subject only")
	})

	It("reads a commit whose message contains the field separator", func() {
		// Nothing stops a commit message from carrying control bytes, so the batch framing has to
		// use a byte that git itself refuses to store in a message.
		expectMatchesGitShow("subject line\n\nbefore\x1fafter")
	})
})

var _ = DescribeTable("fullObjectID",
	func(revision string, accepted bool) {
		Expect(git.FullObjectID.MatchString(revision)).To(Equal(accepted))
	},
	Entry("a SHA-1 object ID", "60d5f4b2e5a2c1f2a0b39a4a30fb0e40b30d2c11", true),
	Entry("an uppercase SHA-1 object ID", "60D5F4B2E5A2C1F2A0B39A4A30FB0E40B30D2C11", true),
	Entry("a SHA-256 object ID", strings.Repeat("a", 64), true),
	Entry("an abbreviated object ID", "60d5f4b", false),
	Entry("a revision-selection option", "--all", false),
	Entry("a ref name", "refs/heads/main", false),
	Entry("an object ID with a trailing revision", "60d5f4b2e5a2c1f2a0b39a4a30fb0e40b30d2c11\n--all", false),
	Entry("an empty revision", "", false),
)

var _ = Describe("LoadCommits", func() {
	var g *git.EnvironmentOperations
	var sha string

	BeforeEach(func() {
		tempRepoDir, err := os.MkdirTemp("", "git-cache-test-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(tempRepoDir)).To(Succeed()) })

		workDir, err := os.MkdirTemp("", "git-cache-work-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(workDir)).To(Succeed()) })

		_, err = runGitCmd(tempRepoDir, "init", "--bare")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "clone", tempRepoDir, ".")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "config", "user.name", "Test User")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "config", "user.email", "test@example.com")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "config", "commit.gpgsign", "false")
		Expect(err).NotTo(HaveOccurred())
		err = os.WriteFile(filepath.Join(workDir, "f"), []byte("v"), 0o644)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "add", "f")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "commit", "-m", "init")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "origin", "HEAD")
		Expect(err).NotTo(HaveOccurred())

		sha, err = runGitCmd(workDir, "rev-parse", "HEAD")
		Expect(err).NotTo(HaveOccurred())
		sha = strings.TrimSpace(sha)

		gap := &fakeGitProvider{tempDirPath: tempRepoDir}
		g = git.NewEnvironmentOperations(newTestGitRepository(), gap, "default/cache")
		Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())
	})

	It("deduplicates requests and serves repeated reads from the cache", func() {
		ctx := GinkgoT().Context()
		Expect(g.LoadCommits(ctx, sha, sha)).To(Succeed())
		meta1, err := g.GetShaMetadataFromGit(ctx, sha)
		Expect(err).NotTo(HaveOccurred())
		meta2, err := g.GetShaMetadataFromGit(ctx, sha)
		Expect(err).NotTo(HaveOccurred())
		Expect(meta1).To(Equal(meta2))
	})

	It("resolves an uppercase object ID", func() {
		ctx := GinkgoT().Context()
		upper := strings.ToUpper(sha)

		// git resolves an uppercase revision but always emits %H in lowercase, so the batch
		// results are keyed lowercase no matter how the SHA was written.
		Expect(g.LoadCommits(ctx, upper)).To(Succeed())

		meta, err := g.GetShaMetadataFromGit(ctx, upper)
		Expect(err).NotTo(HaveOccurred())
		Expect(meta.Sha).To(Equal(sha))
	})

	It("keeps an option-like revision out of the batch and still resolves the others", func() {
		ctx := GinkgoT().Context()

		// Revisions read out of commit trailers are untrusted, and git log --stdin treats
		// option-like input such as --all as a revision selector rather than a revision.
		Expect(g.LoadCommits(ctx, "--all", sha)).To(Succeed())

		meta, err := g.GetShaMetadataFromGit(ctx, sha)
		Expect(err).NotTo(HaveOccurred())
		Expect(meta.Sha).To(Equal(sha))

		_, err = g.GetShaMetadataFromGit(ctx, "--all")
		Expect(err).To(MatchError(ContainSubstring("not a full git object ID")))
	})

	It("tolerates a SHA that is absent from the clone and still resolves the others", func() {
		ctx := GinkgoT().Context()
		absent := "0000000000000000000000000000000000000000"

		// The batch is fatal for every SHA once one is absent, so prefetch failure must not surface
		// as an error; the present SHA is then resolved individually and only the absent one fails.
		Expect(g.LoadCommits(ctx, absent, sha)).To(Succeed())

		meta, err := g.GetShaMetadataFromGit(ctx, sha)
		Expect(err).NotTo(HaveOccurred())
		Expect(meta.Sha).To(Equal(sha))

		_, err = g.GetShaMetadataFromGit(ctx, absent)
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("GetTrailers", func() {
	It("parses trailers once and serves repeated reads from the commit cache", func() {
		tempRepoDir, err := os.MkdirTemp("", "git-trailer-cache-test-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(tempRepoDir)).To(Succeed()) })

		workDir, err := os.MkdirTemp("", "git-trailer-cache-work-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { Expect(os.RemoveAll(workDir)).To(Succeed()) })

		_, err = runGitCmd(tempRepoDir, "init", "--bare")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "clone", tempRepoDir, ".")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "config", "user.name", "Test User")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "config", "user.email", "test@example.com")
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "config", "commit.gpgsign", "false")
		Expect(err).NotTo(HaveOccurred())
		err = os.WriteFile(filepath.Join(workDir, "f"), []byte("v"), 0o644)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "add", "f")
		Expect(err).NotTo(HaveOccurred())

		const trailerKey = "Promoter-Test-Trailer"
		const trailerValue = "cached-on-commit-object"
		_, err = runGitCmd(workDir, "commit", "-m", "subject with trailer", "-m", trailerKey+": "+trailerValue)
		Expect(err).NotTo(HaveOccurred())
		_, err = runGitCmd(workDir, "push", "origin", "HEAD")
		Expect(err).NotTo(HaveOccurred())

		sha, err := runGitCmd(workDir, "rev-parse", "HEAD")
		Expect(err).NotTo(HaveOccurred())
		sha = strings.TrimSpace(sha)

		gap := &fakeGitProvider{tempDirPath: tempRepoDir}
		g := git.NewEnvironmentOperations(newTestGitRepository(), gap, "default/trailer-cache")
		Expect(g.CloneRepo(GinkgoT().Context())).To(Succeed())

		ctx := GinkgoT().Context()
		Expect(g.LoadCommits(ctx, sha)).To(Succeed())

		trailers1, err := g.GetTrailers(ctx, sha)
		Expect(err).NotTo(HaveOccurred())
		Expect(trailers1[trailerKey]).To(Equal([]string{trailerValue}))

		trailers2, err := g.GetTrailers(ctx, sha)
		Expect(err).NotTo(HaveOccurred())
		Expect(trailers2).To(Equal(trailers1))
	})
})

func newTestGitRepository() *v1alpha1.GitRepository {
	return &v1alpha1.GitRepository{
		Spec: v1alpha1.GitRepositorySpec{
			GitHub: &v1alpha1.GitHubRepo{Owner: "test-owner", Name: "testrepo"},
			ScmProviderRef: v1alpha1.ScmProviderObjectReference{
				Kind: "ScmProvider",
				Name: "testprovider",
			},
		},
		ObjectMeta: metav1.ObjectMeta{Name: "testrepo", Namespace: "default"},
	}
}

func setupBenchmarkRepo(b *testing.B) (*fakeGitProvider, *v1alpha1.GitRepository, []string) {
	b.Helper()
	tempRepoDir, err := os.MkdirTemp("", "git-bench-repo-*")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(tempRepoDir) })

	workDir, err := os.MkdirTemp("", "git-bench-work-*")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = os.RemoveAll(workDir) })

	mustGit := func(dir string, args ...string) string {
		b.Helper()
		out, err := runGitCmd(dir, args...)
		if err != nil {
			b.Fatalf("git %v in %s: %v", args, dir, err)
		}
		return out
	}

	mustGit(tempRepoDir, "init", "--bare")
	mustGit(workDir, "clone", tempRepoDir, ".")
	mustGit(workDir, "config", "user.name", "Bench User")
	mustGit(workDir, "config", "user.email", "bench@example.com")
	mustGit(workDir, "config", "commit.gpgsign", "false")

	shas := make([]string, 0, 5)
	for i := range 5 {
		if err := os.WriteFile(filepath.Join(workDir, "f.txt"), []byte(fmt.Sprintf("v%d", i)), 0o644); err != nil {
			b.Fatal(err)
		}
		mustGit(workDir, "add", "f.txt")
		mustGit(workDir, "commit", "-m", fmt.Sprintf("commit %d\n\nbody %d", i, i))
		shas = append(shas, strings.TrimSpace(mustGit(workDir, "rev-parse", "HEAD")))
	}
	mustGit(workDir, "push", "origin", "HEAD")

	gap := &fakeGitProvider{tempDirPath: tempRepoDir}
	return gap, newTestGitRepository(), shas
}

func BenchmarkCatFileIndividualReads(b *testing.B) {
	gap, repo, shas := setupBenchmarkRepo(b)
	ctx := b.Context()
	b.ResetTimer()
	for i := range b.N {
		g := git.NewEnvironmentOperations(repo, gap, fmt.Sprintf("default/bench-%d", i))
		if err := g.CloneRepo(ctx); err != nil {
			b.Fatal(err)
		}
		for _, sha := range shas {
			if _, err := g.GetShaMetadataFromGit(ctx, sha); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkCatFilePrefetchedReads(b *testing.B) {
	gap, repo, shas := setupBenchmarkRepo(b)
	ctx := b.Context()
	g := git.NewEnvironmentOperations(repo, gap, "default/bench-prefetch")
	if err := g.CloneRepo(ctx); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		if err := g.LoadCommits(ctx, shas...); err != nil {
			b.Fatal(err)
		}
		for _, sha := range shas {
			if _, err := g.GetShaMetadataFromGit(ctx, sha); err != nil {
				b.Fatal(err)
			}
		}
	}
}
