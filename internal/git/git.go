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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/argoproj-labs/gitops-promoter/internal/types/constants"
	"github.com/relvacode/iso8601"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/metrics"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
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

// HydratorMetadata contains metadata about the commit that is used to hydrate a branch.
type HydratorMetadata struct {
	// RepoURL is the URL of the repository where the commit is located.
	RepoURL string `json:"repoURL,omitempty"`
	// DrySha is the SHA of the commit that was used as the dry source for hydration.
	DrySha string `json:"drySha,omitempty"`
	// Author is the author of the dry commit that was used to hydrate the branch.
	Author string `json:"author,omitempty"`
	// Date is the date of the dry commit that was used to hydrate the branch.
	Date v1.Time `json:"date,omitempty"`
	// Subject is the subject line of the dry commit that was used to hydrate the branch.
	Subject string `json:"subject,omitempty"`
	// Body is the body of the dry commit that was used to hydrate the branch without the subject.
	Body string `json:"body,omitempty"`
	// References are the references to other commits, that went into the hydration of the branch.
	References []v1alpha1.RevisionReference `json:"references,omitempty"`
}

// NewEnvironmentOperations creates a new EnvironmentOperations instance. The activeBranch parameter is used to differentiate
// between different environments that might use the same GitRepository and avoid conflicts between concurrent
// operations.
func NewEnvironmentOperations(ctx context.Context, k8sClient client.Client, gap scms.GitOperationsProvider, repoRef v1alpha1.ObjectReference, obj v1.Object, activeBranch string) (*EnvironmentOperations, error) {
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, k8sClient, client.ObjectKey{Namespace: obj.GetNamespace(), Name: repoRef.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	gitOperations := EnvironmentOperations{
		gap:          gap,
		gitRepo:      gitRepo,
		activeBranch: activeBranch,
	}

	return &gitOperations, nil
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
	_, stderr, err := g.runCmd(ctx, gitPath, "checkout", "--progress", "-B", branch, "origin/"+branch)
	if err != nil {
		logger.Error(err, "could not git checkout", "gitError", stderr)
		return BranchShas{}, err
	}
	logger.V(4).Info("Checked out branch", "branch", branch)

	start := time.Now()
	_, stderr, err = g.runCmd(ctx, gitPath, "pull", "--progress")
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationPull, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		logger.Error(err, "could not git pull", "gitError", stderr)
		return BranchShas{}, err
	}
	logger.V(4).Info("Pulled branch", "branch", branch)

	stdout, stderr, err := g.runCmd(ctx, gitPath, "rev-parse", branch)
	if err != nil {
		logger.Error(err, "could not get branch shas", "gitError", stderr)
		return BranchShas{}, err
	}

	shas := BranchShas{}
	shas.Hydrated = strings.TrimSpace(stdout)
	logger.V(4).Info("Got hydrated branch sha", "branch", branch, "sha", shas.Hydrated)

	// TODO: safe path join
	metadataFile := gitPath + "/hydrator.metadata"
	jsonFile, err := os.Open(metadataFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logger.Info("hydrator.metadata file not found", "file", metadataFile)
			return shas, nil
		}
		return BranchShas{}, fmt.Errorf("could not open metadata file: %w", err)
	}

	var hydratorFile HydratorMetadata
	decoder := json.NewDecoder(jsonFile)
	err = decoder.Decode(&hydratorFile)
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
		logger.Error(err, "could not git show file", "gitError", stderr)
		return v1alpha1.CommitShaState{}, nil
	}
	logger.V(4).Info("Got metadata file", "sha", sha, "file", metadataFileStdout)

	var hydratorFile HydratorMetadata
	err = json.Unmarshal([]byte(metadataFileStdout), &hydratorFile)
	if err != nil {
		return v1alpha1.CommitShaState{}, fmt.Errorf("could not unmarshal metadata file: %w", err)
	}

	commitState := v1alpha1.CommitShaState{
		Sha:        hydratorFile.DrySha,
		CommitTime: hydratorFile.Date,
		RepoURL:    hydratorFile.RepoURL,
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

// PromoteEnvironmentWithMerge merges the next environment branch into the current environment branch and pushes the result.
func (g *EnvironmentOperations) PromoteEnvironmentWithMerge(ctx context.Context, environmentBranch, environmentNextBranch string) error {
	logger := log.FromContext(ctx)
	logger.Info("Promoting environment with merge", "environmentBranch", environmentBranch, "environmentNextBranch", environmentNextBranch)

	gitPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)
	if gitPath == "" {
		return fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	start := time.Now()
	_, stderr, err := g.runCmd(ctx, gitPath, "fetch", "origin")
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetch, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		logger.Error(err, "could not fetch origin", "gitError", stderr)
		return err
	}

	_, stderr, err = g.runCmd(ctx, gitPath, "checkout", "--progress", "-B", environmentBranch, "origin/"+environmentBranch)
	if err != nil {
		logger.Error(err, "could not git checkout", "gitError", stderr)
		return err
	}
	logger.V(4).Info("Checked out branch", "branch", environmentBranch)

	start = time.Now()
	_, stderr, err = g.runCmd(ctx, gitPath, "merge", "--no-ff", "origin/"+environmentNextBranch, "-m", "This is a no-op commit merging from "+environmentNextBranch+" into "+environmentBranch+"\n\n"+constants.TrailerNoOp+": true\n")
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationPull, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		logger.Error(err, "could not git merge", "gitError", stderr)
		return err
	}
	logger.V(4).Info("Merged branch", "branch", environmentNextBranch)

	start = time.Now()
	_, stderr, err = g.runCmd(ctx, gitPath, "push", "--progress", "origin", environmentBranch)
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationPush, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		logger.Error(err, "could not git push", "gitError", stderr)
		return err
	}
	logger.Info("Pushed branch", "branch", environmentBranch)

	return nil
}

