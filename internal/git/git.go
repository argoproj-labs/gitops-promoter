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
	"strings"
	"time"

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

type GitOperations struct {
	gap         scms.GitOperationsProvider
	gitRepo     *v1alpha1.GitRepository
	scmProvider *v1alpha1.ScmProvider
	pathContext string
}

type HydratorMetadataFile struct {
	Commands []string `json:"commands"`
	DrySHA   string   `json:"drySha"`
}

func NewGitOperations(ctx context.Context, k8sClient client.Client, gap scms.GitOperationsProvider, repoRef v1alpha1.ObjectReference, obj v1.Object, pathConext string) (*GitOperations, error) {
	gitRepo, err := utils.GetGitRepositoryFromObjectKey(ctx, k8sClient, client.ObjectKey{Namespace: obj.GetNamespace(), Name: repoRef.Name})
	if err != nil {
		return nil, fmt.Errorf("failed to get GitRepository: %w", err)
	}

	scmProvider, err := utils.GetScmProviderFromGitRepository(ctx, k8sClient, gitRepo, obj)
	if err != nil {
		return nil, fmt.Errorf("failed to get ScmProvider: %w", err)
	}

	gitOperations := GitOperations{
		gap:         gap,
		scmProvider: scmProvider,
		gitRepo:     gitRepo,
		pathContext: pathConext,
	}

	return &gitOperations, nil
}

// CloneRepo clones the gitRepo to a temporary directory if needed does nothing if the repo is already cloned.
func (g *GitOperations) CloneRepo(ctx context.Context) error {
	if gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext) != "" {
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

	gitpaths.Set(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext, path)

	return nil
}

type BranchShas struct {
	Dry      string
	Hydrated string
}

func (g *GitOperations) GetBranchShas(ctx context.Context, branch string) (BranchShas, error) {
	logger := log.FromContext(ctx)
	if gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext) == "" {
		return BranchShas{}, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	p := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo) + g.pathContext)
	logger.V(4).Info("git path", "path", p)
	_, stderr, err := g.runCmd(ctx, gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext), "checkout", "--progress", "-B", branch, fmt.Sprintf("origin/%s", branch))
	if err != nil {
		logger.Error(err, "could not git checkout", "gitError", stderr)
		return BranchShas{}, err
	}
	logger.V(4).Info("Checked out branch", "branch", branch)

	start := time.Now()
	_, stderr, err = g.runCmd(ctx, gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext), "pull", "--progress")
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationPull, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		logger.Error(err, "could not git pull", "gitError", stderr)
		return BranchShas{}, err
	}
	logger.V(4).Info("Pulled branch", "branch", branch)

	stdout, stderr, err := g.runCmd(ctx, gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext), "rev-parse", branch)
	if err != nil {
		logger.Error(err, "could not get branch shas", "gitError", stderr)
		return BranchShas{}, err
	}

	shas := BranchShas{}
	shas.Hydrated = strings.TrimSpace(stdout)
	logger.V(4).Info("Got hydrated branch sha", "branch", branch, "sha", shas.Hydrated)

	// TODO: safe path join
	metadataFile := gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext) + "/hydrator.metadata"
	jsonFile, err := os.Open(metadataFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			logger.Info("hydrator.metadata file not found", "file", metadataFile)
			return shas, nil
		}
		return BranchShas{}, fmt.Errorf("could not open metadata file: %w", err)
	}

	var hydratorFile HydratorMetadataFile
	decoder := json.NewDecoder(jsonFile)
	err = decoder.Decode(&hydratorFile)
	if err != nil {
		return BranchShas{}, fmt.Errorf("could not unmarshal metadata file: %w", err)
	}
	shas.Dry = hydratorFile.DrySHA
	logger.V(4).Info("Got dry branch sha", "branch", branch, "sha", shas.Dry)

	return shas, nil
}

func (g *GitOperations) GetShaTime(ctx context.Context, sha string) (v1.Time, error) {
	logger := log.FromContext(ctx)
	if gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext) == "" {
		return v1.Time{}, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	stdout, stderr, err := g.runCmd(ctx, gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext), "show", "-s", "--format=%cI", sha)
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

func (g *GitOperations) PromoteEnvironmentWithMerge(ctx context.Context, environmentBranch, environmentNextBranch string) error {
	logger := log.FromContext(ctx)
	logger.Info("Promoting environment with merge", "environmentBranch", environmentBranch, "environmentNextBranch", environmentNextBranch)

	if gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext) == "" {
		return fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	start := time.Now()
	_, stderr, err := g.runCmd(ctx, gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext), "fetch", "origin")
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetch, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		logger.Error(err, "could not fetch origin", "gitError", stderr)
		return err
	}

	_, stderr, err = g.runCmd(ctx, gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext), "checkout", "--progress", "-B", environmentBranch, fmt.Sprintf("origin/%s", environmentBranch))
	if err != nil {
		logger.Error(err, "could not git checkout", "gitError", stderr)
		return err
	}
	logger.V(4).Info("Checked out branch", "branch", environmentBranch)

	// Don't think this is needed
	// _, stderr, err = g.runCmd(ctx, gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.repoRef)+g.pathContext), "pull", "--progress")
	// if err != nil {
	// 	logger.Error(err, "could not git pull", "gitError", stderr)
	// 	return err
	// }
	// logger.V(4).Info("Pulled branch", "branch", environmentBranch)

	start = time.Now()
	_, stderr, err = g.runCmd(ctx, gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext), "pull", "--progress", "origin", environmentNextBranch)
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationPull, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		logger.Error(err, "could not git pull", "gitError", stderr)
		return err
	}
	logger.V(4).Info("Pulled branch", "branch", environmentNextBranch)

	start = time.Now()
	_, stderr, err = g.runCmd(ctx, gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext), "push", "--progress", "origin", environmentBranch)
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationPush, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		logger.Error(err, "could not git pull", "gitError", stderr)
		return err
	}
	logger.Info("Pushed branch", "branch", environmentBranch)

	return nil
}

