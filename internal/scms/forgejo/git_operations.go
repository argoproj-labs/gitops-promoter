package forgejo

import (
	"context"
	"fmt"
	"net/url"

	forgejo "codeberg.org/mvdkleijn/forgejo-sdk/forgejo/v2"
	promoterv1alpha1 "github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
	k8sV1 "k8s.io/api/core/v1"
)

// GitAuthenticationProvider implements the scms.GitOperationsProvider interface for Forgejo.
type GitAuthenticationProvider struct {
	scmProvider promoterv1alpha1.GenericScmProvider
	secret      *k8sV1.Secret
	client      *forgejo.Client
}

var _ scms.GitOperationsProvider = &GitAuthenticationProvider{}

// NewForgejoGitAuthenticationProvider creates a new instance of GitAuthenticationProvider for Forgejo.
func NewForgejoGitAuthenticationProvider(scmProvider promoterv1alpha1.GenericScmProvider, secret *k8sV1.Secret) *GitAuthenticationProvider {
	client, err := GetClient(scmProvider.GetSpec().Forgejo.Domain, *secret)
	if err != nil {
		panic(err)
	}

	return &GitAuthenticationProvider{
		scmProvider: scmProvider,
		secret:      secret,
		client:      client,
	}
}

// GetGitHttpsRepoUrl constructs the HTTPS URL for a Forgejo repository.
func (gap GitAuthenticationProvider) GetGitHttpsRepoUrl(repo promoterv1alpha1.GitRepository) string {
	repoUrl := fmt.Sprintf(
		"https://%s/%s/%s.git",
		gap.scmProvider.GetSpec().Forgejo.Domain,
		repo.Spec.Forgejo.Owner,
		repo.Spec.Forgejo.Name,
	)
	if _, err := url.Parse(repoUrl); err != nil {
		return ""
	}
	return repoUrl
}

// GetToken returns the authentication token from the secret.
func (gap GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	return string(gap.secret.Data["token"]), nil
}

// GetUser returns a fixed user name for Forgejo authentication.
func (gap GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	return "oauth2", nil
}

// GetClient creates a new Forgejo client using the provided domain and secret.
func GetClient(domain string, secret k8sV1.Secret) (*forgejo.Client, error) {
	options := make([]forgejo.ClientOption, 0)

	token := string(secret.Data["token"])
	if token != "" {
		options = append(options, forgejo.SetToken(token))
	}

	basicAuthUser := string(secret.Data["username"])
	basicAuthPassword := string(secret.Data["password"])
	if basicAuthUser != "" && basicAuthPassword != "" {
		options = append(options, forgejo.SetBasicAuth(basicAuthUser, basicAuthPassword))
	}

	if len(options) == 0 {
		return nil, fmt.Errorf("missing token or user/password keys in the secret %q", secret.Name)
	}
	if len(options) > 1 {
		return nil, fmt.Errorf("too many authentication methods in the secret %q, choose one between token or user/password", secret.Name)
	}

	client, err := forgejo.NewClient(
		"https://"+domain,
		options...,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Forgejo client: %w", err)
	}

	return client, nil
}
