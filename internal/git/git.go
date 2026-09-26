// Package git provides operations for managing Git repositories.
//
// # Clones
//
// EnvironmentOperations interacts with a single on-disk clone of a repository. Each clone is keyed by
// repo URL + a caller-supplied identity (see NewEnvironmentOperations), so every distinct identity
// gets its own clone. Operations that do not need a clone are implemented as
// package-level functions that accept a GitOperationsProvider and a GitRepository (for example
// LsRemote); prefer those when no clone is required, as they hold no state.
//
// # Concurrency
//
// EnvironmentOperations is NOT safe for concurrent use within a single identity. Its methods shell
// out to git against a shared working copy (the .git index, HEAD, refs, FETCH_HEAD and the object
// store), so concurrent calls for the same identity can corrupt that state or compute a result from
// a mix of versions. Callers MUST serialize all operations for a given identity. (Callers that
// already process one owner at a time, such as a controller whose work queue serializes reconciles
// per object, get this for free.)
//
// Because each identity has its own clone, EnvironmentOperations for DIFFERENT identities are
// independent and may be used concurrently. The one exception is remote operations: two identities
// targeting the same repo and branch (for example two owners pushing the same branch) can lose a
// race on the remote ref. That failure is transient and non-corrupting — git updates refs atomically
// and quarantines incoming objects — so retrying (re-fetching and recomputing) eventually succeeds.
//
// The package-level functions that do not use a clone (LsRemote, AddTrailerToCommitMessage,
// ParseTrailersFromMessage) are concurrency-safe.
//
// # Clone state invariant
//
// Operations leave the clone in a "resting state" on return (success or error): an empty
// `git status --porcelain`, no in-progress markers (.git/MERGE_HEAD, CHERRY_PICK_HEAD,
// rebase-merge, rebase-apply), and a HEAD that resolves to a commit. In practice every operation
// here satisfies this trivially, because none of them mutate the clone's index, worktree, or HEAD:
//
//   - Read operations resolve everything from refs and the object DB (rev-parse, ls-tree, show,
//     cat-file, log, notes, rev-list, merge-tree --write-tree). They work even on an otherwise dirty
//     clone and never write to the index/worktree/HEAD.
//   - The merges (MergeWithOursStrategy, MergeWithOursStrategyForPath) and RestoreActiveBranch
//     build their result entirely in the object DB — commit-tree, and a temporary index
//     (GIT_INDEX_FILE) plus read-tree/write-tree/commit-tree when the result is path-scoped —
//     then push the computed commit straight to the remote ref. They never check out a branch,
//     so they cannot be wedged by, nor leave behind, a dirty worktree or a half-finished merge.
//
// The clone's working tree therefore stays exactly as CloneRepo left it for the life of the clone.
//
// MAINTAINER NOTE: new code MUST NOT introduce worktree-mutating git operations (checkout, merge,
// reset, add, commit, rm against the real index/worktree). Prefer object-DB plumbing as the merges
// do; if a worktree mutation is ever truly unavoidable, it MUST restore the resting state before
// returning (on success AND error) and be covered by an invariant test (see the merge specs that
// assert HEAD and `git status --porcelain` are unchanged across a merge).
//
// Note on freshness preconditions (a separate concern from the resting-state invariant): operations
// that read a specific ref/SHA/note assume the relevant objects were already fetched. HasConflict
// and the merges assume origin/<active> and origin/<proposed> were fetched by an earlier
// GetBranchSha; SHA-based readers (GetShaMetadataFromGit, GetShaMetadataFromFile,
// GetRevListFirstParent, GetTrailers) assume the commit is present; GetHydratorNote assumes
// FetchNotes ran. These are forward ordering requirements satisfied by the controller's call
// sequence; they are documented per method but not enforced here.
//
// # Future work
//
// The per-identity-clone model trades disk space for safety. In the future the library could be made
// concurrency-safe within a single identity (for example by serializing access to each clone with a
// lock), which would in turn allow multiple identities for the same repo to share one clone to save
// disk space. Those improvements are intentionally left for later.
package git

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/argoproj-labs/gitops-promoter/internal/utils/gitpaths"
)

// EnvironmentOperations provides methods for interacting with a specific clone of a Git repository.
//
// EnvironmentOperations is NOT safe for concurrent use within a single identity: its methods operate
// on a shared on-disk clone, so callers must serialize all operations for a given identity. Distinct
// identities use distinct clones and are independent (see the package documentation for details,
// including the remote-operation caveat).
type EnvironmentOperations struct {
	gap      scms.GitOperationsProvider
	gitRepo  *v1alpha1.GitRepository
	blobs    map[string]blobObject
	commits  map[string]commitObject
	identity string
}

// HydratorMetadata is an alias to v1alpha1.HydratorMetadata for convenience.
type HydratorMetadata = v1alpha1.HydratorMetadata

// HydratorNotesRef is the git notes reference used by hydrators to store metadata about hydrated commits.
const HydratorNotesRef = "refs/notes/hydrator.metadata"

// PromoterHistoryNotesRef is the git notes reference used by the ChangeTransferPolicy controller to store
// promotion-history metadata (the commit message trailers) on merge commits at pull request finalization.
// This keeps history reconstructable even when the SCM rewrites the merge commit message (e.g. squash merges
// or merges performed directly on the SCM).
const PromoterHistoryNotesRef = "refs/notes/promoter.history"

// MaxHydratorNoteFirstParentWalk bounds how many first-parent commits are inspected when the
// proposed branch tip has no hydrator note. Ours-merge conflict resolution adds one commit on
// top of the hydrated tip; a modest limit covers that without scanning deep history.
const MaxHydratorNoteFirstParentWalk = 32

// gitBin is resolved once at package init. CommandContext("git", ...) calls LookPath on every
// invocation; passing the absolute path skips that walk. Missing git is a process configuration
// error, so we panic instead of failing every later command.
var gitBin = mustLookPathGit()

func mustLookPathGit() string {
	path, err := exec.LookPath("git")
	if err != nil {
		panic("git executable not found: " + err.Error())
	}
	return path
}

func gitCommandContext(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, gitBin, args...)
}

// NewEnvironmentOperations creates a new EnvironmentOperations instance. The identity parameter is an opaque,
// caller-supplied identifier (for example the owning object's namespace/name); together with the repo URL it forms
// the clone key, so each identity gets its own on-disk clone. Each identity corresponds to a single environment, so
// the active branch is not part of the key. Callers must serialize operations for a given identity.
func NewEnvironmentOperations(gitRepo *v1alpha1.GitRepository, gap scms.GitOperationsProvider, identity string) *EnvironmentOperations {
	return &EnvironmentOperations{
		gap:      gap,
		gitRepo:  gitRepo,
		identity: identity,
		blobs:    make(map[string]blobObject),
		commits:  make(map[string]commitObject),
	}
}

// cloneKey returns the gitpaths key identifying this environment's on-disk clone.
//
// The key is the repo URL plus the caller-supplied identity, so each identity gets its own clone. This keeps
// concurrent callers that share a repository from interleaving local git operations on a shared working copy.
func (g *EnvironmentOperations) cloneKey() gitpaths.Key {
	return gitpaths.Key{
		RepoURL:  g.gap.GetGitHttpsRepoUrl(*g.gitRepo),
		Identity: g.identity,
	}
}

// ClonePath returns the on-disk path of this environment's registered clone, or "" if it has not been cloned.
//
// ClonePath is concurrency-safe: it only reads from the process-wide clone registry (a sync.Map).
func (g *EnvironmentOperations) ClonePath() string {
	return gitpaths.Get(g.cloneKey())
}

