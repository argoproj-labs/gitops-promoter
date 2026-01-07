// Package git provides operations for managing Git repositories.
//
// The EnvironmentOperations struct provides methods for interacting with a particular clone of a repository. It ensures
// there is a separate clone for each environment to avoid concurrency issues.
//
// When implementing operations that do not require an environment-specific clone, create a static function that accepts
// the GitOperationsProvider and the GitRepository as parameters. This avoids the need to manage state to avoid
// concurrency issues. See LsRemote for an example of such a function.
package git

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

// EnvironmentOperations provides methods for interacting with a specific clone of a Git repository for an environment.
type EnvironmentOperations struct {
	gap     scms.GitOperationsProvider
	gitRepo *v1alpha1.GitRepository
	// activeBranch is used as part of the git path key to make sure there's one clone "per environment". Since there
	// should be only one CTP for each unique active branch, we shouldn't run into concurrency issues between clones.
	activeBranch string
}

// HydratorMetadata is an alias to v1alpha1.HydratorMetadata for convenience.
type HydratorMetadata = v1alpha1.HydratorMetadata

// HydratorNotesRef is the git notes reference used by hydrators to store metadata about hydrated commits.
const HydratorNotesRef = "refs/notes/hydrator.metadata"

// NewEnvironmentOperations creates a new EnvironmentOperations instance. The activeBranch parameter is used to differentiate
// between different environments that might use the same GitRepository and avoid conflicts between concurrent
// operations.
func NewEnvironmentOperations(gitRepo *v1alpha1.GitRepository, gap scms.GitOperationsProvider, activeBranch string) *EnvironmentOperations {
	return &EnvironmentOperations{
		gap:          gap,
		gitRepo:      gitRepo,
		activeBranch: activeBranch,
	}
}

// CloneRepo clones the gitRepo to a temporary directory if needed. Does nothing if the repo is already cloned.
func (g *EnvironmentOperations) CloneRepo(ctx context.Context) error {
	if gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.activeBranch) != "" {
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

	logger.V(4).Info("Cloned repo successful", "repo", g.gap.GetGitHttpsRepoUrl(*g.gitRepo))

	gitpaths.Set(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.activeBranch, path)

	return nil
}

// BranchShas holds the hydrated and dry commit SHAs for a branch.
type BranchShas struct {
	// Dry is the SHA of the commit that was used as the dry source for hydration.
	Dry string
	// Hydrated is the SHA of the commit on the hydrated branch.
	Hydrated string
}

// GetBranchShas checks out the given branch, pulls the latest changes, and returns the hydrated and dry SHAs.
func (g *EnvironmentOperations) GetBranchShas(ctx context.Context, branch string) (BranchShas, error) {
	logger := log.FromContext(ctx)
	gitPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)
	if gitPath == "" {
		return BranchShas{}, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	logger.V(4).Info("git path", "path", gitPath)

	// Fetch the branch to ensure we have the latest remote ref
	start := time.Now()
	_, stderr, err := g.runCmd(ctx, gitPath, "fetch", "origin", branch)
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetch, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		logger.Error(err, "could not fetch branch", "gitError", stderr)
		return BranchShas{}, fmt.Errorf("failed to fetch branch %q: %w", branch, err)
	}
	logger.V(4).Info("Fetched branch", "branch", branch)

	// Get the SHA of the remote branch
	stdout, stderr, err := g.runCmd(ctx, gitPath, "rev-parse", "origin/"+branch)
	if err != nil {
		logger.Error(err, "could not get branch sha", "gitError", stderr)
		return BranchShas{}, fmt.Errorf("failed to get SHA for branch %q: %w", branch, err)
	}

	shas := BranchShas{}
	shas.Hydrated = strings.TrimSpace(stdout)
	logger.V(4).Info("Got hydrated branch sha", "branch", branch, "sha", shas.Hydrated)

	// Get the metadata file contents directly from the remote branch
	metadataFileStdout, stderr, err := g.runCmd(ctx, gitPath, "show", "origin/"+branch+":hydrator.metadata")
	if err != nil {
		if strings.Contains(stderr, "does not exist") || strings.Contains(stderr, "Path not in") {
			logger.Info("hydrator.metadata file not found", "branch", branch)
			return shas, nil
		}
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

// GetShaMetadataFromFile retrieves commit metadata from the hydrator.metadata file for a given SHA.
func (g *EnvironmentOperations) GetShaMetadataFromFile(ctx context.Context, sha string) (v1alpha1.CommitShaState, error) {
	logger := log.FromContext(ctx)

	gitPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)
	if gitPath == "" {
		return v1alpha1.CommitShaState{}, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	metadataFileStdout, stderr, err := g.runCmd(ctx, gitPath, "show", sha+":hydrator.metadata")
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
func (g *EnvironmentOperations) GetShaMetadataFromGit(ctx context.Context, sha string) (v1alpha1.CommitShaState, error) {
	gitPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)
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
		return v1alpha1.CommitShaState{}, fmt.Errorf("failed to get commit time for hydrated SHA %q: %w", sha, err)
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

	gitPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)
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
	gitPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)
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
	gitPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)
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
	gitPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)
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
func LsRemote(ctx context.Context, gap scms.GitOperationsProvider, gitRepo *v1alpha1.GitRepository, branches ...string) (map[string]string, error) {
	logger := log.FromContext(ctx)

	start := time.Now()
	args := []string{"ls-remote", "--heads", gap.GetGitHttpsRepoUrl(*gitRepo)}
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
		return nil, fmt.Errorf("expected %d lines from ls-remote, got %d: %s", len(branches), len(lines), stdout)
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
	return runCmd(ctx, g.gap, directory, args...)
}

