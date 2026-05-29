package git_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/git"
)

// These specs guard against the most common races described in gitops-promoter#1495. Each goroutine
// models a distinct ChangeTransferPolicy (its own namespace/name identity) that targets the same
// repo + activeBranch. Because the clone key includes CTP identity, each gets its own on-disk clone,
// so concurrent reconciles cannot interleave git operations on a shared working copy. The specs
// assert the SAFE invariants this guarantees (a healthy repo, consistent reads); they would fail if
// the per-CTP clone key regressed (e.g. if identity were dropped from gitpaths.Key).
var _ = Describe("Concurrency (gitops-promoter#1495)", func() {
	var (
		s   *sharedRepo
		ctx context.Context
	)

	BeforeEach(func() {
		s = buildConflictRepo()
		ctx = context.Background()
	})

	// Heavy corruption case: multiple CTPs reconciling against the same repo + activeBranch. Each CTP
	// has its own proposed branch (so pushes target different remote refs and there is no remote-push
	// contention) and, thanks to the per-CTP clone key, its own on-disk clone. The concurrent
	// checkout -B / merge / push operations are therefore isolated and every clone stays healthy. If
	// the clones were shared, these would interleave on one .git and produce index.lock/fsck damage.
	It("keeps each CTP's clone healthy under concurrent merges", func() {
		const (
			goroutines = 8
			iterations = 3
		)

		// Each goroutine is a distinct CTP: its own identity and its own proposed branch. Create the
		// branches, then pre-clone sequentially so the concurrent phase exercises only merge races,
		// not clone or remote-push races.
		proposedBranches := make([]string, goroutines)
		for i := range proposedBranches {
			branch := fmt.Sprintf("proposed-%d", i)
			s.addProposedBranch(branch)
			proposedBranches[i] = branch
		}
		envs := make([]*git.EnvironmentOperations, goroutines)
		for i := range envs {
			envs[i] = s.newEnv(fmt.Sprintf("tenant-%d/ctp", i))
			Expect(envs[i].CloneRepo(ctx)).To(Succeed())
		}

		errs := runConcurrently(goroutines, func(i int) error {
			env := envs[i]
			proposed := proposedBranches[i]
			for range iterations {
				if _, err := env.GetBranchShas(ctx, proposed); err != nil {
					return fmt.Errorf("get proposed shas: %w", err)
				}
				if _, err := env.GetBranchShas(ctx, s.active); err != nil {
					return fmt.Errorf("get active shas: %w", err)
				}
				if err := env.MergeWithOursStrategy(ctx, proposed, s.active); err != nil {
					return fmt.Errorf("merge: %w", err)
				}
			}
			return nil
		})
		for i, err := range errs {
			Expect(err).NotTo(HaveOccurred(), "goroutine %d reconcile", i)
		}

		for i, env := range envs {
			path := env.ClonePath()
			Expect(path).NotTo(BeEmpty(), "CTP %d has no registered clone", i)
			assertHealthy(path)
		}
	})

	// Mixed-version read case: GetBranchShas fetches, then reads the tip SHA (Hydrated) and the
	// hydrator.metadata (Dry) in separate git invocations. If distinct CTPs shared a clone, another
	// goroutine's fetch could move origin/<branch> between those two reads, so a single GetBranchShas
	// would return a tip and metadata from different commits. Because each CTP identity fetches into
	// its own clone, every snapshot is self-consistent even as the remote advances.
	//
	// Each commit publishes hydrator.metadata drySha "dry-proposed-<n>" alongside its tip SHA,
	// recorded in publishedPairs. The safe invariant: any (Hydrated, Dry) a reader observes must be
	// a pair that was actually published together.
	It("does not produce mixed-version reads while the remote advances", func() {
		const (
			readers    = 12
			iterations = 6
			writes     = 12
		)

		// Each reader is a distinct CTP identity. Pre-clone sequentially so the concurrent phase
		// exercises only fetch races, not clone races.
		envs := make([]*git.EnvironmentOperations, readers)
		for i := range envs {
			envs[i] = s.newEnv(fmt.Sprintf("tenant-%d/ctp", i))
			Expect(envs[i].CloneRepo(ctx)).To(Succeed())
		}

		var (
			mu             sync.Mutex
			publishedPairs = map[string]string{} // tip SHA -> drySha published with it
			violations     []string
		)
		record := func(sha, dry string) {
			mu.Lock()
			publishedPairs[sha] = dry
			mu.Unlock()
		}
		record(strings.TrimSpace(mustGit(s.workDir, "rev-parse", s.proposed)), "dry-proposed-0")

		start := make(chan struct{})
		var wg sync.WaitGroup
		var writerErr error
		readerErrs := make([]error, readers)

		// Writer: advance the proposed tip on the remote, recording each published pair.
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for n := 1; n <= writes; n++ {
				sha, err := s.advanceProposed(n)
				if err != nil {
					writerErr = err
					return
				}
				record(sha, fmt.Sprintf("dry-proposed-%d", n))
			}
		}()

		// Readers: fetch and verify each GetBranchShas snapshot is self-consistent.
		for i := range readers {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				env := envs[i]
				for range iterations {
					if err := env.FetchNotes(ctx); err != nil {
						readerErrs[i] = err
						return
					}
					shas, err := env.GetBranchShas(ctx, s.proposed)
					if err != nil {
						readerErrs[i] = err
						return
					}
					if shas.Hydrated == "" {
						continue
					}
					mu.Lock()
					if wantDry, known := publishedPairs[shas.Hydrated]; known && shas.Dry != wantDry {
						violations = append(violations, fmt.Sprintf(
							"reader %d torn read: tip %s reported Dry %q but was published with %q",
							i, shas.Hydrated, shas.Dry, wantDry))
					}
					mu.Unlock()
				}
			}(i)
		}
		close(start)
		wg.Wait()

		Expect(writerErr).NotTo(HaveOccurred())
		for i, err := range readerErrs {
			Expect(err).NotTo(HaveOccurred(), "reader %d", i)
		}
		Expect(violations).To(BeEmpty())

		for i, env := range envs {
			path := env.ClonePath()
			Expect(path).NotTo(BeEmpty(), "CTP %d has no registered clone", i)
			assertHealthy(path)
		}
	})
})

