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
//   - The merges (MergeWithOursStrategy, MergeWithOursStrategyForPath) build their result entirely
//     in the object DB — commit-tree for the "ours" merge, and a temporary index (GIT_INDEX_FILE)
//     plus read-tree/write-tree/commit-tree for the path-scoped merge — then push the computed
//     commit straight to the remote ref. They never check out a branch, so they cannot be wedged by,
//     nor leave behind, a dirty worktree or a half-finished merge.
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
// GetBranchShas; SHA-based readers (GetShaMetadataFromGit, GetShaMetadataFromFile,
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
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/relvacode/iso8601"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils/gitpaths"
)

// EnvironmentOperations provides methods for interacting with a specific clone of a Git repository.
//
// EnvironmentOperations is NOT safe for concurrent use within a single identity: its methods operate
// on a shared on-disk clone, so callers must serialize all operations for a given identity. Distinct
// identities use distinct clones and are independent (see the package documentation for details,
// including the remote-operation caveat).
type EnvironmentOperations struct {
	gap     scms.GitOperationsProvider
	gitRepo *v1alpha1.GitRepository
	// identity is an opaque, caller-supplied identifier that, together with the repo URL, forms the clone key (see
	// cloneKey). Distinct identities get distinct on-disk clones, which isolates concurrent callers that share a
	// repository.
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

// NewEnvironmentOperations creates a new EnvironmentOperations instance. The identity parameter is an opaque,
// caller-supplied identifier (for example the owning object's namespace/name); together with the repo URL it forms
// the clone key, so each identity gets its own on-disk clone. Each identity corresponds to a single environment, so
// the active branch is not part of the key. Callers must serialize operations for a given identity.
func NewEnvironmentOperations(gitRepo *v1alpha1.GitRepository, gap scms.GitOperationsProvider, identity string) *EnvironmentOperations {
	return &EnvironmentOperations{
		gap:      gap,
		gitRepo:  gitRepo,
		identity: identity,
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

// BranchShas holds the hydrated and dry commit SHAs for a branch.
type BranchShas struct {
	// Dry is the SHA of the commit that was used as the dry source for hydration.
	Dry string
	// Hydrated is the SHA of the commit on the hydrated branch.
	Hydrated string
}

func buildHydratorMetadataPath(activePath string) string {
	if activePath == "" {
		return "hydrator.metadata"
	}
	return path.Join(activePath, "hydrator.metadata")
}

// GetBranchShas fetches the given branch and returns its hydrated and dry SHAs.
//
// Read-only: fetches the branch ref and reads from refs/object DB; never mutates the clone's
// index/worktree/HEAD.
func (g *EnvironmentOperations) GetBranchShas(ctx context.Context, branch, activePath string) (BranchShas, error) {
	logger := log.FromContext(ctx)
	gitPath := g.ClonePath()
	if gitPath == "" {
		return BranchShas{}, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	logger.V(4).Info("git path", "path", gitPath)

	// Fetch the branch to ensure we have the latest remote ref
	if err := g.FetchBranch(ctx, branch); err != nil {
		return BranchShas{}, err
	}

	// Get the SHA of the remote branch
	stdout, stderr, err := g.runCmd(ctx, gitPath, "rev-parse", "origin/"+branch)
	if err != nil {
		logger.Error(err, "could not get branch sha", "gitError", stderr)
		return BranchShas{}, fmt.Errorf("failed to get SHA for branch %q: %w", branch, err)
	}

	shas := BranchShas{}
	shas.Hydrated = strings.TrimSpace(stdout)
	logger.V(4).Info("Got hydrated branch sha", "branch", branch, "sha", shas.Hydrated)

	// Determine whether <activePath>/hydrator.metadata exists on the ref using ls-tree, which reads
	// the tree object and never consults the worktree. The metadata file legitimately may not exist
	// on this ref yet — most commonly with activePath, where <activePath>/hydrator.metadata only
	// appears on the active branch after that app's first promotion. We must not treat that normal
	// pre-promotion state as a reconcile error.
	metaPath := buildHydratorMetadataPath(activePath)
	lsTreeStdout, stderr, err := g.runCmd(ctx, gitPath, "ls-tree", "origin/"+branch, ":(literal)"+metaPath)
	if err != nil {
		logger.Error(err, "could not list metadata file", "gitError", stderr)
		return BranchShas{}, fmt.Errorf("failed to list hydrator.metadata on branch %q: %w", branch, err)
	}
	if strings.TrimSpace(lsTreeStdout) == "" {
		logger.Info("hydrator.metadata file not found", "branch", branch, "activePath", activePath)
		return shas, nil
	}

	// Get the metadata file contents directly from the remote branch. The path is known to exist on
	// the ref at this point, so this only fails on a genuine git error.
	metadataFileStdout, stderr, err := g.runCmd(ctx, gitPath, "show", "origin/"+branch+":"+metaPath)
	if err != nil {
		logger.Error(err, "could not get metadata file", "gitError", stderr)
		return BranchShas{}, fmt.Errorf("failed to read hydrator.metadata from branch %q: %w", branch, err)
	}
	logger.V(4).Info("Got metadata file", "branch", branch)

	var hydratorFile HydratorMetadata
	err = json.Unmarshal([]byte(metadataFileStdout), &hydratorFile)
	if err != nil {
		return BranchShas{}, fmt.Errorf("could not unmarshal metadata file: %w", err)
	}
	shas.Dry = hydratorFile.DrySha
	logger.V(4).Info("Got dry branch sha", "branch", branch, "sha", shas.Dry)

	return shas, nil
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
func (g *EnvironmentOperations) GetShaMetadataFromFile(ctx context.Context, sha, activePath string) (v1alpha1.CommitShaState, error) {
	logger := log.FromContext(ctx)

	gitPath := g.ClonePath()
	if gitPath == "" {
		return v1alpha1.CommitShaState{}, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	metadataFileStdout, stderr, err := g.runCmd(ctx, gitPath, "show", sha+":"+buildHydratorMetadataPath(activePath))
	if err != nil {
		logger.V(4).Info("could not git show file", "sha", sha, "gitError", stderr, "err", err)
		return v1alpha1.CommitShaState{}, nil
	}
	logger.V(4).Info("Got metadata file", "sha", sha, "file", metadataFileStdout)

	var hydratorFile HydratorMetadata
	err = json.Unmarshal([]byte(metadataFileStdout), &hydratorFile)
	if err != nil {
		return v1alpha1.CommitShaState{}, fmt.Errorf("could not unmarshal metadata file: %w", err)
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
	gitPath := g.ClonePath()
	if gitPath == "" {
		return v1alpha1.CommitShaState{}, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	commitTime, err := g.GetShaTime(ctx, sha)
	if err != nil {
		return v1alpha1.CommitShaState{}, fmt.Errorf("failed to get commit time for hydrated SHA %q: %w", sha, err)
	}

	commitAuthor, err := g.GetShaAuthor(ctx, sha)
	if err != nil {
		return v1alpha1.CommitShaState{}, fmt.Errorf("failed to get commit author for hydrated SHA %q: %w", sha, err)
	}

	commitSubject, err := g.GetShaSubject(ctx, sha)
	if err != nil {
		return v1alpha1.CommitShaState{}, fmt.Errorf("failed to get commit subject for hydrated SHA %q: %w", sha, err)
	}

	commitBody, err := g.GetShaBody(ctx, sha)
	if err != nil {
		return v1alpha1.CommitShaState{}, fmt.Errorf("failed to get commit body for hydrated SHA %q: %w", sha, err)
	}

	commitState := v1alpha1.CommitShaState{
		Sha:        sha,
		CommitTime: commitTime,
		Author:     commitAuthor,
		Subject:    commitSubject,
		Body:       commitBody,
	}

	return commitState, nil
}

// GetShaBody retrieves the body of a commit given its SHA.
func (g *EnvironmentOperations) GetShaBody(ctx context.Context, sha string) (string, error) {
	logger := log.FromContext(ctx)

	gitPath := g.ClonePath()
	if gitPath == "" {
		return "", fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	stdout, stderr, err := g.runCmd(ctx, gitPath, "show", "-s", "--format=%b", sha)
	if err != nil {
		logger.Error(err, "could not git show", "gitError", stderr)
		return "", fmt.Errorf("failed to get commit body for sha %q: %w", sha, err)
	}
	logger.V(4).Info("Got sha body", "sha", sha, "body", stdout)

	return strings.TrimSpace(stdout), nil
}

// GetShaAuthor retrieves the author of a commit given its SHA.
func (g *EnvironmentOperations) GetShaAuthor(ctx context.Context, sha string) (string, error) {
	logger := log.FromContext(ctx)
	gitPath := g.ClonePath()
	if gitPath == "" {
		return "", fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	stdout, stderr, err := g.runCmd(ctx, gitPath, "show", "-s", "--format=%an", sha)
	if err != nil {
		logger.Error(err, "could not git show", "gitError", stderr)
		return "", fmt.Errorf("failed to get author for sha %q: %w", sha, err)
	}
	logger.V(4).Info("Got sha author", "sha", sha, "author", stdout)

	return strings.TrimSpace(stdout), nil
}

// GetShaSubject retrieves the subject of a commit given its SHA.
func (g *EnvironmentOperations) GetShaSubject(ctx context.Context, sha string) (string, error) {
	logger := log.FromContext(ctx)
	gitPath := g.ClonePath()
	if gitPath == "" {
		return "", fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	stdout, stderr, err := g.runCmd(ctx, gitPath, "show", "-s", "--format=%s", sha)
	if err != nil {
		logger.Error(err, "could not git show", "gitError", stderr)
		return "", fmt.Errorf("failed to get commit subject for sha %q: %w", sha, err)
	}
	logger.V(4).Info("Got sha subject", "sha", sha, "subject", stdout)

	return strings.TrimSpace(stdout), nil
}

// GetShaTime retrieves the commit time of a commit given its SHA.
func (g *EnvironmentOperations) GetShaTime(ctx context.Context, sha string) (v1.Time, error) {
	logger := log.FromContext(ctx)
	gitPath := g.ClonePath()
	if gitPath == "" {
		return v1.Time{}, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	stdout, stderr, err := g.runCmd(ctx, gitPath, "show", "-s", "--format=%cI", sha)
	if err != nil {
		logger.Error(err, "could not git show", "gitError", stderr)
		return v1.Time{}, err
	}
	logger.V(4).Info("Got sha time", "sha", sha, "time", stdout)

	trimmedStdout := strings.TrimSpace(stdout)
	cTime, err := iso8601.ParseString(trimmedStdout)
	if err != nil {
		return v1.Time{}, fmt.Errorf("failed to parse time %q: %w", trimmedStdout, err)
	}

	return v1.Time{Time: cTime}, nil
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

// runCmdWithEnv runs a git command, appending extraEnv to the standard auth environment, and
// returns stdout, stderr, and error.
func runCmdWithEnv(ctx context.Context, gap scms.GitOperationsProvider, directory string, extraEnv []string, args ...string) (string, string, error) {
	user, err := gap.GetUser(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to get user: %w", err)
	}

	token, err := gap.GetToken(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to get token: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append([]string{
		"GIT_ASKPASS=promoter_askpass.sh", // Needs to be on path
		"GIT_USERNAME=" + user,
		"GIT_PASSWORD=" + token,
		"PATH=" + os.Getenv("PATH"),
		"GIT_TERMINAL_PROMPT=0",
	}, proxyRelatedEnvVars()...)
	cmd.Env = append(cmd.Env, extraEnv...)
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
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
// been fetched (GetBranchShas earlier in the reconcile).
func (g *EnvironmentOperations) HasConflict(ctx context.Context, proposedBranch, activeBranch string) (bool, error) {
	logger := log.FromContext(ctx)
	repoPath := g.ClonePath()

	// Use git merge-tree --write-tree to perform a stateless merge check
	// With --write-tree, git exits with code 1 if conflicts exist, and writes conflict info to stdout
	stdout, stderr, err := g.runCmd(ctx, repoPath, "merge-tree", "--write-tree", "origin/"+activeBranch, "origin/"+proposedBranch)
	if err != nil {
		// Exit code 1 with conflict info in stderr means conflicts were detected
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
// (GetBranchShas earlier in the reconcile).
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
	commitSha, stderr, err := g.runCmd(ctx, gitPath, "commit-tree", treeSha, "-p", proposedRef, "-p", activeRef, "-m", commitMessage)
	if err != nil {
		logger.Error(err, "Failed to create merge commit", "proposedBranch", proposedBranch, "activeBranch", activeBranch, "stderr", stderr)
		return fmt.Errorf("failed to create 'ours' merge commit for branch %q: %w (stderr: %s)", proposedBranch, err, stderr)
	}
	commitSha = strings.TrimSpace(commitSha)

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

	// Build the resolved tree in a temporary index so the clone's real index/worktree/HEAD are never
	// touched. The temp index lives outside the clone so it cannot appear as an untracked file.
	tmpIndex, err := os.CreateTemp("", "promoter-merge-index-*")
	if err != nil {
		return fmt.Errorf("failed to create temp index for path-scoped merge: %w", err)
	}
	tmpIndexPath := tmpIndex.Name()
	_ = tmpIndex.Close()
	defer func() {
		if rmErr := os.Remove(tmpIndexPath); rmErr != nil && !os.IsNotExist(rmErr) {
			logger.Error(rmErr, "failed to remove temp index", "path", tmpIndexPath)
		}
	}()
	indexEnv := []string{"GIT_INDEX_FILE=" + tmpIndexPath}

	// Start from active's tree (active wins outside activePath).
	_, stderr, err := g.runCmdWithEnv(ctx, gitPath, indexEnv, "read-tree", activeRef)
	if err != nil {
		logger.Error(err, "Failed to read active tree into temp index", "activeBranch", activeBranch, "stderr", stderr)
		return fmt.Errorf("failed to read tree for branch %q: %w (stderr: %s)", activeBranch, err, stderr)
	}

	// Drop the activePath subtree so proposed's version (including any deletions) fully replaces it.
	_, stderr, err = g.runCmdWithEnv(ctx, gitPath, indexEnv, "rm", "-r", "-f", "--cached", "--ignore-unmatch", "--", ":(literal)"+activePath)
	if err != nil {
		logger.Error(err, "Failed to remove activePath from temp index", "activePath", activePath, "stderr", stderr)
		return fmt.Errorf("failed to remove activePath %q from index: %w (stderr: %s)", activePath, err, stderr)
	}

	// Overlay proposed's activePath subtree (proposed wins inside activePath). Skip the overlay if
	// proposed has no content at activePath — the subtree then stays removed, matching proposed.
	lsTreeStdout, stderr, err := g.runCmd(ctx, gitPath, "ls-tree", proposedRef, "--", ":(literal)"+activePath)
	if err != nil {
		logger.Error(err, "Failed to inspect proposed activePath", "proposedBranch", proposedBranch, "activePath", activePath, "stderr", stderr)
		return fmt.Errorf("failed to inspect activePath %q on branch %q: %w (stderr: %s)", activePath, proposedBranch, err, stderr)
	}
	if strings.TrimSpace(lsTreeStdout) != "" {
		_, stderr, err = g.runCmdWithEnv(ctx, gitPath, indexEnv, "read-tree", "--prefix="+activePath+"/", proposedRef+":"+activePath)
		if err != nil {
			logger.Error(err, "Failed to overlay proposed activePath into temp index", "proposedBranch", proposedBranch, "activePath", activePath, "stderr", stderr)
			return fmt.Errorf("failed to overlay activePath %q from branch %q: %w (stderr: %s)", activePath, proposedBranch, err, stderr)
		}
	}

	// Write the resolved tree and create the merge commit. Parents are [proposed, active], preserving
	// the "ours"-style topology so the subsequent SCM merge stays clean.
	treeSha, stderr, err := g.runCmdWithEnv(ctx, gitPath, indexEnv, "write-tree")
	if err != nil {
		logger.Error(err, "Failed to write resolved tree", "stderr", stderr)
		return fmt.Errorf("failed to write resolved tree for branch %q: %w (stderr: %s)", proposedBranch, err, stderr)
	}
	treeSha = strings.TrimSpace(treeSha)

	commitSha, stderr, err := g.runCmd(ctx, gitPath, "commit-tree", treeSha, "-p", proposedRef, "-p", activeRef, "-m", "Resolve conflicts for "+activePath)
	if err != nil {
		logger.Error(err, "Failed to create path-scoped merge commit", "proposedBranch", proposedBranch, "activeBranch", activeBranch, "activePath", activePath, "stderr", stderr)
		return fmt.Errorf("failed to create path-scoped merge commit for branch %q: %w (stderr: %s)", proposedBranch, err, stderr)
	}
	commitSha = strings.TrimSpace(commitSha)

	// Push the computed commit straight to the remote proposed ref; no local branch, no checkout.
	_, stderr, err = g.runCmd(ctx, gitPath, "push", "origin", commitSha+":refs/heads/"+proposedBranch)
	if err != nil {
		logger.Error(err, "Failed to push merged branch", "proposedBranch", proposedBranch, "activeBranch", activeBranch, "stderr", stderr)
		return fmt.Errorf("failed to push merged branch %q: %w (stderr: %s)", proposedBranch, err, stderr)
	}

	logger.Info("Successfully merged branches with path-scoped strategy", "proposedBranch", proposedBranch, "activeBranch", activeBranch, "activePath", activePath)
	return nil
}

// GetRevListFirstParent retrieves the first parent commit SHAs for the given branch using git rev-list.
//
// Read-only: never mutates the clone's index/worktree/HEAD. Requires the branch's commits to have
// been fetched.
func (g *EnvironmentOperations) GetRevListFirstParent(ctx context.Context, branch string, maxCount int) ([]string, error) {
	logger := log.FromContext(ctx)

	gitPath := g.ClonePath()
	if gitPath == "" {
		return nil, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	args := make([]string, 0, 4)
	args = append(args, "rev-list", "--first-parent")
	args = append(args, "--max-count="+strconv.Itoa(maxCount))
	args = append(args, branch)

	stdout, stderr, err := g.runCmd(ctx, gitPath, args...)
	if err != nil {
		logger.Error(err, "could not get rev-list first parent", "gitError", stderr)
		return nil, fmt.Errorf("failed to get rev-list first parent for branch %q: %w", branch, err)
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	return lines, nil
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

	cmd := exec.CommandContext(ctx, "git", "interpret-trailers", "--trailer", trailerLine)
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
		if !strings.Contains(stderr, "non-fast-forward") && !strings.Contains(stderr, "fetch first") && !strings.Contains(stderr, "[rejected]") {
			logger.Error(err, "Failed to push history note", "sha", sha, "stderr", stderr)
			return lastErr
		}
		logger.V(4).Info("History note push rejected, retrying", "sha", sha, "attempt", attempt, "stderr", stderr)
	}

	return fmt.Errorf("failed to push history note after %d attempts: %w", setHistoryNoteMaxAttempts, lastErr)
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

// IsAncestor reports whether ancestor is an ancestor of descendant using git merge-base --is-ancestor.
//
// Read-only: never mutates the clone's index/worktree/HEAD. Requires both commits to have been fetched.
func (g *EnvironmentOperations) IsAncestor(ctx context.Context, ancestor, descendant string) (bool, error) {
	gitPath := g.ClonePath()
	if gitPath == "" {
		return false, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	_, stderr, err := g.runCmd(ctx, gitPath, "merge-base", "--is-ancestor", ancestor, descendant)
	if err != nil {
		// Exit code 1 means "not an ancestor"; anything else is a real error.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("failed to check ancestry of %q in %q: %w (stderr: %s)", ancestor, descendant, err, stderr)
	}

	return true, nil
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

// FetchSha fetches a single commit object by SHA from origin. Best-effort callers should tolerate errors:
// not all servers allow fetching arbitrary SHAs (uploadpack.allowAnySHA1InWant).
//
// Read-only: writes only fetched objects; never mutates the clone's index/worktree/HEAD.
func (g *EnvironmentOperations) FetchSha(ctx context.Context, sha string) error {
	logger := log.FromContext(ctx)
	gitPath := g.ClonePath()
	if gitPath == "" {
		return fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	start := time.Now()
	_, stderr, err := g.runCmd(ctx, gitPath, "fetch", "origin", sha)
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetch, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		logger.V(4).Info("could not fetch sha from origin", "sha", sha, "stderr", stderr, "err", err)
		return fmt.Errorf("failed to fetch sha %q: %w", sha, err)
	}

	return nil
}

// ParseTrailersFromMessage parses git trailers from a commit message using git interpret-trailers.
// Returns a map where each key can have multiple values (e.g., multiple "Signed-off-by" trailers).
//
// ParseTrailersFromMessage is concurrency-safe: it operates only on the provided message via stdin and uses no clone.
func ParseTrailersFromMessage(ctx context.Context, commitMessage string) (map[string][]string, error) {
	logger := log.FromContext(ctx)

	// Pipe the message to git interpret-trailers using stdin
	cmd := exec.CommandContext(ctx, "git", "interpret-trailers", "--only-trailers")
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
	logger := log.FromContext(ctx)
	// run git interpret-trailers to get the trailers from the last commit
	gitPath := g.ClonePath()
	if gitPath == "" {
		return nil, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	// First get the commit message
	msgStdout, stderr, err := g.runCmd(ctx, gitPath, "log", "-1", "--format=%B", sha)
	if err != nil {
		return nil, fmt.Errorf("failed to get commit message for sha %q: %w", sha, err)
	}
	if stderr != "" {
		logger.V(4).Info("git log returned an error", "stderr", stderr)
	}

	// Use the standalone parser
	return ParseTrailersFromMessage(ctx, msgStdout)
}