// IsPullRequestRequired will compare the environment branch with the next environment branch and return true if a PR is required.
// The PR is required if the diff between the two branches contain edits to yaml files.
func (g *EnvironmentOperations) IsPullRequestRequired(ctx context.Context, environmentNextBranch, environmentBranch string) (bool, error) {
	logger := log.FromContext(ctx)

	gitPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)
	if gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.activeBranch) == "" {
		return false, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	// Checkout the environment branch
	_, stderr, err := g.runCmd(ctx, gitPath, "checkout", "--progress", "-B", environmentBranch, "origin/"+environmentBranch)
	if err != nil {
		logger.Error(err, "could not git checkout", "gitError", stderr)
		return false, err
	}
	logger.V(4).Info("Checked out branch", "branch", environmentBranch)

	// Fetch the next environment branch
	start := time.Now()
	_, stderr, err = g.runCmd(ctx, gitPath, "fetch", "origin", environmentNextBranch)
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetch, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		logger.Error(err, "could not fetch branch", "gitError", stderr)
		return false, err
	}
	logger.V(4).Info("Fetched branch", "branch", environmentNextBranch)

	// Get the diff between the two branches
	stdout, stderr, err := g.runCmd(ctx, gitPath, "diff", fmt.Sprintf("origin/%s...origin/%s", environmentBranch, environmentNextBranch), "--name-only", "--diff-filter=ACMRT")
	if err != nil {
		logger.Error(err, "could not get diff", "gitError", stderr)
		return false, err
	}
	logger.V(4).Info("Got diff", "diff", stdout)

	return containsYamlFileSuffix(ctx, strings.Split(stdout, "\n")), nil
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

	cmd := exec.Command("git", args...)
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
		// exitErr := err.(*exec.ExitError)
		return stdoutBuf.String(), stderrBuf.String(), err
	}

	return stdoutBuf.String(), stderrBuf.String(), nil
}

