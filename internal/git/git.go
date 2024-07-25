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
	"strings"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	"github.com/argoproj-labs/gitops-promoter/internal/utils"
	"github.com/relvacode/iso8601"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

type GitOperations struct {
	gap         scms.GitOperationsProvider
	repoRef     *v1alpha1.Repository
	scmProvider *v1alpha1.ScmProvider
	pathLookup  utils.PathLookup
	pathContext string
}

type HydratorMetadataFile struct {
	Commands []string `json:"commands"`
	DrySHA   string   `json:"drySha"`
}

func NewGitOperations(ctx context.Context, k8sClient client.Client, gap scms.GitOperationsProvider, pathLookup utils.PathLookup, repoRef v1alpha1.Repository, obj v1.Object, pathConext string) (*GitOperations, error) {

	scmProvider, err := utils.GetScmProviderFromRepositoryReference(ctx, k8sClient, repoRef, obj)
	if err != nil {
		return nil, err
	}

	gitOperations := GitOperations{
		gap:         gap,
		scmProvider: scmProvider,
		repoRef:     &repoRef,
		pathLookup:  pathLookup,
		pathContext: pathConext,
	}

	return &gitOperations, nil
}

func (g *GitOperations) CloneRepo(ctx context.Context) error {
	logger := log.FromContext(ctx)
	if g.pathLookup.Get(g.gap.GetGitHttpsRepoUrl(*g.repoRef)+g.pathContext) == "" {
		path, err := os.MkdirTemp("", "*")
		if err != nil {
			return err
		}
		logger.V(4).Info("Created directory", "directory", path)

		_, stdout, stderr, err := g.runCmd(ctx, path, "git", "clone", "--verbose", "--progress", "--filter=blob:none", g.gap.GetGitHttpsRepoUrl(*g.repoRef), path)
		if err != nil {
			logger.Info("Cloned repo failed", "repo", g.gap.GetGitHttpsRepoUrl(*g.repoRef), "stdout", stdout, "stderr", stderr)
			return err
		}
		g.pathLookup.Set(g.gap.GetGitHttpsRepoUrl(*g.repoRef)+g.pathContext, path)
		logger.V(4).Info("Cloned repo successful", "repo", g.gap.GetGitHttpsRepoUrl(*g.repoRef))

	}

	return nil
}

func (g *GitOperations) GetBranchShas(ctx context.Context, branches []string) (dry map[string]string, hydrated map[string]string, err error) {
	logger := log.FromContext(ctx)
	if g.pathLookup.Get(g.gap.GetGitHttpsRepoUrl(*g.repoRef)+g.pathContext) == "" {
		return nil, nil, fmt.Errorf("no repo path found")
	}

	hydratedBranchShas := make(map[string]string)
	dryBranchShas := make(map[string]string)

	for _, branch := range branches {
		_, _, stderr, err := g.runCmd(ctx, g.pathLookup.Get(g.gap.GetGitHttpsRepoUrl(*g.repoRef)+g.pathContext), "git", "checkout", "--progress", "-B", branch, fmt.Sprintf("origin/%s", branch))
		if err != nil {
			logger.Error(err, "could not git checkout", "gitError", stderr)
			return nil, nil, err
		}
		logger.V(4).Info("Checked out branch", "branch", branch)

		_, _, stderr, err = g.runCmd(ctx, g.pathLookup.Get(g.gap.GetGitHttpsRepoUrl(*g.repoRef)+g.pathContext), "git", "pull", "--progress")
		if err != nil {
			logger.Error(err, "could not git pull", "gitError", stderr)
			return nil, nil, err
		}
		logger.V(4).Info("Pulled branch", "branch", branch)

		_, stdout, stderr, err := g.runCmd(ctx, g.pathLookup.Get(g.gap.GetGitHttpsRepoUrl(*g.repoRef)+g.pathContext), "git", "rev-parse", branch)
		if err != nil {
			logger.Error(err, "could not get branch shas", "gitError", stderr)
			return nil, nil, err
		}
		hydratedBranchShas[branch] = strings.TrimSpace(stdout)
		logger.V(4).Info("Got hydrated branch sha", "branch", branch, "sha", hydratedBranchShas[branch])

		metadataFile := g.pathLookup.Get(g.gap.GetGitHttpsRepoUrl(*g.repoRef)+g.pathContext) + "/hydrator.metadata"
		if _, err := os.Stat(metadataFile); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				logger.Info("dry sha does not exist", "branch", branch)
				continue
			}
		}
		jsonFile, err := os.Open(metadataFile)
		if err != nil {
			return nil, nil, err
		}
		byteValue, err := io.ReadAll(jsonFile)
		if err != nil {
			return nil, nil, err
		}
		var hydratorFile HydratorMetadataFile
		err = json.Unmarshal(byteValue, &hydratorFile)
		if err != nil {
			return nil, nil, err
		}
		dryBranchShas[branch] = hydratorFile.DrySHA
		logger.V(4).Info("Got dry branch sha", "branch", branch, "sha", dryBranchShas[branch])
	}

	return dryBranchShas, hydratedBranchShas, nil
}

func (g *GitOperations) GetShaTime(ctx context.Context, sha string) (v1.Time, error) {
	logger := log.FromContext(ctx)
	if g.pathLookup.Get(g.gap.GetGitHttpsRepoUrl(*g.repoRef)+g.pathContext) == "" {
		return v1.Time{}, fmt.Errorf("no repo path found")
	}

	_, stdout, stderr, err := g.runCmd(ctx, g.pathLookup.Get(g.gap.GetGitHttpsRepoUrl(*g.repoRef)+g.pathContext), "git", "show", "-s", "--format=%cI", sha)
	if err != nil {
		logger.Error(err, "could not git show", "gitError", stderr)
		return v1.Time{}, err
	}
	logger.V(4).Info("Got sha time", "sha", sha, "time", stdout)

	cTime, err := iso8601.ParseString(strings.TrimSpace(stdout))
	if err != nil {
		return v1.Time{}, err
	}

	return v1.Time{Time: cTime}, nil
}

func (g *GitOperations) runCmd(ctx context.Context, directory string, name string, args ...string) (*exec.Cmd, string, string, error) {
	user, err := g.gap.GetUser(ctx)
	if err != nil {
		return nil, "", "", err
	}

	token, err := g.gap.GetToken(ctx)
	if err != nil {
		return nil, "", "", err
	}

	cmd := exec.Command(name, args...)
	cmd.Env = []string{
		"GIT_ASKPASS=promoter_askpass.sh", // Needs to be on path
		fmt.Sprintf("GIT_USER=%s", user),
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
		return nil, "", "failed to start", err
	}

	if err := cmd.Wait(); err != nil {
		exitErr := err.(*exec.ExitError)
		return nil, "", exitErr.String(), err
	}

	return cmd, stdoutBuf.String(), stderrBuf.String(), nil
}