// CloneRepo clones the gitRepo to a temporary directory if needed. Does nothing if the repo is already cloned.
func (g *EnvironmentOperations) CloneRepo(ctx context.Context) error {
	if g.ClonePath() != "" {
		// Already cloned
		return nil
	}

	logger := log.FromContext(ctx)

	path, err := os.MkdirTemp("", "*")
	if err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}
	logger.V(4).Info("Created directory", "directory", path)

	start := time.Now()
	stdout, stderr, err := g.runCmd(ctx, path, "clone", "--verbose", "--progress", "--filter=blob:none", g.gap.GetGitHttpsRepoUrl(*g.gitRepo), path)
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationClone, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		logger.Error(err, "Cloned repo failed", "repo", g.gap.GetGitHttpsRepoUrl(*g.gitRepo), "stdout", stdout, "stderr", stderr)
		return err
	}

	stdout, stderr, err = g.runCmd(ctx, path, "config", "pull.rebase", "false")
	if err != nil {
		logger.Error(err, "could not set git config", "stdout", stdout, "stderr", stderr)
		return err
	}
	stdout, stderr, err = g.runCmd(ctx, path, "config", "user.name", "GitOps Promoter")
	if err != nil {
		logger.Error(err, "could not set git config", "stdout", stdout, "stderr", stderr)
		return err
	}

	stdout, stderr, err = g.runCmd(ctx, path, "config", "user.email", "GitOpsPromoter@argoproj.io")
	if err != nil {
		logger.Error(err, "could not set git config", "stdout", stdout, "stderr", stderr)
		return err
	}

	logger.V(4).Info("Cloned repo successful", "repo", g.gap.GetGitHttpsRepoUrl(*g.gitRepo), "identity", g.identity)

	gitpaths.Set(g.cloneKey(), path)

	return nil
}

func buildHydratorMetadataPath(activePath string) string {
	if activePath == "" {
		return "hydrator.metadata"
	}
	return path.Join(activePath, "hydrator.metadata")
}

// MalformedHydratorMetadataError reports that a hydrator.metadata blob was present at the requested
// revision but did not parse as hydrator metadata. It is distinct both from a missing file and from
// git or infrastructure failures, so callers that intentionally degrade on unusable metadata (for
// example promotion-history note writing, which keeps its trailer snapshot instead) can match it with
// errors.AsType rather than treating it as retryable.
type MalformedHydratorMetadataError struct {
	Err error
	// Revision is the revision the blob was read from: a commit SHA or a ref such as origin/<branch>.
	Revision string
	Path     string
}

// Error implements the error interface for MalformedHydratorMetadataError.
func (e *MalformedHydratorMetadataError) Error() string {
	return fmt.Sprintf("could not unmarshal metadata file %q at revision %q: %v", e.Path, e.Revision, e.Err)
}

// Unwrap exposes the underlying decode failure.
func (e *MalformedHydratorMetadataError) Unwrap() error { return e.Err }