// runCmd runs a git command with the provided arguments and returns stdout, stderr, and error.
func runCmd(ctx context.Context, gap scms.GitOperationsProvider, directory string, args ...string) (string, string, error) {
	user, err := gap.GetUser(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to get user: %w", err)
	}

	token, err := gap.GetToken(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to get token: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = []string{
		"GIT_ASKPASS=promoter_askpass.sh", // Needs to be on path
		"GIT_USERNAME=" + user,
		"GIT_PASSWORD=" + token,
		"PATH=" + os.Getenv("PATH"),
		"GIT_TERMINAL_PROMPT=0",
	}
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
// This performs a stateless merge check without modifying the working directory. It assumes that origin/<branch> is
// currently fetched and updated in the local repository. This should happen via GetBranchShas function earlier in the reconcile.
func (g *EnvironmentOperations) HasConflict(ctx context.Context, proposedBranch, activeBranch string) (bool, error) {
	logger := log.FromContext(ctx)
	repoPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)

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

// MergeWithOursStrategy merges the proposed branch into the active branch using the "ours" strategy.
// This assumes that both branches have already been fetched via GetBranchShas earlier in the reconciliation,
// ensuring we merge the exact same refs that were checked for conflicts.
func (g *EnvironmentOperations) MergeWithOursStrategy(ctx context.Context, proposedBranch, activeBranch string) error {
	logger := log.FromContext(ctx)
	gitPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)

	// Checkout the proposed branch from the already-fetched origin ref
	// We use the origin ref to ensure we're working with the same commits that were checked for conflicts
	_, stderr, err := g.runCmd(ctx, gitPath, "checkout", "-B", proposedBranch, "origin/"+proposedBranch)
	if err != nil {
		logger.Error(err, "Failed to checkout branch", "branch", proposedBranch, "stderr", stderr)
		return fmt.Errorf("failed to checkout branch %q: %w", proposedBranch, err)
	}

	// Perform the merge with "ours" strategy using the already-fetched origin ref
	_, stderr, err = g.runCmd(ctx, gitPath, "merge", "-s", "ours", "origin/"+activeBranch)
	if err != nil {
		logger.Error(err, "Failed to merge branch", "proposedBranch", proposedBranch, "activeBranch", activeBranch, "stderr", stderr)
		return fmt.Errorf("failed to merge branch %q into %q with 'ours' strategy: %w", activeBranch, proposedBranch, err)
	}

	// Push the changes to the remote repository
	_, stderr, err = g.runCmd(ctx, gitPath, "push", "origin", proposedBranch)
	if err != nil {
		logger.Error(err, "Failed to push merged branch", "proposedBranch", proposedBranch, "activeBranch", activeBranch, "stderr", stderr)
		return fmt.Errorf("failed to push merged branch %q: %w", proposedBranch, err)
	}

	logger.Info("Successfully merged branches with 'ours' strategy", "proposedBranch", proposedBranch, "activeBranch", activeBranch)
	return nil
}

// GetRevListFirstParent retrieves the first parent commit SHAs for the given branch using git rev-list.
func (g *EnvironmentOperations) GetRevListFirstParent(ctx context.Context, branch string, maxCount int) ([]string, error) {
	logger := log.FromContext(ctx)

	gitPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)
	if gitPath == "" {
		return nil, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	args := []string{"rev-list", "--first-parent"}
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
func (g *EnvironmentOperations) FetchNotes(ctx context.Context) error {
	logger := log.FromContext(ctx)
	gitPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)
	if gitPath == "" {
		return fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	// Fetch the notes ref from origin. We use + to force update in case of divergence.
	start := time.Now()
	_, stderr, err := g.runCmd(ctx, gitPath, "fetch", "origin", "+"+HydratorNotesRef+":"+HydratorNotesRef)
	if err != nil {
		// Notes ref might not exist yet, which is fine
		if strings.Contains(stderr, "couldn't find remote ref") {
			metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetchNotes, metrics.GitOperationResultSuccess, time.Since(start))
			logger.V(4).Info("Git notes ref does not exist on remote", "ref", HydratorNotesRef)
			return nil
		}
		metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetchNotes, metrics.GitOperationResultFailure, time.Since(start))
		logger.Error(err, "Failed to fetch git notes", "stderr", stderr)
		return fmt.Errorf("failed to fetch git notes: %w", err)
	}
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetchNotes, metrics.GitOperationResultSuccess, time.Since(start))

	logger.V(4).Info("Fetched git notes", "ref", HydratorNotesRef)
	return nil
}

