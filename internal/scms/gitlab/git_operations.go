package gitlab

import (
	"context"
	"fmt"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	gitlab "gitlab.com/gitlab-org/api/client-go"
	v1 "k8s.io/api/core/v1"
)

type GitAuthenticationProvider struct {
	scmProvider *v1alpha1.ScmProvider
	secret      *v1.Secret
	client      *gitlab.Client
}

func NewGitlabGitAuthenticationProvider(scmProvider *v1alpha1.ScmProvider, secret *v1.Secret) (*GitAuthenticationProvider, error) {
	client, err := GetClient(*secret, scmProvider.Spec.GitLab.Domain)
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
	if gl.scmProvider.Spec.GitLab != nil && gl.scmProvider.Spec.GitLab.Domain != "" {
		return fmt.Sprintf("https://%s/%s/%s.git", gl.scmProvider.Spec.GitLab.Domain, repo.Spec.GitLab.Namespace, repo.Spec.GitLab.Name)
	}
	return fmt.Sprintf("https://gitlab.com/%s/%s.git", repo.Spec.GitLab.Namespace, repo.Spec.GitLab.Name)
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
		return nil, fmt.Errorf("GitLab token is required")
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