// sharedRepo is a local bare repository with conflicting active/proposed branches.
// Each goroutine models a distinct ChangeTransferPolicy identity; because the clone key includes
// identity, multiple EnvironmentOperations against the same repo get distinct on-disk clones.
type sharedRepo struct {
	gap      *fakeGitProvider
	repo     *v1alpha1.GitRepository
	bareRepo string
	workDir  string
	base     string
	active   string
	proposed string
}

// buildConflictRepo creates a bare repo with a base commit plus "active" and "proposed" branches
// that modify the same file, so HasConflict/MergeWithOursStrategy have real work to do.
func buildConflictRepo() *sharedRepo {
	GinkgoHelper()

	bareRepo := GinkgoT().TempDir()
	workDir := GinkgoT().TempDir()

	mustGit(bareRepo, "init", "--bare")
	mustGit(workDir, "clone", bareRepo, ".")
	mustGit(workDir, "config", "user.name", "Test User")
	mustGit(workDir, "config", "user.email", "test@example.com")
	mustGit(workDir, "config", "commit.gpgsign", "false")

	const appConfig = "apps/app-one/config.yaml"
	writeFile(workDir, appConfig, "version: base\n")
	writeFile(workDir, "hydrator.metadata", `{"drySha":"base"}`)
	mustGit(workDir, "add", "-A")
	mustGit(workDir, "commit", "-m", "base")

	base := strings.TrimSpace(mustGit(workDir, "rev-parse", "--abbrev-ref", "HEAD"))
	mustGit(workDir, "push", "-u", "origin", base)

	active, proposed := "active", "proposed"
	createBranch(workDir, base, active, map[string]string{
		appConfig:           "version: active\n",
		"hydrator.metadata": `{"drySha":"dry-active"}`,
	})
	createBranch(workDir, base, proposed, map[string]string{
		appConfig:           "version: proposed\n",
		"hydrator.metadata": `{"drySha":"dry-proposed-0"}`,
	})

	// Seed a hydrator notes ref so FetchNotes performs a real force-updating fetch (otherwise it
	// returns early on "couldn't find remote ref" and exercises no concurrency).
	baseSha := strings.TrimSpace(mustGit(workDir, "rev-parse", base))
	mustGit(workDir, "notes", "--ref="+git.HydratorNotesRef, "add", "-m", `{"drySha":"base"}`, baseSha)
	mustGit(workDir, "push", "origin", git.HydratorNotesRef+":"+git.HydratorNotesRef)

	return &sharedRepo{
		gap:      &fakeGitProvider{tempDirPath: bareRepo},
		bareRepo: bareRepo,
		workDir:  workDir,
		base:     base,
		active:   active,
		proposed: proposed,
		repo: &v1alpha1.GitRepository{
			ObjectMeta: metav1.ObjectMeta{Name: "testrepo", Namespace: "default"},
			Spec: v1alpha1.GitRepositorySpec{
				GitHub: &v1alpha1.GitHubRepo{Owner: "test-owner", Name: "testrepo"},
				ScmProviderRef: v1alpha1.ScmProviderObjectReference{
					Kind: "ScmProvider",
					Name: "testprovider",
				},
			},
		},
	}
}