// GetBranchSha fetches the given branch when needed and returns the commit SHA at origin/<branch>.
//
// Before fetching, it first checks - via a cheap, live ls-remote against this same clone's
// repository - whether the branch's current remote SHA still matches lastKnownHydratedSha (the
// Hydrated SHA this same branch/identity returned on a previous, successful call). If it matches,
// the commit is guaranteed to already be present in this clone (it was fetched the last time this
// identity observed that SHA), so the network fetch is skipped and rev-parse resolves the tip from
// the existing local objects instead. This keeps the skip decision self-contained: callers cannot
// accidentally skip the fetch based on a stale probe or a SHA observed by a different clone/identity,
// since the check against the live remote happens inside this call, right before the fetch would.
//
// Pass an empty lastKnownHydratedSha (e.g. before any SHA has been observed for this branch, or when
// the caller doesn't track one) to always fetch; the probe is skipped in that case since there's
// nothing to compare against.
//
// Hydrator dry metadata for the tip is not read here; callers that need it should prefetch the tip
// (for example via LoadCommitAndMetadataBlobs) and use GetShaMetadataFromFile.
//
// Read-only: fetches the branch ref and reads from refs/object DB; never mutates the clone's
// index/worktree/HEAD.
func (g *EnvironmentOperations) GetBranchSha(ctx context.Context, branch, lastKnownHydratedSha string) (string, error) {
	logger := log.FromContext(ctx)

	skipFetch := false
	if lastKnownHydratedSha != "" {
		remoteHeads, err := LsRemote(ctx, g.gap, g.gitRepo, branch)
		if err != nil {
			logger.V(4).Info("ls-remote probe failed, falling back to unconditional fetch", "branch", branch, "error", err)
		} else {
			skipFetch = remoteHeads[branch] == lastKnownHydratedSha
		}
	}

	gitPath := g.ClonePath()
	if gitPath == "" {
		return "", fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	logger.V(4).Info("git path", "path", gitPath)

	if skipFetch {
		logger.V(4).Info("branch unchanged on remote since last reconcile, skipping fetch", "branch", branch)
	} else if err := g.FetchBranch(ctx, branch); err != nil {
		return "", err
	}

	// Get the SHA of the remote branch
	stdout, stderr, err := g.runCmd(ctx, gitPath, "rev-parse", "origin/"+branch)
	if err != nil {
		logger.Error(err, "could not get branch sha", "gitError", stderr)
		return "", fmt.Errorf("failed to get SHA for branch %q: %w", branch, err)
	}

	sha := strings.TrimSpace(stdout)
	logger.V(4).Info("Got branch sha", "branch", branch, "sha", sha)
	return sha, nil
}

// FetchBranch fetches the given branch from origin so origin/<branch> reflects the latest remote state.
//
// Read-only: updates the remote-tracking ref only; never mutates the clone's index/worktree/HEAD.
func (g *EnvironmentOperations) FetchBranch(ctx context.Context, branch string) error {
	logger := log.FromContext(ctx)
	gitPath := g.ClonePath()
	if gitPath == "" {
		return fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	start := time.Now()
	_, stderr, err := g.runCmd(ctx, gitPath, "fetch", "origin", branch)
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetch, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		logger.Error(err, "could not fetch branch", "gitError", stderr)
		return fmt.Errorf("failed to fetch branch %q: %w", branch, err)
	}
	logger.V(4).Info("Fetched branch", "branch", branch)

	return nil
}

// GetShaMetadataFromFile retrieves commit metadata from the hydrator.metadata file for a given SHA.
//
// Read-only: never mutates the clone's index/worktree/HEAD. Requires the SHA's objects to have been
// fetched.
//
// When the path is absent from the commit's tree, returns an empty CommitShaState and a nil error so
// callers can degrade gracefully (for example pre-promotion commits without activePath metadata).
// Transient read failures (network, promisor/lazy-fetch, unknown revisions) are returned as errors.
func (g *EnvironmentOperations) GetShaMetadataFromFile(ctx context.Context, sha, activePath string) (v1alpha1.CommitShaState, error) {
	logger := log.FromContext(ctx)

	if g.ClonePath() == "" {
		return v1alpha1.CommitShaState{}, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	metaPath := buildHydratorMetadataPath(activePath)
	ref := sha + ":" + metaPath
	obj, err := g.getBlob(ctx, ref)
	if err != nil {
		return v1alpha1.CommitShaState{}, err
	}
	if obj.Missing {
		// cat-file --batch reports both "path absent from tree" and "unknown SHA" as missing.
		// Only degrade when the commit itself exists; unknown revisions must stay errors.
		if g.CommitExists(ctx, sha) {
			logger.V(4).Info("hydrator metadata path not present in commit", "sha", sha, "path", metaPath)
			return v1alpha1.CommitShaState{}, nil
		}
		logger.V(4).Info("could not git cat-file blob", "sha", sha, "ref", ref)
		return v1alpha1.CommitShaState{}, fmt.Errorf("failed to read hydrator.metadata from commit %q: blob %q is missing", sha, ref)
	}
	logger.V(4).Info("Got metadata file", "sha", sha, "file", string(obj.Data))

	var hydratorFile HydratorMetadata
	err = json.Unmarshal(obj.Data, &hydratorFile)
	if err != nil {
		return v1alpha1.CommitShaState{}, &MalformedHydratorMetadataError{Revision: sha, Path: metaPath, Err: err}
	}

	// Use the HTTPS URL from the SCM provider instead of the repoURL from hydrator.metadata
	// to ensure compatibility with the UI which expects HTTP(S) URLs. ArgoCD may use SSH URLs
	// in its hydrator.metadata which won't work for creating web links.
	// Strip the .git suffix as the UI appends /commit/{sha} directly.
	httpsRepoURL := strings.TrimSuffix(g.gap.GetGitHttpsRepoUrl(*g.gitRepo), ".git")

	commitState := v1alpha1.CommitShaState{
		Sha:        hydratorFile.DrySha,
		CommitTime: hydratorFile.Date,
		RepoURL:    httpsRepoURL,
		Author:     hydratorFile.Author,
		Subject:    hydratorFile.Subject,
		Body:       hydratorFile.Body,
		References: hydratorFile.References,
	}

	return commitState, nil
}

// GetShaMetadataFromGit retrieves commit metadata by running git commands for a given SHA.
//
// Read-only: never mutates the clone's index/worktree/HEAD. Requires the SHA's commit object to have
// been fetched.
func (g *EnvironmentOperations) GetShaMetadataFromGit(ctx context.Context, sha string) (v1alpha1.CommitShaState, error) {
	if g.ClonePath() == "" {
		return v1alpha1.CommitShaState{}, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	commit, err := g.getCommit(ctx, sha)
	if err != nil {
		return v1alpha1.CommitShaState{}, fmt.Errorf("failed to get commit metadata for hydrated SHA %q: %w", sha, err)
	}
	return commit.State, nil
}

// LsRemote returns a map of branch names to SHAs for the given branches using git ls-remote.
//
// LsRemote is concurrency-safe: it queries the remote directly and uses no on-disk clone or other shared state.
func LsRemote(ctx context.Context, gap scms.GitOperationsProvider, gitRepo *v1alpha1.GitRepository, branches ...string) (map[string]string, error) {
	logger := log.FromContext(ctx)

	start := time.Now()
	args := make([]string, 0, 3+len(branches))
	args = append(args, "ls-remote", "--heads", gap.GetGitHttpsRepoUrl(*gitRepo))
	args = append(args, branches...)
	stdout, stderr, err := runCmd(ctx, gap, "", args...)
	metrics.RecordGitOperation(gitRepo, metrics.GitOperationLsRemote, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		logger.Error(err, "could not git ls-remote", "gitError", stderr)
		return nil, err
	}
	stdout = strings.TrimSpace(stdout)
	lines := strings.Split(stdout, "\n")
	if len(lines) != len(branches) {
		// Determine which branches are missing
		foundBranches := make(map[string]bool)
		for _, line := range lines {
			if line == "" {
				continue
			}
			_, ref, found := strings.Cut(line, "\t")
			if found {
				branch := strings.TrimPrefix(ref, "refs/heads/")
				foundBranches[branch] = true
			}
		}
		missingBranches := make([]string, 0)
		for _, branch := range branches {
			if !foundBranches[branch] {
				missingBranches = append(missingBranches, branch)
			}
		}
		return nil, fmt.Errorf("missing branches: [%s] (these branches may not exist yet - check your PromotionStrategy to verify the environment branches have been created)", strings.Join(missingBranches, ", "))
	}
	shas := make(map[string]string, len(branches))
	for i := range lines {
		sha, ref, found := strings.Cut(lines[i], "\t")
		if !found {
			return nil, fmt.Errorf("could not parse line %q from ls-remote output", lines[i])
		}
		branch := strings.TrimPrefix(ref, "refs/heads/")
		shas[branch] = sha
	}

	logger.Info("ls-remote called", "repoUrl", gap.GetGitHttpsRepoUrl(*gitRepo), "branches", branches, "shas", shas)

	return shas, nil
}

// runCmd runs a git command in the given directory with the provided arguments and returns stdout, stderr, and error.
func (g *EnvironmentOperations) runCmd(ctx context.Context, directory string, args ...string) (string, string, error) {
	return runCmdWithEnv(ctx, g.gap, directory, nil, args...)
}

// runCmdWithEnv is like runCmd but sets additional environment variables (for example
// GIT_INDEX_FILE) on top of the standard auth env. It is used by the plumbing-based merges, which
// build trees in a temporary index so the clone's real index and worktree are never touched.
func (g *EnvironmentOperations) runCmdWithEnv(ctx context.Context, directory string, extraEnv []string, args ...string) (string, string, error) {
	return runCmdWithEnv(ctx, g.gap, directory, extraEnv, args...)
}

// runCmd runs a git command with the provided arguments and returns stdout, stderr, and error.
func runCmd(ctx context.Context, gap scms.GitOperationsProvider, directory string, args ...string) (string, string, error) {
	return runCmdWithEnv(ctx, gap, directory, nil, args...)
}

// proxyRelatedEnvVars returns proxy/TLS env vars from the current process so git subprocesses
// honor HTTPS_PROXY and GIT_SSL_CAINFO when the controller is run behind an MITM proxy.
// cmd.Env replaces the entire child environment; without this, git bypasses the proxy.
func proxyRelatedEnvVars() []string {
	var out []string
	for _, key := range []string{
		"HTTPS_PROXY", "https_proxy",
		"HTTP_PROXY", "http_proxy",
		"NO_PROXY", "no_proxy",
		"GIT_SSL_CAINFO", "SSL_CERT_FILE",
	} {
		if v := os.Getenv(key); v != "" {
			out = append(out, key+"="+v)
		}
	}
	return out
}

// gitChildEnv is the environment for git subprocesses. cmd.Env replaces the child environment
// entirely, so auth, PATH, and proxy/TLS vars from this process must be copied explicitly.
func gitChildEnv(user, token string, extraEnv []string) []string {
	env := []string{
		"GIT_ASKPASS=promoter_askpass.sh", // Needs to be on path
		"GIT_USERNAME=" + user,
		"GIT_PASSWORD=" + token,
		"PATH=" + os.Getenv("PATH"),
		"GIT_TERMINAL_PROMPT=0",
	}
	env = append(env, proxyRelatedEnvVars()...)
	return append(env, extraEnv...)
}

// runCmdWithEnv runs a git command, appending extraEnv to the standard auth environment, and
// returns stdout, stderr, and error.
func runCmdWithEnv(ctx context.Context, gap scms.GitOperationsProvider, directory string, extraEnv []string, args ...string) (string, string, error) {
	return runCmdWithEnvAndStdin(ctx, gap, directory, extraEnv, nil, args...)
}

// runCmdWithEnvAndStdin is the single place git subprocesses are run. It is like runCmdWithEnv but
// feeds the command stdin, which the batched readers (cat-file --batch, log --stdin) use to pass
// their request list. A nil stdin means the command reads from /dev/null.
func runCmdWithEnvAndStdin(ctx context.Context, gap scms.GitOperationsProvider, directory string, extraEnv []string, stdin io.Reader, args ...string) (string, string, error) {
	user, err := gap.GetUser(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to get user: %w", err)
	}

	token, err := gap.GetToken(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to get token: %w", err)
	}

	cmd := gitCommandContext(ctx, args...)
	cmd.Env = gitChildEnv(user, token, extraEnv)
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	cmd.Stdin = stdin
	cmd.Dir = directory

	if err = cmd.Start(); err != nil {
		return "", "failed to start", fmt.Errorf("failed to start git command: %w", err)
	}

	if err = cmd.Wait(); err != nil {
		stdErr := stderrBuf.String()
		if stdErr != "" {
			return stdoutBuf.String(), stdErr, fmt.Errorf("%w: %s", err, stdErr)
		}
		return stdoutBuf.String(), stdErr, err
	}

	return stdoutBuf.String(), stderrBuf.String(), nil
}

// HasConflict checks if there is a merge conflict between the proposed branch and the active branch using git merge-tree.
//
// Read-only: uses merge-tree --write-tree, a stateless check that writes only loose objects and never
// mutates the clone's index/worktree/HEAD. Requires origin/<active> and origin/<proposed> to have
// been fetched (GetBranchSha earlier in the reconcile).
func (g *EnvironmentOperations) HasConflict(ctx context.Context, proposedBranch, activeBranch string) (bool, error) {
	logger := log.FromContext(ctx)
	repoPath := g.ClonePath()

	// Use git merge-tree --write-tree to perform a stateless merge check
	// With --write-tree, git exits with code 1 if conflicts exist, and writes conflict info to stdout
	stdout, stderr, err := g.runCmd(ctx, repoPath, "merge-tree", "--write-tree", "origin/"+activeBranch, "origin/"+proposedBranch)
	if err != nil {
		// Unrelated histories cannot be inspected by merge-tree, but they need the same
		// resolution as a content conflict: create an ours-style merge commit on proposed
		// that keeps its tree and records both branch tips as parents. That commit establishes
		// the merge-base required by the subsequent pull request.
		//
		// NOTE: we're intentionally taking on the risk of blowing away someone's unrelated
		// proposed branch contents when adopted by Promoter. While that's always a possibility,
		// the likelihood that we're making a mistake is higher with two branches that don't
		// even share a history.
		if strings.Contains(stderr, "refusing to merge unrelated histories") {
			logger.Info("Unrelated branch histories detected via merge-tree --write-tree", "proposedBranch", proposedBranch, "activeBranch", activeBranch)
			return true, nil
		}
		// Exit code 1 with conflict info means conflicts were detected.
		if strings.Contains(stdout, "CONFLICT") {
			logger.V(4).Info("Merge conflict detected via merge-tree --write-tree", "proposedBranch", proposedBranch, "activeBranch", activeBranch)
			return true, nil
		}
		// Some other error occurred
		logger.Error(err, "could not run merge-tree --write-tree", "proposedBranch", proposedBranch, "activeBranch", activeBranch, "stdout", stdout, "stderr", stderr)
		return false, fmt.Errorf("failed to run merge-tree for branches %q and %q: %w", activeBranch, proposedBranch, err)
	}

	// Exit code 0 means clean merge - stdout contains the resulting tree SHA
	logger.V(4).Info("No merge conflicts detected via merge-tree --write-tree", "proposedBranch", proposedBranch, "activeBranch", activeBranch, "mergeTreeSHA", strings.TrimSpace(stdout))
	return false, nil
}

// MergeWithOursStrategy merges the active branch into the proposed branch using the "ours" strategy
// and pushes the result to the proposed branch.
//
// Operates on the object DB only: it builds the merge commit with commit-tree and pushes it directly,
// never checking out or otherwise mutating the clone's worktree/index/HEAD (see the package "Clone
// state invariant" docs). Requires origin/<proposed> and origin/<active> to have been fetched
// (GetBranchSha earlier in the reconcile).
func (g *EnvironmentOperations) MergeWithOursStrategy(ctx context.Context, proposedBranch, activeBranch string) error {
	logger := log.FromContext(ctx)
	gitPath := g.ClonePath()

	proposedRef := "origin/" + proposedBranch
	activeRef := "origin/" + activeBranch

	// The "ours" strategy keeps proposed's tree wholesale and records active as a second parent. We
	// build that commit directly from the already-fetched refs, so nothing is checked out and the
	// clone's worktree/index are never touched.
	treeSha, stderr, err := g.runCmd(ctx, gitPath, "rev-parse", proposedRef+"^{tree}")
	if err != nil {
		logger.Error(err, "Failed to resolve proposed tree", "proposedBranch", proposedBranch, "stderr", stderr)
		return fmt.Errorf("failed to resolve tree for branch %q: %w (stderr: %s)", proposedBranch, err, stderr)
	}
	treeSha = strings.TrimSpace(treeSha)

	commitMessage := fmt.Sprintf("Merge %s into %s (ours)", activeBranch, proposedBranch)
	commitSha, err := g.commitTree(ctx, treeSha, []string{proposedRef, activeRef}, commitMessage)
	if err != nil {
		logger.Error(err, "Failed to create merge commit", "proposedBranch", proposedBranch, "activeBranch", activeBranch)
		return fmt.Errorf("failed to create 'ours' merge commit for branch %q: %w", proposedBranch, err)
	}

	// Push the computed commit straight to the remote proposed ref; no local branch, no checkout.
	_, stderr, err = g.runCmd(ctx, gitPath, "push", "origin", commitSha+":refs/heads/"+proposedBranch)
	if err != nil {
		logger.Error(err, "Failed to push merged branch", "proposedBranch", proposedBranch, "activeBranch", activeBranch, "stderr", stderr)
		return fmt.Errorf("failed to push merged branch %q: %w (stderr: %s)", proposedBranch, err, stderr)
	}

	logger.Info("Successfully merged branches with 'ours' strategy", "proposedBranch", proposedBranch, "activeBranch", activeBranch)
	return nil
}

// MergeWithOursStrategyForPath resolves conflicts by taking proposed branch content only within activePath and
// active branch content everywhere else, then pushes the result to the proposed branch.
//
// Operates on the object DB only: it assembles the resolved tree in a temporary index (via
// GIT_INDEX_FILE), creates the merge commit with commit-tree, and pushes it directly. The clone's
// real index/worktree/HEAD are never touched, so this cannot be wedged by — or leave behind — a
// dirty worktree or an in-progress merge (see the package "Clone state invariant" docs). Requires
// origin/<proposed> and origin/<active> to have been fetched (GetBranchShas earlier in the reconcile).
func (g *EnvironmentOperations) MergeWithOursStrategyForPath(ctx context.Context, proposedBranch, activeBranch, activePath string) error {
	logger := log.FromContext(ctx)
	gitPath := g.ClonePath()

	proposedRef := "origin/" + proposedBranch
	activeRef := "origin/" + activeBranch

	// Active wins outside activePath, proposed wins inside it (including deletions).
	treeSha, err := g.overlayPathTree(ctx, activeRef, proposedRef, activePath)
	if err != nil {
		logger.Error(err, "Failed to build path-scoped tree", "proposedBranch", proposedBranch, "activeBranch", activeBranch, "activePath", activePath)
		return fmt.Errorf("failed to build path-scoped tree for branch %q: %w", proposedBranch, err)
	}

	// Parents are [proposed, active], preserving the "ours"-style topology so the subsequent SCM
	// merge stays clean.
	commitSha, err := g.commitTree(ctx, treeSha, []string{proposedRef, activeRef}, "Resolve conflicts for "+activePath)
	if err != nil {
		logger.Error(err, "Failed to create path-scoped merge commit", "proposedBranch", proposedBranch, "activeBranch", activeBranch, "activePath", activePath)
		return fmt.Errorf("failed to create path-scoped merge commit for branch %q: %w", proposedBranch, err)
	}

	// Push the computed commit straight to the remote proposed ref; no local branch, no checkout.
	_, stderr, err := g.runCmd(ctx, gitPath, "push", "origin", commitSha+":refs/heads/"+proposedBranch)
	if err != nil {
		logger.Error(err, "Failed to push merged branch", "proposedBranch", proposedBranch, "activeBranch", activeBranch, "stderr", stderr)
		return fmt.Errorf("failed to push merged branch %q: %w (stderr: %s)", proposedBranch, err, stderr)
	}

	logger.Info("Successfully merged branches with path-scoped strategy", "proposedBranch", proposedBranch, "activeBranch", activeBranch, "activePath", activePath)
	return nil
}

// GetRevListFirstParent retrieves the first-parent commit SHAs starting at revision using git rev-list.
// revision may be a branch ref (e.g. origin/main) or a commit SHA.
//
// Read-only: never mutates the clone's index/worktree/HEAD. Requires the revision's commits to have
// been fetched.
func (g *EnvironmentOperations) GetRevListFirstParent(ctx context.Context, revision string, maxCount int) ([]string, error) {
	logger := log.FromContext(ctx)

	gitPath := g.ClonePath()
	if gitPath == "" {
		return nil, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	args := make([]string, 0, 4)
	args = append(args, "rev-list", "--first-parent")
	args = append(args, "--max-count="+strconv.Itoa(maxCount))
	args = append(args, revision)

	stdout, stderr, err := g.runCmd(ctx, gitPath, args...)
	if err != nil {
		logger.Error(err, "could not get rev-list first parent", "gitError", stderr, "revision", revision)
		return nil, fmt.Errorf("failed to get rev-list first parent for %q: %w", revision, err)
	}

	if strings.TrimSpace(stdout) == "" {
		return nil, nil
	}

	return strings.Split(strings.TrimSpace(stdout), "\n"), nil
}

// AddTrailerToCommitMessage adds a trailer to a commit message using git interpret-trailers.
// This ensures we follow Git's exact trailer conventions and formatting rules.
// The trailer will be appended at the end of the trailer block.
//
// AddTrailerToCommitMessage is concurrency-safe: it operates only on the provided message via stdin and uses no clone.
//
// Note: We use git interpret-trailers instead of manually parsing/formatting trailers to ensure
// we follow Git's exact trailer conventions and formatting rules. While git interpret-trailers
// doesn't provide a way to place one trailer directly after another specific trailer (the --where
// flag only accepts general positions like 'after', 'before', 'start', 'end' relative to ALL trailers,
// not a specific one), it's still the most reliable approach. The alternative would be maintaining
// complex custom parsing logic, which is error-prone and doesn't handle all of Git's trailer edge cases.
func AddTrailerToCommitMessage(ctx context.Context, commitMessage, trailerKey, trailerValue string) (string, error) {
	trailerLine := fmt.Sprintf("%s: %s", trailerKey, trailerValue)

	cmd := gitCommandContext(ctx, "interpret-trailers", "--trailer", trailerLine)
	cmd.Stdin = strings.NewReader(commitMessage)

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to run git interpret-trailers: %w (stderr: %s)", err, stderrBuf.String())
	}

	return strings.TrimSpace(stdoutBuf.String()), nil
}

// FetchNotes fetches the git notes from the remote repository.
//
// Read-only: updates the notes refs only; never mutates the clone's index/worktree/HEAD.
func (g *EnvironmentOperations) FetchNotes(ctx context.Context) error {
	// Each ref is fetched in its own invocation because a multi-refspec fetch fails wholesale when any
	// single ref is missing, and either ref can legitimately be absent.
	for _, ref := range []string{HydratorNotesRef, PromoterHistoryNotesRef} {
		if err := g.fetchNotesRef(ctx, ref); err != nil {
			return err
		}
	}
	return nil
}

// fetchNotesRef fetches a single notes ref from origin, tolerating a missing remote ref.
//
// Read-only: updates the notes ref only; never mutates the clone's index/worktree/HEAD.
func (g *EnvironmentOperations) fetchNotesRef(ctx context.Context, ref string) error {
	logger := log.FromContext(ctx)
	gitPath := g.ClonePath()
	if gitPath == "" {
		return fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	// Fetch the notes ref from origin. We use + to force update in case of divergence.
	start := time.Now()
	_, stderr, err := g.runCmd(ctx, gitPath, "fetch", "origin", "+"+ref+":"+ref)
	if err != nil {
		// Notes ref might not exist yet, which is fine
		if strings.Contains(stderr, "couldn't find remote ref") {
			metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetchNotes, metrics.GitOperationResultSuccess, time.Since(start))
			logger.V(4).Info("Git notes ref does not exist on remote", "ref", ref)
			return nil
		}
		metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetchNotes, metrics.GitOperationResultFailure, time.Since(start))
		logger.Error(err, "Failed to fetch git notes", "stderr", stderr)
		return fmt.Errorf("failed to fetch git notes for ref %q: %w", ref, err)
	}
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetchNotes, metrics.GitOperationResultSuccess, time.Since(start))

	logger.V(4).Info("Fetched git notes", "ref", ref)
	return nil
}

// GetHydratorNote reads the hydrator git note for a given commit SHA.
// Returns an empty HydratorMetadata if no note exists for the commit.
//
// Read-only: never mutates the clone's index/worktree/HEAD. Requires FetchNotes to have run.
func (g *EnvironmentOperations) GetHydratorNote(ctx context.Context, sha string) (*HydratorMetadata, error) {
	logger := log.FromContext(ctx)
	gitPath := g.ClonePath()
	if gitPath == "" {
		return nil, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	stdout, stderr, err := g.runCmd(ctx, gitPath, "notes", "--ref="+HydratorNotesRef, "show", sha)
	if err != nil {
		// No note for this commit is not an error - git outputs "error: no note found for object <sha>"
		if strings.Contains(strings.ToLower(stderr), "no note found") {
			logger.V(4).Info("No git note found for commit", "sha", sha)
			return nil, nil
		}
		logger.Error(err, "Failed to read git note", "sha", sha, "stderr", stderr)
		return nil, fmt.Errorf("failed to read git note for sha %q: %w", sha, err)
	}

	var note HydratorMetadata
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &note); err != nil {
		logger.V(4).Info("Failed to parse git note as JSON, ignoring", "sha", sha, "content", stdout, "error", err)
		return nil, nil
	}

	logger.V(4).Info("Got hydrator note", "sha", sha, "note", note)
	return &note, nil
}

// GetHistoryNote reads the promotion-history git note for a given commit SHA from PromoterHistoryNotesRef.
// The note payload is a JSON-encoded trailers map (the same shape ParseTrailersFromMessage returns).
// Returns (nil, nil) when no note exists for the commit or the note is not valid JSON.
//
// Read-only: never mutates the clone's index/worktree/HEAD. Requires FetchNotes to have run.
func (g *EnvironmentOperations) GetHistoryNote(ctx context.Context, sha string) (map[string][]string, error) {
	logger := log.FromContext(ctx)
	gitPath := g.ClonePath()
	if gitPath == "" {
		return nil, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	stdout, stderr, err := g.runCmd(ctx, gitPath, "notes", "--ref="+PromoterHistoryNotesRef, "show", sha)
	if err != nil {
		// No note for this commit is not an error - git outputs "error: no note found for object <sha>"
		if strings.Contains(strings.ToLower(stderr), "no note found") {
			logger.V(4).Info("No history note found for commit", "sha", sha)
			return nil, nil
		}
		logger.Error(err, "Failed to read history note", "sha", sha, "stderr", stderr)
		return nil, fmt.Errorf("failed to read history note for sha %q: %w", sha, err)
	}

	var trailers map[string][]string
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &trailers); err != nil {
		logger.V(4).Info("Failed to parse history note as JSON, ignoring", "sha", sha, "content", stdout, "error", err)
		return nil, nil
	}

	logger.V(4).Info("Got history note", "sha", sha, "trailers", trailers)
	return trailers, nil
}

// setHistoryNoteMaxAttempts bounds the fetch/add/push retry loop in SetHistoryNote. The notes ref is shared
// by every clone of the repository, so concurrent writers can race on the push; each retry re-fetches the
// ref and re-applies the note on top of the latest remote state.
const setHistoryNoteMaxAttempts = 3

// setHistoryNoteRetryBaseDelay is the base delay before each in-loop retry after a rejected notes push.
// Actual sleep is Jitter(base*attempt, 1.0): ~50–100ms before attempt 2, ~100–200ms before attempt 3
// (~150–300ms total sleep when all three push attempts are rejected).
const setHistoryNoteRetryBaseDelay = 50 * time.Millisecond

// SetHistoryNote attaches (or overwrites) the promotion-history note on the given commit SHA and pushes
// PromoterHistoryNotesRef to origin. The payload is JSON-encoded; GetHistoryNote is the reader.
// Retries on non-fast-forward pushes since concurrent clones of the same repository share the remote ref.
//
// Writes only the notes ref and its objects; never mutates the clone's index/worktree/HEAD.
func (g *EnvironmentOperations) SetHistoryNote(ctx context.Context, sha string, payload map[string][]string) error {
	logger := log.FromContext(ctx)
	gitPath := g.ClonePath()
	if gitPath == "" {
		return fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal history note payload: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= setHistoryNoteMaxAttempts; attempt++ {
		// Force-fetch deliberately discards any local note state so the add below re-applies on top of the
		// latest remote state.
		if err := g.fetchNotesRef(ctx, PromoterHistoryNotesRef); err != nil {
			return err
		}

		// -f overwrites an existing note, which keeps retried reconciles idempotent.
		_, stderr, err := g.runCmd(ctx, gitPath, "notes", "--ref="+PromoterHistoryNotesRef, "add", "-f", "-m", string(payloadJSON), sha)
		if err != nil {
			logger.Error(err, "Failed to add history note", "sha", sha, "stderr", stderr)
			return fmt.Errorf("failed to add history note for sha %q: %w", sha, err)
		}

		start := time.Now()
		_, stderr, err = g.runCmd(ctx, gitPath, "push", "origin", PromoterHistoryNotesRef+":"+PromoterHistoryNotesRef)
		metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationPushNotes, metrics.GitOperationResultFromError(err), time.Since(start))
		if err == nil {
			logger.V(4).Info("Pushed history note", "sha", sha, "attempt", attempt)
			return nil
		}

		lastErr = fmt.Errorf("failed to push history note for sha %q: %w", sha, err)
		if !isRetryableHistoryNotePushStderr(stderr) {
			logger.Error(err, "Failed to push history note", "sha", sha, "stderr", stderr)
			return lastErr
		}
		logger.V(4).Info("History note push rejected, retrying", "sha", sha, "attempt", attempt, "stderr", stderr)
		if attempt < setHistoryNoteMaxAttempts {
			delay := wait.Jitter(time.Duration(attempt)*setHistoryNoteRetryBaseDelay, 1.0)
			select {
			case <-ctx.Done():
				return fmt.Errorf("failed to push history note for sha %q: %w", sha, ctx.Err())
			case <-time.After(delay):
			}
		}
	}

	return fmt.Errorf("failed to push history note after %d attempts: %w", setHistoryNoteMaxAttempts, lastErr)
}

func isRetryableHistoryNotePushStderr(stderr string) bool {
	return strings.Contains(stderr, "non-fast-forward") ||
		strings.Contains(stderr, "fetch first") ||
		strings.Contains(stderr, "[rejected]") ||
		strings.Contains(stderr, "cannot lock ref") ||
		strings.Contains(stderr, "remote rejected")
}

// CommitIsAncestor reports whether ancestor is an ancestor of descendant.
// The same SHA is not an ancestor here: a pull request needs a commit that exists only on the
// proposed side. git merge-base --is-ancestor exits 1 when the relationship does not hold.
//
// Read-only. Both commits must already be present in the clone.
func (g *EnvironmentOperations) CommitIsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	if ancestor == "" || descendant == "" || ancestor == descendant {
		return false, nil
	}
	gitPath := g.ClonePath()
	if gitPath == "" {
		return false, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}
	_, _, err := g.runCmd(ctx, gitPath, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("failed to check whether %q is an ancestor of %q: %w", ancestor, descendant, err)
}

// GetCommitParents returns the parent SHAs of the given commit in order (first parent first).
//
// Read-only: never mutates the clone's index/worktree/HEAD. Requires the SHA's commit object to have
// been fetched.
func (g *EnvironmentOperations) GetCommitParents(ctx context.Context, sha string) ([]string, error) {
	gitPath := g.ClonePath()
	if gitPath == "" {
		return nil, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	stdout, stderr, err := g.runCmd(ctx, gitPath, "log", "-1", "--format=%P", sha)
	if err != nil {
		return nil, fmt.Errorf("failed to get parents for sha %q: %w (stderr: %s)", sha, err, stderr)
	}

	return strings.Fields(stdout), nil
}

// CommitExists reports whether the given SHA resolves to a commit object in the local object database.
//
// Read-only: never mutates the clone's index/worktree/HEAD.
func (g *EnvironmentOperations) CommitExists(ctx context.Context, sha string) bool {
	gitPath := g.ClonePath()
	if gitPath == "" {
		return false
	}

	_, _, err := g.runCmd(ctx, gitPath, "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// FindMatchingHydratorNote returns the hydrator note for startSha, or — when the tip has
// no note — the first note on a first-parent ancestor whose DrySha equals expectedDrySha.
// A note on the branch tip is always preferred, including note-only hydrator updates where
// hydrator.metadata still references an older dry SHA. The ancestor walk (filtered by
// expectedDrySha) covers ours-merge tips that advance past the hydrated commit without a note.
func (g *EnvironmentOperations) FindMatchingHydratorNote(ctx context.Context, startSha, expectedDrySha string, maxAncestors int) (*HydratorMetadata, error) {
	tipNote, err := g.GetHydratorNote(ctx, startSha)
	if err != nil {
		return nil, err
	}
	if tipNote != nil {
		return tipNote, nil
	}

	// Without a dry SHA from hydrator.metadata we cannot tell which ancestor note belongs to the
	// current promotion; walking would risk adopting a stale note from an earlier hydrated commit.
	if expectedDrySha == "" {
		return nil, nil
	}

	shas, err := g.GetRevListFirstParent(ctx, startSha, maxAncestors)
	if err != nil {
		return nil, err
	}

	logger := log.FromContext(ctx)
	for _, sha := range shas {
		if sha == startSha {
			continue
		}
		note, err := g.GetHydratorNote(ctx, sha)
		if err != nil {
			return nil, err
		}
		if note == nil || note.DrySha != expectedDrySha {
			continue
		}
		logger.V(4).Info("Adopted hydrator note from first-parent ancestor",
			"startSha", startSha,
			"noteSha", sha,
			"noteDrySha", note.DrySha)
		return note, nil
	}

	return nil, nil
}

// ParseTrailersFromMessage parses git trailers from a commit message using git interpret-trailers.
// Returns a map where each key can have multiple values (e.g., multiple "Signed-off-by" trailers).
//
// ParseTrailersFromMessage is concurrency-safe: it operates only on the provided message via stdin and uses no clone.
func ParseTrailersFromMessage(ctx context.Context, commitMessage string) (map[string][]string, error) {
	logger := log.FromContext(ctx)

	// Pipe the message to git interpret-trailers using stdin
	cmd := gitCommandContext(ctx, "interpret-trailers", "--only-trailers")
	cmd.Stdin = strings.NewReader(commitMessage)

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	stderr := stderrBuf.String()
	if err != nil {
		logger.Error(err, "failed to run git interpret-trailers", "stderr", stderr)
		return nil, fmt.Errorf("failed to run git interpret-trailers: %w", err)
	}
	stdout := stdoutBuf.String()

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	trailers := make(map[string][]string)
	for _, line := range lines {
		if line == "" {
			continue
		}
		if strings.Contains(line, ":") {
			key, value, found := strings.Cut(line, ":")
			if found {
				trimmedKey := strings.TrimSpace(key)
				trimmedValue := strings.TrimSpace(value)
				trailers[trimmedKey] = append(trailers[trimmedKey], trimmedValue)
			} else {
				logger.Error(fmt.Errorf("invalid trailer line: %s", line), "could not parse trailer line")
			}
		}
	}
	logger.V(4).Info("Parsed trailers from message", "trailers", trailers)
	return trailers, nil
}

// GetTrailers retrieves the trailers from the last commit in the repository using git interpret-trailers.
// Returns a map where each key can have multiple values (e.g., multiple "Signed-off-by" trailers).
//
// Read-only: never mutates the clone's index/worktree/HEAD. Requires the SHA's commit object to have
// been fetched.
func (g *EnvironmentOperations) GetTrailers(ctx context.Context, sha string) (map[string][]string, error) {
	if g.ClonePath() == "" {
		return nil, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	trailers, err := g.getTrailers(ctx, sha)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit message for sha %q: %w", sha, err)
	}

	return trailers, nil
}

// RestoreResult is what RestoreActiveBranch observed and wrote.
type RestoreResult struct {
	// ActiveSha is the commit now on the active branch. It is a new commit whose tree matches the
	// restored version, unless the active tip was already that restore.
	ActiveSha string
	// BlockedDrySha is the dry SHA from hydrator.metadata on the active tip this restore moved off
	// of. Empty when that file is absent. A repeat call reads it from the restore commit's parent,
	// which is that same tip.
	BlockedDrySha string
}

// RestoreActiveBranch makes the active branch match targetSha (its whole tree, or only activePath
// when activePath is set). The proposed branch is not read or moved. The dry SHA returned is the
// one on the active tip being moved off of, so the caller can refuse to promote that change back.
//
// The active update is a new commit parented on the current tip, not a reset, so history stays
// fast-forwardable. The commit message and the promotion-history note both carry
// Promoter-restored-from set to targetSha; the note is copied from targetSha with that key and
// Pull-request-merge-time overwritten. The note is pushed before the branch.
//
// A repeat call is a no-op when the active tip already has the restore marker for targetSha and
// the matching tree. Pushes use --force-with-lease against the tip this call observed.
//
// Operates on the object DB only. Requires CloneRepo to have run. Fetches the active branch, the
// target commit when it is not already present, and the promotion-history notes ref.
//
// The git commands run, in order (A = activeBranch, T = targetSha):
//
//	git fetch origin A                      && git rev-parse origin/A          # activeTip
//	git cat-file -e T^{commit} || git fetch origin T                           # make sure T is local
//	git fetch origin +refs/notes/hydrator.metadata:refs/notes/hydrator.metadata
//	git fetch origin +refs/notes/promoter.history:refs/notes/promoter.history
//
//	# tree to restore
//	git rev-parse --verify T^{tree}                                            # no activePath
//	# with activePath, in a throwaway index (GIT_INDEX_FILE=tmp):
//	git read-tree origin/A
//	git rm -r -f --cached --ignore-unmatch -- ':(literal)<activePath>'
//	git ls-tree T -- ':(literal)<activePath>'                                  # skip overlay if absent
//	git read-tree --prefix=<activePath>/ T:<activePath>
//	git write-tree
//
//	# already restored? compare activeTip^{tree} with that tree, then look for
//	# Promoter-restored-from: T in the history note, falling back to the commit trailers
//	git rev-parse --verify <activeTip>^{tree}
//	git notes --ref=refs/notes/promoter.history show <activeTip>
//	git log --no-walk=unsorted --stdin -z --pretty=format:... <<< <activeTip>  # fallback: message
//	git interpret-trailers --only-trailers < message                           # fallback: trailers
//
//	# otherwise create the restore commit, note, and push (note first)
//	git commit-tree <tree> -p <activeTip> -m 'Revert A to <T[:7]>
//
//	Promoter-restored-from: T'
//	git notes --ref=refs/notes/promoter.history show T                         # copy T's note
//	git notes --ref=refs/notes/promoter.history add -f -m '<json>' <restoreSha>
//	git push origin refs/notes/promoter.history:refs/notes/promoter.history
//	git push --force-with-lease=refs/heads/A:<activeTip> origin <restoreSha>:refs/heads/A
//
//	# dry SHA that was on active; on a repeat call activeTip is the restore commit, so use its parent
//	git log -1 --format=%P <activeTip>                                         # repeat call only
//	git cat-file --batch <<< '<rolledBack>:<activePath>/hydrator.metadata'     # .drySha -> BlockedDrySha
//	git cat-file -e <rolledBack>^{commit}                                      # only if that blob is missing
func (g *EnvironmentOperations) RestoreActiveBranch(ctx context.Context, activeBranch, activePath, targetSha string) (RestoreResult, error) {
	logger := log.FromContext(ctx)
	if g.ClonePath() == "" {
		return RestoreResult{}, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	activeTip, err := g.GetBranchSha(ctx, activeBranch, "")
	if err != nil {
		return RestoreResult{}, err
	}
	if err := g.ensureCommit(ctx, targetSha); err != nil {
		return RestoreResult{}, err
	}
	if err := g.FetchNotes(ctx); err != nil {
		return RestoreResult{}, fmt.Errorf("failed to fetch git notes: %w", err)
	}

	wantTree, err := g.restoreTree(ctx, "origin/"+activeBranch, targetSha, activePath)
	if err != nil {
		return RestoreResult{}, err
	}

	restoreSha := activeTip
	already, err := g.commitRestores(ctx, activeTip, targetSha, wantTree)
	if err != nil {
		return RestoreResult{}, err
	}
	if !already {
		short := targetSha
		if len(short) > 7 {
			short = short[:7]
		}
		message := fmt.Sprintf("Revert %s to %s\n\n%s: %s\n", activeBranch, short, constants.TrailerRestoredFrom, targetSha)
		restoreSha, err = g.commitTree(ctx, wantTree, []string{activeTip}, message)
		if err != nil {
			return RestoreResult{}, fmt.Errorf("failed to create restore commit for %q: %w", activeBranch, err)
		}
		// Note first, then branch. If the branch push then loses to a concurrent update, the note
		// is left on a commit that never lands, which nothing reads; the retry writes a new
		// restore commit and note. The other order could leave a restore on the active branch
		// with no note, and history would lose the pull request and checks copied from targetSha.
		if err := g.writeRestoreNote(ctx, restoreSha, targetSha); err != nil {
			return RestoreResult{}, err
		}
		if err := g.pushCommitWithLease(ctx, restoreSha, activeBranch, activeTip); err != nil {
			return RestoreResult{}, err
		}
		logger.Info("Restored active branch", "branch", activeBranch, "from", targetSha, "commit", restoreSha)
	}

	// The dry SHA to refuse is the one this restore moved off of the active branch, not whatever is
	// on proposed. A repeat call's active tip is the restore commit, whose parent is that old tip.
	rolledBack := activeTip
	if already {
		parents, err := g.GetCommitParents(ctx, activeTip)
		if err != nil {
			return RestoreResult{}, err
		}
		if len(parents) > 0 {
			rolledBack = parents[0]
		}
	}
	activeMeta, err := g.GetShaMetadataFromFile(ctx, rolledBack, activePath)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("failed to read hydrator metadata for active branch %q at %q: %w", activeBranch, rolledBack, err)
	}

	return RestoreResult{ActiveSha: restoreSha, BlockedDrySha: activeMeta.Sha}, nil
}

func (g *EnvironmentOperations) commitRestores(ctx context.Context, sha, targetSha, wantTree string) (bool, error) {
	gotTree, err := g.revParse(ctx, sha+"^{tree}")
	if err != nil {
		return false, err
	}
	if gotTree != wantTree {
		return false, nil
	}

	trailers, err := g.GetHistoryNote(ctx, sha)
	if err != nil {
		return false, fmt.Errorf("read promotion-history note for %q: %w", sha, err)
	}
	if len(trailers) == 0 {
		trailers, err = g.GetTrailers(ctx, sha)
		if err != nil {
			return false, err
		}
	}
	values := trailers[constants.TrailerRestoredFrom]
	return len(values) > 0 && values[0] == targetSha, nil
}

func (g *EnvironmentOperations) writeRestoreNote(ctx context.Context, restoreSha, targetSha string) error {
	trailers, err := g.GetHistoryNote(ctx, targetSha)
	if err != nil {
		return fmt.Errorf("read promotion-history note for %q: %w", targetSha, err)
	}
	if trailers == nil {
		trailers = map[string][]string{}
	}
	trailers[constants.TrailerRestoredFrom] = []string{targetSha}
	trailers[constants.TrailerPullRequestMergeTime] = []string{time.Now().UTC().Format(time.RFC3339)}
	if err := g.SetHistoryNote(ctx, restoreSha, trailers); err != nil {
		return fmt.Errorf("write restore note for %q: %w", restoreSha, err)
	}
	return nil
}

// restoreTree is targetSha's tree, or the active tip's tree with activePath replaced by targetSha's.
func (g *EnvironmentOperations) restoreTree(ctx context.Context, activeRef, targetSha, activePath string) (string, error) {
	if activePath == "" {
		return g.revParse(ctx, targetSha+"^{tree}")
	}
	return g.overlayPathTree(ctx, activeRef, targetSha, activePath)
}

// overlayPathTree returns baseRef's tree with path replaced by sourceRef's content at path, or with
// path removed when sourceRef has nothing there. The tree is built in a temporary index
// (GIT_INDEX_FILE) outside the clone, so the clone's real index/worktree/HEAD are never touched and
// the index cannot appear as an untracked file.
func (g *EnvironmentOperations) overlayPathTree(ctx context.Context, baseRef, sourceRef, path string) (string, error) {
	logger := log.FromContext(ctx)
	gitPath := g.ClonePath()

	tmpIndex, err := os.CreateTemp("", "promoter-index-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp index: %w", err)
	}
	tmpIndexPath := tmpIndex.Name()
	_ = tmpIndex.Close()
	defer func() {
		if rmErr := os.Remove(tmpIndexPath); rmErr != nil && !os.IsNotExist(rmErr) {
			logger.Error(rmErr, "failed to remove temp index", "path", tmpIndexPath)
		}
	}()
	indexEnv := []string{"GIT_INDEX_FILE=" + tmpIndexPath}

	if _, stderr, err := g.runCmdWithEnv(ctx, gitPath, indexEnv, "read-tree", baseRef); err != nil {
		return "", fmt.Errorf("failed to read tree %q: %w (stderr: %s)", baseRef, err, stderr)
	}

	// Drop the whole subtree first so sourceRef's version, including its deletions, replaces it.
	if _, stderr, err := g.runCmdWithEnv(ctx, gitPath, indexEnv, "rm", "-r", "-f", "--cached", "--ignore-unmatch", "--", ":(literal)"+path); err != nil {
		return "", fmt.Errorf("failed to remove %q from index: %w (stderr: %s)", path, err, stderr)
	}

	// Skip the overlay when sourceRef has no content at path; the subtree then stays removed.
	lsTreeStdout, stderr, err := g.runCmd(ctx, gitPath, "ls-tree", sourceRef, "--", ":(literal)"+path)
	if err != nil {
		return "", fmt.Errorf("failed to inspect %q on %q: %w (stderr: %s)", path, sourceRef, err, stderr)
	}
	if strings.TrimSpace(lsTreeStdout) != "" {
		if _, stderr, err := g.runCmdWithEnv(ctx, gitPath, indexEnv, "read-tree", "--prefix="+path+"/", sourceRef+":"+path); err != nil {
			return "", fmt.Errorf("failed to overlay %q from %q: %w (stderr: %s)", path, sourceRef, err, stderr)
		}
	}

	treeSha, stderr, err := g.runCmdWithEnv(ctx, gitPath, indexEnv, "write-tree")
	if err != nil {
		return "", fmt.Errorf("failed to write tree: %w (stderr: %s)", err, stderr)
	}
	return strings.TrimSpace(treeSha), nil
}

// ensureCommit makes sure sha is a commit in the clone, fetching it from origin when it is not
// present yet.
func (g *EnvironmentOperations) ensureCommit(ctx context.Context, sha string) error {
	gitPath := g.ClonePath()
	if _, _, err := g.runCmd(ctx, gitPath, "cat-file", "-e", sha+"^{commit}"); err == nil {
		return nil
	}
	if _, stderr, err := g.runCmd(ctx, gitPath, "fetch", "origin", sha); err != nil {
		return fmt.Errorf("commit %q is not available: fetch failed: %w (stderr: %s)", sha, err, stderr)
	}
	if _, stderr, err := g.runCmd(ctx, gitPath, "cat-file", "-e", sha+"^{commit}"); err != nil {
		return fmt.Errorf("%q is not a commit: %w (stderr: %s)", sha, err, stderr)
	}
	return nil
}

func (g *EnvironmentOperations) revParse(ctx context.Context, rev string) (string, error) {
	stdout, stderr, err := g.runCmd(ctx, g.ClonePath(), "rev-parse", "--verify", rev)
	if err != nil {
		return "", fmt.Errorf("failed to resolve %q: %w (stderr: %s)", rev, err, stderr)
	}
	return strings.TrimSpace(stdout), nil
}

func (g *EnvironmentOperations) commitTree(ctx context.Context, tree string, parents []string, message string) (string, error) {
	args := make([]string, 0, 2+2*len(parents)+2)
	args = append(args, "commit-tree", tree)
	for _, parent := range parents {
		args = append(args, "-p", parent)
	}
	args = append(args, "-m", message)
	stdout, stderr, err := g.runCmd(ctx, g.ClonePath(), args...)
	if err != nil {
		return "", fmt.Errorf("commit-tree: %w (stderr: %s)", err, stderr)
	}
	return strings.TrimSpace(stdout), nil
}

// pushCommitWithLease pushes commitSha to branch only if the remote branch is still expectedTip.
// commitSha is parented on expectedTip, so a plain push would already reject a branch that moved
// forward; the lease also rejects one that was rewound to an ancestor, where a plain push would
// fast-forward and put back the commits that were removed.
func (g *EnvironmentOperations) pushCommitWithLease(ctx context.Context, commitSha, branch, expectedTip string) error {
	lease := "refs/heads/" + branch + ":" + expectedTip
	refspec := commitSha + ":refs/heads/" + branch
	if _, stderr, err := g.runCmd(ctx, g.ClonePath(), "push", "--force-with-lease="+lease, "origin", refspec); err != nil {
		return fmt.Errorf("failed to push %s to %q: %w (stderr: %s)", commitSha, branch, err, stderr)
	}
	return nil
}
