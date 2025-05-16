package gitlab

import (
	"context"
	"fmt"
	"net/url"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	v1 "k8s.io/api/core/v1"
)

type GitAuthenticationProvider struct {
	scmProvider v1alpha1.GenericScmProvider
	secret      *v1.Secret
	client      *gitlab.Client
}

func NewGitlabGitAuthenticationProvider(scmProvider v1alpha1.GenericScmProvider, secret *v1.Secret) (*GitAuthenticationProvider, error) {
	client, err := GetClient(*secret, scmProvider.GetSpec().GitLab.Domain)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitLab Client: %w", err)
	}

	return &GitAuthenticationProvider{
		scmProvider: scmProvider,
		secret:      secret,
		client:      client,
	}, nil
}

func (gl GitAuthenticationProvider) GetGitHttpsRepoUrl(repo v1alpha1.GitRepository) string {
	var repoUrl string
	if gl.scmProvider.GetSpec().GitLab != nil && gl.scmProvider.GetSpec().GitLab.Domain != "" {
		repoUrl = fmt.Sprintf("https://%s/%s/%s.git", gl.scmProvider.GetSpec().GitLab.Domain, repo.Spec.GitLab.Namespace, repo.Spec.GitLab.Name)
	} else {
		repoUrl = fmt.Sprintf("https://gitlab.com/%s/%s.git", repo.Spec.GitLab.Namespace, repo.Spec.GitLab.Name)
	}
	if _, err := url.Parse(repoUrl); err != nil {
		return ""
	}
	return repoUrl
}

func (gl GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	return string(gl.secret.Data["token"]), nil
}

func (gl GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	return "oauth2", nil
}

func GetClient(secret v1.Secret, domain string) (*gitlab.Client, error) {
	token := string(secret.Data["token"])
	if token == "" {
		return nil, fmt.Errorf("secret %q is missing required data key 'token'", secret.Name)
	}

	opts := []gitlab.ClientOptionFunc{}
	if domain != "" {
		opts = append(opts, gitlab.WithBaseURL(fmt.Sprintf("https://%s/api/v4", domain)))
	}

	client, err := gitlab.NewClient(token, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create GitLab client: %w", err)
	}

	return client, nil
}