// newEnv returns an EnvironmentOperations for a distinct CTP identity (namespace/name) against the
// same repo + activeBranch. Because the clone key includes identity, each env gets its own on-disk
// clone.
func (s *sharedRepo) newEnv(identity string) *git.EnvironmentOperations {
	return git.NewEnvironmentOperations(s.repo, s.gap, identity)
}

// addProposedBranch creates a distinct proposed branch off base. Distinct CTPs have their own
// proposed branches, so concurrent merges push to different remote refs (no remote-push contention)
// while still sharing the clone key on repo + activeBranch.
func (s *sharedRepo) addProposedBranch(branch string) {
	GinkgoHelper()
	createBranch(s.workDir, s.base, branch, map[string]string{
		"apps/app-one/config.yaml": "version: " + branch + "\n",
		"hydrator.metadata":        fmt.Sprintf(`{"drySha":"dry-%s"}`, branch),
	})
}

// advanceProposed pushes a new commit to the proposed branch whose hydrator.metadata drySha is
// "dry-proposed-<n>", and returns the new tip SHA. It is goroutine-safe (returns errors instead of
// asserting) because it runs from a writer goroutine alongside concurrent readers.
func (s *sharedRepo) advanceProposed(n int) (string, error) {
	if err := gitErr(s.workDir, "checkout", s.proposed); err != nil {
		return "", err
	}
	files := map[string]string{
		"apps/app-one/config.yaml": fmt.Sprintf("version: proposed-%d\n", n),
		"hydrator.metadata":        fmt.Sprintf(`{"drySha":"dry-proposed-%d"}`, n),
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(s.workDir, rel), []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", rel, err)
		}
	}
	for _, args := range [][]string{
		{"add", "-A"},
		{"commit", "-m", fmt.Sprintf("advance proposed %d", n)},
		{"push", "origin", s.proposed},
	} {
		if err := gitErr(s.workDir, args...); err != nil {
			return "", err
		}
	}
	out, err := runGitCmd(s.workDir, "rev-parse", s.proposed)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func createBranch(workDir, base, branch string, files map[string]string) {
	GinkgoHelper()
	mustGit(workDir, "checkout", "-b", branch, base)
	for rel, content := range files {
		writeFile(workDir, rel, content)
	}
	mustGit(workDir, "add", "-A")
	mustGit(workDir, "commit", "-m", branch+" change")
	mustGit(workDir, "push", "-u", "origin", branch)
	mustGit(workDir, "checkout", base)
}

func writeFile(dir, relPath, content string) {
	GinkgoHelper()
	full := filepath.Join(dir, relPath)
	Expect(os.MkdirAll(filepath.Dir(full), 0o755)).To(Succeed())
	Expect(os.WriteFile(full, []byte(content), 0o644)).To(Succeed())
}

func mustGit(dir string, args ...string) string {
	GinkgoHelper()
	out, err := runGitCmd(dir, args...)
	Expect(err).NotTo(HaveOccurred(), "git %s\n%s", strings.Join(args, " "), out)
	return out
}

// gitErr runs a git command and returns a descriptive error. Safe to call from any goroutine.
func gitErr(dir string, args ...string) error {
	if out, err := runGitCmd(dir, args...); err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}

// assertHealthy fails the test if the clone has leftover lock files or fails fsck.
func assertHealthy(path string) {
	GinkgoHelper()
	for _, lock := range []string{
		filepath.Join(path, ".git", "index.lock"),
		filepath.Join(path, ".git", "HEAD.lock"),
	} {
		_, err := os.Stat(lock)
		Expect(os.IsNotExist(err)).To(BeTrue(), "stale lock file present: %s", lock)
	}
	out, err := runGitCmd(path, "fsck", "--no-progress")
	Expect(err).NotTo(HaveOccurred(), "git fsck for %s\n%s", path, out)
}

// runConcurrently releases n goroutines simultaneously via a start gate and collects their errors.
func runConcurrently(n int, fn func(i int) error) []error {
	errs := make([]error, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start
			errs[i] = fn(i)
		})
	}
	close(start)
	wg.Wait()
	return errs
}