// GetHydratorNote reads the hydrator git note for a given commit SHA.
// Returns an empty HydratorMetadata if no note exists for the commit.
func (g *EnvironmentOperations) GetHydratorNote(ctx context.Context, sha string) (HydratorMetadata, error) {
	logger := log.FromContext(ctx)
	gitPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)
	if gitPath == "" {
		return HydratorMetadata{}, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	stdout, stderr, err := g.runCmd(ctx, gitPath, "notes", "--ref="+HydratorNotesRef, "show", sha)
	if err != nil {
		// No note for this commit is not an error - git outputs "error: no note found for object <sha>"
		if strings.Contains(strings.ToLower(stderr), "no note found") {
			logger.V(4).Info("No git note found for commit", "sha", sha)
			return HydratorMetadata{}, nil
		}
		logger.Error(err, "Failed to read git note", "sha", sha, "stderr", stderr)
		return HydratorMetadata{}, fmt.Errorf("failed to read git note for sha %q: %w", sha, err)
	}

	var note HydratorMetadata
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &note); err != nil {
		logger.V(4).Info("Failed to parse git note as JSON, ignoring", "sha", sha, "content", stdout, "error", err)
		return HydratorMetadata{}, nil
	}

	logger.V(4).Info("Got hydrator note", "sha", sha, "note", note)
	return note, nil
}

// ParseTrailersFromMessage parses git trailers from a commit message using git interpret-trailers.
// Returns a map where each key can have multiple values (e.g., multiple "Signed-off-by" trailers).
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
func (g *EnvironmentOperations) GetTrailers(ctx context.Context, sha string) (map[string][]string, error) {
	logger := log.FromContext(ctx)
	// run git interpret-trailers to get the trailers from the last commit
	gitPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)
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

// GitShow runs git show with a specific format string for a commit SHA.
// The format parameter uses git's pretty-format placeholders (e.g., %ae for author email, %ce for committer email).
// See https://git-scm.com/docs/git-show#_pretty_formats for available format options.
func (g *EnvironmentOperations) GitShow(ctx context.Context, sha, format string) (string, error) {
	logger := log.FromContext(ctx)
	gitPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)
	if gitPath == "" {
		return "", fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	stdout, stderr, err := g.runCmd(ctx, gitPath, "show", "-s", "--format="+format, sha)
	if err != nil {
		logger.Error(err, "could not git show", "gitError", stderr, "sha", sha, "format", format)
		return "", fmt.Errorf("failed to run git show for sha %q with format %q: %w", sha, format, err)
	}
	logger.V(4).Info("Git show completed", "sha", sha, "format", format)

	return strings.TrimSpace(stdout), nil
}
