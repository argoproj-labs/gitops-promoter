package git

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"github.com/argoproj/promoter/api/v1alpha1"
	"github.com/argoproj/promoter/internal/scms"
	"github.com/argoproj/promoter/internal/utils"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"os"
	"os/exec"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"strings"
)

type GitOperations struct {
	gap         scms.GitOperationsProvider
	repoRef     *v1alpha1.RepositoryRef
	scmProvider *v1alpha1.ScmProvider
	pathLookup  utils.PathLookup
}

func NewGitOperations(ctx context.Context, k8sClient client.Client, gap scms.GitOperationsProvider, pathLookup utils.PathLookup, repoRef v1alpha1.RepositoryRef, obj v1.Object) (*GitOperations, error) {

	scmProvider, err := utils.GetScmProviderFromRepositoryReference(ctx, k8sClient, repoRef, obj)
	if err != nil {
		return nil, err
	}

	gitOperations := GitOperations{
		gap:         gap,
		scmProvider: scmProvider,
		repoRef:     &repoRef,
		pathLookup:  pathLookup,
	}

	return &gitOperations, nil
}

func (g *GitOperations) GetUpdateRepo(ctx context.Context) error {
	logger := log.FromContext(ctx)
	if g.pathLookup.Get(g.gap.GetGitHttpsRepoUrl(*g.repoRef)) == "" {
		path, err := os.MkdirTemp("", "*")
		if err != nil {
			return err
		}
		logger.Info("Created directory", "directory", path)

		_, stdout, stderr, err := g.runCmd(ctx, path, "git", "clone", "--progress", "--filter=blob:none", g.gap.GetGitHttpsRepoUrl(*g.repoRef), path)
		if err != nil {
			logger.Info("Cloned repo failed", "repo", g.gap.GetGitHttpsRepoUrl(*g.repoRef), "stdout", stdout, "stderr", stderr)
			return err
		}
		g.pathLookup.Set(g.gap.GetGitHttpsRepoUrl(*g.repoRef), path)
		logger.Info("Cloned repo successful", "repo", g.gap.GetGitHttpsRepoUrl(*g.repoRef))

	} else {
		//logger.Info("Repo exists, fetching instead")
		//_, stdout, stderr, err := g.runCmd(ctx, g.pathLookup.Get(g.gap.GetGitHttpsRepoUrl(*g.repoRef)), "git", "fetch", "origin")
		//if err != nil {
		//	return err
		//}
		//
		//logger.Info("Fetched Repo", "repo", g.gap.GetGitHttpsRepoUrl(*g.repoRef), "stdout", stdout, "stderr", stderr)
	}

	return nil
}

func (g *GitOperations) GetBranchShas(ctx context.Context, branches []string) (dry map[string]string, hydrated map[string]string, err error) {
	logger := log.FromContext(ctx)
	if g.pathLookup.Get(g.gap.GetGitHttpsRepoUrl(*g.repoRef)) == "" {
		return nil, nil, fmt.Errorf("no repo path available")
	}

	hydratedBranchShas := make(map[string]string)
	dryBranchShas := make(map[string]string)

	for _, branch := range branches {
		logger.Info("Checking out branch", "branch", branch)
		_, _, _, err := g.runCmd(ctx, g.pathLookup.Get(g.gap.GetGitHttpsRepoUrl(*g.repoRef)), "git", "checkout", "--progress", "-B", branch, fmt.Sprintf("origin/%s", branch))
		if err != nil {
			return nil, nil, err
		}

		_, stdout, stderr, err := g.runCmd(ctx, g.pathLookup.Get(g.gap.GetGitHttpsRepoUrl(*g.repoRef)), "git", "rev-parse", branch)
		if err != nil {
			logger.Error(err, "could not get brach shas", "gitError", stderr)
			return nil, nil, err
		}
		hydratedBranchShas[branch] = strings.TrimSpace(stdout)
		dryBranchShas[branch] = "todo:look-into-file"
	}

	return dryBranchShas, hydratedBranchShas, nil
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