// HasConflict checks if there is a merge conflict between the proposed branch and the active branch. It assumes that
// origin/<branch> is currently fetched and updated in the local repository. This should happen via GetBranchShas function
// earlier in the reconcile.
func (g *EnvironmentOperations) HasConflict(ctx context.Context, proposedBranch, activeBranch string) (bool, error) {
	logger := log.FromContext(ctx)
	repoPath := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.activeBranch)

	// Checkout the active branch
	if _, stderr, err := g.runCmd(ctx, repoPath, "checkout", "--progress", "-B", activeBranch, "origin/"+activeBranch); err != nil {
		logger.Error(err, "could not checkout active branch", "branch", activeBranch, "stderr", stderr)
		return false, fmt.Errorf("failed to checkout active branch %q: %w", activeBranch, err)
	}

	// Merge the proposed branch without committing
	stdout, stderr, mergeErr := g.runCmd(ctx, repoPath, "merge", "--no-commit", "--no-ff", "origin/"+proposedBranch)
	conflictDetected := strings.Contains(stdout, "CONFLICT")

	// Always attempt to abort the merge to clean up
	if _, abortStderr, abortErr := g.runCmd(ctx, repoPath, "merge", "--abort"); abortErr != nil {
		if !strings.Contains(abortStderr, "MERGE_HEAD missing") { // Ignore the error if there is no merge in progress
			logger.Error(abortErr, "could not abort merge", "stderr", abortStderr)
			return false, fmt.Errorf("failed to abort merge: %w", abortErr)
		}
	}

	if conflictDetected {
		return true, nil
	}
	if mergeErr != nil {
		logger.Error(mergeErr, "could not merge branches", "proposedBranch", proposedBranch, "activeBranch", activeBranch, "stderr", stderr)
		return false, fmt.Errorf("failed to test merge branch %q into %q: %w", proposedBranch, activeBranch, mergeErr)
	}

	return false, nil
}

// MergeWithOursStrategy merges the proposed branch into the active branch using the "ours" strategy.
func (g *EnvironmentOperations) MergeWithOursStrategy(ctx context.Context, proposedBranch, activeBranch string) error {
	logger := log.FromContext(ctx)

	// Checkout the proposed branch
	_, stderr, err := g.runCmd(ctx, gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.activeBranch), "checkout", proposedBranch)
	if err != nil {
		logger.Error(err, "Failed to checkout branch", "branch", proposedBranch, "stderr", stderr)
		return fmt.Errorf("failed to checkout branch %q: %w", proposedBranch, err)
	}

	// Perform the merge with "ours" strategy
	_, stderr, err = g.runCmd(ctx, gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.activeBranch), "merge", "-s", "ours", activeBranch)
	if err != nil {
		logger.Error(err, "Failed to merge branch", "proposedBranch", proposedBranch, "activeBranch", activeBranch, "stderr", stderr)
		return fmt.Errorf("failed to merge branch %q into %q with 'ours' strategy: %w", activeBranch, proposedBranch, err)
	}

	// Push the changes to the remote repository
	_, stderr, err = g.runCmd(ctx, gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.activeBranch), "push", "origin", proposedBranch)
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

// GetTrailers retrieves the trailers from the last commit in the repository using git interpret-trailers.
func (g *EnvironmentOperations) GetTrailers(ctx context.Context, sha string) (map[string]string, error) {
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

	// Then pipe it to git interpret-trailers using stdin
	cmd := exec.Command("git", "interpret-trailers", "--only-trailers")
	cmd.Dir = gitPath
	cmd.Stdin = strings.NewReader(msgStdout)

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()
	stderr = stderrBuf.String()
	if err != nil {
		logger.Error(err, "failed to run git interpret-trailers", "stderr", stderr)
		return nil, fmt.Errorf("failed to run git interpret-trailers: %w", err)
	}
	stdout := stdoutBuf.String()

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	trailers := make(map[string]string, len(lines))
	for _, line := range lines {
		if strings.Contains(line, ":") {
			key, value, found := strings.Cut(line, ":")
			if found {
				trailers[strings.TrimSpace(key)] = strings.TrimSpace(value)
			} else {
				logger.Error(fmt.Errorf("invalid trailer line: %s", line), "could not parse trailer line")
			}
		}
	}
	logger.V(4).Info("Got trailers", "sha", sha, "trailers", trailers)
	return trailers, nil
}
