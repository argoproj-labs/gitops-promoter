package scms

import (
	"github.com/argoproj/promoter/api/v1alpha1"
	"github.com/argoproj/promoter/internal/scms/github"
)

func NewScmProvider(providerType ScmProviderType) PullRequestProvider {
	switch providerType {
	case "github":
		return github.NewGithubProvider()
	default:
		return nil
	}
}

type ScmProviderType string

const (
	GitHub ScmProviderType = "github"
	GitLab ScmProviderType = "gitlab"
)

type PullRequestProvider interface {
	Create(title, head, base, description string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error)
	Close(pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error)
	Update(title, description string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error)
	Merge(commitMessage string, pullRequest *v1alpha1.PullRequest) (*v1alpha1.PullRequest, error)
}
