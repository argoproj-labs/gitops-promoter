package gitea

import (
	"context"
	"fmt"
	"net/url"

	"code.gitea.io/sdk/gitea"
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	k8sV1 "k8s.io/api/core/v1"
)

// GitAuthenticationProvider implements the scms.GitOperationsProvider interface for Gitea.
type GitAuthenticationProvider struct {
	scmProvider promoterv1alpha1.GenericScmProvider
	secret      *k8sV1.Secret
	client      *gitea.Client
}

var _ scms.GitOperationsProvider = &GitAuthenticationProvider{}

// NewGiteaGitAuthenticationProvider creates a new instance of GitAuthenticationProvider for Gitea.
func NewGiteaGitAuthenticationProvider(scmProvider promoterv1alpha1.GenericScmProvider, secret *k8sV1.Secret) (*GitAuthenticationProvider, error) {
	client, err := GetClient(scmProvider.GetSpec().Gitea.Domain, *secret)
	if err != nil {
		return nil, fmt.Errorf("failed to create Gitea client: %w", err)
	}

	return &GitAuthenticationProvider{
		scmProvider: scmProvider,
		secret:      secret,
		client:      client,
	}, nil
}

// GetGitHttpsRepoUrl constructs the HTTPS URL for a Gitea repository.
func (gap GitAuthenticationProvider) GetGitHttpsRepoUrl(repo promoterv1alpha1.GitRepository) string {
	repoUrl := fmt.Sprintf(
		"https://%s/%s/%s.git",
		gap.scmProvider.GetSpec().Gitea.Domain,
		repo.Spec.Gitea.Owner,
		repo.Spec.Gitea.Name,
	)
	if _, err := url.Parse(repoUrl); err != nil {
		return ""
	}
	return repoUrl
}

// GetToken returns the password used for git HTTPS authentication.
// If the secret has a "token" key it is returned; otherwise the basic auth password is returned.
// Returns an error when neither credential is present.
func (gap GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	if token := string(gap.secret.Data["token"]); token != "" {
		return token, nil
	}
	password := string(gap.secret.Data["password"])
	if password == "" {
		return "", fmt.Errorf("secret %q must contain either 'token' or both 'username' and 'password'", gap.secret.Name)
	}
	return password, nil
}

// GetUser returns the user name used for git HTTPS authentication.
// Gitea accepts any user name alongside a token, so "oauth2" is returned when the secret has a token;
// otherwise the basic auth username is returned. Returns an error when neither credential is present.
func (gap GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	if string(gap.secret.Data["token"]) != "" {
		return "oauth2", nil
	}
	username := string(gap.secret.Data["username"])
	if username == "" {
		return "", fmt.Errorf("secret %q must contain either 'token' or both 'username' and 'password'", gap.secret.Name)
	}
	return username, nil
}

// GetClient creates a new Gitea client using the provided domain and secret.
func GetClient(domain string, secret k8sV1.Secret) (*gitea.Client, error) {
	options := make([]gitea.ClientOption, 0)

	token := string(secret.Data["token"])
	if token != "" {
		options = append(options, gitea.SetToken(token))
	}

	basicAuthUser := string(secret.Data["username"])
	basicAuthPassword := string(secret.Data["password"])
	if basicAuthUser != "" && basicAuthPassword != "" {
		options = append(options, gitea.SetBasicAuth(basicAuthUser, basicAuthPassword))
	}

	if len(options) == 0 {
		return nil, fmt.Errorf("missing token or user/password keys in the secret %q", secret.Name)
	}
	if len(options) > 1 {
		return nil, fmt.Errorf("too many authentication methods in the secret %q, choose one between token or user/password", secret.Name)
	}

	client, err := gitea.NewClient(
		"https://"+domain,
		options...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Gitea client: %w", err)
	}

	return client, nil
}