// IsPullRequestRequired will compare the environment branch with the next environment branch and return true if a PR is required.
// The PR is required if the diff between the two branches contain edits to yaml files.
func (g *GitOperations) IsPullRequestRequired(ctx context.Context, environmentNextBranch, environmentBranch string) (bool, error) {
	logger := log.FromContext(ctx)

	if gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext) == "" {
		return false, fmt.Errorf("no repo path found for repo %q", g.gitRepo.Name)
	}

	// Checkout the environment branch
	_, stderr, err := g.runCmd(ctx, gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext), "checkout", "--progress", "-B", environmentBranch, fmt.Sprintf("origin/%s", environmentBranch))
	if err != nil {
		logger.Error(err, "could not git checkout", "gitError", stderr)
		return false, err
	}
	logger.V(4).Info("Checked out branch", "branch", environmentBranch)

	// Fetch the next environment branch
	start := time.Now()
	_, stderr, err = g.runCmd(ctx, gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext), "fetch", "origin", environmentNextBranch)
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationFetch, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		logger.Error(err, "could not fetch branch", "gitError", stderr)
		return false, err
	}
	logger.V(4).Info("Fetched branch", "branch", environmentNextBranch)

	// Get the diff between the two branches
	stdout, stderr, err := g.runCmd(ctx, gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext), "diff", fmt.Sprintf("origin/%s", environmentBranch), fmt.Sprintf("origin/%s", environmentNextBranch), "--name-only", "--diff-filter=ACMRT")
	if err != nil {
		logger.Error(err, "could not get diff", "gitError", stderr)
		return false, err
	}
	logger.V(4).Info("Got diff", "diff", stdout)

	// Check if the diff contains any YAML files if so we expect a manifest to have changed
	// TODO: This is temporary check we should add some path globbing support to the specs
	for _, file := range strings.Split(stdout, "\n") {
		if strings.HasSuffix(file, ".yaml") || strings.HasSuffix(file, ".yml") {
			logger.V(4).Info("YAML file changed", "file", file)
			return true, nil
		}
	}

	return false, nil
}

func (g *GitOperations) LsRemote(ctx context.Context, branch string) (string, error) {
	logger := log.FromContext(ctx)

	start := time.Now()
	stdout, stderr, err := g.runCmd(ctx, gitpaths.Get(g.gap.GetGitHttpsRepoUrl(*g.gitRepo)+g.pathContext),
		"ls-remote", g.gap.GetGitHttpsRepoUrl(*g.gitRepo), branch)
	metrics.RecordGitOperation(g.gitRepo, metrics.GitOperationLsRemote, metrics.GitOperationResultFromError(err), time.Since(start))
	if err != nil {
		logger.Error(err, "could not git ls-remote", "gitError", stderr)
		return "", err
	}
	if len(strings.Split(stdout, "\t")) == 0 {
		return "", fmt.Errorf("no sha found for branch %q", branch)
	}

	resolvedSha := strings.Split(stdout, "\t")[0]
	logger.Info("ls-remote called", "repoUrl", g.gap.GetGitHttpsRepoUrl(*g.gitRepo), "branch", branch, "sha", resolvedSha)

	return resolvedSha, nil
}

func (g *GitOperations) runCmd(ctx context.Context, directory string, args ...string) (string, string, error) {
	user, err := g.gap.GetUser(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to get user: %w", err)
	}

	token, err := g.gap.GetToken(ctx)
	if err != nil {
		return "", "", fmt.Errorf("failed to get token: %w", err)
	}

	cmd := exec.Command("git", args...)
	cmd.Env = []string{
		"GIT_ASKPASS=promoter_askpass.sh", // Needs to be on path
		fmt.Sprintf("GIT_USERNAME=%s", user),
		fmt.Sprintf("GIT_PASSWORD=%s", token),
		fmt.Sprintf("PATH=%s", os.Getenv("PATH")),
		"GIT_TERMINAL_PROMPT=0",
	}
	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	cmd.Dir = directory

	if cmd.Start() != nil {
		return "", "failed to start", fmt.Errorf("failed to start git command: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		// exitErr := err.(*exec.ExitError)
		return "", stderrBuf.String(), err
	}

	return stdoutBuf.String(), stderrBuf.String(), nil
}
