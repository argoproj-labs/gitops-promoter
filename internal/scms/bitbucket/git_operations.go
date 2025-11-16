package bitbucket

import (
	"context"
	"fmt"
	"net/url"

	"github.com/ktrysmt/go-bitbucket"
	v1 "k8s.io/api/core/v1"

	"github.com/argoproj-labs/gitops-promoter/api/v1alpha1"
	"github.com/argoproj-labs/gitops-promoter/internal/scms"
)

// GitAuthenticationProvider implements the scms.GitOperationsProvider interface for Bitbucket.
type GitAuthenticationProvider struct {
	scmProvider v1alpha1.GenericScmProvider
	secret      *v1.Secret
	client      *bitbucket.Client
}

var _ scms.GitOperationsProvider = &GitAuthenticationProvider{}

// NewBitbucketGitAuthenticationProvider creates a new instance of GitAuthenticationProvider for Bitbucket.
func NewBitbucketGitAuthenticationProvider(scmProvider v1alpha1.GenericScmProvider, secret *v1.Secret) (*GitAuthenticationProvider, error) {
	client, err := GetClient(*secret)
	if err != nil {
		return nil, fmt.Errorf("failed to create Bitbucket Client: %w", err)
	}

	return &GitAuthenticationProvider{
		scmProvider: scmProvider,
		secret:      secret,
		client:      client,
	}, nil
}

// GetGitHttpsRepoUrl constructs the HTTPS URL for a Bitbucket repository based on the provided GitRepository object.
func (_ GitAuthenticationProvider) GetGitHttpsRepoUrl(repo v1alpha1.GitRepository) string {
	var repoUrl string
	repoUrl = fmt.Sprintf("https://bitbucket.com/%s/%s.git", repo.Spec.Bitbucket.Workspace, repo.Spec.Bitbucket.Repository)
	if _, err := url.Parse(repoUrl); err != nil {
		return ""
	}
	return repoUrl
}

// GetToken retrieves the Bitbucket access token from the secret.
func (bb GitAuthenticationProvider) GetToken(ctx context.Context) (string, error) {
	return string(bb.secret.Data["token"]), nil
}

// GetUser returns a placeholder user for Bitbucket authentication.
func (_ GitAuthenticationProvider) GetUser(ctx context.Context) (string, error) {
	return "x-token-auth", nil
}

// GetClient creates a new Bitbucket client using the provided secret and domain.
func GetClient(secret v1.Secret) (*bitbucket.Client, error) {
	token := string(secret.Data["token"])
	if token == "" {
		return nil, fmt.Errorf("secret %q is missing required data key 'token'", secret.Name)
	}

	client := bitbucket.NewOAuthbearerToken(token)

	return client, nil
}
