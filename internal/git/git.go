package git

import (
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
		logger.Info("Creating Directory", "directory", path)

		cmd, err := g.runCmd(ctx, "git", "clone", "--filter=blob:none", g.gap.GetGitHttpsRepoUrl(*g.repoRef), path)
		if err != nil {
			return err
		}

		logger.Info("Cloning Repo", "repo", g.gap.GetGitHttpsRepoUrl(*g.repoRef))
		_, err = cmd.Output()
		if err != nil {
			return err
		}
		g.pathLookup.Set(g.gap.GetGitHttpsRepoUrl(*g.repoRef), path)

	} else {
		logger.Info("Repo Exists, Fetching Instead")
		//TODO: fetch
		cmd, err := g.runCmd(ctx, "git", "fetch", "origin", "-v")
		if err != nil {
			return nil
		}
		stdout, err := cmd.Output()
		if err != nil {
			return err
		}
		logger.Info("Fetched Repo", "repo", g.gap.GetGitHttpsRepoUrl(*g.repoRef), "stdout", string(stdout))
	}

	return nil
}

func (g *GitOperations) runCmd(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	user, err := g.gap.GetUser(ctx)
	if err != nil {
		return nil, err
	}

	token, err := g.gap.GetToken(ctx)
	if err != nil {
		return nil, err
	}

	cmd := exec.Command(name, args...)
	cmd.Env = []string{
		"GIT_ASKPASS=promoter_askpass.sh", // Needs to be on path
		fmt.Sprintf("GIT_USER=%s", user),
		fmt.Sprintf("GIT_PASSWORD=%s", token),
		fmt.Sprintf("PATH=%s", os.Getenv("PATH")),
	}

	return cmd, nil
}
